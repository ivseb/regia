package contract

import (
	"reflect"
	"testing"
)

func TestCommitKeys(t *testing.T) {
	cases := []struct {
		in   []string
		want []string
	}{
		{[]string{"HL-12 sistema il login"}, []string{"HL-12"}},
		{[]string{"fix: login (HL-12)\n\nCloses HL-13 e HL-12 di nuovo"}, []string{"HL-12", "HL-13"}},
		{[]string{"feature/HL-7-nuova-pagina"}, []string{"HL-7"}},
		{[]string{"titolo PR", "bugfix/ARC2-30_x"}, []string{"ARC2-30"}},
		{[]string{"hl-12 minuscolo, X-1 corto"}, nil},
		{[]string{"XHL-12 ok"}, []string{"XHL-12"}},
		{[]string{"nessuna chiave qui"}, nil},
		{[]string{"ABCDEFGHIJKLMNOPQRSTU-4 troppo lungo, HL-0 zero"}, nil},
	}
	for _, c := range cases {
		var got []string
		for _, r := range CommitKeys(c.in...) {
			got = append(got, r.String())
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("CommitKeys(%q) = %v, voluto %v", c.in, got, c.want)
		}
	}
}

func TestCommitTitle(t *testing.T) {
	if got := CommitTitle("  prima riga \nseconda", 50); got != "prima riga" {
		t.Errorf("titolo = %q", got)
	}
	if got := CommitTitle("abcdefghij", 5); got != "abcd…" {
		t.Errorf("troncato = %q", got)
	}
}
