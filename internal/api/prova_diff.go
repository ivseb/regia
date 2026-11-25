package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Confronto e promozione della configurazione tra la prova e il principale (migrazione 0065).
//
// Si confrontano oggetti di sei tipi: campi (con le opzioni), tipi (con i campi del tipo), processi
// (versione pubblicata), gate di rilascio, organi di governo e cataloghi di requisiti. Ciò che si
// propone di promuovere è ciò che la prova ha cambiato rispetto alla copia di partenza (baseline,
// un'impronta per oggetto salvata alla creazione); se nel frattempo anche il principale è cambiato
// l'oggetto è segnato «conflitto» e non è preselezionato.

const (
	kindField   = "field"
	kindType    = "type"
	kindProcess = "process"
	kindGate    = "gate"
	kindBody    = "body"
	kindCatalog = "catalog"
)

// provaKinds nell'ordine in cui si promuove: i campi prima dei tipi che li usano, i tipi prima dei processi.
var provaKinds = []string{kindField, kindType, kindProcess, kindGate, kindBody, kindCatalog}

// pObj è un oggetto di configurazione, in una forma confrontabile.
type pObj struct {
	Kind, Key, Name string
	Data            map[string]any
	Def             *engine.Workflow
	Hash            string
}

type pSnap map[string]*pObj

func snapKey(kind, key string) string { return kind + "/" + key }

func (o *pObj) seal() {
	raw, _ := json.Marshal(o.Data)
	sum := sha256.Sum256(raw)
	o.Hash = hex.EncodeToString(sum[:8])
}

func (m pSnap) add(o *pObj) {
	o.seal()
	m[snapKey(o.Kind, o.Key)] = o
}

// snapshot legge la configurazione del tenant corrente della transazione.
func snapshot(ctx context.Context, tx pgx.Tx) (pSnap, error) {
	out := pSnap{}
	if err := snapFields(ctx, tx, out); err != nil {
		return nil, fmt.Errorf("campi: %w", err)
	}
	if err := snapTypes(ctx, tx, out); err != nil {
		return nil, fmt.Errorf("tipi: %w", err)
	}
	if err := snapProcesses(ctx, tx, out); err != nil {
		return nil, fmt.Errorf("processi: %w", err)
	}
	if err := snapMisc(ctx, tx, out); err != nil {
		return nil, fmt.Errorf("gate, organi e cataloghi: %w", err)
	}
	return out, nil
}

