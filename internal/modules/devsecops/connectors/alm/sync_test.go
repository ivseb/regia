package alm

import (
	"regexp"
	"testing"
)

func TestNormStatus(t *testing.T) {
	for in, want := range map[string]string{"Passed": "passed", "Failed": "failed", "Blocked": "blocked", "Not Completed": "not_completed",
		"No Run": "no_run", "": "no_run", "N/A": "na", "Passed with warnings": "passed", "Da rivedere": "na"} {
		if got := NormStatus(in); got != want {
			t.Errorf("%q: %s, atteso %s", in, got, want)
		}
	}
}

func TestNormSeverity(t *testing.T) {
	for in, want := range map[string]string{"5-Urgent": "critical", "4-Very High": "high", "3-High": "high", "2-Medium": "medium",
		"1-Low": "low", "Bloccante": "critical", "Alta": "high", "Media": "medium", "Bassa": "low", "": "", "Boh": ""} {
		if got := NormSeverity(in); got != want {
			t.Errorf("%q: %s, atteso %s", in, got, want)
		}
	}
}

func TestChangeOf(t *testing.T) {
	// changeOf: il numero si estrae dal valore del campo o dal nome, in maiuscolo.
	pc := &projCtx{}
	pc.pattern = mustPattern(DefaultChangePattern)
	for in, want := range map[string]string{"CHG0045120": "CHG0045120", "chg0045120 - regressione": "CHG0045120", "Regressione dicembre": "", " ": ""} {
		if got := pc.changeOf(in); got != want {
			t.Errorf("%q: %q, atteso %q", in, got, want)
		}
	}
}

func mustPattern(p string) *regexp.Regexp { return regexp.MustCompile(p) }
