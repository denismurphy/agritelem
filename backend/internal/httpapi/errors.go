package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/denismurphy/agritelem/backend/internal/auth"
	"github.com/denismurphy/agritelem/backend/internal/domain"
	"github.com/denismurphy/agritelem/backend/internal/service"
)

// Problem types for failures that are not domain validation problems.
const (
	typeInvalidRequest  = "INVALID_REQUEST"
	typeUnauthenticated = "UNAUTHENTICATED"
	typeForbidden       = "FORBIDDEN"
	typeNotFound        = "NOT_FOUND"
	typeTooLarge        = "PAYLOAD_TOO_LARGE"
	typeInternal        = "INTERNAL_ERROR"
)

func message(status int, typ, title, detail, instance string) IcarResponseMessageResource {
	sev := Error
	st := int32(status) //nolint:gosec // HTTP status codes fit in int32
	m := IcarResponseMessageResource{Type: &typ, Severity: &sev, Status: &st, Title: &title, Detail: &detail}
	if instance != "" {
		m.Instance = &instance
	}
	return m
}

func problemMessages(status int, problems []*domain.Problem) []IcarResponseMessageResource {
	out := make([]IcarResponseMessageResource, len(problems))
	for i, p := range problems {
		out[i] = message(status, string(p.Code), string(p.Code), p.Detail, p.Field)
	}
	return out
}

// writeErrors writes an ICAR icarErrorCollection.
func writeErrors(w http.ResponseWriter, status int, messages []IcarResponseMessageResource) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(IcarErrorCollection{Errors: &messages})
}

func writeError(w http.ResponseWriter, status int, typ, detail string) {
	writeErrors(w, status, []IcarResponseMessageResource{message(status, typ, http.StatusText(status), detail, "")})
}

// handleError maps an error returned by a handler to an HTTP response.
// Unexpected errors are logged and reported without internal detail.
func handleError(logger *slog.Logger) func(http.ResponseWriter, *http.Request, error) {
	return func(w http.ResponseWriter, r *http.Request, err error) {
		var problem *domain.Problem
		switch {
		case errors.As(err, &problem):
			writeErrors(w, http.StatusUnprocessableEntity, problemMessages(http.StatusUnprocessableEntity, []*domain.Problem{problem}))
		case errors.Is(err, service.ErrNotFound):
			writeError(w, http.StatusNotFound, typeNotFound, "the requested resource does not exist at this location")
		case errors.Is(err, auth.ErrForbidden):
			writeError(w, http.StatusForbidden, typeForbidden, err.Error())
		case errors.Is(err, errBadRequest):
			writeError(w, http.StatusBadRequest, typeInvalidRequest, err.Error())
		default:
			logger.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "error", err)
			writeError(w, http.StatusInternalServerError, typeInternal, "the server could not complete the request")
		}
	}
}

// errBadRequest marks client errors detected in handlers outside the schema.
var errBadRequest = errors.New("bad request")
