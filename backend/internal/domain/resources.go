package domain

import (
	"crypto/sha256"
	"encoding/binary"
	"strings"
	"time"
	"unicode/utf8"
)

// Location is an ICAR location (farm or herd) identifier.
type Location struct {
	Scheme string
	ID     string
}

// Animal is an ICAR icarAnimalCoreResource as stored.
type Animal struct {
	ID            AnimalID
	Location      Location
	Specie        string
	Gender        string
	BirthDate     *time.Time
	Name          *string
	ManagementTag *string
	Breed         *Breed
	Status        string
	Meta          Meta
	Deleted       bool
}

// SchemeICARBreed3 is the ICAR 3-character breed code scheme
// (well-known/icarBreedIdentifierType.md), e.g. HOL for Holstein.
const SchemeICARBreed3 = "icar.breed-3"

// Breed is an ICAR icarBreedIdentifierType, e.g. {icar.breed-3, HOL}.
type Breed struct {
	Scheme string
	ID     string
}

// Device is an ICAR icarDeviceResource with its assignment at a given time.
type Device struct {
	ID         string
	Serial     *string
	Name       *string
	Modified   time.Time
	Assignment *DeviceAssignment
}

// DeviceAssignment attaches a device to an animal for [ValidFrom, ValidTo).
// ValidTo is nil while the assignment is open-ended.
type DeviceAssignment struct {
	Animal    AnimalID
	ValidFrom time.Time
	ValidTo   *time.Time
}

// RemarkCategory classifies a remark (Agritelem extension to ICAR).
type RemarkCategory string

// Remark categories.
const (
	RemarkGeneral    RemarkCategory = "General"
	RemarkVeterinary RemarkCategory = "Veterinary"
	RemarkBehaviour  RemarkCategory = "Behaviour"
	RemarkFeeding    RemarkCategory = "Feeding"
)

// Remark limits.
const (
	MaxRemarkNoteRunes        = 2000
	MaxRemarkResponsibleRunes = 200
)

// Remark is an ICAR icarRemarkEventResource. ID and Meta.Modified are
// assigned by the server.
type Remark struct {
	ID            string
	Location      Location
	Animal        AnimalID
	EventDateTime time.Time
	Note          string
	Category      RemarkCategory
	Responsible   *string
	Meta          Meta
}

// RawRemark is a remark as received, before validation.
type RawRemark struct {
	AnimalScheme  string
	AnimalID      string
	EventDateTime *time.Time
	Note          *string
	Category      *string
	Responsible   *string
	Source        *string
	SourceID      *string
	Creator       *string
	Modified      *time.Time
}

// ValidateRemark checks a remark submitted by principal at server time now.
// It returns every problem found.
func ValidateRemark(raw RawRemark, loc Location, principal string, now time.Time, maxSkew time.Duration) (Remark, []*Problem) {
	var problems []*Problem
	add := func(err error) {
		if p := AsProblem(err, ""); p != nil {
			problems = append(problems, p)
		}
	}
	r := Remark{Location: loc, Category: RemarkGeneral}

	animal, err := ParseAnimalID(raw.AnimalScheme, raw.AnimalID)
	if p := AsProblem(err, "/animal"); p != nil {
		problems = append(problems, p)
	}
	r.Animal = animal

	switch {
	case raw.EventDateTime == nil:
		add(problemf(CodeMissingField, "/eventDateTime", "eventDateTime is required"))
	case RequireUTC("/eventDateTime", *raw.EventDateTime) != nil:
		add(RequireUTC("/eventDateTime", *raw.EventDateTime))
	case raw.EventDateTime.After(now.Add(maxSkew)):
		add(problemf(CodeFutureTimestamp, "/eventDateTime", "eventDateTime %s is in the future", raw.EventDateTime.Format(time.RFC3339)))
	default:
		r.EventDateTime = raw.EventDateTime.UTC()
	}

	switch {
	case raw.Note == nil || strings.TrimSpace(*raw.Note) == "":
		add(problemf(CodeMissingField, "/note", "note is required"))
	case utf8.RuneCountInString(*raw.Note) > MaxRemarkNoteRunes:
		add(problemf(CodeInvalidValue, "/note", "note exceeds %d characters", MaxRemarkNoteRunes))
	case !utf8.ValidString(*raw.Note):
		add(problemf(CodeInvalidValue, "/note", "note is not valid UTF-8"))
	default:
		r.Note = *raw.Note
	}

	if raw.Category != nil {
		switch c := RemarkCategory(*raw.Category); c {
		case RemarkGeneral, RemarkVeterinary, RemarkBehaviour, RemarkFeeding:
			r.Category = c
		default:
			add(problemf(CodeInvalidValue, "/agritelem/category", "unknown remark category %q", *raw.Category))
		}
	}
	if raw.Responsible != nil {
		if utf8.RuneCountInString(*raw.Responsible) > MaxRemarkResponsibleRunes {
			add(problemf(CodeInvalidValue, "/responsible", "responsible exceeds %d characters", MaxRemarkResponsibleRunes))
		} else {
			r.Responsible = raw.Responsible
		}
	}

	switch {
	case raw.Source == nil:
		add(problemf(CodeMissingField, "/meta", "meta with source and sourceId is required"))
	case *raw.Source != principal:
		add(problemf(CodeSourceMismatch, "/meta/source", "meta.source %q does not match the authenticated source %q", *raw.Source, principal))
	default:
		r.Meta.Source = *raw.Source
	}
	if raw.SourceID == nil || strings.TrimSpace(*raw.SourceID) == "" {
		add(problemf(CodeSourceIDRequired, "/meta/sourceId", "meta.sourceId is required so that retries are idempotent"))
	} else {
		r.Meta.SourceID = *raw.SourceID
	}
	if raw.Creator != nil {
		r.Meta.Creator = *raw.Creator
	}
	if raw.Modified != nil {
		if err := RequireUTC("/meta/modified", *raw.Modified); err != nil {
			add(err)
		} else {
			r.Meta.Modified = raw.Modified.UTC()
		}
	} else {
		r.Meta.Modified = now
	}

	if len(problems) > 0 {
		return Remark{}, problems
	}
	return r, nil
}

// ContentHash identifies a remark's content for idempotency, excluding the
// server-assigned ID and the client's meta.modified and meta.creator.
func (r Remark) ContentHash() [sha256.Size]byte {
	h := sha256.New()
	field := func(v string) {
		var n [8]byte
		binary.BigEndian.PutUint64(n[:], uint64(len(v)))
		h.Write(n[:])
		h.Write([]byte(v))
	}
	field(r.Location.Scheme)
	field(r.Location.ID)
	field(r.Animal.Scheme)
	field(r.Animal.ID)
	field(r.EventDateTime.UTC().Format(time.RFC3339Nano))
	field(r.Note)
	field(string(r.Category))
	if r.Responsible != nil {
		field("1" + *r.Responsible)
	} else {
		field("0")
	}
	field(r.Meta.Source)
	field(r.Meta.SourceID)
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))
	return sum
}
