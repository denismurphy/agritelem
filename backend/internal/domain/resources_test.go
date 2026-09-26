package domain

import (
	"slices"
	"strings"
	"testing"
	"time"
)

var (
	remarkNow = time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	farm      = Location{Scheme: "dk.herdnumber", ID: "123456"}
)

func sp(s string) *string       { return &s }
func tp(t time.Time) *time.Time { return &t }

func validRawRemark() RawRemark {
	return RawRemark{
		AnimalScheme:  SchemeISO11785,
		AnimalID:      "372000012345678",
		EventDateTime: tp(remarkNow.Add(-time.Hour)),
		Note:          sp("Lame on left hind"),
		Category:      sp("Veterinary"),
		Source:        sp("ie.example.app"),
		SourceID:      sp("0192d7c4-7f5e-7a3b-9c1d-000000000001"),
		Modified:      tp(remarkNow.Add(-time.Hour)),
	}
}

func remarkCodes(ps []*Problem) []Code {
	var out []Code
	for _, p := range ps {
		out = append(out, p.Code)
	}
	slices.Sort(out)
	return out
}

func TestValidateRemark(t *testing.T) {
	r, problems := ValidateRemark(validRawRemark(), farm, "ie.example.app", remarkNow, 5*time.Minute)
	if problems != nil {
		t.Fatalf("unexpected problems %v", problems)
	}
	if r.Category != RemarkVeterinary || r.Meta.SourceID == "" || r.Location != farm {
		t.Errorf("got %+v", r)
	}

	defaulted := validRawRemark()
	defaulted.Category = nil
	defaulted.Modified = nil
	r, _ = ValidateRemark(defaulted, farm, "ie.example.app", remarkNow, 5*time.Minute)
	if r.Category != RemarkGeneral || !r.Meta.Modified.Equal(remarkNow) {
		t.Errorf("defaults: category %q modified %v", r.Category, r.Meta.Modified)
	}

	tests := []struct {
		name   string
		mutate func(*RawRemark)
		want   []Code
	}{
		{"bad animal", func(r *RawRemark) { r.AnimalID = "1" }, []Code{CodeInvalidAnimalID}},
		{"missing event time", func(r *RawRemark) { r.EventDateTime = nil }, []Code{CodeMissingField}},
		{"event time with offset", func(r *RawRemark) {
			r.EventDateTime = tp(remarkNow.In(time.FixedZone("", -3600)))
		}, []Code{CodeNonUTCTimestamp}},
		{"event in the future", func(r *RawRemark) { r.EventDateTime = tp(remarkNow.Add(time.Hour)) }, []Code{CodeFutureTimestamp}},
		{"blank note", func(r *RawRemark) { r.Note = sp(" \n ") }, []Code{CodeMissingField}},
		{"overlong note", func(r *RawRemark) { r.Note = sp(strings.Repeat("é", MaxRemarkNoteRunes+1)) }, []Code{CodeInvalidValue}},
		{"note at limit", func(r *RawRemark) { r.Note = sp(strings.Repeat("é", MaxRemarkNoteRunes)) }, nil},
		{"invalid UTF-8", func(r *RawRemark) { r.Note = sp("ok \xff") }, []Code{CodeInvalidValue}},
		{"unknown category", func(r *RawRemark) { r.Category = sp("Gossip") }, []Code{CodeInvalidValue}},
		{"missing meta", func(r *RawRemark) { r.Source = nil }, []Code{CodeMissingField}},
		{"spoofed source", func(r *RawRemark) { r.Source = sp("ie.other") }, []Code{CodeSourceMismatch}},
		{"missing sourceId", func(r *RawRemark) { r.SourceID = nil }, []Code{CodeSourceIDRequired}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := validRawRemark()
			tt.mutate(&raw)
			_, problems := ValidateRemark(raw, farm, "ie.example.app", remarkNow, 5*time.Minute)
			if got := remarkCodes(problems); !slices.Equal(got, tt.want) {
				t.Errorf("codes = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRemarkContentHash(t *testing.T) {
	base, _ := ValidateRemark(validRawRemark(), farm, "ie.example.app", remarkNow, 5*time.Minute)
	restamped := base
	restamped.Meta.Modified = restamped.Meta.Modified.Add(time.Minute)
	restamped.ID = "server-assigned"
	if restamped.ContentHash() != base.ContentHash() {
		t.Error("meta.modified or ID changed the hash")
	}
	for name, mutate := range map[string]func(*Remark){
		"note":       func(r *Remark) { r.Note += "!" },
		"category":   func(r *Remark) { r.Category = RemarkGeneral },
		"event time": func(r *Remark) { r.EventDateTime = r.EventDateTime.Add(time.Second) },
		// "0" is the encoding of a nil responsible without the presence prefix.
		"responsible": func(r *Remark) { r.Responsible = sp("0") },
		"animal":      func(r *Remark) { r.Animal.ID = "372000012345679" },
	} {
		changed := base
		mutate(&changed)
		if changed.ContentHash() == base.ContentHash() {
			t.Errorf("changing %s did not change the hash", name)
		}
	}
}
