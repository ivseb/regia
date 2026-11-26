package profile

import (
	"reflect"
	"testing"
)

func TestSchemeOf(t *testing.T) {
	cases := map[string]string{
		"v1.4.0":                  "v[0-9]*",
		"1.4.0":                   "[0-9]*",
		"chart-1.4.0":             "chart-[0-9]*",
		"smart-proxy-chart-0.3.1": "smart-proxy-chart-[0-9]*",
		"k8s-app-2.0":             "k8s-app-[0-9]*",
		"release-42":              "release-[0-9]*",
		"helm/v1.2":               "helm/v[0-9]*",
		"latest":                  "latest",
		"a*b-1.0":                 `a\*b-[0-9]*`,
	}
	for tag, want := range cases {
		got := SchemeOf(tag)
		if got != want {
			t.Errorf("SchemeOf(%q) = %q, atteso %q", tag, got, want)
		}
		if ok, err := matchTag(got, tag); err != nil || !ok {
			t.Errorf("il modello %q non riconosce il suo tag %q (%v)", got, tag, err)
		}
	}
}

func TestTagSchemes(t *testing.T) {
	got := TagSchemes([]string{"v1.2.0", "chart-0.3.1", "v1.1.0", "chart-0.3.0", "v1.0.0", "chart-0.2.0", "v0.9.0", "v0.8.0"})
	if len(got) != 2 || got[0].Pattern != "v[0-9]*" || got[0].Count != 5 || got[1].Pattern != "chart-[0-9]*" || got[1].Count != 3 {
		t.Fatalf("schemi: %+v", got)
	}
	if !reflect.DeepEqual(got[0].Examples, []string{"v1.2.0", "v1.1.0", "v1.0.0"}) {
		t.Fatalf("esempi: %v", got[0].Examples)
	}
	// Lo schema «v» non prende i tag di un prefisso che inizia con v (vapp-1.0).
	if ok, _ := matchTag("v[0-9]*", "vapp-1.0"); ok {
		t.Fatal("v[0-9]* non deve riconoscere vapp-1.0")
	}
	if one := TagSchemes([]string{"v1.0", "v1.1"}); len(one) != 1 {
		t.Fatalf("un solo schema: %+v", one)
	}
}

func TestTagFilterPerRepository(t *testing.T) {
	// Senza profilo: un profilo nuovo con il filtro del repository e il prefisso «v» dei dati base.
	d, err := (*Definition)(nil).WithTagFilter("ivan/smart-proxy", "v[0-9]*")
	if err != nil {
		t.Fatal(err)
	}
	if ps := d.Validate(); len(ps) > 0 {
		t.Fatalf("profilo nuovo non valido: %v", ps)
	}
	if !d.AcceptsRepoTag("ivan/smart-proxy", "v1.2.0", false) || d.AcceptsRepoTag("ivan/smart-proxy", "chart-1.2.0", false) {
		t.Fatal("filtro del repository non applicato")
	}
	if !d.AcceptsRepoTag("ivan/altro", "chart-1.2.0", false) || d.AcceptsRepoTag("ivan/altro", "v2.0", true) {
		t.Fatal("gli altri repository leggono tutti i tag, senza pre-release")
	}
	if out := d.ExtractRelease(ReleaseInput{Tag: "v1.2.0"}, ""); out.Release.Version != "1.2.0" {
		t.Fatalf("versione: %q", out.Release.Version)
	}
	if !d.HasTagFilter("ivan/smart-proxy") || d.HasTagFilter("ivan/altro") {
		t.Fatal("HasTagFilter")
	}
	// Con un profilo che ha già un filtro generale: quello del repository vale al suo posto,
	// «tutti i tag» compreso; l'originale non cambia.
	base := &Definition{Format: Format, Release: &ReleaseSpec{TagFilter: "v*"}}
	d2, err := base.WithTagFilter("ivan/charts", "")
	if err != nil {
		t.Fatal(err)
	}
	if !d2.AcceptsRepoTag("ivan/charts", "chart-1.0", false) || d2.AcceptsRepoTag("ivan/altro", "chart-1.0", false) {
		t.Fatal("filtro generale e filtro del repository")
	}
	if base.HasTagFilter("ivan/charts") {
		t.Fatal("WithTagFilter ha cambiato l'originale")
	}
	if _, err := base.WithTagFilter("x/y", "["); err == nil {
		t.Fatal("modello non valido accettato")
	}
	bad := &Definition{Format: Format, Release: &ReleaseSpec{TagFilters: map[string]string{"x/y": "["}}}
	if len(bad.Validate()) == 0 {
		t.Fatal("Validate accetta un modello non valido")
	}
}

func TestTagOfReleaseURL(t *testing.T) {
	cases := map[string]string{
		"https://github.com/ivan/smart-proxy/releases/tag/v1.2.0":        "v1.2.0",
		"https://git.example.it/casa/infra/releases/tag/chart%2F1.0?x=1": "chart/1.0",
		"https://example.com/altro":                                      "",
	}
	for u, want := range cases {
		if got := TagOfReleaseURL(u); got != want {
			t.Errorf("TagOfReleaseURL(%q) = %q, atteso %q", u, got, want)
		}
	}
}
