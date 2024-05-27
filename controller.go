package nimby

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"sync"

	"github.com/hashicorp/nomad/api"
	"github.com/jmanero/nimby/logging"
	"go.uber.org/zap"
)

// Handler implements a dynamic HTTP request router
type Handler interface {
	http.Handler
	Add(context.Context, *api.ServiceRegistration) Handler
	Del(context.Context, *api.ServiceRegistration) Handler
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
func (controller *Controller) Add(ctx context.Context, service *api.ServiceRegistration) Handler {

	domain, has := DomainTag(service.Tags)
	if !has {
		return controller
	}

	ctx, logger := logging.Logger(ctx,
		zap.String("domain", domain),
		zap.String("service", service.ServiceName),
		zap.String("ns", service.Namespace))

	controller.mu.Lock()
	defer controller.mu.Unlock()

	balancer, has := controller.Get(domain)
	if !has {
		logger.Info("service.add")
		balancer = NewBalancer(service.Tags)
	}

	controller.Store(domain, balancer.Add(ctx, service))
	return controller
}

// Del removes a service instance from the controller, removing an empty Balancer
func (controller *Controller) Del(ctx context.Context, service *api.ServiceRegistration) Handler {

	domain, has := DomainTag(service.Tags)
	if !has {
		return controller
	}

	ctx, logger := logging.Logger(ctx,
		zap.String("domain", domain),
		zap.String("service", service.ServiceName),
		zap.String("ns", service.Namespace))

	controller.mu.Lock()
	defer controller.mu.Unlock()

	balancer, has := controller.Get(domain)
	if !has {
		return controller
	}

	balancer = balancer.Del(ctx, service)

	if balancer.Empty() {
		// Remove the domain for an empty Balancer from the controller
		logger.Info("service.remove")
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
	ctx, logger := logging.Logger(ctx, zap.String("routine", "token"))
	logger.Info("token.started")

	reload := make(chan os.Signal, 1)
	defer close(reload)

	signal.Notify(reload, controller.TokenReload...)
	defer signal.Stop(reload)

	for {
		select {
		case <-ctx.Done():
			logger.Info("token.stopped")
			return

		case sig := <-reload:
			logger.Info("token.reloading", zap.Stringer("signal", sig), zap.String("path", controller.TokenPath))

			err = controller.LoadToken()
			if err != nil {
				logger.Error("token.error", zap.Error(err))
			}
		}
	}
}

// Updater consumes Service events from the Nomad API to update the controller's backend mappings
func (controller *Controller) Updater(ctx context.Context) (err error) {
	ctx, logger := logging.Logger(ctx, zap.String("routine", "updater"))
	logger.Info("updater.starting", zap.Strings("services", controller.WatchServices))

	// If WatchServices is '*', collect a list of existing services
	services := controller.WatchServices
	if slices.Contains(services, "*") {
		services = services[:0]

		list, _, err := controller.Services().List(nil)
		if err != nil {
			return err
		}

		// TODO: Handle '*' namespace... Currently assume that NOMAD_NAMESPACE is not '*'
		for _, svc := range list[0].Services {
			services = append(services, svc.ServiceName)
		}
	}

	// List existing members of each service and collect the update index to start watching events
	var index uint64
	for _, service := range services {
		entries, meta, err := controller.Services().Get(service, nil)
		if err != nil {
			return err
		}

		logger.Info("updater.sync", zap.String("service", service), zap.Uint64("index", meta.LastIndex))
		if meta.LastIndex > index {
			index = meta.LastIndex + 1
		}

		for _, svc := range entries {
			controller.Add(ctx, svc)
		}
	}

	for {
		// Check for context cancellation before making new events request
		select {
		case <-ctx.Done():
			logger.Info("updater.stopped")
			return
		default:
		}

		logger.Info("updater.streaming", zap.Uint64("index", index))
		events, err := controller.EventStream().Stream(ctx,
			map[api.Topic][]string{api.TopicService: controller.WatchServices}, index, nil)

		if err != nil {
			logger.Error("updater.error", zap.Error(err))
			return err
		}

		// Stream() will close the events channel when the calling context is canceled
		for evs := range events {
			for _, ev := range evs.Events {
				logger.Info("updaterevent", zap.String("type", ev.Type), zap.Uint64("index", ev.Index))
				index = ev.Index

				sv, err := ev.Service()
				if err != nil {
					logger.Warn("updater.error", zap.Error(err))
					continue
				}

				if sv == nil {
					continue
				}

				switch ev.Type {
				case "ServiceRegistration":
					controller.Add(ctx, sv)
				case "ServiceDeregistration":
					controller.Del(ctx, sv)
				}
			}
		}
	}
}
