-- name: CreateResource :one
--
-- The tenant is not a parameter. It is established by the transaction with
-- SET LOCAL before this statement runs, and the row-level-security policy's
-- WITH CHECK clause is what puts it in the row — so a resource cannot be
-- created in an organization the caller is not inside, whatever the caller
-- passes.
INSERT INTO resources (org_id, name, description, location)
VALUES (current_tenant(), $1, $2, $3)
RETURNING id, org_id, name, description, location, photo_key, retired_at, created_at, updated_at;

-- name: GetResource :one
SELECT id, org_id, name, description, location, photo_key, retired_at, created_at, updated_at
FROM resources
WHERE id = $1;

-- name: UpdateResource :one
UPDATE resources
SET name = $2, description = $3, location = $4, updated_at = now()
WHERE id = $1 AND retired_at IS NULL
RETURNING id, org_id, name, description, location, photo_key, retired_at, created_at, updated_at;

-- name: SetResourcePhoto :one
UPDATE resources
SET photo_key = $2, updated_at = now()
WHERE id = $1
RETURNING id, org_id, name, description, location, photo_key, retired_at, created_at, updated_at;

-- name: RetireResource :one
--
-- Retiring is idempotent by omission rather than by construction: the WHERE
-- clause matches only a resource still in service, so a second call returns no
-- rows and the use case reports the resource as already retired instead of
-- moving the timestamp.
UPDATE resources
SET retired_at = now(), updated_at = now()
WHERE id = $1 AND retired_at IS NULL
RETURNING id, org_id, name, description, location, photo_key, retired_at, created_at, updated_at;

-- name: ListResources :many
--
-- Cursor pagination, keyed on (lower(name), id). The name is what somebody
-- browses by, and the identifier breaks ties so that two resources with the
-- same lowercased name cannot make a page boundary ambiguous — which is how a
-- cursor silently skips or repeats a row.
--
-- sqlc.arg(after_name) and sqlc.arg(after_id) are empty on the first page; the
-- comparison is written so that an empty cursor matches everything.
SELECT id, org_id, name, description, location, photo_key, retired_at, created_at, updated_at
FROM resources
WHERE (sqlc.arg(include_retired)::boolean OR retired_at IS NULL)
  AND (
    sqlc.arg(after_name)::text = ''
    OR (lower(name), id) > (sqlc.arg(after_name)::text, sqlc.arg(after_id)::uuid)
  )
ORDER BY lower(name), id
LIMIT sqlc.arg(page_size);

-- name: CountResources :one
SELECT count(*)
FROM resources
WHERE (sqlc.arg(include_retired)::boolean OR retired_at IS NULL);
