package domain

import (
	"math"
	"testing"
	"time"
)

func submission() SummarySubmission {
	start := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	return SummarySubmission{
		Animal: AnimalID{SchemeISO11785, "372000012345678"},
		Meta:   Meta{Source: "ie.example.gateway", SourceID: "abc", Modified: start.Add(time.Hour)},
		Observations: []Observation{
			{RuminationTime, start, 31},
			{StepCount, start, 120},
		},
	}
}

func TestContentHash(t *testing.T) {
	base := submission().ContentHash()

	same := []struct {
		name   string
		mutate func(*SummarySubmission)
	}{
		{"observation order", func(s *SummarySubmission) {
			s.Observations[0], s.Observations[1] = s.Observations[1], s.Observations[0]
		}},
		{"meta.modified restamped on retry", func(s *SummarySubmission) { s.Meta.Modified = s.Meta.Modified.Add(time.Minute) }},
		{"meta.creator", func(s *SummarySubmission) { s.Meta.Creator = "someone" }},
		{"same instant in another location", func(s *SummarySubmission) {
			s.Observations[0].Start = s.Observations[0].Start.In(time.FixedZone("CET", 3600))
		}},
	}
	for _, tt := range same {
		s := submission()
		tt.mutate(&s)
		if s.ContentHash() != base {
			t.Errorf("%s changed the hash", tt.name)
		}
	}

	negZero := submission()
	negZero.Observations[0].Value = 0
	posZero := negZero
	posZero.Observations = append([]Observation(nil), negZero.Observations...)
	negZero.Observations[0].Value = math.Copysign(0, -1)
	if negZero.ContentHash() != posZero.ContentHash() {
		t.Error("-0 and 0 hash differently")
	}

	different := []struct {
		name   string
		mutate func(*SummarySubmission)
	}{
		{"value", func(s *SummarySubmission) { s.Observations[0].Value = 31.0000001 }},
		{"start", func(s *SummarySubmission) { s.Observations[0].Start = s.Observations[0].Start.Add(time.Hour) }},
		{"metric", func(s *SummarySubmission) { s.Observations[0].Metric = EatingTime }},
		{"animal", func(s *SummarySubmission) { s.Animal.ID = "372000012345679" }},
		{"source", func(s *SummarySubmission) { s.Meta.Source = "other" }},
		{"sourceId", func(s *SummarySubmission) { s.Meta.SourceID = "abd" }},
		{"dropped observation", func(s *SummarySubmission) { s.Observations = s.Observations[:1] }},
		{"field boundary shift", func(s *SummarySubmission) { s.Meta.Source, s.Meta.SourceID = "ie.example.gatewaya", "bc" }},
	}
	for _, tt := range different {
		s := submission()
		tt.mutate(&s)
		if s.ContentHash() == base {
			t.Errorf("changing %s did not change the hash", tt.name)
		}
	}
}
