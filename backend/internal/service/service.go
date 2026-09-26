// Package service implements Agritelem's use cases on top of the store and
// the cleaning pipeline. It knows nothing about HTTP.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/denismurphy/agritelem/backend/internal/domain"
	"github.com/denismurphy/agritelem/backend/internal/pipeline"
	"github.com/denismurphy/agritelem/backend/internal/store"
)

// Store is the persistence the service needs; *store.Store implements it.
type Store interface {
	Ingest(ctx context.Context, loc domain.Location, subs []domain.SummarySubmission, rejected []store.Rejection) ([]store.IngestOutcome, error)
	ActiveAnimals(ctx context.Context, loc domain.Location, ids []domain.AnimalID) (map[domain.AnimalID]bool, error)
	ActiveAnimalsAt(ctx context.Context, loc domain.Location) ([]domain.Animal, error)
	DailyAggregates(ctx context.Context, animal domain.AnimalID, from, to time.Time) ([]pipeline.DailyAggregate, error)
	LocationDailyAggregates(ctx context.Context, loc domain.Location, from, to time.Time) (map[domain.AnimalID][]pipeline.DailyAggregate, error)
	HourlyObservations(ctx context.Context, animal domain.AnimalID, from, to time.Time) ([]domain.Observation, error)
	ListAnimals(ctx context.Context, loc domain.Location, from time.Time, after store.Cursor, limit int) ([]domain.Animal, error)
	ListDevices(ctx context.Context, loc domain.Location, from, at time.Time) ([]domain.Device, error)
	CreateRemark(ctx context.Context, r domain.Remark) (store.RemarkStatus, domain.Remark, error)
	ListRemarks(ctx context.Context, loc domain.Location, from time.Time, animal *domain.AnimalID, after store.Cursor, limit int) ([]domain.Remark, error)
	AnimalsWithRemarkSince(ctx context.Context, loc domain.Location, category domain.RemarkCategory, since time.Time) (map[domain.AnimalID]bool, error)
}

// Limits on read requests.
const (
	MaxSummaryDays         = 366
	DefaultSummaryDays     = 30
	VeterinaryAttentionFor = 7 * 24 * time.Hour
)

// ErrNotFound means the requested resource does not exist at the location.
var ErrNotFound = errors.New("not found")

// Service implements Agritelem's use cases.
type Service struct {
	store Store
	now   func() time.Time
}

// New returns a Service. now is the clock; pass time.Now outside tests.
func New(s Store, now func() time.Time) *Service {
	return &Service{store: s, now: func() time.Time { return now().UTC() }}
}

// IngestStatus extends store.IngestStatus with validation rejection.
type IngestStatus int

// Ingest result statuses.
const (
	IngestAccepted IngestStatus = iota + 1
	IngestReplayed
	IngestConflict
	IngestRejected
)

// IngestResult is the outcome for one submitted resource, in request order.
type IngestResult struct {
	Status     IngestStatus
	ResourceID string // empty when rejected
	Problems   []*domain.Problem
}

// RawResource is one submitted resource: parsed fields plus the original
// JSON, which is quarantined if the resource is rejected.
type RawResource struct {
	Summary pipeline.RawSummary
	JSON    json.RawMessage
}

// IngestSummaries validates and stores a batch of hourly observation
// resources submitted by principal for loc.
func (s *Service) IngestSummaries(ctx context.Context, principal string, loc domain.Location, raws []RawResource) ([]IngestResult, error) {
	now := s.now()
	results := make([]IngestResult, len(raws))
	valid := make([]domain.SummarySubmission, 0, len(raws))
	validIndex := make([]int, 0, len(raws))
	var ids []domain.AnimalID

	for i, raw := range raws {
		sub, problems := pipeline.Validate(raw.Summary, principal, now)
		if len(problems) > 0 {
			results[i] = IngestResult{Status: IngestRejected, Problems: problems}
			continue
		}
		valid = append(valid, sub)
		validIndex = append(validIndex, i)
		ids = append(ids, sub.Animal)
	}

	active, err := s.store.ActiveAnimals(ctx, loc, ids)
	if err != nil {
		return nil, err
	}
	accepted := valid[:0]
	acceptedIndex := make([]int, 0, len(valid))
	for n, sub := range valid {
		if !active[sub.Animal] {
			results[validIndex[n]] = IngestResult{Status: IngestRejected, Problems: []*domain.Problem{{
				Code: domain.CodeUnknownAnimal, Field: "/animal",
				Detail: fmt.Sprintf("animal %s/%s is not registered at location %s/%s", sub.Animal.Scheme, sub.Animal.ID, loc.Scheme, loc.ID),
			}}}
			continue
		}
		accepted = append(accepted, sub)
		acceptedIndex = append(acceptedIndex, validIndex[n])
	}

	var rejected []store.Rejection
	for i, r := range results {
		if r.Status == IngestRejected {
			rejected = append(rejected, store.Rejection{Principal: principal, Resource: raws[i].JSON, Problems: r.Problems})
		}
	}

	outcomes, err := s.store.Ingest(ctx, loc, accepted, rejected)
	if err != nil {
		return nil, err
	}
	for n, o := range outcomes {
		r := IngestResult{ResourceID: o.ResourceID.String()}
		switch o.Status {
		case store.Accepted:
			r.Status = IngestAccepted
		case store.Replayed:
			r.Status = IngestReplayed
		case store.Conflict:
			r.Status = IngestConflict
			r.Problems = []*domain.Problem{{Code: domain.CodeConflictingResubmission, Field: "/meta/sourceId",
				Detail: "this source and sourceId were already stored with different content; submit corrections under a new sourceId"}}
		default:
			return nil, fmt.Errorf("store returned unknown ingest status %d", o.Status)
		}
		results[acceptedIndex[n]] = r
	}
	return results, nil
}

