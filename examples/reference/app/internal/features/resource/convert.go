// Generated once by nise; owned by this application. Nise will not overwrite it.

package resource

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"workbench/internal/features/resource/store"
)

func toResource(row store.Resource) Resource {
	return Resource{
		ID:          uuidString(row.ID),
		OrgID:       uuidString(row.OrgID),
		Name:        row.Name,
		Description: row.Description,
		Location:    row.Location,
		PhotoKey:    row.PhotoKey.String,
		RetiredAt:   timeOf(row.RetiredAt),
		CreatedAt:   timeOf(row.CreatedAt),
		UpdatedAt:   timeOf(row.UpdatedAt),
	}
}

// cursorSeparator cannot appear in a UUID and is not a character a lowercased
// name is likely to end on, so splitting on the last one is unambiguous.
const cursorSeparator = "\x1f"

// encodeCursor packs the sort key of the last row on a page.
//
// It is base64 of "name<US>id" rather than an offset. An offset shifts when
// somebody inserts a resource earlier in the ordering, and a shifted offset is
// how a listing silently repeats or skips a row. It is encoded rather than
// plain so that a caller treats it as opaque instead of constructing one.
func encodeCursor(name, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(name + cursorSeparator + id))
}

// decodeCursor unpacks one. An empty cursor is the first page, not an error.
//
// A malformed cursor is refused rather than treated as empty: silently
// restarting from the beginning would repeat a page somebody has already
// scrolled past, which looks like the data changed.
func decodeCursor(cursor string) (name, id string, err error) {
	if cursor == "" {
		// The query compares an empty name against everything, and the
		// identifier is never read in that branch — but it still has to be a
		// well-formed UUID for the cast in the query, so the nil UUID stands
		// in.
		return "", "00000000-0000-0000-0000-000000000000", nil
	}
	decoded, decodeErr := base64.RawURLEncoding.DecodeString(cursor)
	if decodeErr != nil {
		return "", "", errors.New("the page cursor is not readable")
	}
	before, after, found := strings.Cut(string(decoded), cursorSeparator)
	if !found || before == "" || after == "" {
		return "", "", errors.New("the page cursor is not readable")
	}
	if _, parseErr := parseUUID(after); parseErr != nil {
		return "", "", errors.New("the page cursor is not readable")
	}
	return before, after, nil
}

// parseUUID converts an identifier that arrived as a string. A malformed one
// is a caller error, not a database round trip.
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

// isNameConflict reports the unique index on (org_id, lower(name)).
//
// It matches on the index name rather than on the SQLSTATE alone, because
// 23505 covers every unique constraint on the table and reporting "another
// resource already has this name" for a different one would send somebody
// looking in the wrong place.
func isNameConflict(err error) bool {
	return err != nil && strings.Contains(err.Error(), "resources_org_name_unique")
}
