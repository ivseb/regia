package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/db"
)

// PermProcessAdmin è il permesso dell'amministratore di processo (sdlc.md §1).
const PermProcessAdmin = "process.admin"

// BundleFormat identifica il formato di export e import dei processi.
const BundleFormat = "regia.process/v1"

// VersionInfo descrive una versione di un processo.
type VersionInfo struct {
	ID          string     `json:"id"`
	Number      int        `json:"number"`
	Status      string     `json:"status"`
	Notes       string     `json:"notes"`
	CreatedAt   time.Time  `json:"created_at"`
	CreatedBy   *string    `json:"created_by"`
	PublishedAt *time.Time `json:"published_at"`
	PublishedBy *string    `json:"published_by"`
	Elements    int        `json:"elements"`
	// Vigore (migrazione 0069): da quando vale per le richieste nuove, fine facoltativa, deprecazione,
	// regola di transizione e stato derivato (draft | scheduled | in_force | deprecated | concluded | withdrawn).
	EffectiveFrom *time.Time      `json:"effective_from"`
	EffectiveTo   *time.Time      `json:"effective_to"`
	DeprecatedAt  *time.Time      `json:"deprecated_at"`
	Transition    json.RawMessage `json:"transition,omitempty"`
	Lifecycle     string          `json:"lifecycle"`
}

// ProcessInfo è un processo con le sue versioni.
type ProcessInfo struct {
	ID          string        `json:"id"`
	Key         string        `json:"key"`
	Name        string        `json:"name"`
	Description string        `json:"description"`
	TypeKey     string        `json:"type"`
	TypeName    string        `json:"type_name"`
	Priority    int           `json:"priority"`
	AppliesWhen string        `json:"applies_when"`
	Archived    bool          `json:"archived"`
	UpdatedAt   time.Time     `json:"updated_at"`
	Versions    []VersionInfo `json:"versions"`
	// Current e Draft: definizioni della versione corrente e della bozza, se esistono (solo nel dettaglio).
	Current       *Workflow `json:"current,omitempty"`
	Draft         *Workflow `json:"draft,omitempty"`
	DraftProblems []Problem `json:"draft_problems,omitempty"`
	// Warnings: avvisi sull'applicabilità (solo nel dettaglio), per esempio un pareggio di priorità.
	Warnings []string `json:"warnings,omitempty"`
}

func requireProcessAdmin(a Actor) error {
	if !a.Has(PermProcessAdmin) {
		return ErrForbidden
	}
	return nil
}

// ListProcesses elenca i processi del tenant con le versioni.
func (s *Service) ListProcesses(ctx context.Context, tx pgx.Tx, actor Actor) ([]ProcessInfo, error) {
	if err := requireProcessAdmin(actor); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `
		select p.id, p.key, p.name, p.description, t.key, t.name, p.priority, p.applies_when, p.archived, p.updated_at
		from process_definitions p join element_types t on t.tenant_id = p.tenant_id and t.id = p.type_id
		order by t.name, p.priority, p.name`)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (ProcessInfo, error) {
		var p ProcessInfo
		err := r.Scan(&p.ID, &p.Key, &p.Name, &p.Description, &p.TypeKey, &p.TypeName, &p.Priority, &p.AppliesWhen, &p.Archived, &p.UpdatedAt)
		return p, err
	})
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Versions, err = versions(ctx, tx, list[i].ID); err != nil {
			return nil, err
		}
	}
	return list, nil
}

func versions(ctx context.Context, tx pgx.Tx, processID string) ([]VersionInfo, error) {
	rows, err := tx.Query(ctx, `
		select v.id, v.number, v.status, v.notes, v.created_at, cm.display_name, v.published_at, pm.display_name,
		       (select count(*) from elements e where e.process_version_id = v.id and e.deleted_at is null),
		       v.effective_from, v.effective_to, v.deprecated_at, v.transition
		from process_versions v
		left join members cm on cm.tenant_id = v.tenant_id and cm.id = v.created_by
		left join members pm on pm.tenant_id = v.tenant_id and pm.id = v.published_by
		where v.process_id = $1 order by v.number desc`, processID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (VersionInfo, error) {
		var v VersionInfo
		var tr []byte
		err := r.Scan(&v.ID, &v.Number, &v.Status, &v.Notes, &v.CreatedAt, &v.CreatedBy, &v.PublishedAt, &v.PublishedBy,
			&v.Elements, &v.EffectiveFrom, &v.EffectiveTo, &v.DeprecatedAt, &tr)
		v.Transition = tr
		v.Lifecycle = Lifecycle(v.Status, v.EffectiveFrom, v.EffectiveTo, v.DeprecatedAt, now)
		return v, err
	})
}

