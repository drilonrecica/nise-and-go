package pagination

import (
	"errors"
	"fmt"
	"net/url"
)

// Query parameter names offset reporting pagination uses. They are
// deliberately different from the cursor parameters: a report is a separate
// contract, not a mode of cursor paging, and a request that mixes the two is
// refused rather than resolved.
const (
	// PageParam is the 1-based page number.
	PageParam = "page"
	// SizeParam is the requested page size.
	SizeParam = "size"
)

// Ceilings every report is held to.
const (
	// MaxReportPage is the largest page number a request may name.
	MaxReportPage = 100000
	// MaxReportOffset is the deepest row a report may start at. Past this
	// point the database is scanning and discarding more rows than it
	// returns, and the honest answer is a narrower filter or an export, not
	// a slower page.
	MaxReportOffset = 1000000
)

// Errors reported by report parsing.
var (
	// ErrReportLimits reports a [ReportLimits] configuration outside the
	// permitted bounds.
	ErrReportLimits = errors.New("report limits are outside the permitted bounds")
	// ErrInvalidPage reports a page parameter that is repeated, non-numeric,
	// zero, negative, or above [MaxReportPage].
	ErrInvalidPage = errors.New("page parameter is not a page number within bounds")
	// ErrInvalidSize reports a size parameter that is repeated, non-numeric,
	// zero, negative, or above the configured maximum.
	ErrInvalidSize = errors.New("size parameter is not a page size within bounds")
	// ErrReportTooDeep reports a page whose first row is past the
	// configured offset ceiling.
	ErrReportTooDeep = errors.New("report page starts beyond the offset ceiling")
	// ErrMixedPagination reports a request combining report and cursor
	// parameters.
	ErrMixedPagination = errors.New("report and cursor pagination parameters cannot be combined")
	// ErrNegativeTotal reports a total row count below zero.
	ErrNegativeTotal = errors.New("report total must not be negative")
	// ErrUnboundedReport reports a hand-built [Report] whose page number or
	// size is outside the bounds ParseReport enforces.
	ErrUnboundedReport = errors.New("report page number or size is outside the permitted bounds")
)

// ReportLimits is one report's page-size and depth policy.
//
// It is a separate type from [Limits] because offset pagination is a separate
// contract: it has a bound cursor paging does not need, and sharing one type
// would invite a collection to be configured for both.
//
// The zero value is unusable; construct one with [NewReportLimits].
type ReportLimits struct {
	defaultSize int
	maxSize     int
	maxOffset   int64
}

// NewReportLimits validates one report's default size, maximum size, and
// deepest permitted offset.
func NewReportLimits(defaultSize, maxSize int, maxOffset int64) (ReportLimits, error) {
	switch {
	case defaultSize <= 0:
		return ReportLimits{}, fmt.Errorf("%w: default %d must be greater than zero", ErrReportLimits, defaultSize)
	case maxSize <= 0:
		return ReportLimits{}, fmt.Errorf("%w: maximum %d must be greater than zero", ErrReportLimits, maxSize)
	case maxSize > MaxPageLimit:
		return ReportLimits{}, fmt.Errorf("%w: maximum size %d exceeds %d", ErrReportLimits, maxSize, MaxPageLimit)
	case defaultSize > maxSize:
		return ReportLimits{}, fmt.Errorf("%w: default %d exceeds maximum %d", ErrReportLimits, defaultSize, maxSize)
	case maxOffset < 0:
		return ReportLimits{}, fmt.Errorf("%w: maximum offset %d must not be negative", ErrReportLimits, maxOffset)
	case maxOffset > MaxReportOffset:
		return ReportLimits{}, fmt.Errorf("%w: maximum offset %d exceeds %d", ErrReportLimits, maxOffset, MaxReportOffset)
	}
	return ReportLimits{defaultSize: defaultSize, maxSize: maxSize, maxOffset: maxOffset}, nil
}

// Default returns the page size used when a request names none.
func (l ReportLimits) Default() int { return l.defaultSize }

// Max returns the largest page size a request may ask for.
func (l ReportLimits) Max() int { return l.maxSize }

// MaxOffset returns the deepest row a request may start at.
func (l ReportLimits) MaxOffset() int64 { return l.maxOffset }

