package service

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/denismurphy/agritelem/backend/internal/domain"
	"github.com/denismurphy/agritelem/backend/internal/pipeline"
	"github.com/denismurphy/agritelem/backend/internal/store"
)

var (
	now    = time.Date(2026, 9, 10, 9, 30, 0, 0, time.UTC)
	today  = time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC)
	farm   = domain.Location{Scheme: "dk.herdnumber", ID: "123456"}
	bessie = domain.AnimalID{Scheme: domain.SchemeISO11785, ID: "372000000000001"}
	daisy  = domain.AnimalID{Scheme: domain.SchemeISO11785, ID: "372000000000002"}
	ghost  = domain.AnimalID{Scheme: domain.SchemeISO11785, ID: "372000000000099"}
)

// fakeStore records calls and serves canned data.
type fakeStore struct {
	Store // unimplemented methods panic if called

	active      map[domain.AnimalID]bool
	outcomes    func(subs []domain.SummarySubmission) []store.IngestOutcome
	ingested    []domain.SummarySubmission
	quarantined []store.Rejection
	aggs        map[domain.AnimalID][]pipeline.DailyAggregate
	aggRange    [2]time.Time
	vets        map[domain.AnimalID]bool
}

func (f *fakeStore) ActiveAnimals(_ context.Context, _ domain.Location, ids []domain.AnimalID) (map[domain.AnimalID]bool, error) {
	out := map[domain.AnimalID]bool{}
	for _, id := range ids {
		if f.active[id] {
			out[id] = true
		}
	}
	return out, nil
}

func (f *fakeStore) Ingest(_ context.Context, _ domain.Location, subs []domain.SummarySubmission, rejected []store.Rejection) ([]store.IngestOutcome, error) {
	f.ingested = append(f.ingested, subs...)
	f.quarantined = append(f.quarantined, rejected...)
	return f.outcomes(subs), nil
}

func (f *fakeStore) DailyAggregates(_ context.Context, animal domain.AnimalID, from, to time.Time) ([]pipeline.DailyAggregate, error) {
	f.aggRange = [2]time.Time{from, to}
	return f.aggs[animal], nil
}

func (f *fakeStore) ActiveAnimalsAt(context.Context, domain.Location) ([]domain.Animal, error) {
	var out []domain.Animal
	for _, id := range []domain.AnimalID{bessie, daisy} {
		if f.active[id] {
			out = append(out, domain.Animal{ID: id})
		}
	}
	return out, nil
}

func (f *fakeStore) LocationDailyAggregates(_ context.Context, _ domain.Location, from, to time.Time) (map[domain.AnimalID][]pipeline.DailyAggregate, error) {
	f.aggRange = [2]time.Time{from, to}
	return f.aggs, nil
}

func (f *fakeStore) AnimalsWithRemarkSince(context.Context, domain.Location, domain.RemarkCategory, time.Time) (map[domain.AnimalID]bool, error) {
	return f.vets, nil
}

func newService(f *fakeStore) *Service { return New(f, func() time.Time { return now }) }

func ptr[T any](v T) *T { return &v }

func raw(animal domain.AnimalID, sourceID string, value float64) RawResource {
	hour := today.Add(-3 * time.Hour)
	summary := pipeline.RawSummary{
		AnimalScheme: animal.Scheme, AnimalID: animal.ID,
		Meta: &pipeline.RawMeta{Source: "gw", SourceID: ptr(sourceID), Modified: now},
		Statistics: []pipeline.RawStatistic{{MetricScheme: domain.SchemeICARActivities, MetricID: "RuminationTime",
			Start: hour, Duration: "PT1H", Value: ptr(value)}},
	}
	body, _ := json.Marshal(map[string]string{"sourceId": sourceID})
	return RawResource{Summary: summary, JSON: body}
}