// resolveProcess accetta l'id o la chiave del processo.
func resolveProcess(ctx context.Context, tx pgx.Tx, ref string) (id, typeID string, err error) {
	err = tx.QueryRow(ctx, `select id, type_id from process_definitions where id::text = $1 or key = $1`, ref).Scan(&id, &typeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return id, typeID, err
}

// GetProcess restituisce il processo con la definizione corrente e la bozza.
func (s *Service) GetProcess(ctx context.Context, tx pgx.Tx, actor Actor, ref string) (*ProcessInfo, error) {
	if err := requireProcessAdmin(actor); err != nil {
		return nil, err
	}
	id, typeID, err := resolveProcess(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	p := &ProcessInfo{}
	if err := tx.QueryRow(ctx, `
		select p.id, p.key, p.name, p.description, t.key, t.name, p.priority, p.applies_when, p.archived, p.updated_at
		from process_definitions p join element_types t on t.tenant_id = p.tenant_id and t.id = p.type_id
		where p.id = $1`, id).Scan(&p.ID, &p.Key, &p.Name, &p.Description, &p.TypeKey, &p.TypeName, &p.Priority,
		&p.AppliesWhen, &p.Archived, &p.UpdatedAt); err != nil {
		return nil, err
	}
	if p.Versions, err = versions(ctx, tx, id); err != nil {
		return nil, err
	}
	if !p.Archived {
		if p.Warnings, err = priorityWarnings(ctx, tx, typeID, id, p.Priority, p.AppliesWhen); err != nil {
			return nil, err
		}
	}
	for _, v := range p.Versions {
		switch v.Status {
		case "published":
			if p.Current, err = loadVersion(ctx, tx, v.ID); err != nil {
				return nil, err
			}
		case "draft":
			if p.Draft, err = loadVersion(ctx, tx, v.ID); err != nil {
				return nil, err
			}
			p.DraftProblems = Validate(p.Draft)
			refs, err := s.validateRefs(ctx, tx, p.Draft, typeID)
			if err != nil {
				return nil, err
			}
			p.DraftProblems = append(p.DraftProblems, refs...)
		}
	}
	return p, nil
}

// VersionDefinition restituisce la definizione di una versione per numero.
func (s *Service) VersionDefinition(ctx context.Context, tx pgx.Tx, actor Actor, ref string, number int) (*Workflow, error) {
	if err := requireProcessAdmin(actor); err != nil {
		return nil, err
	}
	id, _, err := resolveProcess(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	var vid string
	if err := tx.QueryRow(ctx, "select id from process_versions where process_id = $1 and number = $2", id, number).Scan(&vid); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return loadVersion(ctx, tx, vid)
}

// ProcessInput crea o modifica le impostazioni di un processo.
type ProcessInput struct {
	Key         string  `json:"key"`
	Name        *string `json:"name"`
	Description *string `json:"description"`
	TypeKey     string  `json:"type"`
	Priority    *int    `json:"priority"`
	AppliesWhen *string `json:"applies_when"`
	Archived    *bool   `json:"archived"`
	// CopyFrom: chiave di un processo esistente da cui copiare la versione corrente come prima bozza.
	CopyFrom string `json:"copy_from"`
}

// CreateProcess crea un processo con una prima bozza (vuota o copiata).
func (s *Service) CreateProcess(ctx context.Context, tx pgx.Tx, actor Actor, in ProcessInput, def *Workflow) (*ProcessInfo, error) {
	if err := requireProcessAdmin(actor); err != nil {
		return nil, err
	}
	if !gateKeyRe.MatchString(in.Key) {
		return nil, InvalidValue("key", "chiave non valida (minuscole, cifre e _, inizia con una lettera)")
	}
	name := ""
	if in.Name != nil {
		name = strings.TrimSpace(*in.Name)
	}
	if name == "" {
		return nil, invalidCode("validation.name_required", "name", "il nome è obbligatorio")
	}
	t, err := LoadType(ctx, tx, in.TypeKey)
	if err != nil {
		return nil, err
	}
	var exists bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from process_definitions where key = $1)", in.Key).Scan(&exists); err != nil {
		return nil, err
	}
	if exists {
		return nil, Duplicate("key", "esiste già un processo con chiave %q", in.Key)
	}
	if in.AppliesWhen != nil && *in.AppliesWhen != "" {
		if _, err := ParseCondition(*in.AppliesWhen); err != nil {
			return nil, InvalidValue("applies_when", "condizione non valida: %v", err)
		}
	}
	if def == nil && in.CopyFrom != "" {
		src, _, err := resolveProcess(ctx, tx, in.CopyFrom)
		if err != nil {
			return nil, NotFound("copy_from", "process", "processo %q inesistente", in.CopyFrom)
		}
		var vid string
		if err := tx.QueryRow(ctx, "select id from process_versions where process_id = $1 and status = 'published'", src).Scan(&vid); err != nil {
			return nil, invalid("copy_from", "il processo %q non ha una versione pubblicata", in.CopyFrom)
		}
		if def, err = loadVersion(ctx, tx, vid); err != nil {
			return nil, err
		}
	}
	if def == nil {
		def = starterDefinition(t)
	}
	id := db.NewID()
	desc, when := "", ""
	if in.Description != nil {
		desc = *in.Description
	}
	if in.AppliesWhen != nil {
		when = strings.TrimSpace(*in.AppliesWhen)
	}
	priority := 100
	if in.Priority != nil {
		priority = *in.Priority
	} else if priority, err = defaultPriority(ctx, tx, t.ID, when); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `insert into process_definitions (tenant_id, id, key, name, description, type_id, priority, applies_when, created_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8)`, id, in.Key, name, desc, t.ID, priority, when, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if err := saveDraft(ctx, tx, actor, id, def, "Prima bozza"); err != nil {
		return nil, err
	}
	if err := auditProcess(ctx, tx, actor, "process.create", id, map[string]any{"key": in.Key, "type": t.Key}); err != nil {
		return nil, err
	}
	return s.GetProcess(ctx, tx, actor, id)
}

// starterDefinition è la bozza minima per un tipo senza processo: le fasi del suo workflow.
func starterDefinition(t *TypeInfo) *Workflow {
	w := &Workflow{}
	if t.Workflow != nil {
		b, _ := json.Marshal(t.Workflow)
		_ = json.Unmarshal(b, w)
	}
	if len(w.Phases) == 0 {
		for _, st := range t.options {
			w.Phases = append(w.Phases, Phase{Name: st.Name, Category: st.Category})
		}
	}
	if w.Initial == "" && len(w.Phases) > 0 {
		w.Initial = w.Phases[0].Name
	}
	if w.Transitions == nil {
		w.Transitions = []Transition{}
	}
	return w
}

// UpdateProcess modifica nome, descrizione, priorità, applicabilità o archiviazione.
func (s *Service) UpdateProcess(ctx context.Context, tx pgx.Tx, actor Actor, ref string, in ProcessInput) (*ProcessInfo, error) {
	if err := requireProcessAdmin(actor); err != nil {
		return nil, err
	}
	id, _, err := resolveProcess(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	if in.Name != nil && strings.TrimSpace(*in.Name) == "" {
		return nil, invalidCode("validation.name_required", "name", "il nome è obbligatorio")
	}
	if in.AppliesWhen != nil && *in.AppliesWhen != "" {
		if _, err := ParseCondition(*in.AppliesWhen); err != nil {
			return nil, InvalidValue("applies_when", "condizione non valida: %v", err)
		}
	}
	// Senza condizione il processo vale per tutti gli elementi del tipo: la priorità è l'unica cosa che
	// decide chi vince contro gli altri processi, quindi va detta (non si lascia quella di prima per caso).
	if in.AppliesWhen != nil && strings.TrimSpace(*in.AppliesWhen) == "" && in.Priority == nil {
		return nil, invalidCode("validation.process.priority_required", "priority", "la priorità è obbligatoria quando il processo non ha una condizione: vale per tutti gli elementi del tipo")
	}
	if in.Priority != nil && *in.Priority < 0 {
		return nil, invalidCode("validation.process.priority_negative", "priority", "la priorità è un numero intero, zero o positivo")
	}
	if _, err := tx.Exec(ctx, `update process_definitions set
		name = coalesce($2, name), description = coalesce($3, description), priority = coalesce($4, priority),
		applies_when = coalesce($5, applies_when), archived = coalesce($6, archived), updated_at = now()
		where id = $1`, id, in.Name, in.Description, in.Priority, in.AppliesWhen, in.Archived); err != nil {
		return nil, err
	}
	if err := auditProcess(ctx, tx, actor, "process.update", id, map[string]any{"input": in}); err != nil {
		return nil, err
	}
	return s.GetProcess(ctx, tx, actor, id)
}

// SaveDraft salva la bozza (anche con errori, che si vedono nei problemi: chi configura
// non perde il lavoro). La pubblicazione rifiuta le bozze con errori.
func (s *Service) SaveDraft(ctx context.Context, tx pgx.Tx, actor Actor, ref string, raw []byte, notes string) (*ProcessInfo, error) {
	if err := requireProcessAdmin(actor); err != nil {
		return nil, err
	}
	id, _, err := resolveProcess(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	w, err := DecodeDefinition(raw)
	if err != nil {
		return nil, err
	}
	if err := saveDraft(ctx, tx, actor, id, w, notes); err != nil {
		return nil, err
	}
	if err := auditProcess(ctx, tx, actor, "process.draft.save", id, nil); err != nil {
		return nil, err
	}
	return s.GetProcess(ctx, tx, actor, id)
}

func saveDraft(ctx context.Context, tx pgx.Tx, actor Actor, processID string, w *Workflow, notes string) error {
	def, err := json.Marshal(w)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `update process_versions set definition = $2, notes = $3, created_by = $4, created_at = now()
		where process_id = $1 and status = 'draft'`, processID, def, notes, nullable(actor.MemberID))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		_, err = tx.Exec(ctx, `insert into process_versions (tenant_id, id, process_id, number, status, definition, notes, created_by)
			values (app_tenant(), $1, $2, (select coalesce(max(number), 0) + 1 from process_versions where process_id = $2), 'draft', $3, $4, $5)`,
			db.NewID(), processID, def, notes, nullable(actor.MemberID))
	}
	if err == nil {
		_, err = tx.Exec(ctx, "update process_definitions set updated_at = now() where id = $1", processID)
	}
	return err
}

// DiscardDraft cancella la bozza.
func (s *Service) DiscardDraft(ctx context.Context, tx pgx.Tx, actor Actor, ref string) error {
	if err := requireProcessAdmin(actor); err != nil {
		return err
	}
	id, _, err := resolveProcess(ctx, tx, ref)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "delete from process_versions where process_id = $1 and status = 'draft'", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return invalid("draft", "non c'è nessuna bozza")
	}
	return auditProcess(ctx, tx, actor, "process.draft.discard", id, nil)
}

