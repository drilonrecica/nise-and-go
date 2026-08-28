package check

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const invariantSecrets = "No secret material is committed in the project's tracked files (Global Constraint 8)."

const (
	// maxScanBytes bounds how much of one file is scanned. A file larger
	// than this is not secret material a human pasted in; it is a lockfile,
	// a build artifact, or a binary, and reading megabytes of it to look
	// for a PEM header would only make the check slow.
	maxScanBytes = 1 << 20 // 1 MiB
	// maxFindings bounds how many offending paths are listed, so a tree
	// that trips a rule everywhere produces a readable finding rather than
	// a thousand-line one.
	maxFindings = 20
	// minSecretValueLen is how long an assigned value must be before it is
	// treated as material rather than a marker. Below it, a value in a
	// committed file is far more likely to be a stub than a credential, and
	// a check that fires on "TOKEN=dev" is a check people turn off.
	minSecretValueLen = 8
)

// skippedDirs are directory names never walked when no git repository is
// available to say what is tracked. Each is either not the project's source
// (a dependency tree, a build output) or is the repository's own metadata.
// This is a fixed, short list, deliberately not an attempt to re-implement
// gitignore: when git is available its own answer is used instead.
var skippedDirs = map[string]struct{}{
	".git":         {},
	"node_modules": {},
	".svelte-kit":  {},
	"dist":         {},
	"build":        {},
	"vendor":       {},
}

// envExampleNames are the environment-file names that exist to be committed.
// Everything else matching .env* is the real thing.
var envExampleNames = map[string]struct{}{
	".env.example":  {},
	".env.sample":   {},
	".env.template": {},
	".env.dist":     {},
}

// keyFileExtensions and keyFileNames are file shapes whose whole purpose is
// to hold a private key.
var (
	keyFileExtensions = map[string]struct{}{
		".pem": {}, ".key": {}, ".p12": {}, ".pfx": {}, ".jks": {}, ".keystore": {},
	}
	keyFileNames = map[string]struct{}{
		"id_rsa": {}, "id_dsa": {}, "id_ecdsa": {}, "id_ed25519": {},
	}
)

var (
	// pemPrivateKey matches the armor header of any private key format.
	// This is the highest-confidence rule in the file: the string exists
	// for exactly one reason.
	pemPrivateKey = regexp.MustCompile(`-----BEGIN (?:[A-Z0-9 ]+ )?PRIVATE KEY-----`)
	// secretAssignment matches a secret-named variable being given a value
	// in an environment file. The name half is what makes it specific: a
	// long value assigned to PORT is not a finding.
	secretAssignment = regexp.MustCompile(`(?i)^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*(?:SECRET|TOKEN|PASSWORD|PASSWD|APIKEY|API_KEY|PRIVATE_KEY|CREDENTIAL|CREDENTIALS)[A-Za-z0-9_]*)\s*=\s*(.*)$`)
	// dsnCredentials matches a URL carrying inline userinfo with a
	// password, e.g. postgres://user:pass@host/db.
	dsnCredentials = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*)://([^\s/:@"']+):([^\s/@"']+)@`)
	// placeholderValue matches the conventional stand-ins a committed
	// example file uses. A value matching one of these is a marker, not
	// material, and firing on it would make the check useless in exactly
	// the file it is most likely to read.
	placeholderValue = regexp.MustCompile(`(?i)^(?:|-|""|''|x+|change[-_]?me|changeit|placeholder|example|sample|todo|tbd|none|null|nil|secret|password|passwd|token|your[-_ ].*|<.*>|\$\{.*\}|\$[A-Za-z_][A-Za-z0-9_]*)$`)
)

// checkSecrets scans the project's tracked files for committed secret
// material.
//
// # Which files
//
// Git's own answer is preferred: `git ls-files` is what "tracked" means, and
// it needs no reimplementation of gitignore. A generated project is not a
// git repository until the developer makes it one — `nise new` deliberately
// runs no git — so the fallback matters as much as the primary path: with no
// repository, every file in the tree is scanned instead, minus a short fixed
// list of dependency and build directories, and the check says which of the
// two it did. Reporting "skipped: not a git repository" in the very first
// context a user runs the command would put the check's most valuable moment
// out of reach.
//
// # Which rules
//
// Five, each chosen to be high-confidence rather than broad, because a
// security check that fails a correct project in CI is a check that gets
// disabled. They are documented rule by rule, with their placeholder
// tolerances, in docs/commands/check.md.
func checkSecrets(ctx context.Context, root string, git TrackedLister) Check {
	if git == nil {
		git = GitTracked
	}

	files, tracked, err := git(ctx, root)
	source := "git-tracked file(s)"
	if err != nil {
		return skip(CheckSecrets, invariantSecrets,
			fmt.Sprintf("could not list the project's files (%s)", err))
	}
	if !tracked {
		files, err = walkTree(root)
		if err != nil {
			return skip(CheckSecrets, invariantSecrets,
				fmt.Sprintf("could not walk the project tree (%s)", err))
		}
		source = "file(s) in the tree (this project is not a git repository yet, so every file was scanned rather than only the tracked ones)"
	}
	slices.Sort(files)

	var findings []string
	var offending []string
	for _, rel := range files {
		for _, f := range scanFile(root, rel) {
			offending = append(offending, rel)
			findings = append(findings, f)
		}
	}

	if len(findings) == 0 {
		return pass(CheckSecrets, invariantSecrets, fmt.Sprintf(
			"scanned %d %s; none carries a private key, a committed environment file, or an assigned secret value", len(files), source))
	}

	offending = slices.Compact(offending)
	shown := findings
	extra := 0
	if len(shown) > maxFindings {
		extra = len(shown) - maxFindings
		shown = shown[:maxFindings]
	}
	detail := strings.Join(shown, "; ")
	if extra > 0 {
		detail += fmt.Sprintf("; and %d more", extra)
	}

	c := fail(CheckSecrets, invariantSecrets,
		fmt.Sprintf("%d finding(s) across %d file(s): %s", len(findings), len(offending), detail),
		"Treat every value named above as compromised and rotate it. Remove it from the file, supply it at runtime through the environment instead (NAME, or NAME_FILE naming a file outside the repository), and purge it from the history — deleting it in a new commit leaves it readable in every earlier one.",
	)
	c.Paths = offending
	return c
}

