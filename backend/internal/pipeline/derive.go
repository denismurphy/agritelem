package pipeline

import (
	"cmp"
	"fmt"
	"slices"
	"time"

	"github.com/denismurphy/agritelem/backend/internal/domain"
)

// DailyAggregate is the store's rollup of the latest revision of each hourly
// observation for one metric on one UTC day.
type DailyAggregate struct {
	Metric domain.Metric
	Day    time.Time // midnight UTC
	Hours  int       // distinct hours with a value, 1..24
	Sum    float64   // sum of the hourly values
}

// Quality grades a derived daily value.
type Quality string

// Quality grades.
const (
	QualityVerified Quality = "Verified"
	QualityImputed  Quality = "Imputed"
)

// Flag is a cross-metric or range finding on a derived day.
type Flag string

// Flags.
const (
	FlagHeatSignature      Flag = "HeatSignature"
	FlagTimeBudgetConflict Flag = "TimeBudgetConflict"
	FlagThermalStress      Flag = "ThermalStress"
	FlagDailyOutOfRange    Flag = "DailyOutOfRange"
)

// DailyStatistic is a derived daily value with its quality, baseline and flags.
type DailyStatistic struct {
	Metric         domain.Metric
	Day            time.Time
	Value          float64
	Coverage       float64
	Incomplete     bool
	Quality        Quality
	BaselineMedian *float64
	ZScore         *float64
	Flags          []Flag
}

// Pipeline thresholds. VerifiedCoverage and BaselineDays come from the
// Agritelem spec; the rest are engineering choices documented in
// docs/pipeline.md.
const (
	HoursPerDay      = 24
	VerifiedCoverage = 0.85
	MinimumCoverage  = 0.5
	BaselineDays     = 14
	MinBaselineDays  = 7

	// madToSigma scales the median absolute deviation to estimate the
	// standard deviation of normally distributed data.
	madToSigma = 1.4826

	HeatRuminationZ      = -1.5
	HeatStepZ            = 2.0
	ThermalStressCelsius = 39.5
	MinutesPerDay        = 24 * 60
)

type dayMetric struct {
	day    time.Time
	metric domain.Metric
}

// Derive applies steps 2-5 of the pipeline to one animal's daily aggregates
// and returns derived statistics for the UTC days from..to inclusive, ordered
// by day then metric. Aggregates before from are used only as baseline
// history. An error means the aggregates violate the store's invariants.
func Derive(aggs []DailyAggregate, from, to time.Time) ([]DailyStatistic, error) {
	// Steps 2 and 5: coverage, imputation and the daily value.
	days := make(map[dayMetric]*DailyStatistic, len(aggs))
	for _, a := range aggs {
		s, err := rollup(a)
		if err != nil {
			return nil, err
		}
		k := dayMetric{a.Day, a.Metric}
		if _, dup := days[k]; dup {
			return nil, fmt.Errorf("duplicate aggregate for %s on %s", a.Metric.ID, a.Day.Format(time.DateOnly))
		}
		if s != nil {
			days[k] = s
		}
	}

	// Step 3: robust baseline against the trailing 14 days.
	for k, s := range days {
		var history []float64
		for back := 1; back <= BaselineDays; back++ {
			if prior, ok := days[dayMetric{k.day.AddDate(0, 0, -back), k.metric}]; ok {
				history = append(history, prior.Value)
			}
		}
		if len(history) < MinBaselineDays {
			continue
		}
		med := median(history)
		s.BaselineMedian = &med
		deviations := make([]float64, len(history))
		for i, v := range history {
			deviations[i] = abs(v - med)
		}
		if mad := median(deviations); mad > 0 {
			z := (s.Value - med) / (madToSigma * mad)
			s.ZScore = &z
		}
	}

	// Step 4: range and cross-metric checks, for the requested days only.
	var out []DailyStatistic
	for k, s := range days {
		if k.day.Before(from) || k.day.After(to) {
			continue
		}
		flag(days, k)
		out = append(out, *s)
	}
	slices.SortFunc(out, func(a, b DailyStatistic) int {
		return cmp.Or(a.Day.Compare(b.Day), cmp.Compare(a.Metric.Scheme, b.Metric.Scheme), cmp.Compare(a.Metric.ID, b.Metric.ID))
	})
	return out, nil
}

// rollup returns the daily value for a, or nil if coverage is too low to
// estimate one.
func rollup(a DailyAggregate) (*DailyStatistic, error) {
	spec, err := domain.LookupMetric(a.Metric)
	if err != nil {
		return nil, fmt.Errorf("aggregate on %s: %w", a.Day.Format(time.DateOnly), err)
	}
	if a.Hours < 1 || a.Hours > HoursPerDay {
		return nil, fmt.Errorf("aggregate for %s on %s has %d hours, want 1..%d", a.Metric.ID, a.Day.Format(time.DateOnly), a.Hours, HoursPerDay)
	}
	if !a.Day.Equal(a.Day.Truncate(HoursPerDay*time.Hour)) || a.Day.Location() != time.UTC {
		return nil, fmt.Errorf("aggregate day %s is not midnight UTC", a.Day.Format(time.RFC3339))
	}

	coverage := float64(a.Hours) / HoursPerDay
	if coverage < MinimumCoverage {
		return nil, nil
	}
	s := &DailyStatistic{
		Metric:     a.Metric,
		Day:        a.Day,
		Coverage:   coverage,
		Incomplete: a.Hours < HoursPerDay,
		Quality:    QualityVerified,
	}
	if coverage < VerifiedCoverage {
		s.Quality = QualityImputed
	}
	switch spec.Aggregation {
	case domain.AggregationSum:
		// Impute missing hours at the observed hourly mean.
		s.Value = a.Sum * HoursPerDay / float64(a.Hours)
	case domain.AggregationAverage:
		s.Value = a.Sum / float64(a.Hours)
	default:
		return nil, fmt.Errorf("metric %s has unknown aggregation %q", a.Metric.ID, spec.Aggregation)
	}
	return s, nil
}

func flag(days map[dayMetric]*DailyStatistic, k dayMetric) {
	s := days[k]
	other := func(m domain.Metric) *DailyStatistic { return days[dayMetric{k.day, m}] }

	if spec, err := domain.LookupMetric(s.Metric); err == nil && !spec.Daily.Contains(s.Value) {
		s.Flags = append(s.Flags, FlagDailyOutOfRange)
	}

	switch s.Metric {
	case domain.RuminationTime, domain.StepCount:
		rum, steps := other(domain.RuminationTime), other(domain.StepCount)
		if rum != nil && steps != nil && rum.ZScore != nil && steps.ZScore != nil &&
			*rum.ZScore <= HeatRuminationZ && *steps.ZScore >= HeatStepZ {
			s.Flags = append(s.Flags, FlagHeatSignature)
		}
	case domain.EatingTime, domain.LyingTime:
		eat, lie := other(domain.EatingTime), other(domain.LyingTime)
		if eat != nil && lie != nil && eat.Value+lie.Value > MinutesPerDay {
			s.Flags = append(s.Flags, FlagTimeBudgetConflict)
		}
	case domain.ReticularTemperature:
		if s.Value >= ThermalStressCelsius {
			s.Flags = append(s.Flags, FlagThermalStress)
		}
	}
}

func median(values []float64) float64 {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	n := len(sorted)
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
