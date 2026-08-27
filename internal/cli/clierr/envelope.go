package clierr

// Envelope is the stable JSON shape a nise CLI error renders as in --json
// mode: {"error": {...}}. Field names and meanings are documented in
// docs/cli-output.md and must not change without updating that contract.
type Envelope struct {
	Error EnvelopeError `json:"error"`
}

// EnvelopeError is the body of Envelope. Code, Message, and Recovery are
// always present; Details, Docs, and Chain are omitted when empty. Chain is
// populated only when the caller asks for verbose rendering — see
// JSONEnvelope.
type EnvelopeError struct {
	Code     string            `json:"code"`
	Message  string            `json:"message"`
	Recovery string            `json:"recovery"`
	Details  map[string]string `json:"details,omitempty"`
	Docs     string            `json:"docs,omitempty"`
	Chain    []string          `json:"chain,omitempty"`
}

// JSONEnvelope returns the stable JSON envelope for e. When verbose is
// true, the optional "chain" field is populated from e.Chain(); it is
// omitted otherwise, matching the human renderer's rule that the
// underlying error chain is a --verbose-only detail.
func (e *Error) JSONEnvelope(verbose bool) Envelope {
	body := EnvelopeError{
		Code:     e.Code(),
		Message:  e.Cause(),
		Recovery: e.Recovery(),
		Details:  e.Details(),
		Docs:     e.Docs(),
	}
	if verbose {
		body.Chain = e.Chain()
	}
	return Envelope{Error: body}
}

// HumanLines returns the lines a human-mode writer should print for e, in
// order: the cause, then the recovery action, then — only when verbose is
// true and a chain exists — one line per wrapped error, indented.
func (e *Error) HumanLines(verbose bool) []string {
	lines := []string{e.Cause(), e.Recovery()}
	if verbose {
		for _, c := range e.Chain() {
			lines = append(lines, "  "+c)
		}
	}
	return lines
}
