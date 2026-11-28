package servicenow

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
)

// Incidenti e change di ServiceNow come li vede Regia. Le funzioni lavorano dentro una
// transazione del tenant aperta dall'API, che controlla i permessi: la lettura per chi vede
// le applicazioni, le decisioni e la proposta per chi può segnalare un incidente.

// IncidentStateLabel è lo stato di un incidente (valori standard della tabella incident).
func IncidentStateLabel(v string) string {
	switch v {
	case "1":
		return "Nuovo"
	case "2":
		return "In corso"
	case "3":
		return "In attesa"
	case "6":
		return "Risolto"
	case "7":
		return "Chiuso"
	case "8":
		return "Annullato"
	}
	return v
}

// ChangeStateLabel è lo stato di una change request (modello di stati da Geneva in poi).
func ChangeStateLabel(v string) string {
	switch v {
	case "-5":
		return "Nuova"
	case "-4":
		return "Valutazione"
	case "-3":
		return "Autorizzazione"
	case "-2":
		return "Pianificata"
	case "-1":
		return "In implementazione"
	case "0":
		return "Revisione"
	case "3":
		return "Chiusa"
	case "4":
		return "Annullata"
	}
	return v
}

// ApprovalLabel è lo stato di approvazione di una change request.
func ApprovalLabel(v string) string {
	switch v {
	case "approved":
		return "Approvata"
	case "rejected":
		return "Rifiutata"
	case "requested":
		return "Approvazione richiesta"
	case "not requested", "not yet requested":
		return "Approvazione non richiesta"
	}
	return v
}

// Ref è un elemento di Regia citato (applicazione, incidente, richiesta).
type Ref struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Title string `json:"title"`
}

