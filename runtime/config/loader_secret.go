package config

import (
	"fmt"
	"os"
)

// Secret loads name as a [Secret]. Precedence is: the plain environment
// variable name; then, if that is not set, the file named by name+"_FILE"
// (its contents become the value, per [readSecretFile]); then, if neither is
// set, the [Default] option. It is required unless Default is given.
//
// Setting both name and name+"_FILE" is always an error, never a silent
// preference for one over the other — see the package doc comment.
func (l *Loader) Secret(name string, opts ...Option) Secret {
	o := resolveOptions(opts)
	fileName := name + "_FILE"

	direct, directOK := os.LookupEnv(name)
	filePath, fileOK := os.LookupEnv(fileName)

	switch {
	case directOK && fileOK:
		l.addErr(fmt.Errorf("%s: both %s and %s are set; set only one", name, name, fileName))
		return Secret{}

	case directOK:
		return NewSecret(direct)

	case fileOK:
		value, warning, err := readSecretFile(filePath)
		if err != nil {
			l.addErr(fmt.Errorf("%s: %w; fix or remove %s", name, err, fileName))
			return Secret{}
		}
		if warning != "" {
			l.addWarning(fmt.Sprintf("%s: %s", name, warning))
		}
		return NewSecret(value)

	case o.hasDefault:
		return NewSecret(o.def)

	default:
		l.addErr(fmt.Errorf("%s: required secret is not set; set %s or %s", name, name, fileName))
		return Secret{}
	}
}
