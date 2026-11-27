package engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"
	"unicode" // fusi orari inclusi nel binario: le date relative usano il fuso del tenant

	"github.com/jackc/pgx/v5"
)

// Dalla query analizzata al piano: campi e valori risolti sul tenant, condizioni SQL
// parametrizzate (mai testo dell'utente nell'SQL) e ordinamento in "gruppi" con cursore.
//
// Strategia di prestazioni (architecture.md §7):
//   - tipo, fase e spazio restano predicati sulle colonne, già risolti in id: Postgres
//     scorre l'indice ordinato (tipo|stato, updated_at, id) e si ferma alla pagina;
//   - gli altri filtri (campi personalizzati, persone, date, team, applicazioni...) si
//     risolvono prima in id con il loro indice, fino a QueryMaxIDs: se sono selettivi la
//     pagina si costruisce su quegli id, se sono densi restano predicati valutati
//     scorrendo l'indice ordinato (la pagina si riempie presto);
//   - l'ordinamento per fase o per un campo a elenco è una sequenza di gruppi (una fase
//     per volta, nell'ordine del processo), ognuno letto con l'indice per data;
//     l'ordinamento per una colonna con valori nulli ha i nulli in un gruppo finale.

// Le sottoquery exists dei predicati finiscono con "offset 0": così Postgres non le
// trasforma in una tabella hash di tutte le corrispondenze (centinaia di migliaia di righe
// per un filtro denso) ma le valuta riga per riga mentre scorre l'indice ordinato, e si
// ferma alla pagina (benchmark del 2026-11-24: da 34 a 0,4 ms su 300.000 elementi).

// QueryMaxIDs è il numero massimo di id in cui si risolve un filtro: oltre, il filtro è
// denso e resta un predicato. Regolabile per i test (0 = mai risolvere).
var QueryMaxIDs = 5000

type sqlb struct{ args []any }

func (b *sqlb) arg(v any) string {
	b.args = append(b.args, v)
	return "$" + strconv.Itoa(len(b.args))
}

// cond è una condizione su "e" (elements).
type cond struct {
	pred func(b *sqlb) string
	// res, se presente, è una query che restituisce gli id degli elementi che possono
	// corrispondere (un superinsieme va bene: il predicato vale comunque).
	res func(b *sqlb) string
}

// bucket è un gruppo dell'ordinamento: gli elementi che soddisfano extra, ordinati per expr.
type bucket struct {
	extra *cond
	join  func(b *sqlb) string
	expr  string
	cast  string
	asc   bool
}

type plan struct {
	conds []cond
	// Filtri passati anche a fts_match (indice GIN): solo quelli semplici e positivi.
	ftsTypes, ftsStates, ftsSpaces []string
	ftsOpen                        *bool
	text, prefix                   string // testo libero e prefissi (tsquery)
	key                            *keyRef
	buckets                        []bucket
	none                           bool // la query non può avere risultati
	// Accesso per spazio (space_access.go): spazi riservati nascosti all'attore e spazi
	// leggibili grazie a un ruolo sullo spazio.
	scope spaceScope
}

type keyRef struct {
	space  string
	number int64
}

type binder struct {
	ctx   context.Context
	tx    pgx.Tx
	actor Actor
	q     *Query
	cat   *catalog
	now   time.Time
	nowOK bool
	// svc e scope servono alle sottoquery (query_rel.go): si risolvono con la visibilità della persona.
	svc   *Service
	scope *spaceScope
	rels  []relName // tipi di relazione del tenant, letti alla prima clausola di relazione
}

func (b *binder) err(pos, end int, format string, args ...any) error {
	return qerr(b.q.Src, pos, end, format, args...)
}

var tzCache sync.Map // tenant -> *time.Location

// clock è l'ora corrente nel fuso del tenant (Europe/Rome se non indicato).
func (b *binder) clock() (time.Time, error) {
	if b.nowOK {
		return b.now, nil
	}
	var tenant, tz string
	if err := b.tx.QueryRow(b.ctx, `select app_tenant()::text, coalesce((select default_timezone from tenants where id = app_tenant()), 'Europe/Rome')`).Scan(&tenant, &tz); err != nil {
		return time.Time{}, err
	}
	loc, _ := tzCache.Load(tenant + "|" + tz)
	if loc == nil {
		l, err := time.LoadLocation(tz)
		if err != nil {
			l, _ = time.LoadLocation("Europe/Rome")
		}
		tzCache.Store(tenant+"|"+tz, l)
		loc = l
	}
	b.now, b.nowOK = queryNow().In(loc.(*time.Location)), true
	return b.now, nil
}

// queryNow è l'ora usata per le date relative (sostituibile nei test).
var queryNow = time.Now

// plan risolve la query sul tenant. Non esegue i filtri: lo fa Search.
func (s *Service) plan(ctx context.Context, tx pgx.Tx, actor Actor, q *Query) (*plan, error) {
	cat, err := s.catalog(ctx, tx)
	if err != nil {
		return nil, err
	}
	b := &binder{ctx: ctx, tx: tx, actor: actor, q: q, cat: cat, svc: s}
	return b.bind(q)
}

// bind costruisce il piano di q (la query o una sua sottoquery) con il binder b.
func (b *binder) bind(q *Query) (*plan, error) {
	q0 := b.q
	b.q = q
	defer func() { b.q = q0 }()
	cat := b.cat
	pl := &plan{}
	for i := range q.Clauses {
		c := &q.Clauses[i]
		f := cat.lookup(c.Name)
		var rel *relSpec
		if f == nil {
			var err error
			if rel, err = b.relation(c.Name); err != nil {
				return nil, err
			}
			if rel == nil {
				return nil, b.err(c.NamePos, c.NameEnd, "«%s» non è un campo né una relazione: usa tipo, fase, spazio, assegnatario, aggiornata, collegata, un campo personalizzato… (l'autocompletamento li propone)", c.Name)
			}
		}
		if len(c.Values) == 0 {
			return nil, b.err(c.Pos, c.End, "manca il valore di «%s»", c.Name)
		}
		var cd cond
		var err error
		if rel != nil {
			cd, err = b.bindRelation(rel, c)
		} else {
			if err := b.subAllowed(f, c); err != nil {
				return nil, err
			}
			cd, err = b.bindClause(pl, f, c)
		}
		if err != nil {
			return nil, err
		}
		if c.Negated {
			p := cd.pred
			cd = cond{pred: func(sb *sqlb) string { return "not coalesce((" + p(sb) + "), false)" }}
		}
		pl.conds = append(pl.conds, cd)
	}
	if err := b.bindText(pl); err != nil {
		return nil, err
	}
	if err := b.bindSort(pl); err != nil {
		return nil, err
	}
	sc, err := b.spaceScope()
	if err != nil {
		return nil, err
	}
	pl.scope = sc
	return pl, nil
}

