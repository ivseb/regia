// Package importer porta in Regia i progetti che un'azienda ha in Jira, YouTrack o in un
// foglio di calcolo: procedura guidata in passi (caricamento o connessione, analisi,
// mappatura salvabile come profilo, anteprima senza salvare, esecuzione in background a
// lotti, report e annullamento). docs/import.md spiega cosa si porta e cosa no.
//
// Clean-room: si leggono solo i formati di export pubblici e documentati (CSV di Jira e di
// YouTrack, le loro API REST con il token dell'utente, in sola lettura); i parser sono
// scritti da zero.
//
// Gli elementi importati sono elementi del motore come gli altri (tipi epic, story, task,
// bug e sprint del work management), scritti in blocco: senza passaggi di fase né regole
// (la storia degli stati dell'origine non si ricostruisce), con la chiave d'origine nel
// campo «origine» e in import_origins, così un secondo import aggiorna invece di duplicare.
package importer

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/collab"
	"regia/internal/platform/db"
)

// Target è il valore di import_profiles.target e import_runs.target per questo import.
const Target = "work_items"

// Sorgenti.
const (
	SourceJiraCSV     = "jira_csv"
	SourceYouTrackCSV = "youtrack_csv"
	SourceCSV         = "csv"
	SourceJiraAPI     = "jira_api"
	SourceYouTrackAPI = "youtrack_api"
)

var sourceSystem = map[string]string{SourceJiraCSV: "jira", SourceYouTrackCSV: "youtrack", SourceCSV: "csv", SourceJiraAPI: "jira", SourceYouTrackAPI: "youtrack"}

// staleAfter: un'esecuzione senza segni di vita da così tanto è interrotta (riavvio del server).
const staleAfter = 3 * time.Minute

// Options configura il servizio.
type Options struct {
	DB     *db.DB
	Engine *engine.Service
	// Files è l'archivio degli allegati (nil = allegati dell'API solo come link).
	Files *collab.FileStore
	// MaxAttachment è il limite di un allegato scaricato dall'API.
	MaxAttachment int64
	// AllowPrivate permette i connettori verso indirizzi interni (sviluppo, server on-premise).
	AllowPrivate bool
	Log          *slog.Logger
}

// Service gestisce gli import. Le esecuzioni girano in goroutine con un contesto proprio,
// chiuso da Close allo spegnimento.
type Service struct {
	db           *db.DB
	eng          *engine.Service
	files        *collab.FileStore
	maxAtt       int64
	allowPrivate bool
	log          *slog.Logger

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu sync.Mutex
	// tokens: i token delle API restano solo in memoria, per esecuzione (mai nel database).
	tokens map[string]apiCreds
	// batch: dimensione dei lotti (i test la riducono).
	batch int
}

type apiCreds struct {
	conn    Connection
	expires time.Time
}

func New(o Options) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.MaxAttachment <= 0 {
		o.MaxAttachment = 25 << 20
	}
	return &Service{db: o.DB, eng: o.Engine, files: o.Files, maxAtt: o.MaxAttachment, allowPrivate: o.AllowPrivate, log: o.Log,
		ctx: ctx, cancel: cancel, tokens: map[string]apiCreds{}, batch: 1000}
}

// Close ferma le esecuzioni in corso (restano «interrotte» e si riprendono) e aspetta.
func (s *Service) Close() {
	s.cancel()
	s.wg.Wait()
}

// SetBatchSize cambia la dimensione dei lotti (test).
func (s *Service) SetBatchSize(n int) {
	if n > 0 {
		s.batch = n
	}
}

// Wait aspetta la fine delle esecuzioni in background (test).
func (s *Service) Wait() { s.wg.Wait() }

// requireAdmin: l'import crea spazi, campi e membri, quindi serve l'amministratore.
func requireAdmin(a engine.Actor) error {
	if a.Has("tenant.admin") {
		return nil
	}
	return engine.ErrForbidden
}

// admin è l'attore con cui l'import chiama le funzioni dei moduli (campi, team, spazi):
// il permesso l'ha già verificato requireAdmin.
func admin(a engine.Actor) engine.Actor {
	a.Permissions = []string{"*"}
	return a
}

// Progress è l'avanzamento di un'esecuzione in background.
type Progress struct {
	Phase   string `json:"phase,omitempty"`
	Done    int    `json:"done"`
	Total   int    `json:"total"`
	Message string `json:"message,omitempty"`
}

