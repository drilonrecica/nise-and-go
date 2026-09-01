// Generated once by nise; owned by this application. Nise will not overwrite it.

// Package resource is the bookable equipment an organization shares.
//
// It is the CRUD half of Workbench, and the half that exists to be
// unremarkable: an instrument has a name, a description, a location, and a
// photo, and somebody with the right role can add one, edit one, or take one
// out of service.
//
// Two things about it are not unremarkable, and both are visible in the
// queries rather than here.
//
// # The tenant is never a parameter
//
// `CreateResource` writes `current_tenant()` rather than an org identifier the
// caller supplied. The transaction established the tenant with `SET LOCAL`
// before the statement ran, and the row-level-security policy's WITH CHECK
// clause is what puts it in the row. So a resource cannot be created in an
// organization the caller is not inside, whatever the caller passes — there is
// no parameter to get wrong.
//
// # Retiring is not deleting
//
// A resource is never removed. Reservations refer to it, and a retired
// resource is the answer to "what was this booking for" long after nobody can
// book it again. Retiring sets a timestamp, and the unique index on the name
// excludes retired rows — so a lab that retires "Microscope 2" and buys a
// replacement can call the new one "Microscope 2".
package resource

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/drilonrecica/nise-and-go/runtime/transaction"

	"workbench/internal/features/resource/store"
	"workbench/internal/platform/database"
)

// Bounds, matching the schema's own constraints so a violation is reported
// here with a readable message rather than by PostgreSQL with a constraint
// name.
const (
	MaxNameBytes        = 200
	MaxDescriptionBytes = 2000
	MaxLocationBytes    = 200
	MaxPhotoKeyBytes    = 512

	// DefaultPageSize and MaxPageSize bound a listing. The maximum is a limit
	// on what one request can cost the database, not a preference.
	DefaultPageSize = 50
	MaxPageSize     = 200
)

// Errors this package returns.
var (
	// ErrNotFound reports a resource that does not exist, or one outside this
	// transaction's tenant. The two are the same value: whether an identifier
	// exists is itself information, and across a tenant boundary it is the
	// information most worth withholding.
	ErrNotFound = errors.New("resource: no such resource")
	// ErrInvalid reports input that does not satisfy the schema.
	ErrInvalid = errors.New("resource: invalid resource")
	// ErrRetired reports an operation on a resource that is out of service.
	// It is distinct from ErrNotFound because the resource is visible, and
	// telling somebody it does not exist when they can see it in a list is
	// how a person concludes the software is broken.
	ErrRetired = errors.New("resource: this resource has been retired")
	// ErrNameTaken reports a name another resource in this organization is
	// already using.
	ErrNameTaken = errors.New("resource: another resource already has this name")
)

// Resource is one piece of shared equipment.
type Resource struct {
	ID          string
	OrgID       string
	Name        string
	Description string
	Location    string
	// PhotoKey is the storage key of the resource's photo, empty when it has
	// none. It is a key rather than a URL: the object is served through the
	// application's own upload lifecycle, and a URL in a row would be a
	// rendering decision stored in the database.
	PhotoKey string
	// RetiredAt is zero while the resource is in service.
	RetiredAt time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// InService reports whether the resource can still be booked.
func (r Resource) InService() bool { return r.RetiredAt.IsZero() }

// Page is one page of resources, with the cursor for the next one.
type Page struct {
	Resources []Resource
	// NextCursor is empty when this is the last page. It is opaque to a
	// caller and is not a row count: an offset shifts when somebody inserts,
	// and a shifted offset is how a listing repeats or skips a row.
	NextCursor string
	// Total is the number of resources matching the filter, across all pages.
	Total int64
}

// Resources is the use case.
type Resources struct {
	transactor *database.Transactor
}

// New builds it.
func New(transactor *database.Transactor) (*Resources, error) {
	if transactor == nil {
		return nil, errors.New("resource: a transaction runner is required")
	}
	return &Resources{transactor: transactor}, nil
}

// Details are the writable fields of a resource.
type Details struct {
	Name        string
	Description string
	Location    string
}

// Create adds a resource to the caller's organization.
func (r *Resources) Create(ctx context.Context, orgID string, details Details) (Resource, error) {
	normalized, err := validate(details)
	if err != nil {
		return Resource{}, err
	}

	var row store.Resource
	err = r.transactor.WithinTenant(ctx, orgID, transaction.Options{}, func(ctx context.Context, tx pgx.Tx) error {
		var txErr error
		row, txErr = store.New(tx).CreateResource(ctx, store.CreateResourceParams{
			Name:        normalized.Name,
			Description: normalized.Description,
			Location:    normalized.Location,
		})
		return txErr
	})
	if err != nil {
		if isNameConflict(err) {
			return Resource{}, ErrNameTaken
		}
		return Resource{}, fmt.Errorf("resource: creating: %w", err)
	}
	return toResource(row), nil
}

// Get returns one resource.
func (r *Resources) Get(ctx context.Context, orgID, resourceID string) (Resource, error) {
	id, err := parseUUID(resourceID)
	if err != nil {
		return Resource{}, ErrNotFound
	}
	var row store.Resource
	err = r.transactor.WithinTenant(ctx, orgID, transaction.Options{Access: transaction.ReadOnly},
		func(ctx context.Context, tx pgx.Tx) error {
			var txErr error
			row, txErr = store.New(tx).GetResource(ctx, store.GetResourceParams{ID: id})
			return txErr
		})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Resource{}, ErrNotFound
		}
		return Resource{}, fmt.Errorf("resource: reading: %w", err)
	}
	return toResource(row), nil
}

