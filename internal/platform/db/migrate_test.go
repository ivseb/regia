package db

import (
	"reflect"
	"testing"
)

func TestSplitStatements(t *testing.T) {
	sql := `-- regia:no-transaction
-- Indici senza blocco.
create index concurrently if not exists a_idx on t (x);
/* commento; con punto e virgola */
insert into t values ('a;b', 'it''s; ok', "col;umn");
do $$ begin perform 1; end $$;
select $tag$ testo; $$ annidato $tag$;
-- solo un commento;
select 1`
	got, err := SplitStatements(sql)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"-- regia:no-transaction\n-- Indici senza blocco.\ncreate index concurrently if not exists a_idx on t (x)",
		"/* commento; con punto e virgola */\ninsert into t values ('a;b', 'it''s; ok', \"col;umn\")",
		"do $$ begin perform 1; end $$",
		"select $tag$ testo; $$ annidato $tag$",
		"-- solo un commento;\nselect 1",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("istruzioni:\n%q\nattese:\n%q", got, want)
	}
	for _, bad := range []string{"select 'aperta", "select $$ aperto", "/* aperto"} {
		if _, err := SplitStatements(bad); err == nil {
			t.Errorf("%q: atteso un errore", bad)
		}
	}
}

func TestNoTxDirective(t *testing.T) {
	cases := map[string]bool{
		"-- regia:no-transaction\ncreate index concurrently x on t (a);":                     true,
		"-- Indici grandi\n\n-- regia:no-transaction\ncreate index concurrently x on t (a);": true,
		"create table t (a int);\n-- regia:no-transaction":                                   false, // solo in testa
		"-- regia:no-transactions\nselect 1":                                                 false,
		"select 1":                                                                           false,
	}
	for body, want := range cases {
		if got := hasNoTxDirective([]byte(body)); got != want {
			t.Errorf("%q: %v, atteso %v", body, got, want)
		}
	}
	m := concurrentIndex.FindStringSubmatch(stripLeadingComments("-- c\ncreate unique index concurrently if not exists \"elements_x\" on elements (a)"))
	if m == nil || m[1] == "" || m[2] != `"elements_x"` || m[3] != "elements" || m[4] != "(a)" {
		t.Fatalf("nome dell'indice: %v", m)
	}
}

func TestChildIndexName(t *testing.T) {
	if n := childIndexName("public.activities_2026_11", "activities_x_idx"); n != "activities_x_idx_activities_2026_11" {
		t.Fatal(n)
	}
	long := childIndexName("activities_partizione_con_un_nome_molto_lungo", "indice_con_un_nome_anche_lui_lungo")
	if len(long) > 63 {
		t.Fatalf("%s: %d byte", long, len(long))
	}
}