// SummaryQuery selects derived or hourly statistics for one animal.
type SummaryQuery struct {
	Animal   domain.AnimalID
	From, To *time.Time // UTC dates, inclusive; defaults to the last DefaultSummaryDays days
	Duration string     // "P1D" or "PT1H"
	Metric   *domain.Metric
}

// Summary is one animal's statistics for a query.
type Summary struct {
	Animal domain.AnimalID
	Daily  []pipeline.DailyStatistic
	Hourly []domain.Observation
}

// Summaries returns daily statistics (derived by pipeline steps 2-5) or the
// latest hourly values for one animal at loc.
func (s *Service) Summaries(ctx context.Context, loc domain.Location, q SummaryQuery) (Summary, error) {
	to := s.now().Truncate(24 * time.Hour)
	if q.To != nil {
		to = q.To.UTC().Truncate(24 * time.Hour)
	}
	from := to.AddDate(0, 0, -(DefaultSummaryDays - 1))
	if q.From != nil {
		from = q.From.UTC().Truncate(24 * time.Hour)
	}
	switch days := int(to.Sub(from).Hours()/24) + 1; {
	case days < 1:
		return Summary{}, &domain.Problem{Code: domain.CodeInvalidValue, Field: "date-from", Detail: "date-from is after date-to"}
	case days > MaxSummaryDays:
		return Summary{}, &domain.Problem{Code: domain.CodeInvalidValue, Field: "date-from",
			Detail: fmt.Sprintf("the range covers %d days; at most %d are allowed", days, MaxSummaryDays)}
	}

	active, err := s.store.ActiveAnimals(ctx, loc, []domain.AnimalID{q.Animal})
	if err != nil {
		return Summary{}, err
	}
	if !active[q.Animal] {
		return Summary{}, ErrNotFound
	}

	end := to.AddDate(0, 0, 1)
	out := Summary{Animal: q.Animal}
	switch q.Duration {
	case "P1D":
		aggs, err := s.store.DailyAggregates(ctx, q.Animal, from.AddDate(0, 0, -pipeline.BaselineDays), end)
		if err != nil {
			return Summary{}, err
		}
		daily, err := pipeline.Derive(aggs, from, to)
		if err != nil {
			return Summary{}, fmt.Errorf("derive statistics for %s: %w", q.Animal.ID, err)
		}
		for _, d := range daily {
			if q.Metric == nil || d.Metric == *q.Metric {
				out.Daily = append(out.Daily, d)
			}
		}
	case pipeline.HourlyDuration:
		hourly, err := s.store.HourlyObservations(ctx, q.Animal, from, end)
		if err != nil {
			return Summary{}, err
		}
		for _, h := range hourly {
			if q.Metric == nil || h.Metric == *q.Metric {
				out.Hourly = append(out.Hourly, h)
			}
		}
	default:
		return Summary{}, &domain.Problem{Code: domain.CodeUnsupportedDuration, Field: "duration",
			Detail: fmt.Sprintf("duration %q is not supported; use P1D or PT1H", q.Duration)}
	}
	return out, nil
}

// AnimalStatus is the dashboard status of one animal.
type AnimalStatus string

// Animal statuses.
const (
	StatusNormal              AnimalStatus = "Normal"
	StatusInHeat              AnimalStatus = "InHeat"
	StatusVeterinaryAttention AnimalStatus = "VeterinaryAttention"
	StatusHeatStress          AnimalStatus = "HeatStress"
)

// HerdAnimal is one row of the herd overview.
type HerdAnimal struct {
	Animal            domain.Animal
	Statuses          []AnimalStatus
	RuminationMinutes *float64
}

// HerdOverview summarises a location for the most recent complete UTC day.
type HerdOverview struct {
	AsOf                  time.Time
	Day                   time.Time
	MeanRuminationMinutes *float64
	EstrusAlerts          int
	HeatStress            int
	VeterinaryAttention   int
	Animals               []HerdAnimal
}

