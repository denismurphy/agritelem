package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/denismurphy/agritelem/backend/internal/domain"
	"github.com/denismurphy/agritelem/backend/internal/store/db"
)

// RemarkStatus is the outcome of creating a remark.
type RemarkStatus int

// Remark outcomes; see IngestStatus for their meaning.
const (
	RemarkCreated RemarkStatus = iota + 1
	RemarkReplayed
	RemarkConflict
)

// CreateRemark stores r unless a remark with the same (source, sourceId)
// exists. It returns the stored remark: the new one, or the existing one for
// a replay or conflict.
func (s *Store) CreateRemark(ctx context.Context, r domain.Remark) (RemarkStatus, domain.Remark, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return 0, domain.Remark{}, fmt.Errorf("generate remark id: %w", err)
	}
	hash := r.ContentHash()
	row, err := s.q.InsertRemark(ctx, db.InsertRemarkParams{
		ID:             id,
		LocationScheme: r.Location.Scheme,
		LocationID:     r.Location.ID,
		AnimalScheme:   r.Animal.Scheme,
		AnimalID:       r.Animal.ID,
		EventDatetime:  r.EventDateTime.UTC(),
		Note:           r.Note,
		Category:       string(r.Category),
		Responsible:    r.Responsible,
		MetaSource:     r.Meta.Source,
		MetaSourceID:   r.Meta.SourceID,
		MetaCreator:    nonEmpty(r.Meta.Creator),
		ClientModified: r.Meta.Modified.UTC(),
		ContentHash:    hash[:],
	})
	if err == nil {
		return RemarkCreated, remarkFromRow(row), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, domain.Remark{}, fmt.Errorf("insert remark: %w", err)
	}

	existing, err := s.q.GetRemarkBySource(ctx, db.GetRemarkBySourceParams{MetaSource: r.Meta.Source, MetaSourceID: r.Meta.SourceID})
	if err != nil {
		return 0, domain.Remark{}, fmt.Errorf("read existing remark: %w", err)
	}
	status := RemarkConflict
	if bytes.Equal(existing.ContentHash, hash[:]) {
		status = RemarkReplayed
	}
	return status, remarkFromRow(existing), nil
}

// ListRemarks returns up to limit remarks at loc modified at or after from,
// strictly after the cursor, optionally for one animal.
func (s *Store) ListRemarks(ctx context.Context, loc domain.Location, from time.Time, animal *domain.AnimalID, after Cursor, limit int) ([]domain.Remark, error) {
	afterID := uuid.Nil
	if after.Key1 != "" {
		parsed, err := uuid.Parse(after.Key1)
		if err != nil {
			return nil, fmt.Errorf("remark cursor: %w", err)
		}
		afterID = parsed
	}
	params := db.ListRemarksParams{
		LocationScheme: loc.Scheme, LocationID: loc.ID, ModifiedFrom: from,
		AfterModified: after.Modified, AfterID: afterID, PageLimit: int32(limit), //nolint:gosec // limit is bounded by the API schema
	}
	if animal != nil {
		params.AnimalScheme, params.AnimalID = &animal.Scheme, &animal.ID
	}
	rows, err := s.q.ListRemarks(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("list remarks: %w", err)
	}
	out := make([]domain.Remark, len(rows))
	for i, r := range rows {
		out[i] = remarkFromRow(r)
	}
	return out, nil
}

// remarkFromRow maps a stored remark. meta.modified is the server's
// modification time, which is what synchronising clients filter on.
func remarkFromRow(r db.Remark) domain.Remark {
	creator := ""
	if r.MetaCreator != nil {
		creator = *r.MetaCreator
	}
	return domain.Remark{
		ID:            r.ID.String(),
		Location:      domain.Location{Scheme: r.LocationScheme, ID: r.LocationID},
		Animal:        domain.AnimalID{Scheme: r.AnimalScheme, ID: r.AnimalID},
		EventDateTime: utc(r.EventDatetime),
		Note:          r.Note,
		Category:      domain.RemarkCategory(r.Category),
		Responsible:   r.Responsible,
		Meta:          domain.Meta{Source: r.MetaSource, SourceID: r.MetaSourceID, Creator: creator, Modified: utc(r.Modified)},
	}
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
