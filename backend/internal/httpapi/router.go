package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/getkin/kin-openapi/routers/legacy"

	"github.com/denismurphy/agritelem/backend/internal/auth"
	"github.com/denismurphy/agritelem/backend/internal/service"
)

// MaxBodyBytes bounds request bodies (a full batch of hourly resources).
const MaxBodyBytes = 5 << 20

// Config wires the HTTP API.
type Config struct {
	Service   *service.Service
	Verifier  *auth.Verifier
	Logger    *slog.Logger
	PublicURL *url.URL                        // absolute base for pagination links
	Ready     func(ctx context.Context) error // readiness probe, e.g. a database ping
}

// NewHandler returns the complete HTTP handler: health probes plus the
// authenticated, contract-validated API.
func NewHandler(cfg Config) (http.Handler, error) {
	if cfg.Service == nil || cfg.Verifier == nil || cfg.Logger == nil || cfg.PublicURL == nil || cfg.Ready == nil {
		return nil, errors.New("httpapi: incomplete configuration")
	}
	spec, err := GetSwagger()
	if err != nil {
		return nil, fmt.Errorf("load embedded OpenAPI document: %w", err)
	}
	spec.Servers = nil // match request paths without a server prefix
	router, err := legacy.NewRouter(spec)
	if err != nil {
		return nil, fmt.Errorf("build request router: %w", err)
	}

	h := &handlers{svc: cfg.Service, publicURL: cfg.PublicURL}
	strict := NewStrictHandlerWithOptions(h, nil, StrictHTTPServerOptions{
		RequestErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, http.StatusBadRequest, typeInvalidRequest, err.Error())
		},
		ResponseErrorHandlerFunc: handleError(cfg.Logger),
	})
	api := HandlerWithOptions(strict, StdHTTPServerOptions{
		ErrorHandlerFunc: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, http.StatusBadRequest, typeInvalidRequest, err.Error())
		},
	})

	protected := api
	protected = validateRequests(router)(protected)
	protected = limitBody(MaxBodyBytes)(protected)
	protected = authenticate(cfg.Verifier)(protected)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := cfg.Ready(r.Context()); err != nil {
			cfg.Logger.WarnContext(r.Context(), "not ready", "error", err)
			writeError(w, http.StatusServiceUnavailable, typeInternal, "dependencies are unavailable")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.Handle("/", protected)

	var root http.Handler = mux
	root = recoverPanics(cfg.Logger)(root)
	root = accessLog(cfg.Logger)(root)
	root = requestID(root)
	return root, nil
}
