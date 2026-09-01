-- name: CreateReservation :one
--
-- The tenant is not a parameter: it is established by the transaction and the
-- policy's WITH CHECK clause is what puts it in the row.
--
-- Nothing here checks whether the window is free. That check does not exist in
-- this file because it cannot be written correctly: under concurrency, two
-- transactions both read "free" and both insert. The exclusion constraint on
-- the table is what refuses the second one, and it is the only version of the
-- check that is true.
INSERT INTO reservations (org_id, resource_id, holder_id, during, note)
VALUES (current_tenant(), $1, $2, tstzrange(sqlc.arg(starts_at)::timestamptz, sqlc.arg(ends_at)::timestamptz, '[)'), $3)
RETURNING id, org_id, resource_id, holder_id, during, state, note, return_note,
          return_photo_key, checked_out_at, returned_at, cancelled_at, created_at, updated_at;

-- name: GetReservation :one
SELECT id, org_id, resource_id, holder_id, during, state, note, return_note,
       return_photo_key, checked_out_at, returned_at, cancelled_at, created_at, updated_at
FROM reservations
WHERE id = $1;

-- name: GetReservationForUpdate :one
--
-- The row-locking read every state transition starts with. FOR UPDATE is what
-- makes "read the state, decide, write the state" atomic; without it two
-- concurrent check-outs both read 'booked' and the second overwrites the
-- first's timestamp.
SELECT id, org_id, resource_id, holder_id, during, state, note, return_note,
       return_photo_key, checked_out_at, returned_at, cancelled_at, created_at, updated_at
FROM reservations
WHERE id = $1
FOR UPDATE;

-- name: CheckOutReservation :one
--
-- The state is in the WHERE clause as well as being checked by the use case.
-- The use case's check produces the readable refusal; this one is what makes
-- the transition atomic even if a future caller forgets to lock the row first.
UPDATE reservations
SET state = 'checked_out', checked_out_at = now(), updated_at = now()
WHERE id = $1 AND state = 'booked'
RETURNING id, org_id, resource_id, holder_id, during, state, note, return_note,
          return_photo_key, checked_out_at, returned_at, cancelled_at, created_at, updated_at;

-- name: ReturnReservation :one
UPDATE reservations
SET state = 'returned', returned_at = now(), return_note = $2,
    return_photo_key = sqlc.narg(return_photo_key), updated_at = now()
WHERE id = $1 AND state = 'checked_out'
RETURNING id, org_id, resource_id, holder_id, during, state, note, return_note,
          return_photo_key, checked_out_at, returned_at, cancelled_at, created_at, updated_at;

-- name: CancelReservation :one
UPDATE reservations
SET state = 'cancelled', cancelled_at = now(), updated_at = now()
WHERE id = $1 AND state = 'booked'
RETURNING id, org_id, resource_id, holder_id, during, state, note, return_note,
          return_photo_key, checked_out_at, returned_at, cancelled_at, created_at, updated_at;

-- name: MarkNoShows :many
--
-- The periodic sweep. A reservation still 'booked' after its window has closed
-- was never collected.
--
-- The cutoff is a parameter rather than now(), so a test can run the sweep
-- against a clock it controls instead of waiting, and so an operator can
-- replay it for a past window after an outage without it seeing a different
-- "now" than the one being repaired.
UPDATE reservations
SET state = 'no_show', updated_at = now()
WHERE state = 'booked' AND upper(during) <= sqlc.arg(cutoff)::timestamptz
RETURNING id, org_id, resource_id, holder_id, during, state, note, return_note,
          return_photo_key, checked_out_at, returned_at, cancelled_at, created_at, updated_at;

-- name: ListReservations :many
--
-- The filtered, paginated listing. Every filter is optional and expressed as
-- "the argument is empty OR it matches", so one query serves the calendar, the
-- steward view, and "my reservations" rather than three that can disagree.
--
-- Ordering is by window start then identifier: the identifier breaks ties so a
-- page boundary between two reservations starting at the same instant cannot
-- skip or repeat one.
SELECT id, org_id, resource_id, holder_id, during, state, note, return_note,
       return_photo_key, checked_out_at, returned_at, cancelled_at, created_at, updated_at
FROM reservations
WHERE (sqlc.narg(resource_id)::uuid IS NULL OR resource_id = sqlc.narg(resource_id)::uuid)
  AND (sqlc.narg(holder_id)::uuid IS NULL OR holder_id = sqlc.narg(holder_id)::uuid)
  AND (sqlc.arg(states)::text[] = '{}' OR state = ANY (sqlc.arg(states)::text[]))
  AND (sqlc.narg(window_start)::timestamptz IS NULL
       OR during && tstzrange(sqlc.narg(window_start)::timestamptz, sqlc.narg(window_end)::timestamptz, '[)'))
  AND (sqlc.arg(after_start)::timestamptz IS NULL
       OR (lower(during), id) > (sqlc.arg(after_start)::timestamptz, sqlc.arg(after_id)::uuid))
ORDER BY lower(during), id
LIMIT sqlc.arg(page_size);

-- name: CountReservations :one
SELECT count(*)
FROM reservations
WHERE (sqlc.narg(resource_id)::uuid IS NULL OR resource_id = sqlc.narg(resource_id)::uuid)
  AND (sqlc.narg(holder_id)::uuid IS NULL OR holder_id = sqlc.narg(holder_id)::uuid)
  AND (sqlc.arg(states)::text[] = '{}' OR state = ANY (sqlc.arg(states)::text[]))
  AND (sqlc.narg(window_start)::timestamptz IS NULL
       OR during && tstzrange(sqlc.narg(window_start)::timestamptz, sqlc.narg(window_end)::timestamptz, '[)'));
