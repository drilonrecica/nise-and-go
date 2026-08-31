package pagination_test

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/runtime/pagination"
)

func testReportLimits(t *testing.T) pagination.ReportLimits {
	t.Helper()
	limits, err := pagination.NewReportLimits(50, 200, 100000)
	if err != nil {
		t.Fatalf("NewReportLimits: %v", err)
	}
	return limits
}

func TestNewReportLimits(t *testing.T) {
	t.Parallel()

	limits := testReportLimits(t)
	if limits.Default() != 50 || limits.Max() != 200 || limits.MaxOffset() != 100000 {
		t.Fatalf("limits = %d/%d/%d", limits.Default(), limits.Max(), limits.MaxOffset())
	}

	tests := []struct {
		name        string
		defaultSize int
		maxSize     int
		maxOffset   int64
	}{
		{name: "zero default", defaultSize: 0, maxSize: 10, maxOffset: 100},
		{name: "zero maximum", defaultSize: 10, maxSize: 0, maxOffset: 100},
		{name: "default above maximum", defaultSize: 20, maxSize: 10, maxOffset: 100},
		{name: "size above the package ceiling", defaultSize: 10, maxSize: pagination.MaxPageLimit + 1, maxOffset: 100},
		{name: "negative offset ceiling", defaultSize: 10, maxSize: 10, maxOffset: -1},
		{name: "offset above the package ceiling", defaultSize: 10, maxSize: 10, maxOffset: pagination.MaxReportOffset + 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := pagination.NewReportLimits(tc.defaultSize, tc.maxSize, tc.maxOffset)
			if !errors.Is(err, pagination.ErrReportLimits) {
				t.Fatalf("error = %v, want ErrReportLimits", err)
			}
		})
	}
}

func TestParseReportDefaultsToTheFirstPage(t *testing.T) {
	t.Parallel()

	report, err := pagination.ParseReport(url.Values{}, testReportLimits(t))
	if err != nil {
		t.Fatalf("ParseReport: %v", err)
	}
	if report.Number != 1 || report.Size != 50 || report.Offset() != 0 {
		t.Fatalf("report = %#v, want page 1 of the default size at offset 0", report)
	}
}

func TestParseReportBoundaries(t *testing.T) {
	t.Parallel()

	limits := testReportLimits(t)

	accepted := []struct {
		query      url.Values
		wantNumber int
		wantSize   int
		wantOffset int64
	}{
		{query: url.Values{"page": {"1"}}, wantNumber: 1, wantSize: 50, wantOffset: 0},
		{query: url.Values{"page": {"2"}, "size": {"25"}}, wantNumber: 2, wantSize: 25, wantOffset: 25},
		{query: url.Values{"size": {"200"}}, wantNumber: 1, wantSize: 200, wantOffset: 0},
		{query: url.Values{"page": {"501"}, "size": {"200"}}, wantNumber: 501, wantSize: 200, wantOffset: 100000},
	}
	for _, tc := range accepted {
		t.Run("accepts "+tc.query.Encode(), func(t *testing.T) {
			t.Parallel()
			report, err := pagination.ParseReport(tc.query, limits)
			if err != nil {
				t.Fatalf("ParseReport: %v", err)
			}
			if report.Number != tc.wantNumber || report.Size != tc.wantSize || report.Offset() != tc.wantOffset {
				t.Fatalf("report = %#v (offset %d), want %d/%d at %d", report, report.Offset(), tc.wantNumber, tc.wantSize, tc.wantOffset)
			}
		})
	}

	rejected := []struct {
		name  string
		query url.Values
		want  error
	}{
		{name: "page zero", query: url.Values{"page": {"0"}}, want: pagination.ErrInvalidPage},
		{name: "negative page", query: url.Values{"page": {"-1"}}, want: pagination.ErrInvalidPage},
		{name: "page above the ceiling", query: url.Values{"page": {"100001"}}, want: pagination.ErrInvalidPage},
		{name: "page with a leading zero", query: url.Values{"page": {"01"}}, want: pagination.ErrInvalidPage},
		{name: "signed page", query: url.Values{"page": {"+1"}}, want: pagination.ErrInvalidPage},
		{name: "page is not a number", query: url.Values{"page": {"first"}}, want: pagination.ErrInvalidPage},
		{name: "page is a huge numeral", query: url.Values{"page": {strings.Repeat("9", 40)}}, want: pagination.ErrInvalidPage},
		{name: "page overflows an int64", query: url.Values{"page": {fmt.Sprint(uint64(math.MaxInt64))}}, want: pagination.ErrInvalidPage},
		{name: "size zero", query: url.Values{"size": {"0"}}, want: pagination.ErrInvalidSize},
		{name: "size above the maximum", query: url.Values{"size": {"201"}}, want: pagination.ErrInvalidSize},
		{name: "size is not a number", query: url.Values{"size": {"all"}}, want: pagination.ErrInvalidSize},
		{name: "repeated page", query: url.Values{"page": {"1", "2"}}, want: pagination.ErrRepeatedParameter},
		{name: "repeated size", query: url.Values{"size": {"1", "2"}}, want: pagination.ErrRepeatedParameter},
		{name: "page present but empty", query: url.Values{"page": {""}}, want: pagination.ErrEmptyParameter},
		{name: "size present but empty", query: url.Values{"size": {""}}, want: pagination.ErrEmptyParameter},
		{name: "one page too deep", query: url.Values{"page": {"502"}, "size": {"200"}}, want: pagination.ErrReportTooDeep},
		{name: "far too deep", query: url.Values{"page": {"100000"}, "size": {"200"}}, want: pagination.ErrReportTooDeep},
		{name: "cursor limit alongside", query: url.Values{"page": {"1"}, "limit": {"10"}}, want: pagination.ErrMixedPagination},
		{name: "cursor after alongside", query: url.Values{"page": {"1"}, "after": {"nc1.a.b.c"}}, want: pagination.ErrMixedPagination},
		{name: "cursor before alongside", query: url.Values{"before": {"nc1.a.b.c"}}, want: pagination.ErrMixedPagination},
	}
	for _, tc := range rejected {
		t.Run("rejects "+tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := pagination.ParseReport(tc.query, limits); !errors.Is(err, tc.want) {
				t.Fatalf("ParseReport(%v): error = %v, want %v", tc.query, err, tc.want)
			}
		})
	}

	t.Run("unconstructed limits", func(t *testing.T) {
		t.Parallel()
		if _, err := pagination.ParseReport(url.Values{}, pagination.ReportLimits{}); !errors.Is(err, pagination.ErrReportLimits) {
			t.Fatalf("error = %v, want ErrReportLimits", err)
		}
	})
}

