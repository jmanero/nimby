package nimby

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/nomad/api"
)

// Tag Prefixes
const (
	DomainTagPrefix = "nimby-domain:"
	WeightTagPrefix = "nimby-weight:"
	ProtoTagPrefix  = "nimby-proto:"
	PathTagPrefix   = "nimby-path:"
)

// DomainTag attempts to extract a DOMAIN value from a tag string `nimby-domain:STRING`
func DomainTag(tags []string) (value string, has bool) {
	for _, tag := range tags {
		value, has = strings.CutPrefix(tag, DomainTagPrefix)
		if has {
			return
		}
	}

	return
}

// WeightTag attempts to extract a uint64 value from a tag string `nimby-weight:UINT64`
func WeightTag(tags []string) (value uint64, _ bool) {
	for _, tag := range tags {
		val, has := strings.CutPrefix(tag, WeightTagPrefix)

		if has {
			value, _ = strconv.ParseUint(val, 10, 8)
			if value > 0 {
				return value, true
			}
		}
	}

	return 1, false
}

// ProtoTag attempts to extract a URL scheme value from a tag string `nimby-proto:STRING`
func ProtoTag(tags []string) (value string, has bool) {
	for _, tag := range tags {
		value, has = strings.CutPrefix(tag, ProtoTagPrefix)
		if has {
			return
		}
	}

	return
}

// PathTag attempts to extract a URL path value from a tag string `nimby-path:STRING`
func PathTag(tags []string) (value string, has bool) {
	for _, tag := range tags {
		value, has = strings.CutPrefix(tag, PathTagPrefix)
		if has {
			return
		}
	}

	return
}

// UpstreamService builds a URL from a ServiceRegistration struct
func UpstreamService(service *api.ServiceRegistration) (uri url.URL) {
	uri.Scheme = "http"
	uri.Host = net.JoinHostPort(service.Address, strconv.FormatInt(int64(service.Port), 10))
	uri.Path = "/"

	if value, has := ProtoTag(service.Tags); has {
		uri.Scheme = value
	}

	if value, has := PathTag(service.Tags); has {
		uri.Path = value
	}

	return
}

// EnvString is a helper to lookup an environment variable value or return a default
func EnvString(name, value string) string {
	if env, has := os.LookupEnv(name); has {
		return env
	}

	return value
}

// EnvStrings is a helper to lookup an environment variable list-value or return a default
func EnvStrings(name, sep string, values []string) []string {
	if env, has := os.LookupEnv(name); has {
		return strings.Split(env, sep)
	}

	return values
}

// Shutdown is a helper to shutdown an HTTP server with a timeout
func Shutdown(server *http.Server, timeout time.Duration) error {
	ctx, done := context.WithTimeout(context.Background(), timeout)

	defer done()
	return server.Shutdown(ctx)
}
