// Generated once by nise; owned by this application. Nise will not overwrite it.

package httpapi

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/drilonrecica/nise-and-go/runtime/authz"
	"github.com/drilonrecica/nise-and-go/runtime/pagination"

	"workbench/internal/features/organizations"
	"workbench/internal/features/reservation"
	"workbench/internal/features/resource"
	"workbench/internal/platform/authorization"
	"workbench/internal/platform/httpapi/openapigen"
	"workbench/internal/platform/httpapi/problem"
)

// The HTTP surface of Workbench's own two features.
//
// It contains no rules. Every decision — who may do what, whether a window is
// free, what a state transition requires — belongs to the use cases, and this
// file translates between them and the wire. When something here looks like it
// is deciding, it is a bug.
//
// # The tenant is a path segment
//
// `/orgs/{orgId}/resources`, not `/resources`. A person can belong to more
// than one organization, and a request that inferred which one from the
// session would be a request whose meaning depends on data the caller cannot
// see — and whose meaning changes when somebody adds them to a second one.
//
// The tenant is therefore named, and membership is verified **inside** it:
// `organizations.MemberOf` runs in a transaction that established the named
// tenant, so row-level security is what makes a non-member's lookup return
// nothing. A caller who is not in an organization gets exactly the answer a
// caller naming one that does not exist gets, because which of the two it was
// is itself information.

// Tenancy resolves the caller's membership of an organization.
//
// An interface declared here rather than a dependency on the organizations use
// case, because what this file needs is one question answered.
type Tenancy interface {
	MemberOf(ctx context.Context, orgID, userID string) (organizations.Member, error)
}

// caller is a request's identity and tenant, resolved once.
type caller struct {
	userID string
	orgID  string
	// role is the caller's role inside this organization, as recorded in
	// organization_members. It is not a permission — permissions come from
	// the authorization catalog — and it is carried so a handler can say what
	// it refused on.
	role string
}

// requireMember resolves the session, then the caller's membership of the
// named organization.
//
// The order matters: an unauthenticated request is 401 whatever tenant it
// names, and a request from somebody outside the tenant is 404 rather than 403
// — telling them the organization exists is telling them something about a
// tenant they are not in.
func (s *Server) requireMember(ctx context.Context, orgID string) (caller, error) {
	session, err := requireSession(ctx)
	if err != nil {
		return caller{}, err
	}
	if s.tenancy == nil {
		return caller{}, errors.New("httpapi: no tenancy resolver is configured")
	}
	member, err := s.tenancy.MemberOf(ctx, orgID, session.UserID)
	if err != nil {
		if errors.Is(err, organizations.ErrNotMember) || errors.Is(err, organizations.ErrNotFound) {
			return caller{}, problem.Fail(problem.NotFound())
		}
		return caller{}, err
	}
	return caller{userID: session.UserID, orgID: orgID, role: member.Role}, nil
}

// requirePermission is the permission check, kept separate from membership.
//
// The two answer different questions and neither substitutes for the other: a
// member without `resources.manage` may read the equipment and not add any,
// and somebody holding `resources.manage` may not use it inside an
// organization they are not in.
func requirePermission(ctx context.Context, permission authz.Permission) error {
	if err := authorization.Require(ctx, permission); err != nil {
		return problem.Wrap(problem.PermissionDenied(), err)
	}
	return nil
}

// mayManageReservations reports whether the caller holds the permission to act
// on somebody else's reservation. It is not an error when they do not — acting
// on your own needs nothing — so this returns a boolean rather than refusing.
func mayManageReservations(ctx context.Context) bool {
	return authorization.Require(ctx, authorization.ReservationsManage) == nil
}

// --- Resources ------------------------------------------------------------

// resourceCollection is the cursor binding's resource name.
//
// It includes the tenant, because the binding fingerprints what a page's
// result set depends on: two organizations' listings are different result
// sets, and a cursor issued for one must not page into the other.
func resourceCollection(orgID string) string { return "/orgs/" + orgID + "/resources" }

func (s *Server) ListResources(ctx context.Context, request openapigen.ListResourcesRequestObject) (openapigen.ListResourcesResponseObject, error) {
	if _, err := s.requireMember(ctx, string(request.OrgId)); err != nil {
		return nil, err
	}
	if err := requirePermission(ctx, authorization.ResourcesRead); err != nil {
		return nil, err
	}

	includeRetired := request.Params.IncludeRetired != nil && *request.Params.IncludeRetired
	filters := url.Values{}
	filters.Set("include_retired", strconv.FormatBool(includeRetired))
	binding := pagination.NewBinding(resourceCollection(string(request.OrgId)), filters)

	page, err := s.parseForward(binding, resourceCursorValues(request.Params), resource.DefaultPageSize, resource.MaxPageSize)
	if err != nil {
		return nil, err
	}

	result, err := s.resources.List(ctx, string(request.OrgId), resource.ListOptions{
		IncludeRetired: includeRetired,
		Cursor:         cursorText(page),
		PageSize:       page.Limit,
	})
	if err != nil {
		return nil, resourceFailure(err)
	}

	issued, err := s.issueNext(binding, result.NextCursor)
	if err != nil {
		return nil, err
	}
	items := make([]openapigen.Resource, 0, len(result.Resources))
	for _, record := range result.Resources {
		items = append(items, resourceView(record))
	}
	return openapigen.ListResources200JSONResponse{Items: items, Page: issued, Total: result.Total}, nil
}

