// Package risk è la parte di GRC dei rischi, delle terze parti e degli incidenti
// (visione §7.4-7.5; piano, passo 20):
//   - rischi ICT (elemento "risk") valutati su una matrice configurabile probabilità ×
//     impatto, con rischio inerente e residuo, trattamento con piano e scadenza, accettazione
//     approvata dal motore delle approvazioni (politica "@risk_acceptance");
//   - fornitori ICT (elemento "ict_provider") con i contratti, le applicazioni servite e il
//     fatto calcolato "critico"; registro delle informazioni esportabile in CSV con colonne
//     configurabili; scadenze contrattuali con preavviso;
//   - incidenti (elemento "incident") con timeline, applicazioni impattate, classificazione
//     normativa per schemi del tenant (criteri e regola nel linguaggio di condizioni), notifiche
//     normative con le scadenze di ogni tappa, postmortem con azioni correttive (task).
//
// Come negli altri moduli nessun contenuto è nel codice: matrici, livelli, soglie, schemi,
// criteri, scadenze e politica arrivano dal pacchetto di partenza (starter/) e si
// modificano per tenant. I testi delle norme non ci sono: solo riferimenti e riassunti.
package risk

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

const (
	RiskType     = "risk"
	IncidentType = "incident"
	ProviderType = "ict_provider"
	AppType      = "application"   // modulo architecture
	AssetType    = "logical_asset" // modulo architecture
	ControlType  = "control"       // modulo di conformità (altro ramo): solo per nome
	TaskType     = "task"          // modulo work

	RiskSpace     = "RIS"
	IncidentSpace = "INC"
	ProviderSpace = "FOR"

	// Ruoli sugli elementi.
	RoleRiskOwner     = "risk_owner"     // titolare del rischio (accetta il rischio residuo)
	RoleRiskCommittee = "risk_committee" // comitato rischi (di solito il gruppo Risk management)
	RoleIncidentLead  = "incident_lead"  // responsabile dell'incidente
	RoleIncidentTeam  = "incident_team"  // gestione incidenti (di solito il gruppo Incident management)
	RoleProviderOwner = "provider_owner" // referente del fornitore

	GroupRisk     = "Risk management"
	GroupIncident = "Incident management"

	// Relazioni.
	RelRiskOf       = "risk_of"          // rischio → applicazione, asset o fornitore
	RelMitigates    = "mitigates"        // controllo → rischio ("mitigato da" dal lato del rischio)
	RelProvidedBy   = "provided_by"      // applicazione → fornitore ICT
	RelImpacts      = "incident_impacts" // incidente → applicazione o asset
	RelMaterializes = "materializes"     // incidente → rischio
	RelCorrective   = "corrective_for"   // task → incidente (azione correttiva)

	// Azioni delle regole di processo.
	ActionAcceptance = "risk_acceptance" // sul rischio: submitted, accepted, reopened
	ActionIncident   = "incident_stage"  // sull'incidente: detected, classified, resolved, closed

	// Politica e soggetto dell'approvazione dell'accettazione.
	PolicyAcceptance  = "@risk_acceptance"
	SubjectAcceptance = "risk_acceptance"

	PermAdmin    = rbac.RiskAdmin
	PermIncident = rbac.IncidentManage
)

var acceptanceRefs = []string{"submitted", "accepted", "reopened"}
var incidentRefs = []string{"detected", "classified", "resolved", "closed"}

// Service è il modulo. Now si può sostituire nei test per scadenze e conti alla rovescia.
type Service struct {
	eng *engine.Service
	ap  *approval.Service
	Now func() time.Time
}

// Register collega il modulo al motore: fatti, azioni delle regole, pacchetti di partenza dei
// processi ed esito dell'approvazione dell'accettazione del rischio.
func Register(eng *engine.Service, ap *approval.Service) *Service {
	s := &Service{eng: eng, ap: ap, Now: time.Now}
	eng.Facts(RiskType, s.riskFacts)
	eng.Facts(IncidentType, s.incidentFacts)
	eng.Facts(ProviderType, s.providerFacts)
	eng.Facts(AppType, s.appFacts)
	eng.Action(ActionAcceptance, s.actionAcceptance, checkIn(acceptanceRefs, "stadio dell'accettazione"))
	eng.Action(ActionIncident, s.actionIncident, checkIn(incidentRefs, "stadio dell'incidente"))
	if ap != nil {
		ap.OnClose(SubjectAcceptance, s.closeAcceptance)
		ap.SeparateRequester(SubjectAcceptance)
	}
	describe(eng)
	for _, f := range processFiles {
		raw, err := starters.ReadFile("starter/" + f)
		if err != nil {
			panic(err)
		}
		eng.Starter(raw)
	}
	return s
}

