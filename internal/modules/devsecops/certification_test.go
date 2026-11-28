package devsecops

import (
	"testing"

	"regia/internal/modules/devsecops/contract"
)

func TestTestRunOf(t *testing.T) {
	n := func(p *int) int {
		if p == nil {
			return -1
		}
		return *p
	}
	cases := []struct {
		summary                           string
		executed, passed, failed, skipped int
	}{
		{"120 passed, 2 skipped", 122, 120, -1, 2},
		{"Tests: 50, 48 passati, 2 falliti", 50, 48, 2, -1},
		{"tutto verde", -1, -1, -1, -1},
	}
	for _, c := range cases {
		r := testRunOf("pipeline", ReleaseView{Service: "s", Version: "1"}, contract.CheckResult{Name: "test", Outcome: "passed", Summary: c.summary})
		if n(r.Executed) != c.executed || n(r.Passed) != c.passed || n(r.Failed) != c.failed || n(r.Skipped) != c.skipped {
			t.Errorf("%q: %d/%d/%d/%d", c.summary, n(r.Executed), n(r.Passed), n(r.Failed), n(r.Skipped))
		}
	}
	for name, want := range map[string]bool{"test": true, "unit-tests": true, "e2e": true, "lint": false, "sonar": false, "integration-test": true} {
		if isTestCheck(name) != want {
			t.Errorf("isTestCheck(%q) = %v", name, !want)
		}
	}
	if !isSonarCheck("sonarqube") || isSonarCheck("semgrep") {
		t.Error("isSonarCheck")
	}
}

func TestNoteGroup(t *testing.T) {
	for k, want := range map[string]string{"story": NoteNew, "bug": NoteFix, "incident": NoteFix, "task": NoteOther, "epic": NoteNew} {
		if noteGroup(k) != want {
			t.Errorf("noteGroup(%q) = %q", k, noteGroup(k))
		}
	}
}
