package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Option configures how a single field is loaded. The only Option in this
// package is [Default]; it exists as a function type, rather than a plain
// parameter, so a field can be declared required by simply omitting it.
type Option func(*fieldOptions)

type fieldOptions struct {
	hasDefault bool
	def        string
}

// Default supplies the compiled-in fallback value used when the
// corresponding environment variable (and, for [Loader.Secret], its
// `_FILE` variant) is not set. A field loaded without Default is required:
// loading fails if no source provides a value.
func Default(value string) Option {
	return func(o *fieldOptions) {
		o.hasDefault = true
		o.def = value
	}
}

func resolveOptions(opts []Option) fieldOptions {
	var o fieldOptions
	for _, opt := range opts {
		opt(&o)
	}
	return o
}

// Loader reads typed configuration values from the process environment. It
// collects every load failure rather than stopping at the first one; call
// [Loader.Err] after loading every field to get a single combined error, or
// nil if every field loaded successfully. See the package doc comment for
// the source precedence Loader applies.
//
// A Loader is not safe for concurrent use. Build one, load every field from
// a single goroutine, and discard it.
type Loader struct {
	errs     []error
	warnings []string
}

// NewLoader returns a Loader that reads from the real process environment
// ([os.LookupEnv]).
func NewLoader() *Loader {
	return &Loader{}
}

// Err returns a combined error naming every field that failed to load, or
// nil if every field loaded successfully.
func (l *Loader) Err() error {
	if len(l.errs) == 0 {
		return nil
	}
	return errors.Join(l.errs...)
}

// Warnings returns non-fatal problems noticed while loading, such as an
// overly permissive `_FILE` secret file. It never contains a secret value.
func (l *Loader) Warnings() []string {
	if len(l.warnings) == 0 {
		return nil
	}
	out := make([]string, len(l.warnings))
	copy(out, l.warnings)
	return out
}

func (l *Loader) addErr(err error) {
	l.errs = append(l.errs, err)
}

func (l *Loader) addWarning(w string) {
	l.warnings = append(l.warnings, w)
}

func missingVarError(name string) error {
	return fmt.Errorf("%s: required environment variable is not set; set %s", name, name)
}

// String loads name as a plain string. It is required unless [Default] is
// given.
func (l *Loader) String(name string, opts ...Option) string {
	o := resolveOptions(opts)
	if v, ok := os.LookupEnv(name); ok {
		return v
	}
	if o.hasDefault {
		return o.def
	}
	l.addErr(missingVarError(name))
	return ""
}

// Int loads name as a base-10 integer. It is required unless [Default] is
// given; a Default value is parsed with the same rules as a value from the
// environment.
func (l *Loader) Int(name string, opts ...Option) int {
	raw, ok := l.rawValue(name, opts)
	if !ok {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		l.addErr(fmt.Errorf("%s: %q is not a valid integer; set %s to a whole number", name, raw, name))
		return 0
	}
	return n
}

// Bool loads name as a boolean, accepting any value [strconv.ParseBool]
// accepts ("1", "t", "T", "TRUE", "true", "True", "0", "f", "F", "FALSE",
// "false", "False"). It is required unless [Default] is given.
func (l *Loader) Bool(name string, opts ...Option) bool {
	raw, ok := l.rawValue(name, opts)
	if !ok {
		return false
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		l.addErr(fmt.Errorf("%s: %q is not a valid boolean; set %s to true or false", name, raw, name))
		return false
	}
	return b
}

// Duration loads name as a [time.Duration] using [time.ParseDuration]
// syntax (for example "30s", "5m"). It is required unless [Default] is
// given.
func (l *Loader) Duration(name string, opts ...Option) time.Duration {
	raw, ok := l.rawValue(name, opts)
	if !ok {
		return 0
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		l.addErr(fmt.Errorf("%s: %q is not a valid duration; set %s to a Go duration such as \"30s\"", name, raw, name))
		return 0
	}
	return d
}

// Environment loads name as an [Environment] using [ParseEnvironment]. It is
// required unless [Default] is given.
func (l *Loader) Environment(name string, opts ...Option) Environment {
	raw, ok := l.rawValue(name, opts)
	if !ok {
		return ""
	}
	env, err := ParseEnvironment(raw)
	if err != nil {
		l.addErr(fmt.Errorf("%s: %w; set %s", name, err, name))
		return ""
	}
	return env
}

// rawValue resolves name to its environment-or-default string, recording a
// missing-variable error and reporting ok=false if neither is available. It
// is the shared precedence step behind Int, Bool, Duration, and Environment.
func (l *Loader) rawValue(name string, opts []Option) (string, bool) {
	o := resolveOptions(opts)
	if v, ok := os.LookupEnv(name); ok {
		return v, true
	}
	if o.hasDefault {
		return o.def, true
	}
	l.addErr(missingVarError(name))
	return "", false
}