// spaceScope: spazi nascosti e concessi all'attore, letti una volta per query.
func (b *binder) spaceScope() (spaceScope, error) {
	if b.scope == nil {
		sc, err := b.svc.scopeOf(b.ctx, b.tx, b.actor)
		if err != nil {
			return sc, err
		}
		b.scope = &sc
	}
	return *b.scope, nil
}

// ---------------------------------------------------------------------------
// Clausole

func (b *binder) bindClause(pl *plan, f *qfield, c *QClause) (cond, error) {
	switch {
	case f.custom != nil:
		return b.bindCustom(f, c)
	case f.mod != nil:
		return b.bindModule(f, c)
	}
	sf := f.sys
	switch sf.kind {
	case qkType:
		return b.bindType(pl, c)
	case qkState:
		return b.bindState(pl, c)
	case qkSpace:
		return b.bindSpace(pl, c)
	case qkMember:
		ids, flags, err := b.members(c, f)
		if err != nil {
			return cond{}, err
		}
		return columnCond(sf, refVals(ids, flags), true), nil
	case qkElement:
		ids, matched, flags, err := b.elementRefs(c, f, "", false)
		if err != nil {
			return cond{}, err
		}
		for i, v := range c.Values {
			if v.Op == "eq" && !matched[i] {
				if k := refKey(v); !keyRe.MatchString(k) && !uuidRe.MatchString(k) {
					return cond{}, b.err(v.Pos, v.End, "«%s» non è una chiave: scrivi ad esempio DEM-12", v.A)
				}
				return cond{}, b.err(v.Pos, v.End, "elemento «%s» inesistente", v.A)
			}
		}
		return columnCond(sf, refVals(ids, flags), true), nil
	case qkDate:
		vals, err := b.dates(c, f)
		if err != nil {
			return cond{}, err
		}
		return columnCond(sf, vals, true), nil
	case qkNumber:
		vals, err := b.numbers(c, f, true)
		if err != nil {
			return cond{}, err
		}
		return columnCond(sf, vals, true), nil
	case qkKey:
		return b.bindKey(c, f)
	case qkWatch:
		ids, flags, err := b.members(c, f)
		if err != nil {
			return cond{}, err
		}
		if flags.empty || flags.present {
			return cond{}, b.err(c.Pos, c.End, "per «%s» indica una persona, es. %s: me", c.Name, c.Name)
		}
		return watchCond(ids), nil
	case qkText:
		return cond{}, b.err(c.Pos, c.End, "«%s» serve solo per ordinare: per cercare nel testo scrivi le parole senza campo", c.Name)
	}
	return cond{}, b.err(c.Pos, c.End, "campo «%s» non filtrabile", c.Name)
}

// bval è un valore già validato.
type bval struct {
	op     string // in | span | eq | gt | ge | lt | le | range | empty | present
	ids    []string
	sp     span
	a, bb  any // numeri o testi
	hasA   bool
	hasB   bool
	isOpen *bool // solo per la fase: aperta / chiusa
	day    bool  // span confrontato con una colonna date
}

func valSQL(b *sqlb, col string, v bval) string {
	switch v.op {
	case "in":
		if len(v.ids) == 1 {
			return col + " = " + b.arg(v.ids[0]) + "::uuid"
		}
		return col + " = any(" + b.arg(v.ids) + "::uuid[])"
	case "span":
		var parts []string
		bound := func(t time.Time) string {
			if v.day {
				return b.arg(t.Format(time.DateOnly)) + "::date"
			}
			return b.arg(t)
		}
		if v.sp.lo != nil {
			parts = append(parts, col+" >= "+bound(*v.sp.lo))
		}
		if v.sp.hi != nil {
			parts = append(parts, col+" < "+bound(*v.sp.hi))
		}
		if len(parts) == 0 {
			return col + " is not null"
		}
		return "(" + strings.Join(parts, " and ") + ")"
	case "eq":
		return col + " = " + b.arg(v.a)
	case "gt":
		return col + " > " + b.arg(v.a)
	case "ge":
		return col + " >= " + b.arg(v.a)
	case "lt":
		return col + " < " + b.arg(v.a)
	case "le":
		return col + " <= " + b.arg(v.a)
	case "range":
		var parts []string
		if v.hasA {
			parts = append(parts, col+" >= "+b.arg(v.a))
		}
		if v.hasB {
			parts = append(parts, col+" <= "+b.arg(v.bb))
		}
		return "(" + strings.Join(parts, " and ") + ")"
	case "empty":
		return col + " is null"
	case "present":
		return col + " is not null"
	}
	return "false"
}

func orSQL(b *sqlb, col string, vals []bval) string {
	parts := make([]string, 0, len(vals))
	for _, v := range vals {
		parts = append(parts, valSQL(b, col, v))
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return "(" + strings.Join(parts, " or ") + ")"
}

// columnCond: condizione su una colonna di elements; con un indice si risolve anche in id.
func columnCond(sf *sysField, vals []bval, resolvable bool) cond {
	c := cond{pred: func(b *sqlb) string { return orSQL(b, sf.col, vals) }}
	if resolvable && sf.indexed {
		c.res = func(b *sqlb) string {
			return "select e.id from elements e where " + orSQL(b, sf.col, vals) + " and e.deleted_at is null and not e.is_draft"
		}
	}
	return c
}

// refFlags: «vuoto», «presente» e almeno una sottoquery (che può non trovare niente).
type refFlags struct{ empty, present, sub bool }

func refVals(ids []string, f refFlags) []bval {
	var out []bval
	if len(ids) > 0 || f.sub {
		out = append(out, bval{op: "in", ids: ids})
	}
	if f.empty {
		out = append(out, bval{op: "empty"})
	}
	if f.present {
		out = append(out, bval{op: "present"})
	}
	return out
}

// noOps rifiuta operatori di confronto e intervalli per i campi a valori.
func (b *binder) noOps(c *QClause, v QValue, nullable bool) error {
	switch v.Op {
	case "eq":
		return nil
	case "empty", "present":
		if nullable {
			return nil
		}
		return b.err(v.Pos, v.End, "«%s» ha sempre un valore: «vuoto» e «presente» non servono", c.Name)
	}
	return b.err(v.Pos, v.End, "per «%s» indica dei valori, senza operatori né intervalli", c.Name)
}

func lowerAll(vs []QValue) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		if v.Op == "eq" {
			out = append(out, strings.ToLower(v.A))
		}
	}
	return out
}

