package main

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// isOverlap reports whether err is the device_assignments exclusion
// constraint, which a repeated seed run triggers for existing assignments.
func isOverlap(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.ConstraintName == "device_assignments_no_overlap"
}
