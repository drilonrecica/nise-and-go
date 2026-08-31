package pagination

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
)

// Query parameter names this package parses. They are constants rather than
// caller-supplied strings so that every collection in an application, and its
// OpenAPI document, name the same three.
const (
	// LimitParam is the requested page size.
	LimitParam = "limit"
	// AfterParam carries a cursor issued for reading forward.
	AfterParam = "after"
	// BeforeParam carries a cursor issued for reading backward.
	BeforeParam = "before"
)

// MaxPageLimit is the largest page size any collection may configure. A
// caller may set a smaller ceiling; it may not raise this one, because the
// bound exists to keep one request from asking for an unbounded scan.
const MaxPageLimit = 200

// Errors reported by request parsing.
var (
	// ErrLimits reports a [Limits] configuration outside the permitted
	// bounds.
	ErrLimits = errors.New("page limits are outside the permitted bounds")
	// ErrInvalidLimit reports a limit parameter that is repeated,
	// non-numeric, zero, negative, or above the configured maximum.
	ErrInvalidLimit = errors.New("limit parameter is not a page size within bounds")
	// ErrConflictingCursors reports a request carrying both after and
	// before.
	ErrConflictingCursors = errors.New("after and before cannot be combined")
	// ErrRepeatedParameter reports a pagination parameter supplied more than
	// once.
	ErrRepeatedParameter = errors.New("pagination parameter is repeated")
	// ErrEmptyParameter reports a pagination parameter present with an empty
	// value, which is a client bug rather than a request for the default.
	ErrEmptyParameter = errors.New("pagination parameter is present but empty")
)

// Limits is one collection's page-size policy.
//
// The zero value is unusable; construct one with [NewLimits].
type Limits struct {
	defaultSize int
	maxSize     int
}

// NewLimits validates one collection's default and maximum page size.
func NewLimits(defaultSize, maxSize int) (Limits, error) {
	switch {
	case defaultSize <= 0:
		return Limits{}, fmt.Errorf("%w: default %d must be greater than zero", ErrLimits, defaultSize)
	case maxSize <= 0:
		return Limits{}, fmt.Errorf("%w: maximum %d must be greater than zero", ErrLimits, maxSize)
	case maxSize > MaxPageLimit:
		return Limits{}, fmt.Errorf("%w: maximum %d exceeds %d", ErrLimits, maxSize, MaxPageLimit)
	case defaultSize > maxSize:
		return Limits{}, fmt.Errorf("%w: default %d exceeds maximum %d", ErrLimits, defaultSize, maxSize)
	}
	return Limits{defaultSize: defaultSize, maxSize: maxSize}, nil
}

// Default returns the page size used when a request names none.
func (l Limits) Default() int { return l.defaultSize }

// Max returns the largest page size a request may ask for.
func (l Limits) Max() int { return l.maxSize }

// Page is one parsed pagination request.
type Page struct {
	// Limit is the resolved page size.
	Limit int
	// Direction is the direction to read in. A request with no cursor reads
	// [Forward].
	Direction Direction
	// Cursor is the verified position. It is meaningful only when HasCursor
	// is true.
	Cursor Cursor
	// HasCursor reports whether the request carried a cursor. A first page
	// does not.
	HasCursor bool
}

// ParsePage reads the limit and cursor parameters of one request.
//
// Direction comes from which parameter carried the cursor, and the cursor's
// own recorded direction must agree: a "next" cursor replayed as before would
// otherwise silently read the wrong side of a position. A request naming
// neither parameter is the first page.
//
// query must be the request's raw values. binding must fingerprint the same
// request's non-pagination filters, so that changing a filter invalidates a
// cursor rather than paging into a different result set.
func (c *Codec) ParsePage(binding Binding, query url.Values, limits Limits) (Page, error) {
	if binding.IsZero() {
		return Page{}, ErrBinding
	}
	if limits.defaultSize == 0 {
		return Page{}, fmt.Errorf("%w: limits are not constructed", ErrLimits)
	}

	limit, err := parseLimit(query, limits)
	if err != nil {
		return Page{}, err
	}

	after, hasAfter, err := single(query, AfterParam)
	if err != nil {
		return Page{}, err
	}
	before, hasBefore, err := single(query, BeforeParam)
	if err != nil {
		return Page{}, err
	}
	if hasAfter && hasBefore {
		return Page{}, ErrConflictingCursors
	}

	page := Page{Limit: limit, Direction: Forward}
	token := after
	if hasBefore {
		page.Direction = Backward
		token = before
	}
	if !hasAfter && !hasBefore {
		return page, nil
	}

	cursor, err := c.Decode(binding, token)
	if err != nil {
		return Page{}, err
	}
	if cursor.Direction != page.Direction {
		return Page{}, fmt.Errorf("%w: issued %s, used as %s", ErrCursorDirection, cursor.Direction, page.Direction)
	}
	page.Cursor = cursor
	page.HasCursor = true
	return page, nil
}

// parseLimit resolves the page size, refusing anything a caller could have
// meant two ways. An absent parameter takes the default; a present one must be
// a plain decimal within bounds, because silently clamping an out-of-range
// value hides a client bug behind a page that is quietly the wrong size.
func parseLimit(query url.Values, limits Limits) (int, error) {
	raw, present, err := single(query, LimitParam)
	if err != nil {
		return 0, err
	}
	if !present {
		return limits.defaultSize, nil
	}
	limit, ok := decimalValue(raw, 4)
	if !ok {
		return 0, fmt.Errorf("%w: %q is not a decimal integer", ErrInvalidLimit, raw)
	}
	if limit < 1 || limit > limits.maxSize {
		return 0, fmt.Errorf("%w: %d is outside 1..%d", ErrInvalidLimit, limit, limits.maxSize)
	}
	return limit, nil
}

// single returns the one value for name and whether it was present at all.
//
// A repeated parameter is refused rather than resolved by picking the first or
// the last of two contradictory instructions, and a parameter present with an
// empty value is refused rather than treated as absent: "?limit=" is a client
// building a URL wrongly, not a request for the default.
func single(query url.Values, name string) (string, bool, error) {
	values := query[name]
	switch len(values) {
	case 0:
		return "", false, nil
	case 1:
		if values[0] == "" {
			return "", false, fmt.Errorf("%w: %s", ErrEmptyParameter, name)
		}
		return values[0], true, nil
	default:
		return "", false, fmt.Errorf("%w: %s appears %d times", ErrRepeatedParameter, name, len(values))
	}
}

// decimalValue parses a plain decimal of at most maxDigits digits.
//
// It rejects the forms strconv.Atoi accepts but a page parameter should not
// carry — a sign, surrounding space, a leading zero, a Unicode digit — and
// refuses anything longer than the parameter's own ceiling before parsing, so
// an arbitrarily long numeric string is a length comparison rather than a
// conversion.
func decimalValue(raw string, maxDigits int) (int, bool) {
	if raw == "" || len(raw) > maxDigits || (len(raw) > 1 && raw[0] == '0') {
		return 0, false
	}
	for i := range len(raw) {
		if raw[i] < '0' || raw[i] > '9' {
			return 0, false
		}
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return value, true
}