func TestIngestSummaries(t *testing.T) {
	existing := uuid.MustParse("01920000-0000-7000-8000-000000000001")
	f := &fakeStore{
		active: map[domain.AnimalID]bool{bessie: true},
		outcomes: func(subs []domain.SummarySubmission) []store.IngestOutcome {
			out := make([]store.IngestOutcome, len(subs))
			for i, s := range subs {
				switch s.Meta.SourceID {
				case "replay":
					out[i] = store.IngestOutcome{Status: store.Replayed, ResourceID: existing}
				case "conflict":
					out[i] = store.IngestOutcome{Status: store.Conflict, ResourceID: existing}
				default:
					out[i] = store.IngestOutcome{Status: store.Accepted, ResourceID: uuid.New()}
				}
			}
			return out
		},
	}
	results, err := newService(f).IngestSummaries(context.Background(), "gw", farm, []RawResource{
		raw(bessie, "new", 30),
		raw(bessie, "outlier", 61),
		raw(ghost, "unknown", 30),
		raw(bessie, "replay", 30),
		raw(bessie, "conflict", 30),
	})
	if err != nil {
		t.Fatal(err)
	}

	want := []IngestStatus{IngestAccepted, IngestRejected, IngestRejected, IngestReplayed, IngestConflict}
	for i, r := range results {
		if r.Status != want[i] {
			t.Errorf("result %d status = %v, want %v", i, r.Status, want[i])
		}
	}
	if firstCode(results[1]) != domain.CodePhysiologicalOutlier || firstCode(results[2]) != domain.CodeUnknownAnimal {
		t.Errorf("problems = %v / %v", results[1].Problems, results[2].Problems)
	}
	if results[3].ResourceID != existing.String() || firstCode(results[4]) != domain.CodeConflictingResubmission {
		t.Errorf("replay/conflict = %+v / %+v", results[3], results[4])
	}

	var stored []string
	for _, s := range f.ingested {
		stored = append(stored, s.Meta.SourceID)
	}
	if !slices.Equal(stored, []string{"new", "replay", "conflict"}) {
		t.Errorf("stored %v, want only valid resources for known animals", stored)
	}
	if len(f.quarantined) != 2 || string(f.quarantined[0].Resource) != `{"sourceId":"outlier"}` {
		t.Errorf("quarantined %+v, want the two rejected resources verbatim", f.quarantined)
	}
}

func TestSummaries(t *testing.T) {
	f := &fakeStore{active: map[domain.AnimalID]bool{bessie: true}, aggs: map[domain.AnimalID][]pipeline.DailyAggregate{
		bessie: {
			{Metric: domain.RuminationTime, Day: today.AddDate(0, 0, -1), Hours: 24, Sum: 500},
			{Metric: domain.StepCount, Day: today.AddDate(0, 0, -1), Hours: 24, Sum: 3000},
		},
	}}
	svc := newService(f)
	ctx := context.Background()

	got, err := svc.Summaries(ctx, farm, SummaryQuery{Animal: bessie, Duration: "P1D", Metric: &domain.RuminationTime})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Daily) != 1 || got.Daily[0].Value != 500 {
		t.Errorf("daily = %+v, want the one rumination day", got.Daily)
	}
	wantFrom := today.AddDate(0, 0, -(DefaultSummaryDays-1)-pipeline.BaselineDays)
	if !f.aggRange[0].Equal(wantFrom) || !f.aggRange[1].Equal(today.AddDate(0, 0, 1)) {
		t.Errorf("aggregates fetched for %v, want [%v, %v) to include baseline history", f.aggRange, wantFrom, today.AddDate(0, 0, 1))
	}

	if _, err := svc.Summaries(ctx, farm, SummaryQuery{Animal: ghost, Duration: "P1D"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown animal: err = %v, want ErrNotFound", err)
	}

	tests := []struct {
		name string
		q    SummaryQuery
		want domain.Code
	}{
		{"inverted range", SummaryQuery{Animal: bessie, Duration: "P1D", From: ptr(today), To: ptr(today.AddDate(0, 0, -1))}, domain.CodeInvalidValue},
		{"range too long", SummaryQuery{Animal: bessie, Duration: "P1D", From: ptr(today.AddDate(0, 0, -MaxSummaryDays)), To: ptr(today)}, domain.CodeInvalidValue},
		{"weekly duration", SummaryQuery{Animal: bessie, Duration: "P1W"}, domain.CodeUnsupportedDuration},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := svc.Summaries(ctx, farm, tt.q)
			var p *domain.Problem
			if !errors.As(err, &p) || p.Code != tt.want {
				t.Errorf("err = %v, want %s", err, tt.want)
			}
		})
	}
	if _, err := svc.Summaries(ctx, farm, SummaryQuery{Animal: bessie, Duration: "P1D", From: ptr(today.AddDate(0, 0, -(MaxSummaryDays - 1))), To: ptr(today)}); err != nil {
		t.Errorf("a range of exactly %d days was rejected: %v", MaxSummaryDays, err)
	}
}