// Publish pubblica la bozza: diventa la versione corrente e le nuove richieste la usano.
// Gli elementi già avviati restano sulla loro versione (vedi Migrate). Le fasi nuove
// diventano opzioni dell'insieme di stati del tipo.
func (s *Service) Publish(ctx context.Context, tx pgx.Tx, actor Actor, ref, notes string) (*ProcessInfo, []Problem, error) {
	return s.PublishWith(ctx, tx, actor, ref, PublishInput{Notes: notes})
}

// PublishWith è Publish con data di entrata in vigore, data di fine e regola di transizione (versioni.go).
func (s *Service) PublishWith(ctx context.Context, tx pgx.Tx, actor Actor, ref string, in PublishInput) (*ProcessInfo, []Problem, error) {
	notes := in.Notes
	if err := requireProcessAdmin(actor); err != nil {
		return nil, nil, err
	}
	id, typeID, err := resolveProcess(ctx, tx, ref)
	if err != nil {
		return nil, nil, err
	}
	// Una pubblicazione alla volta per processo.
	if _, err := tx.Exec(ctx, "select 1 from process_definitions where id = $1 for update", id); err != nil {
		return nil, nil, err
	}
	var vid string
	var number int
	var raw []byte
	err = tx.QueryRow(ctx, "select id, number, definition from process_versions where process_id = $1 and status = 'draft'", id).Scan(&vid, &number, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, invalid("draft", "non c'è nessuna bozza da pubblicare")
	}
	if err != nil {
		return nil, nil, err
	}
	w, err := DecodeDefinition(raw)
	if err != nil {
		return nil, nil, err
	}
	problems := Validate(w)
	refs, err := s.validateRefs(ctx, tx, w, typeID)
	if err != nil {
		return nil, nil, err
	}
	problems = append(problems, refs...)
	if HasErrors(problems) {
		if err := RuleCodeError(problems); err != nil {
			return nil, problems, err
		}
		return nil, problems, invalid("definition", "la bozza ha errori: correggili prima di pubblicare")
	}
	if err := ensurePhaseOptions(ctx, tx, typeID, w); err != nil {
		return nil, nil, err
	}
	if notes == "" {
		var cur string
		_ = tx.QueryRow(ctx, "select notes from process_versions where id = $1", vid).Scan(&cur)
		notes = cur
	}
	if _, err := tx.Exec(ctx, `update process_versions set notes = $2 where id = $1`, vid, notes); err != nil {
		return nil, nil, err
	}
	if err := s.publishVersion(ctx, tx, actor, id, typeID, vid, number, in); err != nil {
		return nil, nil, err
	}
	p, err := s.GetProcess(ctx, tx, actor, id)
	return p, problems, err
}

