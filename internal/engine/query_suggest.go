package engine

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Autocompletamento del linguaggio di query: in base alla posizione del cursore propone
// campi, valori (fasi del tipo, opzioni, persone, team, applicazioni, asset, spazi,
// parole come me, oggi, aperta), operatori e ordinamenti. Le letture sono poche e
// limitate (al più qualche decina di righe), per restare sotto i 30 ms.

// Suggestion è una proposta: Insert sostituisce l'intervallo [From, To) della query.
type Suggestion struct {
	Kind   string `json:"kind"` // field | value | keyword | operator | sort | direction
	Label  string `json:"label"`
	Insert string `json:"insert"`
	Detail string `json:"detail,omitempty"`
}

// Suggestions è la risposta dell'autocompletamento. From e To sono in unità UTF-16.
type Suggestions struct {
	From    int          `json:"from"`
	To      int          `json:"to"`
	Context string       `json:"context"` // field | value | sort | direction
	Field   string       `json:"field,omitempty"`
	Items   []Suggestion `json:"items"`
	// Error è il primo errore della query intera (sintassi o significato), da sottolineare.
	Error *QueryError `json:"error,omitempty"`
}

const maxSuggestions = 20

// spot descrive il punto della query in cui si trova il cursore.
type spot struct {
	kind       string
	field      string
	from, to   int      // byte: intervallo da sostituire
	prefix     string   // testo già scritto (senza virgolette né operatore)
	op         string   // operatore già scritto, da conservare
	neg        bool     // "-" davanti al nome del campo
	colonAfter bool     // il nome del campo è già seguito da ":"
	types      []string // tipi indicati nella query, per proporre le loro fasi
}

func locate(src string, toks []qtok, bpos int) spot {
	sp := spot{kind: "field", from: bpos, to: bpos}
	ci := -1
	for i, t := range toks {
		if (t.kind == qWord || t.kind == qQuoted) && t.pos < bpos && bpos <= t.end {
			ci = i
			break
		}
	}
	pi := -1
	if ci >= 0 {
		t := toks[ci]
		sp.from, sp.to = t.pos, t.end
		if t.kind == qQuoted {
			end := bpos
			if end > t.pos+1+len(t.text) {
				end = t.pos + 1 + len(t.text)
			}
			sp.prefix = src[t.pos+1 : max(end, t.pos+1)]
		} else {
			sp.prefix = src[t.pos:bpos]
		}
		pi = ci - 1
	} else {
		for i, t := range toks {
			if t.end <= bpos {
				pi = i
			}
		}
	}
	// Nome di campo già seguito da ":": si corregge il nome.
	if ci >= 0 && ci+1 < len(toks) && toks[ci+1].kind == qColon && sortHead(toks, ci) == 0 {
		sp.colonAfter = true
		return fieldSpot(sp)
	}
	if pi < 0 {
		return fieldSpot(sp)
	}
	clauseField := func(j int) (string, bool) {
		// Risale fino ai due punti della clausola: il nome è il token prima.
		for ; j >= 0; j-- {
			if toks[j].kind == qColon {
				if j >= 1 && (sortHead(toks, j-1) == 2 || (j >= 2 && sortHead(toks, j-2) == 3)) {
					return "", false
				}
				if j >= 1 && (toks[j-1].kind == qWord || toks[j-1].kind == qQuoted) {
					return strings.TrimPrefix(toks[j-1].text, "-"), true
				}
				return "", false
			}
		}
		return "", false
	}
	p := toks[pi]
	switch {
	case p.kind == qColon:
		if (pi >= 1 && sortHead(toks, pi-1) == 2) || (pi >= 2 && sortHead(toks, pi-2) == 3) {
			sp.kind = "sort"
			return sp
		}
		if f, ok := clauseField(pi); ok {
			return valueSpot(sp, f)
		}
	case p.kind == qComma, p.kind == qWord && (orWords[strings.ToLower(p.text)] || opOnly[p.text]):
		if f, ok := clauseField(pi); ok {
			return valueSpot(sp, f)
		}
	case p.kind == qWord || p.kind == qQuoted:
		// Dopo il campo dell'ordinamento si propone la direzione.
		if (pi >= 2 && sortHead(toks, pi-2) == 2) || (pi >= 3 && sortHead(toks, pi-3) == 3) {
			sp.kind = "direction"
			return sp
		}
	}
	return fieldSpot(sp)
}

