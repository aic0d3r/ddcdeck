package model

import (
	"fmt"
	"net/url"
	"strings"
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
