package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine/notify"
	"regia/internal/platform/audit"
)

// TransitionOpts: System = la richiesta viene dal motore (es. esito di un'approvazione),
// non da una persona; solo così si eseguono le transizioni marcate "system".
type TransitionOpts struct {
	System bool
}

// Transition porta l'elemento nello stato "to" se il workflow lo consente.
// Controlli, nell'ordine: transizione esistente, autorizzazione (ruoli stakeholder
// o element.write), commento obbligatorio, guardie dei moduli. Poi stato, attività,
// audit e reazioni dei moduli, tutto nella stessa transazione.
func (s *Service) Transition(ctx context.Context, tx pgx.Tx, actor Actor, elementID, to, comment string, o TransitionOpts) (*Element, error) {
	el, err := s.lockForWrite(ctx, tx, actor, elementID)
	if err != nil {
		return nil, err
	}
	fl, err := s.flowOf(ctx, tx, el)
	if err != nil {
		return nil, err
	}
	from, ok := findStateByID(fl.states, deref(el.StateID))
	if !ok {
		return nil, invalid("state", "l'elemento non ha uno stato valido per il suo processo")
	}
	lf := &lazyFacts{s: s, el: el}
	var tr Transition
	found := false
	for _, cand := range fl.w.find(from, to) {
		if cand.When != "" {
			f, err := lf.get(ctx, tx)
			if err != nil {
				return nil, err
			}
			if !fl.w.cond(cand.When).Eval(f) {
				continue
			}
		}
		if cand.System && !o.System {
			continue // una persona non esegue i passaggi di sistema; il motore può eseguire anche gli altri
		}
		tr, found = cand, true
		break
	}
	if !found {
		for _, cand := range fl.w.find(from, to) {
			if cand.System && !o.System {
				return nil, invalid("to", "il passaggio a %q avviene solo con l'esito dell'approvazione", to)
			}
		}
		return nil, invalid("to", "passaggio da %q a %q non previsto dal processo", from.Name, to)
	}
	override := false
	if !o.System {
		allowed, ovr, err := s.mayRun(ctx, tx, actor, el, tr)
		if err != nil {
			return nil, err
		}
		if !allowed {
			return nil, ErrForbidden
		}
		override = ovr
	}
	comment = strings.TrimSpace(comment)
	if tr.CommentRequired && comment == "" {
		return nil, Required("comment", "per questo passaggio serve una motivazione")
	}
	gates, err := s.checkGates(ctx, tx, el, fl.w, tr, lf)
	if err != nil {
		return nil, err
	}
	var failed []string
	for _, g := range gates {
		if !g.Passed {
			failed = append(failed, g.Message)
		}
	}
	target, ok := findState(fl.states, tr.To)
	if !ok {
		return nil, NotFound("to", "", "stato %q inesistente", tr.To)
	}
	// Gate implicito dei campi personalizzati obbligatori nella fase di arrivo (anche per i
	// passaggi di sistema, come l'esito di un'approvazione: la regola non si aggira).
	missing, err := s.missingForPhase(ctx, tx, el, fl.states, target, lf)
	if err != nil {
		return nil, err
	}
	if len(missing) > 0 {
		failed = append(failed, requiredMessage(target.Name, missing))
	}
	if !o.System {
		msg, err := s.approvalExitMissing(ctx, tx, el, fl, tr, target, lf)
		if err != nil {
			return nil, err
		}
		if msg != "" {
			failed = append(failed, msg)
		}
	}
	if len(failed) > 0 {
		return nil, invalid("gate", "%s", strings.Join(failed, "; "))
	}
	resolved := target.Category == "done" || target.Category == "cancelled"
	if _, err := tx.Exec(ctx, `update elements set state_id = $2, version = version + 1, updated_by = $3,
		resolved_at = case when $4 then coalesce(resolved_at, now()) else null end
		where id = $1`, el.ID, target.ID, nullable(actor.MemberID), resolved); err != nil {
		return nil, err
	}
	actorID := actor.MemberID
	if err := addActivity(ctx, tx, el.ID, actorID, "state", "state",
		map[string]any{"name": target.Name, "comment": comment}, map[string]any{"name": from.Name}); err != nil {
		return nil, err
	}
	payload := map[string]any{"from": from.Name, "to": target.Name}
	if override {
		payload["override"] = true
	}
	if o.System {
		payload["system"] = true
	}
	if len(gates) > 0 {
		payload["gates"] = gates
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "element.transition", EntityType: el.TypeKey, EntityID: el.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: payload}); err != nil {
		return nil, err
	}
	// Gli osservatori sanno del cambio di fase (chi lo fa escluso).
	if _, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindPhase, ElementID: el.ID, ActorID: actor.MemberID,
		Followers: true, Payload: map[string]any{"from": from.Name, "to": target.Name}}); err != nil {
		return nil, err
	}
	el.State, el.StateID, el.StateCategory = target.Name, &target.ID, target.Category
	el.Version++
	if err := s.runRules(ctx, tx, actor, el, fl.w, "exit:"+from.Name); err != nil {
		return nil, err
	}
	if err := s.runRules(ctx, tx, actor, el, fl.w, "enter:"+target.Name); err != nil {
		return nil, err
	}
	ev := TransitionEvent{Element: el, From: from.Name, To: target.Name, Transition: tr, Actor: actor, Comment: comment, System: o.System}
	for _, fn := range s.hooks.AfterTransition {
		if err := fn(ctx, tx, ev); err != nil {
			return nil, err
		}
	}
	return s.Get(ctx, tx, Actor{MemberID: actor.MemberID, Permissions: []string{"element.read"}}, el.ID)
}

