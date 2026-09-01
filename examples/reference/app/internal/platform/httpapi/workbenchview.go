// Generated once by nise; owned by this application. Nise will not overwrite it.

package httpapi

import (
	"errors"
	"time"

	"github.com/drilonrecica/nise-and-go/runtime/pagination"

	"workbench/internal/features/reservation"
	"workbench/internal/features/resource"
	"workbench/internal/platform/authorization"
	"workbench/internal/platform/httpapi/openapigen"
	"workbench/internal/platform/httpapi/problem"
)

// The wire views, and the error mapping. Kept apart from the handlers so that
// reading a handler is reading what it does rather than how a timestamp is
// rendered.

func resourceDetails(name string, description, location *string) resource.Details {
	return resource.Details{
		Name:        name,
		Description: optionalString(description),
		Location:    optionalString(location),
	}
}

func resourceView(record resource.Resource) openapigen.Resource {
	view := openapigen.Resource{
		Id:          openapigen.ResourceIdValue(record.ID),
		OrgId:       openapigen.OrgIdValue(record.OrgID),
		Name:        record.Name,
		Description: record.Description,
		Location:    record.Location,
		InService:   record.InService(),
		CreatedAt:   record.CreatedAt,
		UpdatedAt:   record.UpdatedAt,
	}
	if record.PhotoKey != "" {
		view.PhotoKey = &record.PhotoKey
	}
	if !record.RetiredAt.IsZero() {
		retired := record.RetiredAt
		view.RetiredAt = &retired
	}
	return view
}

func reservationView(record reservation.Reservation) openapigen.Reservation {
	view := openapigen.Reservation{
		Id:         openapigen.ReservationIdValue(record.ID),
		OrgId:      openapigen.OrgIdValue(record.OrgID),
		ResourceId: openapigen.ResourceIdValue(record.ResourceID),
		HolderId:   openapigen.UserIdValue(record.HolderID),
		StartsAt:   record.StartsAt,
		EndsAt:     record.EndsAt,
		State:      openapigen.ReservationState(record.State),
		Note:       record.Note,
		CreatedAt:  record.CreatedAt,
		UpdatedAt:  record.UpdatedAt,
	}
	if record.ReturnNote != "" {
		view.ReturnNote = &record.ReturnNote
	}
	view.CheckedOutAt = optionalTime(record.CheckedOutAt)
	view.ReturnedAt = optionalTime(record.ReturnedAt)
	view.CancelledAt = optionalTime(record.CancelledAt)
	return view
}

// optionalTime renders a zero time as absent rather than as the zero instant.
//
// The contract says these members are absent when they have not happened, and
// "0001-01-01T00:00:00Z" is a value a client would have to know to ignore.
func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copied := value
	return &copied
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

// resourceFailure maps a resource use-case error onto the public catalog.
//
// Only the ones a caller can act on become a status they might trust;
// everything else is a defect and becomes the generic 500.
func resourceFailure(err error) error {
	switch {
	case errors.Is(err, authorization.ErrDenied):
		return problem.Wrap(problem.PermissionDenied(), err)
	case errors.Is(err, resource.ErrNotFound):
		return problem.Wrap(problem.NotFound(), err)
	case errors.Is(err, resource.ErrNameTaken):
		return problem.Wrap(problem.NameTaken(), err)
	case errors.Is(err, resource.ErrRetired):
		return problem.Wrap(problem.ResourceRetired(), err)
	case errors.Is(err, resource.ErrInvalid), errors.Is(err, resource.ErrPhotoUnavailable):
		return problem.Wrap(problem.InvalidRequest(), err)
	default:
		return err
	}
}

func reservationFailure(err error) error {
	switch {
	case errors.Is(err, authorization.ErrDenied), errors.Is(err, reservation.ErrNotHolder):
		return problem.Wrap(problem.PermissionDenied(), err)
	case errors.Is(err, reservation.ErrNotFound):
		return problem.Wrap(problem.NotFound(), err)
	// Three separate conflicts rather than one. Each is a conflict with the
	// world as it is rather than with the request's shape — retrying an
	// unchanged one later can succeed, which is what distinguishes 409 from
	// 422 — and each tells the caller a different thing to do next.
	case errors.Is(err, reservation.ErrConflict):
		return problem.Wrap(problem.WindowUnavailable(), err)
	case errors.Is(err, reservation.ErrNotStarted):
		return problem.Wrap(problem.NotStarted(), err)
	case errors.Is(err, reservation.ErrWrongState):
		return problem.Wrap(problem.WrongState(), err)
	case errors.Is(err, reservation.ErrResourceUnavailable):
		// Not 404: the reservation path exists and the caller may be in the
		// tenant. What is missing is the resource they named, and saying so
		// as an invalid request keeps 404 meaning "this URL addresses
		// nothing".
		return problem.Wrap(problem.InvalidRequest(), err)
	case errors.Is(err, reservation.ErrInvalid):
		return problem.Wrap(problem.InvalidRequest(), err)
	default:
		return err
	}
}

// parseForward parses a forward-only cursor page.
//
// Backward paging is refused rather than silently read forward: the queries
// behind these collections read one direction, and answering a different
// question than the one asked is worse than refusing it.
func (s *Server) parseForward(binding pagination.Binding, values map[string][]string, defaultSize, maxSize int) (pagination.Page, error) {
	limits, err := pagination.NewLimits(defaultSize, maxSize)
	if err != nil {
		return pagination.Page{}, err
	}
	page, err := s.cursors.ParsePage(binding, values, limits)
	if err != nil {
		return pagination.Page{}, problem.Wrap(paginationProblem(err), err)
	}
	if page.Direction == pagination.Backward {
		return pagination.Page{}, problem.Fail(problem.InvalidPagination())
	}
	return page, nil
}

// cursorText recovers the use case's own opaque cursor from the authenticated
// envelope the codec verified.
//
// Two layers, and they answer different questions. The codec's token is signed
// and bound to this collection and these filters, so a cursor cannot be
// forged, reused on another collection, or survive a filter change. The value
// inside it is the feature's own position key, which the codec neither reads
// nor needs to.
func cursorText(page pagination.Page) string {
	if !page.HasCursor || len(page.Cursor.Values) == 0 {
		return ""
	}
	return page.Cursor.Values[0]
}

// issueNext wraps a feature's own next-page cursor in an authenticated token,
// or reports that there is no next page.
func (s *Server) issueNext(binding pagination.Binding, next string) (openapigen.CursorPage, error) {
	boundaries := pageBoundaries{HasMore: next != ""}
	if next != "" {
		boundaries.Last = []string{next}
	}
	return issueCursorPage(s.cursors, binding, boundaries)
}