// ensurePhaseOptions crea le opzioni di stato mancanti e aggiorna ordine, colore e categoria
// di quelle esistenti secondo la versione pubblicata. Le opzioni non più usate restano
// (servono alle versioni precedenti e alla storia).
func ensurePhaseOptions(ctx context.Context, tx pgx.Tx, typeID string, w *Workflow) error {
	var setID *string
	if err := tx.QueryRow(ctx, "select state_set_id from element_types where id = $1", typeID).Scan(&setID); err != nil {
		return err
	}
	if setID == nil {
		id := db.NewID()
		if _, err := tx.Exec(ctx, `insert into option_sets (tenant_id, id, name, kind) values (app_tenant(), $1, 'Fasi', 'state')`, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "update element_types set state_set_id = $2 where id = $1", typeID, id); err != nil {
			return err
		}
		setID = &id
	}
	for i, p := range w.Phases {
		color := nullable(p.Color)
		lb := labelsJSON(p.Labels)
		tag, err := tx.Exec(ctx, `update options set ordinal = $3, category = $4, color = coalesce($5, color), archived = false, labels = $6::jsonb
			where option_set_id = $1 and lower(name) = lower($2)`, *setID, p.Name, i, p.Category, color, string(lb))
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			if _, err := tx.Exec(ctx, `insert into options (tenant_id, id, option_set_id, name, color, ordinal, category, labels)
				values (app_tenant(), $1, $2, $3, $4, $5, $6, $7::jsonb)`, db.NewID(), *setID, p.Name, color, i, p.Category, string(lb)); err != nil {
				return err
			}
		}
	}
	return nil
}