// scanFile returns every finding for one file, described relative to the
// project root.
func scanFile(root, rel string) []string {
	base := path.Base(rel)
	lower := strings.ToLower(base)

	var findings []string

	// Rule 1: an environment file that is not one of the example forms.
	// The generated .gitignore excludes exactly this shape, so a tracked
	// one means the exclusion was overridden.
	if _, isExample := envExampleNames[lower]; !isExample && (lower == ".env" || strings.HasPrefix(lower, ".env.")) {
		findings = append(findings, rel+" is a committed environment file, not one of the .env.example forms")
	}

	// Rule 2: a file whose name or extension exists to hold a private key.
	if _, ok := keyFileNames[lower]; ok {
		findings = append(findings, rel+" has the name of a private key file")
	} else if _, ok := keyFileExtensions[strings.ToLower(path.Ext(rel))]; ok {
		findings = append(findings, rel+" has the extension of a private key or keystore file")
	}

	full := filepath.Join(root, filepath.FromSlash(rel))
	info, err := os.Stat(full)
	if err != nil || info.IsDir() || info.Size() == 0 || info.Size() > maxScanBytes {
		return findings
	}
	data, err := os.ReadFile(full) // #nosec G304 -- full is the project root joined with a path git or the tree walk produced; reading the project's own files is what this check does.
	if err != nil {
		return findings
	}
	// A file holding a NUL byte is binary. Regexes over it produce noise,
	// never a finding a human can act on.
	if bytes.IndexByte(data, 0) >= 0 {
		return findings
	}
	content := string(data)

	// Rule 3: a private key armor block, wherever it appears.
	if m := pemPrivateKey.FindString(content); m != "" {
		findings = append(findings, fmt.Sprintf("%s contains a %q block", rel, m))
	}

	// Rule 4: a secret-named variable assigned a real value, in a file
	// whose whole format is KEY=VALUE. Restricting this rule to
	// environment files is what keeps it off source code, where
	// `token := cfg.OperatorToken` is a variable, not a credential.
	if isEnvShaped(lower) {
		for i, line := range strings.Split(content, "\n") {
			trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\r"))
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			m := secretAssignment.FindStringSubmatch(trimmed)
			if m == nil {
				continue
			}
			value := unquoteEnvValue(m[2])
			if len(value) < minSecretValueLen || placeholderValue.MatchString(value) {
				continue
			}
			findings = append(findings, fmt.Sprintf("%s line %d assigns a %d-character value to %s", rel, i+1, len(value), m[1]))
		}
	}

	// Rule 5: a connection string carrying an inline password. A password
	// equal to its own username, or matching a placeholder, is the shape
	// of a throwaway local credential rather than a leak.
	for _, m := range dsnCredentials.FindAllStringSubmatch(content, -1) {
		scheme, user, pass := m[1], m[2], m[3]
		if pass == user || placeholderValue.MatchString(pass) || len(pass) < minSecretValueLen {
			continue
		}
		findings = append(findings, fmt.Sprintf("%s contains a %s:// URL with an inline password for user %q", rel, scheme, user))
	}

	return findings
}

// isEnvShaped reports whether a file's name says its contents are KEY=VALUE
// lines.
func isEnvShaped(lowerBase string) bool {
	return lowerBase == ".env" ||
		strings.HasPrefix(lowerBase, ".env.") ||
		strings.HasSuffix(lowerBase, ".env") ||
		lowerBase == ".envrc"
}

// GitTracked is the real TrackedLister: it asks git which files under dir
// are tracked. found is false — with no error — when git is not installed or
// dir is not inside a work tree, which is the ordinary state of a freshly
// generated project.
//
// This is the one subprocess `nise check` runs, and it makes no network
// access: `git ls-files` reads the local index and nothing else.
func GitTracked(ctx context.Context, dir string) (files []string, found bool, err error) {
	git, lookErr := exec.LookPath("git")
	if lookErr != nil {
		return nil, false, nil
	}
	// #nosec G204 -- git is the absolute path exec.LookPath resolved for
	// the "git" binary, every argument but one is a fixed literal, and the
	// remaining one is the project root this command already resolved from
	// the user's own working directory. Nothing here is attacker-supplied.
	cmd := exec.CommandContext(ctx, git, "-C", dir, "ls-files", "-z")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if runErr := cmd.Run(); runErr != nil {
		// A non-zero exit here means "not a git repository" far more often
		// than it means anything else, and either way the fallback (walk
		// the tree) produces a superset of the tracked files rather than a
		// gap. Reporting it as an error would turn "you have not run git
		// init yet" into a check failure.
		return nil, false, nil
	}
	for _, name := range strings.Split(stdout.String(), "\x00") {
		if name != "" {
			files = append(files, name)
		}
	}
	return files, true, nil
}

// walkTree lists every file under root, relative and slash-separated,
// skipping skippedDirs.
func walkTree(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p == root {
				return nil
			}
			if _, skipped := skippedDirs[d.Name()]; skipped {
				return fs.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	return out, err
}
