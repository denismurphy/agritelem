// Command seed writes a fictional demonstration herd: one location, its
// animals and their sensor devices. It is idempotent and deterministic.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/denismurphy/agritelem/backend/internal/domain"
	"github.com/denismurphy/agritelem/backend/internal/store"
)

// Farm is the demonstration location. dk.herdnumber is an ICAR well-known
// location scheme; the herd number is the example from its specification.
var Farm = domain.Location{Scheme: "dk.herdnumber", ID: "123456"}

var names = []string{
	"Bessie", "Clover", "Daisy", "Buttercup", "Primrose", "Hazel", "Willow", "Maple",
	"Poppy", "Rosie", "Bluebell", "Heather", "Juniper", "Marigold", "Nettle", "Olive",
	"Pearl", "Saffron", "Tansy", "Violet", "Wren", "Ivy", "Fern", "Sorrel",
}

var breeds = []string{"HOL", "HOL", "HOL", "JER", "BSW", "HOL", "MON", "JER"}

func main() {
	if err := run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	dsn := os.Getenv("AGRITELEM_DATABASE_URL")
	if dsn == "" {
		return errors.New("AGRITELEM_DATABASE_URL is required")
	}
	st, err := store.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return err
	}
	if err := st.UpsertLocation(ctx, Farm, "Glenbrook Farm"); err != nil {
		return err
	}

	epoch := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, name := range names {
		id := AnimalID(i)
		birth := time.Date(2019+i%5, time.Month(1+i%12), 1+i%27, 0, 0, 0, 0, time.UTC)
		tag := fmt.Sprintf("%03d", 101+i)
		animal := domain.Animal{
			ID: id, Location: Farm, Specie: "Cattle", Gender: "Female", Status: "Alive",
			Name: &name, ManagementTag: &tag, BirthDate: &birth,
			Breed: &domain.Breed{Scheme: domain.SchemeICARBreed3, ID: breeds[i%len(breeds)]},
			Meta:  domain.Meta{Source: "io.agritelem.seed", SourceID: id.ID},
		}
		if err := st.UpsertAnimal(ctx, animal); err != nil {
			return err
		}
		serial := fmt.Sprintf("RB-%06d", 40000+i)
		model := "Reticular bolus"
		device := domain.Device{ID: "bolus-" + tag, Serial: &serial, Name: &model}
		if err := st.UpsertDevice(ctx, Farm, device); err != nil {
			return err
		}
		// Assignment is idempotent only on first seed; later runs keep it.
		if err := st.AssignDevice(ctx, device.ID, id, epoch, nil); err != nil && !isOverlap(err) {
			return err
		}
	}
	fmt.Printf("seeded %d animals at %s/%s\n", len(names), Farm.Scheme, Farm.ID)
	return nil
}

// AnimalID returns the ISO 11785 identifier of the i-th demo animal, using
// Ireland's country code (372).
func AnimalID(i int) domain.AnimalID {
	return domain.AnimalID{Scheme: domain.SchemeISO11785, ID: fmt.Sprintf("372%012d", 100000001+i)}
}
