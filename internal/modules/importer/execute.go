package importer

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"regia/internal/engine"
	"regia/internal/modules/architecture"
	"regia/internal/modules/work"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Scrittura del piano: prepare crea ciò che manca (spazio, campi, opzioni, membro
// «importato», tipi di relazione, sprint), poi gli elementi a lotti (COPY in una tabella
// temporanea e un solo INSERT … SELECT per lotto), quindi relazioni, commenti, ore e
// allegati. Ogni lotto è una transazione: il server resta libero e l'avanzamento si vede.

// Report è l'esito di un'anteprima o di un'esecuzione.
type Report struct {
	DryRun      bool           `json:"dry_run"`
	Created     int            `json:"created"`
	Updated     int            `json:"updated"`
	Skipped     int            `json:"skipped"` // tipi non importati
	Errors      int            `json:"errors"`
	Warnings    int            `json:"warnings"`
	ByType      map[string]int `json:"by_type"`
	Comments    int            `json:"comments"`
	Worklogs    int            `json:"worklogs"`
	Relations   int            `json:"relations"`
	Hierarchy   int            `json:"hierarchy"` // elementi con un genitore
	Sprints     int            `json:"sprints"`
	Attachments int            `json:"attachments"`
	AttLinks    int            `json:"attachment_links"`
	Fields      []string       `json:"fields_created"`
	Options     int            `json:"options_created"`
	Space       string         `json:"space"`
	SpaceNew    bool           `json:"space_new"`
	Placeholder string         `json:"placeholder,omitempty"`
	Messages    []string       `json:"messages"`
	// Trial: la prova sul database (primo lotto scritto e annullato) è andata bene.
	Trial      string          `json:"trial,omitempty"`
	DurationMS int64           `json:"duration_ms"`
	Rollback   *RollbackReport `json:"rollback,omitempty"`
}

func (p *plan) report() *Report {
	errs, warns := p.errorCount()
	r := &Report{Created: p.created, Updated: p.updated, Skipped: p.skipped, Errors: errs, Warnings: warns, ByType: p.counts,
		Space: p.space.key, SpaceNew: p.space.isNew, AttLinks: p.attLinks, Messages: append([]string{}, p.warnings...), Fields: []string{}}
	for _, pi := range p.issues {
		r.Comments += len(pi.comments)
		r.Worklogs += len(pi.worklogs)
		r.Attachments += len(pi.atts)
		if pi.parentID != nil {
			r.Hierarchy++
		}
		if pi.sprint >= 0 {
			r.Relations++
		}
	}
	r.Relations += len(p.links)
	r.Sprints = len(p.sprints)
	for _, f := range p.fields {
		if f.isNew && f.used() {
			r.Fields = append(r.Fields, f.name)
		}
		r.Options += len(f.newOptions)
	}
	if p.needHolder {
		r.Placeholder = p.holderName
	}
	return r
}

// used: il campo nuovo serve davvero (almeno un valore, o è il campo origine).
func (f *fieldMeta) used() bool { return f.key == originFieldKey || f.hasValues }

// recorder raccoglie gli oggetti creati (import_created).
type recorder struct {
	kinds []string
	ids   []string
}

func (r *recorder) add(kind, id string) { r.kinds, r.ids = append(r.kinds, kind), append(r.ids, id) }

func (r *recorder) flush(ctx context.Context, tx pgx.Tx, runID string) error {
	if len(r.ids) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `insert into import_created (tenant_id, run_id, kind, target_id)
		select app_tenant(), $1, k, t from unnest($2::text[], $3::uuid[]) as x(k, t) on conflict do nothing`, runID, r.kinds, r.ids)
	r.kinds, r.ids = nil, nil
	return err
}

func importMode(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `select set_config('app.import', 'on', true)`)
	return err
}

