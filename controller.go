package nimby

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"strings"
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

// Options for the controller's event watcher
type Options struct {
	WatchServices []string

	TokenPath   string
	TokenReload []os.Signal
}

// Controller uses a Nomad event stream to update ingress mappings
type Controller struct {
	Options
	sync.Map

	*api.Client
	notEmpty

	mu sync.Mutex
}

// New initializes a Controller
func New(opts Options) (*Controller, error) {
	client, err := api.NewClient(api.DefaultConfig())
	if err != nil {
		return nil, err
	}

	controller := &Controller{Options: opts, Client: client}
	return controller, controller.LoadToken()
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
	domain, has := DomainTag(service.Tags)
	if !has {
		// WARN: Service does not have a nimby-domain tag
		return controller
	}

	controller.mu.Lock()
	defer controller.mu.Unlock()

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
	domain, has := DomainTag(service.Tags)
	if !has {
		// WARN: Service does not have a nimby-domain tag
		return controller
	}

	controller.mu.Lock()
	defer controller.mu.Unlock()

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

// LoadToken reads and sets the controller's Nomad API token from TokenPath
func (controller *Controller) LoadToken() error {
	token, err := os.ReadFile(controller.TokenPath)
	if err != nil {
		return err
	}

	controller.SetSecretID(strings.TrimSpace(string(token)))
	return nil
}

// TokenReloader watches for OS signals to re-read the nomad auth-token from a file
func (controller *Controller) TokenReloader(ctx context.Context) (err error) {
	reload := make(chan os.Signal, 1)

	signal.Notify(reload, controller.TokenReload...)
	defer signal.Stop(reload)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-reload:
			err = controller.LoadToken()
			if err != nil {
				return
			}
		}
	}
}

// Updater consumes Service events from the Nomad API to update the controller's backend mappings
func (controller *Controller) Updater(ctx context.Context) (err error) {
	logger := logging.Logger(ctx)
	logger.Info("controller.start")

	// Get the current snapshot of services, and the last modify-index for the events stream
	services := controller.WatchServices
	if len(services) == 1 && services[0] == "*" {
		list, _, err := controller.Services().List(nil)
		if err != nil {
			return err
		}

		for _, svc := range list[0].Services {
			services = append(services, svc.ServiceName)
		}
	}

	var index uint64
	for _, service := range services {
		logger.Info("controller.init", zap.String("service", service))
		entries, meta, err := controller.Services().Get(service, nil)
		if err != nil {
			return err
		}

		if meta.LastIndex > index {
			index = meta.LastIndex
		}

		for _, svc := range entries {
			controller.Add(svc)
		}
	}

	logger.Info("controller.run")
	events, err := controller.EventStream().Stream(ctx,
		map[api.Topic][]string{api.TopicService: controller.WatchServices}, index, nil)

	if err != nil {
		return
	}

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

	logger.Info("controller.stop", zap.Error(err))
	return
}
