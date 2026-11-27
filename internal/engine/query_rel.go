package engine

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Relazioni e sottoquery nel linguaggio di query (ondata 3 dell'AI, replica/ai.md).
//
//	tipo: application collegata: (tipo: risk fase: aperta)    applicazioni collegate a un rischio aperto
//	tipo: application "è usata da": (tipo: application)       nel verso del tipo di relazione
//	-collegata: presente                                       elementi senza collegamenti
//	genitore: (tipo: epic fase: aperta)                        figli di un'epic aperta
//
// Visibilità: una sottoquery si risolve con gli stessi limiti della ricerca (visibleWhere, le
// regole di CanRead in SQL): trova solo elementi che la persona può leggere, e «presente» conta
// solo i collegamenti verso elementi leggibili. Così una condizione non rivela mai che esiste
// un collegamento con un elemento nascosto. Una chiave scritta a mano che la persona non può
// leggere vale come inesistente.

// SubqueryMaxIDs è il numero massimo di elementi che una sottoquery può trovare: oltre, si
// chiede di restringerla (la condizione diventerebbe un elenco troppo lungo).
var SubqueryMaxIDs = 2000

// linkWords sono i nomi della relazione generica: qualsiasi tipo, nei due versi.
var linkWords = map[string]bool{"collegata": true, "collegato": true, "collegate": true, "collegati": true, "collegamento": true,
	"collegamenti": true, "linked": true, "related": true, "relazione": true, "relation": true}

type relName struct{ ID, Key, Out, In string }

// relSpec è la relazione di una clausola: any = qualsiasi tipo nei due versi; altrimenti i tipi
// in cui l'elemento è origine (out) o destinazione (in). virt: relazione che non sta nella tabella
// delle relazioni ma in quella di un oggetto (gli impatti, ext_impact).
type relSpec struct {
	any     bool
	out, in []string
	virt    []relEnd
}

// Relazioni dagli impatti (seguito dell'ondata 3, replica/ai.md): un impatto lega una richiesta a
// un'applicazione, quindi «quali applicazioni sono impattate da richieste urgenti» si scrive
//
//	tipo: application impattata_da: (tipo: business_request urgenza: Alta)
//	tipo: business_request impatta: APP-12
//	tipo: application impatti: (tipo: impact fase: Stimato)
//
// Valgono solo se il tenant non ha un tipo di relazione con lo stesso nome.
var impactRels = map[string][]relEnd{
	"impatta":      {{table: "ext_impact", self: "r.request_id", other: "r.application_id"}},
	"impacts":      {{table: "ext_impact", self: "r.request_id", other: "r.application_id"}},
	"impattata_da": {{table: "ext_impact", self: "r.application_id", other: "r.request_id"}},
	"impattato_da": {{table: "ext_impact", self: "r.application_id", other: "r.request_id"}},
	"impacted_by":  {{table: "ext_impact", self: "r.application_id", other: "r.request_id"}},
	"impatti": {{table: "ext_impact", self: "r.application_id", other: "r.element_id"},
		{table: "ext_impact", self: "r.request_id", other: "r.element_id"}},
	"impact_items": {{table: "ext_impact", self: "r.application_id", other: "r.element_id"},
		{table: "ext_impact", self: "r.request_id", other: "r.element_id"}},
}

// ImpactRelations sono i nomi delle relazioni dagli impatti, per il catalogo delle domande sul grafo.
var ImpactRelations = []RelationInfo{
	{Key: "impatta", Outward: "richiesta → applicazioni su cui ha un impatto", Inward: "impattata_da"},
	{Key: "impattata_da", Outward: "applicazione ← richieste che hanno un impatto su di lei", Inward: "impatta"},
	{Key: "impatti", Outward: "applicazione o richiesta → elementi impatto (tipo: impact)", Inward: ""},
}

// RelationInfo descrive un tipo di relazione per il catalogo delle domande sul grafo.
type RelationInfo struct {
	Key     string `json:"key"`
	Outward string `json:"outward"`
	Inward  string `json:"inward"`
}

// QueryRelations elenca i tipi di relazione del tenant, con i nomi nei due versi.
func (s *Service) QueryRelations(ctx context.Context, tx pgx.Tx) ([]RelationInfo, error) {
	rows, err := tx.Query(ctx, `select key, outward_name, inward_name from relation_types order by key`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[RelationInfo])
}

// relation riconosce il nome di una clausola di relazione; nil se non lo è.
func (b *binder) relation(name string) (*relSpec, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	if linkWords[n] {
		return &relSpec{any: true}, nil
	}
	if b.rels == nil {
		rows, err := b.tx.Query(b.ctx, `select id::text, key, lower(outward_name), lower(inward_name) from relation_types`)
		if err != nil {
			return nil, err
		}
		if b.rels, err = pgx.CollectRows(rows, pgx.RowToStructByPos[relName]); err != nil {
			return nil, err
		}
		if b.rels == nil {
			b.rels = []relName{}
		}
	}
	sp := strings.ReplaceAll(n, "_", " ")
	rs := &relSpec{}
	for _, r := range b.rels {
		if n == r.Key || n == r.Out || sp == r.Out {
			rs.out = append(rs.out, r.ID)
		}
		if n == r.In || sp == r.In {
			rs.in = append(rs.in, r.ID)
		}
	}
	if len(rs.out)+len(rs.in) == 0 {
		if v, ok := impactRels[strings.ReplaceAll(n, " ", "_")]; ok {
			return &relSpec{virt: v}, nil
		}
		return nil, nil
	}
	return rs, nil
}

// subAllowed: una sottoquery vale solo per i campi che puntano a elementi.
func (b *binder) subAllowed(f *qfield, c *QClause) error {
	ok := (f.sys != nil && f.sys.kind == qkElement) || (f.mod != nil && f.mod.Kind == "ref" && f.mod.RefType != "") ||
		(f.custom != nil && f.custom.qkind() == qkElement)
	for _, v := range c.Values {
		if v.Op == "sub" && !ok {
			return b.err(v.Pos, v.End, "«%s» non accetta una sottoquery: valgono collegata, i tipi di relazione e i campi che puntano a elementi (genitore, applicazione…)", c.Name)
		}
	}
	return nil
}

// planWhere sono le condizioni di un piano su «e»: visibilità, clausole, id già risolti, chiave
// e testo libero valutato riga per riga.
func planWhere(sb *sqlb, pl *plan, actor Actor, ids []string, mode textMode) []string {
	where := visibleWhere(sb, "e", actor, pl.scope)
	for i := range pl.conds {
		where = append(where, pl.conds[i].pred(sb))
	}
	if ids != nil {
		where = append(where, "e.id = any("+sb.arg(ids)+"::uuid[])")
	}
	switch {
	case pl.key != nil:
		where = append(where, "s.key = "+sb.arg(pl.key.space)+" and e.number = "+sb.arg(pl.key.number))
	case mode == textWalk:
		where = append(where, textPred(sb, pl.text, pl.prefix))
	}
	return where
}

// subIDs risolve la sottoquery di un valore negli id degli elementi che la persona può leggere.
func (b *binder) subIDs(v QValue) ([]string, error) {
	pl, err := b.bind(v.Sub)
	if err != nil {
		return nil, err
	}
	ids, err := pl.resolve(b.ctx, b.tx)
	if errors.Is(err, errNoMatch) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	mode, err := b.svc.textStrategy(b.ctx, b.tx, pl)
	if err != nil {
		return nil, err
	}
	if mode.ids != nil {
		if ids = intersect(ids, mode.ids); len(ids) == 0 {
			return []string{}, nil
		}
	}
	sb := &sqlb{}
	sql := "select e.id::text" + elementFrom + " where " + strings.Join(planWhere(sb, pl, b.actor, ids, mode.mode), " and ") +
		" limit " + sb.arg(SubqueryMaxIDs+1)
	rows, err := b.tx.Query(b.ctx, sql, sb.args...)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	if len(out) > SubqueryMaxIDs {
		return nil, b.err(v.Pos, v.End, "la sottoquery trova più di %d elementi: aggiungi condizioni per restringerla", SubqueryMaxIDs)
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}

// visibleIDs tiene degli id solo quelli che la persona può leggere.
func (b *binder) visibleIDs(ids []string) ([]string, error) {
	if len(ids) == 0 {
		return ids, nil
	}
	sc, err := b.spaceScope()
	if err != nil {
		return nil, err
	}
	sb := &sqlb{}
	where := append(visibleWhere(sb, "e", b.actor, sc), "e.id = any("+sb.arg(ids)+"::uuid[])")
	rows, err := b.tx.Query(b.ctx, "select e.id::text from elements e where "+strings.Join(where, " and "), sb.args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// relEnd è un verso della relazione: la colonna dell'elemento, quella dell'altro estremo e i tipi.
// table: la tabella (alias «r»), vuota = relations.
type relEnd struct {
	table       string
	self, other string
	types       []string
}

func (e relEnd) from() string {
	if e.table != "" {
		return e.table + " r"
	}
	return "relations r"
}

func (rs *relSpec) ends() []relEnd {
	if rs.virt != nil {
		return rs.virt
	}
	if rs.any {
		return []relEnd{{self: "r.source_id", other: "r.target_id"}, {self: "r.target_id", other: "r.source_id"}}
	}
	var out []relEnd
	if len(rs.out) > 0 {
		out = append(out, relEnd{self: "r.source_id", other: "r.target_id", types: rs.out})
	}
	if len(rs.in) > 0 {
		out = append(out, relEnd{self: "r.target_id", other: "r.source_id", types: rs.in})
	}
	return out
}

func (e relEnd) typeSQL(sb *sqlb) string {
	if len(e.types) == 0 {
		return ""
	}
	return " and r.type_id = any(" + sb.arg(e.types) + "::uuid[])"
}

// bindRelation: elementi collegati (nel verso e con i tipi della relazione) a uno degli
// elementi indicati (chiavi o sottoquery), oppure con o senza collegamenti («presente», «vuoto»).
func (b *binder) bindRelation(rs *relSpec, c *QClause) (cond, error) {
	var ids []string
	hasIDs, present, empty := false, false, false
	for _, v := range c.Values {
		switch v.Op {
		case "sub":
			got, err := b.subIDs(v)
			if err != nil {
				return cond{}, err
			}
			ids, hasIDs = append(ids, got...), true
		case "empty":
			empty = true
		case "present":
			present = true
		case "eq":
			one := QClause{Name: c.Name, Values: []QValue{v}}
			got, matched, _, err := b.elementRefs(&one, &qfield{}, "", false)
			if err != nil {
				return cond{}, err
			}
			if matched[0] {
				if got, err = b.visibleIDs(got); err != nil {
					return cond{}, err
				}
			}
			if len(got) == 0 {
				// Un codice del catalogo di architettura (APP-MOB) vale come la chiave dell'elemento.
				if id := b.svc.catalogCodeID(b.ctx, b.tx, refKey(v)); id != "" {
					if got, err = b.visibleIDs([]string{id}); err != nil {
						return cond{}, err
					}
				}
			}
			if len(got) == 0 {
				if k := refKey(v); !keyRe.MatchString(k) && !uuidRe.MatchString(k) && b.svc.catalogCodeID(b.ctx, b.tx, k) == "" {
					return cond{}, b.err(v.Pos, v.End, "«%s» non è una chiave: scrivi ad esempio DEM-12, oppure una sottoquery come (tipo: risk)", v.A)
				}
				return cond{}, b.err(v.Pos, v.End, "elemento «%s» inesistente", v.A)
			}
			ids, hasIDs = append(ids, got...), true
		default:
			return cond{}, b.err(v.Pos, v.End, "per «%s» indica chiavi, una sottoquery tra parentesi, «presente» o «vuoto»", c.Name)
		}
	}
	ids = uniq(ids)
	sc, err := b.spaceScope()
	if err != nil {
		return cond{}, err
	}
	actor := b.actor
	ends := rs.ends()
	// any: c'è un collegamento verso uno degli id; visible: verso un elemento leggibile.
	toIDs := func(sb *sqlb) string {
		arg := sb.arg(ids)
		var ors []string
		for _, e := range ends {
			ors = append(ors, "exists (select 1 from "+e.from()+" where r.tenant_id = e.tenant_id and "+e.self+" = e.id"+e.typeSQL(sb)+
				" and "+e.other+" = any("+arg+"::uuid[]) offset 0)")
		}
		return "(" + strings.Join(ors, " or ") + ")"
	}
	toVisible := func(sb *sqlb) string {
		var ors []string
		for _, e := range ends {
			ors = append(ors, "exists (select 1 from "+e.from()+" join elements o on o.tenant_id = r.tenant_id and o.id = "+e.other+
				" where r.tenant_id = e.tenant_id and "+e.self+" = e.id"+e.typeSQL(sb)+" and "+strings.Join(visibleWhere(sb, "o", actor, sc), " and ")+" offset 0)")
		}
		return "(" + strings.Join(ors, " or ") + ")"
	}
	cd := cond{pred: func(sb *sqlb) string {
		var ors []string
		if hasIDs {
			ors = append(ors, toIDs(sb))
		}
		if present {
			ors = append(ors, toVisible(sb))
		}
		if empty {
			ors = append(ors, "not "+toVisible(sb))
		}
		if len(ors) == 1 {
			return ors[0]
		}
		return "(" + strings.Join(ors, " or ") + ")"
	}}
	if !empty {
		// Gli elementi candidati si risolvono dagli indici delle relazioni (un superinsieme va bene).
		cd.res = func(sb *sqlb) string {
			var parts []string
			for _, e := range ends {
				where := "true"
				if hasIDs && !present {
					where = e.other + " = any(" + sb.arg(ids) + "::uuid[])"
				}
				parts = append(parts, "select "+e.self+" from "+e.from()+" where "+where+e.typeSQL(sb))
			}
			return strings.Join(parts, " union all ")
		}
	}
	return cd, nil
}
