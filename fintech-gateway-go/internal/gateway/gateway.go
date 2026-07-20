// Package gateway composes every other package in this module into one
// request pipeline: WAF -> rate limit -> (auth if the route requires
// it) -> cache-or-proxy, where proxying is protected per-backend by a
// circuit breaker and distributed by the route's load-balancing pool.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"fintechgateway/internal/auth"
	"fintechgateway/internal/breaker"
	"fintechgateway/internal/cache"
	"fintechgateway/internal/loadbalancer"
	"fintechgateway/internal/ratelimit"
	"fintechgateway/internal/waf"
)

// Route maps a path prefix to a backend pool and its policy.
type Route struct {
	PathPrefix  string
	Pool        *loadbalancer.Pool
	Cacheable   bool // cache GET responses under 500
	RequireAuth bool
}

type Config struct {
	Routes        []Route
	RateLimiter   *ratelimit.Limiter // nil disables rate limiting
	Verifier      *auth.Verifier     // required if any Route.RequireAuth is true
	Cache         *cache.Cache       // required if any Route.Cacheable is true
	WAFConfig     waf.Config
	BreakerConfig breaker.Config
	// ClientKey extracts the identity used for rate limiting and
	// consistent-hash routing. Defaults to the request's remote IP.
	ClientKey func(*http.Request) string
}

type compiledRoute struct {
	Route
	breakers map[string]*breaker.Breaker
	handler  http.Handler
}

type Gateway struct {
	routes        []compiledRoute
	limiter       *ratelimit.Limiter
	wafCfg        waf.Config
	clientKeyFunc func(*http.Request) string
	reverseProxy  *httputil.ReverseProxy
}

type backendContextKey struct{}

func New(cfg Config) (*Gateway, error) {
	keyFunc := cfg.ClientKey
	if keyFunc == nil {
		keyFunc = defaultClientKey
	}

	transport := &http.Transport{
		MaxIdleConns:          1000,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	reverseProxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			b, _ := req.Context().Value(backendContextKey{}).(*loadbalancer.Backend)
			if b == nil {
				return
			}
			req.URL.Scheme = "http"
			req.URL.Host = b.Address
		},
		Transport: transport,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			w.WriteHeader(http.StatusBadGateway)
		},
	}

	g := &Gateway{wafCfg: cfg.WAFConfig, clientKeyFunc: keyFunc, reverseProxy: reverseProxy, limiter: cfg.RateLimiter}

	for _, route := range cfg.Routes {
		if route.Pool == nil {
			return nil, fmt.Errorf("gateway: route %q has no Pool", route.PathPrefix)
		}
		if route.RequireAuth && cfg.Verifier == nil {
			return nil, fmt.Errorf("gateway: route %q requires auth but Config.Verifier is nil", route.PathPrefix)
		}
		if route.Cacheable && cfg.Cache == nil {
			return nil, fmt.Errorf("gateway: route %q is cacheable but Config.Cache is nil", route.PathPrefix)
		}

		cr := compiledRoute{Route: route, breakers: make(map[string]*breaker.Breaker)}
		for _, b := range route.Pool.Backends() {
			cr.breakers[b.ID] = breaker.New(cfg.BreakerConfig)
		}

		var h http.Handler = http.HandlerFunc(g.makeRouteHandler(cr, cfg.Cache))
		if route.RequireAuth {
			h = auth.Middleware(cfg.Verifier, h)
		}
		cr.handler = h
		g.routes = append(g.routes, cr)
	}

	return g, nil
}

// Handler returns the composed http.Handler: WAF wraps everything else.
func (g *Gateway) Handler() http.Handler {
	return waf.Middleware(g.wafCfg, http.HandlerFunc(g.serveHTTP))
}

func (g *Gateway) serveHTTP(w http.ResponseWriter, r *http.Request) {
	route, ok := g.matchRoute(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}

	if g.limiter != nil && !g.limiter.Allow(g.clientKeyFunc(r)) {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}

	route.handler.ServeHTTP(w, r)
}

