// Generated once by nise; owned by this application. Nise will not overwrite it.

package reservation

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"workbench/internal/features/reservation/store"
)

func toReservation(row store.Reservation) Reservation {
	return Reservation{
		ID:             uuidString(row.ID),
		OrgID:          uuidString(row.OrgID),
		ResourceID:     uuidString(row.ResourceID),
		HolderID:       uuidString(row.HolderID),
		StartsAt:       rangeLower(row.During),
		EndsAt:         rangeUpper(row.During),
		State:          State(row.State),
		Note:           row.Note,
		ReturnNote:     row.ReturnNote,
		ReturnPhotoKey: row.ReturnPhotoKey.String,
		CheckedOutAt:   timeOf(row.CheckedOutAt),
		ReturnedAt:     timeOf(row.ReturnedAt),
		CancelledAt:    timeOf(row.CancelledAt),
		CreatedAt:      timeOf(row.CreatedAt),
		UpdatedAt:      timeOf(row.UpdatedAt),
	}
}

// params turns the caller's filters into the query's arguments.
//
// It is a method on ListOptions rather than code inside List so that the
// half-set window — a From with no To — is refused in one place. A query that
// accepted it would silently build a range ending at NULL, which overlaps
// nothing, and return an empty page that looks like "no reservations" instead
// of "your filter was wrong".
func (o ListOptions) params() (store.ListReservationsParams, int, error) {
	size := o.PageSize
	switch {
	case size <= 0:
		size = DefaultPageSize
	case size > MaxPageSize:
		size = MaxPageSize
	}

	params := store.ListReservationsParams{
		States: []string{},
		// One more than asked for, so "is there a next page" is answered by
		// what came back rather than by a count that could disagree with it.
		PageSize: int32(size) + 1, // #nosec G115 -- size is bounded by MaxPageSize.
	}

	if o.ResourceID != "" {
		id, err := parseUUID(o.ResourceID)
		if err != nil {
			return store.ListReservationsParams{}, 0, fmt.Errorf("%w: the resource filter is not an identifier", ErrInvalid)
		}
		params.ResourceID = id
	}
	if o.HolderID != "" {
		id, err := parseUUID(o.HolderID)
		if err != nil {
			return store.ListReservationsParams{}, 0, fmt.Errorf("%w: the holder filter is not an identifier", ErrInvalid)
		}
		params.HolderID = id
	}
	for _, state := range o.States {
		if !state.valid() {
			return store.ListReservationsParams{}, 0, fmt.Errorf("%w: %q is not a reservation state", ErrInvalid, state)
		}
		params.States = append(params.States, string(state))
	}

	switch {
	case o.From.IsZero() != o.To.IsZero():
		return store.ListReservationsParams{}, 0,
			fmt.Errorf("%w: a window filter needs both a start and an end", ErrInvalid)
	case !o.From.IsZero():
		if !o.To.After(o.From) {
			return store.ListReservationsParams{}, 0,
				fmt.Errorf("%w: the window filter ends before it starts", ErrInvalid)
		}
		params.WindowStart = timestamptz(o.From)
		params.WindowEnd = timestamptz(o.To)
	}

	start, id, err := decodeCursor(o.Cursor)
	if err != nil {
		return store.ListReservationsParams{}, 0, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	params.AfterStart = start
	params.AfterID = id

	return params, size, nil
}

// valid reports whether s is one of the five states the schema allows.
func (s State) valid() bool {
	switch s {
	case StateBooked, StateCheckedOut, StateReturned, StateCancelled, StateNoShow:
		return true
	default:
		return false
	}
}

func (s State) String() string { return string(s) }

const cursorSeparator = "\x1f"

// encodeCursor packs the sort key of the last row on a page: the window's
// start and the identifier that breaks ties between two starting at the same
// instant.
//
// The time is Unix nanoseconds rather than a formatted timestamp, so a cursor
// cannot be changed by a locale, a timezone database update, or a rounding
// difference between two replicas.
func encodeCursor(start time.Time, id string) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(strconv.FormatInt(start.UnixNano(), 10) + cursorSeparator + id))
}

// decodeCursor unpacks one. An empty cursor is the first page, not an error;
// a malformed one is refused rather than treated as empty, because silently
// restarting from the beginning repeats a page somebody has scrolled past and
// looks like the data changed.
func decodeCursor(cursor string) (pgtype.Timestamptz, pgtype.UUID, error) {
	if cursor == "" {
		return pgtype.Timestamptz{}, pgtype.UUID{}, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return pgtype.Timestamptz{}, pgtype.UUID{}, errors.New("the page cursor is not readable")
	}
	before, after, found := strings.Cut(string(decoded), cursorSeparator)
	if !found {
		return pgtype.Timestamptz{}, pgtype.UUID{}, errors.New("the page cursor is not readable")
	}
	nanos, err := strconv.ParseInt(before, 10, 64)
	if err != nil {
		return pgtype.Timestamptz{}, pgtype.UUID{}, errors.New("the page cursor is not readable")
	}
	id, err := parseUUID(after)
	if err != nil {
		return pgtype.Timestamptz{}, pgtype.UUID{}, errors.New("the page cursor is not readable")
	}
	return timestamptz(time.Unix(0, nanos).UTC()), id, nil
}

func parseUUID(value string) (pgtype.UUID, error) {
	var id pgtype.UUID
	if err := id.Scan(value); err != nil || !id.Valid {
		return pgtype.UUID{}, fmt.Errorf("%q is not a valid identifier", value)
	}
	return id, nil
}

func uuidString(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	value, err := id.Value()
	if err != nil {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

func timeOf(value pgtype.Timestamptz) time.Time {
	if !value.Valid {
		return time.Time{}
	}
	return value.Time.UTC()
}

func timestamptz(value time.Time) pgtype.Timestamptz {
	if value.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func rangeLower(during pgtype.Range[pgtype.Timestamptz]) time.Time { return timeOf(during.Lower) }
func rangeUpper(during pgtype.Range[pgtype.Timestamptz]) time.Time { return timeOf(during.Upper) }

// isOverlap reports the exclusion constraint that prevents double-booking.
//
// It matches the constraint's name rather than the SQLSTATE alone: 23P01
// covers every exclusion constraint on the table, and reporting "that window
// is already booked" for a different one would send somebody looking in the
// wrong place.
func isOverlap(err error) bool {
	return err != nil && strings.Contains(err.Error(), "reservations_no_overlap")
}

// isUnbookableResource reports the composite foreign key that ties a
// reservation's resource to its organization.
func isUnbookableResource(err error) bool {
	return err != nil && strings.Contains(err.Error(), "reservations_resource_same_org")
}