// Bundle è il formato di export e import (anche dei pacchetti di partenza).
type Bundle struct {
	Format      string    `json:"format"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Type        string    `json:"type"`
	Priority    int       `json:"priority"`
	AppliesWhen string    `json:"applies_when,omitempty"`
	Version     int       `json:"version,omitempty"`
	Notes       string    `json:"notes,omitempty"`
	ExportedAt  string    `json:"exported_at,omitempty"`
	Definition  *Workflow `json:"definition"`
}

// Export restituisce la versione indicata (0 = corrente, altrimenti la bozza se non ce n'è una corrente).
func (s *Service) Export(ctx context.Context, tx pgx.Tx, actor Actor, ref string, number int) (*Bundle, error) {
	p, err := s.GetProcess(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	b := &Bundle{Format: BundleFormat, Key: p.Key, Name: p.Name, Description: p.Description, Type: p.TypeKey,
		Priority: p.Priority, AppliesWhen: p.AppliesWhen, ExportedAt: time.Now().UTC().Format(time.RFC3339)}
	for _, v := range p.Versions {
		if (number == 0 && v.Status == "published") || (number != 0 && v.Number == number) {
			b.Version, b.Notes = v.Number, v.Notes
			b.Definition, err = loadVersion(ctx, tx, v.ID)
			return b, err
		}
	}
	if number == 0 && p.Draft != nil {
		for _, v := range p.Versions {
			if v.Status == "draft" {
				b.Version, b.Notes = v.Number, v.Notes
			}
		}
		b.Definition = p.Draft
		return b, nil
	}
	return nil, ErrNotFound
}

// ParseBundle legge un bundle JSON, rifiutando i campi sconosciuti.
func ParseBundle(raw []byte) (*Bundle, error) {
	var head struct {
		Format     string          `json:"format"`
		Definition json.RawMessage `json:"definition"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, InvalidValue("bundle", "JSON non valido: %v", err)
	}
	if head.Format != BundleFormat {
		return nil, InvalidValue("format", "formato %q non riconosciuto (atteso %s)", head.Format, BundleFormat)
	}
	b := &Bundle{}
	if err := json.Unmarshal(raw, b); err != nil {
		return nil, InvalidValue("bundle", "JSON non valido: %v", err)
	}
	def, err := DecodeDefinition(head.Definition)
	if err != nil {
		return nil, err
	}
	b.Definition = def
	return b, nil
}

