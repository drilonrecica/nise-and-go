package clierr

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestJSONEnvelopeShape(t *testing.T) {
	t.Parallel()
	e := Usage("unknown command \"fro\"", "Run \"nise help\".").
		WithDocs("docs/cli-output.md#usage-errors").
		WithDetail("input", "fro")

	env := e.JSONEnvelope(false)
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	errObj, ok := got["error"].(map[string]any)
	if !ok {
		t.Fatalf("marshaled envelope has no error object: %s", b)
	}
	for _, field := range []string{"code", "message", "recovery", "docs", "details"} {
		if _, ok := errObj[field]; !ok {
			t.Errorf("error object missing field %q: %s", field, b)
		}
	}
	if _, ok := errObj["chain"]; ok {
		t.Errorf("error object has 'chain' with verbose=false: %s", b)
	}
	if errObj["code"] != "usage_error" {
		t.Errorf("code = %v, want usage_error", errObj["code"])
	}
}

func TestJSONEnvelopeOmitsEmptyOptionalFields(t *testing.T) {
	t.Parallel()
	e := New(ExitError, "cause", "recovery")
	b, err := json.Marshal(e.JSONEnvelope(false))
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	want := `{"error":{"code":"error","message":"cause","recovery":"recovery"}}`
	if string(b) != want {
		t.Errorf("JSONEnvelope() = %s, want %s", b, want)
	}
}

func TestJSONEnvelopeChainOnlyUnderVerbose(t *testing.T) {
	t.Parallel()
	e := Wrap(errors.New("boom"), ExitError, "cause", "recovery")

	quiet := e.JSONEnvelope(false)
	if quiet.Error.Chain != nil {
		t.Errorf("verbose=false: Chain = %v, want nil", quiet.Error.Chain)
	}

	verbose := e.JSONEnvelope(true)
	if len(verbose.Error.Chain) != 1 || verbose.Error.Chain[0] != "boom" {
		t.Errorf("verbose=true: Chain = %v, want [\"boom\"]", verbose.Error.Chain)
	}
}

func TestHumanLinesOrderAndVerboseGating(t *testing.T) {
	t.Parallel()
	e := Wrap(errors.New("dial tcp: refused"), ExitPrecondition, "could not reach the database", "start postgres and retry")

	quiet := e.HumanLines(false)
	if len(quiet) != 2 {
		t.Fatalf("HumanLines(false) = %v, want exactly [cause, recovery]", quiet)
	}
	if quiet[0] != "could not reach the database" {
		t.Errorf("HumanLines(false)[0] = %q, want cause first", quiet[0])
	}
	if quiet[1] != "start postgres and retry" {
		t.Errorf("HumanLines(false)[1] = %q, want recovery second", quiet[1])
	}

	verbose := e.HumanLines(true)
	if len(verbose) != 3 {
		t.Fatalf("HumanLines(true) = %v, want cause+recovery+one chain line", verbose)
	}
}
