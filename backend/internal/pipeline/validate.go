// Package pipeline implements the Agritelem cleaning pipeline. Step 1
// (Validate) runs at ingest; steps 2-5 (Derive) run over stored hourly data
// when summaries are read. Both are pure functions.
package pipeline

import (
	"fmt"
	"strings"
	"time"

	"github.com/denismurphy/agritelem/backend/internal/domain"
)

// RawMeta is ICAR meta as received, before validation.
type RawMeta struct {
	Source   string
	SourceID *string
	Creator  *string
	Modified time.Time
}

// RawStatistic is one ICAR icarObservationStatisticsType as received.
type RawStatistic struct {
	MetricScheme string
	MetricID     string
	Start        time.Time
	Duration     string
	Unit         *string
	Aggregation  *string
	Value        *float64
}

// RawSummary is one ICAR icarObservationSummaryResource as received.
type RawSummary struct {
	AnimalScheme string
	AnimalID     string
	Meta         *RawMeta
	Statistics   []RawStatistic
}

// Policy limits applied at ingest.
const (
	// MaxClockSkew is how far ahead of server time a timestamp may be.
	MaxClockSkew = 5 * time.Minute
	// MaxStatisticsPerResource bounds the work one resource can cause: a
	// month of hourly values for one metric.
	MaxStatisticsPerResource = 24 * 31
	// HourlyDuration is the only ICAR icarDurationType accepted at ingest.
	HourlyDuration = "PT1H"
)

// Validate applies step 1 of the pipeline to one resource submitted by
// principal (the authenticated source) at server time now. It returns every
// problem found, not just the first; a resource is accepted only when it has
// none.
func Validate(raw RawSummary, principal string, now time.Time) (domain.SummarySubmission, []*domain.Problem) {
	var v validator
	sub := domain.SummarySubmission{
		Meta: v.meta(raw.Meta, principal, now),
	}

	animal, err := domain.ParseAnimalID(raw.AnimalScheme, raw.AnimalID)
	v.add(err, "/animal")
	sub.Animal = animal

	switch n := len(raw.Statistics); {
	case n == 0:
		v.problem(domain.CodeMissingField, "/statistics", "at least one statistic is required")
	case n > MaxStatisticsPerResource:
		v.problem(domain.CodeTooManyStatistics, "/statistics",
			"%d statistics exceed the limit of %d per resource; split the resource", n, MaxStatisticsPerResource)
	default:
		sub.Observations = v.statistics(raw.Statistics, now)
	}

	if len(v.problems) > 0 {
		return domain.SummarySubmission{}, v.problems
	}
	return sub, nil
}

type validator struct {
	problems []*domain.Problem
}

func (v *validator) problem(code domain.Code, field, format string, args ...any) {
	v.problems = append(v.problems, &domain.Problem{Code: code, Field: field, Detail: fmt.Sprintf(format, args...)})
}

// add records err, which must be nil or a *domain.Problem, prefixing its field
// with base.
func (v *validator) add(err error, base string) {
	if err == nil {
		return
	}
	v.problems = append(v.problems, domain.AsProblem(err, base))
}

func (v *validator) meta(raw *RawMeta, principal string, now time.Time) domain.Meta {
	if raw == nil {
		v.problem(domain.CodeMissingField, "/meta", "meta with source and sourceId is required")
		return domain.Meta{}
	}
	m := domain.Meta{Source: raw.Source, Modified: raw.Modified}
	if raw.SourceID == nil || strings.TrimSpace(*raw.SourceID) == "" {
		v.problem(domain.CodeSourceIDRequired, "/meta/sourceId",
			"meta.sourceId is required so that retries are idempotent")
	} else {
		m.SourceID = *raw.SourceID
	}
	if raw.Creator != nil {
		m.Creator = *raw.Creator
	}
	if raw.Source != principal {
		v.problem(domain.CodeSourceMismatch, "/meta/source",
			"meta.source %q does not match the authenticated source %q", raw.Source, principal)
	}
	if err := domain.RequireUTC("/meta/modified", raw.Modified); err != nil {
		v.add(err, "")
	} else if raw.Modified.After(now.Add(MaxClockSkew)) {
		v.problem(domain.CodeFutureTimestamp, "/meta/modified",
			"meta.modified %s is ahead of server time %s", raw.Modified.Format(time.RFC3339), now.Format(time.RFC3339))
	}
	return m
}

func (v *validator) statistics(raw []RawStatistic, now time.Time) []domain.Observation {
	type key struct {
		metric domain.Metric
		start  time.Time
	}
	seen := make(map[key]int, len(raw))
	out := make([]domain.Observation, 0, len(raw))

	for i, s := range raw {
		base := fmt.Sprintf("/statistics/%d", i)
		before := len(v.problems)

		if s.Duration != HourlyDuration {
			v.problem(domain.CodeUnsupportedDuration, base+"/duration",
				"duration %q is not accepted; gateways submit %s statistics", s.Duration, HourlyDuration)
		}
		v.window(s.Start, base, now)

		metric := domain.Metric{Scheme: s.MetricScheme, ID: s.MetricID}
		spec, err := domain.LookupMetric(metric)
		v.add(err, base)
		if err == nil {
			v.matchesSpec(s, spec, base)
		}

		if len(v.problems) > before {
			continue
		}
		k := key{metric, s.Start.UTC()}
		if first, dup := seen[k]; dup {
			v.problem(domain.CodeDuplicateInResource, base,
				"duplicates /statistics/%d (same metric and hour)", first)
			continue
		}
		seen[k] = i
		out = append(out, domain.Observation{Metric: metric, Start: s.Start.UTC(), Value: *s.Value})
	}
	return out
}

func (v *validator) window(start time.Time, base string, now time.Time) {
	if err := domain.RequireUTC(base+"/startDateTime", start); err != nil {
		v.add(err, "")
		return
	}
	if !start.Equal(start.Truncate(time.Hour)) {
		v.problem(domain.CodeMisalignedWindow, base+"/startDateTime",
			"startDateTime %s is not on an hour boundary", start.Format(time.RFC3339Nano))
		return
	}
	if end := start.Add(time.Hour); end.After(now.Add(MaxClockSkew)) {
		v.problem(domain.CodeFutureTimestamp, base+"/startDateTime",
			"the hour starting %s has not ended at server time %s", start.Format(time.RFC3339), now.Format(time.RFC3339))
	}
}

func (v *validator) matchesSpec(s RawStatistic, spec domain.MetricSpec, base string) {
	if s.Unit != nil && *s.Unit != spec.Unit {
		v.problem(domain.CodeUnitMismatch, base+"/unit",
			"%s is measured in %s, got %q", spec.Metric.ID, spec.Unit, *s.Unit)
	}
	if s.Aggregation != nil && *s.Aggregation != string(spec.Aggregation) {
		v.problem(domain.CodeAggregationMismatch, base+"/aggregation",
			"%s hourly values are aggregated by %s, got %q", spec.Metric.ID, spec.Aggregation, *s.Aggregation)
	}
	switch {
	case s.Value == nil:
		v.problem(domain.CodeMissingField, base+"/value", "value is required")
	case !spec.Hourly.Contains(*s.Value):
		v.problem(domain.CodePhysiologicalOutlier, base+"/value",
			"%s value %v %s is outside the plausible hourly range [%v, %v]",
			spec.Metric.ID, *s.Value, spec.Unit, spec.Hourly.Min, spec.Hourly.Max)
	}
}