func fieldSpot(sp spot) spot {
	sp.kind = "field"
	if strings.HasPrefix(sp.prefix, "-") {
		sp.neg, sp.prefix = true, sp.prefix[1:]
	}
	return sp
}

func valueSpot(sp spot, field string) spot {
	sp.kind, sp.field = "value", field
	for _, op := range []string{">=", "<=", ">", "<", "="} {
		if strings.HasPrefix(sp.prefix, op) {
			sp.op, sp.prefix = op, sp.prefix[len(op):]
			break
		}
	}
	if k := strings.Index(sp.prefix, ".."); k >= 0 {
		sp.op += sp.prefix[:k+2]
		sp.prefix = sp.prefix[k+2:]
	}
	return sp
}

// Suggest propone i completamenti per la query src con il cursore in pos (unità UTF-16).
func (s *Service) Suggest(ctx context.Context, tx pgx.Tx, actor Actor, src string, pos int) (*Suggestions, error) {
	if n := u16(src, len(src)); pos < 0 || pos > n {
		pos = n
	}
	bpos := fromU16(src, pos)
	toks, _ := lexQuery(src, true)
	// Dentro una sottoquery «( … )» si propone come in una query a sé (stesse posizioni).
	from, to := 0, len(src)
	for found := true; found; {
		found = false
		for _, t := range toks {
			if t.kind == qSub && t.in0 <= bpos && bpos <= t.in1 {
				from, to = t.in0, t.in1
				toks, _ = lexRange(src, from, to, true)
				found = true
				break
			}
		}
	}
	cat, err := s.catalog(ctx, tx)
	if err != nil {
		return nil, err
	}
	sp := locate(src, toks, bpos)
	sp.types = []string{}
	if tq, _ := parseToks(src, toks, true, 0); tq != nil {
		for _, c := range tq.Clauses {
			if f := cat.lookup(c.Name); f != nil && f.sys != nil && f.sys.kind == qkType && !c.Negated {
				sp.types = append(sp.types, lowerAll(c.Values)...)
			}
		}
	}
	out := &Suggestions{From: u16(src, sp.from), To: u16(src, sp.to), Context: sp.kind, Items: []Suggestion{}}
	switch sp.kind {
	case "field":
		out.Items = suggestFields(cat, sp)
	case "sort":
		p := strings.ToLower(sp.prefix)
		for _, f := range cat.fields {
			if f.sortable() {
				if n := firstName(f, p); n != "" {
					out.Items = append(out.Items, Suggestion{Kind: "sort", Label: n, Insert: n, Detail: f.label})
				}
			}
		}
	case "direction":
		for _, d := range []Suggestion{{Kind: "direction", Label: "asc", Insert: "asc", Detail: "crescente"}, {Kind: "direction", Label: "desc", Insert: "desc", Detail: "decrescente"}} {
			if strings.HasPrefix(d.Label, strings.ToLower(sp.prefix)) {
				out.Items = append(out.Items, d)
			}
		}
	case "value":
		if f := cat.lookup(sp.field); f != nil {
			out.Field = f.name
			if out.Items, err = s.suggestValues(ctx, tx, actor, f, sp); err != nil {
				return nil, err
			}
		}
	}
	finish(src, sp, out)
	// Errore della query intera: per ultimo, perché un errore SQL interromperebbe la transazione.
	if out.Error, err = s.validate(ctx, tx, actor, src); err != nil {
		return nil, err
	}
	return out, nil
}

