package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"
)

// endpoint is the one URL any nise command ever fetches. GitHub's
// releases/latest resolves to the most recent published, non-draft,
// non-prerelease release — which is the same definition
// .github/workflows/release-validate.yml uses for a rollback target, and the
// same one a person browsing the releases page would land on.
const endpoint = "https://api.github.com/repos/drilonrecica/nise-and-go/releases/latest"

// userAgent is a constant, and carries no version.
//
// GitHub requires a User-Agent. Putting the running version in it would make
// every update check report which nise the caller has — which is the shape of
// the thing this project says it does not do, arriving through the one command
// permitted to open a socket. A constant makes every user's request identical,
// so the header carries no information the connection did not already.
const userAgent = "nise"

// maxBody bounds the response. The document this parses is a few kilobytes; a
// larger one is either not GitHub or not worth reading, and either way an
// unbounded read of somebody else's response is a decision rather than a
// default.
const maxBody = 1 << 20

// requestTimeout bounds the whole exchange, including connection setup. An
// update check that hangs is worse than one that fails: the person typed it to
// find something out quickly and can retry.
const requestTimeout = 10 * time.Second

// tagPattern is the shape a release tag must have. The endpoint is trusted to
// be GitHub, and its answer is still checked: a tag is echoed back to a person
// as something to install, and "whatever the server said" is not a good enough
// provenance for that.
var tagPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// ErrNoRelease reports that the repository has published no release yet. It is
// a distinct error because it is not a failure of the check — the check
// succeeded, and the answer is that there is nothing to install.
var ErrNoRelease = errors.New("no release has been published yet")

// Latest is the most recent published release.
type Latest struct {
	// Tag is the release tag, e.g. "v0.2.0".
	Tag string
	// URL is the release page, for a person to read the notes before
	// installing anything.
	URL string
}

// Fetch asks GitHub for the latest published release.
//
// This is the only network call in nise, and it happens only inside a command
// a person typed. Nothing schedules it, caches its answer, or runs it as a
// side effect of another command — an update check that remembered when it
// last ran would need somewhere to remember it, and
// test/nonetwork/selfupdate_test.go proves nise writes no such file.
func Fetch(ctx context.Context, baseURL string) (Latest, error) {
	if baseURL == "" {
		baseURL = endpoint
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return Latest{}, fmt.Errorf("building the request: %w", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", userAgent)

	response, err := client().Do(request)
	if err != nil {
		return Latest{}, err
	}
	defer func() { _ = response.Body.Close() }()

	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		// GitHub answers 404 both for a repository with no releases and for
		// one that does not exist. The first is the case worth naming.
		return Latest{}, ErrNoRelease
	default:
		return Latest{}, fmt.Errorf("the release index answered %s", response.Status)
	}

	var body struct {
		TagName    string `json:"tag_name"`
		HTMLURL    string `json:"html_url"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxBody)).Decode(&body); err != nil {
		return Latest{}, fmt.Errorf("reading the release index: %w", err)
	}

	// The endpoint already excludes drafts and prereleases. Checking anyway
	// costs two lines and means this package's promise does not depend on
	// somebody else's endpoint continuing to behave.
	if body.Draft || body.Prerelease {
		return Latest{}, ErrNoRelease
	}
	if !tagPattern.MatchString(body.TagName) {
		return Latest{}, fmt.Errorf("the release index named %q, which is not a release tag", body.TagName)
	}
	return Latest{Tag: body.TagName, URL: body.HTMLURL}, nil
}

// client builds the HTTP client for the one request this package makes.
//
// It is constructed here rather than accepted from a caller so that its
// properties are properties of the package: no redirect is followed, and the
// whole exchange is bounded. A redirect is how a captive portal or an
// intercepting proxy steers a request somewhere else, and this endpoint does
// not redirect — so following one would only ever mean talking to a host
// nobody chose.
func client() *http.Client {
	return &http.Client{
		Timeout: requestTimeout,
		CheckRedirect: func(req *http.Request, _ []*http.Request) error {
			return fmt.Errorf("the release index redirected to %s; nise talks to one host or fails", req.URL.Host)
		},
	}
}