// lookupNames esegue una query (id, nome in minuscolo) con $1 = nomi in minuscolo.
func (b *binder) lookupNames(sql string, args ...any) (map[string][]string, error) {
	rows, err := b.tx.Query(b.ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[name] = append(out[name], id)
	}
	return out, rows.Err()
}

func (b *binder) bindType(pl *plan, c *QClause) (cond, error) {
	for _, v := range c.Values {
		if err := b.noOps(c, v, false); err != nil {
			return cond{}, err
		}
	}
	m, err := b.lookupNames(`select id::text, k from element_types, unnest(array[key, lower(name)]) k where k = any($1)`, lowerAll(c.Values))
	if err != nil {
		return cond{}, err
	}
	var ids []string
	for _, v := range c.Values {
		found := m[strings.ToLower(v.A)]
		if len(found) == 0 {
			return cond{}, b.err(v.Pos, v.End, "tipo «%s» inesistente", v.A)
		}
		ids = append(ids, found...)
	}
	ids = uniq(ids)
	if !c.Negated && pl.ftsTypes == nil {
		pl.ftsTypes = ids
	}
	return cond{pred: func(sb *sqlb) string { return valSQL(sb, "e.type_id", bval{op: "in", ids: ids}) }}, nil
}

var (
	openWords   = map[string]bool{"aperta": true, "aperte": true, "aperto": true, "aperti": true, "open": true, "unresolved": true}
	closedWords = map[string]bool{"chiusa": true, "chiuse": true, "chiuso": true, "chiusi": true, "closed": true, "resolved": true}
	meWords     = map[string]bool{"me": true, "io": true, "mio": true, "mia": true, "mine": true, "myself": true}
)

func (b *binder) bindState(pl *plan, c *QClause) (cond, error) {
	var names []string
	var vals []bval
	var openFlag *bool
	onlyNames, onlyOpen := true, true
	for _, v := range c.Values {
		if err := b.noOps(c, v, true); err != nil {
			return cond{}, err
		}
		lv := strings.ToLower(v.A)
		switch {
		case v.Op == "empty" || v.Op == "present":
			vals = append(vals, bval{op: v.Op})
			onlyNames, onlyOpen = false, false
		case !v.Quoted && (openWords[lv] || closedWords[lv]):
			o := openWords[lv]
			vals = append(vals, bval{op: "open", isOpen: &o})
			openFlag = &o
			onlyNames = false
		default:
			names = append(names, lv)
			onlyOpen = false
		}
	}
	if len(names) > 0 {
		m, err := b.lookupNames(`select o.id::text, lower(o.name) from options o
			join option_sets os on os.tenant_id = o.tenant_id and os.id = o.option_set_id
			where os.kind = 'state' and lower(o.name) = any($1)`, names)
		if err != nil {
			return cond{}, err
		}
		var ids []string
		for _, v := range c.Values {
			lv := strings.ToLower(v.A)
			if v.Op != "eq" || (!v.Quoted && (openWords[lv] || closedWords[lv])) {
				continue
			}
			if len(m[lv]) == 0 {
				return cond{}, b.err(v.Pos, v.End, "fase «%s» inesistente", v.A)
			}
			ids = append(ids, m[lv]...)
		}
		ids = uniq(ids)
		vals = append(vals, bval{op: "in", ids: ids})
		if !c.Negated && onlyNames && pl.ftsStates == nil {
			pl.ftsStates = ids
		}
	}
	if !c.Negated && onlyOpen && len(c.Values) == 1 && openFlag != nil {
		pl.ftsOpen = openFlag
	}
	return cond{pred: func(sb *sqlb) string {
		parts := make([]string, 0, len(vals))
		for _, v := range vals {
			switch {
			case v.op == "open" && *v.isOpen:
				parts = append(parts, "e.resolved_at is null")
			case v.op == "open":
				parts = append(parts, "e.resolved_at is not null")
			default:
				parts = append(parts, valSQL(sb, "e.state_id", v))
			}
		}
		if len(parts) == 1 {
			return parts[0]
		}
		return "(" + strings.Join(parts, " or ") + ")"
	}}, nil
}

func (b *binder) bindSpace(pl *plan, c *QClause) (cond, error) {
	for _, v := range c.Values {
		if err := b.noOps(c, v, false); err != nil {
			return cond{}, err
		}
	}
	m, err := b.lookupNames(`select id::text, k from spaces, unnest(array[lower(key), lower(name)]) k where k = any($1)`, lowerAll(c.Values))
	if err != nil {
		return cond{}, err
	}
	var ids []string
	for _, v := range c.Values {
		found := m[strings.ToLower(v.A)]
		if len(found) == 0 {
			return cond{}, b.err(v.Pos, v.End, "spazio «%s» inesistente", v.A)
		}
		ids = append(ids, found...)
	}
	ids = uniq(ids)
	if !c.Negated && pl.ftsSpaces == nil {
		pl.ftsSpaces = ids
	}
	return cond{pred: func(sb *sqlb) string { return valSQL(sb, "e.space_id", bval{op: "in", ids: ids}) }}, nil
}

// members risolve persone: "me" oppure il nome visualizzato (senza distinguere maiuscole).
func (b *binder) members(c *QClause, f *qfield) ([]string, refFlags, error) {
	var ids []string
	var flags refFlags
	var names []string
	for _, v := range c.Values {
		if err := b.noOps(c, v, f.nullable()); err != nil {
			return nil, flags, err
		}
		lv := strings.ToLower(v.A)
		switch {
		case v.Op == "empty":
			flags.empty = true
		case v.Op == "present":
			flags.present = true
		case !v.Quoted && meWords[lv]:
			if b.actor.MemberID == "" {
				return nil, flags, b.err(v.Pos, v.End, "«me» richiede un utente collegato a un membro")
			}
			ids = append(ids, b.actor.MemberID)
		default:
			names = append(names, lv)
		}
	}
	if len(names) > 0 {
		m, err := b.lookupNames(`select id::text, lower(display_name) from members where lower(display_name) = any($1)`, names)
		if err != nil {
			return nil, flags, err
		}
		for _, v := range c.Values {
			lv := strings.ToLower(v.A)
			if v.Op != "eq" || (!v.Quoted && meWords[lv]) {
				continue
			}
			if len(m[lv]) == 0 {
				return nil, flags, b.err(v.Pos, v.End, "persona «%s» non trovata", v.A)
			}
			ids = append(ids, m[lv]...)
		}
	}
	return uniq(ids), flags, nil
}

