-- Generated once by nise; owned by this application. Nise will not overwrite it.
--
-- Workbench's own schema: the resources an organization shares, and the
-- reservations people make against them.
--
-- # Why the number is 00014
--
-- Migrations 00001–00009 are the framework's; 00010–00013 belong to the four
-- optional modules this application selects (notifications, organizations,
-- totp, uploads). This is the first application migration, and it can only
-- claim a fixed number because the recipe fixes the module selection. An
-- application that selects fewer modules starts lower.
--
-- # The constraint this file exists for
--
-- Two people reserving one resource for overlapping windows is the failure
-- this domain is actually about, and it is not prevented by reading before
-- writing: under concurrency both transactions read "free" and both write.
-- The exclusion constraint below is the only version of that check that is
-- true, because PostgreSQL evaluates it at write time against rows the reader
-- could not have seen.
--
-- Everything else here follows from wanting that constraint to be expressible:
-- the window is one `tstzrange` column rather than two timestamps, because
-- `&&` is an operator on ranges and "starts_at < other.ends_at AND ends_at >
-- other.starts_at" is an expression somebody has to write correctly every
-- time.

-- +goose Up

-- btree_gist lets a GiST index — which is what an exclusion constraint needs
-- for the range overlap — also handle the plain equality on resource_id. It
-- is a trusted extension, so the database owner can create it without
-- superuser.
CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE TABLE resources (
    -- org_id is second only to the key: it is the tenant column every policy
    -- on this table compares.
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id      uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    name        text        NOT NULL,
    description text        NOT NULL DEFAULT '',
    location    text        NOT NULL DEFAULT '',
    -- The storage key of the resource's photo, or NULL. It is a key rather
    -- than a URL: a URL would be a rendering decision baked into a row, and
    -- the object it names is served through the application's own upload
    -- lifecycle.
    photo_key   text,
    -- NULL means in service. Retiring is a timestamp rather than a boolean
    -- because "when did this stop being bookable" is a question somebody asks
    -- and a boolean cannot answer, and because a retired resource is never
    -- deleted — reservations refer to it.
    retired_at  timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT resources_name_length CHECK (char_length(name) BETWEEN 1 AND 200),
    CONSTRAINT resources_description_length CHECK (char_length(description) <= 2000),
    CONSTRAINT resources_location_length CHECK (char_length(location) <= 200),
    CONSTRAINT resources_photo_key_length CHECK (photo_key IS NULL OR char_length(photo_key) BETWEEN 1 AND 512),

    -- The composite key a reservation's foreign key points at. It exists only
    -- to make that reference possible; see the reservations table.
    CONSTRAINT resources_id_org_unique UNIQUE (id, org_id)
);

-- Two resources in one organization may not share a name while both are in
-- service. Retired ones are excluded: a lab that retires "Microscope 2" and
-- buys a new one should be able to call it "Microscope 2".
CREATE UNIQUE INDEX resources_org_name_unique
    ON resources (org_id, lower(name))
    WHERE retired_at IS NULL;

CREATE INDEX resources_org_idx ON resources (org_id);

