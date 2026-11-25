package work

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
)

// Assegnazione ai team (piano, passo 10; demand.md §8): quando il team accetta il perimetro
// dell'impatto nasce un epic nello spazio del team, figlio dell'impatto (che è figlio della
// richiesta), con la riga di assignments che lega impatto, team ed epic. L'avanzamento
// dell'epic (story chiuse) risale all'impatto e alla richiesta come fatto "avanzamento".

// Progress è l'avanzamento di un insieme di epic.
type Progress struct {
	Done    int     `json:"done"`    // story (figli) chiuse
	Total   int     `json:"total"`   // story non annullate
	Percent float64 `json:"percent"` // 0..100, una cifra decimale
	Epics   int     `json:"epics"`
	Open    int     `json:"open_epics"`
}

// progressOf calcola l'avanzamento degli epic indicati: figli chiusi su figli non annullati.
// Un epic senza figli conta come una unità, chiusa se l'epic è chiuso. Gli epic annullati
// non contano.
func progressOf(ctx context.Context, tx pgx.Tx, epicIDs []string) (Progress, error) {
	var p Progress
	if len(epicIDs) == 0 {
		return p, nil
	}
	rows, err := tx.Query(ctx, `
		select coalesce(eo.category, ''),
		       count(c.id) filter (where co.category = 'done')::int,
		       count(c.id) filter (where coalesce(co.category, '') <> 'cancelled')::int
		from elements ep
		left join options eo on eo.tenant_id = ep.tenant_id and eo.id = ep.state_id
		left join elements c on c.tenant_id = ep.tenant_id and c.parent_id = ep.id and c.deleted_at is null
		left join options co on co.tenant_id = c.tenant_id and co.id = c.state_id
		where ep.id = any($1::uuid[]) and ep.deleted_at is null
		group by ep.id, eo.category`, epicIDs)
	if err != nil {
		return p, err
	}
	var cat string
	var done, total int
	_, err = pgx.ForEachRow(rows, []any{&cat, &done, &total}, func() error {
		if cat == "cancelled" {
			return nil
		}
		p.Epics++
		if cat != "done" {
			p.Open++
		}
		if total == 0 {
			total = 1
			if cat == "done" {
				done = 1
			}
		}
		p.Done += done
		p.Total += total
		return nil
	})
	if p.Total > 0 {
		p.Percent = math.Round(float64(p.Done)*1000/float64(p.Total)) / 10
	}
	return p, err
}

// epicsOf restituisce gli epic figli di un impatto, o degli impatti di una richiesta.
func epicsOf(ctx context.Context, tx pgx.Tx, el *engine.Element) ([]string, error) {
	var sql string
	switch el.TypeKey {
	case ImpactType:
		sql = `select ep.id::text from elements ep join element_types t on t.tenant_id = ep.tenant_id and t.id = ep.type_id and t.key = 'epic'
			where ep.parent_id = $1 and ep.deleted_at is null`
	case RequestType:
		sql = `select ep.id::text from ext_impact x
			join elements ep on ep.tenant_id = x.tenant_id and ep.parent_id = x.element_id and ep.deleted_at is null
			join element_types t on t.tenant_id = ep.tenant_id and t.id = ep.type_id and t.key = 'epic'
			where x.request_id = $1`
	default:
		return nil, nil
	}
	rows, err := tx.Query(ctx, sql, el.ID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// deliveryFacts: avanzamento degli epic dei team, sull'impatto e sulla richiesta. Senza epic
// i fatti mancano ("avanzamento: assente"): un gate può chiedere "avanzamento: 100".
func (s *Service) deliveryFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	ids, err := epicsOf(ctx, tx, el)
	if err != nil || len(ids) == 0 {
		return err
	}
	p, err := progressOf(ctx, tx, ids)
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `select s.key || '-' || e.number from elements e join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		where e.id = any($1::uuid[]) order by s.key, e.number`, ids)
	if err != nil {
		return err
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	vals := make([]any, len(keys))
	for i, k := range keys {
		vals[i] = k
	}
	f.Set("epic", vals...)
	f.Set("avanzamento", p.Percent)
	f.Set("epic_aperti", p.Open)
	return nil
}

// ---------------------------------------------------------------------------
// Azione create_epic

func (s *Service) actionCreateEpic(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, _ engine.Action) (string, error) {
	switch el.TypeKey {
	case ImpactType:
		ep, created, err := s.ensureEpic(ctx, tx, actor, el.ID)
		if err != nil {
			var ve *engine.ValidationError
			if errors.As(err, &ve) {
				return "epic non creato: " + ve.Msg, nil // resta nella storia, il passaggio non si blocca
			}
			return "", err
		}
		if !created {
			return "", engine.ErrNoEffect
		}
		return "epic " + ep.Key + " nello spazio " + ep.SpaceKey, nil
	case RequestType:
		rows, err := tx.Query(ctx, `select x.element_id::text from ext_impact x
			join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
			left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
			where x.request_id = $1 and x.team_id is not null and coalesce(o.category, '') <> 'cancelled'
			order by e.number`, el.ID)
		if err != nil {
			return "", err
		}
		impacts, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return "", err
		}
		var made, skipped []string
		for _, id := range impacts {
			ep, created, err := s.ensureEpic(ctx, tx, actor, id)
			var ve *engine.ValidationError
			if errors.As(err, &ve) {
				skipped = append(skipped, ve.Msg)
				continue
			}
			if err != nil {
				return "", err
			}
			if created {
				made = append(made, ep.Key)
			}
		}
		if len(made) == 0 && len(skipped) == 0 {
			return "", engine.ErrNoEffect
		}
		note := ""
		if len(made) > 0 {
			note = "epic creati: " + strings.Join(made, ", ")
		}
		if len(skipped) > 0 {
			if note != "" {
				note += "; "
			}
			note += "non creati: " + strings.Join(skipped, "; ")
		}
		return note, nil
	default:
		return "azione create_epic valida solo su impatti e richieste", nil
	}
}