// Update changes a resource's details.
//
// A retired resource cannot be edited. That is not squeamishness: its name is
// no longer covered by the unique index, so allowing a rename would let two
// resources hold one name and then let the retired one be un-retired into a
// conflict the schema cannot express.
func (r *Resources) Update(ctx context.Context, orgID, resourceID string, details Details) (Resource, error) {
	normalized, err := validate(details)
	if err != nil {
		return Resource{}, err
	}
	id, err := parseUUID(resourceID)
	if err != nil {
		return Resource{}, ErrNotFound
	}

	var row store.Resource
	err = r.transactor.WithinTenant(ctx, orgID, transaction.Options{}, func(ctx context.Context, tx pgx.Tx) error {
		queries := store.New(tx)
		updated, txErr := queries.UpdateResource(ctx, store.UpdateResourceParams{
			ID:          id,
			Name:        normalized.Name,
			Description: normalized.Description,
			Location:    normalized.Location,
		})
		if errors.Is(txErr, pgx.ErrNoRows) {
			// No row matched, and the query's WHERE has two conditions. Ask
			// which one, inside the same transaction, so the caller is told
			// "retired" rather than "not found" for a resource they can see.
			return distinguishMissing(ctx, queries, id)
		}
		row = updated
		return txErr
	})
	if err != nil {
		if isNameConflict(err) {
			return Resource{}, ErrNameTaken
		}
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrRetired) {
			return Resource{}, err
		}
		return Resource{}, fmt.Errorf("resource: updating: %w", err)
	}
	return toResource(row), nil
}

// SetPhoto attaches a finalized upload to a resource, or removes the photo
// when key is empty.
//
// It takes a storage key rather than bytes: the upload lifecycle stages,
// quarantines, and finalizes the object elsewhere, and this records the result
// of that. A resource that stored the bytes would be a second copy of
// something the storage layer already owns.
func (r *Resources) SetPhoto(ctx context.Context, orgID, resourceID, key string) (Resource, error) {
	if utf8.RuneCountInString(key) > MaxPhotoKeyBytes {
		return Resource{}, fmt.Errorf("%w: the photo key is longer than %d characters", ErrInvalid, MaxPhotoKeyBytes)
	}
	id, err := parseUUID(resourceID)
	if err != nil {
		return Resource{}, ErrNotFound
	}

	photo := pgtype.Text{String: key, Valid: key != ""}
	var row store.Resource
	err = r.transactor.WithinTenant(ctx, orgID, transaction.Options{}, func(ctx context.Context, tx pgx.Tx) error {
		var txErr error
		row, txErr = store.New(tx).SetResourcePhoto(ctx, store.SetResourcePhotoParams{ID: id, PhotoKey: photo})
		return txErr
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Resource{}, ErrNotFound
		}
		return Resource{}, fmt.Errorf("resource: setting the photo: %w", err)
	}
	return toResource(row), nil
}

