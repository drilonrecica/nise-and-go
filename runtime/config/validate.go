package config

import (
	"errors"
	"fmt"
)

// Validator collects configuration violations so a caller can report every
// problem found in one pass, rather than the first one it happens to check.
// Build one with [Validate]; do not construct a Validator directly.
type Validator struct {
	violations []string
}

// Check records a violation if ok is false. name must be the environment
// variable or setting at fault, problem must state what is wrong, and
// action must state what an operator should do — never include a secret's
// value in problem or action.
func (v *Validator) Check(ok bool, name, problem, action string) {
	if !ok {
		v.violations = append(v.violations, fmt.Sprintf("%s: %s; %s", name, problem, action))
	}
}

// RequireSecretSet records a violation if secret has no value. name is the
// environment variable the secret was loaded from.
func (v *Validator) RequireSecretSet(name string, secret Secret) {
	v.Check(secret.IsSet(), name, "required secret is not set", fmt.Sprintf("set %s or %s_FILE", name, name))
}

// Err returns a combined error naming every recorded violation, or nil if
// there were none.
func (v *Validator) Err() error {
	if len(v.violations) == 0 {
		return nil
	}
	errs := make([]error, len(v.violations))
	for i, msg := range v.violations {
		errs[i] = errors.New(msg)
	}
	return errors.Join(errs...)
}

// Validate runs checks against the application's configuration, but only
// when env is [Production]; for [Development] and [Test] it returns nil
// without calling checks at all. checks should call [Validator.Check] and
// [Validator.RequireSecretSet] for every production safety requirement the
// application has — a missing required secret, debug or verbose mode
// enabled, insecure cookie settings, a default or empty credential, a bind
// address that is unexpectedly public, an unset or non-production-safe
// database connection, and so on. Every violation checks records is
// returned together as one joined error, so a failed startup tells an
// operator everything to fix at once.
func Validate(env Environment, checks func(v *Validator)) error {
	if env != Production {
		return nil
	}
	v := &Validator{}
	checks(v)
	return v.Err()
}
