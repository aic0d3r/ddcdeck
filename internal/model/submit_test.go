package model

import (
	"net/url"
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
