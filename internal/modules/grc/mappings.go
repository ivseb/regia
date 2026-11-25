package grc

import (
	"context"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/db"
)

// Corrispondenze tra requisiti di normative diverse (requirement_mappings): servono a
// orientarsi ("questo articolo NIS2 corrisponde a questi controlli ISO") e a proporre i
// requisiti da collegare a un controllo. Le corrispondenze del tenant hanno owner_tenant_id.

var relations = []string{"equivalent", "subset", "superset", "related"}

// RelationNames sono i nomi italiani delle relazioni.
var RelationNames = map[string]string{"equivalent": "equivalente", "subset": "sottoinsieme", "superset": "sovrainsieme", "related": "correlato"}

// MappingInput è una corrispondenza da creare: "chiave:riferimento" → "chiave:riferimento".
type MappingInput struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Relation string `json:"relation,omitempty"`
	Source   string `json:"source,omitempty"`
}

// MappingView è una corrispondenza.
type MappingView struct {
	ID        string `json:"id"`
	From      string `json:"from"`
	FromID    string `json:"from_id"`
	FromTitle string `json:"from_title"`
	To        string `json:"to"`
	ToID      string `json:"to_id"`
	ToTitle   string `json:"to_title"`
	Relation  string `json:"relation"`
	Source    string `json:"source"`
	System    bool   `json:"system"`
}

// Mappings elenca le corrispondenze tra versioni pubblicate (framework = solo quelle che lo toccano).
func (s *Service) Mappings(ctx context.Context, tx pgx.Tx, actor engine.Actor, framework string) ([]MappingView, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select m.id, ff.key || ':' || rf.ref, rf.id, rf.title, ft.key || ':' || rt.ref, rt.id, rt.title,
		m.relation, coalesce(m.source, ''), m.owner_tenant_id is null
		from requirement_mappings m
		join requirements rf on rf.id = m.from_requirement_id
		join framework_versions vf on vf.id = rf.version_id and vf.status = 'published'
		join frameworks ff on ff.id = vf.framework_id
		join requirements rt on rt.id = m.to_requirement_id
		join framework_versions vt on vt.id = rt.version_id and vt.status = 'published'
		join frameworks ft on ft.id = vt.framework_id
		where $1 = '' or ff.key = $1 or ft.key = $1
		order by ff.key, rf.ordinal, ft.key, rt.ordinal`, framework)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[MappingView])
	if out == nil {
		out = []MappingView{}
	}
	return out, err
}

// AddMapping crea una corrispondenza (grc.admin).
func (s *Service) AddMapping(ctx context.Context, tx pgx.Tx, actor engine.Actor, in MappingInput) (*MappingView, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	n, err := s.applyMappings(ctx, tx, actor, []MappingInput{in}, false)
	if err != nil {
		return nil, err
	}
	ms, err := s.Mappings(ctx, tx, actor, "")
	if err != nil {
		return nil, err
	}
	for i := range ms {
		if ms[i].From == normRef(in.From) && ms[i].To == normRef(in.To) {
			return &ms[i], nil
		}
	}
	if n == 0 {
		return nil, engine.Duplicate("to", "corrispondenza già presente")
	}
	return nil, engine.ErrNotFound
}

func normRef(s string) string {
	fw, ref, _ := SplitRef(s)
	return fw + ":" + ref
}

// applyMappings crea le corrispondenze che mancano. Con skipMissing quelle verso normative o
// riferimenti assenti si saltano (pacchetti); altrimenti sono un errore.
func (s *Service) applyMappings(ctx context.Context, tx pgx.Tx, actor engine.Actor, ms []MappingInput, skipMissing bool) (int, error) {
	if len(ms) == 0 {
		return 0, nil
	}
	refs := []string{}
	for i, m := range ms {
		if m.Relation == "" {
			ms[i].Relation = "related"
		}
		if !slices.Contains(relations, ms[i].Relation) {
			return 0, engine.InvalidValue("relation", "relazione non ammessa (%s)", strings.Join(relations, ", "))
		}
		if len(m.Source) > 200 {
			return 0, engine.InvalidCode("validation.grc.source_too_long", "source", "fonte troppo lunga")
		}
		refs = append(refs, m.From, m.To)
	}
	found, err := resolveRefs(ctx, tx, refs)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, m := range ms {
		from, okf := found[normRef(m.From)]
		to, okt := found[normRef(m.To)]
		if !okf || !okt {
			if skipMissing {
				continue
			}
			missing := m.From
			if okf {
				missing = m.To
			}
			return 0, engine.NotFound("ref", "requirement", "requisito %q inesistente nelle versioni pubblicate", missing)
		}
		if from.ID == to.ID {
			return 0, engine.InvalidCode("validation.grc.mapping_self", "to", "un requisito non corrisponde a se stesso")
		}
		var dup bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from requirement_mappings where
			(from_requirement_id = $1 and to_requirement_id = $2) or (from_requirement_id = $2 and to_requirement_id = $1))`,
			from.ID, to.ID).Scan(&dup); err != nil {
			return 0, err
		}
		if dup {
			continue
		}
		if _, err := tx.Exec(ctx, `insert into requirement_mappings (id, owner_tenant_id, from_requirement_id, to_requirement_id, relation, source)
			values ($1, app_tenant(), $2, $3, $4, $5)`, db.NewID(), from.ID, to.ID, m.Relation, nullable(m.Source)); err != nil {
			return 0, err
		}
		n++
	}
	if n > 0 {
		if err := audited(ctx, tx, actor, "grc.mapping.add", "requirement_mapping", "", map[string]any{"count": n}); err != nil {
			return 0, err
		}
	}
	return n, nil
}

// DeleteMapping toglie una corrispondenza del tenant.
func (s *Service) DeleteMapping(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) error {
	if err := requireAdmin(actor); err != nil {
		return err
	}
	if !uuidRe.MatchString(id) {
		return engine.ErrNotFound
	}
	tag, err := tx.Exec(ctx, "delete from requirement_mappings where id = $1 and owner_tenant_id = app_tenant()", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return engine.ErrNotFound
	}
	return audited(ctx, tx, actor, "grc.mapping.delete", "requirement_mapping", id, nil)
}
