package nimby

import (
	"context"
	"crypto/rand"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/hashicorp/nomad/api"
	"github.com/jmanero/nimby/logging"
	"go.uber.org/zap"
)

// WeightedUpstream provides an HTTP upstream for weighted balancer implementations
type WeightedUpstream struct {
	ID      string
	JobID   string
	AllocID string

	Weight   uint64
	Endpoint url.URL
}

func (backend WeightedUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, logger := logging.Logger(r.Context())

	r.RequestURI = ""
	r.URL = backend.Endpoint.JoinPath(r.URL.RawPath)
	// TODO: Forwarding address headers

	start := time.Now()
	logger.Info("upstream.begin", zap.Time("start", start), zap.String("method", r.Method), zap.Stringer("endpoint", r.URL))

	res, err := http.DefaultClient.Do(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	logger.Info("upstream.response", zap.Time("start", start), zap.Duration("elapsed", time.Since(start)), zap.Int("code", res.StatusCode))

	headers := w.Header()
	for name, values := range res.Header {
		headers[name] = values
	}

	w.WriteHeader(res.StatusCode)
	io.Copy(w, res.Body)
	res.Body.Close()

	// Best effort to propagate trailers downstream... Not tested yet.
	for name, values := range res.Trailer {
		headers[http.TrailerPrefix+name] = values
	}

	logger.Info("upstream.end", zap.Time("start", start), zap.Duration("elapsed", time.Since(start)))
}

// WeightedRandom implements a simple load-balancer Handler
type WeightedRandom struct {
	Upstreams map[string]*WeightedUpstream
	Total     uint64

	weighted []*WeightedUpstream
	random   io.Reader

	notEmpty
}

// NewBalancer creates a new weighted-random load balancer Handler
func NewBalancer(_ []string) Handler {
	// TODO: switch balancer strategies from a tag.
	return WeightedRandom{random: rand.Reader}
}

func (balancer WeightedRandom) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	upstream, err := balancer.Next()
	if err != nil {
		logging.Warn(r.Context(), "balancer.error", zap.Error(err))
		http.Error(w, "Unhandled Error", http.StatusInternalServerError)
		return
	}

	ctx, logger := logging.Logger(r.Context(),
		zap.String("upstream", upstream.ID),
		zap.Uint64("weight", upstream.Weight),
		zap.Stringer("endpoint", &upstream.Endpoint))

	logger.Info("request.begin")
	upstream.ServeHTTP(w, r.Clone(ctx))
	logger.Info("request.end")
}

// Next selects an upstream to use for a request
func (balancer WeightedRandom) Next() (upstream *WeightedUpstream, err error) {
	var buf [8]byte

	_, err = balancer.random.Read(buf[:])
	if err != nil {
		return
	}

	val := uint64(buf[7])
	val += uint64(buf[6]) << 8
	val += uint64(buf[5]) << 16
	val += uint64(buf[4]) << 24
	val += uint64(buf[3]) << 32
	val += uint64(buf[2]) << 40
	val += uint64(buf[1]) << 48
	val += uint64(buf[0]) << 56

	val = val % balancer.Total
	return balancer.weighted[val], nil
}

// Rehash rebuilds the balancer's weighted lookup table
func (balancer *WeightedRandom) Rehash(ctx context.Context) {
	weighted := make([]*WeightedUpstream, 0, balancer.Total)

	// Rehash the backend services by their relative weights
	for _, backend := range balancer.Upstreams {
		for i := uint64(0); i < backend.Weight; i++ {
			weighted = append(weighted, backend)
		}
	}

	balancer.weighted = weighted
	logging.Info(ctx, "balancer.rehash", zap.Int("count", len(balancer.Upstreams)), zap.Uint64("weight", balancer.Total))
}

// Add inserts a backend and rehashes the balancer's internal weighting
func (balancer WeightedRandom) Add(ctx context.Context, service *api.ServiceRegistration) Handler {
	if _, has := balancer.Upstreams[service.ID]; has {
		// NOP if the service is already included in the balancer
		return balancer
	}

	weight, _ := WeightTag(service.Tags)

	_, logger := logging.Logger(ctx)
	logger.Info("upstream.add", zap.String("addr", service.Address), zap.Int("port", service.Port))

	// Rebuild the balancer's map and distribution slice
	backends := make(map[string]*WeightedUpstream, len(balancer.Upstreams)+1)

	// Add the new backend to the balancer's map
	backends[service.ID] = &WeightedUpstream{
		ID:      service.ID,
		JobID:   service.JobID,
		AllocID: service.AllocID,

		Weight:   weight,
		Endpoint: UpstreamService(service),
	}

	balancer.Total = weight

	for id, backend := range balancer.Upstreams {
		backends[id] = backend
		balancer.Total += backend.Weight
	}

	balancer.Upstreams = backends
	balancer.Rehash(ctx)

	return balancer
}

// Del removes a backend and rehashes the balancer's internal weighting
func (balancer WeightedRandom) Del(ctx context.Context, service *api.ServiceRegistration) Handler {
	if _, has := balancer.Upstreams[service.ID]; !has {
		// NOP if the service isn't in the balancer
		return balancer
	}

	weight, _ := WeightTag(service.Tags)

	_, logger := logging.Logger(ctx)
	logger.Info("upstream.del", zap.String("addr", service.Address), zap.Int("port", service.Port), zap.Uint64("weight", weight))

	// Rebuild the balancer's map and distribution slice
	backends := make(map[string]*WeightedUpstream, len(balancer.Upstreams)-1)
	balancer.Total = 0

	for id, backend := range balancer.Upstreams {
		if id == service.ID {
			// Remove the requested backend
			continue
		}

		backends[id] = backend
		balancer.Total += backend.Weight
	}

	if len(backends) == 0 {
		return empty{}
	}

	balancer.Upstreams = backends

	balancer.Rehash(ctx)
	return balancer
}

type empty struct{}

func (empty) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}

func (empty) Add(ctx context.Context, service *api.ServiceRegistration) Handler {
	return NewBalancer(service.Tags).Add(ctx, service)
}

func (e empty) Del(context.Context, *api.ServiceRegistration) Handler { return e }

func (empty) Empty() bool {
	return true
}

type notEmpty struct{}

func (notEmpty) Empty() bool {
	return false
}
