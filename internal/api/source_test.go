package api

import "testing"

// Il link «Codice sorgente» (AGPL-3.0 §13) porta al commit in esecuzione quando è un hash.
func TestSourceLink(t *testing.T) {
	const repo = "https://github.com/ivseb/regia"
	for _, c := range []struct{ repo, commit, want string }{
		{repo, "d79e5e2", repo + "/tree/d79e5e2"},
		{repo, "d79e5e2c1f0a9b8e7d6c5b4a3f2e1d0c9b8a7f6e", repo + "/tree/d79e5e2c1f0a9b8e7d6c5b4a3f2e1d0c9b8a7f6e"},
		{repo, "sconosciuto", repo},
		{repo, "locale", repo},
		{repo, "abc", repo},
		{repo, "d79e5e2-dirty", repo},
		{"", "d79e5e2", ""},
	} {
		if got := sourceLink(c.repo, c.commit); got != c.want {
			t.Errorf("sourceLink(%q, %q) = %q, voglio %q", c.repo, c.commit, got, c.want)
		}
	}
}
