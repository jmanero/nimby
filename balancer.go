package nimby

import (
	"crypto/rand"
	"io"
	"net/http"
	"net/url"

	"github.com/hashicorp/nomad/api"
)

// WeightedBackend provides an HTTP upstream for weighted balancer implementations
type WeightedBackend struct {
	ID      string
	JobID   string
	AllocID string

	Weight   uint64
	Upstream url.URL
}

func (backend WeightedBackend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var request http.Request

	request = *r
	request.URL = &backend.Upstream
	// TODO: Forwarding headers

	res, err := http.DefaultClient.Do(&request)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	headers := w.Header()
	for name, values := range res.Header {
		headers[name] = values
	}

	w.WriteHeader(res.StatusCode)
	io.Copy(w, res.Body)
	res.Body.Close()

	headers = w.Header()
	for name, values := range res.Trailer {
		headers[name] = values
	}
}

// WeightedRandom implements a simple load-balancer Handler
type WeightedRandom struct {
	Backends map[string]*WeightedBackend
	Total    uint64

	weighted []*WeightedBackend
	random   io.Reader

	notEmpty
}

// NewBalancer creates a new weighted-random load balancer Handler
func NewBalancer(_ []string) Handler {
	// TODO: switch balancer strategies from a tag.
	return WeightedRandom{random: rand.Reader}
}

func (balancer WeightedRandom) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	random, err := balancer.Uint64n()
	if err != nil {
		// TODO: Log error
		http.Error(w, "Unhandled Error", http.StatusInternalServerError)
		return
	}

	balancer.weighted[random].ServeHTTP(w, r)
}

// Uint64n generates a random uint64 in the half-range of [0:Total]
func (balancer WeightedRandom) Uint64n() (val uint64, err error) {
	var buf [8]byte

	_, err = balancer.random.Read(buf[:])
	if err != nil {
		return
	}

	val += uint64(buf[7])
	val += uint64(buf[6]) << 8
	val += uint64(buf[5]) << 16
	val += uint64(buf[4]) << 24
	val += uint64(buf[3]) << 32
	val += uint64(buf[2]) << 40
	val += uint64(buf[1]) << 48
	val += uint64(buf[0]) << 56

	return val % balancer.Total, nil
}

// Add inserts a backend and rehashes the balancer's internal weighting
func (balancer WeightedRandom) Add(service *api.ServiceRegistration) Handler {
	weight, _ := WeightTag(service.Tags)
	balancer.Total += uint64(weight)

	// Rebuild the balancer's map and distribution slice
	backends := make(map[string]*WeightedBackend, len(balancer.Backends)+1)
	weighted := make([]*WeightedBackend, 0, balancer.Total)

	// Add the new backend to the balancer's map
	backends[service.ID] = &WeightedBackend{
		ID:      service.ID,
		JobID:   service.JobID,
		AllocID: service.AllocID,

		Weight:   weight,
		Upstream: UpstreamService(service),
	}

	for id, backend := range balancer.Backends {
		backends[id] = backend
		// Rehash the backend's relative weight
		for i := uint64(0); i < backend.Weight; i++ {
			weighted = append(weighted, backend)
		}
	}

	balancer.Backends = backends
	balancer.weighted = weighted

	return balancer
}

// Del removes a backend and rehashes the balancer's internal weighting
func (balancer WeightedRandom) Del(service *api.ServiceRegistration) Handler {
	weight, _ := WeightTag(service.Tags)
	balancer.Total -= weight

	// Rebuild the balancer's map and distribution slice
	backends := make(map[string]*WeightedBackend, len(balancer.Backends)-1)
	weighted := make([]*WeightedBackend, 0, balancer.Total)

	for id, backend := range balancer.Backends {
		if id == service.ID {
			// Remove the requested backend
			continue
		}

		backends[id] = backend
		// Rehash the backend's relative weight
		for i := uint64(0); i < backend.Weight; i++ {
			weighted = append(weighted, backend)
		}
	}

	balancer.Backends = backends
	balancer.weighted = weighted

	return balancer
}

type empty struct{}

func (empty) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	http.NotFound(w, r)
}

func (empty) Add(service *api.ServiceRegistration) Handler {
	return NewBalancer(service.Tags).Add(service)
}

func (empty) Del(*api.ServiceRegistration) {}

func (empty) Empty() bool {
	return true
}

type notEmpty struct{}

func (notEmpty) Empty() bool {
	return false
}
