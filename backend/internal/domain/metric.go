package domain

import "time"

// Metric identifier schemes.
const (
	// SchemeICARActivities holds the metrics predefined by ICAR ADE in
	// well-known/icarObservationSummaryMetrics.md.
	SchemeICARActivities = "org.icar.activities"
	// SchemeAgritelemMetrics holds metrics ICAR does not define. It is a
	// project-local scheme; Agritelem owns no domain to reverse.
	SchemeAgritelemMetrics = "agritelem.metrics"
)

// Metric identifies an observation metric by ICAR scheme and ID.
type Metric struct {
	Scheme string
	ID     string
}

// Metrics understood by Agritelem.
var (
	RuminationTime       = Metric{SchemeICARActivities, "RuminationTime"}
	EatingTime           = Metric{SchemeICARActivities, "EatingTime"}
	LyingTime            = Metric{SchemeICARActivities, "LyingTime"}
	StepCount            = Metric{SchemeICARActivities, "StepCount"}
	ReticularTemperature = Metric{SchemeAgritelemMetrics, "ReticularTemperature"}
)

// Aggregation mirrors ICAR icarAggregationType.
type Aggregation string

// Aggregations used by Agritelem metrics.
const (
	AggregationSum     Aggregation = "Sum"
	AggregationAverage Aggregation = "Average"
)

// Range is an inclusive interval of plausible values.
type Range struct{ Min, Max float64 }

// Contains reports whether v lies within r. NaN compares false with
// everything and infinities fall outside any finite range, so neither is
// contained.
func (r Range) Contains(v float64) bool {
	return v >= r.Min && v <= r.Max
}

// MetricSpec describes how a metric is measured, aggregated and bounded.
type MetricSpec struct {
	Metric      Metric
	Unit        string // UN/CEFACT unit code
	Aggregation Aggregation
	Hourly      Range // physiological bounds for one hour
	Daily       Range // physiological bounds for one UTC day
}

// catalogue lists supported metrics. Only the rumination daily bound comes
// from the Agritelem spec (0-720 min/day); every other bound is an engineering
// estimate chosen to reject sensor faults, not to diagnose animals, and is
// documented as tunable in docs/pipeline.md.
var catalogue = []MetricSpec{
	{RuminationTime, "MIN", AggregationSum, Range{0, 60}, Range{0, 720}},
	{EatingTime, "MIN", AggregationSum, Range{0, 60}, Range{0, 720}},
	{LyingTime, "MIN", AggregationSum, Range{0, 60}, Range{0, 1440}},
	{StepCount, "ACT", AggregationSum, Range{0, 5000}, Range{0, 60000}},
	{ReticularTemperature, "CEL", AggregationAverage, Range{35, 43}, Range{35, 43}},
}

// LookupMetric returns the spec for m, or a Problem if m is unsupported.
func LookupMetric(m Metric) (MetricSpec, error) {
	for _, spec := range catalogue {
		if spec.Metric == m {
			return spec, nil
		}
	}
	return MetricSpec{}, problemf(CodeUnsupportedMetric, "/metric",
		"metric %s/%s is not supported", m.Scheme, m.ID)
}

// Metrics returns every supported metric spec.
func Metrics() []MetricSpec {
	return append([]MetricSpec(nil), catalogue...)
}

// RequireUTC rejects timestamps carrying a non-zero UTC offset. Agritelem
// stores and exchanges UTC only, as ICAR icarDateTimeType requires.
func RequireUTC(field string, t time.Time) error {
	if _, offset := t.Zone(); offset != 0 {
		return problemf(CodeNonUTCTimestamp, field,
			"timestamp %s has UTC offset %+ds; send UTC with a Z suffix", t.Format(time.RFC3339), offset)
	}
	return nil
}