// CreateEpicFor crea (se manca) l'epic di un impatto: per i processi senza la regola o per
// gli impatti accettati prima del work management. Serve il referente del team sull'impatto,
// un ruolo di analisi o element.write.
func (s *Service) CreateEpicFor(ctx context.Context, tx pgx.Tx, actor engine.Actor, impactRef string) (*engine.Element, error) {
	imp, err := s.eng.Get(ctx, tx, actor, impactRef)
	if err != nil {
		return nil, err
	}
	if imp.TypeKey != ImpactType {
		return nil, engine.InvalidCode("validation.work.epic_from_impact", "impact", "l'epic si crea da un impatto")
	}
	if !actor.Has("element.write") {
		ok, err := engine.HasRole(ctx, tx, imp.ID, actor.MemberID, []string{"team_lead", "technical_analyst", "demand_manager", "pmo"})
		if err != nil || !ok {
			return nil, firstErr(err, engine.ErrForbidden)
		}
	}
	ep, created, err := s.ensureEpic(ctx, tx, actor, imp.ID)
	if err != nil {
		return nil, err
	}
	if created {
		if err := engine.AddActivity(ctx, tx, imp.ID, actor.MemberID, "work", "epic", map[string]any{"epic": ep.Key}, nil); err != nil {
			return nil, err
		}
	}
	return ep, nil
}

