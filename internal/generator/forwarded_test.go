package generator_test

import (
	"strings"
	"testing"

	"github.com/drilonrecica/nise-and-go/internal/generator"
)

func TestGeneratedProjectDeclaresItsProxyChain(t *testing.T) {
	t.Parallel()

	files, err := generator.Plan(defaultOptions())
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	content := make(map[string]string, len(files))
	for _, file := range files {
		content[file.Path] = string(file.Content)
	}

	wants := map[string][]string{
		"internal/platform/config/config.go": {
			`"github.com/drilonrecica/nise-and-go/runtime/forwarded"`,
			"TrustedProxies forwarded.Policy",
			`l.Int("TRUSTED_PROXY_COUNT", rtconfig.Default("0"))`,
			`l.String("TRUSTED_PROXY_NETWORKS", rtconfig.Default(""))`,
			"forwarded.ParseNetworks(trustedProxyNetworks)",
			"TRUST_PROXY_HEADERS: set while TRUSTED_PROXY_COUNT is 0",
		},
		"internal/platform/config/config_test.go": {
			"TestLoadDefaultsToTrustingNoProxy",
			"TestLoadBuildsTheTrustedProxyPolicy",
			"TestLoadWarnsWhenAnyPeerMayBeAProxy",
			"TestLoadRefusesHalfConfiguredProxyTrust",
			"TestLoadRejectsUnusableProxyConfiguration",
		},
		"internal/platform/httpapi/router.go": {
			"d.Config.TrustedProxies.Middleware(handler)",
		},
		".env.example": {
			"TRUSTED_PROXY_COUNT=0",
			"TRUSTED_PROXY_NETWORKS=",
		},
	}
	for path, fragments := range wants {
		for _, fragment := range fragments {
			if !strings.Contains(content[path], fragment) {
				t.Errorf("%s lacks %q", path, fragment)
			}
		}
	}

	// The edge view must be resolved outside logging, because logging is one
	// of its consumers: a second resolution downstream would eventually mean
	// two answers to "who is asking".
	router := content["internal/platform/httpapi/router.go"]
	edge := strings.Index(router, "d.Config.TrustedProxies.Middleware(handler)")
	logs := strings.Index(router, "handler = logging.Middleware(")
	security := strings.Index(router, "return secure.Middleware(policy)(handler)")
	if edge < 0 || logs < 0 || security < 0 {
		t.Fatal("the router does not compose the expected core")
	}
	// Composition is written inside-out, so the outermost middleware is
	// applied last: the edge view must appear after logging and before the
	// security policy.
	if logs >= edge || edge >= security {
		t.Errorf("core order is wrong: logging at %d, edge at %d, security at %d", logs, edge, security)
	}

	// The default must trust nothing. A generated project that read
	// forwarded headers out of the box would let anyone name their address.
	if !strings.Contains(content[".env.example"], "TRUSTED_PROXY_COUNT=0") {
		t.Error("the generated environment example does not default to trusting no proxy")
	}
}