// mayRun dice se l'attore può eseguire la transizione. Con ruoli indicati serve uno
// di quei ruoli stakeholder sull'elemento; l'amministratore ("*") può forzare, e la
// forzatura resta nell'audit. Senza ruoli serve element.write.
func (s *Service) mayRun(ctx context.Context, tx pgx.Tx, actor Actor, el *Element, tr Transition) (allowed, override bool, err error) {
	if len(tr.Roles) == 0 {
		ok, err := s.writeBySpace(ctx, tx, actor, el)
		return ok, false, err
	}
	ok, err := HasRole(ctx, tx, el.ID, actor.MemberID, tr.Roles)
	if err != nil || ok {
		return ok, false, err
	}
	if actor.Has("*") {
		return true, true, nil
	}
	return false, false, nil
}

// TransitionView è un passaggio disponibile per l'attore, da mostrare come pulsante.
// Blocked elenca i gate non superati: il pulsante si mostra disabilitato con il motivo.
type TransitionView struct {
	To              string       `json:"to"`
	Label           string       `json:"label"`
	CommentRequired bool         `json:"comment_required"`
	Gates           []GateResult `json:"gates"`
	Blocked         []string     `json:"blocked"`
	// Forward: il passaggio porta avanti nel percorso (la fase di arrivo viene dopo quella attuale e
	// non è fuori percorso). È quello che l'interfaccia mette in primo piano, non «Rimanda» o «Respingi».
	Forward bool `json:"forward"`
}

