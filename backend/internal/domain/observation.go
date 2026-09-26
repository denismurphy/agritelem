package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"slices"
	"strconv"
	"time"
)

// Meta is ICAR icarMetaDataType provenance, after validation.
type Meta struct {
	Source   string
	SourceID string
	Creator  string
	Modified time.Time
}

// Observation is one hourly statistic for one metric.
type Observation struct {
	Metric Metric
	Start  time.Time // UTC, aligned to the hour
	Value  float64
}

// SummarySubmission is a validated ICAR observation summary resource: hourly
// observations for one animal from one source record.
type SummarySubmission struct {
	Animal       AnimalID
	Meta         Meta
	Observations []Observation
}

// ContentHash identifies the submission's content for idempotency. It covers
// the animal, source, source ID and observations (in any order), and
// deliberately excludes meta.modified and meta.creator: a client retrying
// after a lost response may restamp those without changing the data.
func (s SummarySubmission) ContentHash() [sha256.Size]byte {
	obs := slices.Clone(s.Observations)
	slices.SortFunc(obs, func(a, b Observation) int {
		if c := compareMetric(a.Metric, b.Metric); c != 0 {
			return c
		}
		return a.Start.Compare(b.Start)
	})

	h := sha256.New()
	field := func(v string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(v)))
		h.Write(n[:])
		h.Write([]byte(v))
	}
	field(s.Animal.Scheme)
	field(s.Animal.ID)
	field(s.Meta.Source)
	field(s.Meta.SourceID)
	for _, o := range obs {
		value := o.Value
		if value == 0 {
			value = 0 // fold -0 into 0 so equal values hash equally
		}
		field(o.Metric.Scheme)
		field(o.Metric.ID)
		field(o.Start.UTC().Format(time.RFC3339))
		field(strconv.FormatFloat(value, 'g', -1, 64))
	}
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

func compareMetric(a, b Metric) int {
	if a.Scheme != b.Scheme {
		if a.Scheme < b.Scheme {
			return -1
		}
		return 1
	}
	switch {
	case a.ID < b.ID:
		return -1
	case a.ID > b.ID:
		return 1
	default:
		return 0
	}
}
