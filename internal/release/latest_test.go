package release

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// serve starts a release index that answers with body and status. Every test
// here talks to it rather than to GitHub: a test that needs the network to
// pass is a test that fails on an aeroplane and teaches nobody anything when
// it does.
func serve(t *testing.T, status int, body string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

const latestBody = `{"tag_name":"v0.2.0","html_url":"https://github.com/drilonrecica/nise-and-go/releases/tag/v0.2.0","draft":false,"prerelease":false}`

func TestFetchReadsThePublishedRelease(t *testing.T) {
	t.Parallel()
	url := serve(t, http.StatusOK, latestBody)

	got, err := Fetch(context.Background(), url)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got.Tag != "v0.2.0" {
		t.Errorf("Tag = %q, want v0.2.0", got.Tag)
	}
	if !strings.HasSuffix(got.URL, "/releases/tag/v0.2.0") {
		t.Errorf("URL = %q, want the release page", got.URL)
	}
}

// The request must carry nothing that identifies the caller. A version in the
// User-Agent would make every update check report which nise the caller has —
// the shape of the thing this project says it does not do, arriving through
// the one command permitted to open a socket.
func TestTheRequestIdentifiesNobody(t *testing.T) {
	t.Parallel()

	var captured *http.Request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Clone(r.Context())
		_, _ = w.Write([]byte(latestBody))
	}))
	defer server.Close()

	if _, err := Fetch(context.Background(), server.URL); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if captured == nil {
		t.Fatal("the server recorded no request, so this test proves nothing")
	}

	if agent := captured.Header.Get("User-Agent"); agent != userAgent {
		t.Errorf("User-Agent = %q, want the constant %q with no version in it", agent, userAgent)
	}
	for _, header := range []string{"Authorization", "Cookie", "X-Nise-Version", "From"} {
		if value := captured.Header.Get(header); value != "" {
			t.Errorf("the request carried %s: %q; an update check identifies nobody", header, value)
		}
	}
	// Whatever is sent, it is a plain read.
	if captured.Method != http.MethodGet {
		t.Errorf("method = %s, want GET", captured.Method)
	}
	if captured.ContentLength > 0 {
		t.Errorf("the request carried a %d-byte body; an update check sends nothing", captured.ContentLength)
	}
}

// A redirect is how a captive portal or an intercepting proxy steers a request
// somewhere else. This endpoint does not redirect, so following one would only
// ever mean talking to a host nobody chose.
func TestARedirectIsRefused(t *testing.T) {
	t.Parallel()

	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(latestBody))
	}))
	defer elsewhere.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusFound)
	}))
	defer server.Close()

	if _, err := Fetch(context.Background(), server.URL); err == nil {
		t.Fatal("a redirect was followed; nise talks to one host or fails")
	} else if !strings.Contains(err.Error(), "redirected") {
		t.Errorf("the error does not say a redirect was refused: %v", err)
	}
}

// The endpoint already excludes drafts and prereleases. Checking anyway means
// this package's promise does not depend on somebody else's endpoint
// continuing to behave.
func TestADraftOrPrereleaseIsNotTheLatestRelease(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		"draft":      `{"tag_name":"v0.2.0","draft":true,"prerelease":false}`,
		"prerelease": `{"tag_name":"v0.2.0","draft":false,"prerelease":true}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			url := serve(t, http.StatusOK, body)
			if _, err := Fetch(context.Background(), url); !errors.Is(err, ErrNoRelease) {
				t.Errorf("a %s was accepted as the latest release: err = %v", name, err)
			}
		})
	}
}

// A tag is echoed back to a person as something to install. "Whatever the
// server said" is not good enough provenance for that.
func TestAnImplausibleTagIsRefused(t *testing.T) {
	t.Parallel()

	for name, tag := range map[string]string{
		"a branch name":     "main",
		"a shell fragment":  "v1.0.0; rm -rf /",
		"a prerelease tag":  "v1.0.0-rc.1",
		"an unprefixed tag": "1.0.0",
		"nothing at all":    "",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			url := serve(t, http.StatusOK, `{"tag_name":"`+tag+`","draft":false,"prerelease":false}`)
			_, err := Fetch(context.Background(), url)
			if err == nil {
				t.Fatalf("the tag %q was accepted", tag)
			}
			if errors.Is(err, ErrNoRelease) {
				t.Fatalf("the tag %q was reported as no release rather than as a bad tag", tag)
			}
		})
	}
}

func TestNoPublishedReleaseIsItsOwnAnswer(t *testing.T) {
	t.Parallel()
	url := serve(t, http.StatusNotFound, `{"message":"Not Found"}`)

	if _, err := Fetch(context.Background(), url); !errors.Is(err, ErrNoRelease) {
		t.Errorf("a 404 was reported as %v; a repository with no releases is not a broken check", err)
	}
}

func TestAServerErrorIsReported(t *testing.T) {
	t.Parallel()
	url := serve(t, http.StatusServiceUnavailable, "")

	_, err := Fetch(context.Background(), url)
	if err == nil {
		t.Fatal("a 503 was accepted")
	}
	if errors.Is(err, ErrNoRelease) {
		t.Error("a 503 was reported as \"no release published\", which would tell somebody their installation is fine when nothing was checked")
	}
}

// An unbounded read of somebody else's response is a decision rather than a
// default. The body here is far larger than any release document.
func TestAnEnormousResponseIsBounded(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"tag_name":"v0.2.0","html_url":"`))
		for range (maxBody / 1024) + 8 {
			_, _ = w.Write([]byte(strings.Repeat("a", 1024)))
		}
		_, _ = w.Write([]byte(`"}`))
	}))
	defer server.Close()

	if _, err := Fetch(context.Background(), server.URL); err == nil {
		t.Error("a response larger than the limit was read to the end and accepted")
	}
}

// A cancelled context stops the request rather than the timeout doing it ten
// seconds later. Ctrl-C on an update check should end it.
func TestACancelledContextEndsTheRequest(t *testing.T) {
	t.Parallel()
	url := serve(t, http.StatusOK, latestBody)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Fetch(ctx, url); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

// The default destination is the repository this project releases from, and
// nothing else. A check that can be aimed anywhere is a check whose answer
// means nothing.
func TestTheDefaultEndpointIsThisRepository(t *testing.T) {
	t.Parallel()

	if !strings.HasPrefix(endpoint, "https://") {
		t.Error("the release index is not fetched over TLS")
	}
	if !strings.Contains(endpoint, "drilonrecica/nise-and-go") {
		t.Errorf("the default endpoint %q does not name this repository", endpoint)
	}
	if !strings.HasSuffix(endpoint, "/releases/latest") {
		t.Errorf("the default endpoint %q is not the published-release endpoint", endpoint)
	}
}
