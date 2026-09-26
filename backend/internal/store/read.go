package store

import (
	"context"
	"fmt"
	"time"

	"github.com/denismurphy/agritelem/backend/internal/domain"
	"github.com/denismurphy/agritelem/backend/internal/pipeline"
	"github.com/denismurphy/agritelem/backend/internal/store/db"
)

// Cursor is a keyset position in a list ordered by (modified, key).
type Cursor struct {
	Modified time.Time
	Key1     string
	Key2     string
}

// DailyAggregates rolls one animal's hourly observations in [from, to) into
// UTC days, using the latest revision of each hour.
func (s *Store) DailyAggregates(ctx context.Context, animal domain.AnimalID, from, to time.Time) ([]pipeline.DailyAggregate, error) {
	rows, err := s.q.DailyAggregates(ctx, db.DailyAggregatesParams{
		AnimalScheme: animal.Scheme, AnimalID: animal.ID, FromTime: from, ToTime: to,
	})
	if err != nil {
		return nil, fmt.Errorf("daily aggregates for %s: %w", animal.ID, err)
	}
	out := make([]pipeline.DailyAggregate, len(rows))
	for i, r := range rows {
		out[i] = pipeline.DailyAggregate{
			Metric: domain.Metric{Scheme: r.MetricScheme, ID: r.MetricID},
			Day:    utc(r.Day), Hours: int(r.Hours), Sum: r.Total,
		}
	}
	return out, nil
}

// LocationDailyAggregates is DailyAggregates for every active animal at loc.
func (s *Store) LocationDailyAggregates(ctx context.Context, loc domain.Location, from, to time.Time) (map[domain.AnimalID][]pipeline.DailyAggregate, error) {
	rows, err := s.q.LocationDailyAggregates(ctx, db.LocationDailyAggregatesParams{
		LocationScheme: loc.Scheme, LocationID: loc.ID, FromTime: from, ToTime: to,
	})
	if err != nil {
		return nil, fmt.Errorf("location daily aggregates: %w", err)
	}
	out := make(map[domain.AnimalID][]pipeline.DailyAggregate)
	for _, r := range rows {
		id := domain.AnimalID{Scheme: r.AnimalScheme, ID: r.AnimalID}
		out[id] = append(out[id], pipeline.DailyAggregate{
			Metric: domain.Metric{Scheme: r.MetricScheme, ID: r.MetricID},
			Day:    utc(r.Day), Hours: int(r.Hours), Sum: r.Total,
		})
	}
	return out, nil
}

// HourlyObservations returns the latest revision of each hourly value for one
// animal in [from, to).
func (s *Store) HourlyObservations(ctx context.Context, animal domain.AnimalID, from, to time.Time) ([]domain.Observation, error) {
	rows, err := s.q.HourlyObservations(ctx, db.HourlyObservationsParams{
		AnimalScheme: animal.Scheme, AnimalID: animal.ID, FromTime: from, ToTime: to,
	})
	if err != nil {
		return nil, fmt.Errorf("hourly observations for %s: %w", animal.ID, err)
	}
	out := make([]domain.Observation, len(rows))
	for i, r := range rows {
		out[i] = domain.Observation{Metric: domain.Metric{Scheme: r.MetricScheme, ID: r.MetricID}, Start: utc(r.HourStart), Value: r.Value}
	}
	return out, nil
}

// ActiveAnimals reports which of ids exist, are not deleted, and belong to loc.
func (s *Store) ActiveAnimals(ctx context.Context, loc domain.Location, ids []domain.AnimalID) (map[domain.AnimalID]bool, error) {
	schemes := make([]string, len(ids))
	values := make([]string, len(ids))
	for i, id := range ids {
		schemes[i], values[i] = id.Scheme, id.ID
	}
	rows, err := s.q.ActiveAnimalsIn(ctx, db.ActiveAnimalsInParams{Schemes: schemes, Ids: values, LocationScheme: loc.Scheme, LocationID: loc.ID})
	if err != nil {
		return nil, fmt.Errorf("look up animals: %w", err)
	}
	out := make(map[domain.AnimalID]bool, len(rows))
	for _, r := range rows {
		out[domain.AnimalID{Scheme: r.AnimalScheme, ID: r.AnimalID}] = true
	}
	return out, nil
}