func resourceCursorValues(params openapigen.ListResourcesParams) url.Values {
	values := url.Values{}
	if params.Limit != nil {
		values.Set(pagination.LimitParam, strconv.Itoa(int(*params.Limit)))
	}
	if params.After != nil {
		values.Set(pagination.AfterParam, string(*params.After))
	}
	return values
}

func (s *Server) CreateResource(ctx context.Context, request openapigen.CreateResourceRequestObject) (openapigen.CreateResourceResponseObject, error) {
	if _, err := s.requireMember(ctx, string(request.OrgId)); err != nil {
		return nil, err
	}
	if err := requirePermission(ctx, authorization.ResourcesManage); err != nil {
		return nil, err
	}
	created, err := s.resources.Create(ctx, string(request.OrgId), resourceDetails(request.Body.Name, request.Body.Description, request.Body.Location))
	if err != nil {
		return nil, resourceFailure(err)
	}
	return openapigen.CreateResource201JSONResponse(resourceView(created)), nil
}

func (s *Server) GetResource(ctx context.Context, request openapigen.GetResourceRequestObject) (openapigen.GetResourceResponseObject, error) {
	if _, err := s.requireMember(ctx, string(request.OrgId)); err != nil {
		return nil, err
	}
	if err := requirePermission(ctx, authorization.ResourcesRead); err != nil {
		return nil, err
	}
	found, err := s.resources.Get(ctx, string(request.OrgId), string(request.ResourceId))
	if err != nil {
		return nil, resourceFailure(err)
	}
	return openapigen.GetResource200JSONResponse(resourceView(found)), nil
}

func (s *Server) UpdateResource(ctx context.Context, request openapigen.UpdateResourceRequestObject) (openapigen.UpdateResourceResponseObject, error) {
	if _, err := s.requireMember(ctx, string(request.OrgId)); err != nil {
		return nil, err
	}
	if err := requirePermission(ctx, authorization.ResourcesManage); err != nil {
		return nil, err
	}
	updated, err := s.resources.Update(ctx, string(request.OrgId), string(request.ResourceId),
		resourceDetails(request.Body.Name, request.Body.Description, request.Body.Location))
	if err != nil {
		return nil, resourceFailure(err)
	}
	return openapigen.UpdateResource200JSONResponse(resourceView(updated)), nil
}

// RetireResource is the resource-side domain command.
func (s *Server) RetireResource(ctx context.Context, request openapigen.RetireResourceRequestObject) (openapigen.RetireResourceResponseObject, error) {
	if _, err := s.requireMember(ctx, string(request.OrgId)); err != nil {
		return nil, err
	}
	if err := requirePermission(ctx, authorization.ResourcesRetire); err != nil {
		return nil, err
	}
	retired, err := s.resources.Retire(ctx, string(request.OrgId), string(request.ResourceId))
	if err != nil {
		return nil, resourceFailure(err)
	}
	return openapigen.RetireResource200JSONResponse(resourceView(retired)), nil
}

// --- Reservations ---------------------------------------------------------

func reservationCollection(orgID string) string { return "/orgs/" + orgID + "/reservations" }

func (s *Server) ListReservations(ctx context.Context, request openapigen.ListReservationsRequestObject) (openapigen.ListReservationsResponseObject, error) {
	if _, err := s.requireMember(ctx, string(request.OrgId)); err != nil {
		return nil, err
	}
	if err := requirePermission(ctx, authorization.ReservationsRead); err != nil {
		return nil, err
	}

	options, filters := reservationFilters(request.Params)
	binding := pagination.NewBinding(reservationCollection(string(request.OrgId)), filters)

	page, err := s.parseForward(binding, reservationCursorValues(request.Params), reservation.DefaultPageSize, reservation.MaxPageSize)
	if err != nil {
		return nil, err
	}
	options.Cursor = cursorText(page)
	options.PageSize = page.Limit

	result, err := s.reservations.List(ctx, string(request.OrgId), options)
	if err != nil {
		return nil, reservationFailure(err)
	}

	issued, err := s.issueNext(binding, result.NextCursor)
	if err != nil {
		return nil, err
	}
	items := make([]openapigen.Reservation, 0, len(result.Reservations))
	for _, record := range result.Reservations {
		items = append(items, reservationView(record))
	}
	return openapigen.ListReservations200JSONResponse{Items: items, Page: issued, Total: result.Total}, nil
}