// finish completa i testi da inserire: operatore già scritto, spazio dopo il frammento.
func finish(src string, sp spot, out *Suggestions) {
	// I campi si elencano tutti (sono qualche decina); i valori al massimo maxSuggestions.
	if len(out.Items) > maxSuggestions && sp.kind != "field" && sp.kind != "sort" {
		out.Items = out.Items[:maxSuggestions]
	}
	next := byte(0)
	if sp.to < len(src) {
		next = src[sp.to]
	}
	lead := ""
	if sp.kind == "value" && sp.from == sp.to && sp.from > 0 && (src[sp.from-1] == ':' || src[sp.from-1] == ',') {
		lead = " "
	}
	for i := range out.Items {
		it := &out.Items[i]
		switch {
		case it.Kind == "field" && sp.colonAfter:
		case it.Kind == "operator":
			it.Insert = lead + it.Insert
			continue
		case it.Kind == "value" || it.Kind == "keyword":
			it.Insert = lead + sp.op + it.Insert
		}
		if !strings.HasSuffix(it.Insert, " ") && !sp.colonAfter && next != ' ' && next != ',' {
			it.Insert += " "
		}
	}
}

// validate restituisce il primo errore della query, se c'è.
func (s *Service) validate(ctx context.Context, tx pgx.Tx, actor Actor, src string) (*QueryError, error) {
	q, err := Parse(src)
	if err == nil {
		_, err = s.plan(ctx, tx, actor, q)
	}
	var qe *QueryError
	var ve *ValidationError
	switch {
	case err == nil:
		return nil, nil
	case errors.As(err, &qe):
		return qe, nil
	case errors.As(err, &ve):
		return &QueryError{Msg: ve.Msg, Pos: 0, End: u16(src, len(src))}, nil
	}
	return nil, err
}

func allNames(f *qfield) []string {
	switch {
	case f.sys != nil:
		return f.sys.names
	case f.mod != nil:
		return f.mod.Names
	}
	return append([]string{f.custom.Key}, f.custom.Aliases...)
}

// firstName è il primo nome del campo che inizia con il prefisso ("" = nessuno).
func firstName(f *qfield, p string) string {
	for _, n := range allNames(f) {
		if strings.HasPrefix(strings.ToLower(n), p) {
			return strings.ToLower(n)
		}
	}
	return ""
}

func suggestFields(cat *catalog, sp spot) []Suggestion {
	p := strings.ToLower(sp.prefix)
	var out []Suggestion
	for _, f := range cat.fields {
		n := firstName(f, p)
		if n == "" {
			continue
		}
		ins := n
		if !sp.colonAfter {
			ins += ": "
		}
		if sp.neg {
			ins = "-" + ins
		}
		out = append(out, Suggestion{Kind: "field", Label: n, Insert: ins, Detail: f.label})
	}
	// La relazione generica (query_rel.go): i tipi di relazione si scrivono per nome.
	if strings.HasPrefix("collegata", p) {
		ins := "collegata"
		if !sp.colonAfter {
			ins += ": "
		}
		if sp.neg {
			ins = "-" + ins
		}
		out = append(out, Suggestion{Kind: "field", Label: "collegata", Insert: ins, Detail: "Collegata a elementi: chiavi o sottoquery, es. collegata: (tipo: risk fase: aperta)"})
	}
	// Le relazioni dagli impatti (query_rel.go), quando si comincia a scriverle.
	for _, r := range ImpactRelations {
		if p == "" || !strings.HasPrefix(r.Key, p) {
			continue
		}
		ins := r.Key
		if !sp.colonAfter {
			ins += ": "
		}
		if sp.neg {
			ins = "-" + ins
		}
		out = append(out, Suggestion{Kind: "field", Label: r.Key, Insert: ins, Detail: "Dagli impatti: " + r.Outward})
	}
	if !sp.neg && !sp.colonAfter && p != "" && (strings.HasPrefix("ordina per", p) || strings.HasPrefix("sort by", p)) {
		out = append([]Suggestion{{Kind: "sort", Label: "ordina per", Insert: "ordina per: ", Detail: "Ordinamento dei risultati"}}, out...)
	} else if p == "" && !sp.neg && !sp.colonAfter {
		out = append(out, Suggestion{Kind: "sort", Label: "ordina per", Insert: "ordina per: ", Detail: "Ordinamento dei risultati"})
	}
	return out
}

