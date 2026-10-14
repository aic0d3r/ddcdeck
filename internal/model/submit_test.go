package model

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSubmitURL(t *testing.T) {
	m := Model{Mfg: "AUS", Model: "PA279CRV", Product: 10209}
	toml := "mfg     = \"AUS\"\nmodel   = \"PA279CRV\"\nproduct = 10209\n\n[feature.bluelight]\nvcp = 229\n"
	u := m.SubmitURL(toml)

	if !strings.HasPrefix(u, RepoURL+"/issues/new?") {
		t.Fatalf("not an issue URL: %s", u)
	}
	q, err := url.ParseQuery(u[strings.Index(u, "?")+1:])
	if err != nil {
		t.Fatal(err)
	}
	if q.Get("title") != "Model: AUS-PA279CRV-10209" {
		t.Errorf("title = %q", q.Get("title"))
	}
	body := q.Get("body")
	for _, want := range []string{"data/models/", "```toml", "[feature.bluelight]", "export-mccs"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q:\n%s", want, body)
		}
	}
	if strings.Contains(strings.ToLower(body), "serial") {
		t.Errorf("serials must never be submitted:\n%s", body)
	}
}

func TestFetch(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	const good = "mfg = \"AUS\"\nmodel = \"PA279CRV\"\nproduct = 10209\n\n[feature.bluelight]\nvcp = 0xE6\ntype = \"range\"\nmax = 20\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/data/models/AUS-PA279CRV-10209.toml":
			fmt.Fprint(w, good)
		case "/data/models/LIE-FAKE-1.toml":
			fmt.Fprint(w, "mfg = \"OTH\"\nmodel = \"ER\"\nproduct = 2\n\n[feature.x]\nvcp = 1\ntype = \"range\"\n")
		case "/data/models/BAD-1.toml":
			fmt.Fprint(w, "not toml at all {{{")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := repoRaw
	repoRaw = srv.URL + "/data/models"
	t.Cleanup(func() { repoRaw = old })

	if _, err := Fetch("../etc/passwd"); err == nil {
		t.Error("ids are file names: path traversal must be rejected")
	}
	if _, err := Fetch("AUS-PA279CRV-10209"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(UserModelsDir(), "AUS-PA279CRV-10209.toml")); err != nil {
		t.Error("fetched file not saved")
	}
	for _, id := range []string{"LIE-FAKE-1", "BAD-1", "NO-SUCH-9"} {
		if _, err := Fetch(id); err == nil {
			t.Errorf("%s must be rejected", id)
		}
	}
}

func TestFeedbackURL(t *testing.T) {
	u, err := url.Parse(FeedbackURL("DEL-U2720Q-16823", false, []string{"Ecomode"}, "v1"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("title") != "Model feedback: DEL-U2720Q-16823 does not work" {
		t.Errorf("title = %q", q.Get("title"))
	}
	if b := q.Get("body"); !strings.Contains(b, "- Ecomode") || !strings.Contains(b, "ddcdeck v1") {
		t.Errorf("body = %q", b)
	}
}