func snapFields(ctx context.Context, tx pgx.Tx, out pSnap) error {
	opts := map[string]map[string]any{}
	rows, err := tx.Query(ctx, `select option_set_id, name, color, ordinal, archived, category, labels from options`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var set, name string
		var color, category *string
		var ord int
		var archived bool
		var labels any
		if err := rows.Scan(&set, &name, &color, &ord, &archived, &category, &labels); err != nil {
			rows.Close()
			return err
		}
		if opts[set] == nil {
			opts[set] = map[string]any{}
		}
		opts[set][name] = map[string]any{"color": color, "ordinal": ord, "archived": archived, "category": category, "labels": labels}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	fr, err := tx.Query(ctx, `select key, name, data_type, is_multi, settings, aliases, help, default_value, archived, option_set_id from field_defs order by key`)
	if err != nil {
		return err
	}
	defer fr.Close()
	for fr.Next() {
		var key, name, dt, help string
		var multi, archived bool
		var settings, def any
		var aliases []string
		var set *string
		if err := fr.Scan(&key, &name, &dt, &multi, &settings, &aliases, &help, &def, &archived, &set); err != nil {
			return err
		}
		d := map[string]any{"name": name, "data_type": dt, "multi": multi, "settings": settings, "aliases": aliases, "help": help, "default": def, "archived": archived}
		if set != nil {
			d["options"] = opts[*set]
		}
		out.add(&pObj{Kind: kindField, Key: key, Name: name, Data: d})
	}
	return fr.Err()
}

func snapTypes(ctx context.Context, tx pgx.Tx, out pSnap) error {
	tf := map[string]map[string]any{}
	rows, err := tx.Query(ctx, `select tf.type_id, f.key, tf.required, tf.default_value, tf.ordinal, tf.is_private, tf.show_in_list, tf.condition,
		tf.section, tf.visible_when, tf.required_from from type_fields tf join field_defs f on f.id = tf.field_id where tf.space_id is null`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var typ, key, section, vis, reqFrom string
		var required, private, show bool
		var def, cond any
		var ord int
		if err := rows.Scan(&typ, &key, &required, &def, &ord, &private, &show, &cond, &section, &vis, &reqFrom); err != nil {
			rows.Close()
			return err
		}
		if tf[typ] == nil {
			tf[typ] = map[string]any{}
		}
		tf[typ][key] = map[string]any{"required": required, "default": def, "ordinal": ord, "private": private, "show_in_list": show,
			"condition": cond, "section": section, "visible_when": vis, "required_from": reqFrom}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	tr, err := tx.Query(ctx, `select id, key, name, plural_name, description, icon, color, portal_enabled, archived_at is not null, labels from element_types order by key`)
	if err != nil {
		return err
	}
	defer tr.Close()
	for tr.Next() {
		var id, key, name, desc string
		var plural, icon, color *string
		var portal, archived bool
		var labels any
		if err := tr.Scan(&id, &key, &name, &plural, &desc, &icon, &color, &portal, &archived, &labels); err != nil {
			return err
		}
		d := map[string]any{"name": name, "plural_name": plural, "description": desc, "icon": icon, "color": color, "portal": portal,
			"archived": archived, "labels": labels, "fields": tf[id]}
		out.add(&pObj{Kind: kindType, Key: key, Name: name, Data: d})
	}
	return tr.Err()
}

func snapProcesses(ctx context.Context, tx pgx.Tx, out pSnap) error {
	rows, err := tx.Query(ctx, `select d.key, d.name, d.description, t.key, d.priority, d.applies_when, d.archived, v.definition
		from process_definitions d join element_types t on t.id = d.type_id
		join process_versions v on v.process_id = d.id and v.status = 'published' order by d.key`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var key, name, desc, typ, when string
		var prio int
		var archived bool
		var raw []byte
		if err := rows.Scan(&key, &name, &desc, &typ, &prio, &when, &archived, &raw); err != nil {
			return err
		}
		w, err := engine.DecodeDefinition(raw)
		if err != nil {
			return fmt.Errorf("processo %s: %w", key, err)
		}
		norm, _ := json.Marshal(w)
		var def any
		_ = json.Unmarshal(norm, &def)
		out.add(&pObj{Kind: kindProcess, Key: key, Name: name, Def: w, Data: map[string]any{"name": name, "description": desc, "type": typ,
			"priority": prio, "applies_when": when, "archived": archived, "definition": def}})
	}
	return rows.Err()
}

func snapMisc(ctx context.Context, tx pgx.Tx, out pSnap) error {
	g, err := tx.Query(ctx, `select environment, mode, rules, override_enabled, override_hours from gate_policies order by environment`)
	if err != nil {
		return err
	}
	for g.Next() {
		var env, mode string
		var rules any
		var en bool
		var hours int
		if err := g.Scan(&env, &mode, &rules, &en, &hours); err != nil {
			g.Close()
			return err
		}
		out.add(&pObj{Kind: kindGate, Key: env, Name: env, Data: map[string]any{"mode": mode, "rules": rules, "override_enabled": en, "override_hours": hours}})
	}
	g.Close()
	if err := g.Err(); err != nil {
		return err
	}
	b, err := tx.Query(ctx, `select id, name, kind, description, approval_limit_cents, quorum, archived, inline_decision from governance_bodies order by name`)
	if err != nil {
		return err
	}
	for b.Next() {
		var id, name, kind, desc string
		var limit *int64
		var quorum *int16
		var archived, inline bool
		if err := b.Scan(&id, &name, &kind, &desc, &limit, &quorum, &archived, &inline); err != nil {
			b.Close()
			return err
		}
		out.add(&pObj{Kind: kindBody, Key: id, Name: name, Data: map[string]any{"name": name, "kind": kind, "description": desc,
			"approval_limit_cents": limit, "quorum": quorum, "archived": archived, "inline_decision": inline}})
	}
	b.Close()
	if err := b.Err(); err != nil {
		return err
	}
	c, err := tx.Query(ctx, `select key, name, description, definition from requirement_catalogs where status = 'published' order by key`)
	if err != nil {
		return err
	}
	defer c.Close()
	for c.Next() {
		var key, name, desc string
		var def any
		if err := c.Scan(&key, &name, &desc, &def); err != nil {
			return err
		}
		out.add(&pObj{Kind: kindCatalog, Key: key, Name: name, Data: map[string]any{"name": name, "description": desc, "definition": def}})
	}
	return c.Err()
}

// provaChange è una differenza leggibile: attributo (con eventuale voce annidata: un campo del tipo,
// un'opzione) e valori prima (principale) e dopo (prova). Detail «+» e «-» indicano una voce annidata
// aggiunta o tolta.
type provaChange struct {
	Attr   string `json:"attr"`
	Sub    string `json:"sub,omitempty"`
	Detail string `json:"detail,omitempty"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

type provaItem struct {
	Kind      string           `json:"kind"`
	Key       string           `json:"key"`
	Name      string           `json:"name"`
	Change    string           `json:"change"` // added | modified | removed
	Conflict  bool             `json:"conflict"`
	Changes   []provaChange    `json:"changes"`
	BeforeDef *engine.Workflow `json:"before_def,omitempty"`
	AfterDef  *engine.Workflow `json:"after_def,omitempty"`
}

func same(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func sortedKeys(a, b map[string]any) []string {
	set := map[string]bool{}
	for k := range a {
		set[k] = true
	}
	for k := range b {
		set[k] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// diffData elenca le differenze tra due oggetti; «fields» e «options» si scompongono per voce.
func diffData(kind string, before, after map[string]any) []provaChange {
	out := []provaChange{}
	for _, attr := range sortedKeys(before, after) {
		b, a := before[attr], after[attr]
		if (attr == "definition" && kind == kindProcess) || same(b, a) {
			continue
		}
		bm, bok := b.(map[string]any)
		am, aok := a.(map[string]any)
		if (attr == "fields" || attr == "options") && (bok || b == nil) && (aok || a == nil) {
			for _, sub := range sortedKeys(bm, am) {
				sb, sa := bm[sub], am[sub]
				switch {
				case sb == nil:
					out = append(out, provaChange{Attr: attr, Sub: sub, Detail: "+"})
				case sa == nil:
					out = append(out, provaChange{Attr: attr, Sub: sub, Detail: "-"})
				default:
					sbm, _ := sb.(map[string]any)
					sam, _ := sa.(map[string]any)
					for _, d := range sortedKeys(sbm, sam) {
						if !same(sbm[d], sam[d]) {
							out = append(out, provaChange{Attr: attr, Sub: sub, Detail: d, Before: sbm[d], After: sam[d]})
						}
					}
				}
			}
			continue
		}
		out = append(out, provaChange{Attr: attr, Before: b, After: a})
	}
	return out
}

// computeDiff confronta la prova con il principale a partire dalla baseline.
func computeDiff(main, sbx pSnap, baseline map[string]string) []provaItem {
	keys := map[string]bool{}
	for k := range main {
		keys[k] = true
	}
	for k := range sbx {
		keys[k] = true
	}
	for k := range baseline {
		keys[k] = true
	}
	order := map[string]int{}
	for i, k := range provaKinds {
		order[k] = i
	}
	items := []provaItem{}
	for k := range keys {
		m, s := main[k], sbx[k]
		base, hadBase := baseline[k]
		var kind, key, name string
		switch {
		case s != nil:
			kind, key, name = s.Kind, s.Key, s.Name
		case m != nil:
			kind, key, name = m.Kind, m.Key, m.Name
		default:
			continue
		}
		if s != nil && hadBase && s.Hash == base {
			continue // la prova non l'ha toccato
		}
		it := provaItem{Kind: kind, Key: key, Name: name, Changes: []provaChange{}}
		switch {
		case s != nil && m == nil:
			it.Change = "added"
			it.Changes = diffData(kind, map[string]any{}, s.Data)
		case s != nil:
			if m.Hash == s.Hash {
				continue // già uguale nel principale
			}
			it.Change = "modified"
			it.Changes = diffData(kind, m.Data, s.Data)
			it.Conflict = hadBase && m.Hash != base
		default: // s == nil: tolto nella prova
			if !hadBase || kind == kindCatalog {
				continue
			}
			if m.Data["archived"] == true {
				continue
			}
			it.Change = "removed"
			it.Conflict = m.Hash != base
		}
		if kind == kindProcess {
			if s != nil {
				it.AfterDef = s.Def
			}
			if m != nil {
				it.BeforeDef = m.Def
			}
			if s != nil && m != nil && same(m.Data["definition"], s.Data["definition"]) {
				it.BeforeDef, it.AfterDef = nil, nil
			}
		}
		items = append(items, it)
	}
	sort.Slice(items, func(i, j int) bool {
		if order[items[i].Kind] != order[items[j].Kind] {
			return order[items[i].Kind] < order[items[j].Kind]
		}
		return items[i].Name < items[j].Name
	})
	return items
}

// ---------------------------------------------------------------------
// Applicazione nel principale
// ---------------------------------------------------------------------

// promoter porta gli oggetti scelti dalla prova nel principale, dentro la transazione del chiamante.
type promoter struct {
	s         *Server
	env       *provaEnv
	sbx       pSnap
	remap     remapper // prova → principale (tenant e persona)
	actorMain engine.Actor
	actorSbx  engine.Actor
}

func depError(kind, key, needs string) error {
	return engine.InvalidCode("validation.sandbox.dependency", "items", "«%s» (%s) richiede «%s», che nel principale non c'è: promuovi anche quello", key, kind, needs)
}

func (pr *promoter) apply(ctx context.Context, tx pgx.Tx, it provaItem) error {
	o := pr.sbx[snapKey(it.Kind, it.Key)]
	if err := bindTo(ctx, tx, pr.env.MainID); err != nil {
		return err
	}
	switch it.Kind {
	case kindField:
		return pr.field(ctx, tx, it, o)
	case kindType:
		return pr.typ(ctx, tx, it, o)
	case kindProcess:
		return pr.process(ctx, tx, it, o)
	case kindGate:
		return pr.gate(ctx, tx, it, o)
	case kindBody:
		return pr.body(ctx, tx, it, o)
	case kindCatalog:
		return pr.catalog(ctx, tx, it, o)
	}
	return engine.InvalidCode("validation.sandbox.item_unknown", "items", "oggetto sconosciuto: %s", it.Kind)
}

// fromSandbox legge righe della prova (JSON) e torna nel principale.
func (pr *promoter) fromSandbox(ctx context.Context, tx pgx.Tx, table, where string, args ...any) (string, error) {
	if err := bindTo(ctx, tx, pr.env.SbxID); err != nil {
		return "", err
	}
	var raw []byte
	err := tx.QueryRow(ctx, fmt.Sprintf("select coalesce(jsonb_agg(to_jsonb(t)), '[]'::jsonb) from %s t where %s", table, where), args...).Scan(&raw)
	if err != nil {
		return "", err
	}
	if err := bindTo(ctx, tx, pr.env.MainID); err != nil {
		return "", err
	}
	return pr.remap.apply(string(raw)), nil
}

func (pr *promoter) field(ctx context.Context, tx pgx.Tx, it provaItem, o *pObj) error {
	if it.Change == "removed" {
		_, err := tx.Exec(ctx, "update field_defs set archived = true, updated_at = now() where key = $1", it.Key)
		return err
	}
	var setID *string
	if err := bindTo(ctx, tx, pr.env.SbxID); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, "select option_set_id from field_defs where key = $1", it.Key).Scan(&setID); err != nil {
		return err
	}
	var exists bool
	if err := bindTo(ctx, tx, pr.env.MainID); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, "select exists (select 1 from field_defs where key = $1)", it.Key).Scan(&exists); err != nil {
		return err
	}
	if setID != nil {
		sets, err := pr.fromSandbox(ctx, tx, "option_sets", "id = $1", *setID)
		if err != nil {
			return err
		}
		if err := writeRows(ctx, tx, "option_sets", sets, true); err != nil {
			return err
		}
		// Le opzioni: quelle già presenti (per nome) si aggiornano, le altre si aggiungono.
		opts, err := pr.fromSandbox(ctx, tx, "options", "option_set_id = $1", *setID)
		if err != nil {
			return err
		}
		var rows []map[string]any
		if err := json.Unmarshal([]byte(opts), &rows); err != nil {
			return err
		}
		for _, r := range rows {
			tag, err := tx.Exec(ctx, `update options set color = $3, ordinal = $4, archived = $5, category = $6, labels = $7
				where option_set_id = $1 and name = $2`, r["option_set_id"], r["name"], r["color"], r["ordinal"], r["archived"], r["category"], r["labels"])
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				one, _ := json.Marshal([]map[string]any{r})
				if err := writeRows(ctx, tx, "options", string(one), true); err != nil {
					return err
				}
			}
		}
	}
	if !exists {
		row, err := pr.fromSandbox(ctx, tx, "field_defs", "key = $1", it.Key)
		if err != nil {
			return err
		}
		return writeRows(ctx, tx, "field_defs", row, false)
	}
	d := o.Data
	_, err := tx.Exec(ctx, `update field_defs set name = $2, help = $3, default_value = $4, settings = $5, aliases = $6, archived = $7, updated_at = now() where key = $1`,
		it.Key, d["name"], d["help"], d["default"], d["settings"], d["aliases"], d["archived"])
	return err
}

func (pr *promoter) typ(ctx context.Context, tx pgx.Tx, it provaItem, o *pObj) error {
	if it.Change == "removed" {
		_, err := tx.Exec(ctx, "update element_types set archived_at = now(), updated_at = now() where key = $1 and archived_at is null", it.Key)
		return err
	}
	if err := bindTo(ctx, tx, pr.env.SbxID); err != nil {
		return err
	}
	var typeID string
	if err := tx.QueryRow(ctx, "select id from element_types where key = $1", it.Key).Scan(&typeID); err != nil {
		return err
	}
	if err := bindTo(ctx, tx, pr.env.MainID); err != nil {
		return err
	}
	var mainID *string
	err := tx.QueryRow(ctx, "select id from element_types where key = $1", it.Key).Scan(&mainID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	d := o.Data
	if mainID == nil {
		// Tipo nuovo: insieme degli stati e flusso di lavoro prima, poi il tipo e la sua presenza negli spazi.
		row, err := pr.fromSandbox(ctx, tx, "element_types", "id = $1", typeID)
		if err != nil {
			return err
		}
		var rows []map[string]any
		if err := json.Unmarshal([]byte(row), &rows); err != nil || len(rows) != 1 {
			return fmt.Errorf("tipo %s non leggibile", it.Key)
		}
		if set, ok := rows[0]["state_set_id"].(string); ok {
			sets, err := pr.fromSandbox(ctx, tx, "option_sets", "id = $1", set)
			if err != nil {
				return err
			}
			if err := writeRows(ctx, tx, "option_sets", sets, true); err != nil {
				return err
			}
			opts, err := pr.fromSandbox(ctx, tx, "options", "option_set_id = $1", set)
			if err != nil {
				return err
			}
			if err := writeRows(ctx, tx, "options", opts, true); err != nil {
				return err
			}
		}
		if wf, ok := rows[0]["workflow_id"].(string); ok {
			w, err := pr.fromSandbox(ctx, tx, "workflows", "id = $1", wf)
			if err != nil {
				return err
			}
			if err := writeRows(ctx, tx, "workflows", w, true); err != nil {
				return err
			}
		}
		if err := writeRows(ctx, tx, "element_types", row, false); err != nil {
			return err
		}
		st, err := pr.fromSandbox(ctx, tx, "space_types", "type_id = $1", typeID)
		if err != nil {
			return err
		}
		var sts []map[string]any
		_ = json.Unmarshal([]byte(st), &sts)
		for _, r := range sts {
			var ok bool
			if err := tx.QueryRow(ctx, "select exists (select 1 from spaces where id = $1)", r["space_id"]).Scan(&ok); err != nil {
				return err
			}
			if ok {
				one, _ := json.Marshal([]map[string]any{r})
				if err := writeRows(ctx, tx, "space_types", string(one), true); err != nil {
					return err
				}
			}
		}
		mainID = &typeID
	} else {
		_, err := tx.Exec(ctx, `update element_types set name = $2, plural_name = $3, description = $4, icon = $5, color = $6, portal_enabled = $7,
			labels = coalesce($8, labels), archived_at = case when $9 then coalesce(archived_at, now()) else null end, updated_at = now() where id = $1`,
			*mainID, d["name"], d["plural_name"], d["description"], d["icon"], d["color"], d["portal"], d["labels"], d["archived"])
		if err != nil {
			return err
		}
	}
	return pr.typeFields(ctx, tx, it, *mainID, d)
}

// typeFields riallinea i campi del tipo (quelli di base, non gli override per spazio).
func (pr *promoter) typeFields(ctx context.Context, tx pgx.Tx, it provaItem, typeID string, d map[string]any) error {
	fields, _ := d["fields"].(map[string]any)
	keep := []string{}
	for fkey, v := range fields {
		f, _ := v.(map[string]any)
		var fieldID string
		if err := tx.QueryRow(ctx, "select id from field_defs where key = $1", fkey).Scan(&fieldID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return depError(it.Kind, it.Key, fkey)
			}
			return err
		}
		keep = append(keep, fieldID)
		tag, err := tx.Exec(ctx, `update type_fields set required = $3, default_value = $4, ordinal = $5, is_private = $6, show_in_list = $7, condition = $8,
			section = $9, visible_when = $10, required_from = $11 where type_id = $1 and field_id = $2 and space_id is null`,
			typeID, fieldID, f["required"], f["default"], f["ordinal"], f["private"], f["show_in_list"], f["condition"], f["section"], f["visible_when"], f["required_from"])
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			if _, err := tx.Exec(ctx, `insert into type_fields (tenant_id, id, type_id, field_id, required, default_value, ordinal, is_private, show_in_list, condition,
				section, visible_when, required_from) values (app_tenant(), gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
				typeID, fieldID, f["required"], f["default"], f["ordinal"], f["private"], f["show_in_list"], f["condition"], f["section"], f["visible_when"], f["required_from"]); err != nil {
				return err
			}
		}
	}
	_, err := tx.Exec(ctx, "delete from type_fields where type_id = $1 and space_id is null and not (field_id = any($2::uuid[]))", typeID, keep)
	return err
}

