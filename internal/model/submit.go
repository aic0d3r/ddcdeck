package model

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// RepoURL is where model files are contributed to and ship from: the repo is the database.
const RepoURL = "https://github.com/aic0d3r/ddcdeck"

// IssueBody renders the submission text for a data/models/ PR. toml is the whole
// accumulated model file — one issue per monitor, not one per feature; serials never
// enter the file, and `verified = true` is the quality signal for the reviewer.
func (m Model) IssueBody(toml string) string {
	var b strings.Builder
	b.WriteString("Found with the ddcdeck discovery wizard (OSD diffing; `verified = true` features were confirmed by writing the value back and watching the menu).\n")
	b.WriteString("Please merge into `data/models/` so it ships for everyone.\n\n")
	fmt.Fprintf(&b, "```toml\n%s\n```\n", strings.TrimSpace(toml))
	b.WriteString("\n`ddcdeck model export-mccs` regenerates the ddcutil user-defined features file from it.\n")
	return b.String()
}

// SubmitURL is the prefilled GitHub issue. Submission stays one deliberate click: the
// user reviews the text in the browser and presses Submit — ddcdeck uploads nothing
// itself (offline-first, no phone-home, and no GitHub token shipped in the binary).
func (m Model) SubmitURL(toml string) string {
	return fmt.Sprintf("%s/issues/new?title=%s&body=%s", RepoURL,
		url.QueryEscape(fmt.Sprintf("Model: %s-%s-%d", m.Mfg, m.Model, m.Product)),
		url.QueryEscape(m.IssueBody(toml)))
}

// Verdicts a user can report for a model file, in order of good to bad.
var Verdicts = []string{"works", "partial", "broken"}

// ReportURL is the prefilled "did the model file work?" issue. A maintainer flips
// `verified` once enough reports agree. Like SubmitURL it uploads nothing by itself.
func ReportURL(id, verdict, note string) string {
	body := fmt.Sprintf("Model file `%s`: **%s**\n\n%s\n\n(Run `ddcdeck doctor` and paste anything unusual below.)\n", id, verdict, strings.TrimSpace(note))
	return fmt.Sprintf("%s/issues/new?title=%s&body=%s", RepoURL,
		url.QueryEscape(fmt.Sprintf("Report: %s %s", id, verdict)), url.QueryEscape(body))
}

// repoRaw is the base URL Fetch pulls one model file from; a var so tests can point it
// at a local server.
var repoRaw = RepoURL + "/raw/master/data/models"

var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Fetch downloads one community model file into the user model dir and returns its path.
// This is the opt-in lookup from the design: ddcdeck never checks by itself, and what it
// downloads is parsed, validated and identity-checked before it is written — these are
// VCP codes that will be sent to hardware.
func Fetch(id string) (string, error) {
	if !idPattern.MatchString(id) {
		return "", fmt.Errorf("expected MFG-MODEL-PRODUCT like AUS-PA279CRV-10209, got %q", id)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, repoRaw+"/"+id+".toml", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "ddcdeck")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusNotFound:
		return "", fmt.Errorf("no community model file for %s — discover it (`ddcdeck discover`) and submit it", id)
	case res.StatusCode != http.StatusOK:
		return "", fmt.Errorf("fetch %s: %s", id, res.Status)
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return "", err
	}
	var m Model
	if _, err := toml.Decode(string(b), &m); err != nil {
		return "", fmt.Errorf("downloaded file is not a valid model: %v", err)
	}
	if err := m.validate(); err != nil {
		return "", fmt.Errorf("downloaded file rejected: %v", err)
	}
	if got := fmt.Sprintf("%s-%s-%d", m.Mfg, m.Model, m.Product); !strings.EqualFold(got, id) {
		return "", fmt.Errorf("downloaded file identifies %s, not %s", got, id)
	}
	return SaveUserModel(m)
}

// FeedbackURL is the prefilled GitHub issue for "this imported model file works / doesn't
// work on my monitor": the quality signal for files that were never verified on hardware.
// Like SubmitURL it is one deliberate click in the browser; nothing is uploaded by ddcdeck.
func FeedbackURL(id string, works bool, features []string, version string) string {
	verdict := "works"
	if !works {
		verdict = "does not work"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Model file `%s` %s on my monitor.\n\nUnverified features I tried:\n", id, verdict)
	for _, f := range features {
		fmt.Fprintf(&b, "- %s\n", f)
	}
	fmt.Fprintf(&b, "\nddcdeck %s\n", version)
	return fmt.Sprintf("%s/issues/new?title=%s&body=%s", RepoURL,
		url.QueryEscape("Model feedback: "+id+" "+verdict), url.QueryEscape(b.String()))
}