func checkIn(allowed []string, what string) engine.ActionCheck {
	return func(_ context.Context, _ pgx.Tx, a engine.Action) ([]string, error) {
		if !slices.Contains(allowed, a.Ref) {
			return []string{fmt.Sprintf("%s %q non ammesso (%s)", what, a.Ref, strings.Join(allowed, ", "))}, nil
		}
		return nil, nil
	}
}

func describe(eng *engine.Service) {
	treat := []string{"mitigare", "accettare", "trasferire", "evitare"}
	eng.DescribeFacts(RiskType,
		engine.FactInfo{Key: "punteggio_inerente", Name: "Punteggio del rischio inerente", Kind: "number", Example: "12"},
		engine.FactInfo{Key: "livello_inerente", Name: "Livello del rischio inerente", Kind: "text", Example: "alto"},
		engine.FactInfo{Key: "punteggio_residuo", Name: "Punteggio del rischio residuo", Kind: "number", Example: "6"},
		engine.FactInfo{Key: "livello_residuo", Name: "Livello del rischio residuo", Kind: "text", Example: "medio"},
		engine.FactInfo{Key: "oltre_soglia", Name: "Residuo oltre la soglia di attenzione", Kind: "bool", Values: []string{"sì", "no"}},
		engine.FactInfo{Key: "trattamento", Name: "Trattamento", Kind: "text", Values: treat},
		engine.FactInfo{Key: "piano_trattamento", Name: "Piano di trattamento", Kind: "presence"},
		engine.FactInfo{Key: "scadenza_trattamento", Name: "Scadenza del trattamento", Kind: "date", Example: "2027-03-31"},
		engine.FactInfo{Key: "revisione", Name: "Prossima revisione", Kind: "date", Example: "2027-06-30"},
		engine.FactInfo{Key: "categoria", Name: "Categoria del rischio", Kind: "text", Example: "Terze parti"},
		engine.FactInfo{Key: "accettazione", Name: "Stato dell'accettazione", Kind: "text", Values: []string{"nessuna", "in approvazione", "accettato", "non approvata", "scaduta"}},
		engine.FactInfo{Key: "controlli", Name: "Controlli che lo mitigano", Kind: "number", Example: "2"},
		engine.FactInfo{Key: "applicazioni", Name: "Applicazioni esposte", Kind: "text"},
		engine.FactInfo{Key: "fornitori", Name: "Fornitori esposti", Kind: "text"},
		engine.FactInfo{Key: "funzione_critica", Name: "Riguarda una funzione critica (DORA)", Kind: "bool"},
		engine.FactInfo{Key: "incidenti", Name: "Incidenti che lo hanno concretizzato", Kind: "number", Example: "0"},
	)
	eng.DescribeFacts(IncidentType,
		engine.FactInfo{Key: "clienti_coinvolti", Name: "Clienti coinvolti", Kind: "number", Example: "12000"},
		engine.FactInfo{Key: "clienti_coinvolti_pct", Name: "Clienti coinvolti (%)", Kind: "number", Example: "8"},
		engine.FactInfo{Key: "durata_ore", Name: "Durata dalla rilevazione (ore)", Kind: "number", Example: "6"},
		engine.FactInfo{Key: "fermo_minuti", Name: "Fermo del servizio (minuti)", Kind: "number", Example: "90"},
		engine.FactInfo{Key: "perdita_dati", Name: "Perdita di dati", Kind: "text", Values: []string{"disponibilità", "integrità", "riservatezza", "autenticità"}},
		engine.FactInfo{Key: "impatto_economico", Name: "Impatto economico (euro)", Kind: "number", Example: "150000"},
		engine.FactInfo{Key: "stati_membri", Name: "Stati membri coinvolti", Kind: "number", Example: "2"},
		engine.FactInfo{Key: "impatto_reputazionale", Name: "Impatto reputazionale", Kind: "bool"},
		engine.FactInfo{Key: "funzione_critica", Name: "Colpisce una funzione critica (DORA)", Kind: "bool"},
		engine.FactInfo{Key: "criticita_max", Name: "Criticità massima delle applicazioni impattate", Kind: "text", Values: []string{"bassa", "media", "alta", "critica"}},
		engine.FactInfo{Key: "applicazioni", Name: "Applicazioni impattate", Kind: "text"},
		engine.FactInfo{Key: "causa", Name: "Causa", Kind: "text", Values: causeNames()},
		engine.FactInfo{Key: "ricorrente", Name: "Incidente ricorrente", Kind: "bool"},
		engine.FactInfo{Key: "grave", Name: "Classificato grave o significativo da uno schema", Kind: "bool"},
		engine.FactInfo{Key: "classificazione", Name: "Schemi con esito positivo", Kind: "text", Example: "dora"},
		engine.FactInfo{Key: "notifiche_aperte", Name: "Notifiche normative non ancora inviate", Kind: "number", Example: "0"},
		engine.FactInfo{Key: "postmortem", Name: "Postmortem compilato", Kind: "presence"},
		engine.FactInfo{Key: "azioni_correttive", Name: "Azioni correttive", Kind: "number", Example: "2"},
		engine.FactInfo{Key: "azioni_aperte", Name: "Azioni correttive aperte", Kind: "number", Example: "1"},
	)
	eng.DescribeFacts(ProviderType,
		engine.FactInfo{Key: "critico", Name: "Fornitore critico (calcolato)", Kind: "bool"},
		engine.FactInfo{Key: "funzioni_critiche", Name: "Applicazioni con funzione critica servite", Kind: "number", Example: "1"},
		engine.FactInfo{Key: "applicazioni", Name: "Applicazioni servite", Kind: "text"},
		engine.FactInfo{Key: "paese", Name: "Paese della sede", Kind: "text", Example: "IT"},
		engine.FactInfo{Key: "intragruppo", Name: "Fornitore intragruppo", Kind: "bool"},
		engine.FactInfo{Key: "sostituibilita", Name: "Sostituibilità", Kind: "text", Values: []string{"facile", "media", "difficile", "impossibile"}},
		engine.FactInfo{Key: "contratti", Name: "Contratti", Kind: "number", Example: "2"},
		engine.FactInfo{Key: "scadenza_contratto", Name: "Prima scadenza contrattuale", Kind: "date", Example: "2027-01-31"},
	)
	eng.DescribeFacts(AppType,
		engine.FactInfo{Key: "fornitori_ict", Name: "Fornitori ICT da cui dipende", Kind: "text"},
		engine.FactInfo{Key: "rischi_oltre_soglia", Name: "Rischi oltre la soglia", Kind: "number", Example: "0"},
	)
	eng.DescribeAction(engine.ActionSchema{Kind: ActionAcceptance, Name: "Accettazione del rischio",
		Description: "Usata dal processo dei rischi: submitted apre l'approvazione secondo la politica @risk_acceptance, accepted registra l'accettazione, reopened chiude un'accettazione in corso o scaduta.",
		Fields:      []engine.ActionField{{Key: "ref", Name: "Stadio", Kind: "text", Required: true}}})
	eng.DescribeAction(engine.ActionSchema{Kind: ActionIncident, Name: "Momento dell'incidente",
		Description: "Usata dal processo degli incidenti: detected, classified (applica gli schemi di classificazione e genera le notifiche), resolved, closed.",
		Fields:      []engine.ActionField{{Key: "ref", Name: "Stadio", Kind: "text", Required: true}}})
}