// HerdOverview derives every active animal's status at loc.
func (s *Service) HerdOverview(ctx context.Context, loc domain.Location) (HerdOverview, error) {
	now := s.now()
	day := now.Truncate(24*time.Hour).AddDate(0, 0, -1)
	animals, err := s.store.ActiveAnimalsAt(ctx, loc)
	if err != nil {
		return HerdOverview{}, err
	}
	aggs, err := s.store.LocationDailyAggregates(ctx, loc, day.AddDate(0, 0, -pipeline.BaselineDays), day.AddDate(0, 0, 1))
	if err != nil {
		return HerdOverview{}, err
	}
	vets, err := s.store.AnimalsWithRemarkSince(ctx, loc, domain.RemarkVeterinary, now.Add(-VeterinaryAttentionFor))
	if err != nil {
		return HerdOverview{}, err
	}

	out := HerdOverview{AsOf: now, Day: day, Animals: make([]HerdAnimal, 0, len(animals))}
	var ruminationTotal float64
	var ruminationCount int
	for _, a := range animals {
		daily, err := pipeline.Derive(aggs[a.ID], day, day)
		if err != nil {
			return HerdOverview{}, fmt.Errorf("derive statistics for %s: %w", a.ID.ID, err)
		}
		row := HerdAnimal{Animal: a}
		flags := map[pipeline.Flag]bool{}
		for _, d := range daily {
			for _, f := range d.Flags {
				flags[f] = true
			}
			if d.Metric == domain.RuminationTime && d.Quality == pipeline.QualityVerified {
				v := d.Value
				row.RuminationMinutes = &v
				ruminationTotal += v
				ruminationCount++
			}
		}
		if flags[pipeline.FlagHeatSignature] {
			row.Statuses = append(row.Statuses, StatusInHeat)
			out.EstrusAlerts++
		}
		if flags[pipeline.FlagThermalStress] {
			row.Statuses = append(row.Statuses, StatusHeatStress)
			out.HeatStress++
		}
		if vets[a.ID] {
			row.Statuses = append(row.Statuses, StatusVeterinaryAttention)
			out.VeterinaryAttention++
		}
		if len(row.Statuses) == 0 {
			row.Statuses = []AnimalStatus{StatusNormal}
		}
		out.Animals = append(out.Animals, row)
	}
	if ruminationCount > 0 {
		mean := ruminationTotal / float64(ruminationCount)
		out.MeanRuminationMinutes = &mean
	}
	return out, nil
}

// ListAnimals returns a page of animals modified at or after from.
func (s *Service) ListAnimals(ctx context.Context, loc domain.Location, from time.Time, after store.Cursor, limit int) ([]domain.Animal, error) {
	return s.store.ListAnimals(ctx, loc, from, after, limit)
}

// ListDevices returns the devices at loc with their current assignments.
func (s *Service) ListDevices(ctx context.Context, loc domain.Location, from time.Time) ([]domain.Device, error) {
	return s.store.ListDevices(ctx, loc, from, s.now())
}

// ListRemarks returns a page of remarks modified at or after from.
func (s *Service) ListRemarks(ctx context.Context, loc domain.Location, from time.Time, animal *domain.AnimalID, after store.Cursor, limit int) ([]domain.Remark, error) {
	return s.store.ListRemarks(ctx, loc, from, animal, after, limit)
}

// RemarkResult is the outcome of creating a remark.
type RemarkResult struct {
	Status store.RemarkStatus
	Remark domain.Remark
}

// CreateRemark validates and stores a remark submitted by principal. Invalid
// input is reported through problems with a nil error; err is reserved for
// failures the client cannot fix.
func (s *Service) CreateRemark(ctx context.Context, principal string, loc domain.Location, raw domain.RawRemark) (RemarkResult, []*domain.Problem, error) {
	remark, problems := domain.ValidateRemark(raw, loc, principal, s.now(), pipeline.MaxClockSkew)
	if len(problems) > 0 {
		return RemarkResult{}, problems, nil
	}
	active, err := s.store.ActiveAnimals(ctx, loc, []domain.AnimalID{remark.Animal})
	if err != nil {
		return RemarkResult{}, nil, err
	}
	if !active[remark.Animal] {
		return RemarkResult{}, []*domain.Problem{{Code: domain.CodeUnknownAnimal, Field: "/animal",
			Detail: fmt.Sprintf("animal %s/%s is not registered at location %s/%s", remark.Animal.Scheme, remark.Animal.ID, loc.Scheme, loc.ID)}}, nil
	}
	status, stored, err := s.store.CreateRemark(ctx, remark)
	if err != nil {
		return RemarkResult{}, nil, err
	}
	return RemarkResult{Status: status, Remark: stored}, nil, nil
}