// reservationFilters turns the typed parameters into use-case options and into
// the values the cursor binding fingerprints.
//
// The two are built together on purpose. The binding exists so a cursor cannot
// survive a filter change and page into a different result set — which only
// holds if every filter that reaches the query also reaches the binding, and
// building them apart is how one of them gets forgotten.
func reservationFilters(params openapigen.ListReservationsParams) (reservation.ListOptions, url.Values) {
	options := reservation.ListOptions{}
	filters := url.Values{}

	if params.ResourceId != nil {
		options.ResourceID = string(*params.ResourceId)
		filters.Set("resource_id", options.ResourceID)
	}
	if params.HolderId != nil {
		options.HolderID = string(*params.HolderId)
		filters.Set("holder_id", options.HolderID)
	}
	if params.State != nil {
		for _, state := range *params.State {
			options.States = append(options.States, reservation.State(state))
			filters.Add("state", string(state))
		}
	}
	if params.From != nil {
		options.From = *params.From
		filters.Set("from", params.From.UTC().Format(time.RFC3339Nano))
	}
	if params.To != nil {
		options.To = *params.To
		filters.Set("to", params.To.UTC().Format(time.RFC3339Nano))
	}
	return options, filters
}

func reservationCursorValues(params openapigen.ListReservationsParams) url.Values {
	values := url.Values{}
	if params.Limit != nil {
		values.Set(pagination.LimitParam, strconv.Itoa(int(*params.Limit)))
	}
	if params.After != nil {
		values.Set(pagination.AfterParam, string(*params.After))
	}
	return values
}

func (s *Server) CreateReservation(ctx context.Context, request openapigen.CreateReservationRequestObject) (openapigen.CreateReservationResponseObject, error) {
	current, err := s.requireMember(ctx, string(request.OrgId))
	if err != nil {
		return nil, err
	}
	if err := requirePermission(ctx, authorization.ReservationsCreate); err != nil {
		return nil, err
	}
	// The holder is the caller, always. A body field naming somebody else
	// would be a way to book equipment in another person's name, and the
	// steward's ability to act on an existing booking is a different thing
	// from creating one.
	created, err := s.reservations.Create(ctx, string(request.OrgId), reservation.Request{
		ResourceID: string(request.Body.ResourceId),
		HolderID:   current.userID,
		StartsAt:   request.Body.StartsAt,
		EndsAt:     request.Body.EndsAt,
		Note:       optionalString(request.Body.Note),
	})
	if err != nil {
		return nil, reservationFailure(err)
	}
	return openapigen.CreateReservation201JSONResponse(reservationView(created)), nil
}

func (s *Server) GetReservation(ctx context.Context, request openapigen.GetReservationRequestObject) (openapigen.GetReservationResponseObject, error) {
	if _, err := s.requireMember(ctx, string(request.OrgId)); err != nil {
		return nil, err
	}
	if err := requirePermission(ctx, authorization.ReservationsRead); err != nil {
		return nil, err
	}
	found, err := s.reservations.Get(ctx, string(request.OrgId), string(request.ReservationId))
	if err != nil {
		return nil, reservationFailure(err)
	}
	return openapigen.GetReservation200JSONResponse(reservationView(found)), nil
}

// CheckOutReservation is the first domain command.
func (s *Server) CheckOutReservation(ctx context.Context, request openapigen.CheckOutReservationRequestObject) (openapigen.CheckOutReservationResponseObject, error) {
	current, err := s.requireMember(ctx, string(request.OrgId))
	if err != nil {
		return nil, err
	}
	updated, err := s.reservations.CheckOut(ctx, string(request.OrgId), string(request.ReservationId), actorFrom(ctx, current))
	if err != nil {
		return nil, reservationFailure(err)
	}
	return openapigen.CheckOutReservation200JSONResponse(reservationView(updated)), nil
}

// ReturnReservation is the second.
func (s *Server) ReturnReservation(ctx context.Context, request openapigen.ReturnReservationRequestObject) (openapigen.ReturnReservationResponseObject, error) {
	current, err := s.requireMember(ctx, string(request.OrgId))
	if err != nil {
		return nil, err
	}
	var note, photo string
	if request.Body != nil {
		note = optionalString(request.Body.Note)
		photo = optionalString(request.Body.PhotoKey)
	}
	updated, err := s.reservations.Return(ctx, string(request.OrgId), string(request.ReservationId), actorFrom(ctx, current), note, photo)
	if err != nil {
		return nil, reservationFailure(err)
	}
	return openapigen.ReturnReservation200JSONResponse(reservationView(updated)), nil
}

func (s *Server) CancelReservation(ctx context.Context, request openapigen.CancelReservationRequestObject) (openapigen.CancelReservationResponseObject, error) {
	current, err := s.requireMember(ctx, string(request.OrgId))
	if err != nil {
		return nil, err
	}
	updated, err := s.reservations.Cancel(ctx, string(request.OrgId), string(request.ReservationId), actorFrom(ctx, current))
	if err != nil {
		return nil, reservationFailure(err)
	}
	return openapigen.CancelReservation200JSONResponse(reservationView(updated)), nil
}

// actorFrom builds the use case's actor from the resolved caller.
//
// The permission is read here rather than refused here, because acting on your
// own reservation needs none. Passing the boolean is what lets the use case
// apply "holder or manage" as one rule rather than making this file guess
// which reservation the caller is about to touch.
func actorFrom(ctx context.Context, current caller) reservation.Actor {
	return reservation.Actor{UserID: current.userID, MayManageOthers: mayManageReservations(ctx)}
}