// Import porta un bundle nel tenant: se il processo esiste diventa la sua bozza,
// altrimenti si crea il processo. Con publish = true la bozza si pubblica subito.
func (s *Service) Import(ctx context.Context, tx pgx.Tx, actor Actor, b *Bundle, publish bool) (*ProcessInfo, []Problem, error) {
	if err := requireProcessAdmin(actor); err != nil {
		return nil, nil, err
	}
	notes := b.Notes
	if notes == "" {
		notes = "Importato"
	}
	id, _, err := resolveProcess(ctx, tx, b.Key)
	switch {
	case errors.Is(err, ErrNotFound):
		name, desc, when, prio := b.Name, b.Description, b.AppliesWhen, b.Priority
		if prio == 0 {
			prio = 100
		}
		p, err := s.CreateProcess(ctx, tx, actor, ProcessInput{Key: b.Key, Name: &name, Description: &desc, TypeKey: b.Type,
			Priority: &prio, AppliesWhen: &when}, b.Definition)
		if err != nil {
			return nil, nil, err
		}
		id = p.ID
		if err := saveDraft(ctx, tx, actor, id, b.Definition, notes); err != nil {
			return nil, nil, err
		}
	case err != nil:
		return nil, nil, err
	default:
		if err := saveDraft(ctx, tx, actor, id, b.Definition, notes); err != nil {
			return nil, nil, err
		}
	}
	if err := auditProcess(ctx, tx, actor, "process.import", id, map[string]any{"key": b.Key, "publish": publish}); err != nil {
		return nil, nil, err
	}
	if publish {
		return s.Publish(ctx, tx, actor, id, notes)
	}
	p, err := s.GetProcess(ctx, tx, actor, id)
	if err != nil {
		return nil, nil, err
	}
	return p, p.DraftProblems, nil
}

// MigrationGroup è un gruppo di elementi da migrare: stessa versione di partenza, stessa fase.
type MigrationGroup struct {
	FromVersion int    `json:"from_version"`
	Phase       string `json:"phase"`
	Count       int    `json:"count"`
	TargetPhase string `json:"target_phase"` // vuoto = nessuna fase corrispondente nella versione di arrivo
	OK          bool   `json:"ok"`
}

// MigrationPlan è l'anteprima di una migrazione (sdlc.md §7).
type MigrationPlan struct {
	ToVersion int              `json:"to_version"`
	Groups    []MigrationGroup `json:"groups"`
	Total     int              `json:"total"`
	Blocked   int              `json:"blocked"`
}

// MigrationInput: versione di arrivo (0 = corrente), versioni di partenza (vuoto = tutte le altre),
// corrispondenza tra fasi per quelle rinominate o tolte.
type MigrationInput struct {
	ToVersion    int               `json:"to_version"`
	FromVersions []int             `json:"from_versions"`
	PhaseMap     map[string]string `json:"phase_map"`
}