// Run è un'esecuzione dell'import, dal caricamento al report.
type Run struct {
	ID          string     `json:"id"`
	Source      string     `json:"source"`
	System      string     `json:"system"`
	FileName    string     `json:"file_name"`
	Status      string     `json:"status"` // validating | pending | preview | running | done | failed | rolled_back
	ProfileID   *string    `json:"profile_id"`
	Profile     *string    `json:"profile"`
	RowsTotal   int        `json:"rows_total"`
	RowsOK      int        `json:"rows_ok"`
	RowsError   int        `json:"rows_error"`
	Analysis    *Analysis  `json:"analysis,omitempty"`
	Mapping     *Mapping   `json:"mapping,omitempty"`
	Progress    Progress   `json:"progress"`
	Report      *Report    `json:"report,omitempty"`
	Errors      []RowError `json:"errors"`
	SpaceID     *string    `json:"space_id"`
	SpaceKey    *string    `json:"space_key"`
	CreatedBy   *string    `json:"created_by"`
	CreatedAt   time.Time  `json:"created_at"`
	StartedAt   *time.Time `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at"`
	HeartbeatAt *time.Time `json:"heartbeat_at"`
	// Interrupted: in esecuzione ma ferma da minuti (server riavviato): si può riprendere.
	Interrupted bool `json:"interrupted"`
	// NeedsToken: origine API senza token in memoria (dopo un riavvio va inserito di nuovo).
	NeedsToken bool `json:"needs_token"`
}

const runColumns = `r.id::text, coalesce(r.source, ''), r.file_name, r.status, r.profile_id::text, p.name, coalesce(r.rows_total, 0),
	coalesce(r.rows_ok, 0), coalesce(r.rows_error, 0), r.analysis, r.mapping, r.progress, r.report, r.errors, r.space_id::text, sp.key,
	m.display_name, r.created_at, r.started_at, r.finished_at, r.heartbeat_at`

const runFrom = ` from import_runs r
	left join import_profiles p on p.tenant_id = r.tenant_id and p.id = r.profile_id
	left join spaces sp on sp.tenant_id = r.tenant_id and sp.id = r.space_id
	left join members m on m.tenant_id = r.tenant_id and m.id = r.created_by`

func (s *Service) scanRun(row pgx.CollectableRow, full bool) (Run, error) {
	var x Run
	var analysis, mapping, progress, report, errs []byte
	if err := row.Scan(&x.ID, &x.Source, &x.FileName, &x.Status, &x.ProfileID, &x.Profile, &x.RowsTotal, &x.RowsOK, &x.RowsError,
		&analysis, &mapping, &progress, &report, &errs, &x.SpaceID, &x.SpaceKey, &x.CreatedBy, &x.CreatedAt, &x.StartedAt,
		&x.FinishedAt, &x.HeartbeatAt); err != nil {
		return x, err
	}
	x.System = sourceSystem[x.Source]
	_ = json.Unmarshal(progress, &x.Progress)
	if len(report) > 0 {
		x.Report = &Report{}
		_ = json.Unmarshal(report, x.Report)
	}
	x.Errors = []RowError{}
	if full {
		if len(analysis) > 0 {
			x.Analysis = &Analysis{}
			_ = json.Unmarshal(analysis, x.Analysis)
		}
		if len(mapping) > 0 {
			x.Mapping = &Mapping{}
			_ = json.Unmarshal(mapping, x.Mapping)
		}
		if len(errs) > 0 {
			_ = json.Unmarshal(errs, &x.Errors)
		}
	}
	if x.Status == "running" && (x.HeartbeatAt == nil || time.Since(*x.HeartbeatAt) > staleAfter) {
		x.Interrupted = true
	}
	if strings.HasSuffix(x.Source, "_api") && (x.Status == "pending" || x.Status == "preview" || x.Interrupted || x.Status == "failed") {
		s.mu.Lock()
		c, ok := s.tokens[x.ID]
		s.mu.Unlock()
		x.NeedsToken = !ok || time.Now().After(c.expires)
	}
	return x, nil
}

// Runs elenca le ultime esecuzioni (senza analisi ed errori).
func (s *Service) Runs(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]Run, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `select `+runColumns+runFrom+` where r.target = $1 order by r.created_at desc limit 50`, Target)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Run, error) { return s.scanRun(r, false) })
	if out == nil {
		out = []Run{}
	}
	return out, err
}

// GetRun restituisce un'esecuzione con analisi, mappatura, errori e report.
func (s *Service) GetRun(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*Run, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	return s.run(ctx, tx, id)
}

func (s *Service) run(ctx context.Context, tx pgx.Tx, id string) (*Run, error) {
	if !engine.IsUUID(strings.ToLower(id)) {
		return nil, engine.ErrNotFound
	}
	rows, err := tx.Query(ctx, `select `+runColumns+runFrom+` where r.id = $1 and r.target = $2`, strings.ToLower(id), Target)
	if err != nil {
		return nil, err
	}
	runs, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Run, error) { return s.scanRun(r, true) })
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, engine.ErrNotFound
	}
	return &runs[0], nil
}

// lockRun blocca la riga dell'esecuzione e ne restituisce stato e sorgente.
func lockRun(ctx context.Context, tx pgx.Tx, id string) (status, source string, heartbeat *time.Time, err error) {
	if !engine.IsUUID(strings.ToLower(id)) {
		return "", "", nil, engine.ErrNotFound
	}
	err = tx.QueryRow(ctx, `select status, coalesce(source, ''), heartbeat_at from import_runs where id = $1 and target = $2 for update`,
		strings.ToLower(id), Target).Scan(&status, &source, &heartbeat)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", nil, engine.ErrNotFound
	}
	return status, source, heartbeat, err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func jsonb(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		return []byte("null")
	}
	return b
}