// Incident è un incidente di ServiceNow letto da Regia.
type Incident struct {
	ID               string     `json:"id"`
	IntegrationID    string     `json:"integration_id"`
	SysID            string     `json:"sys_id"`
	Number           string     `json:"number"`
	Priority         int        `json:"priority"`
	State            string     `json:"state"`
	StateLabel       string     `json:"state_label"`
	OpenedAt         time.Time  `json:"opened_at"`
	ResolvedAt       *time.Time `json:"resolved_at"`
	ShortDescription string     `json:"short_description"`
	URL              string     `json:"url"`
	Service          string     `json:"service"`
	Application      *Ref       `json:"application"`
	DoraStatus       string     `json:"dora_status"` // pending | counted | excluded
	DoraMode         string     `json:"dora_mode"`   // confirm | auto (dall'integrazione)
	Counts           bool       `json:"counts"`      // conta nelle metriche DORA adesso
	RegiaIncident    *Ref       `json:"regia_incident"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// Change è una change request di ServiceNow letta da Regia.
type Change struct {
	ID               string     `json:"id"`
	IntegrationID    string     `json:"integration_id"`
	SysID            string     `json:"sys_id"`
	Number           string     `json:"number"`
	State            string     `json:"state"`
	StateLabel       string     `json:"state_label"`
	Approval         string     `json:"approval"`
	ApprovalLabel    string     `json:"approval_label"`
	Type             string     `json:"type"`
	PlannedStart     *time.Time `json:"planned_start"`
	PlannedEnd       *time.Time `json:"planned_end"`
	ActualStart      *time.Time `json:"actual_start"`
	ActualEnd        *time.Time `json:"actual_end"`
	ShortDescription string     `json:"short_description"`
	URL              string     `json:"url"`
	Service          string     `json:"service"`
	Application      *Ref       `json:"application"`
	RegiaChange      *string    `json:"regia_change"` // numero del change di Regia collegato
	Request          *Ref       `json:"request"`      // richiesta di Regia del change
	UpdatedAt        time.Time  `json:"updated_at"`
}

// IncidentFilter filtra gli incidenti.
type IncidentFilter struct {
	IntegrationID string
	ApplicationID string
	// Proposals: solo quelli senza incidente di Regia e non esclusi (le proposte).
	Proposals bool
	Limit     int
}

const selectIncident = `
	select x.id, x.integration_id, x.sys_id, x.number, x.priority, x.state, x.opened_at, x.resolved_at, x.short_description, x.url,
	  coalesce(r.name, ''), a.id, sa.key || '-' || a.number, a.title, x.dora_status, coalesce(i.config->>'dora_mode', 'confirm'),
	  ri.id, sri.key || '-' || ri.number, ri.title, x.updated_at
	from servicenow_incidents x
	join integrations i on i.tenant_id = x.tenant_id and i.id = x.integration_id
	left join connector_repositories r on r.tenant_id = x.tenant_id and r.id = x.repository_id
	left join elements a on a.tenant_id = x.tenant_id and a.id = x.application_element_id and a.deleted_at is null
	left join spaces sa on sa.tenant_id = a.tenant_id and sa.id = a.space_id
	left join elements ri on ri.tenant_id = x.tenant_id and ri.id = x.regia_incident_element_id and ri.deleted_at is null
	left join spaces sri on sri.tenant_id = ri.tenant_id and sri.id = ri.space_id
	where true`

func scanIncidents(rows pgx.Rows) ([]Incident, error) {
	defer rows.Close()
	out := []Incident{}
	for rows.Next() {
		var x Incident
		var aID, aKey, aTitle, rID, rKey, rTitle *string
		if err := rows.Scan(&x.ID, &x.IntegrationID, &x.SysID, &x.Number, &x.Priority, &x.State, &x.OpenedAt, &x.ResolvedAt, &x.ShortDescription, &x.URL,
			&x.Service, &aID, &aKey, &aTitle, &x.DoraStatus, &x.DoraMode, &rID, &rKey, &rTitle, &x.UpdatedAt); err != nil {
			return nil, err
		}
		x.StateLabel = IncidentStateLabel(x.State)
		if aID != nil {
			x.Application = &Ref{ID: *aID, Key: deref(aKey), Title: deref(aTitle)}
		}
		if rID != nil {
			x.RegiaIncident = &Ref{ID: *rID, Key: deref(rKey), Title: deref(rTitle)}
		}
		x.Counts = x.RegiaIncident == nil && x.Application != nil && x.State != "8" &&
			(x.DoraStatus == "counted" || (x.DoraStatus == "pending" && x.DoraMode == DoraAuto))
		out = append(out, x)
	}
	return out, rows.Err()
}

// Incidents elenca gli incidenti, dal più recente.
func Incidents(ctx context.Context, tx pgx.Tx, f IncidentFilter) ([]Incident, error) {
	q, args := selectIncident, []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		q += fmt.Sprintf(" and "+cond, len(args))
	}
	if f.IntegrationID != "" {
		if !validID(f.IntegrationID) {
			return []Incident{}, nil
		}
		add("x.integration_id = $%d", f.IntegrationID)
	}
	if f.ApplicationID != "" {
		if !validID(f.ApplicationID) {
			return []Incident{}, nil
		}
		add("x.application_element_id = $%d", f.ApplicationID)
	}
	if f.Proposals {
		q += " and x.regia_incident_element_id is null and x.dora_status <> 'excluded' and x.state <> '8' and x.application_element_id is not null and i.active"
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	args = append(args, f.Limit)
	rows, err := tx.Query(ctx, q+fmt.Sprintf(" order by x.opened_at desc, x.number limit $%d", len(args)), args...)
	if err != nil {
		return nil, err
	}
	return scanIncidents(rows)
}

func loadIncident(ctx context.Context, tx pgx.Tx, id string, lock bool) (*Incident, error) {
	if !validID(id) {
		return nil, ErrRecordNotFound
	}
	q := selectIncident + " and x.id = $1"
	if lock {
		q += " for update of x"
	}
	rows, err := tx.Query(ctx, q, id)
	if err != nil {
		return nil, err
	}
	list, err := scanIncidents(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrRecordNotFound
	}
	return &list[0], nil
}

func auditActor(ctx context.Context, tx pgx.Tx, actor engine.Actor, action, id string, payload map[string]any) error {
	return audit.Append(ctx, tx, audit.Event{Action: action, EntityType: "servicenow_incident", EntityID: id, Payload: payload,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP})
}

// DecideIncident registra se un incidente conta nelle metriche DORA: counted (conta),
// excluded (non rilevante) o pending (torna in attesa).
func DecideIncident(ctx context.Context, tx pgx.Tx, actor engine.Actor, id, status string) (*Incident, error) {
	switch status {
	case "counted", "excluded", "pending":
	default:
		return nil, invalid("status", "ammessi counted (conta nelle DORA), excluded (non rilevante) e pending (da decidere)")
	}
	cur, err := loadIncident(ctx, tx, id, true)
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `update servicenow_incidents set dora_status = $2, decided_by = $3, decided_at = case when $2 = 'pending' then null else now() end,
		updated_at = now() where id = $1`, id, status, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := auditActor(ctx, tx, actor, "devsecops.servicenow.incident_decide", id, map[string]any{"number": cur.Number, "status": status, "previous": cur.DoraStatus}); err != nil {
		return nil, err
	}
	return loadIncident(ctx, tx, id, false)
}

// Proposal è un incidente di Regia precompilato da un incidente di ServiceNow: lo
// conferma (e lo classifica DORA/NIS2) una persona. Mai creato in automatico.
type Proposal struct {
	Source      Incident `json:"source"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	DetectedAt  string   `json:"detected_at"`
	ResolvedAt  *string  `json:"resolved_at"`
	Impacted    []string `json:"impacted"` // id delle applicazioni
}

