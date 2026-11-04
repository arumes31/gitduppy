package models

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestRedactURLCredentials(t *testing.T) {
	cases := map[string]string{
		"https://user:token@github.com/a/b.git": "https://github.com/a/b.git",
		"https://ghp_secret@github.com/a/b.git": "https://github.com/a/b.git",
		"http://u:p@host:8080/x.git":            "http://host:8080/x.git",
		"ssh://git:pw@host:2222/a/b.git":        "ssh://git@host:2222/a/b.git",
		"ssh://git@host/a/b.git":                "ssh://git@host/a/b.git",
		"git@github.com:user/repo.git":          "git@github.com:user/repo.git",
		"https://github.com/a/b.git":            "https://github.com/a/b.git",
		"https://host/a.git?token=secret":       "https://host/a.git",
		"https://u:p@host/a.git?x=1#f":          "https://host/a.git",
		"https://us er:t%zz@host/a.git":         "https://host/a.git", // unparseable
		"":                                      "",
	}
	for in, want := range cases {
		if got := RedactURLCredentials(in); got != want {
			t.Errorf("RedactURLCredentials(%q)=%q want %q", in, got, want)
		}
	}
	if !URLHasCredentials("https://u:p@h/a.git") || URLHasCredentials("ssh://git@h/a.git") {
		t.Error("URLHasCredentials misclassified a URL")
	}
}

func TestRepositoryMarshalJSONRedactsURL(t *testing.T) {
	repo := Repository{ID: uuid.New(), Name: "r", URL: "https://user:s3cret@github.com/a/b.git"}
	job := CloneJob{ID: uuid.New(), RepositoryID: repo.ID, Repository: &repo}

	for name, v := range map[string]any{"value": repo, "pointer": &repo, "slice": []Repository{repo}, "preload": job} {
		out, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("%s: marshal: %v", name, err)
		}
		if strings.Contains(string(out), "s3cret") || strings.Contains(string(out), "user@") {
			t.Errorf("%s: credentials leaked in JSON: %s", name, out)
		}
		if !strings.Contains(string(out), `"url":"https://github.com/a/b.git"`) {
			t.Errorf("%s: redacted url missing: %s", name, out)
		}
	}
	if repo.URL != "https://user:s3cret@github.com/a/b.git" {
		t.Error("marshal must not mutate the in-memory URL used for cloning")
	}
}
