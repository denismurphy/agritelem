package pipeline

import (
	"slices"
	"testing"
	"time"

	"github.com/denismurphy/agritelem/backend/internal/domain"
)

var (
	now       = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	hour      = time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	principal = "ie.example.gateway"
)

func ptr[T any](v T) *T { return &v }

func validRaw() RawSummary {
	return RawSummary{
		AnimalScheme: domain.SchemeISO11785,
		AnimalID:     "372000012345678",
		Meta:         &RawMeta{Source: principal, SourceID: ptr("rec-1"), Modified: hour.Add(time.Hour)},
		Statistics: []RawStatistic{
			{MetricScheme: "org.icar.activities", MetricID: "RuminationTime", Start: hour, Duration: "PT1H", Unit: ptr("MIN"), Aggregation: ptr("Sum"), Value: ptr(31.0)},
			{MetricScheme: "org.icar.activities", MetricID: "StepCount", Start: hour, Duration: "PT1H", Value: ptr(120.0)},
		},
	}
}

func codes(problems []*domain.Problem) []domain.Code {
	out := make([]domain.Code, 0, len(problems))
	for _, p := range problems {
		out = append(out, p.Code)
	}
	slices.Sort(out)
	return out
}

func TestValidateAcceptsWellFormedResource(t *testing.T) {
	got, problems := Validate(validRaw(), principal, now)
	if len(problems) != 0 {
		t.Fatalf("unexpected problems: %v", problems)
	}
	want := domain.SummarySubmission{
		Animal: domain.AnimalID{Scheme: domain.SchemeISO11785, ID: "372000012345678"},
		Meta:   domain.Meta{Source: principal, SourceID: "rec-1", Modified: hour.Add(time.Hour)},
		Observations: []domain.Observation{
			{Metric: domain.RuminationTime, Start: hour, Value: 31},
			{Metric: domain.StepCount, Start: hour, Value: 120},
		},
	}
	if got.Animal != want.Animal || got.Meta != want.Meta || !slices.Equal(got.Observations, want.Observations) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestValidateRejects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*RawSummary)
		want   []domain.Code
	}{
		{"missing meta", func(r *RawSummary) { r.Meta = nil }, []domain.Code{domain.CodeMissingField}},
		{"missing sourceId", func(r *RawSummary) { r.Meta.SourceID = nil }, []domain.Code{domain.CodeSourceIDRequired}},
		{"blank sourceId", func(r *RawSummary) { r.Meta.SourceID = ptr("  ") }, []domain.Code{domain.CodeSourceIDRequired}},
		{"source is not the caller", func(r *RawSummary) { r.Meta.Source = "ie.other" }, []domain.Code{domain.CodeSourceMismatch}},
		{"modified in the future", func(r *RawSummary) { r.Meta.Modified = now.Add(MaxClockSkew + time.Second) }, []domain.Code{domain.CodeFutureTimestamp}},
		{"modified with offset", func(r *RawSummary) {
			r.Meta.Modified = r.Meta.Modified.In(time.FixedZone("", 3600))
		}, []domain.Code{domain.CodeNonUTCTimestamp}},
		{"bad animal id", func(r *RawSummary) { r.AnimalID = "123" }, []domain.Code{domain.CodeInvalidAnimalID}},
		{"no statistics", func(r *RawSummary) { r.Statistics = nil }, []domain.Code{domain.CodeMissingField}},
		{"daily duration", func(r *RawSummary) { r.Statistics[0].Duration = "P1D" }, []domain.Code{domain.CodeUnsupportedDuration}},
		{"unaligned start", func(r *RawSummary) { r.Statistics[0].Start = hour.Add(time.Minute) }, []domain.Code{domain.CodeMisalignedWindow}},
		{"start with offset", func(r *RawSummary) {
			r.Statistics[0].Start = hour.In(time.FixedZone("", 3600))
		}, []domain.Code{domain.CodeNonUTCTimestamp}},
		{"window not yet ended", func(r *RawSummary) {
			r.Statistics[0].Start = now.Truncate(time.Hour)
		}, []domain.Code{domain.CodeFutureTimestamp}},
		{"unknown metric", func(r *RawSummary) { r.Statistics[0].MetricID = "ActivityIndex" }, []domain.Code{domain.CodeUnsupportedMetric}},
		{"wrong unit", func(r *RawSummary) { r.Statistics[0].Unit = ptr("HUR") }, []domain.Code{domain.CodeUnitMismatch}},
		{"wrong aggregation", func(r *RawSummary) { r.Statistics[0].Aggregation = ptr("Average") }, []domain.Code{domain.CodeAggregationMismatch}},
		{"missing value", func(r *RawSummary) { r.Statistics[0].Value = nil }, []domain.Code{domain.CodeMissingField}},
		{"value above bound", func(r *RawSummary) { r.Statistics[0].Value = ptr(61.0) }, []domain.Code{domain.CodePhysiologicalOutlier}},
		{"negative value", func(r *RawSummary) { r.Statistics[1].Value = ptr(-1.0) }, []domain.Code{domain.CodePhysiologicalOutlier}},
		{"duplicate metric and hour", func(r *RawSummary) {
			r.Statistics = append(r.Statistics, r.Statistics[0])
		}, []domain.Code{domain.CodeDuplicateInResource}},
		{"too many statistics", func(r *RawSummary) {
			for i := range MaxStatisticsPerResource {
				s := r.Statistics[0]
				s.Start = hour.Add(-time.Duration(i+1) * time.Hour)
				r.Statistics = append(r.Statistics, s)
			}
		}, []domain.Code{domain.CodeTooManyStatistics}},
		{"reports every problem", func(r *RawSummary) {
			r.Meta.SourceID = nil
			r.Statistics[0].Value = ptr(61.0)
			r.Statistics[1].Duration = "P1D"
		}, []domain.Code{domain.CodePhysiologicalOutlier, domain.CodeSourceIDRequired, domain.CodeUnsupportedDuration}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := validRaw()
			tt.mutate(&raw)
			_, problems := Validate(raw, principal, now)
			if got := codes(problems); !slices.Equal(got, tt.want) {
				t.Errorf("codes = %v, want %v (problems: %v)", got, tt.want, problems)
			}
		})
	}
}

func TestValidateReportsFieldPointers(t *testing.T) {
	raw := validRaw()
	raw.Statistics[1].Value = ptr(-1.0)
	_, problems := Validate(raw, principal, now)
	if len(problems) != 1 || problems[0].Field != "/statistics/1/value" {
		t.Fatalf("problems = %v, want one at /statistics/1/value", problems)
	}
}