func (g *Gateway) matchRoute(path string) (compiledRoute, bool) {
	var best compiledRoute
	bestLen := -1
	found := false
	for _, r := range g.routes {
		if strings.HasPrefix(path, r.PathPrefix) && len(r.PathPrefix) > bestLen {
			best, bestLen, found = r, len(r.PathPrefix), true
		}
	}
	return best, found
}

func defaultClientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (g *Gateway) makeRouteHandler(route compiledRoute, c *cache.Cache) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := g.clientKeyFunc(r)
		if r.Method == http.MethodGet && route.Cacheable {
			g.serveCacheable(w, r, route, key, c)
			return
		}
		g.proxyDirect(w, r, route, key)
	}
}

type cachedResponse struct {
	status int
	header http.Header
	body   []byte
}

func (g *Gateway) serveCacheable(w http.ResponseWriter, r *http.Request, route compiledRoute, key string, c *cache.Cache) {
	val, err := c.GetOrLoad(cacheKey(r), func() (any, error) {
		rec, ferr := g.fetch(r, route, key)
		if ferr != nil {
			return nil, ferr
		}
		defer rec.release()
		if rec.status >= 500 {
			return nil, fmt.Errorf("gateway: upstream returned %d", rec.status)
		}
		return cachedResponse{status: rec.status, header: rec.header.Clone(), body: append([]byte(nil), rec.body.Bytes()...)}, nil
	})
	if err != nil {
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	resp := val.(cachedResponse)
	writeResponse(w, resp.status, resp.header, resp.body)
}

func cacheKey(r *http.Request) string {
	return r.URL.String()
}

func (g *Gateway) proxyDirect(w http.ResponseWriter, r *http.Request, route compiledRoute, key string) {
	rec, err := g.fetch(r, route, key)
	if err != nil {
		http.Error(w, "bad gateway", http.StatusBadGateway)
		return
	}
	defer rec.release()
	writeResponse(w, rec.status, rec.header, rec.body.Bytes())
}

func writeResponse(w http.ResponseWriter, status int, header http.Header, body []byte) {
	dst := w.Header()
	for k, vs := range header {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

const maxProxyAttempts = 3

// fetch picks a backend, checks its breaker, and proxies the request,
// retrying against a different pick up to maxProxyAttempts times on a
// 5xx or a tripped breaker. On success (or on exhausting all attempts
// with the last one still failing) it returns the buffered recorder,
// whose body buffer the caller must release. On total failure (every
// attempt blocked by an open breaker, or no healthy backend at all) it
// returns a nil recorder and an error — callers must not call release
// on a nil recorder.
func (g *Gateway) fetch(r *http.Request, route compiledRoute, key string) (*responseRecorder, error) {
	var lastErr error
	var rec *responseRecorder

	for attempt := 0; attempt < maxProxyAttempts; attempt++ {
		backend, err := route.Pool.Pick(key)
		if err != nil {
			lastErr = err
			break // no healthy backend at all; retrying won't help
		}

		br := route.breakers[backend.ID]
		if !br.Allow() {
			route.Pool.Release(backend)
			lastErr = breaker.ErrOpen
			continue
		}

		attemptRec := newResponseRecorder()
		ctx := context.WithValue(r.Context(), backendContextKey{}, backend)
		g.reverseProxy.ServeHTTP(attemptRec, r.WithContext(ctx))
		route.Pool.Release(backend)

		if attemptRec.status < 500 {
			br.Success()
			return attemptRec, nil
		}

		br.Failure()
		lastErr = fmt.Errorf("gateway: backend %s returned %d", backend.ID, attemptRec.status)
		if attempt < maxProxyAttempts-1 {
			attemptRec.release()
			continue
		}
		rec = attemptRec // last attempt: keep it, it's the best we have
	}

	if rec != nil {
		return rec, nil
	}
	if lastErr == nil {
		lastErr = errors.New("gateway: no backend attempt succeeded")
	}
	return nil, lastErr
}
