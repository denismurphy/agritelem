package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"

	"github.com/denismurphy/agritelem/backend/internal/auth"
)

type requestIDKey struct{}

// requestID propagates X-Request-ID, generating one when absent or unusable.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-ID")
		if len(id) == 0 || len(id) > 128 || strings.ContainsFunc(id, func(c rune) bool { return c < 0x21 || c > 0x7e }) {
			var b [16]byte
			_, _ = rand.Read(b[:])
			id = hex.EncodeToString(b[:])
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// accessLog logs one structured line per request. It never logs headers or
// bodies, which may carry tokens or farm data.
func accessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			id, _ := r.Context().Value(requestIDKey{}).(string)
			logger.LogAttrs(r.Context(), slog.LevelInfo, "request",
				slog.String("request_id", id),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int("bytes", rec.bytes),
				slog.Duration("duration", time.Since(start)),
			)
		})
	}
}

// recoverPanics turns a panic into a 500 and logs the stack.
func recoverPanics(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
						panic(v)
					}
					logger.ErrorContext(r.Context(), "panic serving request", "panic", fmt.Sprint(v), "stack", string(debug.Stack()))
					writeError(w, http.StatusInternalServerError, typeInternal, "the server could not complete the request")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// authenticate requires a valid bearer token and stores the principal.
func authenticate(v *auth.Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
			if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
				w.Header().Set("WWW-Authenticate", `Bearer realm="agritelem"`)
				writeError(w, http.StatusUnauthorized, typeUnauthenticated, "a bearer token is required")
				return
			}
			p, err := v.Verify(token)
			if err != nil {
				w.Header().Set("WWW-Authenticate", `Bearer realm="agritelem", error="invalid_token"`)
				writeError(w, http.StatusUnauthorized, typeUnauthenticated, "the bearer token is invalid or expired")
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
		})
	}
}

// limitBody caps request bodies; reads beyond the cap fail, which validation
// reports as 413.
func limitBody(maxBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ContentLength > maxBytes {
				writeError(w, http.StatusRequestEntityTooLarge, typeTooLarge, fmt.Sprintf("request bodies are limited to %d bytes", maxBytes))
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
			next.ServeHTTP(w, r)
		})
	}
}

// validateRequests checks every request against the OpenAPI contract before
// it reaches a handler. Authentication is enforced separately, so security
// requirements are not re-checked here.
func validateRequests(router routers.Router) func(http.Handler) http.Handler {
	opts := &openapi3filter.Options{AuthenticationFunc: openapi3filter.NoopAuthenticationFunc, MultiError: true}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			route, params, err := router.FindRoute(r)
			if err != nil {
				switch {
				case errors.Is(err, routers.ErrMethodNotAllowed):
					writeError(w, http.StatusMethodNotAllowed, typeInvalidRequest, "method not allowed")
				default:
					writeError(w, http.StatusNotFound, typeNotFound, "no such endpoint")
				}
				return
			}
			in := &openapi3filter.RequestValidationInput{Request: r, PathParams: params, Route: route, Options: opts}
			if err := openapi3filter.ValidateRequest(r.Context(), in); err != nil {
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					writeError(w, http.StatusRequestEntityTooLarge, typeTooLarge, fmt.Sprintf("request bodies are limited to %d bytes", tooLarge.Limit))
					return
				}
				writeErrors(w, http.StatusBadRequest, schemaMessages(err))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// maxSchemaMessages bounds the error list for grossly invalid requests.
const maxSchemaMessages = 20

// schemaMessages converts kin-openapi errors into concise messages that name
// the offending field, instead of kin's full schema dumps.
func schemaMessages(err error) []IcarResponseMessageResource {
	var out []IcarResponseMessageResource
	var visit func(error)
	visit = func(err error) {
		if len(out) >= maxSchemaMessages {
			return
		}
		var multi openapi3.MultiError
		if errors.As(err, &multi) {
			for _, e := range multi {
				visit(e)
			}
			return
		}
		var reqErr *openapi3filter.RequestError
		field := ""
		if errors.As(err, &reqErr) && reqErr.Parameter != nil {
			field = reqErr.Parameter.Name
		}
		var schemaErr *openapi3.SchemaError
		if errors.As(err, &schemaErr) {
			// The outermost SchemaError already carries the full pointer
			// (checked for errors nested inside ICAR allOf chains).
			if ptr := schemaErr.JSONPointer(); len(ptr) > 0 {
				field = "/" + strings.Join(ptr, "/")
			}
			out = append(out, message(http.StatusBadRequest, typeInvalidRequest, "Request does not match the API contract", schemaErr.Reason, field))
			return
		}
		detail := err.Error()
		if reqErr != nil && reqErr.Reason != "" {
			detail = reqErr.Reason
		}
		out = append(out, message(http.StatusBadRequest, typeInvalidRequest, "Request does not match the API contract", detail, field))
	}
	visit(err)
	return out
}
