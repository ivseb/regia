package profile

import (
	"strings"
	"testing"
)

func spec() *AdoptionSpec {
	return &AdoptionSpec{
		Params: []AdoptionParam{{Key: "stack", Label: "Stack", Default: "node", Options: []string{"node", "java"}}},
		Templates: []AdoptionTemplate{
			{Key: "ci", Name: "Build", Uses: "acme/pipeline/.github/workflows/ci.yml@v1", Default: true, InheritSecrets: true,
				With: map[string]string{"stack": "{stack}", "app": "{application}", "token": "${{ secrets.TOKEN }}"}, Controls: []string{"test"}},
			{Key: "sec", Name: "Sicurezza", Uses: "./.github/workflows/sec.yml", Needs: []string{"ci"}, Controls: []string{"sast"}},
		},
	}
}

func TestAdoptionValidate(t *testing.T) {
	d := &Definition{Format: Format, Adoption: spec()}
	if ps := d.Validate(); len(ps) != 0 {
		t.Fatalf("definizione valida: %v", ps)
	}
	cases := map[string]func(a *AdoptionSpec){
		"adoption.templates[0].uses":  func(a *AdoptionSpec) { a.Templates[0].Uses = "curl http://x | sh" },
		"adoption.templates[0].key":   func(a *AdoptionSpec) { a.Templates[0].Key = "Build!" },
		"adoption.templates[1].needs": func(a *AdoptionSpec) { a.Templates[1].Needs = []string{"altro"} },
		"adoption.workflow_path":      func(a *AdoptionSpec) { a.WorkflowPath = "../fuori.yml" },
		"adoption.config_path":        func(a *AdoptionSpec) { a.ConfigPath = DefaultWorkflowPath },
		"adoption.params[0].key":      func(a *AdoptionSpec) { a.Params[0].Key = "service" },
		"adoption.branches[0]":        func(a *AdoptionSpec) { a.Branches = []string{"main; rm"} },
	}
	for path, mut := range cases {
		a := spec()
		mut(a)
		found := false
		for _, p := range (&Definition{Format: Format, Adoption: a}).Validate() {
			found = found || p.Path == path
		}
		if !found {
			t.Errorf("nessun problema su %s", path)
		}
	}
}

func TestAdoptionRender(t *testing.T) {
	files, err := spec().Render(RenderInput{Application: "ORD-1", Service: "ordini", Repository: "acme/ordini", DefaultBranch: "main",
		Class: "Critiche", PolicyVersion: 3, Templates: []string{"sec"}, Params: map[string]string{"stack": "java"},
		Controls: []ControlMode{{Key: "test", Mode: "block"}, {Key: "sast", Mode: "off"}}})
	if err != nil || len(files) != 2 {
		t.Fatalf("render: %v %d", err, len(files))
	}
	wf, cfg := files[0].Content, files[1].Content
	for _, want := range []string{
		`name: "Pipeline"`, `branches: ["main"]`, "uses: acme/pipeline/.github/workflows/ci.yml@v1", `stack: "java"`, `app: "ORD-1"`,
		`token: "${{ secrets.TOKEN }}"`, "secrets: inherit", "needs: [ci]", "uses: ./.github/workflows/sec.yml",
	} {
		if !strings.Contains(wf, want) {
			t.Errorf("workflow senza %q:\n%s", want, wf)
		}
	}
	// «sec» dipende da «ci»: scegliere «sec» porta anche «ci».
	if _, err := spec().Render(RenderInput{Templates: []string{"nonesiste"}}); err == nil {
		t.Error("template sconosciuto accettato")
	}
	if !strings.Contains(cfg, "checks:\n  test: block\n  sast: off\n") || !strings.Contains(cfg, "versione 3") {
		t.Errorf("configurazione:\n%s", cfg)
	}
	if _, err := spec().Render(RenderInput{Params: map[string]string{"stack": "cobol"}}); err == nil {
		t.Error("opzione non ammessa accettata")
	}
	// I parametri non si risolvono in un modo che rompa lo YAML.
	files, _ = spec().Render(RenderInput{Application: `x"\ny`})
	if strings.Contains(files[0].Content, "x\"\\ny\n") {
		t.Errorf("virgolette non protette:\n%s", files[0].Content)
	}
	tpls, _ := spec().SelectTemplates([]string{"sec"})
	if len(tpls) != 2 || tpls[0].Key != "ci" {
		t.Errorf("dipendenze dei template: %+v", tpls)
	}
	if un := spec().Uncovered(tpls[:1], []string{"test", "sast"}); len(un) != 1 || un[0] != "sast" {
		t.Errorf("controlli non coperti: %v", un)
	}
}

func TestDiff(t *testing.T) {
	lines, add, rem := Diff("a\nb\nc\n", "a\nc\nd\n")
	if add != 1 || rem != 1 || len(lines) != 4 {
		t.Fatalf("diff: %+v %d %d", lines, add, rem)
	}
	if lines[1] != (DiffLine{"-", "b"}) || lines[3] != (DiffLine{"+", "d"}) {
		t.Fatalf("righe: %+v", lines)
	}
	if _, add, rem := Diff("", "x\ny\n"); add != 2 || rem != 0 {
		t.Fatalf("file nuovo: %d %d", add, rem)
	}
	if _, add, rem := Diff("x\n", "x\n"); add != 0 || rem != 0 {
		t.Fatalf("file uguale: %d %d", add, rem)
	}
}