func (pr *promoter) process(ctx context.Context, tx pgx.Tx, it provaItem, o *pObj) error {
	if it.Change == "removed" {
		archived := true
		_, err := pr.s.eng.UpdateProcess(ctx, tx, pr.actorMain, it.Key, engine.ProcessInput{Archived: &archived})
		return err
	}
	if err := bindTo(ctx, tx, pr.env.SbxID); err != nil {
		return err
	}
	b, err := pr.s.eng.Export(ctx, tx, pr.actorSbx, it.Key, 0)
	if err != nil {
		return err
	}
	if err := bindTo(ctx, tx, pr.env.MainID); err != nil {
		return err
	}
	var exists bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from process_definitions where key = $1)", it.Key).Scan(&exists); err != nil {
		return err
	}
	b.Notes = "Promosso dall'ambiente di prova"
	if exists && it.BeforeDef == nil {
		// Stessa definizione: cambiano solo i dati del processo, niente nuova versione.
	} else if _, _, err := pr.s.eng.Import(ctx, tx, pr.actorMain, b, true); err != nil {
		return err
	}
	if !exists {
		return nil
	}
	d := o.Data
	name, desc, when := fmt.Sprint(d["name"]), fmt.Sprint(d["description"]), fmt.Sprint(d["applies_when"])
	prio, _ := d["priority"].(int)
	archived, _ := d["archived"].(bool)
	_, err = pr.s.eng.UpdateProcess(ctx, tx, pr.actorMain, it.Key, engine.ProcessInput{Name: &name, Description: &desc, Priority: &prio, AppliesWhen: &when, Archived: &archived})
	return err
}