// Report is one parsed offset-pagination request.
//
// It reports a position, not a count: [Report.Offset] and [Report.Size] go
// into the query, and the total comes back from it. Nothing here knows how
// many rows exist until [Report.Totals] is told.
type Report struct {
	// Number is the 1-based page number.
	Number int
	// Size is the resolved page size.
	Size int
}

// Offset is the number of rows to skip. It is int64 because the product of
// two in-range int values is not necessarily an in-range int on a 32-bit
// platform, and this value goes straight into SQL.
func (r Report) Offset() int64 {
	return int64(r.Number-1) * int64(r.Size)
}

// Totals is what a report page reports back about the whole result set.
type Totals struct {
	// Page is the 1-based page number that was read.
	Page int
	// Size is the page size that was used.
	Size int
	// Total is the number of rows matching the query, across every page.
	Total int64
	// TotalPages is how many pages of Size the result set fills. It is zero
	// for an empty result set, so "page 1 of 0" is a legible empty report
	// rather than a page that does not exist.
	TotalPages int64
	// HasMore reports whether a page after this one exists.
	HasMore bool
}

// Totals computes the reporting metadata for a known total row count.
//
// The division is done on int64 without ever forming Page*Size as an int, so
// a large page number cannot overflow its way to a wrong page count.
//
// It re-checks the report's own bounds because a Report is a plain struct a
// caller can build by hand. That check is what lets a transport layer convert
// Page and Size to int32 for the wire without a silent wrap.
func (r Report) Totals(total int64) (Totals, error) {
	if total < 0 {
		return Totals{}, fmt.Errorf("%w: %d", ErrNegativeTotal, total)
	}
	if r.Number < 1 || r.Number > MaxReportPage {
		return Totals{}, fmt.Errorf("%w: page %d is outside 1..%d", ErrUnboundedReport, r.Number, MaxReportPage)
	}
	if r.Size < 1 || r.Size > MaxPageLimit {
		return Totals{}, fmt.Errorf("%w: size %d is outside 1..%d", ErrUnboundedReport, r.Size, MaxPageLimit)
	}
	size := int64(r.Size)
	pages := total / size
	if total%size != 0 {
		pages++
	}
	return Totals{
		Page:       r.Number,
		Size:       r.Size,
		Total:      total,
		TotalPages: pages,
		HasMore:    int64(r.Number) < pages,
	}, nil
}

// ParseReport reads the page and size parameters of one report request.
//
// A request naming neither reads page 1 at the collection's default size. A
// request that also carries a cursor parameter is refused: the two contracts
// answer different questions, and silently honoring one of them would make
// which is honored an implementation detail.
func ParseReport(query url.Values, limits ReportLimits) (Report, error) {
	if limits.defaultSize == 0 {
		return Report{}, fmt.Errorf("%w: limits are not constructed", ErrReportLimits)
	}
	for _, name := range []string{LimitParam, AfterParam, BeforeParam} {
		if _, present := query[name]; present {
			return Report{}, fmt.Errorf("%w: %s belongs to cursor pagination", ErrMixedPagination, name)
		}
	}

	number, err := parseBoundedParameter(query, PageParam, 1, 1, MaxReportPage, ErrInvalidPage)
	if err != nil {
		return Report{}, err
	}
	size, err := parseBoundedParameter(query, SizeParam, limits.defaultSize, 1, limits.maxSize, ErrInvalidSize)
	if err != nil {
		return Report{}, err
	}

	report := Report{Number: number, Size: size}
	if offset := report.Offset(); offset > limits.maxOffset {
		return Report{}, fmt.Errorf("%w: row %d, ceiling is %d", ErrReportTooDeep, offset, limits.maxOffset)
	}
	return report, nil
}

// parseBoundedParameter resolves one optional positive integer parameter,
// refusing every form a caller could have meant two ways.
func parseBoundedParameter(query url.Values, name string, fallback, low, high int, sentinel error) (int, error) {
	raw, present, err := single(query, name)
	if err != nil {
		return 0, err
	}
	if !present {
		return fallback, nil
	}
	value, ok := decimalValue(raw, digitsIn(high))
	if !ok {
		return 0, fmt.Errorf("%w: %q is not a decimal integer", sentinel, raw)
	}
	if value < low || value > high {
		return 0, fmt.Errorf("%w: %d is outside %d..%d", sentinel, value, low, high)
	}
	return value, nil
}

func digitsIn(value int) int {
	digits := 1
	for value >= 10 {
		value /= 10
		digits++
	}
	return digits
}