// ProposalFor prepara la proposta di incidente di Regia.
func ProposalFor(ctx context.Context, tx pgx.Tx, id string) (*Proposal, error) {
	x, err := loadIncident(ctx, tx, id, false)
	if err != nil {
		return nil, err
	}
	if x.RegiaIncident != nil {
		return nil, &Error{Status: 409, Code: "servicenow.incident_linked", Msg: "questo incidente di ServiceNow è già collegato all'incidente " + x.RegiaIncident.Key}
	}
	title := x.ShortDescription
	if title == "" {
		title = "Incidente " + x.Number
	}
	p := &Proposal{Source: *x, Title: truncate(title, 200), DetectedAt: x.OpenedAt.UTC().Format(time.RFC3339), Impacted: []string{}}
	if x.ResolvedAt != nil {
		r := x.ResolvedAt.UTC().Format(time.RFC3339)
		p.ResolvedAt = &r
	}
	if x.Application != nil {
		p.Impacted = append(p.Impacted, x.Application.ID)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Da ServiceNow: %s, priorità %d, stato «%s».\n", x.Number, x.Priority, x.StateLabel)
	if x.Service != "" {
		fmt.Fprintf(&b, "Servizio: %s.\n", x.Service)
	}
	fmt.Fprintf(&b, "Record: %s", x.URL)
	p.Description = b.String()
	return p, nil
}

// LinkRegiaIncident collega l'incidente di ServiceNow all'incidente di Regia nato dalla
// proposta (o già esistente): da qui in poi conta una volta sola, tramite quello di Regia.
func LinkRegiaIncident(ctx context.Context, tx pgx.Tx, actor engine.Actor, id, regiaIncidentID string) (*Incident, error) {
	cur, err := loadIncident(ctx, tx, id, true)
	if err != nil {
		return nil, err
	}
	if cur.RegiaIncident != nil && cur.RegiaIncident.ID != regiaIncidentID {
		return nil, &Error{Status: 409, Code: "servicenow.incident_linked", Msg: "questo incidente di ServiceNow è già collegato all'incidente " + cur.RegiaIncident.Key}
	}
	var ok bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from ext_incident i join elements e on e.tenant_id = i.tenant_id and e.id = i.element_id
		where i.element_id::text = $1 and e.deleted_at is null)`, regiaIncidentID).Scan(&ok); err != nil {
		return nil, err
	}
	if !ok {
		return nil, invalid("incident", "incidente di Regia inesistente")
	}
	if _, err := tx.Exec(ctx, `update servicenow_incidents set regia_incident_element_id = $2, dora_status = 'counted', decided_by = $3,
		decided_at = now(), updated_at = now() where id = $1`, id, regiaIncidentID, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := auditActor(ctx, tx, actor, "devsecops.servicenow.incident_link", id, map[string]any{"number": cur.Number, "incident": regiaIncidentID}); err != nil {
		return nil, err
	}
	return loadIncident(ctx, tx, id, false)
}

// ChangeFilter filtra le change.
type ChangeFilter struct {
	IntegrationID string
	ApplicationID string
	Limit         int
}

const selectChange = `
	select x.id, x.integration_id, x.sys_id, x.number, x.state, x.approval, x.type, x.planned_start, x.planned_end, x.actual_start, x.actual_end,
	  x.short_description, x.url, coalesce(r.name, ''), a.id, sa.key || '-' || a.number, a.title,
	  c.number, re.id, sre.key || '-' || re.number, re.title, x.updated_at
	from servicenow_changes x
	left join connector_repositories r on r.tenant_id = x.tenant_id and r.id = x.repository_id
	left join elements a on a.tenant_id = x.tenant_id and a.id = x.application_element_id and a.deleted_at is null
	left join spaces sa on sa.tenant_id = a.tenant_id and sa.id = a.space_id
	left join lateral (select c.number, c.request_element_id from changes c
	  where c.tenant_id = x.tenant_id and (c.number = upper(x.number) or upper(x.number) = any(c.aliases)) limit 1) c on true
	left join elements re on re.tenant_id = x.tenant_id and re.id = c.request_element_id and re.deleted_at is null
	left join spaces sre on sre.tenant_id = re.tenant_id and sre.id = re.space_id
	where true`

func scanChanges(rows pgx.Rows) ([]Change, error) {
	defer rows.Close()
	out := []Change{}
	for rows.Next() {
		var x Change
		var aID, aKey, aTitle, rID, rKey, rTitle *string
		if err := rows.Scan(&x.ID, &x.IntegrationID, &x.SysID, &x.Number, &x.State, &x.Approval, &x.Type, &x.PlannedStart, &x.PlannedEnd,
			&x.ActualStart, &x.ActualEnd, &x.ShortDescription, &x.URL, &x.Service, &aID, &aKey, &aTitle, &x.RegiaChange, &rID, &rKey, &rTitle, &x.UpdatedAt); err != nil {
			return nil, err
		}
		x.StateLabel, x.ApprovalLabel = ChangeStateLabel(x.State), ApprovalLabel(x.Approval)
		if aID != nil {
			x.Application = &Ref{ID: *aID, Key: deref(aKey), Title: deref(aTitle)}
		}
		if rID != nil {
			x.Request = &Ref{ID: *rID, Key: deref(rKey), Title: deref(rTitle)}
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Changes elenca le change, dalla più recente.
func Changes(ctx context.Context, tx pgx.Tx, f ChangeFilter) ([]Change, error) {
	q, args := selectChange, []any{}
	add := func(cond string, v any) {
		args = append(args, v)
		q += fmt.Sprintf(" and "+cond, len(args))
	}
	if f.IntegrationID != "" {
		if !validID(f.IntegrationID) {
			return []Change{}, nil
		}
		add("x.integration_id = $%d", f.IntegrationID)
	}
	if f.ApplicationID != "" {
		if !validID(f.ApplicationID) {
			return []Change{}, nil
		}
		add("x.application_element_id = $%d", f.ApplicationID)
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	args = append(args, f.Limit)
	rows, err := tx.Query(ctx, q+fmt.Sprintf(" order by coalesce(x.planned_start, x.sys_updated_on) desc, x.number limit $%d", len(args)), args...)
	if err != nil {
		return nil, err
	}
	return scanChanges(rows)
}

// ChangeByNumber trova la change di ServiceNow di un numero (o di un alias del change di
// Regia con quel numero). nil se ServiceNow non la conosce.
func ChangeByNumber(ctx context.Context, tx pgx.Tx, numbers []string) (*Change, error) {
	var up []string
	for _, n := range numbers {
		if n = strings.ToUpper(strings.TrimSpace(n)); n != "" {
			up = append(up, n)
		}
	}
	if len(up) == 0 {
		return nil, nil
	}
	rows, err := tx.Query(ctx, selectChange+` and upper(x.number) = any($1) order by x.sys_updated_on desc limit 1`, up)
	if err != nil {
		return nil, err
	}
	list, err := scanChanges(rows)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return &list[0], nil
}
