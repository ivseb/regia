package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	en, it, bad := "en", "it", "fr"
	for _, c := range []struct {
		member, tenant *string
		want           string
	}{
		{nil, nil, "it"}, {&en, nil, "en"}, {nil, &en, "en"}, {&it, &en, "it"}, {&en, &it, "en"}, {&bad, &en, "en"}, {&bad, &bad, "it"},
	} {
		if got := Resolve(c.member, c.tenant); got != c.want {
			t.Errorf("Resolve(%v, %v) = %q, voglio %q", c.member, c.tenant, got, c.want)
		}
	}
	for in, want := range map[string]string{"en-GB": "en", "EN": "en", "it": "it", "de": "it", "": "it"} {
		if got := Norm(in); got != want {
			t.Errorf("Norm(%q) = %q, voglio %q", in, got, want)
		}
	}
}

func TestTemplates(t *testing.T) {
	Register(map[string]Msg{"test.plain": {It: "Ciao {nome}", En: "Hello {nome}"},
		"test.plural": {It: "{count} elementi", En: "{count} items", OneIt: "1 elemento", OneEn: "1 item"}})
	if got := T("en", "test.plain", P{"nome": "Ada"}); got != "Hello Ada" {
		t.Errorf("segnaposto: %q", got)
	}
	if got := T("it", "test.plain"); got != "Ciao {nome}" {
		t.Errorf("segnaposto mancante resta com'è: %q", got)
	}
	for _, c := range []struct {
		lang string
		n    int
		want string
	}{{"it", 1, "1 elemento"}, {"it", 3, "3 elementi"}, {"en", 1, "1 item"}, {"en", 0, "0 items"}} {
		if got := T(c.lang, "test.plural", P{"count": c.n}); got != c.want {
			t.Errorf("plurale %s %d: %q, voglio %q", c.lang, c.n, got, c.want)
		}
	}
	if got := T("en", "non.esiste"); got != "non.esiste" {
		t.Errorf("chiave sconosciuta: %q", got)
	}
	if got := Name("en", "Fase attuale"); got != "Current phase" {
		t.Errorf("nome in inglese: %q", got)
	}
	if got := Name("it", "Fase attuale"); got != "Fase attuale" {
		t.Errorf("nome in italiano: %q", got)
	}
	if got := Name("en", "Un nome senza traduzione"); got != "Un nome senza traduzione" {
		t.Errorf("riserva in italiano: %q", got)
	}
}

var placeholders = regexp.MustCompile(`\{[a-z_]+\}`)

// Ogni testo del catalogo ha le due lingue e gli stessi segnaposto in entrambe (anche nel singolare).
func TestCatalogConsistency(t *testing.T) {
	set := func(s string) string {
		m := placeholders.FindAllString(s, -1)
		sort.Strings(m)
		return strings.Join(m, " ")
	}
	for _, k := range Keys() {
		m, _ := Entry(k)
		if set(m.It) != set(m.En) {
			t.Errorf("%s: segnaposto diversi tra le lingue: %q / %q", k, m.It, m.En)
		}
		if (m.OneIt == "") != (m.OneEn == "") {
			t.Errorf("%s: il singolare c'è solo in una lingua", k)
		}
		if m.OneIt != "" && set(m.OneIt) != set(m.OneEn) {
			t.Errorf("%s: segnaposto diversi nel singolare", k)
		}
	}
}

// Nessun nome di sistema tradotto è vuoto o uguale a una chiave con spazi di troppo.
func TestNamesWellFormed(t *testing.T) {
	for it, en := range NamesEn() {
		if strings.TrimSpace(it) != it || strings.TrimSpace(en) != en || en == "" {
			t.Errorf("voce malformata: %q → %q", it, en)
		}
	}
}

func evalString(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind == token.STRING {
			s, err := strconv.Unquote(x.Value)
			return s, err == nil
		}
	case *ast.BinaryExpr:
		if x.Op == token.ADD {
			a, ok1 := evalString(x.X)
			b, ok2 := evalString(x.Y)
			return a + b, ok1 && ok2
		}
	case *ast.ParenExpr:
		return evalString(x.X)
	}
	return "", false
}

// I nomi dei fatti e delle azioni che i moduli registrano (FactInfo, ActionSchema, ActionField:
// Name, Description, Help) hanno la traduzione inglese: sono i testi dell'editor dei processi.
func TestSystemNamesAreTranslated(t *testing.T) {
	root := filepath.Join("..", "..") // internal/
	fset := token.NewFileSet()
	missing := map[string]string{}
	typeName := func(e ast.Expr) string {
		switch x := e.(type) {
		case *ast.SelectorExpr:
			return x.Sel.Name
		case *ast.Ident:
			return x.Name
		}
		return ""
	}
	check := func(kind string, cl *ast.CompositeLit, file string) {
		for _, el := range cl.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			k, _ := kv.Key.(*ast.Ident)
			if k == nil || (k.Name != "Name" && k.Name != "Description" && k.Name != "Help") {
				continue
			}
			if s, ok := evalString(kv.Value); ok && s != "" {
				if _, has := NameEn(s); !has {
					missing[s] = kind + "." + k.Name + " " + file
				}
			}
		}
	}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			cl, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			switch tn := typeName(cl.Type); tn {
			case "FactInfo", "ActionSchema", "ActionField":
				check(tn, cl, filepath.Base(p))
			}
			// Elementi con il tipo sottinteso: []engine.ActionField{{Key: …}}.
			if at, ok := cl.Type.(*ast.ArrayType); ok {
				if tn := typeName(at.Elt); tn == "FactInfo" || tn == "ActionField" {
					for _, el := range cl.Elts {
						if inner, ok := el.(*ast.CompositeLit); ok && inner.Type == nil {
							check(tn, inner, filepath.Base(p))
						}
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var list []string
	for s, where := range missing {
		list = append(list, where+": "+s)
	}
	sort.Strings(list)
	for _, l := range list {
		t.Errorf("senza traduzione inglese in internal/platform/i18n/names_en.go: %s", l)
	}
}