func (s *Service) now() time.Time { return s.Now().UTC() }

func (s *Service) today() time.Time {
	t := s.now()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

// reader è un attore di sola lettura per i caricamenti interni del modulo.
func reader(memberID string) engine.Actor {
	if memberID == "" {
		memberID = "00000000-0000-0000-0000-000000000000"
	}
	return engine.Actor{MemberID: memberID, Permissions: []string{rbac.ElementRead}}
}

func requireRead(actor engine.Actor) error {
	if !actor.Has(rbac.ElementRead) {
		return engine.ErrForbidden
	}
	return nil
}

func requireAdmin(actor engine.Actor) error {
	if !actor.Has(PermAdmin) {
		return engine.ErrForbidden
	}
	return nil
}

func requireIncident(actor engine.Actor) error {
	if !actor.Has(PermIncident) {
		return engine.ErrForbidden
	}
	return nil
}

func nullable(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func audited(ctx context.Context, tx pgx.Tx, actor engine.Actor, action, entity, id string, payload map[string]any) error {
	return audit.Append(ctx, tx, audit.Event{Action: action, EntityType: entity, EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: payload})
}

func typeExists(ctx context.Context, tx pgx.Tx, key string) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, "select exists (select 1 from element_types where key = $1)", key).Scan(&ok)
	return ok, err
}

// resolveTyped trova un elemento per chiave o id e controlla il tipo.
func (s *Service) resolveTyped(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, typeKey string) (*engine.Element, error) {
	el, err := s.eng.Get(ctx, tx, actor, strings.TrimSpace(ref))
	if err != nil {
		return nil, err
	}
	if el.TypeKey != typeKey {
		return nil, engine.ErrNotFound
	}
	return el, nil
}

