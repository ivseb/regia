package knowledge

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Template di pagina. Quelli di partenza valgono per tutti gli spazi (space_id null) e li
// modifica l'amministratore del tenant; ogni spazio può aggiungere i suoi. Nel testo
// {{titolo}}, {{data}} e {{autore}} si sostituiscono alla creazione della pagina.

// Template è un modello di pagina.
type Template struct {
	ID           string `json:"id"`
	SpaceKey     string `json:"space_key,omitempty"` // vuoto = tutti gli spazi
	Key          string `json:"key"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Body         string `json:"body"`
	Controlled   bool   `json:"controlled"`
	ReviewMonths *int   `json:"review_months"`
	Archived     bool   `json:"archived"`
	CanEdit      bool   `json:"can_edit"`
}

func months(n int) *int { return &n }

var starterTemplates = []Template{
	{Key: "runbook", Name: "Runbook", Description: "Procedura operativa di un servizio: avvio, arresto, controlli, incidenti tipici.", Body: `> [!NOTA] Servizio, responsabile e reperibilità sono all'inizio: chi apre il runbook durante un incidente deve trovarli subito.

| | |
|---|---|
| Servizio | |
| Responsabile | |
| Reperibilità | |
| Ultima verifica | {{data}} |

## Panoramica
Cosa fa il servizio, da cosa dipende e chi lo usa.

## Avvio e arresto
1.
2.

## Controlli di salute
-

## Incidenti tipici
### Sintomo
**Causa probabile:**

**Rimedio:**
` + "```" + `
comando
` + "```" + `

> [!ATTENZIONE] Indica qui le operazioni irreversibili e chi deve approvarle.

## Contatti ed escalation
-
`},
	{Key: "adr", Name: "Decisione di architettura (ADR)", Description: "Una decisione, il contesto, le alternative scartate e le conseguenze.", Body: `| | |
|---|---|
| Stato | Proposta |
| Data | {{data}} |
| Autore | {{autore}} |

## Contesto
Quale problema, quali vincoli (normativi, tecnici, di costo).

## Decisione
Cosa si fa, in una frase.

## Alternative considerate
| alternativa | pro | contro |
|---|---|---|
| | | |

## Conseguenze
-

## Collegamenti
Richieste e applicazioni interessate (es. DEM-12, APP-3).
`},
	{Key: "policy", Name: "Policy", Description: "Documento controllato con scopo, ambito, regole, ruoli ed eccezioni; approvazione e revisione annuale.", Controlled: true, ReviewMonths: months(12), Body: `> [!NOTA] Documento controllato: vale la versione approvata. Le modifiche passano da revisori e approvatori.

## Scopo

## Ambito di applicazione

## Riferimenti normativi
-

## Regole
1.

## Ruoli e responsabilità
| ruolo | responsabilità |
|---|---|
| | |

## Eccezioni
Come si chiede una deroga e chi la approva.

## Storia del documento
Le versioni, gli autori e le approvazioni sono nello storico della pagina.
`},
	{Key: "verbale", Name: "Verbale di riunione", Description: "Partecipanti, ordine del giorno, decisioni e azioni con responsabile e scadenza.", Body: `| | |
|---|---|
| Data | {{data}} |
| Redatto da | {{autore}} |
| Partecipanti | |

## Ordine del giorno
1.

## Discussione

## Decisioni
-

## Azioni
| azione | responsabile | scadenza |
|---|---|---|
| | | |
`},
	{Key: "guida", Name: "Guida", Description: "Istruzioni passo passo per chi usa un sistema o un processo.", Body: `## A chi serve

## Prima di iniziare
-

## Passi
1.
2.

> [!SUGGERIMENTO] Aggiungi le immagini trascinandole nell'editor o incollandole.

## Domande frequenti
`},
}

var templateKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)

// Templates elenca i template disponibili nello spazio: quelli di tutti gli spazi e i suoi.
func (s *Service) Templates(ctx context.Context, tx pgx.Tx, actor engine.Actor, spaceKey string) ([]Template, error) {
	sp, err := s.space(ctx, tx, actor, spaceKey)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		select t.id, coalesce(s.key, ''), t.key, t.name, t.description, t.body, t.controlled, t.review_months, t.archived
		from kb_templates t left join spaces s on s.tenant_id = t.tenant_id and s.id = t.space_id
		where t.space_id is null or t.space_id = $1
		order by t.space_id nulls first, t.ordinal, t.name`, sp.ID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Template, error) {
		var t Template
		err := r.Scan(&t.ID, &t.SpaceKey, &t.Key, &t.Name, &t.Description, &t.Body, &t.Controlled, &t.ReviewMonths, &t.Archived)
		if t.SpaceKey == "" {
			t.CanEdit = actor.Has("*") || actor.Has("tenant.admin")
		} else {
			t.CanEdit = sp.Rights.Manage
		}
		return t, err
	})
	if out == nil {
		out = []Template{}
	}
	return out, err
}

// TemplateInput crea o modifica un template.
type TemplateInput struct {
	Key          string  `json:"key"`
	Name         *string `json:"name"`
	Description  *string `json:"description"`
	Body         *string `json:"body"`
	Controlled   *bool   `json:"controlled"`
	ReviewMonths *int    `json:"review_months"`
	Archived     *bool   `json:"archived"`
}

