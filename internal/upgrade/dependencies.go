package upgrade

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

// packageJSONFileName is the generated frontend's manifest.
const packageJSONFileName = "frontend/package.json"

// RequireChange is one pinned dependency an upgrade will move.
type RequireChange struct {
	// File is the manifest the requirement lives in, relative to the project
	// root: go.mod or frontend/package.json.
	File string `json:"file"`
	// Module is the module or package name.
	Module string `json:"module"`
	From   string `json:"from"`
	To     string `json:"to"`
}

// goRequireLine matches one requirement inside a go.mod require block:
// leading whitespace, a module path, a version, and an optional trailing
// comment. It is deliberately anchored to a whole line — a version token
// replaced anywhere else in the file would be a different edit than the one
// being announced.
var goRequireLine = regexp.MustCompile(`(?m)^(\s*)(\S+)(\s+)(v\S+)(.*)$`)

// jsonDependencyLine matches one "name": "version" entry.
var jsonDependencyLine = regexp.MustCompile(`(?m)^(\s*)"([^"]+)"(\s*:\s*)"([^"]+)"(,?)\s*$`)

// bumpGoMod rewrites the version of every requirement nise pins, and nothing
// else.
//
// It is a line rewrite rather than a call into the go toolchain on purpose.
// `go mod edit -require` would be equivalent and would also reformat the file,
// reordering the require block and dropping the comments the generated go.mod
// carries — turning a three-line diff a developer can read into a whole-file
// one they will scroll past. And `go mod tidy` resolves the entire module
// graph over the network, which an upgrade must not do silently.
//
// A requirement the project has removed or renamed is left alone: the
// application owns go.mod, and an upgrade that re-adds a dependency somebody
// deliberately dropped has overruled them.
func bumpGoMod(source string, pins []generator.Dependency) (string, []RequireChange) {
	want := make(map[string]string, len(pins))
	for _, pin := range pins {
		want[pin.Name] = pin.Version
	}

	var changes []RequireChange
	rewritten := goRequireLine.ReplaceAllStringFunc(source, func(line string) string {
		parts := goRequireLine.FindStringSubmatch(line)
		module, version := parts[2], parts[4]
		target, pinned := want[module]
		if !pinned || target == version || !newer(target, version) {
			return line
		}
		changes = append(changes, RequireChange{
			File: goModFileName, Module: module, From: version, To: target,
		})
		return parts[1] + parts[2] + parts[3] + target + parts[5]
	})
	return rewritten, changes
}

// bumpPackageJSON does the same for the generated frontend's pinned packages.
//
// Same reasoning, and one more: a package.json is JSON, and re-encoding it
// would reorder keys and reformat the whole document. An exact line rewrite
// leaves a developer's own dependencies, scripts, and formatting untouched.
func bumpPackageJSON(source string, pins []generator.Dependency) (string, []RequireChange) {
	want := make(map[string]string, len(pins))
	for _, pin := range pins {
		want[pin.Name] = pin.Version
	}

	var changes []RequireChange
	rewritten := jsonDependencyLine.ReplaceAllStringFunc(source, func(line string) string {
		parts := jsonDependencyLine.FindStringSubmatch(line)
		name, version := parts[2], parts[4]
		target, pinned := want[name]
		if !pinned || target == version || !newer(target, version) {
			return line
		}
		changes = append(changes, RequireChange{
			File: packageJSONFileName, Module: name, From: version, To: target,
		})
		return parts[1] + `"` + parts[2] + `"` + parts[3] + `"` + target + `"` + parts[5]
	})
	return rewritten, changes
}

// newer reports whether a is a strictly later version than b.
//
// Only a forward move is applied. A project that has deliberately pinned a
// *newer* dependency than nise ships — because it needed a fix, or because the
// developer is testing something — must not be silently moved backwards by an
// upgrade; that is a downgrade wearing an upgrade's name. Such a pin is
// reported as a note instead, where a person can decide.
func newer(a, b string) bool {
	ap, aok := parseVersion(a)
	bp, bok := parseVersion(b)
	if !aok || !bok {
		// Something unparseable on either side: leave it alone and let a note
		// carry it. Guessing at an ordering is how a "safe" bump stops being
		// one.
		return false
	}
	for i := range 3 {
		if ap[i] != bp[i] {
			return ap[i] > bp[i]
		}
	}
	return false
}

// parseVersion reads the three numeric components of a version, with or
// without a leading "v" and ignoring any prerelease or build suffix.
func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	trimmed := strings.TrimPrefix(v, "v")
	if cut := strings.IndexAny(trimmed, "-+"); cut >= 0 {
		trimmed = trimmed[:cut]
	}
	parts := strings.Split(trimmed, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// aheadOfNise reports the pins a project holds at a *newer* version than nise
// ships, so they can be named rather than silently left behind.
func aheadOfNise(source string, pins []generator.Dependency, file string, match *regexp.Regexp, nameGroup, versionGroup int) []string {
	have := make(map[string]string, len(pins))
	for _, pin := range pins {
		have[pin.Name] = pin.Version
	}
	var notes []string
	for _, parts := range match.FindAllStringSubmatch(source, -1) {
		name, version := parts[nameGroup], parts[versionGroup]
		pinned, ok := have[name]
		if !ok || version == pinned || !newer(version, pinned) {
			continue
		}
		notes = append(notes, fmt.Sprintf(
			"%s pins %s at %s, which is newer than the %s this nise ships. It was left alone — an upgrade never moves a dependency backwards.",
			file, name, version, pinned))
	}
	return notes
}

// planDependencies computes every pinned-dependency change, without writing.
func planDependencies(root string) (changes []RequireChange, notes []string, err error) {
	goPins := generator.GoDependencies()
	frontendPins := append(generator.FrontendRuntimeDependencies(), generator.FrontendDependencies()...)

	for _, manifest := range []struct {
		name  string
		pins  []generator.Dependency
		bump  func(string, []generator.Dependency) (string, []RequireChange)
		match *regexp.Regexp
		// groups locate the name and version in match's submatches.
		nameGroup, versionGroup int
	}{
		{goModFileName, goPins, bumpGoMod, goRequireLine, 2, 4},
		{packageJSONFileName, frontendPins, bumpPackageJSON, jsonDependencyLine, 2, 4},
	} {
		source, readErr := readManifest(root, manifest.name)
		if errors.Is(readErr, os.ErrNotExist) {
			// The application deleted it, which is its prerogative — the
			// frontend is app-owned in its entirety.
			continue
		}
		if readErr != nil {
			return nil, nil, readErr
		}
		_, bumped := manifest.bump(source, manifest.pins)
		changes = append(changes, bumped...)
		notes = append(notes,
			aheadOfNise(source, manifest.pins, manifest.name, manifest.match, manifest.nameGroup, manifest.versionGroup)...)
	}
	return changes, notes, nil
}

func readManifest(root, name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name))) // #nosec G304 -- root joined with this package's own manifest name constants.
	if err != nil {
		return "", err
	}
	return string(data), nil
}
