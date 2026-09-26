//go:build integration

package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"slices"
	"testing"
	"time"

	"github.com/denismurphy/agritelem/backend/internal/auth"
	"github.com/denismurphy/agritelem/backend/internal/domain"
	"github.com/denismurphy/agritelem/backend/internal/service"
	"github.com/denismurphy/agritelem/backend/internal/store"
	"github.com/denismurphy/agritelem/backend/internal/testdb"
)

var dsn string

func TestMain(m *testing.M) { os.Exit(testdb.Main(m, &dsn)) }

const (
	batchPath    = "/batches/locations/dk.herdnumber/123456/observation-summary-metrics"
	summaryPath  = "/locations/dk.herdnumber/123456/observation-summary-metrics"
	overviewPath = "/agritelem/locations/dk.herdnumber/123456/herd-overview"
	gateway      = "ie.example.gateway"
)

// e2eNow is 09:00 UTC; the last complete day is the estrus day.
var (
	e2eNow    = time.Date(2026, 9, 16, 9, 0, 0, 0, time.UTC)
	estrusDay = time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC)
)

func newE2E(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertLocation(ctx, farm, "Test farm"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertAnimal(ctx, bessie); err != nil {
		t.Fatal(err)
	}

	priv, pub, err := auth.GenerateKeyPEM()
	if err != nil {
		t.Fatal(err)
	}
	issuer, _ := auth.NewIssuer(priv, "test", "agritelem-api")
	verifier, _ := auth.NewVerifier(pub, "test", "agritelem-api")
	public, _ := url.Parse("https://api.example.test")
	handler, err := NewHandler(Config{
		Service:   service.New(st, func() time.Time { return e2eNow }),
		Verifier:  verifier,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		PublicURL: public,
		Ready:     st.Ping,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &harness{server: srv, issuer: issuer}
}

// dayResource is one day of hourly rumination and step statistics.
func dayResource(day time.Time, sourceID string, rumPerHour, stepsPerHour float64) map[string]any {
	var stats []map[string]any
	for h := range 24 {
		start := day.Add(time.Duration(h) * time.Hour).Format(time.RFC3339)
		stats = append(stats,
			map[string]any{"startDateTime": start, "duration": "PT1H", "metric": map[string]any{"scheme": "org.icar.activities", "id": "RuminationTime"}, "unit": "MIN", "aggregation": "Sum", "value": rumPerHour},
			map[string]any{"startDateTime": start, "duration": "PT1H", "metric": map[string]any{"scheme": "org.icar.activities", "id": "StepCount"}, "value": stepsPerHour},
		)
	}
	return map[string]any{
		"resourceType": "icarObservationSummaryResource",
		"animal":       map[string]any{"scheme": bessie.ID.Scheme, "id": bessie.ID.ID},
		"meta":         map[string]any{"source": gateway, "sourceId": sourceID, "modified": day.Add(25 * time.Hour).Format(time.RFC3339)},
		"statistics":   stats,
	}
}

func messageTypes(result map[string]any) []string {
	msgs, _ := result["messages"].([]any)
	var out []string
	for _, m := range msgs {
		out = append(out, m.(map[string]any)["type"].(string))
	}
	return out
}

func TestEndToEnd(t *testing.T) {
	h := newE2E(t)
	write := h.token(t, gateway, auth.ScopeObservationsWrite)
	read := h.token(t, gateway, auth.ScopeAnimalsRead)

	// 14 ordinary days (rumination ~480 min, steps ~3000) then an estrus day.
	var batch []any
	for i := 14; i >= 1; i-- {
		day := estrusDay.AddDate(0, 0, -i)
		wobble := float64(i % 3)
		batch = append(batch, dayResource(day, fmt.Sprintf("day-%d", i), 20+wobble*0.4, 125+wobble*4))
	}
	batch = append(batch, dayResource(estrusDay, "estrus", 15, 300))
	outlier := dayResource(estrusDay.AddDate(0, 0, -20), "outlier", 61, 100)
	batch = append(batch, outlier)

	results := postBatch(t, h, write, batch)
	for i := range 15 {
		if types := messageTypes(results[i]); len(types) != 0 || results[i]["id"] == nil {
			t.Fatalf("resource %d: messages %v id %v, want accepted", i, types, results[i]["id"])
		}
	}
	if types := messageTypes(results[15]); !slices.Contains(types, string(domain.CodePhysiologicalOutlier)) {
		t.Errorf("outlier messages = %v", types)
	}

	// Retrying the whole batch changes nothing and reports replays.
	replayed := postBatch(t, h, write, batch[:1])
	if types := messageTypes(replayed[0]); !slices.Equal(types, []string{"REPLAYED"}) || replayed[0]["id"] != results[0]["id"] {
		t.Errorf("replay = %v, want REPLAYED with the original id", replayed[0])
	}
	changed := dayResource(estrusDay.AddDate(0, 0, -14), "day-14", 19, 125)
	if types := messageTypes(postBatch(t, h, write, []any{changed})[0]); !slices.Equal(types, []string{string(domain.CodeConflictingResubmission)}) {
		t.Errorf("conflict messages = %v", types)
	}

	// Derived daily statistics flag the estrus day on both signals.
	q := fmt.Sprintf("%s?animal-scheme=%s&animal-id=%s&duration=P1D&date-from=%s&date-to=%s",
		summaryPath, url.QueryEscape(bessie.ID.Scheme), bessie.ID.ID, estrusDay.Format(time.DateOnly), estrusDay.Format(time.DateOnly))
	resp, body := h.do(t, http.MethodGet, q, read, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("summary: %d %v", resp.StatusCode, body)
	}
	stats := body["member"].([]any)[0].(map[string]any)["statistics"].([]any)
	if len(stats) != 2 {
		t.Fatalf("statistics = %v, want rumination and steps for one day", stats)
	}
	for _, s := range stats {
		stat := s.(map[string]any)
		ext := stat["agritelem"].(map[string]any)
		flags, _ := ext["flags"].([]any)
		if !slices.Contains(flags, any("HeatSignature")) || ext["dataQuality"] != "Verified" || stat["isIncomplete"] != false {
			t.Errorf("%v: agritelem %v, want a verified complete day flagged HeatSignature", stat["metric"], ext)
		}
	}

	resp, body = h.do(t, http.MethodGet, overviewPath, read, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("overview: %d %v", resp.StatusCode, body)
	}
	if kpis := body["kpis"].(map[string]any); kpis["estrusAlerts"] != float64(1) || body["day"] != estrusDay.Format(time.DateOnly) {
		t.Errorf("overview = %v, want one estrus alert for %s", body, estrusDay.Format(time.DateOnly))
	}
}

func postBatch(t *testing.T, h *harness, token string, batch []any) []map[string]any {
	t.Helper()
	resp, data := h.doRaw(t, http.MethodPost, batchPath, token, batch)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("batch status %d: %s", resp.StatusCode, data)
	}
	var out []map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("decode batch results %s: %v", data, err)
	}
	return out
}

// wireTime matches the spec's timestamp format exactly: UTC, whole seconds.
var wireTime = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z$`)

func TestRemarkLifecycleEndToEnd(t *testing.T) {
	h := newE2E(t)
	token := h.token(t, "ie.app", auth.ScopeRemarksWrite, auth.ScopeRemarksRead)
	body := remarkBody("ie.app", "e2e-remark")
	body["eventDateTime"] = e2eNow.Add(-time.Hour).Format(time.RFC3339)

	resp, created := h.do(t, http.MethodPost, remarks, token, body)
	if resp.StatusCode != http.StatusOK || created["id"] == nil {
		t.Fatalf("create: %d %v", resp.StatusCode, created)
	}
	meta := created["meta"].(map[string]any)
	if !wireTime.MatchString(meta["modified"].(string)) {
		t.Errorf("meta.modified = %q, want YYYY-MM-DDTHH:MM:SSZ", meta["modified"])
	}

	resp, replayed := h.do(t, http.MethodPost, remarks, token, body)
	if resp.StatusCode != http.StatusOK || replayed["id"] != created["id"] {
		t.Errorf("replay: %d id %v, want 200 with id %v", resp.StatusCode, replayed["id"], created["id"])
	}

	body["note"] = "Something else"
	resp, conflict := h.do(t, http.MethodPost, remarks, token, body)
	current, _ := conflict["current"].(map[string]any)
	if resp.StatusCode != http.StatusConflict || current["note"] != "Lame on left hind" {
		t.Errorf("conflict: %d %v, want 409 with the stored remark", resp.StatusCode, conflict)
	}

	resp, list := h.do(t, http.MethodGet, remarks, token, nil)
	members, _ := list["member"].([]any)
	if resp.StatusCode != http.StatusOK || len(members) != 1 {
		t.Errorf("list: %d %v, want the one remark", resp.StatusCode, list)
	}
}
