package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/db"
)

// impactPatch aggiunge al processo degli impatti la regola che crea l'epic quando il team
// accetta il perimetro. È una regola come le altre: l'azienda la sposta in un'altra fase (o
// sulla richiesta) dall'editor dei processi.
var impactPatch = engine.Rule{
	Key: "epic_del_team", Name: "All'accettazione nasce l'epic nello spazio del team", On: "enter:Accettato",
	Actions: []engine.Action{{Kind: ActionCreateEpic}},
}

// Provision configura il modulo in un tenant. È idempotente e va eseguito dopo demand e
// architettura (la regola dell'epic si aggiunge al processo degli impatti).
//   - tipi epic, story, task, bug e sprint con i processi dei pacchetti di partenza;
//   - relazione "pianificato in";
//   - campi priorità, punti e stima associati ai tipi;
//   - regola create_epic sul processo degli impatti (nuova versione pubblicata).
//
// Gli spazi dei team si creano dalla scheda Team (o alla creazione del team).
func Provision(ctx context.Context, tx pgx.Tx, adminMemberID string) error {
	exec := func(sql string, args ...any) error { _, err := tx.Exec(ctx, sql, args...); return err }
	for _, t := range workTypes {
		var id string
		err := tx.QueryRow(ctx, "select id from element_types where key = $1", t.key).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			err = exec(`insert into element_types (tenant_id, id, key, name, module, extension, is_system)
				values (app_tenant(), $1, $2, $3, 'work', $4, true)`, db.NewID(), t.key, t.name, nullable(t.ext))
		}
		if err != nil {
			return err
		}
	}
	if err := exec(`insert into relation_types (tenant_id, id, key, outward_name, inward_name, is_system)
		values (app_tenant(), $1, $2, 'pianificato in', 'contiene', true) on conflict do nothing`, db.NewID(), RelPlannedIn); err != nil {
		return err
	}
	// Gli spazi dei team esistenti (creati prima di un aggiornamento) ricevono i tipi nuovi.
	if err := exec(`insert into space_types (tenant_id, space_id, type_id)
		select app_tenant(), t.delivery_space_id, et.id from teams t cross join element_types et
		where t.delivery_space_id is not null and et.key = any($1) on conflict do nothing`, typeKeys()); err != nil {
		return err
	}
	eng := engine.New()
	s := Register(eng)
	actor := engine.Actor{MemberID: adminMemberID, Permissions: []string{"*"}}
	for _, f := range processFiles {
		raw, err := starters.ReadFile("starter/" + f)
		if err != nil {
			return err
		}
		b, err := engine.ParseBundle(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", f, err)
		}
		var has bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from process_definitions p join element_types t on t.tenant_id = p.tenant_id and t.id = p.type_id
			where t.key = $1)`, b.Type).Scan(&has); err != nil {
			return err
		}
		if has {
			continue
		}
		if _, problems, err := eng.Import(ctx, tx, actor, b, true); err != nil {
			return fmt.Errorf("pacchetto %s: %w %v", f, err, problems)
		}
	}
	// Campi di default: si importano una volta (poi sono configurazione del tenant).
	var hasFields bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from field_defs where key = $1)", FieldPoints).Scan(&hasFields); err != nil {
		return err
	}
	if !hasFields {
		raw, err := starters.ReadFile("starter/work_fields.json")
		if err != nil {
			return err
		}
		fb, err := engine.ParseFieldsBundle(raw)
		if err != nil {
			return err
		}
		if _, err := eng.ImportFields(ctx, tx, actor, fb); err != nil {
			return fmt.Errorf("campi del work management: %w", err)
		}
	}
	return s.patchImpactProcess(ctx, tx, actor)
}

func typeKeys() []string {
	out := make([]string, len(workTypes))
	for i, t := range workTypes {
		out[i] = t.key
	}
	return out
}

// patchImpactProcess aggiunge la regola dell'epic al processo degli impatti, come nuova
// versione pubblicata. Non fa nulla se il processo non c'è, ha una bozza aperta, ha già la
// regola o usa già l'azione create_epic in un'altra regola (scelta dell'azienda).
func (s *Service) patchImpactProcess(ctx context.Context, tx pgx.Tx, actor engine.Actor) error {
	adm := actor
	adm.Permissions = append(slices.Clone(actor.Permissions), engine.PermProcessAdmin)
	pi, err := s.eng.GetProcess(ctx, tx, adm, "impact_standard")
	if errors.Is(err, engine.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if pi.Draft != nil || pi.Current == nil {
		return nil
	}
	raw, _ := json.Marshal(pi.Current)
	w, err := engine.DecodeDefinition(raw)
	if err != nil {
		return err
	}
	for _, r := range w.Rules {
		if r.Key == impactPatch.Key || slices.ContainsFunc(r.Actions, func(a engine.Action) bool { return a.Kind == ActionCreateEpic }) {
			return nil
		}
	}
	if !slices.ContainsFunc(w.Phases, func(p engine.Phase) bool { return strings.EqualFold(p.Name, "Accettato") }) {
		return nil // processo personalizzato senza la fase: l'azienda sceglie dove mettere la regola
	}
	// Il motore del provisioning conosce solo le azioni di questo modulo: quelle degli altri
	// moduli già presenti nel processo (es. sync_reviews) erano valide alla loro pubblicazione
	// e si registrano qui come segnaposto, solo per la validazione della nuova versione.
	known := s.eng.ActionKinds()
	for _, r := range w.Rules {
		for _, a := range r.Actions {
			if !slices.Contains(known, a.Kind) {
				s.eng.Action(a.Kind, func(context.Context, pgx.Tx, engine.Actor, *engine.Element, engine.Action) (string, error) {
					return "", engine.ErrNoEffect
				})
				known = append(known, a.Kind)
			}
		}
	}
	guards := s.eng.GuardNames()
	for _, g := range w.Gates {
		if g.Check != "" && !slices.Contains(guards, g.Check) {
			s.eng.Guard(g.Check, func(context.Context, pgx.Tx, *engine.Element) error { return nil })
			guards = append(guards, g.Check)
		}
	}
	w.Rules = append(w.Rules, impactPatch)
	def, _ := json.Marshal(w)
	const notes = "Work management: l'epic nasce nello spazio del team all'accettazione"
	if _, err := s.eng.SaveDraft(ctx, tx, adm, pi.ID, def, notes); err != nil {
		return err
	}
	if _, problems, err := s.eng.Publish(ctx, tx, adm, pi.ID, notes); err != nil {
		return fmt.Errorf("processo degli impatti: %w %v", err, problems)
	}
	return nil
}
