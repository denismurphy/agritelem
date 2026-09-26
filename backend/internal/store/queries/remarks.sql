-- name: InsertRemark :one
-- Returns a row only when (meta_source, meta_source_id) is new.
INSERT INTO remarks (
    id, location_scheme, location_id, animal_scheme, animal_id, event_datetime,
    note, category, responsible, meta_source, meta_source_id, meta_creator,
    client_modified, content_hash
) VALUES (
    @id, @location_scheme, @location_id, @animal_scheme, @animal_id, @event_datetime,
    @note, @category, @responsible, @meta_source, @meta_source_id, @meta_creator,
    @client_modified, @content_hash
)
ON CONFLICT (meta_source, meta_source_id) DO NOTHING
RETURNING *;

-- name: GetRemarkBySource :one
SELECT * FROM remarks WHERE meta_source = @meta_source AND meta_source_id = @meta_source_id;

-- name: ListRemarks :many
SELECT * FROM remarks
WHERE location_scheme = @location_scheme
  AND location_id = @location_id
  AND modified >= @modified_from
  AND (sqlc.narg(animal_scheme)::text IS NULL OR animal_scheme = sqlc.narg(animal_scheme))
  AND (sqlc.narg(animal_id)::text IS NULL OR animal_id = sqlc.narg(animal_id))
  AND (modified, id) > (@after_modified::timestamptz, @after_id::uuid)
ORDER BY modified, id
LIMIT @page_limit;

-- name: AnimalsWithRemarkSince :many
SELECT DISTINCT animal_scheme, animal_id
FROM remarks
WHERE location_scheme = @location_scheme AND location_id = @location_id
  AND category = @category AND event_datetime >= @since;
