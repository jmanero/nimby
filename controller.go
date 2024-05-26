package nimby

import (
	"context"
	"net/http"
	"sync"

	"github.com/hashicorp/nomad/api"
	"github.com/jmanero/nimby/logging"
	"go.uber.org/zap"
)

// Handler implements a dynamic HTTP request router
type Handler interface {
	http.Handler
	Add(*api.ServiceRegistration) Handler
	Del(*api.ServiceRegistration) Handler
	Empty() bool
}

// Controller uses a Nomad event stream to update ingress mappings
type Controller struct {
	sync.Map

	notEmpty
	mu sync.Mutex
}

// New initializes a Controller
func New() *Controller {
	return &Controller{}
}

func (controller *Controller) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if balancer, has := controller.Get(r.Host); has {
		balancer.ServeHTTP(w, r)
		return
	}

	http.NotFound(w, r)
}

// Get attempts to retrieve an existing Balancer instance from the controller's service set
func (controller *Controller) Get(domain string) (balancer Handler, has bool) {
	maybe, has := controller.Load(domain)
	if !has {
		return
	}

	balancer, has = maybe.(Handler)
	return
}

// Add inserts a new service instance to the controller, creating a Balancer
// instance for the service if one does not already exist
func (controller *Controller) Add(service *api.ServiceRegistration) Handler {
	controller.mu.Lock()
	defer controller.mu.Unlock()

	domain, has := DomainTag(service.Tags)
	if !has {
		// WARN: Service does not have a nimby-domain tag
		return controller
	}

	balancer, has := controller.Get(domain)
	if !has {
		// Create a balancer for a new service domain
		balancer = NewBalancer(service.Tags)
	}

	controller.Store(domain, balancer.Add(service))
	return controller
}

// Del removes a service instance from the controller, removing an empty Balancer
func (controller *Controller) Del(service *api.ServiceRegistration) Handler {
	controller.mu.Lock()
	defer controller.mu.Unlock()

	domain, has := DomainTag(service.Tags)
	if !has {
		// WARN: Service does not have a nimby-domain tag
		return controller
	}

	balancer, has := controller.Get(domain)
	if !has {
		return controller
	}

	balancer = balancer.Del(service)

	if balancer.Empty() {
		// Remove the domain for an empty Balancer from the controller
		controller.Delete(domain)
	} else {
		controller.Store(domain, balancer)
	}

	return controller
}

// Run consumes Service events from the Nomad API to update the controller's backend mappings
func (controller *Controller) Run(ctx context.Context, services []string) (err error) {
	logger := logging.Logger(ctx)

	client, err := api.NewClient(api.DefaultConfig())
	if err != nil {
		return
	}

	// Get the current snapshot of services, and the
	// client.Services().List(&api.QueryOptions{})

	events, err := client.EventStream().Stream(ctx, map[api.Topic][]string{
		api.TopicService: services,
	}, 0, &api.QueryOptions{})

	// Stream() will close the events channel when the calling context is canceled
	for evs := range events {
		for _, ev := range evs.Events {
			logger.Info("controller.event", zap.String("event.type", ev.Type), zap.Uint64("event.index", ev.Index))

			sv, err := ev.Service()
			if err != nil {
				logger.Warn("controller.error", zap.Error(err))
				continue
			}

			switch ev.Type {
			case "ServiceRegistration":
				controller.Add(sv)
			case "ServiceDeregistration":
				controller.Del(sv)
			}
		}
	}

	return
}
