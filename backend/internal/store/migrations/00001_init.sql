-- +goose Up
CREATE EXTENSION IF NOT EXISTS timescaledb;
CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE TABLE locations (
    scheme text NOT NULL,
    id     text NOT NULL,
    name   text NOT NULL,
    PRIMARY KEY (scheme, id)
);

-- ICAR icarAnimalCoreResource. `modified` is set by the server on every write
-- and drives meta-modified-from synchronisation.
CREATE TABLE animals (
    animal_scheme   text        NOT NULL,
    animal_id       text        NOT NULL,
    location_scheme text        NOT NULL,
    location_id     text        NOT NULL,
    specie          text        NOT NULL,
    gender          text        NOT NULL,
    birth_date      timestamptz,
    name            text,
    management_tag  text,
    breed_scheme    text,
    breed_id        text,
    status          text        NOT NULL DEFAULT 'Alive',
    meta_source     text        NOT NULL,
    meta_source_id  text        NOT NULL,
    modified        timestamptz NOT NULL DEFAULT now(),
    is_deleted      boolean     NOT NULL DEFAULT false,
    PRIMARY KEY (animal_scheme, animal_id),
    FOREIGN KEY (location_scheme, location_id) REFERENCES locations (scheme, id),
    CONSTRAINT animals_breed_pair CHECK ((breed_scheme IS NULL) = (breed_id IS NULL))
);
CREATE INDEX animals_sync_idx ON animals (location_scheme, location_id, modified, animal_scheme, animal_id);

CREATE TABLE devices (
    device_id       text        NOT NULL PRIMARY KEY,
    location_scheme text        NOT NULL,
    location_id     text        NOT NULL,
    serial          text,
    name            text,
    modified        timestamptz NOT NULL DEFAULT now(),
    FOREIGN KEY (location_scheme, location_id) REFERENCES locations (scheme, id)
);

-- A device is attached to at most one animal at any instant.
CREATE TABLE device_assignments (
    device_id     text      NOT NULL REFERENCES devices (device_id),
    animal_scheme text      NOT NULL,
    animal_id     text      NOT NULL,
    during        tstzrange NOT NULL,
    FOREIGN KEY (animal_scheme, animal_id) REFERENCES animals (animal_scheme, animal_id),
    CONSTRAINT device_assignments_no_overlap EXCLUDE USING gist (device_id WITH =, during WITH &&),
    CONSTRAINT device_assignments_not_empty CHECK (NOT isempty(during))
);

-- Idempotency ledger for ingested resources. Hypertables cannot enforce a
-- unique index without the partitioning column, so global (source, sourceId)
-- uniqueness lives here. `seq` orders revisions that share meta.modified.
CREATE TABLE ingest_keys (
    source       text        NOT NULL,
    source_id    text        NOT NULL,
    content_hash bytea       NOT NULL,
    resource_id  uuid        NOT NULL,
    seq          bigint      GENERATED ALWAYS AS IDENTITY,
    received_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (source, source_id)
);

-- Append-only hourly observations. A correction is a new row from a new
-- source record; readers take the latest by (meta_modified, ingest_seq).
CREATE TABLE observation_hourly (
    animal_scheme  text             NOT NULL,
    animal_id      text             NOT NULL,
    metric_scheme  text             NOT NULL,
    metric_id      text             NOT NULL,
    hour_start     timestamptz      NOT NULL,
    value          double precision NOT NULL,
    meta_source    text             NOT NULL,
    meta_source_id text             NOT NULL,
    meta_modified  timestamptz      NOT NULL,
    ingest_seq     bigint           NOT NULL,
    CONSTRAINT observation_hourly_aligned CHECK (hour_start = date_trunc('hour', hour_start, 'UTC'))
);
SELECT create_hypertable('observation_hourly', by_range('hour_start', INTERVAL '7 days'));
CREATE INDEX observation_hourly_latest_idx ON observation_hourly
    (animal_scheme, animal_id, metric_scheme, metric_id, hour_start, meta_modified DESC, ingest_seq DESC);

-- Rejected resources (as parsed, re-encoded as JSON), kept for diagnosis.
CREATE TABLE quarantine (
    id          bigint      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    principal   text        NOT NULL,
    location_scheme text    NOT NULL,
    location_id text        NOT NULL,
    resource    jsonb       NOT NULL,
    problems    jsonb       NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX quarantine_received_idx ON quarantine (received_at);

-- ICAR icarRemarkEventResource. `modified` is server-set for synchronisation;
-- `client_modified` preserves the submitted meta.modified.
CREATE TABLE remarks (
    id              uuid        NOT NULL PRIMARY KEY,
    location_scheme text        NOT NULL,
    location_id     text        NOT NULL,
    animal_scheme   text        NOT NULL,
    animal_id       text        NOT NULL,
    event_datetime  timestamptz NOT NULL,
    note            text        NOT NULL,
    category        text        NOT NULL,
    responsible     text,
    meta_source     text        NOT NULL,
    meta_source_id  text        NOT NULL,
    meta_creator    text,
    client_modified timestamptz NOT NULL,
    content_hash    bytea       NOT NULL,
    modified        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (meta_source, meta_source_id),
    FOREIGN KEY (animal_scheme, animal_id) REFERENCES animals (animal_scheme, animal_id),
    FOREIGN KEY (location_scheme, location_id) REFERENCES locations (scheme, id)
);
CREATE INDEX remarks_sync_idx ON remarks (location_scheme, location_id, modified, id);
CREATE INDEX remarks_animal_idx ON remarks (animal_scheme, animal_id, event_datetime);

-- +goose Down
DROP TABLE remarks;
DROP TABLE quarantine;
DROP TABLE observation_hourly;
DROP TABLE ingest_keys;
DROP TABLE device_assignments;
DROP TABLE devices;
DROP TABLE animals;
DROP TABLE locations;
