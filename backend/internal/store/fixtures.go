package store

import (
	"context"
	"fmt"
	"time"

	"github.com/denismurphy/agritelem/backend/internal/domain"
	"github.com/denismurphy/agritelem/backend/internal/store/db"
)

// The methods in this file write reference data that the MVP API does not
// accept over HTTP (locations, animals, devices). They are used by cmd/seed
// and by tests.

// UpsertLocation creates or renames a location.
func (s *Store) UpsertLocation(ctx context.Context, loc domain.Location, name string) error {
	if err := s.q.UpsertLocation(ctx, db.UpsertLocationParams{Scheme: loc.Scheme, ID: loc.ID, Name: name}); err != nil {
		return fmt.Errorf("upsert location %s/%s: %w", loc.Scheme, loc.ID, err)
	}
	return nil
}

// UpsertAnimal creates or updates an animal and bumps its modified time.
func (s *Store) UpsertAnimal(ctx context.Context, a domain.Animal) error {
	var breedScheme, breedID *string
	if a.Breed != nil {
		breedScheme, breedID = &a.Breed.Scheme, &a.Breed.ID
	}
	err := s.q.UpsertAnimal(ctx, db.UpsertAnimalParams{
		AnimalScheme: a.ID.Scheme, AnimalID: a.ID.ID,
		LocationScheme: a.Location.Scheme, LocationID: a.Location.ID,
		Specie: a.Specie, Gender: a.Gender, BirthDate: a.BirthDate,
		Name: a.Name, ManagementTag: a.ManagementTag, BreedScheme: breedScheme, BreedID: breedID, Status: a.Status,
		MetaSource: a.Meta.Source, MetaSourceID: a.Meta.SourceID,
	})
	if err != nil {
		return fmt.Errorf("upsert animal %s: %w", a.ID.ID, err)
	}
	return nil
}

// UpsertDevice creates or updates a device at loc.
func (s *Store) UpsertDevice(ctx context.Context, loc domain.Location, d domain.Device) error {
	err := s.q.InsertDevice(ctx, db.InsertDeviceParams{DeviceID: d.ID, LocationScheme: loc.Scheme, LocationID: loc.ID, Serial: d.Serial, Name: d.Name})
	if err != nil {
		return fmt.Errorf("upsert device %s: %w", d.ID, err)
	}
	return nil
}

// AssignDevice attaches a device to an animal from validFrom until validTo
// (nil for open-ended). Overlapping assignments of one device are rejected by
// the database.
func (s *Store) AssignDevice(ctx context.Context, deviceID string, animal domain.AnimalID, validFrom time.Time, validTo *time.Time) error {
	err := s.q.AssignDevice(ctx, db.AssignDeviceParams{DeviceID: deviceID, AnimalScheme: animal.Scheme, AnimalID: animal.ID, ValidFrom: validFrom, ValidTo: validTo})
	if err != nil {
		return fmt.Errorf("assign device %s to %s: %w", deviceID, animal.ID, err)
	}
	return nil
}