// ensureEpic crea l'epic dell'impatto nello spazio del team owner (creando lo spazio se
// manca) e l'assegnazione al team. Se c'è già, lo restituisce con created = false.
func (s *Service) ensureEpic(ctx context.Context, tx pgx.Tx, actor engine.Actor, impactID string) (*engine.Element, bool, error) {
	sys := engine.Actor{MemberID: actor.MemberID, AccountID: actor.AccountID, IP: actor.IP, Permissions: []string{"element.read"}}
	var title, reqKey, reqTitle, app, note string
	var teamID, team, lead *string
	var effort *float64
	var cost *int64
	err := tx.QueryRow(ctx, `
		select e.title, rs.key || '-' || r.number, r.title, ap.title, x.technical_note, x.team_id, t.name, t.lead_id,
		       x.estimate_effort_days::float8, x.estimate_cost_cents
		from ext_impact x
		join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id
		join elements r on r.tenant_id = x.tenant_id and r.id = x.request_id
		join spaces rs on rs.tenant_id = r.tenant_id and rs.id = r.space_id
		join elements ap on ap.tenant_id = x.tenant_id and ap.id = x.application_id
		left join teams t on t.tenant_id = x.tenant_id and t.id = x.team_id
		where x.element_id = $1`, impactID).Scan(&title, &reqKey, &reqTitle, &app, &note, &teamID, &team, &lead, &effort, &cost)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, engine.NotFound("impact", "", "impatto inesistente")
	}
	if err != nil {
		return nil, false, err
	}
	if teamID == nil {
		return nil, false, engine.InvalidCode("validation.work.impact_no_team", "team", "l'impatto non ha un team owner")
	}
	// Già creato? (assegnazione attiva con l'epic, oppure un epic figlio dell'impatto)
	var existing *string
	if err := tx.QueryRow(ctx, `select coalesce(
			(select delivery_element_id::text from assignments where element_id = $1 and team_id = $2
			   and status not in ('rejected', 'cancelled') and delivery_element_id is not null limit 1),
			(select ep.id::text from elements ep join element_types t on t.tenant_id = ep.tenant_id and t.id = ep.type_id and t.key = 'epic'
			   where ep.parent_id = $1 and ep.deleted_at is null limit 1))`, impactID, *teamID).Scan(&existing); err != nil {
		return nil, false, err
	}
	if existing != nil {
		ep, err := s.eng.Get(ctx, tx, sys, *existing)
		return ep, false, err
	}
	spaceID, err := EnsureTeamSpace(ctx, tx, actor, SpaceInput{TeamID: *teamID})
	if err != nil {
		return nil, false, err
	}
	var spaceKey string
	if err := tx.QueryRow(ctx, "select key from spaces where id = $1", spaceID).Scan(&spaceKey); err != nil {
		return nil, false, err
	}
	body := fmt.Sprintf("Perimetro del team %s sulla richiesta %s «%s», applicazione %s.", deref(team), reqKey, reqTitle, app)
	if strings.TrimSpace(note) != "" {
		body += "\n\n" + note
	}
	if effort != nil {
		body += fmt.Sprintf("\n\nStima dell'analisi: %s giornate.", strings.TrimSuffix(strings.TrimSuffix(fmt.Sprintf("%.1f", *effort), "0"), "."))
	}
	epTitle := reqTitle + " · " + app
	if len(epTitle) > 500 {
		epTitle = epTitle[:500]
	}
	ep, err := s.eng.Create(ctx, tx, actor, engine.CreateInput{SpaceKey: spaceKey, TypeKey: TypeEpic, Title: epTitle,
		BodyText: body, AssigneeID: deref(lead), ParentID: impactID})
	if err != nil {
		return nil, false, err
	}
	if err := fillRanks(ctx, tx, spaceID); err != nil {
		return nil, false, err
	}
	var days *float64
	if effort != nil {
		days = effort
	}
	if _, err := tx.Exec(ctx, `insert into assignments (tenant_id, element_id, team_id, status, scope_note, estimate_effort_days,
		estimate_cost_cents, delivery_element_id, responded_by, responded_at)
		values (app_tenant(), $1, $2, 'accepted', $3, $4, $5, $6, $7, now())
		on conflict (tenant_id, element_id, team_id) where status not in ('rejected','cancelled')
		do update set delivery_element_id = excluded.delivery_element_id, status = 'accepted'`,
		impactID, *teamID, note, days, cost, ep.ID, nullable(actor.MemberID)); err != nil {
		return nil, false, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "assignment.accept", EntityType: "assignment", EntityID: impactID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"team_id": *teamID, "epic_id": ep.ID}}); err != nil {
		return nil, false, err
	}
	return ep, true, nil
}

// afterTransition tiene allineato lo stato dell'assegnazione con quello dell'epic.
func (s *Service) afterTransition(ctx context.Context, tx pgx.Tx, ev engine.TransitionEvent) error {
	if ev.Element.TypeKey != TypeEpic {
		return nil
	}
	status := map[string]string{"todo": "accepted", "in_progress": "active", "done": "done", "cancelled": "cancelled"}[ev.Element.StateCategory]
	if status == "" {
		return nil
	}
	_, err := tx.Exec(ctx, `update assignments set status = $2 where delivery_element_id = $1 and status <> $2`, ev.Element.ID, status)
	return err
}

// ---------------------------------------------------------------------------
// Vista per il dettaglio

// EpicView è un epic di un team con il suo avanzamento.
type EpicView struct {
	ID            string   `json:"id"`
	Key           string   `json:"key"`
	Title         string   `json:"title"`
	State         string   `json:"state"`
	StateCategory string   `json:"state_category"`
	SpaceKey      string   `json:"space_key"`
	Team          *string  `json:"team"`
	ImpactKey     *string  `json:"impact_key"`
	Application   *string  `json:"application"`
	Assignee      *string  `json:"assignee"`
	Progress      Progress `json:"progress"`
}

// Delivery è ciò che il dettaglio di un elemento mostra del lavoro dei team.
type Delivery struct {
	Kind     string     `json:"kind"` // request | impact | epic | item | sprint | none
	Epics    []EpicView `json:"epics"`
	Progress *Progress  `json:"progress,omitempty"`
	// CanCreateEpic: impatto accettato senza epic, e l'attore può crearlo.
	CanCreateEpic bool     `json:"can_create_epic"`
	Sprint        *Ref     `json:"sprint,omitempty"`
	Space         *Ref     `json:"space,omitempty"`
	Points        *float64 `json:"points,omitempty"`
}

