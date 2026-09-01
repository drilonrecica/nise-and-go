// Generated once by nise; owned by this application. Nise will not overwrite it.

// Package authorization declares this application's permissions and the roles
// that bundle them.
//
// The declarations are code, not configuration or data. A permission's meaning
// is decided by the use case that checks it, so the list has to be reviewed
// alongside that code; a role's meaning is a decision worth reviewing too, and
// one that must be identical on every replica. Only the *assignments* — which
// account holds which role — are data, in user_roles, where an administrator
// can change them without a deploy.
//
// Adding a permission is two edits in one commit: declare it here, and check it
// in the use case that needs it. The catalog refuses a role bundling a
// permission that is not declared, so the two cannot drift apart silently.
package authorization

import (
	"fmt"

	"github.com/drilonrecica/nise-and-go/runtime/authz"
)

// The permissions this application checks.
//
// Workbench's own permissions sit beside the ones the framework's surfaces
// need. The split that shapes them is between what you do to *your own*
// reservation and what you do to *somebody else's* — the first needs no
// permission at all, because it is yours, and the second is the whole reason
// the steward role exists.
var (
	// UsersRead permits reading account records other than one's own.
	UsersRead = authz.MustPermission("users.read")
	// UsersManage permits enabling, disabling, and enrolling accounts.
	UsersManage = authz.MustPermission("users.manage")
	// RolesRead permits seeing who holds which role.
	RolesRead = authz.MustPermission("roles.read")
	// RolesManage permits granting and revoking roles. It is the permission
	// that can lead to every other one, so it belongs to the smallest
	// possible number of people.
	RolesManage = authz.MustPermission("roles.manage")
	// SessionsRevoke permits ending another account's sessions.
	SessionsRevoke = authz.MustPermission("sessions.revoke")
	// AuditRead permits reading the audit log. It is separate from
	// UsersManage on purpose: the people who can change things and the
	// people who can see what was changed do not have to be the same, and in
	// some organizations must not be.
	AuditRead = authz.MustPermission("audit.read")
	// SystemRead permits seeing the About/System page: the running binary's
	// version, the framework it was built against, and which optional
	// modules it carries.
	SystemRead = authz.MustPermission("system.read")

	// ResourcesRead permits seeing the organization's bookable resources.
	// Everyone who can reserve anything needs it, because you cannot choose
	// a resource you cannot see.
	ResourcesRead = authz.MustPermission("resources.read")
	// ResourcesManage permits adding a resource and editing its details and
	// photo.
	ResourcesManage = authz.MustPermission("resources.manage")
	// ResourcesRetire permits taking a resource out of service.
	//
	// It is separate from ResourcesManage, and the separation is the point:
	// editing a description inconveniences nobody, while retiring a resource
	// withdraws capability from every member at once and cancels reservations
	// people have planned around. It is also the one Workbench action that
	// requires reauthentication — see internal/platform/reauth.
	ResourcesRetire = authz.MustPermission("resources.retire")

	// ReservationsRead permits seeing the organization's reservations,
	// including who holds them.
	//
	// This is a member permission rather than a steward one, because a shared
	// calendar you cannot read is not a shared calendar: knowing an
	// instrument is booked until Thursday is the information that stops
	// somebody walking down there to find out.
	ReservationsRead = authz.MustPermission("reservations.read")
	// ReservationsCreate permits making a reservation for yourself.
	ReservationsCreate = authz.MustPermission("reservations.create")
	// ReservationsManage permits acting on **somebody else's** reservation:
	// cancelling it, checking it out, or returning it on their behalf.
	//
	// Acting on your own needs no permission — it is yours. That asymmetry is
	// the entire reason this permission exists, and the reason the check in
	// the use case is "holder or ReservationsManage" rather than a plain
	// permission test.
	ReservationsManage = authz.MustPermission("reservations.manage")
)

// Role names this application grants.
const (
	// RoleMember is everybody who uses the equipment. It can see what exists,
	// see when it is taken, and book it.
	RoleMember = "member"
	// RoleSteward runs the shared equipment day to day: adds resources, fixes
	// details, and sorts out somebody else's reservation when they are ill,
	// away, or have left the building with the key.
	RoleSteward = "steward"
	// RoleAdministrator can do everything, including the account and role
	// management the framework's own surfaces offer.
	RoleAdministrator = "administrator"
	// RoleAuditor can read and change nothing — including the equipment. It
	// exists to make "look, do not touch" expressible, which is what makes an
	// audit role safe to grant widely: a safety officer reviewing who used
	// which instrument needs the log and the calendar, and needs no ability
	// to alter either.
	RoleAuditor = "auditor"
)

// permissions is every permission this application checks.
func permissions() []authz.Permission {
	return []authz.Permission{
		UsersRead,
		UsersManage,
		RolesRead,
		RolesManage,
		SessionsRevoke,
		AuditRead,
		SystemRead,
		ResourcesRead,
		ResourcesManage,
		ResourcesRetire,
		ReservationsRead,
		ReservationsCreate,
		ReservationsManage,
	}
}

// roles are the bundles this application grants.
//
// They nest — member ⊂ steward ⊂ administrator — because in this domain they
// genuinely do: a steward is a member who also tidies up after other people,
// and an administrator is a steward who also runs the accounts. Writing the
// nesting out rather than composing it programmatically keeps each bundle
// readable on its own, which is what the matrix test compares against.
func roles() ([]authz.Role, error) {
	member, err := authz.NewRole(RoleMember,
		ResourcesRead, ReservationsRead, ReservationsCreate)
	if err != nil {
		return nil, err
	}
	steward, err := authz.NewRole(RoleSteward,
		ResourcesRead, ResourcesManage,
		ReservationsRead, ReservationsCreate, ReservationsManage)
	if err != nil {
		return nil, err
	}
	administrator, err := authz.NewRole(RoleAdministrator,
		UsersRead, UsersManage, RolesRead, RolesManage, SessionsRevoke, AuditRead, SystemRead,
		ResourcesRead, ResourcesManage, ResourcesRetire,
		ReservationsRead, ReservationsCreate, ReservationsManage)
	if err != nil {
		return nil, err
	}
	auditor, err := authz.NewRole(RoleAuditor,
		UsersRead, RolesRead, AuditRead, ResourcesRead, ReservationsRead)
	if err != nil {
		return nil, err
	}
	return []authz.Role{member, steward, administrator, auditor}, nil
}

// Catalog builds this application's permission catalog.
func Catalog() (*authz.Catalog, error) {
	bundles, err := roles()
	if err != nil {
		return nil, fmt.Errorf("building the role bundles: %w", err)
	}
	catalog, err := authz.NewCatalog(permissions(), bundles)
	if err != nil {
		return nil, fmt.Errorf("building the permission catalog: %w", err)
	}
	return catalog, nil
}
