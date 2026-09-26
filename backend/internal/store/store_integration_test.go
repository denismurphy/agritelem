//go:build integration

package store_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/denismurphy/agritelem/backend/internal/domain"
	"github.com/denismurphy/agritelem/backend/internal/pipeline"
	"github.com/denismurphy/agritelem/backend/internal/store"
	"github.com/denismurphy/agritelem/backend/internal/store/db"
	"github.com/denismurphy/agritelem/backend/internal/testdb"
)

var dsn string

func TestMain(m *testing.M) { os.Exit(testdb.Main(m, &dsn)) }

var (
	seq  atomic.Int64
	day  = time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	once sync.Once
)

func open(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	var migrateErr error
	once.Do(func() { migrateErr = s.Migrate(ctx) })
	if migrateErr != nil {
		t.Fatal(migrateErr)
	}
	return s
}

// fixture creates a fresh location with one animal so tests do not interfere.
func fixture(t *testing.T, s *store.Store) (domain.Location, domain.AnimalID) {
	t.Helper()
	n := seq.Add(1)
	loc := domain.Location{Scheme: "test.location", ID: fmt.Sprintf("%s-%d", t.Name(), n)}
	animal := domain.AnimalID{Scheme: domain.SchemeISO11785, ID: fmt.Sprintf("372%012d", n)}
	ctx := context.Background()
	if err := s.UpsertLocation(ctx, loc, "Test farm"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertAnimal(ctx, domain.Animal{ID: animal, Location: loc, Specie: "Cattle", Gender: "Female", Status: "Alive",
		Meta: domain.Meta{Source: "test", SourceID: animal.ID}}); err != nil {
		t.Fatal(err)
	}
	return loc, animal
}

func submission(animal domain.AnimalID, sourceID string, modified time.Time, values map[int]float64) domain.SummarySubmission {
	sub := domain.SummarySubmission{Animal: animal, Meta: domain.Meta{Source: "ie.test.gateway", SourceID: sourceID, Modified: modified}}
	for hour, v := range values {
		sub.Observations = append(sub.Observations, domain.Observation{Metric: domain.RuminationTime, Start: day.Add(time.Duration(hour) * time.Hour), Value: v})
	}
	return sub
}

func ingest(t *testing.T, s *store.Store, loc domain.Location, subs ...domain.SummarySubmission) []store.IngestOutcome {
	t.Helper()
	out, err := s.Ingest(context.Background(), loc, subs, nil)
	if err != nil {
		t.Fatalf("Ingest: %v", err)
	}
	return out
}

func rumination(t *testing.T, s *store.Store, animal domain.AnimalID) pipeline.DailyAggregate {
	t.Helper()
	aggs, err := s.DailyAggregates(context.Background(), animal, day, day.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	if len(aggs) != 1 {
		t.Fatalf("got %d aggregates, want 1: %+v", len(aggs), aggs)
	}
	return aggs[0]
}

func TestMigrateIsIdempotent(t *testing.T) {
	s := open(t)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("second migration run: %v", err)
	}
}

func TestIngestIdempotency(t *testing.T) {
	s := open(t)
	loc, animal := fixture(t, s)
	modified := day.Add(24 * time.Hour)
	original := submission(animal, "rec-1", modified, map[int]float64{0: 30, 1: 20})

	first := ingest(t, s, loc, original)
	if first[0].Status != store.Accepted {
		t.Fatalf("first status = %v, want Accepted", first[0].Status)
	}

	restamped := original
	restamped.Meta.Modified = modified.Add(time.Minute)
	replay := ingest(t, s, loc, restamped)
	if replay[0] != (store.IngestOutcome{Status: store.Replayed, ResourceID: first[0].ResourceID}) {
		t.Errorf("replay = %+v, want Replayed with the original resource id", replay[0])
	}

	changed := submission(animal, "rec-1", modified, map[int]float64{0: 31, 1: 20})
	conflict := ingest(t, s, loc, changed)
	if conflict[0] != (store.IngestOutcome{Status: store.Conflict, ResourceID: first[0].ResourceID}) {
		t.Errorf("conflict = %+v, want Conflict with the original resource id", conflict[0])
	}

	if got := rumination(t, s, animal); got.Hours != 2 || got.Sum != 50 {
		t.Errorf("aggregate = %+v, want the original 2 hours summing to 50", got)
	}
}

func TestIngestDuplicateKeyWithinBatch(t *testing.T) {
	s := open(t)
	loc, animal := fixture(t, s)
	sub := submission(animal, "dup", day.Add(24*time.Hour), map[int]float64{0: 30})
	out := ingest(t, s, loc, sub, sub)
	if out[0].Status != store.Accepted || out[1].Status != store.Replayed || out[0].ResourceID != out[1].ResourceID {
		t.Errorf("outcomes = %+v, want Accepted then Replayed with one id", out)
	}
	if got := rumination(t, s, animal); got.Sum != 30 {
		t.Errorf("sum = %v, want 30 (stored once)", got.Sum)
	}
}

func TestLatestRevisionWins(t *testing.T) {
	s := open(t)
	loc, animal := fixture(t, s)
	t0 := day.Add(24 * time.Hour)

	ingest(t, s, loc, submission(animal, "v1", t0, map[int]float64{0: 10}))
	ingest(t, s, loc, submission(animal, "v2", t0.Add(time.Hour), map[int]float64{0: 20}))
	// An older correction arriving late must not win.
	ingest(t, s, loc, submission(animal, "v0", t0.Add(-time.Hour), map[int]float64{0: 5}))
	if got := rumination(t, s, animal); got.Sum != 20 || got.Hours != 1 {
		t.Errorf("aggregate = %+v, want the latest-modified value 20", got)
	}

	// Equal meta.modified: the later ingest wins.
	ingest(t, s, loc, submission(animal, "v3", t0.Add(time.Hour), map[int]float64{0: 25}))
	if got := rumination(t, s, animal); got.Sum != 25 {
		t.Errorf("tie: sum = %v, want 25 from the later ingest", got.Sum)
	}
}

func TestConcurrentIngestOfSameKey(t *testing.T) {
	s := open(t)
	loc, animal := fixture(t, s)
	sub := submission(animal, "race", day.Add(24*time.Hour), map[int]float64{0: 30, 1: 30})

	const writers = 8
	var wg sync.WaitGroup
	statuses := make([]store.IngestStatus, writers)
	errs := make([]error, writers)
	for i := range writers {
		wg.Go(func() {
			out, err := s.Ingest(context.Background(), loc, []domain.SummarySubmission{sub}, nil)
			errs[i] = err
			if err == nil {
				statuses[i] = out[0].Status
			}
		})
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if accepted := slices.Index(statuses, store.Accepted); accepted < 0 || slices.Index(statuses[accepted+1:], store.Accepted) >= 0 {
		t.Errorf("statuses = %v, want exactly one Accepted", statuses)
	}
	if got := rumination(t, s, animal); got.Sum != 60 {
		t.Errorf("sum = %v, want 60 (stored once)", got.Sum)
	}
}

func TestIngestRollsBackOnFailure(t *testing.T) {
	s := open(t)
	loc, animal := fixture(t, s)
	good := submission(animal, "good", day.Add(24*time.Hour), map[int]float64{0: 30})
	bad := submission(animal, "bad", day.Add(24*time.Hour), nil)
	// Bypasses Validate: a misaligned hour violates the table's CHECK.
	bad.Observations = []domain.Observation{{Metric: domain.RuminationTime, Start: day.Add(90 * time.Minute), Value: 1}}

	if _, err := s.Ingest(context.Background(), loc, []domain.SummarySubmission{good, bad}, nil); err == nil {
		t.Fatal("Ingest succeeded with a row violating a CHECK constraint")
	}
	// Nothing from the failed batch may remain, including its ingest keys.
	if out := ingest(t, s, loc, good); out[0].Status != store.Accepted {
		t.Errorf("resubmission after rollback = %v, want Accepted", out[0].Status)
	}
}

func TestDailyAggregatesUseUTCDaysRegardlessOfSessionZone(t *testing.T) {
	s := open(t)
	loc, animal := fixture(t, s)
	sub := submission(animal, "tz", day.Add(48*time.Hour), nil)
	sub.Observations = []domain.Observation{
		{Metric: domain.RuminationTime, Start: day.Add(23 * time.Hour), Value: 10},
		{Metric: domain.RuminationTime, Start: day.Add(24 * time.Hour), Value: 20},
	}
	ingest(t, s, loc, sub)

	// Query through a pool whose session zone is UTC+14 to prove the SQL
	// itself buckets by UTC day rather than relying on the store's settings.
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["timezone"] = "Pacific/Kiritimati"
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	rows, err := db.New(pool).DailyAggregates(context.Background(), db.DailyAggregatesParams{
		AnimalScheme: animal.Scheme, AnimalID: animal.ID, FromTime: day, ToTime: day.AddDate(0, 0, 2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || !rows[0].Day.Equal(day) || rows[0].Total != 10 || !rows[1].Day.Equal(day.AddDate(0, 0, 1)) || rows[1].Total != 20 {
		t.Errorf("rows = %+v, want 10 on %s and 20 on the next UTC day", rows, day.Format(time.DateOnly))
	}
}

func TestQuarantineKeepsRejections(t *testing.T) {
	s := open(t)
	loc, _ := fixture(t, s)
	rej := store.Rejection{Principal: "ie.test.gateway", Resource: []byte(`{"animal":{"id":"x"}}`),
		Problems: []*domain.Problem{{Code: domain.CodePhysiologicalOutlier, Field: "/statistics/0/value", Detail: "too high"}}}
	notJSON := store.Rejection{Principal: "ie.test.gateway", Resource: []byte("not json"), Problems: rej.Problems}
	if _, err := s.Ingest(context.Background(), loc, nil, []store.Rejection{rej, notJSON}); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var n int
	var code string
	err = pool.QueryRow(context.Background(),
		`SELECT count(*), min(problems->0->>'code') FROM quarantine WHERE location_id = $1`, loc.ID).Scan(&n, &code)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 || code != string(domain.CodePhysiologicalOutlier) {
		t.Errorf("quarantine has %d rows with code %q, want 2 with %s", n, code, domain.CodePhysiologicalOutlier)
	}
}

func TestDeviceAssignments(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	loc, animal := fixture(t, s)
	_, other := fixture(t, s)
	device := domain.Device{ID: "bolus-" + animal.ID}
	if err := s.UpsertDevice(ctx, loc, device); err != nil {
		t.Fatal(err)
	}
	feb := day.AddDate(0, 1, 0)
	if err := s.AssignDevice(ctx, device.ID, animal, day, &feb); err != nil {
		t.Fatal(err)
	}
	if err := s.AssignDevice(ctx, device.ID, other, day.AddDate(0, 0, 10), nil); err == nil {
		t.Fatal("overlapping assignment was accepted")
	}
	if err := s.AssignDevice(ctx, device.ID, other, feb, nil); err != nil {
		t.Fatalf("adjacent assignment rejected: %v", err)
	}

	for _, tt := range []struct {
		at         time.Time
		wantAnimal domain.AnimalID
		wantOpen   bool
	}{
		{day.AddDate(0, 0, 5), animal, false},
		{feb.AddDate(0, 0, 5), other, true},
	} {
		devices, err := s.ListDevices(ctx, loc, time.Time{}, tt.at)
		if err != nil {
			t.Fatal(err)
		}
		if len(devices) != 1 || devices[0].Assignment == nil {
			t.Fatalf("at %s: devices = %+v", tt.at, devices)
		}
		a := devices[0].Assignment
		if a.Animal != tt.wantAnimal || (a.ValidTo == nil) != tt.wantOpen {
			t.Errorf("at %s: assignment = %+v, want animal %s open=%v", tt.at, a, tt.wantAnimal.ID, tt.wantOpen)
		}
	}
}

func TestRemarks(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	loc, animal := fixture(t, s)
	remark := domain.Remark{Location: loc, Animal: animal, EventDateTime: day, Note: "Lame", Category: domain.RemarkVeterinary,
		Meta: domain.Meta{Source: "ie.test.app", SourceID: "r1", Modified: day}}

	status, created, err := s.CreateRemark(ctx, remark)
	if err != nil || status != store.RemarkCreated || created.ID == "" {
		t.Fatalf("create: status %v remark %+v err %v", status, created, err)
	}
	status, replayed, err := s.CreateRemark(ctx, remark)
	if err != nil || status != store.RemarkReplayed || replayed.ID != created.ID {
		t.Errorf("replay: status %v id %s err %v", status, replayed.ID, err)
	}
	changed := remark
	changed.Note = "Very lame"
	status, current, err := s.CreateRemark(ctx, changed)
	if err != nil || status != store.RemarkConflict || current.Note != "Lame" {
		t.Errorf("conflict: status %v note %q err %v", status, current.Note, err)
	}

	vets, err := s.AnimalsWithRemarkSince(ctx, loc, domain.RemarkVeterinary, day)
	if err != nil || !vets[animal] {
		t.Errorf("veterinary animals = %v err %v", vets, err)
	}
}

func TestKeysetPagination(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	loc, first := fixture(t, s)
	seen := map[domain.AnimalID]bool{first: true}
	for i := range 4 {
		id := domain.AnimalID{Scheme: domain.SchemeISO11785, ID: fmt.Sprintf("372%012d", 900000+seq.Add(1)*10+int64(i))}
		if err := s.UpsertAnimal(ctx, domain.Animal{ID: id, Location: loc, Specie: "Cattle", Gender: "Female", Status: "Alive", Meta: domain.Meta{Source: "t", SourceID: id.ID}}); err != nil {
			t.Fatal(err)
		}
		seen[id] = true
	}

	var got []domain.AnimalID
	var after store.Cursor
	for page := 0; page < 10; page++ {
		animals, err := s.ListAnimals(ctx, loc, time.Time{}, after, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(animals) == 0 {
			break
		}
		for _, a := range animals {
			got = append(got, a.ID)
		}
		last := animals[len(animals)-1]
		after = store.Cursor{Modified: last.Meta.Modified, Key1: last.ID.Scheme, Key2: last.ID.ID}
	}
	if len(got) != len(seen) {
		t.Fatalf("paged %d animals, want %d: %v", len(got), len(seen), got)
	}
	for _, id := range got {
		if !seen[id] {
			t.Errorf("unexpected animal %v", id)
		}
		delete(seen, id)
	}
}

// TestTiesBreakBySequenceWithoutIndexOrder checks the query's ORDER BY rather
// than the index: with index scans disabled, only the explicit ingest_seq
// tie-break makes the last of several equally-modified revisions win.
func TestTiesBreakBySequenceWithoutIndexOrder(t *testing.T) {
	s := open(t)
	loc, animal := fixture(t, s)
	modified := day.Add(24 * time.Hour)
	for v := 21; v <= 25; v++ {
		ingest(t, s, loc, submission(animal, fmt.Sprintf("tie-%d", v), modified, map[int]float64{0: float64(v)}))
	}

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["enable_indexscan"] = "off"
	cfg.ConnConfig.RuntimeParams["enable_bitmapscan"] = "off"
	cfg.ConnConfig.RuntimeParams["enable_indexonlyscan"] = "off"
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	q := db.New(pool)

	daily, err := q.DailyAggregates(context.Background(), db.DailyAggregatesParams{AnimalScheme: animal.Scheme, AnimalID: animal.ID, FromTime: day, ToTime: day.AddDate(0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if len(daily) != 1 || daily[0].Total != 25 {
		t.Errorf("daily = %+v, want 25 from the last ingest", daily)
	}
	hourly, err := q.HourlyObservations(context.Background(), db.HourlyObservationsParams{AnimalScheme: animal.Scheme, AnimalID: animal.ID, FromTime: day, ToTime: day.AddDate(0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	if len(hourly) != 1 || hourly[0].Value != 25 {
		t.Errorf("hourly = %+v, want 25 from the last ingest", hourly)
	}
}

func TestHourlyObservationsReturnLatestRevisionPerHour(t *testing.T) {
	s := open(t)
	loc, animal := fixture(t, s)
	t0 := day.Add(24 * time.Hour)
	ingest(t, s, loc, submission(animal, "h1", t0, map[int]float64{0: 10, 1: 11}))
	ingest(t, s, loc, submission(animal, "h2", t0.Add(time.Hour), map[int]float64{1: 12}))
	ingest(t, s, loc, submission(animal, "h0", t0.Add(-time.Hour), map[int]float64{0: 9}))

	got, err := s.HourlyObservations(context.Background(), animal, day, day.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.Observation{
		{Metric: domain.RuminationTime, Start: day, Value: 10},
		{Metric: domain.RuminationTime, Start: day.Add(time.Hour), Value: 12},
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// TestReassignmentBumpsDeviceModified: clients sync devices with
// meta-modified-from, so a new assignment must mark the device as modified or
// the move to another animal is never seen.
func TestReassignmentBumpsDeviceModified(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	loc, animal := fixture(t, s)
	_, other := fixture(t, s)
	device := domain.Device{ID: "collar-" + animal.ID}
	if err := s.UpsertDevice(ctx, loc, device); err != nil {
		t.Fatal(err)
	}
	moveAt := time.Now().UTC().Add(-time.Hour)
	if err := s.AssignDevice(ctx, device.ID, animal, moveAt.Add(-24*time.Hour), &moveAt); err != nil {
		t.Fatal(err)
	}
	watermark := time.Now().UTC()
	time.Sleep(10 * time.Millisecond)

	if err := s.AssignDevice(ctx, device.ID, other, moveAt, nil); err != nil {
		t.Fatal(err)
	}
	changed, err := s.ListDevices(ctx, loc, watermark, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 1 || changed[0].Assignment == nil || changed[0].Assignment.Animal != other {
		t.Fatalf("devices modified since the watermark = %+v, want the reassigned device now on %s", changed, other.ID)
	}
}