// Ref è un riferimento leggibile.
type Ref struct {
	Key   string `json:"key"`
	Title string `json:"title"`
	State string `json:"state,omitempty"`
}

// DeliveryOf restituisce epic e avanzamento per richiesta e impatto, avanzamento per l'epic,
// sprint per story, task e bug.
func (s *Service) DeliveryOf(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*Delivery, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	out := &Delivery{Kind: "none", Epics: []EpicView{}}
	switch el.TypeKey {
	case RequestType, ImpactType:
		out.Kind = map[string]string{RequestType: "request", ImpactType: "impact"}[el.TypeKey]
		ids, err := epicsOf(ctx, tx, el)
		if err != nil {
			return nil, err
		}
		if out.Epics, err = epicViews(ctx, tx, ids); err != nil {
			return nil, err
		}
		if len(ids) > 0 {
			p, err := progressOf(ctx, tx, ids)
			if err != nil {
				return nil, err
			}
			out.Progress = &p
		}
		if el.TypeKey == ImpactType && len(ids) == 0 && (el.StateCategory == "in_progress" || el.StateCategory == "done") {
			if actor.Has("element.write") {
				out.CanCreateEpic = true
			} else if ok, err := engine.HasRole(ctx, tx, el.ID, actor.MemberID, []string{"team_lead", "technical_analyst", "demand_manager", "pmo"}); err != nil {
				return nil, err
			} else {
				out.CanCreateEpic = ok
			}
		}
	case TypeEpic:
		out.Kind = "epic"
		if out.Epics, err = epicViews(ctx, tx, []string{el.ID}); err != nil {
			return nil, err
		}
		p, err := progressOf(ctx, tx, []string{el.ID})
		if err != nil {
			return nil, err
		}
		out.Progress = &p
	case TypeStory, TypeTask, TypeBug:
		out.Kind = "item"
		var sp Ref
		err := tx.QueryRow(ctx, `select ss.key || '-' || e.number, e.title, coalesce(o.name, '') from relations r
			join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = 'planned_in'
			join elements e on e.tenant_id = r.tenant_id and e.id = r.target_id
			join spaces ss on ss.tenant_id = e.tenant_id and ss.id = e.space_id
			left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
			where r.source_id = $1 limit 1`, el.ID).Scan(&sp.Key, &sp.Title, &sp.State)
		if err == nil {
			out.Sprint = &sp
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	case TypeSprint:
		out.Kind = "sprint"
	}
	if out.Kind != "none" && out.Kind != "request" && out.Kind != "impact" {
		out.Space = &Ref{Key: el.SpaceKey}
		_ = tx.QueryRow(ctx, "select name from spaces where id = $1", el.SpaceID).Scan(&out.Space.Title)
	}
	return out, nil
}

func epicViews(ctx context.Context, tx pgx.Tx, ids []string) ([]EpicView, error) {
	if len(ids) == 0 {
		return []EpicView{}, nil
	}
	rows, err := tx.Query(ctx, `
		select e.id::text, s.key || '-' || e.number, e.title, coalesce(o.name, ''), coalesce(o.category, ''), s.key, t.name,
		       ps.key || '-' || p.number, ap.title, m.display_name
		from elements e
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		left join teams t on t.tenant_id = s.tenant_id and t.delivery_space_id = s.id
		left join elements p on p.tenant_id = e.tenant_id and p.id = e.parent_id
		left join spaces ps on ps.tenant_id = p.tenant_id and ps.id = p.space_id
		left join ext_impact x on x.tenant_id = e.tenant_id and x.element_id = e.parent_id
		left join elements ap on ap.tenant_id = x.tenant_id and ap.id = x.application_id
		left join members m on m.tenant_id = e.tenant_id and m.id = e.assignee_id
		where e.id = any($1::uuid[]) order by s.key, e.number`, ids)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (EpicView, error) {
		var v EpicView
		err := r.Scan(&v.ID, &v.Key, &v.Title, &v.State, &v.StateCategory, &v.SpaceKey, &v.Team, &v.ImpactKey, &v.Application, &v.Assignee)
		return v, err
	})
	if err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Progress, err = progressOf(ctx, tx, []string{out[i].ID}); err != nil {
			return nil, err
		}
	}
	return out, nil
}
