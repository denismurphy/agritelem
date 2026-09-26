package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/denismurphy/agritelem/backend/internal/auth"
	"github.com/denismurphy/agritelem/backend/internal/domain"
	"github.com/denismurphy/agritelem/backend/internal/pipeline"
	"github.com/denismurphy/agritelem/backend/internal/service"
	"github.com/denismurphy/agritelem/backend/internal/store"
)

var (
	now    = time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)
	farm   = domain.Location{Scheme: "dk.herdnumber", ID: "123456"}
	bessie = domain.Animal{ID: domain.AnimalID{Scheme: domain.SchemeISO11785, ID: "372000000000001"}, Location: farm,
		Specie: "Cattle", Gender: "Female", Status: "Alive", Meta: domain.Meta{Source: "seed", SourceID: "1", Modified: now}}
)

// fakeStore serves one animal and records writes.
type fakeStore struct {
	service.Store
	remarks []domain.Remark
}

func (f *fakeStore) ActiveAnimals(_ context.Context, _ domain.Location, ids []domain.AnimalID) (map[domain.AnimalID]bool, error) {
	out := map[domain.AnimalID]bool{}
	for _, id := range ids {
		out[id] = id == bessie.ID
	}
	return out, nil
}

func (f *fakeStore) ListAnimals(_ context.Context, _ domain.Location, _ time.Time, after store.Cursor, limit int) ([]domain.Animal, error) {
	if after.Key2 != "" {
		return nil, nil
	}
	a2 := bessie
	a2.ID.ID = "372000000000002"
	return []domain.Animal{bessie, a2}[:min(2, limit)], nil
}

func (f *fakeStore) CreateRemark(_ context.Context, r domain.Remark) (store.RemarkStatus, domain.Remark, error) {
	r.ID = "0192d7c4-0000-7000-8000-000000000001"
	f.remarks = append(f.remarks, r)
	return store.RemarkCreated, r, nil
}

func (f *fakeStore) Ingest(_ context.Context, _ domain.Location, subs []domain.SummarySubmission, _ []store.Rejection) ([]store.IngestOutcome, error) {
	return make([]store.IngestOutcome, len(subs)), nil
}

func (f *fakeStore) DailyAggregates(context.Context, domain.AnimalID, time.Time, time.Time) ([]pipeline.DailyAggregate, error) {
	return nil, nil
}