// Available elenca i passaggi che l'attore può avviare dallo stato attuale, con l'esito dei gate.
func (s *Service) Available(ctx context.Context, tx pgx.Tx, actor Actor, el *Element) ([]TransitionView, error) {
	fl, err := s.flowOf(ctx, tx, el)
	if err != nil {
		return nil, err
	}
	from, ok := findStateByID(fl.states, deref(el.StateID))
	if !ok {
		return []TransitionView{}, nil
	}
	lf := &lazyFacts{s: s, el: el}
	out := []TransitionView{}
	seen := map[string]bool{}
	for _, tr := range fl.w.Transitions {
		if tr.System || !tr.startsFrom(from) || seen[strings.ToLower(tr.To)] {
			continue
		}
		if tr.When != "" {
			f, err := lf.get(ctx, tx)
			if err != nil {
				return nil, err
			}
			if !fl.w.cond(tr.When).Eval(f) {
				continue
			}
		}
		allowed, override, err := s.mayRun(ctx, tx, actor, el, tr)
		if err != nil {
			return nil, err
		}
		if !allowed || override {
			continue
		}
		gates, err := s.checkGates(ctx, tx, el, fl.w, tr, lf)
		if err != nil {
			return nil, err
		}
		v := TransitionView{To: tr.To, Label: tr.Label, CommentRequired: tr.CommentRequired, Gates: gates, Blocked: []string{},
			Forward: movesForward(fl.states, from, tr.To)}
		for _, g := range gates {
			if !g.Passed {
				v.Blocked = append(v.Blocked, g.Message)
			}
		}
		if target, ok := findState(fl.states, tr.To); ok {
			missing, err := s.missingForPhase(ctx, tx, el, fl.states, target, lf)
			if err != nil {
				return nil, err
			}
			if len(missing) > 0 {
				msg := requiredMessage(target.Name, missing)
				v.Gates = append(v.Gates, GateResult{Key: "campi", Name: "Campi obbligatori", Message: msg})
				v.Blocked = append(v.Blocked, msg)
			}
			msg, err := s.approvalExitMissing(ctx, tx, el, fl, tr, target, lf)
			if err != nil {
				return nil, err
			}
			if msg != "" {
				v.Gates = append(v.Gates, GateResult{Key: "campi_esito", Name: "Campi della fase dopo l'approvazione", Message: msg})
				v.Blocked = append(v.Blocked, msg)
			}
		}
		seen[strings.ToLower(tr.To)] = true
		out = append(out, v)
	}
	return out, nil
}