// CreateTemplate aggiunge un template allo spazio (chi amministra lo spazio).
func (s *Service) CreateTemplate(ctx context.Context, tx pgx.Tx, actor engine.Actor, spaceKey string, in TemplateInput) (*Template, error) {
	sp, err := s.space(ctx, tx, actor, spaceKey)
	if err != nil {
		return nil, err
	}
	if !sp.Rights.Manage {
		return nil, engine.ErrForbidden
	}
	in.Key = strings.TrimSpace(strings.ToLower(in.Key))
	if !templateKeyRe.MatchString(in.Key) {
		return nil, engine.InvalidCode("validation.knowledge.template_key_format", "key", "chiave del template: minuscole, cifre e _, iniziando con una lettera")
	}
	if in.Name == nil || strings.TrimSpace(*in.Name) == "" {
		return nil, engine.InvalidCode("validation.name_required", "name", "il nome è obbligatorio")
	}
	t := Template{Key: in.Key, Name: strings.TrimSpace(*in.Name), SpaceKey: sp.Key, CanEdit: true}
	if in.Description != nil {
		t.Description = strings.TrimSpace(*in.Description)
	}
	if in.Body != nil {
		b, ok := cleanBody(*in.Body)
		if !ok {
			return nil, engine.InvalidValue("body", "testo non valido o troppo lungo")
		}
		t.Body = b
	}
	if in.Controlled != nil {
		t.Controlled = *in.Controlled
	}
	if err := validMonths(in.ReviewMonths); err != nil {
		return nil, err
	}
	t.ReviewMonths = in.ReviewMonths
	t.ID = db.NewID()
	tag, err := tx.Exec(ctx, `insert into kb_templates (tenant_id, id, space_id, key, name, description, body, controlled, review_months, ordinal)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, (select coalesce(max(ordinal), 0) + 10 from kb_templates where space_id = $2))
		on conflict do nothing`, t.ID, sp.ID, t.Key, t.Name, t.Description, t.Body, t.Controlled, t.ReviewMonths)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 0 {
		return nil, engine.Duplicate("key", "esiste già un template %q nello spazio", t.Key)
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "kb.template.create", EntityType: "kb_template", EntityID: t.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"space_id": sp.ID, "key": t.Key}}); err != nil {
		return nil, err
	}
	return &t, nil
}

func validMonths(m *int) error {
	if m != nil && (*m < 1 || *m > 60) {
		return engine.OutOfRange("review_months", 1, 60, "la revisione periodica va da 1 a 60 mesi")
	}
	return nil
}

// UpdateTemplate modifica un template: quelli dello spazio chi lo amministra, quelli di tutti
// gli spazi l'amministratore del tenant.
func (s *Service) UpdateTemplate(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in TemplateInput) (*Template, error) {
	var spaceID *string
	var spaceKey string
	err := tx.QueryRow(ctx, `select t.space_id::text, coalesce(s.key, '') from kb_templates t
		left join spaces s on s.tenant_id = t.tenant_id and s.id = t.space_id where t.id::text = $1 for update of t`, id).Scan(&spaceID, &spaceKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if spaceID == nil {
		if !actor.Has("*") && !actor.Has("tenant.admin") {
			return nil, engine.ErrForbidden
		}
	} else {
		sp, err := s.space(ctx, tx, actor, spaceKey)
		if err != nil {
			return nil, err
		}
		if !sp.Rights.Manage {
			return nil, engine.ErrForbidden
		}
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		return nil, engine.InvalidCode("validation.name_required", "name", "il nome è obbligatorio")
	}
	if err := validMonths(in.ReviewMonths); err != nil {
		return nil, err
	}
	var body *string
	if in.Body != nil {
		b, ok := cleanBody(*in.Body)
		if !ok {
			return nil, engine.InvalidValue("body", "testo non valido o troppo lungo")
		}
		body = &b
	}
	var name *string
	if in.Name != nil {
		n := strings.TrimSpace(*in.Name)
		name = &n
	}
	if _, err := tx.Exec(ctx, `update kb_templates set name = coalesce($2, name), description = coalesce($3, description),
		body = coalesce($4, body), controlled = coalesce($5, controlled),
		review_months = case when $6::boolean then $7 else review_months end, archived = coalesce($8, archived)
		where id = $1`, id, name, in.Description, body, in.Controlled, in.ReviewMonths != nil, in.ReviewMonths, in.Archived); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "kb.template.update", EntityType: "kb_template", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP}); err != nil {
		return nil, err
	}
	var t Template
	err = tx.QueryRow(ctx, `select t.id, coalesce(s.key, ''), t.key, t.name, t.description, t.body, t.controlled, t.review_months, t.archived
		from kb_templates t left join spaces s on s.tenant_id = t.tenant_id and s.id = t.space_id where t.id = $1`, id).
		Scan(&t.ID, &t.SpaceKey, &t.Key, &t.Name, &t.Description, &t.Body, &t.Controlled, &t.ReviewMonths, &t.Archived)
	t.CanEdit = true
	return &t, err
}

// templateFor carica un template utilizzabile nello spazio (anche per chiave).
func templateFor(ctx context.Context, tx pgx.Tx, spaceID, ref string) (*Template, error) {
	var t Template
	err := tx.QueryRow(ctx, `select id, key, name, body, controlled, review_months from kb_templates
		where not archived and (space_id is null or space_id = $1) and (id::text = $2 or key = $2)
		order by space_id nulls last limit 1`, spaceID, ref).Scan(&t.ID, &t.Key, &t.Name, &t.Body, &t.Controlled, &t.ReviewMonths)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.NotFound("template", "", "template inesistente")
	}
	return &t, err
}

// fill sostituisce le variabili del template.
func fill(body, title, author string, now time.Time) string {
	r := strings.NewReplacer("{{titolo}}", title, "{{data}}", now.Format("02/01/2006"), "{{autore}}", author)
	return r.Replace(body)
}