// refKey è la forma di un valore con cui si cerca un elemento: chiave in maiuscolo, id o
// titolo in minuscolo.
func refKey(v QValue) string {
	if k := strings.ToUpper(strings.TrimSpace(v.A)); !v.Quoted && keyRe.MatchString(k) {
		return k
	}
	return strings.ToLower(strings.TrimSpace(v.A))
}

// elementRefs risolve elementi per chiave (DEM-12) o id, limitati a refType se indicato;
// con titles anche per titolo esatto (solo per tipi con pochi elementi, es. applicazioni).
// matched[i] dice se il valore i di c è stato trovato.
func (b *binder) elementRefs(c *QClause, f *qfield, refType string, titles bool) ([]string, []bool, refFlags, error) {
	var flags refFlags
	var spaces, uuids, names []string
	var numbers []int64
	var subIDs []string
	matched := make([]bool, len(c.Values))
	for i, v := range c.Values {
		if v.Op == "sub" {
			got, err := b.subIDs(v)
			if err != nil {
				return nil, nil, flags, err
			}
			subIDs, matched[i], flags.sub = append(subIDs, got...), true, true
			continue
		}
		if err := b.noOps(c, v, f.nullable()); err != nil {
			return nil, nil, flags, err
		}
		switch {
		case v.Op == "empty":
			flags.empty = true
		case v.Op == "present":
			flags.present = true
		default:
			k := refKey(v)
			if m := keyRe.FindStringSubmatch(k); m != nil {
				n, _ := strconv.ParseInt(m[2], 10, 64)
				spaces, numbers = append(spaces, m[1]), append(numbers, n)
			} else if uuidRe.MatchString(k) {
				uuids = append(uuids, k)
			} else if titles {
				names = append(names, k)
			}
		}
	}
	found := map[string][]string{}
	if len(spaces)+len(uuids)+len(names) > 0 {
		// Tre letture con il loro indice: chiave (spazio, numero), id, titolo nel tipo.
		rows, err := b.tx.Query(b.ctx, `
			select e.id::text, s.key || '-' || e.number
			from unnest($1::text[], $2::bigint[]) u(sk, n)
			join spaces s on s.key = u.sk
			join elements e on e.tenant_id = s.tenant_id and e.space_id = s.id and e.number = u.n
			join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
			where e.deleted_at is null and not e.is_draft and ($4 = '' or t.key = $4)
			union all
			select e.id::text, e.id::text from elements e
			join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
			where e.id = any($3::uuid[]) and e.deleted_at is null and not e.is_draft and ($4 = '' or t.key = $4)
			union all
			select e.id::text, lower(e.title) from elements e
			where cardinality($5::text[]) > 0 and e.type_id = (select id from element_types where key = $4)
			  and e.deleted_at is null and not e.is_draft and lower(e.title) = any($5)`,
			spaces, numbers, uuids, refType, names)
		if err != nil {
			return nil, nil, flags, err
		}
		for rows.Next() {
			var id, k string
			if err := rows.Scan(&id, &k); err != nil {
				rows.Close()
				return nil, nil, flags, err
			}
			found[k] = append(found[k], id)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, nil, flags, err
		}
	}
	ids := subIDs
	for i, v := range c.Values {
		if v.Op != "eq" {
			continue
		}
		if got := found[refKey(v)]; len(got) > 0 {
			ids = append(ids, got...)
			matched[i] = true
		}
	}
	return uniq(ids), matched, flags, nil
}

func (b *binder) dates(c *QClause, f *qfield) ([]bval, error) {
	now, err := b.clock()
	if err != nil {
		return nil, err
	}
	var out []bval
	for _, v := range c.Values {
		if v.Op == "empty" || v.Op == "present" {
			if !f.nullable() {
				return nil, b.err(v.Pos, v.End, "«%s» ha sempre un valore: «vuoto» e «presente» non servono", c.Name)
			}
			out = append(out, bval{op: v.Op})
			continue
		}
		sp, ok := dateSpan(v, now)
		if !ok {
			return nil, b.err(v.Pos, v.End, "data «%s» non valida: usa AAAA-MM-GG, oggi, ieri, settimana, mese, -7g, +30g", v.A+rangeTail(v))
		}
		out = append(out, bval{op: "span", sp: sp})
	}
	return out, nil
}

func rangeTail(v QValue) string {
	if v.Op == "range" {
		return ".." + v.B
	}
	return ""
}

func (b *binder) numbers(c *QClause, f *qfield, integer bool) ([]bval, error) {
	parse := func(s string, v QValue) (any, error) {
		s = strings.TrimSpace(s)
		if integer {
			n, err := strconv.ParseInt(s, 10, 64)
			if err != nil {
				return nil, b.err(v.Pos, v.End, "«%s» non è un numero intero", s)
			}
			return n, nil
		}
		x, err := strconv.ParseFloat(strings.ReplaceAll(s, "_", ""), 64)
		if err != nil {
			return nil, b.err(v.Pos, v.End, "«%s» non è un numero (usa il punto per i decimali)", s)
		}
		return x, nil
	}
	var out []bval
	for _, v := range c.Values {
		switch v.Op {
		case "empty", "present":
			if !f.nullable() {
				return nil, b.err(v.Pos, v.End, "«%s» ha sempre un valore", c.Name)
			}
			out = append(out, bval{op: v.Op})
		case "range":
			bv := bval{op: "range"}
			if v.A != "" {
				x, err := parse(v.A, v)
				if err != nil {
					return nil, err
				}
				bv.a, bv.hasA = x, true
			}
			if v.B != "" {
				x, err := parse(v.B, v)
				if err != nil {
					return nil, err
				}
				bv.bb, bv.hasB = x, true
			}
			out = append(out, bv)
		default:
			x, err := parse(v.A, v)
			if err != nil {
				return nil, err
			}
			out = append(out, bval{op: v.Op, a: x})
		}
	}
	return out, nil
}

