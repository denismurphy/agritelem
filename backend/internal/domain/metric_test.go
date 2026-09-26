package domain

import (
	"errors"
	"math"
	"testing"
	"time"
)

func TestRangeContains(t *testing.T) {
	r := Range{Min: 0, Max: 60}
	tests := []struct {
		v    float64
		want bool
	}{
		{0, true}, {60, true}, {30.5, true},
		{-0.001, false}, {60.001, false},
		{math.NaN(), false}, {math.Inf(1), false}, {math.Inf(-1), false},
	}
	for _, tt := range tests {
		if got := r.Contains(tt.v); got != tt.want {
			t.Errorf("Contains(%v) = %v, want %v", tt.v, got, tt.want)
		}
	}
}

func TestLookupMetric(t *testing.T) {
	spec, err := LookupMetric(RuminationTime)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Daily != (Range{0, 720}) {
		t.Errorf("rumination daily bound = %+v, want the spec's 0-720 min/day", spec.Daily)
	}

	_, err = LookupMetric(Metric{SchemeICARActivities, "ActivityIndex"})
	var p *Problem
	if !errors.As(err, &p) || p.Code != CodeUnsupportedMetric {
		t.Errorf("err = %v, want %s", err, CodeUnsupportedMetric)
	}
}

// TestCatalogueIsCoherent guards the bounds table against edits that make a
// daily total impossible to reach or exceed from valid hours.
func TestCatalogueIsCoherent(t *testing.T) {
	for _, spec := range Metrics() {
		name := spec.Metric.ID
		if spec.Unit == "" || spec.Aggregation == "" {
			t.Errorf("%s: missing unit or aggregation", name)
		}
		if spec.Hourly.Min > spec.Hourly.Max || spec.Daily.Min > spec.Daily.Max {
			t.Errorf("%s: inverted range", name)
		}
		if spec.Aggregation == AggregationSum && spec.Daily.Max > 24*spec.Hourly.Max {
			t.Errorf("%s: daily max %v unreachable from 24 hours of at most %v", name, spec.Daily.Max, spec.Hourly.Max)
		}
		if spec.Unit == "MIN" && spec.Hourly.Max > 60 {
			t.Errorf("%s: more than 60 minutes in an hour", name)
		}
	}
}

func TestRequireUTC(t *testing.T) {
	tests := []struct {
		in      string
		wantErr bool
	}{
		{"2026-09-01T10:00:00Z", false},
		{"2026-09-01T10:00:00+00:00", false},
		{"2026-09-01T11:00:00+01:00", true},
		{"2026-09-01T05:30:00-04:30", true},
	}
	for _, tt := range tests {
		ts, err := time.Parse(time.RFC3339, tt.in)
		if err != nil {
			t.Fatal(err)
		}
		err = RequireUTC("/startDateTime", ts)
		var p *Problem
		if tt.wantErr != (err != nil) {
			t.Errorf("%s: err = %v, wantErr %v", tt.in, err, tt.wantErr)
		}
		if err != nil && (!errors.As(err, &p) || p.Code != CodeNonUTCTimestamp) {
			t.Errorf("%s: err = %v, want %s", tt.in, err, CodeNonUTCTimestamp)
		}
	}
}