CREATE TABLE reservations (
    id             uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    org_id         uuid        NOT NULL REFERENCES organizations (id) ON DELETE CASCADE,
    resource_id    uuid        NOT NULL,
    -- The person the reservation belongs to. ON DELETE RESTRICT rather than
    -- CASCADE: an account is disabled, not deleted, and a reservation that
    -- vanished with its holder would take the audit trail's subject with it.
    holder_id      uuid        NOT NULL REFERENCES users (id) ON DELETE RESTRICT,

    -- The booked window, half-open: [start, end). Half-open is what makes
    -- back-to-back reservations legal — a booking ending at 10:00 and one
    -- starting at 10:00 do not overlap — which is the behaviour anybody
    -- sharing equipment expects and the one a closed range gets wrong.
    during         tstzrange   NOT NULL,

    state          text        NOT NULL DEFAULT 'booked',
    -- What the holder said when booking, and what they said on return.
    note           text        NOT NULL DEFAULT '',
    return_note    text        NOT NULL DEFAULT '',
    -- A photo of the resource's condition at return, as a storage key.
    return_photo_key text,

    checked_out_at timestamptz,
    returned_at    timestamptz,
    cancelled_at   timestamptz,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),

    -- The state machine, written where it cannot be bypassed. The use case
    -- refuses an illegal transition with a useful message; this refuses it at
    -- all.
    CONSTRAINT reservations_state CHECK (
        state IN ('booked', 'checked_out', 'returned', 'cancelled', 'no_show')
    ),

    -- Half-open and non-empty, enforced rather than assumed. An empty range
    -- overlaps nothing, so a reservation carrying one would be invisible to
    -- the exclusion constraint below — the single way to book a resource
    -- twice, closed here.
    CONSTRAINT reservations_window_shape CHECK (
        NOT isempty(during) AND lower_inc(during) AND NOT upper_inc(during)
            AND lower(during) IS NOT NULL AND upper(during) IS NOT NULL
    ),

    -- A window nobody would book, and every window somebody would.
    CONSTRAINT reservations_window_length CHECK (
        upper(during) - lower(during) BETWEEN interval '15 minutes' AND interval '30 days'
    ),

    CONSTRAINT reservations_note_length CHECK (char_length(note) <= 2000),
    CONSTRAINT reservations_return_note_length CHECK (char_length(return_note) <= 2000),
    CONSTRAINT reservations_return_photo_key_length CHECK (
        return_photo_key IS NULL OR char_length(return_photo_key) BETWEEN 1 AND 512
    ),

    -- Each timestamp may only be set in the state that produces it. Without
    -- these a bug that wrote returned_at while leaving the state at 'booked'
    -- would be invisible until somebody read the column and believed it.
    CONSTRAINT reservations_checked_out_at_state CHECK (
        (checked_out_at IS NOT NULL) = (state IN ('checked_out', 'returned'))
    ),
    CONSTRAINT reservations_returned_at_state CHECK (
        (returned_at IS NOT NULL) = (state = 'returned')
    ),
    CONSTRAINT reservations_cancelled_at_state CHECK (
        (cancelled_at IS NOT NULL) = (state = 'cancelled')
    ),

    -- The resource is referenced together with its organization, so a
    -- reservation cannot name a resource belonging to another tenant. Row-level
    -- security already hides the other tenant's rows from a reader; this makes
    -- the write impossible rather than merely unreadable, which matters because
    -- the two failures look identical until somebody restores a backup.
    CONSTRAINT reservations_resource_same_org
        FOREIGN KEY (resource_id, org_id) REFERENCES resources (id, org_id) ON DELETE RESTRICT
);

-- The constraint this whole file is arranged around.
--
-- Two reservations for one resource may not overlap while either is live.
-- Cancelled, returned, and no-show reservations are excluded by the WHERE
-- clause: history must not block a new booking of the same slot.
ALTER TABLE reservations
    ADD CONSTRAINT reservations_no_overlap
    EXCLUDE USING gist (
        resource_id WITH =,
        during      WITH &&
    ) WHERE (state IN ('booked', 'checked_out'));

-- The calendar query: one resource's reservations in a window.
CREATE INDEX reservations_resource_during_idx ON reservations USING gist (resource_id, during);
-- "My reservations", newest first.
CREATE INDEX reservations_holder_idx ON reservations (holder_id, created_at DESC);
-- The sweep that marks no-shows reads by state and window end.
CREATE INDEX reservations_state_idx ON reservations (state, upper(during));

--
-- Row-level security. Same mechanism as the organizations module: every
-- policy compares org_id against current_tenant(), which is NULL when the
-- transaction established none — so a query that forgot returns nothing
-- rather than everything.
--
ALTER TABLE resources ENABLE ROW LEVEL SECURITY;
ALTER TABLE resources FORCE ROW LEVEL SECURITY;
ALTER TABLE reservations ENABLE ROW LEVEL SECURITY;
ALTER TABLE reservations FORCE ROW LEVEL SECURITY;

CREATE POLICY resources_tenant_isolation ON resources
    USING (org_id = current_tenant())
    WITH CHECK (org_id = current_tenant());

CREATE POLICY reservations_tenant_isolation ON reservations
    USING (org_id = current_tenant())
    WITH CHECK (org_id = current_tenant());

-- +goose Down
DROP POLICY reservations_tenant_isolation ON reservations;
DROP POLICY resources_tenant_isolation ON resources;
DROP TABLE reservations;
DROP TABLE resources;