func (pr *promoter) gate(ctx context.Context, tx pgx.Tx, it provaItem, o *pObj) error {
	if it.Change == "removed" {
		_, err := tx.Exec(ctx, "delete from gate_policies where environment = $1", it.Key)
		return err
	}
	d := o.Data
	tag, err := tx.Exec(ctx, `update gate_policies set mode = $2, rules = $3, override_enabled = $4, override_hours = $5, updated_by = $6, updated_at = now() where environment = $1`,
		it.Key, d["mode"], d["rules"], d["override_enabled"], d["override_hours"], pr.env.Member[pr.env.MainID])
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	_, err = tx.Exec(ctx, `insert into gate_policies (tenant_id, id, environment, mode, rules, override_enabled, override_hours, updated_by)
		values (app_tenant(), gen_random_uuid(), $1, $2, $3, $4, $5, $6)`,
		it.Key, d["mode"], d["rules"], d["override_enabled"], d["override_hours"], pr.env.Member[pr.env.MainID])
	return err
}

func (pr *promoter) body(ctx context.Context, tx pgx.Tx, it provaItem, o *pObj) error {
	if it.Change == "removed" {
		_, err := tx.Exec(ctx, "update governance_bodies set archived = true where id = $1", it.Key)
		return err
	}
	d := o.Data
	tag, err := tx.Exec(ctx, `update governance_bodies set name = $2, kind = $3, description = $4, approval_limit_cents = $5, quorum = $6, archived = $7, inline_decision = $8
		where id = $1`, it.Key, d["name"], d["kind"], d["description"], d["approval_limit_cents"], d["quorum"], d["archived"], d["inline_decision"])
	if err != nil {
		return err
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	_, err = tx.Exec(ctx, `insert into governance_bodies (tenant_id, id, name, kind, description, approval_limit_cents, quorum, archived, inline_decision)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8)`, it.Key, d["name"], d["kind"], d["description"], d["approval_limit_cents"], d["quorum"], d["archived"], d["inline_decision"])
	return err
}

// catalog pubblica il catalogo come nuova versione (le pubblicate sono immutabili: la vecchia diventa superata).
func (pr *promoter) catalog(ctx context.Context, tx pgx.Tx, it provaItem, o *pObj) error {
	d := o.Data
	var next int
	if err := tx.QueryRow(ctx, "select coalesce(max(version), 0) + 1 from requirement_catalogs where key = $1", it.Key).Scan(&next); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "update requirement_catalogs set status = 'superseded' where key = $1 and status = 'published'", it.Key); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `insert into requirement_catalogs (tenant_id, id, key, version, status, name, description, definition, notes, created_by, published_by, published_at)
		values (app_tenant(), gen_random_uuid(), $1, $2, 'published', $3, $4, $5, $6, $7, $7, now())`,
		it.Key, next, d["name"], d["description"], d["definition"], "Promosso dall'ambiente di prova", pr.env.Member[pr.env.MainID])
	return err
}
