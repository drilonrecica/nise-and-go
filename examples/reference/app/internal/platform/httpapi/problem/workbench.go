// Generated once by nise; owned by this application. Nise will not overwrite it.

package problem

import "net/http"

// Workbench's own refusals.
//
// They are declared here rather than in problem.go so that a reader can see at
// a glance which entries came with the framework and which this application
// added — and so that regenerating the starter catalog cannot silently drop
// them. The catalog is closed either way: a handler answers with a definition
// from it or it answers 500, and there is no path that invents a status.
//
// Each is a **conflict with the world as it is**, not with the shape of the
// request. That distinction is what decides 409 against 422, and it is worth
// stating because the two are easy to swap: retrying an unchanged 409 later
// can succeed — the window frees up, the reservation moves on — and retrying
// an unchanged 422 never can.
var (
	windowUnavailable = mustDefinition(
		"/problems/window-unavailable", "Window Unavailable", http.StatusConflict,
		"That resource is already booked for part of that window.", "window_unavailable",
	)
	wrongState = mustDefinition(
		"/problems/wrong-state", "Wrong State", http.StatusConflict,
		"The reservation is not in a state that allows this.", "wrong_state",
	)
	notStarted = mustDefinition(
		"/problems/not-started", "Not Started", http.StatusConflict,
		"The reservation's window has not started yet.", "not_started",
	)
	nameTaken = mustDefinition(
		"/problems/name-taken", "Name Taken", http.StatusConflict,
		"Another resource in this organization already has that name.", "name_taken",
	)
	resourceRetired = mustDefinition(
		"/problems/resource-retired", "Resource Retired", http.StatusConflict,
		"That resource has been taken out of service.", "resource_retired",
	)
)

// WindowUnavailable returns the double-booking refusal.
//
// It is the one answer in this catalog that comes from the database rather
// than from application code: an exclusion constraint refuses the insert,
// because under concurrency a check that reads before writing passes for both
// callers.
func WindowUnavailable() Definition { return windowUnavailable }

// WrongState returns the illegal-transition refusal. The detail names both
// states, because "you cannot do that" without saying what it currently is
// leaves the caller with nothing to try next.
func WrongState() Definition { return wrongState }

// NotStarted returns the early-check-out refusal. It is separate from
// WrongState because arriving early is a normal thing to do, and a caller who
// is told only "wrong state" would reasonably think something is broken.
func NotStarted() Definition { return notStarted }

// NameTaken returns the duplicate-resource-name refusal.
func NameTaken() Definition { return nameTaken }

// ResourceRetired returns the refusal to change a resource that is out of
// service. It is distinct from NotFound on purpose: the resource is visible in
// a listing, and telling somebody it does not exist when they can see it is
// how a person concludes the software is broken.
func ResourceRetired() Definition { return resourceRetired }
