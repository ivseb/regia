package grc

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Collegamenti tra i requisiti di progetto (modulo requirements, ext_requirement) e i
// requisiti normativi. Due fonti: la voce del catalogo dei requisiti da cui il requisito
// nasce (campo "regulatory") e i collegamenti a mano (grc_requirement_links). La verifica
// del requisito su un'applicazione vale come evidenza dei controlli che coprono quei
// requisiti normativi, nel perimetro di quell'applicazione.

// LinkRef è un requisito normativo collegato.
type LinkRef struct {
	Ref       string `json:"ref"` // "dora:Art. 9"
	Framework string `json:"framework"`
	ReqRef    string `json:"req_ref"`
	ID        string `json:"id,omitempty"`
	Title     string `json:"title"`
	Found     bool   `json:"found"`
}

// LinksView sono i requisiti normativi di un requisito di progetto.
type LinksView struct {
	Requirement ElementLink `json:"requirement"`
	Manual      []LinkRef   `json:"manual"`
	Catalog     []LinkRef   `json:"catalog"`
	CanEdit     bool        `json:"can_edit"`
}

func (s *Service) projectRequirement(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*engine.Element, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if el.TypeKey != "requirement" {
		return nil, engine.ErrNotFound
	}
	return el, nil
}

// RequirementLinks restituisce i collegamenti normativi di un requisito di progetto (chi
// vede il requisito vede i collegamenti).
func (s *Service) RequirementLinks(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*LinksView, error) {
	el, err := s.projectRequirement(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	out := &LinksView{Requirement: ElementLink{ID: el.ID, Key: el.Key, Title: el.Title}, Manual: []LinkRef{}, Catalog: []LinkRef{},
		CanEdit: actor.Has(PermAdmin)}
	rows, err := tx.Query(ctx, "select framework_key || ':' || ref from grc_requirement_links where requirement_element_id = $1 order by 1", el.ID)
	if err != nil {
		return nil, err
	}
	manual, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	rows, err = tx.Query(ctx, `select r.v from ext_requirement x
		join requirement_catalogs c on c.tenant_id = x.tenant_id and c.key = x.catalog_key and c.version = x.catalog_version
		cross join lateral jsonb_array_elements(c.definition -> 'items') it
		cross join lateral jsonb_array_elements_text(coalesce(it -> 'regulatory', '[]'::jsonb)) r(v)
		where x.element_id = $1 and x.origin = 'catalog' and it ->> 'key' = x.catalog_item_key`, el.ID)
	if err != nil {
		return nil, err
	}
	catalog, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	found, err := resolveRefs(ctx, tx, validRefs(append(append([]string{}, manual...), catalog...)))
	if err != nil {
		return nil, err
	}
	conv := func(list []string) []LinkRef {
		out := []LinkRef{}
		for _, r := range list {
			fw, rr, _ := SplitRef(r)
			l := LinkRef{Ref: fw + ":" + rr, Framework: fw, ReqRef: rr}
			if x, ok := found[l.Ref]; ok {
				l.ID, l.Title, l.Found = x.ID, x.Title, true
			}
			out = append(out, l)
		}
		return out
	}
	out.Manual, out.Catalog = conv(manual), conv(catalog)
	return out, nil
}

func validRefs(rs []string) []string {
	out := rs[:0:0]
	for _, r := range rs {
		if _, _, ok := SplitRef(r); ok {
			out = append(out, r)
		}
	}
	return out
}

// SetRequirementLinks sostituisce i collegamenti a mano (grc.admin). I riferimenti devono
// esistere in una versione pubblicata.
func (s *Service) SetRequirementLinks(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, refs []string) (*LinksView, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	el, err := s.projectRequirement(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	var ok bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from ext_requirement where element_id = $1)", el.ID).Scan(&ok); err != nil || !ok {
		return nil, errors.Join(err, engine.ErrNotFound)
	}
	if len(refs) > 50 {
		return nil, engine.InvalidCode("validation.grc.links_too_many", "refs", "al massimo 50 collegamenti")
	}
	found, err := resolveRefs(ctx, tx, refs)
	if err != nil {
		return nil, err
	}
	for _, r := range refs {
		if _, ok := found[normRef(r)]; !ok {
			return nil, engine.NotFound("refs", "requirement", "requisito normativo %q inesistente nelle versioni pubblicate", strings.TrimSpace(r))
		}
	}
	if _, err := tx.Exec(ctx, "delete from grc_requirement_links where requirement_element_id = $1", el.ID); err != nil {
		return nil, err
	}
	for _, r := range refs {
		fw, rr, _ := SplitRef(r)
		if _, err := tx.Exec(ctx, `insert into grc_requirement_links (tenant_id, requirement_element_id, framework_key, ref, created_by)
			values (app_tenant(), $1, $2, $3, $4) on conflict do nothing`, el.ID, fw, rr, nullable(actor.MemberID)); err != nil {
			return nil, err
		}
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "grc", "riferimenti_normativi", refs, nil); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "grc.requirement_links.set", "requirement", el.ID, map[string]any{"refs": refs}); err != nil {
		return nil, err
	}
	return s.RequirementLinks(ctx, tx, actor, el.ID)
}