// ListAnimals returns up to limit animals at loc modified at or after from,
// strictly after the cursor, including deleted ones as tombstones.
func (s *Store) ListAnimals(ctx context.Context, loc domain.Location, from time.Time, after Cursor, limit int) ([]domain.Animal, error) {
	rows, err := s.q.ListAnimals(ctx, db.ListAnimalsParams{
		LocationScheme: loc.Scheme, LocationID: loc.ID, ModifiedFrom: from,
		AfterModified: after.Modified, AfterScheme: after.Key1, AfterID: after.Key2, PageLimit: int32(limit), //nolint:gosec // limit is bounded by the API schema
	})
	if err != nil {
		return nil, fmt.Errorf("list animals: %w", err)
	}
	out := make([]domain.Animal, len(rows))
	for i, r := range rows {
		out[i] = animalFromRow(r)
	}
	return out, nil
}

// ActiveAnimalsAt returns every non-deleted animal at loc.
func (s *Store) ActiveAnimalsAt(ctx context.Context, loc domain.Location) ([]domain.Animal, error) {
	rows, err := s.q.ActiveAnimalsAt(ctx, db.ActiveAnimalsAtParams{LocationScheme: loc.Scheme, LocationID: loc.ID})
	if err != nil {
		return nil, fmt.Errorf("list active animals: %w", err)
	}
	out := make([]domain.Animal, len(rows))
	for i, r := range rows {
		out[i] = animalFromRow(r)
	}
	return out, nil
}

func animalFromRow(r db.Animal) domain.Animal {
	return domain.Animal{
		ID:            domain.AnimalID{Scheme: r.AnimalScheme, ID: r.AnimalID},
		Location:      domain.Location{Scheme: r.LocationScheme, ID: r.LocationID},
		Specie:        r.Specie,
		Gender:        r.Gender,
		BirthDate:     utcPtr(r.BirthDate),
		Name:          r.Name,
		ManagementTag: r.ManagementTag,
		Breed:         breedFromRow(r.BreedScheme, r.BreedID),
		Status:        r.Status,
		Meta:          domain.Meta{Source: r.MetaSource, SourceID: r.MetaSourceID, Modified: utc(r.Modified)},
		Deleted:       r.IsDeleted,
	}
}

// ListDevices returns devices at loc modified at or after from, each with the
// assignment current at time at, if any.
func (s *Store) ListDevices(ctx context.Context, loc domain.Location, from, at time.Time) ([]domain.Device, error) {
	devices, err := s.q.ListDevices(ctx, db.ListDevicesParams{LocationScheme: loc.Scheme, LocationID: loc.ID, ModifiedFrom: from})
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	assignments, err := s.q.CurrentAssignments(ctx, db.CurrentAssignmentsParams{LocationScheme: loc.Scheme, LocationID: loc.ID, At: at})
	if err != nil {
		return nil, fmt.Errorf("list device assignments: %w", err)
	}
	current := make(map[string]*domain.DeviceAssignment, len(assignments))
	for _, a := range assignments {
		assignment := &domain.DeviceAssignment{
			Animal:    domain.AnimalID{Scheme: a.AnimalScheme, ID: a.AnimalID},
			ValidFrom: utc(a.ValidFrom),
		}
		if !a.OpenEnded {
			to := utc(a.ValidTo)
			assignment.ValidTo = &to
		}
		current[a.DeviceID] = assignment
	}
	out := make([]domain.Device, len(devices))
	for i, d := range devices {
		out[i] = domain.Device{ID: d.DeviceID, Serial: d.Serial, Name: d.Name, Modified: utc(d.Modified), Assignment: current[d.DeviceID]}
	}
	return out, nil
}

// AnimalsWithRemarkSince returns animals at loc with a remark of category
// whose event time is at or after since.
func (s *Store) AnimalsWithRemarkSince(ctx context.Context, loc domain.Location, category domain.RemarkCategory, since time.Time) (map[domain.AnimalID]bool, error) {
	rows, err := s.q.AnimalsWithRemarkSince(ctx, db.AnimalsWithRemarkSinceParams{
		LocationScheme: loc.Scheme, LocationID: loc.ID, Category: string(category), Since: since,
	})
	if err != nil {
		return nil, fmt.Errorf("animals with remarks: %w", err)
	}
	out := make(map[domain.AnimalID]bool, len(rows))
	for _, r := range rows {
		out[domain.AnimalID{Scheme: r.AnimalScheme, ID: r.AnimalID}] = true
	}
	return out, nil
}

func breedFromRow(scheme, id *string) *domain.Breed {
	if scheme == nil || id == nil {
		return nil
	}
	return &domain.Breed{Scheme: *scheme, ID: *id}
}
