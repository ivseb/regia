package risk

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Storia delle bozze delle notifiche normative (ondata 5 dell'AI, migrazione 0088). La bozza
// preparata dall'assistente e rivista dalla persona si salva come versione, con il testo e i
// campi del modello; da lì in poi anche le bozze salvate a mano diventano versioni, così la storia
// resta completa. Senza bozze dell'assistente il modulo resta com'era: SaveDraft non scrive
// versioni. La notifica non si invia da Regia: ogni versione si esporta (Markdown o JSON).

// Fonti di una versione.
const (
	DraftByAssistant = "assistente"
	DraftByPerson    = "persona"
)

// NoticeField è un campo del modello di notifica com'è stato salvato.
type NoticeField struct {
	Num    string `json:"num"`
	Name   string `json:"name"`
	Value  string `json:"value"`
	Status string `json:"status"` // compilato | da_verificare | mancante
	Source string `json:"source,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

// DraftVersion è una versione salvata della bozza di una notifica.
type DraftVersion struct {
	Version   int           `json:"version"`
	Content   string        `json:"content"`
	Fields    []NoticeField `json:"fields"`
	Source    string        `json:"source"`
	CreatedBy string        `json:"created_by"`
	CreatedAt time.Time     `json:"created_at"`
}

// DraftVersionInput salva una bozza come versione nuova.
type DraftVersionInput struct {
	Content string        `json:"content"`
	Fields  []NoticeField `json:"fields"`
	Source  string        `json:"source"`
}

// SaveDraftVersion salva il contenuto come bozza (come SaveDraft) e ne registra una versione con i
// campi del modello (incident.manage).
func (s *Service) SaveDraftVersion(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in DraftVersionInput) (*Notification, *DraftVersion, error) {
	if err := requireIncident(actor); err != nil {
		return nil, nil, err
	}
	if in.Source != DraftByAssistant && in.Source != DraftByPerson {
		return nil, nil, engine.InvalidValue("source", "origine non ammessa (assistente, persona)")
	}
	if len(in.Fields) > 200 {
		return nil, nil, engine.InvalidCode("validation.risk.text_too_long", "fields", "troppi campi")
	}
	for i := range in.Fields {
		f := &in.Fields[i]
		f.Value = strings.TrimSpace(f.Value)
		if len(f.Value) > 10000 || len(f.Name) > 300 || len(f.Num) > 10 || len(f.Hint) > 1000 || len(f.Source) > 300 {
			return nil, nil, engine.InvalidCode("validation.risk.text_too_long", "fields", "campo troppo lungo")
		}
		if f.Status != "compilato" && f.Status != "da_verificare" && f.Status != "mancante" {
			return nil, nil, engine.InvalidValue("fields", "stato del campo non ammesso (compilato, da_verificare, mancante)")
		}
		if f.Value == "" {
			f.Status = "mancante"
		}
	}
	content := in.Content
	n, err := s.saveDraft(ctx, tx, actor, id, NotificationInput{Content: &content})
	if err != nil {
		return nil, nil, err
	}
	v, err := s.addDraftVersion(ctx, tx, actor, n, in.Fields, in.Source)
	if err != nil {
		return nil, nil, err
	}
	return n, v, nil
}

// addDraftVersion scrive la versione successiva e la segna nella storia dell'incidente.
func (s *Service) addDraftVersion(ctx context.Context, tx pgx.Tx, actor engine.Actor, n *Notification, fields []NoticeField, source string) (*DraftVersion, error) {
	if fields == nil {
		fields = []NoticeField{}
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	var version int
	if err := tx.QueryRow(ctx, `insert into regulatory_notification_drafts (tenant_id, notification_id, version, content, fields, source, created_by)
		values (app_tenant(), $1, coalesce((select max(version) from regulatory_notification_drafts where notification_id = $1), 0) + 1, $2, $3, $4, $5)
		returning version`, n.ID, n.Content, raw, source, nullable(actor.MemberID)).Scan(&version); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, n.IncidentID, actor.MemberID, "notification", "draft_version",
		map[string]any{"scheme": n.SchemeName, "stage": n.StageName, "version": version, "source": source}, nil); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "regulatory_notification.draft_version", "regulatory_notification", n.ID,
		map[string]any{"incident": n.IncidentID, "stage": n.StageKey, "version": version, "source": source}); err != nil {
		return nil, err
	}
	vs, err := s.versions(ctx, tx, n.ID, version)
	if err != nil || len(vs) == 0 {
		return nil, err
	}
	return &vs[0], nil
}

// hasDraftVersions dice se la notifica ha già una storia di bozze (iniziata dall'assistente).
func hasDraftVersions(ctx context.Context, tx pgx.Tx, id string) (bool, error) {
	var ok bool
	err := tx.QueryRow(ctx, `select exists (select 1 from regulatory_notification_drafts where notification_id = $1)`, id).Scan(&ok)
	return ok, err
}

func (s *Service) versions(ctx context.Context, tx pgx.Tx, notificationID string, only int) ([]DraftVersion, error) {
	rows, err := tx.Query(ctx, `select d.version, d.content, d.fields, d.source, coalesce(m.display_name, ''), d.created_at
		from regulatory_notification_drafts d left join members m on m.tenant_id = d.tenant_id and m.id = d.created_by
		where d.notification_id = $1 and ($2 = 0 or d.version = $2) order by d.version desc limit 100`, notificationID, only)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DraftVersion{}
	for rows.Next() {
		var v DraftVersion
		var raw []byte
		if err := rows.Scan(&v.Version, &v.Content, &raw, &v.Source, &v.CreatedBy, &v.CreatedAt); err != nil {
			return nil, err
		}
		v.Fields = []NoticeField{}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &v.Fields); err != nil {
				return nil, err
			}
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DraftVersions elenca le versioni della bozza di una notifica, dalla più recente (incident.manage).
func (s *Service) DraftVersions(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*Notification, []DraftVersion, error) {
	if err := requireIncident(actor); err != nil {
		return nil, nil, err
	}
	n, err := s.notification(ctx, tx, id, false)
	if err != nil {
		return nil, nil, err
	}
	vs, err := s.versions(ctx, tx, n.ID, 0)
	return n, vs, err
}

// DraftVersionOf restituisce una versione (0 = l'ultima) per l'esportazione (incident.manage).
func (s *Service) DraftVersionOf(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, version int) (*Notification, *DraftVersion, error) {
	n, vs, err := s.DraftVersions(ctx, tx, actor, id)
	if err != nil {
		return nil, nil, err
	}
	for i := range vs {
		if version == 0 || vs[i].Version == version {
			return n, &vs[i], nil
		}
	}
	return nil, nil, engine.ErrNotFound
}

// NotificationOf restituisce una notifica con i permessi di chi gestisce gli incidenti.
func (s *Service) NotificationOf(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*Notification, error) {
	if err := requireIncident(actor); err != nil {
		return nil, err
	}
	n, err := s.notification(ctx, tx, id, false)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	return n, err
}