func (b *binder) bindKey(c *QClause, f *qfield) (cond, error) {
	type part struct {
		space  string
		op     string
		n1, n2 int64
		has1   bool
		has2   bool
	}
	var parts []part
	spaceKeys := map[string]bool{}
	parseKey := func(s string, v QValue) (string, int64, error) {
		m := keyRe.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(s)))
		if m == nil {
			return "", 0, b.err(v.Pos, v.End, "«%s» non è una chiave: scrivi ad esempio DEM-12", s)
		}
		n, _ := strconv.ParseInt(m[2], 10, 64)
		spaceKeys[m[1]] = true
		return m[1], n, nil
	}
	for _, v := range c.Values {
		switch v.Op {
		case "empty", "present":
			return cond{}, b.err(v.Pos, v.End, "ogni elemento ha una chiave")
		case "range":
			p := part{op: "range"}
			if v.A != "" {
				sp, n, err := parseKey(v.A, v)
				if err != nil {
					return cond{}, err
				}
				p.space, p.n1, p.has1 = sp, n, true
			}
			if v.B != "" {
				sp, n, err := parseKey(v.B, v)
				if err != nil {
					return cond{}, err
				}
				if p.has1 && sp != p.space {
					return cond{}, b.err(v.Pos, v.End, "un intervallo di chiavi resta nello stesso spazio")
				}
				p.space, p.n2, p.has2 = sp, n, true
			}
			parts = append(parts, p)
		default:
			sp, n, err := parseKey(v.A, v)
			if err != nil {
				return cond{}, err
			}
			parts = append(parts, part{space: sp, op: v.Op, n1: n, has1: true})
		}
	}
	keys := make([]string, 0, len(spaceKeys))
	for k := range spaceKeys {
		keys = append(keys, strings.ToLower(k))
	}
	m, err := b.lookupNames(`select id::text, lower(key) from spaces where lower(key) = any($1)`, keys)
	if err != nil {
		return cond{}, err
	}
	for k := range spaceKeys {
		if len(m[strings.ToLower(k)]) == 0 {
			return cond{}, b.err(c.Pos, c.End, "spazio «%s» inesistente", k)
		}
	}
	pred := func(sb *sqlb) string {
		var ors []string
		for _, p := range parts {
			sid := m[strings.ToLower(p.space)][0]
			nv := bval{op: p.op, a: p.n1, bb: p.n2, hasA: p.has1, hasB: p.has2}
			ors = append(ors, "(e.space_id = "+sb.arg(sid)+"::uuid and "+valSQL(sb, "e.number", nv)+")")
		}
		if len(ors) == 1 {
			return ors[0]
		}
		return "(" + strings.Join(ors, " or ") + ")"
	}
	return cond{pred: pred, res: func(sb *sqlb) string {
		return "select e.id from elements e where " + pred(sb) + " and e.deleted_at is null and not e.is_draft"
	}}, nil
}

// watchCond: elementi seguiti (osservatori espliciti, segnalatore e stakeholder, tolti
// quelli che la persona ha scelto di non seguire), come le notifiche.
func watchCond(ids []string) cond {
	return cond{
		pred: func(b *sqlb) string {
			var ors []string
			for _, id := range ids {
				m := b.arg(id) + "::uuid"
				ors = append(ors, "((exists (select 1 from watchers w where w.tenant_id = e.tenant_id and w.element_id = e.id and w.member_id = "+m+" and not w.muted offset 0)"+
					" or e.reporter_id = "+m+
					" or exists (select 1 from stakeholders sh where sh.tenant_id = e.tenant_id and sh.element_id = e.id and (sh.member_id = "+m+
					" or sh.group_id in (select member_groups("+m+"))) offset 0))"+
					" and not exists (select 1 from watchers w where w.tenant_id = e.tenant_id and w.element_id = e.id and w.member_id = "+m+" and w.muted offset 0))")
			}
			return "(" + strings.Join(ors, " or ") + ")"
		},
		res: func(b *sqlb) string {
			m := b.arg(ids) + "::uuid[]"
			return "select element_id from watchers where member_id = any(" + m + ") and not muted" +
				" union all select id from elements where reporter_id = any(" + m + ") and deleted_at is null and not is_draft" +
				" union all select element_id from stakeholders where member_id = any(" + m + ")" +
				" union all select sh.element_id from stakeholders sh where sh.group_id in (select member_groups(x) from unnest(" + m + ") x)"
		},
	}
}