type harness struct {
	server *httptest.Server
	issuer *auth.Issuer
	store  *fakeStore
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	priv, pub, err := auth.GenerateKeyPEM()
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := auth.NewIssuer(priv, "test", "agritelem-api")
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := auth.NewVerifier(pub, "test", "agritelem-api")
	if err != nil {
		t.Fatal(err)
	}
	fs := &fakeStore{}
	public, _ := url.Parse("https://api.example.test")
	handler, err := NewHandler(Config{
		Service:   service.New(fs, func() time.Time { return now }),
		Verifier:  verifier,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		PublicURL: public,
		Ready:     func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &harness{server: srv, issuer: issuer, store: fs}
}

func (h *harness) token(t *testing.T, subject string, scopes ...string) string {
	t.Helper()
	tok, err := h.issuer.Issue(subject, scopes, []domain.Location{farm}, time.Now(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// result is the part of a response the tests inspect; the body has already
// been read and closed.
type result struct {
	StatusCode int
	Header     http.Header
}

func (h *harness) do(t *testing.T, method, path, token string, body any) (result, map[string]any) {
	t.Helper()
	resp, data := h.doRaw(t, method, path, token, body)
	var decoded map[string]any
	if len(data) > 0 && data[0] == '{' {
		if err := json.Unmarshal(data, &decoded); err != nil {
			t.Fatalf("decode %s: %v", data, err)
		}
	}
	return resp, decoded
}

func (h *harness) doRaw(t *testing.T, method, path, token string, body any) (result, []byte) {
	t.Helper()
	var r io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		r = strings.NewReader(b)
	default:
		data, err := json.Marshal(b)
		if err != nil {
			t.Fatal(err)
		}
		r = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, h.server.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return result{StatusCode: resp.StatusCode, Header: resp.Header}, data
}

func firstError(body map[string]any) map[string]any {
	errs, _ := body["errors"].([]any)
	if len(errs) == 0 {
		return nil
	}
	m, _ := errs[0].(map[string]any)
	return m
}

const animals = "/locations/dk.herdnumber/123456/animals"
const remarks = "/locations/dk.herdnumber/123456/remarks"

func TestAuthentication(t *testing.T) {
	h := newHarness(t)
	read := h.token(t, "ie.app", auth.ScopeAnimalsRead)

	resp, _ := h.do(t, http.MethodGet, animals, "", nil)
	if resp.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Bearer") {
		t.Errorf("no token: %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
	if resp, _ := h.do(t, http.MethodGet, animals, read+"x", nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("bad signature: %d, want 401", resp.StatusCode)
	}
	if resp, _ := h.do(t, http.MethodGet, "/locations/dk.herdnumber/999999/animals", read, nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("other location: %d, want 403", resp.StatusCode)
	}
	if resp, _ := h.do(t, http.MethodGet, remarks, read, nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("missing scope: %d, want 403", resp.StatusCode)
	}
	if resp, _ := h.do(t, http.MethodGet, "/healthz", "", nil); resp.StatusCode != http.StatusNoContent {
		t.Errorf("healthz: %d, want 204 without auth", resp.StatusCode)
	}
}

func TestPaginationLinks(t *testing.T) {
	h := newHarness(t)
	resp, body := h.do(t, http.MethodGet, animals+"?page-size=1", h.token(t, "ie.app", auth.ScopeAnimalsRead), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %v", resp.StatusCode, body)
	}
	view, _ := body["view"].(map[string]any)
	next, _ := view["next"].(string)
	if !strings.HasPrefix(next, "https://api.example.test"+animals+"?") || !strings.Contains(next, "page-token=") {
		t.Errorf("next = %q, want an absolute link to the next page", next)
	}
	if members, _ := body["member"].([]any); len(members) != 1 {
		t.Errorf("members = %v, want 1", members)
	}

	if resp, body := h.do(t, http.MethodGet, animals+"?page-token=%21%21", h.token(t, "ie.app", auth.ScopeAnimalsRead), nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("malformed token: %d %v, want 400", resp.StatusCode, body)
	}
}

func TestContractValidation(t *testing.T) {
	h := newHarness(t)
	write := h.token(t, "ie.app", auth.ScopeRemarksWrite)

	resp, body := h.do(t, http.MethodPost, remarks, write, map[string]any{"resourceType": "icarRemarkEventResource", "note": "x"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing animal: %d, want 400", resp.StatusCode)
	}
	e := firstError(body)
	if e["instance"] != "/animal" || strings.Contains(e["detail"].(string), "Schema:") {
		t.Errorf("error = %v, want a concise message at /animal", e)
	}

	nested := remarkBody("ie.app", "n1")
	nested["meta"].(map[string]any)["created"] = 5
	resp, body = h.do(t, http.MethodPost, remarks, write, nested)
	if e := firstError(body); resp.StatusCode != http.StatusBadRequest || e["instance"] != "/meta/created" {
		t.Errorf("nested error: %d %v, want 400 at /meta/created", resp.StatusCode, e)
	}

	if resp, _ := h.do(t, http.MethodGet, animals+"?page-size=501", h.token(t, "ie.app", auth.ScopeAnimalsRead), nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("page-size above maximum: %d, want 400", resp.StatusCode)
	}
	if resp, _ := h.do(t, http.MethodPost, remarks, write, "{not json"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("malformed JSON: %d, want 400", resp.StatusCode)
	}
	if resp, _ := h.do(t, http.MethodGet, "/nope", write, nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown path: %d, want 404", resp.StatusCode)
	}
	big := `{"resourceType":"icarRemarkEventResource","note":"` + strings.Repeat("a", MaxBodyBytes) + `"}`
	if resp, _ := h.do(t, http.MethodPost, remarks, write, big); resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("oversized body: %d, want 413", resp.StatusCode)
	}
	if resp, body := h.do(t, http.MethodGet, remarks+"?meta-modified-to=2026-01-01T00:00:00Z", h.token(t, "ie.app", auth.ScopeRemarksRead), nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unsupported filter: %d %v, want 400", resp.StatusCode, body)
	}
}

func remarkBody(source, sourceID string) map[string]any {
	return map[string]any{
		"resourceType":  "icarRemarkEventResource",
		"animal":        map[string]any{"scheme": domain.SchemeISO11785, "id": bessie.ID.ID},
		"eventDateTime": "2026-09-10T08:00:00Z",
		"note":          "Lame on left hind",
		"agritelem":     map[string]any{"category": "Veterinary"},
		"meta":          map[string]any{"source": source, "sourceId": sourceID, "modified": "2026-09-10T08:00:00Z"},
	}
}

func TestPostRemark(t *testing.T) {
	h := newHarness(t)
	write := h.token(t, "ie.app", auth.ScopeRemarksWrite)

	resp, body := h.do(t, http.MethodPost, remarks, write, remarkBody("ie.app", "r1"))
	if resp.StatusCode != http.StatusOK || body["id"] == nil {
		t.Fatalf("create: %d %v, want 200 with the stored remark", resp.StatusCode, body)
	}
	if ext, _ := body["agritelem"].(map[string]any); ext["category"] != "Veterinary" {
		t.Errorf("category not round-tripped: %v", body["agritelem"])
	}

	resp, body = h.do(t, http.MethodPost, remarks, write, remarkBody("ie.other", "r2"))
	if e := firstError(body); resp.StatusCode != http.StatusUnprocessableEntity || e["type"] != string(domain.CodeSourceMismatch) {
		t.Errorf("spoofed source: %d %v, want 422 %s", resp.StatusCode, body, domain.CodeSourceMismatch)
	}
	if len(h.store.remarks) != 1 {
		t.Errorf("stored %d remarks, want only the valid one", len(h.store.remarks))
	}
}

func TestRequestIDIsPropagatedOrGenerated(t *testing.T) {
	h := newHarness(t)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, h.server.URL+"/healthz", nil)
	req.Header.Set("X-Request-ID", "abc-123")
	resp, err := h.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got := resp.Header.Get("X-Request-ID"); got != "abc-123" {
		t.Errorf("request id = %q, want the caller's", got)
	}
	req.Header.Set("X-Request-ID", "has space") // legal on the wire, unsafe in logs
	resp, err = h.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if got := resp.Header.Get("X-Request-ID"); len(got) != 32 {
		t.Errorf("request id = %q, want a generated one replacing the unsafe value", got)
	}
}
