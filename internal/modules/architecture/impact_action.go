package architecture

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// ActionCreateImpact è l'azione delle regole «crea l'impatto sul team» (procedura guidata
// «Coinvolgi un team»): sulla richiesta apre gli impatti di un team. Accettato dal team,
// l'impatto genera l'epic (azione create_epic del modulo del lavoro) e l'avanzamento risale.
const ActionCreateImpact = "create_impact"

func registerImpactAction(eng *engine.Service) {
	eng.DescribeAction(engine.ActionSchema{Kind: ActionCreateImpact, Name: "Crea l'impatto sul team",
		Description: "Sulla richiesta: apre un impatto per il team indicato su ciascuna applicazione che la richiesta tocca (attraverso gli asset citati) " +
			"e che non ha ancora un impatto. Se la richiesta tocca applicazioni del team usa quelle, altrimenti tutte. Un impatto già aperto non si ricrea.",
		Fields: []engine.ActionField{{Key: "ref", Name: "Team (nome)", Kind: "text", Required: true}}})
}

func (s *Service) checkCreateImpact(ctx context.Context, tx pgx.Tx, a engine.Action) ([]string, error) {
	if strings.TrimSpace(a.Ref) == "" {
		return []string{"indica in \"ref\" il nome del team"}, nil
	}
	var ok bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from teams where lower(name) = lower($1) and not archived)", strings.TrimSpace(a.Ref)).Scan(&ok); err != nil {
		return nil, err
	}
	if !ok {
		return []string{fmt.Sprintf("team %q inesistente", a.Ref)}, nil
	}
	return nil, nil
}

// touchedApplications sono le applicazioni toccate dalla richiesta: quelle che compongono o
// usano gli asset logici che la richiesta cita, senza un impatto attivo della richiesta.
func touchedApplications(ctx context.Context, tx pgx.Tx, requestID string) ([]touchedApp, error) {
	rows, err := tx.Query(ctx, `select distinct a.target_id::text, coalesce(x.owner_team_id::text, ''), e.title
		from relations c
		join relation_types ct on ct.tenant_id = c.tenant_id and ct.id = c.type_id and ct.key = $2
		join relations a on a.tenant_id = c.tenant_id and a.source_id = c.target_id
		join relation_types at on at.tenant_id = a.tenant_id and at.id = a.type_id and at.key in ('composed_of', 'uses')
		join ext_application x on x.tenant_id = a.tenant_id and x.element_id = a.target_id
		join elements e on e.tenant_id = a.tenant_id and e.id = a.target_id and e.deleted_at is null
		where c.source_id = $1
		  and not exists (select 1 from ext_impact i join elements ie on ie.tenant_id = i.tenant_id and ie.id = i.element_id and ie.deleted_at is null
		                  where i.request_id = $1 and i.application_id = a.target_id)
		order by e.title`, requestID, RelConcerns)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (touchedApp, error) {
		var t touchedApp
		err := r.Scan(&t.ID, &t.Team, &t.Name)
		return t, err
	})
}

type touchedApp struct{ ID, Team, Name string }

func (s *Service) actionCreateImpact(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, a engine.Action) (string, error) {
	if el.TypeKey != RequestType {
		return "create_impact vale solo sulle richieste", nil
	}
	var teamID, teamName string
	err := tx.QueryRow(ctx, "select id::text, name from teams where lower(name) = lower($1) and not archived", strings.TrimSpace(a.Ref)).Scan(&teamID, &teamName)
	if errors.Is(err, pgx.ErrNoRows) {
		return "team " + a.Ref + " inesistente: impatto non creato", nil
	}
	if err != nil {
		return "", err
	}
	apps, err := touchedApplications(ctx, tx, el.ID)
	if err != nil {
		return "", err
	}
	own := slices.DeleteFunc(slices.Clone(apps), func(t touchedApp) bool { return t.Team != teamID })
	if len(own) > 0 {
		apps = own
	}
	if len(apps) == 0 {
		return "nessuna applicazione da toccare: impatto per il team " + teamName + " non creato", nil
	}
	// La regola agisce per conto del motore: chi ha fatto avanzare la richiesta non deve poter scrivere impatti.
	sys := actor
	sys.Permissions = append(slices.Clone(actor.Permissions), "element.read", "element.write")
	var made []string
	for _, t := range apps {
		imp, err := s.createImpact(ctx, tx, sys, el, ImpactInput{ApplicationID: t.ID, TeamID: &teamID})
		var ve *engine.ValidationError
		if errors.As(err, &ve) {
			continue // per esempio un impatto già aperto da un'altra regola: si lascia stare
		}
		if err != nil {
			return "", err
		}
		made = append(made, imp.Key)
	}
	if len(made) == 0 {
		return "", engine.ErrNoEffect
	}
	return "impatti per il team " + teamName + ": " + strings.Join(made, ", "), nil
}
