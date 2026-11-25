package knowledge

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Collegamenti nel grafo: una pagina «documenta» richieste, applicazioni, asset e qualunque
// altro elemento (relazione del motore), e cita elementi nel testo (kb_refs). Il dettaglio di
// quegli elementi mostra i «Documenti collegati».

// Link collega la pagina a un elemento (relazione «documenta»).
func (s *Service) Link(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, target string) (*PageView, error) {
	el, sp, _, err := s.loadPage(ctx, tx, actor, ref, false)
	if err != nil {
		return nil, err
	}
	if !sp.Rights.Write {
		return nil, engine.ErrForbidden
	}
	t, err := s.eng.Get(ctx, tx, actor, strings.TrimSpace(target))
	if err != nil {
		return nil, engine.NotFound("element", "element", "elemento %q inesistente o non visibile", target)
	}
	if t.ID == el.ID {
		return nil, engine.InvalidCode("validation.knowledge.page_self_link", "element", "una pagina non documenta sé stessa")
	}
	tag, err := tx.Exec(ctx, `insert into relations (tenant_id, id, type_id, source_id, target_id, created_by)
		select app_tenant(), $1, rt.id, $3, $4, $5 from relation_types rt where rt.key = $2
		on conflict do nothing`, db.NewID(), RelDocuments, el.ID, t.ID, nullable(actor.MemberID))
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() > 0 {
		for _, id := range []string{el.ID, t.ID} {
			if err := engine.AddActivity(ctx, tx, id, actor.MemberID, "relation", RelDocuments, map[string]any{"source": el.Key, "target": t.Key}, nil); err != nil {
				return nil, err
			}
		}
		if err := audit.Append(ctx, tx, audit.Event{Action: "page.link", EntityType: TypePage, EntityID: el.ID,
			ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"target_id": t.ID}}); err != nil {
			return nil, err
		}
	}
	return s.GetPage(ctx, tx, actor, el.ID)
}

// Unlink toglie il collegamento.
func (s *Service) Unlink(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, target string) (*PageView, error) {
	el, sp, _, err := s.loadPage(ctx, tx, actor, ref, false)
	if err != nil {
		return nil, err
	}
	if !sp.Rights.Write {
		return nil, engine.ErrForbidden
	}
	tag, err := tx.Exec(ctx, `delete from relations r using relation_types rt, elements e, spaces sp
		where rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = $2 and r.source_id = $1
		  and e.tenant_id = r.tenant_id and e.id = r.target_id and sp.tenant_id = e.tenant_id and sp.id = e.space_id
		  and (e.id::text = $3 or sp.key || '-' || e.number = upper($3))`, el.ID, RelDocuments, strings.TrimSpace(target))
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() > 0 {
		if err := audit.Append(ctx, tx, audit.Event{Action: "page.unlink", EntityType: TypePage, EntityID: el.ID,
			ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"target": target}}); err != nil {
			return nil, err
		}
	}
	return s.GetPage(ctx, tx, actor, el.ID)
}

// Document è una pagina collegata a un elemento.
type Document struct {
	Key             string    `json:"key"`
	Title           string    `json:"title"`
	SpaceKey        string    `json:"space_key"`
	SpaceName       string    `json:"space_name"`
	State           string    `json:"state"`
	StateCategory   string    `json:"state_category"`
	Controlled      bool      `json:"controlled"`
	ApprovedVersion *int      `json:"approved_version"`
	Via             string    `json:"via"` // link (documenta) | ref (citato nel testo)
	UpdatedAt       time.Time `json:"updated_at"`
}

// Documents elenca le pagine che documentano l'elemento o lo citano, che l'attore può leggere.
func (s *Service) Documents(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) ([]Document, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	spaces, err := s.Spaces(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, sp := range spaces {
		if sp.Rights.Read {
			ids = append(ids, sp.ID)
		}
	}
	out := []Document{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, `
		with src as (
			select r.source_id as page_id, 'link' as via from relations r
			join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = $2
			where r.target_id = $1
			union all
			select page_id, 'ref' from kb_refs where target_id = $1
		)
		select distinct on (e.id) s.key || '-' || e.number, e.title, s.key, s.name, coalesce(o.name, ''), coalesce(o.category, ''),
		       coalesce(k.controlled, false), k.approved_version, src.via, e.updated_at
		from src
		join elements e on e.id = src.page_id and e.deleted_at is null and not e.is_draft
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		left join kb_pages k on k.tenant_id = e.tenant_id and k.element_id = e.id
		where e.space_id = any($3::uuid[])
		order by e.id, src.via limit 200`, el.ID, RelDocuments, ids)
	if err != nil {
		return nil, err
	}
	out, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Document])
	if out == nil {
		out = []Document{}
	}
	return out, err
}

// RefInfo descrive un elemento citato in una pagina, per mostrarne lo stato accanto alla chiave.
type RefInfo struct {
	Key           string `json:"key"`
	Title         string `json:"title"`
	Type          string `json:"type"`
	TypeName      string `json:"type_name"`
	State         string `json:"state"`
	StateCategory string `json:"state_category"`
}

// Refs restituisce titolo e stato degli elementi citati che l'attore può leggere (massimo 200).
func (s *Service) Refs(ctx context.Context, tx pgx.Tx, actor engine.Actor, keys []string) ([]RefInfo, error) {
	out := []RefInfo{}
	seen := map[string]bool{}
	for _, k := range keys {
		k = strings.ToUpper(strings.TrimSpace(k))
		if k == "" || seen[k] || !keyRe.MatchString(k) || len(seen) >= 200 {
			continue
		}
		seen[k] = true
		el, err := s.eng.Get(ctx, tx, actor, k)
		if errors.Is(err, engine.ErrNotFound) {
			continue // inesistente o non visibile: resta testo semplice
		}
		if err != nil {
			return nil, err
		}
		out = append(out, RefInfo{Key: el.Key, Title: el.Title, Type: el.TypeKey, TypeName: el.TypeName, State: el.State, StateCategory: el.StateCategory})
	}
	return out, nil
}
