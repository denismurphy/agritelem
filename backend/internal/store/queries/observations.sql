-- name: ClaimIngestKey :batchone
-- Returns a row only when the key is new. On conflict the caller reads the
-- existing key to decide between an idempotent replay and a conflict.
INSERT INTO ingest_keys (source, source_id, content_hash, resource_id)
VALUES (@source, @source_id, @content_hash, @resource_id)
ON CONFLICT (source, source_id) DO NOTHING
RETURNING seq, resource_id;

-- name: GetIngestKey :one
SELECT content_hash, resource_id FROM ingest_keys WHERE source = @source AND source_id = @source_id;

-- name: InsertObservations :copyfrom
INSERT INTO observation_hourly (
    animal_scheme, animal_id, metric_scheme, metric_id, hour_start, value,
    meta_source, meta_source_id, meta_modified, ingest_seq
) VALUES (
    @animal_scheme, @animal_id, @metric_scheme, @metric_id, @hour_start, @value,
    @meta_source, @meta_source_id, @meta_modified, @ingest_seq
);

-- name: InsertQuarantine :exec
INSERT INTO quarantine (principal, location_scheme, location_id, resource, problems)
VALUES (@principal, @location_scheme, @location_id, @resource, @problems);

-- name: HourlyObservations :many
-- The latest revision of each hourly value for one animal in [@from, @to).
SELECT DISTINCT ON (metric_scheme, metric_id, hour_start)
       metric_scheme, metric_id, hour_start, value
FROM observation_hourly
WHERE animal_scheme = @animal_scheme AND animal_id = @animal_id
  AND hour_start >= @from_time AND hour_start < @to_time
ORDER BY metric_scheme, metric_id, hour_start, meta_modified DESC, ingest_seq DESC;

-- name: DailyAggregates :many
-- Rolls the latest revision of each hour into UTC days for one animal.
WITH latest AS (
    SELECT DISTINCT ON (metric_scheme, metric_id, hour_start)
           metric_scheme, metric_id, hour_start, value
    FROM observation_hourly
    WHERE animal_scheme = @animal_scheme AND animal_id = @animal_id
      AND hour_start >= @from_time AND hour_start < @to_time
    ORDER BY metric_scheme, metric_id, hour_start, meta_modified DESC, ingest_seq DESC
)
SELECT metric_scheme, metric_id,
       date_trunc('day', hour_start, 'UTC')::timestamptz AS day,
       count(*)::int AS hours,
       sum(value)::double precision AS total
FROM latest
GROUP BY metric_scheme, metric_id, day
ORDER BY day, metric_scheme, metric_id;

-- name: LocationDailyAggregates :many
-- As DailyAggregates, for every active animal at a location.
WITH latest AS (
    SELECT DISTINCT ON (o.animal_scheme, o.animal_id, o.metric_scheme, o.metric_id, o.hour_start)
           o.animal_scheme, o.animal_id, o.metric_scheme, o.metric_id, o.hour_start, o.value
    FROM observation_hourly o
    JOIN animals a ON a.animal_scheme = o.animal_scheme AND a.animal_id = o.animal_id
    WHERE a.location_scheme = @location_scheme AND a.location_id = @location_id AND NOT a.is_deleted
      AND o.hour_start >= @from_time AND o.hour_start < @to_time
    ORDER BY o.animal_scheme, o.animal_id, o.metric_scheme, o.metric_id, o.hour_start,
             o.meta_modified DESC, o.ingest_seq DESC
)
SELECT animal_scheme, animal_id, metric_scheme, metric_id,
       date_trunc('day', hour_start, 'UTC')::timestamptz AS day,
       count(*)::int AS hours,
       sum(value)::double precision AS total
FROM latest
GROUP BY animal_scheme, animal_id, metric_scheme, metric_id, day
ORDER BY animal_scheme, animal_id, day, metric_scheme, metric_id;
