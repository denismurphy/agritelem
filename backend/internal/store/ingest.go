package store

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/denismurphy/agritelem/backend/internal/domain"
	"github.com/denismurphy/agritelem/backend/internal/store/db"
)

// IngestStatus is the outcome of storing one validated resource.
type IngestStatus int

// Ingest outcomes.
const (
	// Accepted: the resource was new and its observations are stored.
	Accepted IngestStatus = iota + 1
	// Replayed: the same (source, sourceId) with the same content was stored
	// earlier; nothing changed.
	Replayed
	// Conflict: the same (source, sourceId) was stored earlier with
	// different content; nothing changed.
	Conflict
)

// IngestOutcome reports what happened to one resource. ResourceID is the
// server identity of the stored resource (the earlier one for Replayed and
// Conflict).
type IngestOutcome struct {
	Status     IngestStatus
	ResourceID uuid.UUID
}

// Rejection is a resource that failed validation, kept for diagnosis. The
// resource is the parsed request body re-encoded as JSON.
type Rejection struct {
	Principal string
	Resource  json.RawMessage
	Problems  []*domain.Problem
}

// Ingest stores accepted submissions and quarantines rejections in one
// transaction. Outcomes are returned in the order of subs. Either everything
// is committed or nothing is, so a failed call is safe to retry.
func (s *Store) Ingest(ctx context.Context, loc domain.Location, subs []domain.SummarySubmission, rejected []Rejection) ([]IngestOutcome, error) {
	var outcomes []IngestOutcome
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		var seqs map[int]int64
		var err error
		outcomes, seqs, err = claimKeys(ctx, q, subs)
		if err != nil {
			return err
		}
		if err := insertObservations(ctx, q, subs, seqs); err != nil {
			return err
		}
		return quarantine(ctx, q, loc, rejected)
	})
	if err != nil {
		return nil, err
	}
	return outcomes, nil
}

// claimKeys records each submission's (source, sourceId) in ingest_keys. It
// returns an outcome per submission and, for accepted ones, the ingest
// sequence that orders revisions. Keys are claimed in sorted order so that
// concurrent batches with overlapping keys wait on each other rather than
// deadlock.
func claimKeys(ctx context.Context, q *db.Queries, subs []domain.SummarySubmission) ([]IngestOutcome, map[int]int64, error) {
	order := make([]int, len(subs))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(a, b int) int {
		return cmp.Or(cmp.Compare(subs[a].Meta.Source, subs[b].Meta.Source), cmp.Compare(subs[a].Meta.SourceID, subs[b].Meta.SourceID))
	})

	hashes := make([][]byte, len(subs))
	params := make([]db.ClaimIngestKeyParams, len(order))
	for n, i := range order {
		hash := subs[i].ContentHash()
		hashes[i] = hash[:]
		id, err := uuid.NewV7()
		if err != nil {
			return nil, nil, fmt.Errorf("generate resource id: %w", err)
		}
		params[n] = db.ClaimIngestKeyParams{Source: subs[i].Meta.Source, SourceID: subs[i].Meta.SourceID, ContentHash: hashes[i], ResourceID: id}
	}

	outcomes := make([]IngestOutcome, len(subs))
	seqs := make(map[int]int64, len(subs))
	var batchErr error
	results := q.ClaimIngestKey(ctx, params)
	results.QueryRow(func(n int, row db.ClaimIngestKeyRow, err error) {
		i := order[n]
		switch {
		case err == nil && row.Seq == nil:
			batchErr = errors.Join(batchErr, fmt.Errorf("ingest key %s/%s claimed without a sequence", subs[i].Meta.Source, subs[i].Meta.SourceID))
		case err == nil:
			seqs[i] = *row.Seq
			outcomes[i] = IngestOutcome{Status: Accepted, ResourceID: row.ResourceID}
		case errors.Is(err, pgx.ErrNoRows):
			// The key exists (possibly from earlier in this batch); resolved below.
		default:
			batchErr = errors.Join(batchErr, fmt.Errorf("claim ingest key %s/%s: %w", subs[i].Meta.Source, subs[i].Meta.SourceID, err))
		}
	})
	if err := results.Close(); err != nil {
		batchErr = errors.Join(batchErr, err)
	}
	if batchErr != nil {
		return nil, nil, batchErr
	}

	for i := range subs {
		if outcomes[i].Status != 0 {
			continue
		}
		existing, err := q.GetIngestKey(ctx, db.GetIngestKeyParams{Source: subs[i].Meta.Source, SourceID: subs[i].Meta.SourceID})
		if err != nil {
			return nil, nil, fmt.Errorf("read ingest key %s/%s: %w", subs[i].Meta.Source, subs[i].Meta.SourceID, err)
		}
		status := Conflict
		if slices.Equal(existing.ContentHash, hashes[i]) {
			status = Replayed
		}
		outcomes[i] = IngestOutcome{Status: status, ResourceID: existing.ResourceID}
	}
	return outcomes, seqs, nil
}

func insertObservations(ctx context.Context, q *db.Queries, subs []domain.SummarySubmission, seqs map[int]int64) error {
	var rows []db.InsertObservationsParams
	for i, sub := range subs {
		seq, accepted := seqs[i]
		if !accepted {
			continue
		}
		for _, o := range sub.Observations {
			rows = append(rows, db.InsertObservationsParams{
				AnimalScheme: sub.Animal.Scheme,
				AnimalID:     sub.Animal.ID,
				MetricScheme: o.Metric.Scheme,
				MetricID:     o.Metric.ID,
				HourStart:    o.Start.UTC(),
				Value:        o.Value,
				MetaSource:   sub.Meta.Source,
				MetaSourceID: sub.Meta.SourceID,
				MetaModified: sub.Meta.Modified.UTC(),
				IngestSeq:    seq,
			})
		}
	}
	if len(rows) == 0 {
		return nil
	}
	n, err := q.InsertObservations(ctx, rows)
	if err != nil {
		return fmt.Errorf("copy observations: %w", err)
	}
	if n != int64(len(rows)) {
		return fmt.Errorf("copied %d of %d observations", n, len(rows))
	}
	return nil
}

func quarantine(ctx context.Context, q *db.Queries, loc domain.Location, rejected []Rejection) error {
	for _, r := range rejected {
		problems, err := json.Marshal(r.Problems)
		if err != nil {
			return fmt.Errorf("encode problems: %w", err)
		}
		resource := r.Resource
		if !json.Valid(resource) {
			// Keep undecodable input as a JSON string rather than lose it.
			if resource, err = json.Marshal(string(r.Resource)); err != nil {
				return fmt.Errorf("encode rejected resource: %w", err)
			}
		}
		if err := q.InsertQuarantine(ctx, db.InsertQuarantineParams{
			Principal:      r.Principal,
			LocationScheme: loc.Scheme,
			LocationID:     loc.ID,
			Resource:       resource,
			Problems:       problems,
		}); err != nil {
			return fmt.Errorf("quarantine resource: %w", err)
		}
	}
	return nil
}
