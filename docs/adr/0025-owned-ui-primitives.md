# ADR 0025: Own the UI primitives; take no headless component dependency

- **Status:** Accepted
- **Date:** 2026-08-31

## Context

The blueprint calls for "a small curated set of shadcn-svelte/Bits UI
components copied into and owned by the application". shadcn-svelte's model is
copy-in ownership, which is exactly what [ADR 0003](0003-application-ownership.md)
already requires of generated code. Its components are styled wrappers around
Bits UI, which is a real runtime dependency and which supplies the parts that
are genuinely hard: focus management, roving tabindex, dismissal, portalling,
and floating-element positioning.

[ADR 0013](0013-security-headers-and-csp.md) fixes a Content Security Policy
with no `style-src-attr 'unsafe-inline'`. That is not incidental to this
decision, it decides it. A floating element — a menu, a popover, a tooltip —
is positioned by writing coordinates onto the element. When those coordinates
are written as a `style` **attribute**, which is what a Svelte `style="…"`
binding compiles to and what every Floating UI integration produces, the
browser blocks the declaration and the element renders in the wrong place with
a console violation. Only CSSOM property writes (Svelte's `style:` directive,
or `element.style.x = …`) escape the policy, and a third-party component
library cannot be audited for that property once and then trusted across
upgrades.

The generated `app.html` has carried the prose form of this rule since the
frontend existed: "No literal style attribute may appear in this file or in a
component." A dependency whose correctness depends on that rule being false is
not a dependency this policy can hold.

The two ways out were both worse than the third. Adding
`AllowInlineStyleAttributes` to the document policy would weaken a security
control to make a dependency fit, which the project's own implementation rules
forbid outright. Keeping the dependency and hoping its current version happens
not to emit style attributes makes every upgrade a security review of somebody
else's rendering internals.

## Decision

The generated application owns its UI primitives outright. There is no
`bits-ui`, no `shadcn-svelte`, and no headless component library in the
generated `package.json`.

The curated set lives in `frontend/src/lib/components/ui/` as ordinary Svelte 5
components: `Alert`, `Badge`, `Button`, `Checkbox`, `ConfirmDialog`, `Dialog`,
`Field`, `Icon`, `Input`, `Menu`, `Pagination`, `Select`, `Skeleton`,
`Spinner`, `Table`, `Textarea`, `Toaster`, `Tooltip`, and the `toast` module.
It is deliberately small: the set an operational application needs, not the set
a library ships.

Three rules keep that affordable:

1. **Use the platform where the platform is better.** Modal surfaces are
   `<dialog>` with `showModal()`, which supplies the focus trap, the inert
   background, Escape, and the top layer. Selects are `<select>`. Checkboxes
   are `<input type="checkbox">` styled with `accent-color`. Each of those is
   something a replacement has to reimplement and never implements as well.
2. **Position with CSS, not with a positioning library.** The floating
   elements in the set are anchored to their trigger by ordinary absolute
   positioning. A component that genuinely needs collision detection is a
   component to build for that case, with `style:` directives, not a reason to
   take the dependency for all of them.
3. **Follow the ARIA authoring practices where behavior has to be written.**
   `Menu` is the one component with a real keyboard model, and it is written
   out in full rather than approximated.

Where a shadcn-svelte idiom is a good one — the file layout, the variant
naming, the `class` prop escape hatch — it is kept, so the ecosystem's
conventions still apply to this code.

## Consequences

- The Content Security Policy stays as [ADR 0013](0013-security-headers-and-csp.md)
  defines it. No waiver, and no per-upgrade audit of a third party's rendering
  internals.
- The generated `package.json` keeps zero runtime dependencies. Everything it
  pins is a build or test tool.
- The application owns every line of its component set, which is what
  [ADR 0003](0003-application-ownership.md) intends and what makes deleting or
  replacing a component an ordinary edit.
- The cost is real: accessibility behavior that Bits UI would have supplied is
  now this project's to get right and to keep right. It is paid down by
  pinning each property in a test rather than in a review comment, by
  preferring platform elements that cannot regress, and by keeping the set
  small enough to audit.
- Components that need collision-aware positioning, virtualised lists, or drag
  and drop are not in the set. An application that needs one adds it directly;
  the Nise allowlist does not govern an application owner's own dependencies.
- This is revisited if a headless library appears whose floating elements
  position through CSSOM writes rather than style attributes, or if the
  application-owned set grows past the point where auditing it is cheaper than
  auditing a dependency.