func TestHerdOverview(t *testing.T) {
	yesterday := today.AddDate(0, 0, -1)
	var bessieAggs []pipeline.DailyAggregate
	for i := 14; i >= 1; i-- {
		day := yesterday.AddDate(0, 0, -i)
		wobble := float64(i % 3)
		bessieAggs = append(bessieAggs,
			pipeline.DailyAggregate{Metric: domain.RuminationTime, Day: day, Hours: 24, Sum: 480 + wobble*10},
			pipeline.DailyAggregate{Metric: domain.StepCount, Day: day, Hours: 24, Sum: 3000 + wobble*100})
	}
	bessieAggs = append(bessieAggs,
		pipeline.DailyAggregate{Metric: domain.RuminationTime, Day: yesterday, Hours: 24, Sum: 360},
		pipeline.DailyAggregate{Metric: domain.StepCount, Day: yesterday, Hours: 24, Sum: 7000})
	f := &fakeStore{
		active: map[domain.AnimalID]bool{bessie: true, daisy: true},
		aggs: map[domain.AnimalID][]pipeline.DailyAggregate{
			bessie: bessieAggs,
			daisy: {
				{Metric: domain.RuminationTime, Day: yesterday, Hours: 20, Sum: 400}, // imputed: excluded from the mean
				{Metric: domain.ReticularTemperature, Day: yesterday, Hours: 24, Sum: 24 * 40},
			},
		},
		vets: map[domain.AnimalID]bool{daisy: true},
	}
	got, err := newService(f).HerdOverview(context.Background(), farm)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Day.Equal(yesterday) {
		t.Errorf("day = %v, want the last complete UTC day %v", got.Day, yesterday)
	}
	if got.EstrusAlerts != 1 || got.HeatStress != 1 || got.VeterinaryAttention != 1 {
		t.Errorf("kpis = %d estrus, %d heat stress, %d vet; want 1 each", got.EstrusAlerts, got.HeatStress, got.VeterinaryAttention)
	}
	if got.MeanRuminationMinutes == nil || *got.MeanRuminationMinutes != 360 {
		t.Errorf("mean rumination = %v, want 360 from verified days only", got.MeanRuminationMinutes)
	}
	statuses := map[domain.AnimalID][]AnimalStatus{}
	for _, a := range got.Animals {
		statuses[a.Animal.ID] = a.Statuses
	}
	if !slices.Equal(statuses[bessie], []AnimalStatus{StatusInHeat}) ||
		!slices.Equal(statuses[daisy], []AnimalStatus{StatusHeatStress, StatusVeterinaryAttention}) {
		t.Errorf("statuses = %v", statuses)
	}
}

func firstCode(r IngestResult) domain.Code {
	if len(r.Problems) == 0 {
		return ""
	}
	return r.Problems[0].Code
}