// prepare crea gli oggetti che il piano richiede e risolve i valori dei campi.
func (p *plan) prepare(ctx context.Context, tx pgx.Tx, eng *engine.Service, runID string, rec *recorder) error {
	if err := importMode(ctx, tx); err != nil {
		return err
	}
	adm := admin(p.actor)
	// Tipi di relazione dell'import (blocca, collegato a, duplica), se mancano.
	for _, r := range builtinRelations {
		if _, ok := p.relTypes[r.key]; ok {
			continue
		}
		id := db.NewID()
		tag, err := tx.Exec(ctx, `insert into relation_types (tenant_id, id, key, outward_name, inward_name, directed, is_system)
			values (app_tenant(), $1, $2, $3, $4, $5, false) on conflict do nothing`, id, r.key, r.out, r.in, r.directed)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 1 {
			p.relTypes[r.key] = id
			rec.add("relation_type", id)
		} else if err := tx.QueryRow(ctx, `select id::text from relation_types where key = $1`, r.key).Scan(&id); err == nil {
			p.relTypes[r.key] = id
		}
	}
	// Spazio nuovo: un team con il suo spazio di delivery, con la chiave scelta.
	if p.space.isNew {
		teamID, err := (&architecture.Service{}).CreateTeam(ctx, tx, adm, architecture.TeamInput{Name: p.space.team})
		if err != nil {
			return err
		}
		rec.add("team", teamID)
		spaceID, err := work.EnsureTeamSpace(ctx, tx, adm, work.SpaceInput{TeamID: teamID, Key: p.space.key, Name: p.space.team})
		if err != nil {
			return err
		}
		p.space.id = spaceID
		rec.add("space", spaceID)
	}
	if p.keepNumbers {
		// I numeri nuovi vengono dopo quelli d'origine.
		var max int64
		for _, pi := range p.issues {
			if pi.number > max {
				max = pi.number
			}
		}
		if err := audit.LockChain(ctx, tx); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update spaces set next_number = greatest(next_number, $2) where id = $1`, p.space.id, max+1); err != nil {
			return err
		}
	}
	// Membro «importato»: per le persone senza corrispondenza scelte così, e come autore dei
	// commenti e delle ore di chi non ha un membro.
	if p.needHolder && p.holderID == "" {
		id := db.NewID()
		if _, err := tx.Exec(ctx, `insert into members (tenant_id, id, display_name, member_type, status) values (app_tenant(), $1, $2, 'service', 'active')`,
			id, p.holderName); err != nil {
			return err
		}
		p.holderID = id
		rec.add("member", id)
	}
	// Valori dei campi con i nomi delle opzioni: i campi nuovi nascono con le loro opzioni.
	for _, pi := range p.issues {
		for k := range pi.raw {
			if f := p.fields[k]; f != nil {
				f.hasValues = true
			}
		}
	}
	types := map[string]bool{}
	for _, pi := range p.issues {
		types[pi.typ] = true
	}
	for _, f := range p.fields {
		if !f.isNew || !f.used() {
			continue
		}
		if f.dataType == "enum" && len(f.newOptions) > 500 {
			// Troppe opzioni per un campo a scelta (il limite dell'editor dei campi): testo multiplo.
			p.warnings = append(p.warnings, fmt.Sprintf("«%s» ha %d valori diversi: diventa un campo di testo invece che a scelta", f.name, len(f.newOptions)))
			f.dataType, f.newOptions = "string", nil
		}
		in := engine.FieldInput{Key: &f.key, Name: &f.name, DataType: f.dataType}
		multi := f.multi
		in.Multi = &multi
		help := "Creato dall'import da " + strings.TrimPrefix(p.holderName, "Importato da ") + "."
		in.Help = &help
		for _, o := range f.newOptions {
			in.Options = append(in.Options, engine.OptionInput{Name: o})
		}
		fc, err := eng.CreateField(ctx, tx, adm, in)
		if err != nil {
			return fmt.Errorf("campo «%s»: %w", f.name, err)
		}
		f.id, f.optionSetID, f.newOptions = fc.ID, fc.OptionSetID, nil
		for _, o := range fc.Options {
			f.options[strings.ToLower(o.Name)] = o.ID
			f.optionNames[strings.ToLower(o.Name)] = o.Name
		}
		rec.add("field", f.id)
	}
	// Opzioni nuove nei campi esistenti.
	for _, f := range p.fields {
		if f.isNew || len(f.newOptions) == 0 || f.optionSetID == nil {
			continue
		}
		var count, ord int
		if err := tx.QueryRow(ctx, `select count(*), coalesce(max(ordinal) + 1, 0) from options where option_set_id = $1 and not archived`, *f.optionSetID).Scan(&count, &ord); err != nil {
			return err
		}
		for _, name := range f.newOptions {
			if count >= 500 {
				p.warnings = append(p.warnings, fmt.Sprintf("«%s» ha già 500 opzioni: «%s» e i valori successivi non si importano", f.name, name))
				break
			}
			id := db.NewID()
			tag, err := tx.Exec(ctx, `insert into options (tenant_id, id, option_set_id, name, ordinal) values (app_tenant(), $1, $2, $3, $4)
				on conflict do nothing`, id, *f.optionSetID, name, ord)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				// Esiste archiviata: si usa quella.
				if err := tx.QueryRow(ctx, `select id::text from options where option_set_id = $1 and lower(name) = lower($2)`, *f.optionSetID, name).Scan(&id); err != nil {
					return err
				}
			} else {
				rec.add("option", id)
				count++
				ord++
			}
			f.options[strings.ToLower(name)] = id
		}
		f.newOptions = nil
	}
	// Associazione ai tipi importati dei campi usati.
	for _, f := range p.fields {
		if !f.used() || f.id == "" {
			continue
		}
		for t := range types {
			if f.types[t] {
				continue
			}
			if _, err := eng.AssociateField(ctx, tx, adm, f.key, t, engine.AssocInput{Section: "Importazione"}); err != nil {
				return fmt.Errorf("campo «%s» sul tipo %s: %w", f.name, t, err)
			}
			f.types[t] = true
		}
	}
	for t := range types {
		defs, err := engine.TypeFieldDefs(ctx, tx, p.types[t].info.ID)
		if err != nil {
			return err
		}
		p.types[t].defs = defs
	}
	p.resolveValues()
	if err := p.writeSprints(ctx, tx, runID); err != nil {
		return err
	}
	return rec.flush(ctx, tx, runID)
}

// resolveValues: nomi delle opzioni → id, e forma salvata in elements.fields.
func (p *plan) resolveValues() {
	for _, pi := range p.issues {
		out := map[string]any{}
		for key, vals := range pi.raw {
			f := p.fields[key]
			if f == nil || f.id == "" {
				continue
			}
			var stored []any
			for _, v := range vals {
				if f.dataType == "enum" {
					id, ok := f.options[strings.ToLower(fmt.Sprint(v))]
					if !ok {
						continue
					}
					v = id
				}
				stored = append(stored, v)
			}
			switch {
			case len(stored) == 0:
			case f.multi:
				out[key] = stored
			default:
				out[key] = stored[0]
			}
		}
		pi.fields = out
	}
}

// writeSprints crea (o aggiorna) gli sprint nello spazio.
func (p *plan) writeSprints(ctx context.Context, tx pgx.Tx, runID string) error {
	if len(p.sprints) == 0 {
		return nil
	}
	st, err := engine.LoadType(ctx, tx, "sprint")
	if err != nil {
		return err
	}
	phase := func(state string) engine.State {
		cat := map[string]string{"planned": "todo", "active": "in_progress", "closed": "done"}[state]
		for _, s := range st.States {
			if s.Category == cat {
				return s
			}
		}
		return st.States[0]
	}
	var version *string
	if st.Process != nil {
		version = &st.Process.VersionID
	}
	var fresh []*pSprint
	for _, s := range p.sprints {
		if !s.existing {
			fresh = append(fresh, s)
		}
	}
	var first int64
	if len(fresh) > 0 {
		if err := audit.LockChain(ctx, tx); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `update spaces set next_number = next_number + $2 where id = $1 returning next_number - $2`, p.space.id, len(fresh)).Scan(&first); err != nil {
			return err
		}
	}
	n := first
	now := time.Now().UTC()
	for _, s := range p.sprints {
		ph := phase(s.state)
		var resolved, started, closed *time.Time
		if s.state == "closed" {
			t := now
			if s.end != nil {
				t = *s.end
			}
			resolved, closed = &t, &t
		}
		if s.state != "planned" {
			t := now
			if s.start != nil {
				t = *s.start
			}
			started = &t
		}
		if s.existing {
			if _, err := tx.Exec(ctx, `update elements set title = $2, state_id = $3, start_at = $4, due_at = $5, resolved_at = $6,
				version = version + 1, updated_by = $7, updated_at = now() where id = $1`, s.id, s.name, ph.ID, s.start, s.end, resolved, nullable(p.actor.MemberID)); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `update ext_sprint set started_at = coalesce(started_at, $2), closed_at = $3 where element_id = $1`, s.id, started, closed); err != nil {
				return err
			}
		} else {
			if _, err := tx.Exec(ctx, `insert into elements (tenant_id, id, space_id, type_id, number, title, state_id, start_at, due_at, resolved_at,
				process_version_id, created_by, updated_by) values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)`,
				s.id, p.space.id, st.ID, n, s.name, ph.ID, s.start, s.end, resolved, version, nullable(p.actor.MemberID)); err != nil {
				return err
			}
			n++
			if _, err := tx.Exec(ctx, `insert into ext_sprint (tenant_id, element_id, goal, started_at, closed_at) values (app_tenant(), $1, $2, $3, $4)`,
				s.id, s.goal, started, closed); err != nil {
				return err
			}
			if err := engine.AddActivity(ctx, tx, s.id, p.actor.MemberID, "import", "created", map[string]any{"source": p.system, "sprint": s.name, "run_id": runID}, nil); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `insert into import_origins (tenant_id, system, kind, source_key, target_id, run_id, last_run_id)
			values (app_tenant(), $1, 'element', $2, $3, $4, $4)
			on conflict (tenant_id, system, kind, source_key) do update set target_id = excluded.target_id, last_run_id = excluded.last_run_id,
			  run_id = case when import_origins.target_id = excluded.target_id then import_origins.run_id else excluded.run_id end, written_at = now()`,
			p.system, s.key, s.id, runID); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------
// Elementi

func uid(s string) pgtype.UUID {
	var u pgtype.UUID
	if len(s) != 36 {
		return u
	}
	b, err := hex.DecodeString(strings.ReplaceAll(s, "-", ""))
	if err != nil || len(b) != 16 {
		return u
	}
	copy(u.Bytes[:], b)
	u.Valid = true
	return u
}

func uidp(s *string) pgtype.UUID {
	if s == nil || *s == "" {
		return pgtype.UUID{}
	}
	return uid(*s)
}

func tsp(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

// writeIssues scrive un lotto di elementi (nuovi e già importati) con i valori dei campi,
// la voce della storia, le chiavi d'origine e lo sprint.
func (p *plan) writeIssues(ctx context.Context, tx pgx.Tx, runID string, batch []*pIssue, rec *recorder) error {
	if err := importMode(ctx, tx); err != nil {
		return err
	}
	var fresh, old []*pIssue
	for _, pi := range batch {
		if pi.existing {
			old = append(old, pi)
		} else {
			fresh = append(fresh, pi)
		}
	}
	actor := uid(p.actor.MemberID)
	// Numeri: un blocco per il lotto (quelli d'origine sono già fissati).
	need := 0
	for _, pi := range fresh {
		if pi.number == 0 {
			need++
		}
	}
	if need > 0 {
		var first int64
		if err := audit.LockChain(ctx, tx); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `update spaces set next_number = next_number + $2 where id = $1 returning next_number - $2`, p.space.id, need).Scan(&first); err != nil {
			return err
		}
		for _, pi := range fresh {
			if pi.number == 0 {
				pi.number = first
				first++
			}
		}
	}
	// Valori già presenti negli elementi da aggiornare: i campi non importati restano.
	if len(old) > 0 {
		ids := make([]string, len(old))
		for i, pi := range old {
			ids[i] = pi.id
		}
		rows, err := tx.Query(ctx, `select id::text, fields from elements where id = any($1::uuid[]) for update`, ids)
		if err != nil {
			return err
		}
		cur := map[string]map[string]any{}
		var id string
		var f map[string]any
		if _, err := pgx.ForEachRow(rows, []any{&id, &f}, func() error { cur[id] = f; return nil }); err != nil {
			return err
		}
		for _, pi := range old {
			merged := map[string]any{}
			for k, v := range cur[pi.id] {
				merged[k] = v
			}
			for k, v := range pi.fields {
				merged[k] = v
			}
			pi.fields = merged
		}
	}
	if _, err := tx.Exec(ctx, `create temp table if not exists imp_el (id uuid, type_id uuid, number bigint, title text, body_text text,
		state_id uuid, assignee_id uuid, reporter_id uuid, parent_id uuid, start_at timestamptz, due_at timestamptz, resolved_at timestamptz,
		fields jsonb, created_at timestamptz, updated_at timestamptz, process_version_id uuid) on commit drop;
		create temp table if not exists imp_val (element_id uuid, field_id uuid, ord smallint, v_text text, v_num numeric, v_ts timestamptz, v_ref uuid) on commit drop;
		truncate imp_el, imp_val`); err != nil {
		return err
	}
	var vals [][]any
	addVals := func(pi *pIssue) {
		for _, v := range engine.IndexValues(p.types[pi.typ].defs, pi.fields) {
			vals = append(vals, []any{uid(pi.id), uid(v.FieldID), int16(v.Ord), v.Text, v.Num, tsp(v.TS), uidp(v.Ref)})
		}
	}
	if len(fresh) > 0 {
		rows := make([][]any, len(fresh))
		for i, pi := range fresh {
			tt := p.types[pi.typ]
			var pv pgtype.UUID
			if tt.info.Process != nil {
				pv = uid(tt.info.Process.VersionID)
			}
			rows[i] = []any{uid(pi.id), uid(tt.info.ID), pi.number, pi.title, pi.body, uid(pi.state.ID), uidp(pi.assignee), uidp(pi.reporter),
				uidp(pi.parentID), tsp(pi.start), tsp(pi.due), tsp(pi.resolved), pi.fields, pi.created, pi.updated, pv}
			addVals(pi)
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"imp_el"}, []string{"id", "type_id", "number", "title", "body_text", "state_id",
			"assignee_id", "reporter_id", "parent_id", "start_at", "due_at", "resolved_at", "fields", "created_at", "updated_at", "process_version_id"},
			pgx.CopyFromRows(rows)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into elements (tenant_id, id, space_id, type_id, number, title, body_text, state_id, assignee_id,
			reporter_id, parent_id, start_at, due_at, resolved_at, fields, created_by, updated_by, created_at, updated_at, process_version_id)
			select app_tenant(), id, $1, type_id, number, title, body_text, state_id, assignee_id, reporter_id, parent_id,
			       start_at, due_at, resolved_at, fields, $2, $2, created_at, updated_at, process_version_id
			from imp_el`, p.space.id, actor); err != nil {
			return err
		}
	}
	if len(old) > 0 {
		rows := make([][]any, len(old))
		var delIDs []string
		for i, pi := range old {
			rows[i] = []any{uid(pi.id), uid(p.types[pi.typ].info.ID), int64(0), pi.title, pi.body, uid(pi.state.ID), uidp(pi.assignee), uidp(pi.reporter),
				uidp(pi.parentID), tsp(pi.start), tsp(pi.due), tsp(pi.resolved), pi.fields, pi.created, pi.updated, pgtype.UUID{}}
			delIDs = append(delIDs, pi.id)
			addVals(pi)
		}
		if _, err := tx.Exec(ctx, `truncate imp_el`); err != nil {
			return err
		}
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"imp_el"}, []string{"id", "type_id", "number", "title", "body_text", "state_id",
			"assignee_id", "reporter_id", "parent_id", "start_at", "due_at", "resolved_at", "fields", "created_at", "updated_at", "process_version_id"},
			pgx.CopyFromRows(rows)); err != nil {
			return err
		}
		rs, err := tx.Query(ctx, `update elements e set title = x.title, body_text = x.body_text, state_id = x.state_id, assignee_id = x.assignee_id,
			reporter_id = coalesce(x.reporter_id, e.reporter_id), parent_id = coalesce(x.parent_id, e.parent_id),
			start_at = coalesce(x.start_at, e.start_at), due_at = coalesce(x.due_at, e.due_at), resolved_at = x.resolved_at,
			fields = x.fields, updated_at = x.updated_at, version = e.version + 1, updated_by = $1
			from imp_el x where e.id = x.id returning e.id::text, e.version`, actor)
		if err != nil {
			return err
		}
		versions := map[string]int{}
		var id string
		var v int
		if _, err := pgx.ForEachRow(rs, []any{&id, &v}, func() error { versions[id] = v; return nil }); err != nil {
			return err
		}
		for _, pi := range old {
			pi.version = versions[pi.id]
		}
		// L'indice tipizzato si riscrive per i campi dei loro tipi.
		if _, err := tx.Exec(ctx, `delete from element_values v using elements e, type_fields tf
			where e.id = any($1::uuid[]) and v.element_id = e.id and tf.type_id = e.type_id and tf.space_id is null and v.field_id = tf.field_id`, delIDs); err != nil {
			return err
		}
	}
	if len(vals) > 0 {
		if _, err := tx.CopyFrom(ctx, pgx.Identifier{"imp_val"}, []string{"element_id", "field_id", "ord", "v_text", "v_num", "v_ts", "v_ref"},
			pgx.CopyFromRows(vals)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into element_values (tenant_id, element_id, field_id, ord, v_text, v_num, v_ts, v_ref)
			select app_tenant(), element_id, field_id, ord, v_text, v_num, v_ts, v_ref from imp_val
			on conflict (tenant_id, element_id, field_id, ord) do update set v_text = excluded.v_text, v_num = excluded.v_num, v_ts = excluded.v_ts, v_ref = excluded.v_ref`); err != nil {
			return err
		}
	}
	// Storia (una voce per elemento: «importato da Jira, PROJ-123») e chiavi d'origine.
	var aIDs, aKinds, aAdded, oKeys, oIDs []string
	var oVers []int32
	for _, pi := range batch {
		note := map[string]any{"source": p.system, "key": pi.src.Key, "run_id": runID, "status": pi.src.Status}
		for k, v := range pi.userNote {
			note[k] = v
		}
		kind := "created"
		if pi.existing {
			kind = "updated"
		}
		aIDs, aKinds, aAdded = append(aIDs, pi.id), append(aKinds, kind), append(aAdded, string(jsonb(note)))
		oKeys, oIDs = append(oKeys, pi.src.Key), append(oIDs, pi.id)
		v := int32(1)
		if pi.existing {
			v = int32(pi.version)
		}
		oVers = append(oVers, v)
	}
	if _, err := tx.Exec(ctx, `insert into activities (tenant_id, element_id, actor_id, kind, field_key, added)
		select app_tenant(), e, $1, 'import', k, a::jsonb from unnest($2::uuid[], $3::text[], $4::text[]) as x(e, k, a)`,
		nullable(p.actor.MemberID), aIDs, aKinds, aAdded); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `insert into import_origins (tenant_id, system, kind, source_key, target_id, run_id, last_run_id, written_version, written_at)
		select app_tenant(), $1, 'element', k, t, $2, $2, v, now() from unnest($3::text[], $4::uuid[], $5::int[]) as x(k, t, v)
		on conflict (tenant_id, system, kind, source_key) do update set last_run_id = excluded.last_run_id, written_version = excluded.written_version,
		  written_at = now(), run_id = case when import_origins.target_id = excluded.target_id then import_origins.run_id else excluded.run_id end,
		  target_id = excluded.target_id`,
		p.system, runID, oKeys, oIDs, oVers); err != nil {
		return err
	}
	// Sprint: al massimo uno per elemento (relazione «pianificato in»).
	if planned, ok := p.relTypes[work.RelPlannedIn]; ok {
		var src, dst, touched []string
		for _, pi := range batch {
			if pi.existing && pi.sprint >= 0 {
				touched = append(touched, pi.id)
			}
			if pi.sprint >= 0 {
				src, dst = append(src, pi.id), append(dst, p.sprints[pi.sprint].id)
			}
		}
		if len(touched) > 0 {
			if _, err := tx.Exec(ctx, `delete from relations where type_id = $1 and source_id = any($2::uuid[])`, planned, touched); err != nil {
				return err
			}
		}
		if len(src) > 0 {
			rows, err := tx.Query(ctx, `insert into relations (tenant_id, id, type_id, source_id, target_id, created_by)
				select app_tenant(), gen_random_uuid(), $1, s, t, $2 from unnest($3::uuid[], $4::uuid[]) as x(s, t)
				on conflict do nothing returning id::text`, planned, nullable(p.actor.MemberID), src, dst)
			if err != nil {
				return err
			}
			ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				return err
			}
			for _, id := range ids {
				rec.add("relation", id)
			}
		}
	}
	return rec.flush(ctx, tx, runID)
}

// writeLinks scrive le relazioni tra elementi (blocca, duplica, collegato a…).
func (p *plan) writeLinks(ctx context.Context, tx pgx.Tx, runID string, links []pLink, rec *recorder) (int, error) {
	var types, src, dst []string
	for _, l := range links {
		t, ok := p.relTypes[l.relType]
		if !ok {
			continue
		}
		a, b := p.linkEnd(l.source, l.sourceID), p.linkEnd(l.target, l.target2)
		if a == "" || b == "" || a == b {
			continue
		}
		types, src, dst = append(types, t), append(src, a), append(dst, b)
	}
	if len(src) == 0 {
		return 0, nil
	}
	rows, err := tx.Query(ctx, `insert into relations (tenant_id, id, type_id, source_id, target_id, created_by)
		select app_tenant(), gen_random_uuid(), ty, s, t, $1 from unnest($2::uuid[], $3::uuid[], $4::uuid[]) as x(ty, s, t)
		where not exists (select 1 from relations r where r.type_id = x.ty and r.source_id = x.t and r.target_id = x.s
		                  and not (select directed from relation_types where id = x.ty))
		on conflict do nothing returning id::text`, nullable(p.actor.MemberID), types, src, dst)
	if err != nil {
		return 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		rec.add("relation", id)
	}
	return len(ids), rec.flush(ctx, tx, runID)
}

type commentRow struct {
	pi *pIssue
	c  pComment
}

// writeComments scrive i commenti con autore e data originali.
func (p *plan) writeComments(ctx context.Context, tx pgx.Tx, runID string, list []commentRow) error {
	if len(list) == 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `create temp table if not exists imp_cm (id uuid, element_id uuid, author_id uuid, body_text text, created_at timestamptz) on commit drop;
		truncate imp_cm`); err != nil {
		return err
	}
	rows := make([][]any, len(list))
	keys, ids := make([]string, len(list)), make([]string, len(list))
	for i, r := range list {
		id := db.NewID()
		author := r.c.author
		if author == nil {
			author = &p.holderID
		}
		rows[i] = []any{uid(id), uid(r.pi.id), uidp(author), r.c.body, r.c.created}
		keys[i], ids[i] = r.c.key, id
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"imp_cm"}, []string{"id", "element_id", "author_id", "body_text", "created_at"}, pgx.CopyFromRows(rows)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `insert into comments (tenant_id, id, element_id, author_id, body, body_text, created_at, updated_at)
		select app_tenant(), id, element_id, author_id, '{"format": "markdown", "mentions": []}'::jsonb, body_text, created_at, created_at from imp_cm`); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `insert into import_origins (tenant_id, system, kind, source_key, target_id, run_id, last_run_id)
		select app_tenant(), $1, 'comment', k, t, $2, $2 from unnest($3::text[], $4::uuid[]) as x(k, t)
		on conflict (tenant_id, system, kind, source_key) do update set target_id = excluded.target_id, run_id = excluded.run_id, last_run_id = excluded.last_run_id`,
		p.system, runID, keys, ids)
	return err
}

type worklogRow struct {
	pi *pIssue
	w  pWorklog
}

// writeWorklogs scrive le ore in work_logs; restituisce gli id per il calcolo dei costi.
func (p *plan) writeWorklogs(ctx context.Context, tx pgx.Tx, runID string, list []worklogRow) ([]string, error) {
	if len(list) == 0 {
		return nil, nil
	}
	n := len(list)
	ids, els, members, texts, keys := make([]string, n), make([]string, n), make([]string, n), make([]*string, n), make([]string, n)
	days := make([]time.Time, n)
	mins := make([]int32, n)
	for i, r := range list {
		ids[i], els[i], keys[i] = db.NewID(), r.pi.id, r.w.key
		m := r.w.member
		if m == nil {
			m = &p.holderID
		}
		members[i] = *m
		days[i] = r.w.day
		mins[i] = int32(math.Min(float64(r.w.minutes), 1_000_000))
		if t := strings.TrimSpace(r.w.text); t != "" {
			texts[i] = &t
		}
	}
	if _, err := tx.Exec(ctx, `insert into work_logs (tenant_id, id, element_id, member_id, work_date, minutes, activity_type, text, created_by)
		select app_tenant(), id, e, m, d, mi, 'work', tx, $1 from unnest($2::uuid[], $3::uuid[], $4::uuid[], $5::date[], $6::int[], $7::text[]) as x(id, e, m, d, mi, tx)`,
		nullable(p.actor.MemberID), ids, els, members, days, mins, texts); err != nil {
		return nil, err
	}
	_, err := tx.Exec(ctx, `insert into import_origins (tenant_id, system, kind, source_key, target_id, run_id, last_run_id)
		select app_tenant(), $1, 'work_log', k, t, $2, $2 from unnest($3::text[], $4::uuid[]) as x(k, t)
		on conflict (tenant_id, system, kind, source_key) do update set target_id = excluded.target_id, run_id = excluded.run_id, last_run_id = excluded.last_run_id`,
		p.system, runID, keys, ids)
	return ids, err
}

var errTrial = errors.New("prova: nessuna modifica salvata")