func (s *Service) migrationPlan(ctx context.Context, tx pgx.Tx, processID string, in MigrationInput) (*MigrationPlan, string, *Workflow, error) {
	var toID string
	var toNum int
	q := "select id, number from process_versions where process_id = $1 and status = 'published'"
	args := []any{processID}
	if in.ToVersion != 0 {
		q = "select id, number from process_versions where process_id = $1 and number = $2 and status <> 'draft'"
		args = append(args, in.ToVersion)
	}
	if err := tx.QueryRow(ctx, q, args...).Scan(&toID, &toNum); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, "", nil, NotFound("to_version", "", "versione di arrivo inesistente o non pubblicata")
		}
		return nil, "", nil, err
	}
	target, err := loadVersion(ctx, tx, toID)
	if err != nil {
		return nil, "", nil, err
	}
	rows, err := tx.Query(ctx, `
		select v.number, coalesce(o.name, ''), count(*)
		from elements e
		join process_versions v on v.tenant_id = e.tenant_id and v.id = e.process_version_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where v.process_id = $1 and v.id <> $2 and e.deleted_at is null
		  and (coalesce(cardinality($3::int[]), 0) = 0 or v.number = any($3))
		group by 1, 2 order by 1, 2`, processID, toID, in.FromVersions)
	if err != nil {
		return nil, "", nil, err
	}
	plan := &MigrationPlan{ToVersion: toNum, Groups: []MigrationGroup{}}
	var g MigrationGroup
	if _, err := pgx.ForEachRow(rows, []any{&g.FromVersion, &g.Phase, &g.Count}, func() error {
		mg := g
		name := mg.Phase
		if m, ok := in.PhaseMap[name]; ok && m != "" {
			name = m
		}
		for _, p := range target.Phases {
			if strings.EqualFold(p.Name, name) {
				mg.TargetPhase, mg.OK = p.Name, true
			}
		}
		plan.Total += mg.Count
		if !mg.OK {
			plan.Blocked += mg.Count
		}
		plan.Groups = append(plan.Groups, mg)
		return nil
	}); err != nil {
		return nil, "", nil, err
	}
	return plan, toID, target, nil
}

// PreviewMigration mostra cosa succederebbe migrando gli elementi alla versione di arrivo.
func (s *Service) PreviewMigration(ctx context.Context, tx pgx.Tx, actor Actor, ref string, in MigrationInput) (*MigrationPlan, error) {
	if err := requireProcessAdmin(actor); err != nil {
		return nil, err
	}
	id, _, err := resolveProcess(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	plan, _, _, err := s.migrationPlan(ctx, tx, id, in)
	return plan, err
}

// Migrate sposta gli elementi sulla versione di arrivo. Si rifiuta se qualche fase non ha
// corrispondenza: chi configura deve indicarla in PhaseMap. Ogni elemento migrato ha
// una voce nella storia; l'audit registra la migrazione con i numeri.
func (s *Service) Migrate(ctx context.Context, tx pgx.Tx, actor Actor, ref string, in MigrationInput) (*MigrationPlan, error) {
	if err := requireProcessAdmin(actor); err != nil {
		return nil, err
	}
	id, typeID, err := resolveProcess(ctx, tx, ref)
	if err != nil {
		return nil, err
	}
	plan, toID, target, err := s.migrationPlan(ctx, tx, id, in)
	if err != nil {
		return nil, err
	}
	if plan.Blocked > 0 {
		return plan, invalid("phase_map", "%d elementi sono in fasi che la versione %d non ha: indica dove portarli", plan.Blocked, plan.ToVersion)
	}
	if err := s.moveGroups(ctx, tx, actor, id, typeID, toID, plan.ToVersion, target, plan.Groups); err != nil {
		return nil, err
	}
	if err := auditProcess(ctx, tx, actor, "process.migrate", id, map[string]any{
		"to_version": plan.ToVersion, "total": plan.Total, "groups": plan.Groups, "phase_map": in.PhaseMap}); err != nil {
		return nil, err
	}
	return plan, nil
}

// CheckCondition analizza una condizione e, se c'è un elemento, la valuta sui suoi fatti.
type ConditionCheck struct {
	OK     bool     `json:"ok"`
	Error  string   `json:"error,omitempty"`
	Keys   []string `json:"keys"`
	Result *bool    `json:"result,omitempty"`
	Facts  Facts    `json:"facts,omitempty"`
}

func (s *Service) CheckCondition(ctx context.Context, tx pgx.Tx, actor Actor, src, elementRef string) (*ConditionCheck, error) {
	out := &ConditionCheck{Keys: []string{}}
	c, err := ParseCondition(src)
	if err != nil {
		out.Error = err.Error()
	} else {
		out.OK = true
		out.Keys = append(out.Keys, c.Keys()...)
	}
	if elementRef == "" {
		return out, nil
	}
	el, err := s.Get(ctx, tx, actor, elementRef)
	if err != nil {
		return nil, err
	}
	if out.Facts, err = s.ElementFacts(ctx, tx, el); err != nil {
		return nil, err
	}
	if out.OK {
		r := c.Eval(out.Facts)
		out.Result = &r
	}
	return out, nil
}

// defaultPriority sceglie la priorità di un processo nuovo senza pareggiare con gli altri del
// tipo (numero più basso = prima). Con una condizione passa davanti agli altri, perché è un caso
// particolare; senza condizione vale per tutto e va dopo, così non prende gli elementi che hanno
// già un processo più specifico. Il primo processo di un tipo ha 100.
func defaultPriority(ctx context.Context, tx pgx.Tx, typeID, when string) (int, error) {
	var lo, hi *int
	if err := tx.QueryRow(ctx, "select min(priority), max(priority) from process_definitions where type_id = $1 and not archived", typeID).Scan(&lo, &hi); err != nil {
		return 0, err
	}
	switch {
	case lo == nil:
		return 100, nil
	case when != "":
		return max(*lo-10, 1), nil
	default:
		return *hi + 10, nil
	}
}

// priorityWarnings segnala i processi dello stesso tipo con la stessa priorità: a parità, se
// valgono entrambi per un elemento, decide l'ordine alfabetico delle chiavi, che non è una scelta
// di nessuno («bug_critico» prenderebbe tutti i bug prima del processo standard).
func priorityWarnings(ctx context.Context, tx pgx.Tx, typeID, selfID string, priority int, when string) ([]string, error) {
	rows, err := tx.Query(ctx, `select name, applies_when from process_definitions
		where type_id = $1 and id <> $2 and not archived and priority = $3 order by name`, typeID, selfID, priority)
	if err != nil {
		return nil, err
	}
	type other struct{ name, when string }
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (other, error) {
		var o other
		return o, r.Scan(&o.name, &o.when)
	})
	if err != nil {
		return nil, err
	}
	var out []string
	for _, o := range list {
		switch {
		case strings.TrimSpace(o.when) == strings.TrimSpace(when):
			out = append(out, fmt.Sprintf("Stessa priorità (%d) e stessa condizione di «%s»: per gli elementi a cui valgono entrambi decide l'ordine alfabetico delle chiavi. Dai a uno dei due un numero diverso (più basso = prima) o una condizione diversa.", priority, o.name))
		default:
			out = append(out, fmt.Sprintf("Stessa priorità (%d) di «%s»: se le due condizioni valgono insieme, decide l'ordine alfabetico delle chiavi. Meglio un numero diverso (più basso = prima).", priority, o.name))
		}
	}
	return out, nil
}