func TestReportOffsetCannotOverflow(t *testing.T) {
	t.Parallel()

	// Number and Size are both individually in range for an int on every
	// supported platform, but their product is not. Offset returns int64 so
	// that a value built this way is still the number the database is asked
	// for rather than a wrapped one.
	report := pagination.Report{Number: pagination.MaxReportPage, Size: pagination.MaxPageLimit}
	if got, want := report.Offset(), int64(pagination.MaxReportPage-1)*int64(pagination.MaxPageLimit); got != want {
		t.Fatalf("Offset = %d, want %d", got, want)
	}
	if report.Offset() < 0 {
		t.Fatalf("Offset overflowed to %d", report.Offset())
	}
}

func TestReportTotals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		report         pagination.Report
		total          int64
		wantPages      int64
		wantHasMore    bool
		wantReportSize int
	}{
		{name: "empty result set", report: pagination.Report{Number: 1, Size: 25}, total: 0, wantPages: 0, wantHasMore: false, wantReportSize: 25},
		{name: "one partial page", report: pagination.Report{Number: 1, Size: 25}, total: 3, wantPages: 1, wantHasMore: false, wantReportSize: 25},
		{name: "exactly one page", report: pagination.Report{Number: 1, Size: 25}, total: 25, wantPages: 1, wantHasMore: false, wantReportSize: 25},
		{name: "one row into the second page", report: pagination.Report{Number: 1, Size: 25}, total: 26, wantPages: 2, wantHasMore: true, wantReportSize: 25},
		{name: "last page", report: pagination.Report{Number: 2, Size: 25}, total: 26, wantPages: 2, wantHasMore: false, wantReportSize: 25},
		{name: "page past the end", report: pagination.Report{Number: 9, Size: 25}, total: 26, wantPages: 2, wantHasMore: false, wantReportSize: 25},
		{name: "exact multiple", report: pagination.Report{Number: 4, Size: 10}, total: 40, wantPages: 4, wantHasMore: false, wantReportSize: 10},
		{name: "very large total", report: pagination.Report{Number: 1, Size: 200}, total: math.MaxInt64 - 1, wantPages: (math.MaxInt64-1)/200 + 1, wantHasMore: true, wantReportSize: 200},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			totals, err := tc.report.Totals(tc.total)
			if err != nil {
				t.Fatalf("Totals: %v", err)
			}
			if totals.TotalPages != tc.wantPages {
				t.Errorf("TotalPages = %d, want %d", totals.TotalPages, tc.wantPages)
			}
			if totals.HasMore != tc.wantHasMore {
				t.Errorf("HasMore = %t, want %t", totals.HasMore, tc.wantHasMore)
			}
			if totals.Page != tc.report.Number || totals.Size != tc.wantReportSize || totals.Total != tc.total {
				t.Errorf("totals = %#v, want the request echoed back", totals)
			}
			if totals.TotalPages < 0 {
				t.Errorf("TotalPages overflowed to %d", totals.TotalPages)
			}
		})
	}

	if _, err := (pagination.Report{Number: 1, Size: 10}).Totals(-1); !errors.Is(err, pagination.ErrNegativeTotal) {
		t.Errorf("Totals(-1): error = %v, want ErrNegativeTotal", err)
	}

	// A Report is a plain struct. Totals re-checks its bounds so a transport
	// layer can narrow Page and Size to int32 without a silent wrap.
	unbounded := []pagination.Report{
		{Number: 0, Size: 10},
		{Number: -1, Size: 10},
		{Number: math.MaxInt32, Size: 10},
		{Number: 1, Size: 0},
		{Number: 1, Size: -5},
		{Number: 1, Size: pagination.MaxPageLimit + 1},
	}
	for _, report := range unbounded {
		if _, err := report.Totals(10); !errors.Is(err, pagination.ErrUnboundedReport) {
			t.Errorf("Totals for %#v: error = %v, want ErrUnboundedReport", report, err)
		}
	}
}

func TestReportParameterNamesAreSeparateFromTheCursorOnes(t *testing.T) {
	t.Parallel()

	if pagination.PageParam != "page" || pagination.SizeParam != "size" {
		t.Fatalf("report parameter names = %q/%q", pagination.PageParam, pagination.SizeParam)
	}
	for _, cursorName := range []string{pagination.LimitParam, pagination.AfterParam, pagination.BeforeParam} {
		if cursorName == pagination.PageParam || cursorName == pagination.SizeParam {
			t.Fatalf("cursor parameter %q collides with a report parameter", cursorName)
		}
	}
}