var categoryNames = map[string]string{"todo": "da fare", "in_progress": "in corso", "done": "conclusa", "cancelled": "annullata"}

func likePrefix(p string) (string, string) {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	e := r.Replace(p)
	return e + "%", "% " + e + "%"
}

func quoteValue(v string) string {
	if v == "" || strings.ContainsAny(v, " \t,:\"") {
		return `"` + strings.ReplaceAll(v, `"`, "") + `"`
	}
	return v
}

func (s *Service) suggestValues(ctx context.Context, tx pgx.Tx, actor Actor, f *qfield, sp spot) ([]Suggestion, error) {
	p := strings.ToLower(sp.prefix)
	match := func(xs ...string) bool {
		if p == "" {
			return true
		}
		for _, x := range xs {
			lx := strings.ToLower(x)
			if strings.HasPrefix(lx, p) || strings.Contains(lx, " "+p) {
				return true
			}
		}
		return false
	}
	var out []Suggestion
	add := func(kind, label, insert, detail string, also ...string) {
		if match(append([]string{label}, also...)...) {
			out = append(out, Suggestion{Kind: kind, Label: label, Insert: insert, Detail: detail})
		}
	}
	rowsOf := func(sql string, args ...any) ([][2]string, error) {
		rows, err := tx.Query(ctx, sql, args...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var res [][2]string
		for rows.Next() {
			var a, b string
			if err := rows.Scan(&a, &b); err != nil {
				return nil, err
			}
			res = append(res, [2]string{a, b})
		}
		return res, rows.Err()
	}
	l1, l2 := likePrefix(sp.prefix)
	dateWords := func() {
		for _, w := range [][2]string{{"oggi", "il giorno di oggi"}, {"ieri", "il giorno di ieri"}, {"settimana", "da lunedì"}, {"mese", "il mese in corso"},
			{"-7g", "ultimi 7 giorni"}, {"-30g", "ultimi 30 giorni"}, {"+7g", "prossimi 7 giorni"}, {"+30g", "prossimi 30 giorni"}} {
			add("keyword", w[0], w[0], w[1])
		}
	}
	operators := func(date bool) {
		if p != "" || sp.op != "" {
			return
		}
		after, before := "maggiore di", "minore di"
		if date {
			after, before = "dopo", "prima di"
		}
		for _, o := range [][2]string{{">", after}, {"<", before}, {">=", after + " o uguale"}, {"<=", before + " o uguale"}} {
			out = append(out, Suggestion{Kind: "operator", Label: o[0], Insert: o[0], Detail: o[1]})
		}
	}
	members := func() error {
		if !(sp.op != "") {
			add("keyword", "me", "me", "tu")
		}
		rs, err := rowsOf(`select display_name, case member_type when 'requester' then 'richiedente' else 'persona' end from members
			where status = 'active' and (display_name ilike $1 or display_name ilike $2) order by display_name limit 10`, l1, l2)
		for _, r := range rs {
			out = append(out, Suggestion{Kind: "value", Label: r[0], Insert: quoteValue(r[0]), Detail: r[1]})
		}
		return err
	}
	var err error
	switch {
	case f.custom != nil:
		cf := f.custom
		switch cf.qkind() {
		case qkEnum:
			var rs [][2]string
			if rs, err = rowsOf(`select name, '' from options where option_set_id = $1 and not archived order by ordinal, lower(name)`, *cf.OptionSetID); err == nil {
				for _, r := range rs {
					add("value", r[0], quoteValue(r[0]), cf.Name)
				}
			}
		case qkMember:
			err = members()
		case qkGroup:
			var rs [][2]string
			if rs, err = rowsOf(`select name, '' from groups where name ilike $1 or name ilike $2 order by lower(name) limit 10`, l1, l2); err == nil {
				for _, r := range rs {
					add("value", r[0], quoteValue(r[0]), "gruppo")
				}
			}
		case qkElement:
			var rs [][2]string
			if rs, err = rowsOf(`select k, title from (
					select s.key || '-' || e.number as k, e.title from elements e
					join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
					join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
					where (cardinality($3::text[]) = 0 or t.key = any($3)) and e.deleted_at is null and not e.is_draft
					order by e.updated_at desc limit 500) x
				where $1 = '%' or title ilike $1 or title ilike $2 or k ilike $1 order by title limit 10`, l1, l2, append([]string{}, cf.Settings.Types...)); err == nil {
				for _, r := range rs {
					out = append(out, Suggestion{Kind: "value", Label: r[0], Insert: r[0], Detail: r[1]})
				}
			}
		case qkBool:
			add("value", "sì", "sì", "")
			add("value", "no", "no", "")
		case qkDate:
			dateWords()
			operators(true)
		case qkNumber:
			operators(false)
		}
	case f.mod != nil:
		mf := f.mod
		switch {
		case mf.Kind == "date":
			dateWords()
			operators(true)
		case mf.Suggest != "":
			var rs [][2]string
			if rs, err = rowsOf(mf.Suggest, l1, 10); err == nil {
				for _, r := range rs {
					out = append(out, Suggestion{Kind: "value", Label: r[0], Insert: quoteValue(r[0]), Detail: r[1]})
				}
			}
		case mf.RefType != "":
			// Tra gli elementi del tipo aggiornati di recente (lettura limitata anche con 1M elementi).
			var rs [][2]string
			if rs, err = rowsOf(`select k, title from (
					select s.key || '-' || e.number as k, e.title from elements e
					join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
					where e.type_id = (select id from element_types where key = $1) and e.deleted_at is null and not e.is_draft
					order by e.updated_at desc limit 500) x
				where $2 = '%' or title ilike $2 or title ilike $3 or k ilike $2 order by title limit 10`, mf.RefType, l1, l2); err == nil {
				for _, r := range rs {
					out = append(out, Suggestion{Kind: "value", Label: r[0], Insert: r[0], Detail: r[1]})
				}
			}
		}
	default:
		switch f.sys.kind {
		case qkType:
			var rs [][2]string
			if rs, err = rowsOf(`select key, name from element_types order by name`); err == nil {
				for _, r := range rs {
					add("value", r[0], r[0], r[1], r[1])
				}
			}
		case qkState:
			add("keyword", "aperta", "aperta", "non conclusa")
			add("keyword", "chiusa", "chiusa", "conclusa o annullata")
			types := sp.types
			var rs [][2]string
			if rs, err = rowsOf(`select name, category from (
					select distinct on (lower(o.name)) o.name, coalesce(o.category, '') as category, o.ordinal from options o
					join option_sets os on os.tenant_id = o.tenant_id and os.id = o.option_set_id
					where os.kind = 'state' and not o.archived
					  and (cardinality($1::text[]) = 0 or o.option_set_id in (select state_set_id from element_types where key = any($1) or lower(name) = any($1)))
					order by lower(o.name), o.ordinal) x
				order by ordinal, lower(name)`, types); err == nil {
				for _, r := range rs {
					add("value", r[0], quoteValue(r[0]), categoryNames[r[1]])
				}
			}
		case qkSpace:
			var rs [][2]string
			if rs, err = rowsOf(`select key, name from spaces where not archived order by key`); err == nil {
				for _, r := range rs {
					add("value", r[0], r[0], r[1], r[1])
				}
			}
		case qkMember, qkWatch:
			err = members()
		case qkDate:
			dateWords()
			operators(true)
		case qkNumber, qkKey:
			operators(false)
		}
	}
	if err != nil {
		return nil, err
	}
	if f.nullable() && sp.op == "" && !(f.sys != nil && f.sys.kind == qkWatch) {
		add("keyword", "vuoto", "vuoto", "senza valore")
		add("keyword", "presente", "presente", "con un valore qualsiasi")
	}
	return out, nil
}