// moveGroups sposta gli elementi dei gruppi sulla versione di arrivo (nella fase corrispondente),
// con una voce nella storia di ciascuno. È la macchina di migrazione, usata dalla migrazione
// esplicita e dalla regola di transizione all'entrata in vigore.
func (s *Service) moveGroups(ctx context.Context, tx pgx.Tx, actor Actor, processID, typeID, toID string, toNumber int, target *Workflow, groups []MigrationGroup) error {
	t, err := loadTypeByID(ctx, tx, typeID)
	if err != nil {
		return err
	}
	states := phaseStates(target, t.options)
	for _, g := range groups {
		st, ok := findState(states, g.TargetPhase)
		if !ok {
			return fmt.Errorf("fase %q senza opzione di stato", g.TargetPhase)
		}
		var fromID string
		if err := tx.QueryRow(ctx, "select id from process_versions where process_id = $1 and number = $2", processID, g.FromVersion).Scan(&fromID); err != nil {
			return err
		}
		// Storia per ogni elemento, poi lo spostamento, in due istruzioni sullo stesso insieme.
		if _, err := tx.Exec(ctx, `
			insert into activities (tenant_id, element_id, actor_id, kind, field_key, added, removed)
			select e.tenant_id, e.id, $4, 'process', 'migration',
			       jsonb_build_object('version', $5::int, 'state', $3::text),
			       jsonb_build_object('version', $6::int, 'state', $2::text)
			from elements e left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
			where e.process_version_id = $1 and e.deleted_at is null and coalesce(o.name, '') = $2`,
			fromID, g.Phase, st.Name, nullable(actor.MemberID), toNumber, g.FromVersion); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			update elements e set process_version_id = $2, state_id = $4, version = e.version + 1
			from options o
			where e.process_version_id = $1 and e.deleted_at is null and o.tenant_id = e.tenant_id and o.id = e.state_id and o.name = $3`,
			fromID, toID, g.Phase, st.ID); err != nil {
			return err
		}
	}
	return nil
}