// bindCustom: campo personalizzato, filtrato sull'indice tipizzato element_values.
func (b *binder) bindCustom(f *qfield, c *QClause) (cond, error) {
	cf := f.custom
	col := "v." + cf.col()
	var vals []bval
	var empty, present bool
	switch cf.qkind() {
	case qkText:
		for _, v := range c.Values {
			switch v.Op {
			case "empty":
				empty = true
			case "present":
				present = true
			case "range":
				bv := bval{op: "range", a: v.A, bb: v.B, hasA: v.A != "", hasB: v.B != ""}
				vals = append(vals, bv)
			default:
				vals = append(vals, bval{op: v.Op, a: v.A})
			}
		}
	case qkNumber:
		nv, err := b.numbers(c, f, false)
		if err != nil {
			return cond{}, err
		}
		for _, v := range nv {
			switch v.op {
			case "empty":
				empty = true
			case "present":
				present = true
			default:
				vals = append(vals, v)
			}
		}
	case qkBool:
		for _, v := range c.Values {
			switch v.Op {
			case "empty":
				empty = true
			case "present":
				present = true
			case "eq":
				switch strings.ToLower(v.A) {
				case "sì", "si", "vero", "yes", "true", "1":
					vals = append(vals, bval{op: "eq", a: 1.0})
				case "no", "falso", "false", "0":
					vals = append(vals, bval{op: "eq", a: 0.0})
				default:
					return cond{}, b.err(v.Pos, v.End, "per «%s» scrivi sì oppure no", c.Name)
				}
			default:
				return cond{}, b.err(v.Pos, v.End, "per «%s» scrivi sì oppure no", c.Name)
			}
		}
	case qkDate:
		dv, err := b.dates(c, f)
		if err != nil {
			return cond{}, err
		}
		for _, v := range dv {
			switch v.op {
			case "empty":
				empty = true
			case "present":
				present = true
			default:
				vals = append(vals, v)
			}
		}
	case qkEnum:
		var names []string
		for _, v := range c.Values {
			if err := b.noOps(c, v, true); err != nil {
				return cond{}, err
			}
			switch v.Op {
			case "empty":
				empty = true
			case "present":
				present = true
			default:
				names = append(names, strings.ToLower(v.A))
			}
		}
		if len(names) > 0 {
			m, err := b.lookupNames(`select id::text, lower(name) from options where option_set_id = $2::uuid and lower(name) = any($1)`, names, *cf.OptionSetID)
			if err != nil {
				return cond{}, err
			}
			var ids []string
			for _, v := range c.Values {
				if v.Op != "eq" {
					continue
				}
				if len(m[strings.ToLower(v.A)]) == 0 {
					return cond{}, b.err(v.Pos, v.End, "«%s» non è un'opzione di %s", v.A, cf.Name)
				}
				ids = append(ids, m[strings.ToLower(v.A)]...)
			}
			vals = append(vals, bval{op: "in", ids: uniq(ids)})
		}
	case qkMember:
		ids, flags, err := b.members(c, f)
		if err != nil {
			return cond{}, err
		}
		empty, present = flags.empty, flags.present
		if len(ids) > 0 {
			vals = append(vals, bval{op: "in", ids: ids})
		}
	case qkGroup:
		var names []string
		for _, v := range c.Values {
			if err := b.noOps(c, v, true); err != nil {
				return cond{}, err
			}
			switch v.Op {
			case "empty":
				empty = true
			case "present":
				present = true
			default:
				names = append(names, strings.ToLower(v.A))
			}
		}
		if len(names) > 0 {
			m, err := b.lookupNames(`select id::text, lower(name) from groups where lower(name) = any($1)`, names)
			if err != nil {
				return cond{}, err
			}
			var ids []string
			for _, v := range c.Values {
				if v.Op != "eq" {
					continue
				}
				if len(m[strings.ToLower(v.A)]) == 0 {
					return cond{}, b.err(v.Pos, v.End, "gruppo «%s» inesistente", v.A)
				}
				ids = append(ids, m[strings.ToLower(v.A)]...)
			}
			vals = append(vals, bval{op: "in", ids: uniq(ids)})
		}
	case qkElement:
		refType := ""
		if len(cf.Settings.Types) == 1 {
			refType = cf.Settings.Types[0]
		}
		ids, matched, flags, err := b.elementRefs(c, f, refType, false)
		if err != nil {
			return cond{}, err
		}
		for i, v := range c.Values {
			if v.Op == "eq" && !matched[i] {
				if k := refKey(v); !keyRe.MatchString(k) && !uuidRe.MatchString(k) {
					return cond{}, b.err(v.Pos, v.End, "«%s» non è una chiave: scrivi ad esempio DEM-12", v.A)
				}
				return cond{}, b.err(v.Pos, v.End, "elemento «%s» inesistente", v.A)
			}
		}
		empty, present = flags.empty, flags.present
		if len(ids) > 0 || flags.sub {
			vals = append(vals, bval{op: "in", ids: ids})
		}
	}
	fieldID := cf.ID
	exists := func(sb *sqlb, inner string) string {
		s := "exists (select 1 from element_values v where v.tenant_id = e.tenant_id and v.element_id = e.id and v.field_id = " + sb.arg(fieldID) + "::uuid"
		if inner != "" {
			s += " and " + inner
		}
		return s + " offset 0)"
	}
	cd := cond{pred: func(sb *sqlb) string {
		var ors []string
		if len(vals) > 0 {
			ors = append(ors, exists(sb, orSQL(sb, col, vals)))
		}
		if present {
			ors = append(ors, exists(sb, ""))
		}
		if empty {
			ors = append(ors, "not "+exists(sb, ""))
		}
		if len(ors) == 1 {
			return ors[0]
		}
		return "(" + strings.Join(ors, " or ") + ")"
	}}
	if !empty {
		cd.res = func(sb *sqlb) string {
			if lo, loIncl, hi, hiIncl, ok := numRange(col, vals, present); ok {
				// Intervallo su un numero: field_num_match (migrazione 0032) usa l'indice anche con la RLS.
				return "select field_num_match(" + sb.arg(fieldID) + "::uuid, " + sb.arg(lo) + "::numeric, " + sb.arg(loIncl) + "::boolean, " +
					sb.arg(hi) + "::numeric, " + sb.arg(hiIncl) + "::boolean)"
			}
			s := "select v.element_id from element_values v where v.field_id = " + sb.arg(fieldID) + "::uuid"
			if !present {
				s += " and " + orSQL(sb, col, vals)
			}
			return s
		}
	}
	return cd, nil
}

// bindModule: campo di un modulo (team, applicazione, asset, richiesta, scadenza).
func (b *binder) bindModule(f *qfield, c *QClause) (cond, error) {
	mf := f.mod
	var vals []bval
	var empty, present bool
	switch mf.Kind {
	case "date":
		dv, err := b.dates(c, f)
		if err != nil {
			return cond{}, err
		}
		for _, v := range dv {
			switch v.op {
			case "empty":
				empty = true
			case "present":
				present = true
			default:
				v.day = mf.DateOnly
				vals = append(vals, v)
			}
		}
	default:
		var ids []string
		var flags refFlags
		matched := make([]bool, len(c.Values))
		if mf.RefType != "" {
			var err error
			ids, matched, flags, err = b.elementRefs(c, f, mf.RefType, mf.RefTitles)
			if err != nil {
				return cond{}, err
			}
		} else {
			for _, v := range c.Values {
				if err := b.noOps(c, v, true); err != nil {
					return cond{}, err
				}
				flags.empty = flags.empty || v.Op == "empty"
				flags.present = flags.present || v.Op == "present"
			}
		}
		empty, present = flags.empty, flags.present
		if flags.sub && len(ids) == 0 {
			// La sottoquery non trova niente: la condizione non vale per nessun elemento.
			vals = append(vals, bval{op: "in", ids: []string{}})
		}
		var extra map[string][]string
		if mf.Lookup != "" {
			var err error
			if extra, err = b.lookupNames(mf.Lookup, lowerAll(c.Values)); err != nil {
				return cond{}, err
			}
		}
		// Ogni valore deve corrispondere a qualcosa: per chiave o titolo, oppure con Lookup.
		for i, v := range c.Values {
			if v.Op != "eq" {
				continue
			}
			got := extra[strings.ToLower(v.A)]
			ids = append(ids, got...)
			if len(got) == 0 && !matched[i] {
				return cond{}, b.err(v.Pos, v.End, "«%s» non trovato per «%s»", v.A, c.Name)
			}
		}
		if ids = uniq(ids); len(ids) > 0 {
			vals = append(vals, bval{op: "in", ids: ids})
		}
	}
	src := "(" + mf.Source + ") src"
	exists := func(sb *sqlb, inner string) string {
		return "exists (select 1 from " + src + " where src.element_id = e.id and " + inner + " offset 0)"
	}
	cd := cond{pred: func(sb *sqlb) string {
		var ors []string
		if len(vals) > 0 {
			ors = append(ors, exists(sb, orSQL(sb, "src.v", vals)))
		}
		if present {
			ors = append(ors, exists(sb, "src.v is not null"))
		}
		if empty {
			ors = append(ors, "not "+exists(sb, "src.v is not null"))
		}
		if len(ors) == 0 {
			return "false"
		}
		if len(ors) == 1 {
			return ors[0]
		}
		return "(" + strings.Join(ors, " or ") + ")"
	}}
	if !empty {
		cd.res = func(sb *sqlb) string {
			if present && len(vals) == 0 {
				return "select src.element_id from " + src + " where src.v is not null"
			}
			return "select src.element_id from " + src + " where " + orSQL(sb, "src.v", vals)
		}
	}
	return cd, nil
}

