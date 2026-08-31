# modules

Compile-time optional modules recorded in the project recipe and wired with
explicit generated code. The V0.1 list is fixed in `DECISIONS.md`:
organizations/RLS, TOTP, notifications (+SSE), uploads/storage.

Each module is two things and nothing else: a Go package here, holding its
primitives, and template files that render only when the module is selected. An
unselected module contributes no file, no import, and no dead code.
`docs/adr/0022-compile-time-modules.md` records the mechanism; the first module
built to it is `totp/`.

No dynamic loading and no new modules without a decision record.