// relate crea una relazione (idempotente) e la registra nella storia di entrambi gli elementi.
func relate(ctx context.Context, tx pgx.Tx, actor engine.Actor, relKey, sourceID, targetID string) (bool, error) {
	tag, err := tx.Exec(ctx, `insert into relations (tenant_id, id, type_id, source_id, target_id, created_by)
		select app_tenant(), $1, rt.id, $3, $4, $5 from relation_types rt where rt.key = $2
		on conflict do nothing`, db.NewID(), relKey, sourceID, targetID, nullable(actor.MemberID))
	if err != nil {
		if strings.Contains(err.Error(), "foreign key") {
			return false, engine.ErrNotFound
		}
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	if err := engine.AddActivity(ctx, tx, sourceID, actor.MemberID, "relation", relKey, map[string]any{"target": targetID}, nil); err != nil {
		return false, err
	}
	return true, engine.AddActivity(ctx, tx, targetID, actor.MemberID, "relation", relKey+".inverse", map[string]any{"source": sourceID}, nil)
}

func unrelate(ctx context.Context, tx pgx.Tx, actor engine.Actor, relKey, sourceID, targetID string) error {
	tag, err := tx.Exec(ctx, `delete from relations r using relation_types rt
		where rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = $1 and r.source_id = $2 and r.target_id = $3`,
		relKey, sourceID, targetID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return engine.ErrNotFound
	}
	if err := engine.AddActivity(ctx, tx, sourceID, actor.MemberID, "relation", "removed", nil, map[string]any{"target": targetID}); err != nil {
		return err
	}
	return engine.AddActivity(ctx, tx, targetID, actor.MemberID, "relation", "removed", nil, map[string]any{"source": sourceID})
}

// Ref è un elemento collegato, per le viste del modulo.
type Ref struct {
	ID       string `json:"id"`
	Key      string `json:"key"`
	Title    string `json:"title"`
	Type     string `json:"type"`
	TypeName string `json:"type_name"`
	State    string `json:"state"`
	StateCat string `json:"state_category"`
	// Critical: applicazione con funzione critica (DORA).
	Critical bool `json:"critical,omitempty"`
}

const refColumns = `e.id::text, coalesce(s.key || '-' || e.number, ''), e.title, t.key, t.name, coalesce(o.name, ''), coalesce(o.category, ''),
	coalesce((select a.supports_critical_function from ext_application a where a.tenant_id = e.tenant_id and a.element_id = e.id), false)`
const refFrom = `
	from elements e
	join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
	join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
	left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id`

func scanRefs(rows pgx.Rows) ([]Ref, error) {
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Ref, error) {
		var x Ref
		err := r.Scan(&x.ID, &x.Key, &x.Title, &x.Type, &x.TypeName, &x.State, &x.StateCat, &x.Critical)
		return x, err
	})
	if out == nil {
		out = []Ref{}
	}
	return out, err
}

// targetsOf: elementi collegati da relKey nel verso indicato (outward: l'elemento è la sorgente).
func targetsOf(ctx context.Context, tx pgx.Tx, relKey, elementID string, outward bool) ([]Ref, error) {
	join := "r.target_id = e.id where r.source_id = $2"
	if !outward {
		join = "r.source_id = e.id where r.target_id = $2"
	}
	rows, err := tx.Query(ctx, `select `+refColumns+refFrom+`
		join relations r on r.tenant_id = e.tenant_id and `+join+`
		  and r.type_id = (select id from relation_types where key = $1)
		  and e.deleted_at is null
		order by t.key, e.title limit 500`, relKey, elementID)
	if err != nil {
		return nil, err
	}
	return scanRefs(rows)
}

// setOwner assegna il ruolo (sostituendo il titolare) se memberID non è vuoto.
func setOwner(ctx context.Context, tx pgx.Tx, actor engine.Actor, elementID, role, memberID string) error {
	if strings.TrimSpace(memberID) == "" {
		return nil
	}
	return engine.SetStakeholder(ctx, tx, actor, elementID, role, strings.TrimSpace(memberID), "", true)
}

func holderName(ctx context.Context, tx pgx.Tx, elementID, role string) (id, name *string, err error) {
	err = tx.QueryRow(ctx, `select sh.member_id::text, coalesce(m.display_name, g.name) from stakeholders sh
		join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id
		left join members m on m.tenant_id = sh.tenant_id and m.id = sh.member_id
		left join groups g on g.tenant_id = sh.tenant_id and g.id = sh.group_id
		where sh.element_id = $1 and r.key = $2 order by sh.member_id nulls last limit 1`, elementID, role).Scan(&id, &name)
	if err == pgx.ErrNoRows {
		return nil, nil, nil
	}
	return id, name, err
}

func parseDate(field, v string) (*time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	t, err := time.Parse(time.DateOnly, v)
	if err != nil {
		return nil, engine.InvalidCode("validation.date_invalid", field, "data non valida (AAAA-MM-GG)")
	}
	return &t, nil
}

func dateStr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.DateOnly)
	return &s
}