// ---------------------------------------------------------------------------
// Testo libero

func (b *binder) bindText(pl *plan) error {
	var plain []string
	var prefix []string
	for _, t := range b.q.Text {
		if t.Quoted {
			plain = append(plain, `"`+t.Text+`"`)
			continue
		}
		if strings.HasSuffix(t.Text, "*") && len(t.Text) > 1 {
			w := strings.ToLower(strings.TrimRight(t.Text, "*"))
			var sb strings.Builder
			for _, r := range w {
				if isLetterOrDigit(r) {
					sb.WriteRune(r)
				}
			}
			if sb.Len() > 0 {
				prefix = append(prefix, sb.String()+":*")
			}
			continue
		}
		plain = append(plain, t.Text)
	}
	pl.text = strings.Join(plain, " ")
	pl.prefix = strings.Join(prefix, " & ")
	if pl.prefix == "" && len(b.q.Text) == 1 && !b.q.Text[0].Quoted {
		if m := keyRe.FindStringSubmatch(strings.ToUpper(pl.text)); m != nil {
			n, _ := strconv.ParseInt(m[2], 10, 64)
			pl.key = &keyRef{space: m[1], number: n}
		}
	}
	return nil
}

func isLetterOrDigit(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }

// ---------------------------------------------------------------------------
// Ordinamento

func (b *binder) bindSort(pl *plan) error {
	upd := bucket{expr: "e.updated_at", cast: "::timestamptz"}
	s := b.q.Sort
	if s == nil {
		pl.buckets = []bucket{upd}
		return nil
	}
	f := b.cat.lookup(s.Name)
	// Compatibilità con la prima versione: "ordina per: aggiornamento|creazione|numero".
	if f == nil || !f.sortable() {
		var hint []string
		for _, x := range b.cat.fields {
			if x.sortable() {
				hint = append(hint, x.name)
			}
		}
		if f == nil {
			return b.err(s.Pos, s.End, "non si può ordinare per «%s»: usa %s", s.Name, strings.Join(hint, ", "))
		}
		return b.err(s.Pos, s.End, "«%s» non è ordinabile: usa %s", s.Name, strings.Join(hint, ", "))
	}
	asc := s.Asc
	if !s.Explicit {
		asc = (f.sys != nil && f.sys.asc) || (f.custom != nil && (f.custom.qkind() == qkText || f.custom.qkind() == qkEnum))
	}
	if f.sys != nil {
		sf := f.sys
		switch sf.key {
		case "updated", "created":
			pl.buckets = []bucket{{expr: sf.col, cast: "::timestamptz", asc: asc}}
		case "number", "key":
			pl.buckets = []bucket{{expr: "e.number", cast: "::bigint", asc: asc}}
		case "title":
			pl.buckets = []bucket{{expr: "e.title", cast: "::text", asc: asc}}
		case "resolved", "start", "end":
			col := sf.col
			pl.buckets = []bucket{
				{expr: col, cast: "::timestamptz", asc: asc, extra: &cond{pred: func(*sqlb) string { return col + " is not null" }}},
				{expr: "e.updated_at", cast: "::timestamptz", extra: &cond{pred: func(*sqlb) string { return col + " is null" }}},
			}
		case "state":
			return b.stateBuckets(pl, asc)
		}
		return nil
	}
	cf := f.custom
	fieldID := cf.ID
	if cf.qkind() == qkEnum {
		rows, err := b.tx.Query(b.ctx, `select id::text from options where option_set_id = $1 order by ordinal, lower(name)`, *cf.OptionSetID)
		if err != nil {
			return err
		}
		opts, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return err
		}
		if !asc {
			reverse(opts)
		}
		for _, o := range opts {
			o := o
			pl.buckets = append(pl.buckets, bucket{expr: "e.updated_at", cast: "::timestamptz", extra: &cond{
				pred: func(sb *sqlb) string {
					return "exists (select 1 from element_values v where v.tenant_id = e.tenant_id and v.element_id = e.id and v.field_id = " +
						sb.arg(fieldID) + "::uuid and v.ord = 0 and v.v_ref = " + sb.arg(o) + "::uuid offset 0)"
				},
				res: func(sb *sqlb) string {
					return "select v.element_id from element_values v where v.field_id = " + sb.arg(fieldID) + "::uuid and v.ord = 0 and v.v_ref = " + sb.arg(o) + "::uuid"
				},
			}})
		}
	} else {
		col := "sv." + cf.col()
		cast := map[string]string{"v_num": "::numeric", "v_ts": "::timestamptz", "v_text": "::text"}[cf.col()]
		pl.buckets = append(pl.buckets, bucket{expr: col, cast: cast, asc: asc, join: func(sb *sqlb) string {
			return " join element_values sv on sv.tenant_id = e.tenant_id and sv.element_id = e.id and sv.field_id = " + sb.arg(fieldID) +
				"::uuid and sv.ord = 0 and " + col + " is not null"
		}})
	}
	// Senza valore, in fondo.
	pl.buckets = append(pl.buckets, bucket{expr: "e.updated_at", cast: "::timestamptz", extra: &cond{pred: func(sb *sqlb) string {
		return "not exists (select 1 from element_values v where v.tenant_id = e.tenant_id and v.element_id = e.id and v.field_id = " + sb.arg(fieldID) + "::uuid and v.ord = 0 offset 0)"
	}}})
	return nil
}

