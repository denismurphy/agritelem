-- name: UpsertLocation :exec
INSERT INTO locations (scheme, id, name) VALUES (@scheme, @id, @name)
ON CONFLICT (scheme, id) DO UPDATE SET name = EXCLUDED.name;

-- name: UpsertAnimal :exec
INSERT INTO animals (
    animal_scheme, animal_id, location_scheme, location_id, specie, gender,
    birth_date, name, management_tag, breed_scheme, breed_id, status, meta_source, meta_source_id
) VALUES (
    @animal_scheme, @animal_id, @location_scheme, @location_id, @specie, @gender,
    @birth_date, @name, @management_tag, @breed_scheme, @breed_id, @status, @meta_source, @meta_source_id
)
ON CONFLICT (animal_scheme, animal_id) DO UPDATE SET
    location_scheme = EXCLUDED.location_scheme,
    location_id     = EXCLUDED.location_id,
    specie          = EXCLUDED.specie,
    gender          = EXCLUDED.gender,
    birth_date      = EXCLUDED.birth_date,
    name            = EXCLUDED.name,
    management_tag  = EXCLUDED.management_tag,
    breed_scheme    = EXCLUDED.breed_scheme,
    breed_id        = EXCLUDED.breed_id,
    status          = EXCLUDED.status,
    is_deleted      = false,
    modified        = now();

-- name: ListAnimals :many
-- Keyset page ordered by (modified, animal_scheme, animal_id). Includes
-- tombstones so that synchronising clients learn about deletions.
SELECT * FROM animals
WHERE location_scheme = @location_scheme
  AND location_id = @location_id
  AND modified >= @modified_from
  AND (modified, animal_scheme, animal_id) > (@after_modified::timestamptz, @after_scheme::text, @after_id::text)
ORDER BY modified, animal_scheme, animal_id
LIMIT @page_limit;

-- name: ActiveAnimalsIn :many
-- Which of the given animals exist, are not deleted, and belong to the location.
-- @schemes and @ids are parallel arrays of equal length.
SELECT animal_scheme, animal_id
FROM animals
WHERE (animal_scheme, animal_id) IN (SELECT unnest(@schemes::text[]), unnest(@ids::text[]))
  AND location_scheme = @location_scheme
  AND location_id = @location_id
  AND NOT is_deleted;

-- name: ActiveAnimalsAt :many
SELECT * FROM animals
WHERE location_scheme = @location_scheme AND location_id = @location_id AND NOT is_deleted
ORDER BY animal_scheme, animal_id;

-- name: InsertDevice :exec
INSERT INTO devices (device_id, location_scheme, location_id, serial, name)
VALUES (@device_id, @location_scheme, @location_id, @serial, @name)
ON CONFLICT (device_id) DO UPDATE SET serial = EXCLUDED.serial, name = EXCLUDED.name, modified = now();

-- name: AssignDevice :exec
-- Records the assignment and marks the device modified in one statement, so
-- clients syncing devices with meta-modified-from see the move.
WITH assigned AS (
    INSERT INTO device_assignments (device_id, animal_scheme, animal_id, during)
    VALUES (@device_id, @animal_scheme, @animal_id, tstzrange(@valid_from::timestamptz, sqlc.narg(valid_to)::timestamptz, '[)'))
    RETURNING device_id
)
UPDATE devices SET modified = now()
WHERE device_id IN (SELECT device_id FROM assigned);

-- name: ListDevices :many
SELECT device_id, serial, name, modified
FROM devices
WHERE location_scheme = @location_scheme
  AND location_id = @location_id
  AND modified >= @modified_from
ORDER BY device_id;

-- name: CurrentAssignments :many
-- Assignments at a location that contain @at. upper(during) is NULL for an
-- open-ended assignment, so it is reported as open_ended instead.
SELECT a.device_id, a.animal_scheme, a.animal_id,
       lower(a.during)::timestamptz AS valid_from,
       upper_inf(a.during)::boolean AS open_ended,
       COALESCE(upper(a.during), lower(a.during))::timestamptz AS valid_to
FROM device_assignments a
JOIN devices d ON d.device_id = a.device_id
WHERE d.location_scheme = @location_scheme
  AND d.location_id = @location_id
  AND a.during @> @at::timestamptz;
