// Package store persists Agritelem data in PostgreSQL with TimescaleDB.
// Queries live in queries/*.sql and are compiled by sqlc into package db.
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/denismurphy/agritelem/backend/internal/store/db"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store is the PostgreSQL-backed persistence layer. It is safe for
// concurrent use.
type Store struct {
	pool *pgxpool.Pool
	q    *db.Queries
}

// Open connects to dsn. Every connection uses the UTC time zone so that no
// query can depend on a server or role default.
func Open(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse database URL: %w", err)
	}
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to database: %w", err)
	}
	return &Store{pool: pool, q: db.New(pool)}, nil
}

// Close releases all connections.
func (s *Store) Close() { s.pool.Close() }

// Ping reports whether the database is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Migrate applies all pending migrations. It needs a role allowed to create
// the timescaledb and btree_gist extensions.
func (s *Store) Migrate(ctx context.Context) (err error) {
	// Closing this *sql.DB does not close the shared pool.
	sqlDB := stdlib.OpenDBFromPool(s.pool)
	defer func() { err = errors.Join(err, sqlDB.Close()) }()
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("open embedded migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, fsys)
	if err != nil {
		return fmt.Errorf("prepare migrations: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("not found")

// utc normalises a timestamp read from the database. pgx decodes timestamptz
// in the local zone; Agritelem works in UTC only.
func utc(t time.Time) time.Time { return t.UTC() }

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