// stateBuckets: un gruppo per fase, nell'ordine del processo (solo le fasi possibili
// con i filtri di tipo, fase e aperta/chiusa), poi gli elementi senza fase.
func (b *binder) stateBuckets(pl *plan, asc bool) error {
	cats := []string{"todo", "in_progress", "done", "cancelled"}
	if pl.ftsOpen != nil && *pl.ftsOpen {
		cats = []string{"todo", "in_progress"}
	} else if pl.ftsOpen != nil {
		cats = []string{"done", "cancelled"}
	}
	rows, err := b.tx.Query(b.ctx, `select o.id::text from options o
		join option_sets os on os.tenant_id = o.tenant_id and os.id = o.option_set_id
		where os.kind = 'state'
		  and ($1::uuid[] is null or o.option_set_id in (select state_set_id from element_types where id = any($1::uuid[])))
		  and ($2::uuid[] is null or o.id = any($2::uuid[]))
		  and coalesce(o.category, 'todo') = any($3)
		order by o.ordinal, lower(o.name)`, nilIfEmpty(pl.ftsTypes), nilIfEmpty(pl.ftsStates), cats)
	if err != nil {
		return err
	}
	states, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	if !asc {
		reverse(states)
	}
	for _, id := range states {
		id := id
		pl.buckets = append(pl.buckets, bucket{expr: "e.updated_at", cast: "::timestamptz", extra: &cond{pred: func(sb *sqlb) string {
			return "e.state_id = " + sb.arg(id) + "::uuid"
		}}})
	}
	if pl.ftsStates == nil {
		pl.buckets = append(pl.buckets, bucket{expr: "e.updated_at", cast: "::timestamptz", extra: &cond{pred: func(*sqlb) string { return "e.state_id is null" }}})
	}
	return nil
}

func reverse[T any](s []T) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

func uniq(s []string) []string {
	if len(s) < 2 {
		return s
	}
	seen := make(map[string]bool, len(s))
	out := s[:0:0]
	for _, x := range s {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Esecuzione

// resolveIDs esegue la query degli id di una condizione: nil, true se è densa.
func resolveIDs(ctx context.Context, tx pgx.Tx, c *cond) ([]string, bool, error) {
	if c.res == nil || QueryMaxIDs <= 0 {
		return nil, true, nil
	}
	sb := &sqlb{}
	sql := "select r.id::text from (" + c.res(sb) + ") r(id) limit " + sb.arg(QueryMaxIDs+1)
	rows, err := tx.Query(ctx, sql, sb.args...)
	if err != nil {
		return nil, false, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, false, err
	}
	if len(ids) > QueryMaxIDs {
		return nil, true, nil
	}
	return uniq(ids), false, nil
}

// intersect: nil vale "nessun vincolo".
func intersect(a, b []string) []string {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	in := make(map[string]bool, len(b))
	for _, x := range b {
		in[x] = true
	}
	out := []string{}
	for _, x := range a {
		if in[x] {
			out = append(out, x)
		}
	}
	return out
}

// errNoMatch interrompe la ricerca quando i filtri non hanno corrispondenze.
var errNoMatch = errors.New("nessuna corrispondenza")

func (pl *plan) resolve(ctx context.Context, tx pgx.Tx) ([]string, error) {
	var ids []string
	for i := range pl.conds {
		r, dense, err := resolveIDs(ctx, tx, &pl.conds[i])
		if err != nil {
			return nil, err
		}
		if dense {
			continue
		}
		if ids = intersect(ids, r); len(ids) == 0 {
			return nil, errNoMatch
		}
	}
	return ids, nil
}

// buildBucket compone la query di un gruppo dell'ordinamento.
func buildBucket(pl *plan, bk *bucket, actor Actor, ids []string, mode textMode, cur *cursor, n int) (string, []any) {
	sb := &sqlb{}
	where := planWhere(sb, pl, actor, ids, mode)
	join := ""
	if bk.join != nil {
		join = bk.join(sb)
	}
	if bk.extra != nil {
		where = append(where, bk.extra.pred(sb))
	}
	dir, cmp := "desc", "<"
	if bk.asc {
		dir, cmp = "asc", ">"
	}
	if cur != nil {
		where = append(where, fmt.Sprintf("(%s, e.id) %s (%s%s, %s::uuid)", bk.expr, cmp, sb.arg(cur.V), bk.cast, sb.arg(cur.ID)))
	}
	sql := "select" + elementColumns + ", (" + bk.expr + ")::text" + elementFrom + join +
		" where " + strings.Join(where, " and ") +
		fmt.Sprintf(" order by %s %s, e.id %s limit %s", bk.expr, dir, dir, sb.arg(n))
	return sql, sb.args
}

// visibleWhere sono le condizioni di visibilità di un elemento con alias a (le stesse di
// CanRead, in SQL): non cancellato né bozza, spazi riservati esclusi e, per chi non ha
// element.read (richiedente del portale), solo ciò che ha aperto, ciò in cui ha un ruolo e gli
// spazi in cui ha un ruolo che concede la lettura.
func visibleWhere(sb *sqlb, a string, actor Actor, sc spaceScope) []string {
	where := []string{a + ".deleted_at is null", "not " + a + ".is_draft"}
	if !actor.Has("element.read") {
		me := sb.arg(actor.MemberID)
		own := a + ".reporter_id = " + me + "::uuid or " + stakeholderOf(a+".id", me+"::uuid")
		if len(sc.granted) > 0 {
			own += " or " + a + ".space_id = any(" + sb.arg(sc.granted) + "::uuid[])"
		}
		where = append(where, "("+own+")")
	}
	if len(sc.hidden) > 0 {
		where = append(where, "not ("+a+".space_id = any("+sb.arg(sc.hidden)+"::uuid[]))")
	}
	return where
}

// textPred: ricerca full-text valutata riga per riga (scorrimento dell'indice ordinato).
func textPred(sb *sqlb, text, prefix string) string {
	var parts []string
	if text != "" {
		p := sb.arg(text)
		parts = append(parts, "(websearch_to_tsquery('simple', unaccent("+p+")) || websearch_to_tsquery('italian', unaccent("+p+")))")
	}
	if prefix != "" {
		parts = append(parts, "to_tsquery('simple', unaccent("+sb.arg(prefix)+"))")
	}
	return "e.search @@ (" + strings.Join(parts, " && ") + ")"
}

// numRange riconosce una condizione numerica semplice (un solo confronto o intervallo su
// v_num) e ne restituisce gli estremi; nil = estremo assente.
func numRange(col string, vals []bval, present bool) (lo any, loIncl bool, hi any, hiIncl bool, ok bool) {
	if col != "v.v_num" || present || len(vals) != 1 {
		return nil, false, nil, false, false
	}
	v := vals[0]
	switch v.op {
	case "eq":
		return v.a, true, v.a, true, true
	case "gt":
		return v.a, false, nil, false, true
	case "ge":
		return v.a, true, nil, false, true
	case "lt":
		return nil, false, v.a, false, true
	case "le":
		return nil, false, v.a, true, true
	case "range":
		if v.hasA {
			lo, loIncl = v.a, true
		}
		if v.hasB {
			hi, hiIncl = v.bb, true
		}
		return lo, loIncl, hi, hiIncl, lo != nil || hi != nil
	}
	return nil, false, nil, false, false
}