// Retire takes a resource out of service.
//
// It is the operation the reauthentication matrix covers, and the reason is
// visible from here: it withdraws capability from every member at once, and
// there is no undo that gives somebody back the slot they had booked.
//
// Retiring a resource that is already retired is reported rather than
// silently accepted. The alternative — moving the timestamp — would rewrite
// when it happened, which is the one fact anybody asks about afterwards.
func (r *Resources) Retire(ctx context.Context, orgID, resourceID string) (Resource, error) {
	id, err := parseUUID(resourceID)
	if err != nil {
		return Resource{}, ErrNotFound
	}
	var row store.Resource
	err = r.transactor.WithinTenant(ctx, orgID, transaction.Options{}, func(ctx context.Context, tx pgx.Tx) error {
		queries := store.New(tx)
		retired, txErr := queries.RetireResource(ctx, store.RetireResourceParams{ID: id})
		if errors.Is(txErr, pgx.ErrNoRows) {
			return distinguishMissing(ctx, queries, id)
		}
		row = retired
		return txErr
	})
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrRetired) {
			return Resource{}, err
		}
		return Resource{}, fmt.Errorf("resource: retiring: %w", err)
	}
	return toResource(row), nil
}

// ListOptions filter and page a listing.
type ListOptions struct {
	// IncludeRetired adds resources that are out of service. It is off by
	// default because the common question is "what can I book".
	IncludeRetired bool
	// Cursor is the NextCursor of a previous page, or empty for the first.
	Cursor string
	// PageSize is bounded by MaxPageSize; zero means DefaultPageSize.
	PageSize int
}

// List returns one page of resources, ordered by name.
func (r *Resources) List(ctx context.Context, orgID string, opts ListOptions) (Page, error) {
	size := opts.PageSize
	switch {
	case size <= 0:
		size = DefaultPageSize
	case size > MaxPageSize:
		size = MaxPageSize
	}

	afterName, afterIDText, err := decodeCursor(opts.Cursor)
	if err != nil {
		return Page{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	afterID, err := parseUUID(afterIDText)
	if err != nil {
		return Page{}, fmt.Errorf("%w: the page cursor is not readable", ErrInvalid)
	}

	var (
		rows  []store.Resource
		total int64
	)
	err = r.transactor.WithinTenant(ctx, orgID, transaction.Options{Access: transaction.ReadOnly},
		func(ctx context.Context, tx pgx.Tx) error {
			queries := store.New(tx)
			// One more than asked for, so "is there a next page" is answered
			// by what came back rather than by a second count that could
			// disagree with it.
			listed, txErr := queries.ListResources(ctx, store.ListResourcesParams{
				IncludeRetired: opts.IncludeRetired,
				AfterName:      afterName,
				AfterID:        afterID,
				PageSize:       int32(size) + 1, // #nosec G115 -- size is bounded by MaxPageSize.
			})
			if txErr != nil {
				return txErr
			}
			rows = listed
			total, txErr = queries.CountResources(ctx, store.CountResourcesParams{IncludeRetired: opts.IncludeRetired})
			return txErr
		})
	if err != nil {
		return Page{}, fmt.Errorf("resource: listing: %w", err)
	}

	page := Page{Total: total, Resources: make([]Resource, 0, size)}
	for i, row := range rows {
		if i == size {
			last := rows[size-1]
			page.NextCursor = encodeCursor(strings.ToLower(last.Name), uuidString(last.ID))
			break
		}
		page.Resources = append(page.Resources, toResource(row))
	}
	return page, nil
}

// distinguishMissing answers which half of a two-condition WHERE clause failed.
//
// It runs inside the caller's transaction, so the answer is consistent with
// the statement that just returned no rows rather than with the database a
// moment later.
func distinguishMissing(ctx context.Context, queries *store.Queries, id pgtype.UUID) error {
	if _, err := queries.GetResource(ctx, store.GetResourceParams{ID: id}); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return ErrRetired
}

// validate normalizes and bounds the writable fields.
//
// Whitespace is trimmed before length is measured, because a name of two
// hundred spaces is not a name and a rejection mentioning its length is not a
// useful message.
func validate(details Details) (Details, error) {
	normalized := Details{
		Name:        strings.TrimSpace(details.Name),
		Description: strings.TrimSpace(details.Description),
		Location:    strings.TrimSpace(details.Location),
	}
	if normalized.Name == "" {
		return Details{}, fmt.Errorf("%w: a resource needs a name", ErrInvalid)
	}
	for _, bound := range []struct {
		field, value string
		max          int
	}{
		{"name", normalized.Name, MaxNameBytes},
		{"description", normalized.Description, MaxDescriptionBytes},
		{"location", normalized.Location, MaxLocationBytes},
	} {
		if utf8.RuneCountInString(bound.value) > bound.max {
			return Details{}, fmt.Errorf("%w: the %s is longer than %d characters", ErrInvalid, bound.field, bound.max)
		}
	}
	return normalized, nil
}