// Phases restituisce le fasi del processo dell'elemento e il riferimento alla versione.
func (s *Service) Phases(ctx context.Context, tx pgx.Tx, el *Element) ([]State, *ProcessRef, error) {
	fl, err := s.flowOf(ctx, tx, el)
	if err != nil {
		return nil, nil, err
	}
	if el.ProcessVersionID == nil {
		return fl.states, nil, nil
	}
	ref := &ProcessRef{VersionID: *el.ProcessVersionID}
	err = tx.QueryRow(ctx, `select p.id, p.key, p.name, v.number from process_versions v
		join process_definitions p on p.tenant_id = v.tenant_id and p.id = v.process_id where v.id = $1`,
		*el.ProcessVersionID).Scan(&ref.ID, &ref.Key, &ref.Name, &ref.Version)
	return fl.states, ref, err
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// stakeholderOf è la condizione SQL "il membro ha un ruolo stakeholder sull'elemento"
// (direttamente o tramite un gruppo).
func stakeholderOf(elementParam, memberParam string) string {
	return `exists (select 1 from stakeholders sh
		where sh.element_id = ` + elementParam + `
		  and (sh.member_id = ` + memberParam + `
		       or sh.group_id in (select member_groups(` + memberParam + `))))`
}

// HasRole dice se il membro ha uno dei ruoli stakeholder indicati sull'elemento.
func HasRole(ctx context.Context, tx pgx.Tx, elementID, memberID string, roles []string) (bool, error) {
	if memberID == "" {
		return false, nil
	}
	var ok bool
	err := tx.QueryRow(ctx, `
		select exists (select 1 from stakeholders sh
			join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id
			where sh.element_id = $1 and r.key = any($3)
			  and (sh.member_id = $2
			       or sh.group_id in (select member_groups($2))))`,
		elementID, memberID, roles).Scan(&ok)
	return ok, err
}

// Stakeholder è un ruolo su un elemento.
type Stakeholder struct {
	Role     string  `json:"role"`
	RoleName string  `json:"role_name"`
	MemberID *string `json:"member_id"`
	GroupID  *string `json:"group_id"`
	Name     string  `json:"name"`
}

// SetStakeholder assegna un ruolo a un membro o a un gruppo (sostituisce i titolari del ruolo
// se replace = true, utile per ruoli unici come lo sponsor).
func SetStakeholder(ctx context.Context, tx pgx.Tx, actor Actor, elementID, roleKey, memberID, groupID string, replace bool) error {
	var roleID, roleName string
	if err := tx.QueryRow(ctx, "select id, name from stakeholder_roles where key = $1", roleKey).Scan(&roleID, &roleName); err != nil {
		return invalidCode("validation.role_not_found", roleKey, "ruolo inesistente")
	}
	// Chi aveva già il ruolo non riceve di nuovo la notifica (replace cancella e reinserisce).
	var had bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from stakeholders where element_id = $1 and role_id = $2
		and (member_id::text = $3 or group_id::text = $4))`, elementID, roleID, memberID, groupID).Scan(&had); err != nil {
		return err
	}
	if replace {
		if _, err := tx.Exec(ctx, "delete from stakeholders where element_id = $1 and role_id = $2", elementID, roleID); err != nil {
			return err
		}
	}
	if memberID != "" {
		var ok bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from members where id::text = $1 and status = 'active')", memberID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return invalidCode("validation.member_not_found", roleKey, "membro inesistente")
		}
	}
	_, err := tx.Exec(ctx, `insert into stakeholders (tenant_id, element_id, role_id, member_id, group_id)
		values (app_tenant(), $1, $2, $3, $4) on conflict do nothing`, elementID, roleID, nullable(memberID), nullable(groupID))
	if err != nil {
		return err
	}
	if !had {
		kind := notify.KindRole
		if roleKey == "team_lead" {
			kind = notify.KindReferent // nomina a referente del team su un impatto
		}
		if _, err := notify.Emit(ctx, tx, notify.Notice{Kind: kind, ElementID: elementID, ActorID: actor.MemberID,
			Members: []string{memberID}, Groups: []string{groupID},
			Payload: map[string]any{"role": roleKey, "role_name": roleName, "group": groupID != ""}}); err != nil {
			return err
		}
	}
	return addActivity(ctx, tx, elementID, actor.MemberID, "stakeholder", roleKey, map[string]any{"member_id": memberID, "group_id": groupID}, nil)
}

// Stakeholders elenca i ruoli dell'elemento con il nome del titolare.
func Stakeholders(ctx context.Context, tx pgx.Tx, elementID string) ([]Stakeholder, error) {
	rows, err := tx.Query(ctx, `
		select r.key, r.name, sh.member_id, sh.group_id, coalesce(m.display_name, g.name, '')
		from stakeholders sh
		join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id
		left join members m on m.tenant_id = sh.tenant_id and m.id = sh.member_id
		left join groups g on g.tenant_id = sh.tenant_id and g.id = sh.group_id
		where sh.element_id = $1 order by r.key, 5`, elementID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Stakeholder])
}

// approvalExitMissing: chi chiede un'approvazione deve aver già compilato i campi
// obbligatori della fase in cui porta l'esito positivo (la fase successiva del percorso).
// Senza questo controllo l'approvatore riceve un errore su dati che non può compilare e
// la decisione si perde (bug trovato dagli e2e: sponsor bloccato da "Data go-live").
func (s *Service) approvalExitMissing(ctx context.Context, tx pgx.Tx, el *Element, fl *flow, tr Transition, target State, lf *lazyFacts) (string, error) {
	if tr.Approval == "" {
		return "", nil
	}
	idx := mainPathIndex(fl.states, target.Name)
	if idx < 0 {
		return "", nil
	}
	for _, st := range fl.states {
		if st.OffPath || mainPathIndex(fl.states, st.Name) != idx+1 {
			continue
		}
		missing, err := s.missingForPhase(ctx, tx, el, fl.states, st, lf)
		if err != nil || len(missing) == 0 {
			return "", err
		}
		return fmt.Sprintf("l'approvazione porta a «%s»: prima compila %s", st.Name, strings.Join(missing, ", ")), nil
	}
	return "", nil
}

// movesForward: la fase to viene dopo from nell'ordine del percorso e non è fuori percorso.
func movesForward(states []State, from State, to string) bool {
	fi, ti := -1, -1
	for i, st := range states {
		if st.ID == from.ID {
			fi = i
		}
		if strings.EqualFold(st.Name, to) {
			ti = i
			if st.OffPath {
				return false
			}
		}
	}
	return fi >= 0 && ti > fi
}
