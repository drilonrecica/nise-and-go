package generator

import (
	"fmt"
	"regexp"
	"strings"
)

// MaxNameLen bounds a project name. It is well below every filesystem's own
// component limit (255 bytes on ext4, APFS, and NTFS) and leaves room for
// the longest path the generated tree appends underneath it, so a name that
// passes here cannot produce a path a supported filesystem rejects.
const MaxNameLen = 64

// MaxModulePathLen bounds a Go module path. The Go toolchain itself imposes
// no explicit limit, so this is a Nise bound: long enough for any real
// host/owner/repo path, short enough that an accidental paste cannot reach
// a filesystem call unbounded.
const MaxModulePathLen = 255

// nameRE is the shape a project name must have: a lowercase ASCII letter,
// then lowercase letters and digits, with single hyphens allowed between
// runs. It is simultaneously a valid Go module path element, a valid
// directory name on Linux, macOS, and Windows, and a name that survives a
// case-insensitive filesystem unchanged.
var nameRE = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// modulePathRE is the shape a Go module path must have: slash-separated
// elements of ASCII letters, digits, and the punctuation Go module paths
// use. It is deliberately narrower than the full set the Go toolchain
// tolerates — Nise writes this string into a go.mod it also has to be able
// to explain, and every real module path a generated project uses fits.
var modulePathRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~-]*(/[A-Za-z0-9._~-]+)*$`)

// goKeywords are Go's 25 reserved words. A project name becomes a directory
// under cmd/ and appears in generated Go identifiers and import aliases, so
// a name that is a keyword produces source that cannot compile.
var goKeywords = map[string]struct{}{
	"break": {}, "case": {}, "chan": {}, "const": {}, "continue": {},
	"default": {}, "defer": {}, "else": {}, "fallthrough": {}, "for": {},
	"func": {}, "go": {}, "goto": {}, "if": {}, "import": {},
	"interface": {}, "map": {}, "package": {}, "range": {}, "return": {},
	"select": {}, "struct": {}, "switch": {}, "type": {}, "var": {},
}

// windowsDeviceNames are the MS-DOS device names Windows still reserves at
// every directory level. A file or directory with one of these names — with
// or without an extension — cannot be created on Windows, so a project that
// generated cleanly on Linux would be unclonable there. They are compared
// case-insensitively, and against the portion before the first dot, because
// Windows reserves "con.txt" exactly as it reserves "con".
var windowsDeviceNames = map[string]struct{}{
	"con": {}, "prn": {}, "aux": {}, "nul": {},
	"com0": {}, "com1": {}, "com2": {}, "com3": {}, "com4": {},
	"com5": {}, "com6": {}, "com7": {}, "com8": {}, "com9": {},
	"lpt0": {}, "lpt1": {}, "lpt2": {}, "lpt3": {}, "lpt4": {},
	"lpt5": {}, "lpt6": {}, "lpt7": {}, "lpt8": {}, "lpt9": {},
}

// NameError reports a rejected project name or module path. It carries the
// offending value and the concrete action a user can take, so the CLI can
// render a cause-and-recovery message without re-deriving either.
type NameError struct {
	// Kind names what was rejected, e.g. "project name" or "module path".
	Kind string
	// Value is the input that was rejected. It is quoted by Error rather
	// than interpolated raw, so a name containing a control character or a
	// newline cannot forge a second line of terminal output.
	Value string
	// Problem states what is wrong with Value.
	Problem string
	// Action states what the user should do instead.
	Action string
}

// Error implements error.
func (e *NameError) Error() string {
	return fmt.Sprintf("%s %q %s", e.Kind, e.Value, e.Problem)
}

// ValidateName reports whether name is an acceptable project name: a name
// that is at once a valid Go module path element, a valid directory name on
// Linux, macOS, and Windows, and not a Go reserved word.
//
// Every rejection is a distinct, named rule rather than one regexp failure,
// because "invalid name" is not an actionable message for a user who typed
// a path, an uppercase letter, or a Windows device name — each of which has
// a different fix.
func ValidateName(name string) error {
	reject := func(problem, action string) error {
		return &NameError{Kind: "project name", Value: name, Problem: problem, Action: action}
	}

	if name == "" {
		return reject("is empty", "Pass a name, for example: nise new myapp")
	}
	if len(name) > MaxNameLen {
		return reject(
			fmt.Sprintf("is %d bytes, longer than the %d-byte limit", len(name), MaxNameLen),
			fmt.Sprintf("Choose a name of at most %d bytes.", MaxNameLen),
		)
	}
	if i := strings.IndexFunc(name, isControlOrNonASCII); i >= 0 {
		return reject(
			fmt.Sprintf("contains a control or non-ASCII character at byte %d", i),
			"Use only lowercase ASCII letters, digits, and hyphens.",
		)
	}
	if strings.ContainsAny(name, `/\`) {
		return reject(
			"contains a path separator",
			"Pass only the project's own name; nise creates it in the current directory.",
		)
	}
	if name == "." || name == ".." {
		return reject(
			"is a relative path element, not a name",
			"Pass a project name, for example: nise new myapp",
		)
	}
	if strings.HasPrefix(name, ".") {
		return reject(
			"starts with a dot, which hides the directory on Unix",
			"Start the name with a lowercase letter.",
		)
	}
	if strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") {
		return reject(
			"ends with a dot or a space, which Windows silently strips",
			"Remove the trailing dot or space.",
		)
	}
	if strings.HasPrefix(name, " ") {
		return reject(
			"starts with a space",
			"Remove the leading space.",
		)
	}
	base := name
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	if _, reserved := windowsDeviceNames[strings.ToLower(base)]; reserved {
		return reject(
			fmt.Sprintf("is the reserved Windows device name %q, which cannot be a directory on Windows", strings.ToLower(base)),
			"Choose a different name so the project can be cloned on Windows.",
		)
	}
	if name != strings.ToLower(name) {
		return reject(
			"contains an uppercase letter",
			fmt.Sprintf("Use the lowercase form: %s", strings.ToLower(name)),
		)
	}
	if _, keyword := goKeywords[name]; keyword {
		return reject(
			"is a Go reserved word and cannot name a Go package directory",
			"Choose a name that is not a Go keyword.",
		)
	}
	if !nameRE.MatchString(name) {
		return reject(
			"must start with a lowercase letter and contain only lowercase letters, digits, and single hyphens between them",
			"For example: myapp, my-app, invoices2.",
		)
	}
	return nil
}

// ValidateModulePath reports whether path is an acceptable Go module path
// for the generated go.mod.
func ValidateModulePath(path string) error {
	reject := func(problem, action string) error {
		return &NameError{Kind: "module path", Value: path, Problem: problem, Action: action}
	}

	if path == "" {
		return reject("is empty", "Pass --module-path, for example: --module-path github.com/you/myapp")
	}
	if len(path) > MaxModulePathLen {
		return reject(
			fmt.Sprintf("is %d bytes, longer than the %d-byte limit", len(path), MaxModulePathLen),
			fmt.Sprintf("Choose a module path of at most %d bytes.", MaxModulePathLen),
		)
	}
	if i := strings.IndexFunc(path, isControlOrNonASCII); i >= 0 {
		return reject(
			fmt.Sprintf("contains a control or non-ASCII character at byte %d", i),
			"Use only ASCII letters, digits, and the characters . _ ~ - /.",
		)
	}
	if strings.Contains(path, `\`) {
		return reject(
			"contains a backslash",
			"Go module paths always use forward slashes.",
		)
	}
	for _, element := range strings.Split(path, "/") {
		if element == "" {
			return reject(
				"has an empty path element (a leading, trailing, or doubled slash)",
				"Write the path as host/owner/name, for example github.com/you/myapp.",
			)
		}
		if element == "." || element == ".." {
			return reject(
				`contains a "." or ".." path element`,
				"Write an absolute module path, for example github.com/you/myapp.",
			)
		}
	}
	if !modulePathRE.MatchString(path) {
		return reject(
			"is not a valid Go module path",
			"Write the path as host/owner/name, for example github.com/you/myapp.",
		)
	}
	return nil
}

// isControlOrNonASCII reports whether r is outside printable ASCII. It
// catches NUL bytes, newlines, ANSI escape introducers, and every non-ASCII
// rune in one predicate, so a hostile name cannot reach a filesystem call
// or a terminal.
func isControlOrNonASCII(r rune) bool {
	return r < ' ' || r > '~'
}
