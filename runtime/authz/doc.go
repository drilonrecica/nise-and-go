// Package authz holds the primitives an application authorizes with:
// permissions, the sets of them a role bundles, and the closed catalog that
// says which of each exist.
//
// # Permissions are the primitive, roles are a convenience
//
// A permission names one capability — "invoices.create" — and is what code
// checks. A role is a name for a set of them, and exists so that granting
// somebody "billing clerk" is one decision instead of eleven. Nothing in this
// package lets a check ask about a role: a use case that asked "is this person
// an admin" would have encoded an organization chart in a place that has to be
// rewritten every time the chart changes, and would give the wrong answer for
// every deployment whose chart is different.
//
// # The catalog is closed
//
// Every permission an application uses is declared once, in a [Catalog], and a
// role may only bundle permissions the catalog declares. Without that, a typo
// in a role definition grants a permission nothing ever checks — the grant
// looks right in a review, the check looks right in a review, and the two never
// meet. [NewCatalog] refuses it instead.
//
// The same rule is why there are no wildcards. A permission pattern is how an
// accidental grant becomes invisible: nobody reviewing "invoices.*" can see
// what it will mean after the next feature lands.
//
// # What this package does not do
//
// It holds no state, reads no database, and makes no decisions about a request.
// Which permissions a person has is the application's data, and whether a use
// case requires one is the application's code. This package is what both of
// those are written in terms of.
package authz
