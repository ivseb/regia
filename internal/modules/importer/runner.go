package importer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/finance"
	"regia/internal/modules/work"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Passi della procedura guidata: caricamento (Upload) o connessione (Connect), mappatura
// (SaveMapping), anteprima (Preview), esecuzione (Start) e annullamento (Rollback).

// UploadInput descrive un file CSV caricato.
type UploadInput struct {
	Source    string // jira_csv | youtrack_csv | csv
	FileName  string
	Delimiter string // vuoto = automatico
}

type hashingReader struct {
	r io.Reader
	h hash.Hash
	n int64
}

func (h *hashingReader) Read(p []byte) (int, error) {
	n, err := h.r.Read(p)
	h.h.Write(p[:n])
	h.n += int64(n)
	return n, err
}

// Upload legge il CSV, ne fa l'analisi e crea l'esecuzione con la mappatura proposta.
func (s *Service) Upload(ctx context.Context, tenantID string, actor engine.Actor, in UploadInput, r io.Reader) (*Run, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	system, ok := sourceSystem[in.Source]
	if !ok || strings.HasSuffix(in.Source, "_api") {
		return nil, engine.InvalidValue("source", "origine non valida: jira_csv, youtrack_csv o csv")
	}
	if len([]rune(in.Delimiter)) > 1 {
		return nil, engine.InvalidCode("validation.importer.delimiter_single", "delimiter", "il separatore è un solo carattere")
	}
	hr := &hashingReader{r: r, h: sha256.New()}
	header, rows, lines, err := readCSV(hr, in.Delimiter)
	if err != nil {
		return nil, engine.InvalidParams("validation.importer.file_unreadable", "file", map[string]any{"detail": err}, "%v", err)
	}
	if len(rows) == 0 {
		return nil, engine.InvalidCode("validation.importer.file_no_rows", "file", "il file non ha righe dopo l'intestazione")
	}
	ds := &Dataset{System: system, Format: "csv", Header: header, Rows: rows, Lines: lines}
	issues, cols, warns := issuesOf(ds, nil)
	a := analyze(ds, issues, cols, warns)
	if system != "csv" && !hasRole(cols, RoleSummary) {
		return nil, engine.InvalidParams("validation.importer.file_not_export", "file", map[string]any{"system": map[string]string{"jira": "Jira", "youtrack": "YouTrack"}[system]}, "il file non sembra un export di %s: manca la colonna Summary", map[string]string{"jira": "Jira", "youtrack": "YouTrack"}[system])
	}
	name := strings.TrimSpace(in.FileName)
	if name == "" {
		name = "export.csv"
	}
	var out *Run
	err = s.db.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		id, err := s.createRun(ctx, tx, actor, in.Source, name, hr.h.Sum(nil), "pending", ds, a)
		if err != nil {
			return err
		}
		out, err = s.run(ctx, tx, id)
		return err
	})
	return out, err
}

func hasRole(cols []string, role string) bool {
	for _, c := range cols {
		if c == role {
			return true
		}
	}
	return false
}

func (s *Service) createRun(ctx context.Context, tx pgx.Tx, actor engine.Actor, source, name string, sha []byte, status string, ds *Dataset, a *Analysis) (string, error) {
	id := db.NewID()
	var mapping []byte
	if a != nil {
		m, err := suggest(ctx, tx, a)
		if err != nil {
			return "", err
		}
		mapping = jsonb(m)
	}
	var analysis []byte
	total := 0
	if a != nil {
		analysis, total = jsonb(a), a.Issues
	}
	if _, err := tx.Exec(ctx, `insert into import_runs (tenant_id, id, target, source, file_name, file_sha256, status, rows_total, analysis, mapping, created_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		id, Target, source, name, sha, status, total, analysis, mapping, nullable(actor.MemberID)); err != nil {
		return "", err
	}
	if ds != nil {
		if err := saveDataset(ctx, tx, id, ds); err != nil {
			return "", err
		}
	}
	return id, audit.Append(ctx, tx, audit.Event{Action: "import.upload", EntityType: "import_run", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"source": source, "rows": total, "file_sha256": hex.EncodeToString(sha)}})
}

// MappingResult è l'esecuzione con i problemi della mappatura salvata.
type MappingResult struct {
	Run      *Run      `json:"run"`
	Problems []Problem `json:"problems"`
}

// SaveMapping salva la mappatura dell'esecuzione (anche con problemi, che si mostrano). Se
// cambiano i ruoli delle colonne si rifà l'analisi. L'anteprima precedente decade.
func (s *Service) SaveMapping(ctx context.Context, tx pgx.Tx, actor engine.Actor, runID string, m Mapping) (*MappingResult, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	status, _, _, err := lockRun(ctx, tx, runID)
	if err != nil {
		return nil, err
	}
	if status != "pending" && status != "preview" && status != "failed" {
		return nil, engine.InvalidCode("validation.importer.mapping_locked", "status", "la mappatura si cambia prima dell'esecuzione")
	}
	ds, err := loadDataset(ctx, tx, runID)
	if err != nil {
		return nil, err
	}
	problems := m.check(ds.Header)
	cur, err := s.run(ctx, tx, runID)
	if err != nil {
		return nil, err
	}
	analysis := cur.Analysis
	if ds.Format == "csv" && len(m.Columns) == len(ds.Header) && (analysis == nil || strings.Join(analysis.Columns, "\x00") != strings.Join(m.Columns, "\x00")) {
		issues, cols, warns := issuesOf(ds, m.Columns)
		analysis = analyze(ds, issues, cols, warns)
	}
	if problems == nil {
		problems = []Problem{}
	}
	if _, err := tx.Exec(ctx, `update import_runs set mapping = $2, analysis = $3, status = 'pending', rows_total = $4 where id = $1`,
		runID, jsonb(m), jsonb(analysis), analysis.Issues); err != nil {
		return nil, err
	}
	run, err := s.run(ctx, tx, runID)
	return &MappingResult{Run: run, Problems: problems}, err
}

// Suggest ricalcola la mappatura proposta sui dati attuali (es. dopo aver invitato persone).
func (s *Service) Suggest(ctx context.Context, tx pgx.Tx, actor engine.Actor, runID string) (*Mapping, error) {
	r, err := s.GetRun(ctx, tx, actor, runID)
	if err != nil {
		return nil, err
	}
	if r.Analysis == nil {
		return nil, engine.InvalidCode("validation.importer.analysis_not_ready", "status", "l'analisi non è ancora pronta")
	}
	return suggest(ctx, tx, r.Analysis)
}

// Preview calcola il piano completo senza salvare: conteggi, errori e avvisi riga per
// riga e una prova sul database del primo lotto (scritto e annullato nella stessa
// transazione, con tutto ciò che l'import creerebbe).
func (s *Service) Preview(ctx context.Context, tenantID string, actor engine.Actor, runID string) (*Run, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	start := time.Now()
	var rep *Report
	var rowErrs []RowError
	err := s.db.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		status, _, _, err := lockRun(ctx, tx, runID)
		if err != nil {
			return err
		}
		if status != "pending" && status != "preview" && status != "failed" {
			return engine.InvalidCode("validation.importer.preview_before_run", "status", "l'anteprima si fa prima dell'esecuzione")
		}
		p, err := s.loadPlan(ctx, tx, actor, runID)
		if err != nil {
			return err
		}
		rep = p.report()
		rep.DryRun = true
		rowErrs = p.errors
		// Prova sul database: il primo lotto, con le sue relazioni, i commenti e le ore.
		if terr := s.trial(ctx, tx, p, runID); terr != nil {
			var ve *engine.ValidationError
			if errors.As(terr, &ve) {
				rep.Trial = "errore: " + ve.Error()
			} else {
				rep.Trial = "errore: " + terr.Error()
			}
		} else {
			rep.Trial = "ok"
		}
		rep.Messages = append([]string{}, p.warnings...)
		rep.DurationMS = time.Since(start).Milliseconds()
		return errTrial // si annulla tutto
	})
	if err != nil && !errors.Is(err, errTrial) {
		return nil, err
	}
	var out *Run
	err = s.db.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		if rowErrs == nil {
			rowErrs = []RowError{}
		}
		if _, err := tx.Exec(ctx, `update import_runs set status = 'preview', dry_run = true, report = $2, errors = $3, rows_ok = $4, rows_error = $5
			where id = $1`, runID, jsonb(rep), jsonb(rowErrs), rep.Created+rep.Updated, rep.Errors); err != nil {
			return err
		}
		var err error
		out, err = s.run(ctx, tx, runID)
		return err
	})
	return out, err
}

func (s *Service) trial(ctx context.Context, tx pgx.Tx, p *plan, runID string) error {
	sp, err := tx.Begin(ctx)
	if err != nil {
		return err
	}
	defer sp.Rollback(ctx) //nolint:errcheck // il savepoint si annulla comunque
	rec := &recorder{}
	if err := p.prepare(ctx, sp, s.eng, runID, rec); err != nil {
		return err
	}
	n := min(len(p.issues), s.batch)
	if n == 0 {
		return nil
	}
	if err := p.writeIssues(ctx, sp, runID, p.issues[:n], rec); err != nil {
		return err
	}
	in := map[int]bool{}
	for i := range n {
		in[i] = true
	}
	var links []pLink
	for _, l := range p.links {
		if (l.source < 0 || in[l.source]) && (l.target < 0 || in[l.target]) {
			links = append(links, l)
		}
	}
	if _, err := p.writeLinks(ctx, sp, runID, links, rec); err != nil {
		return err
	}
	var cs []commentRow
	var ws []worklogRow
	for _, pi := range p.issues[:n] {
		for _, c := range pi.comments {
			cs = append(cs, commentRow{pi, c})
		}
		for _, w := range pi.worklogs {
			ws = append(ws, worklogRow{pi, w})
		}
	}
	if err := p.writeComments(ctx, sp, runID, cs); err != nil {
		return err
	}
	_, err = p.writeWorklogs(ctx, sp, runID, ws)
	return err
}

// loadPlan rilegge mappatura e dati dell'esecuzione e calcola il piano.
func (s *Service) loadPlan(ctx context.Context, tx pgx.Tx, actor engine.Actor, runID string) (*plan, error) {
	var raw []byte
	if err := tx.QueryRow(ctx, `select mapping from import_runs where id = $1`, runID).Scan(&raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, engine.Required("mapping", "manca la mappatura")
	}
	var m Mapping
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	ds, err := loadDataset(ctx, tx, runID)
	if err != nil {
		return nil, err
	}
	ds.canDownload = s.files != nil
	if probs := m.check(ds.Header); len(probs) > 0 {
		return nil, engine.InvalidParams("validation.importer.mapping_problem", "mapping."+probs[0].Section, map[string]any{"detail": itemPrefix(probs[0].Item) + probs[0].Message}, "%s%s", itemPrefix(probs[0].Item), probs[0].Message)
	}
	return buildPlan(ctx, tx, actor, ds, &m, runID)
}

func itemPrefix(item string) string {
	if item == "" {
		return ""
	}
	return item + ": "
}

// Start avvia l'esecuzione in background (dopo l'anteprima), oppure riprende un'esecuzione
// interrotta o fallita: le chiavi d'origine rendono la ripresa sicura.
func (s *Service) Start(ctx context.Context, tenantID string, actor engine.Actor, runID string, conn *Connection) (*Run, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	var out *Run
	err := s.db.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		status, source, hb, err := lockRun(ctx, tx, runID)
		if err != nil {
			return err
		}
		interrupted := status == "running" && (hb == nil || time.Since(*hb) > staleAfter)
		if status != "preview" && status != "failed" && !interrupted {
			if status == "pending" {
				return engine.InvalidCode("validation.importer.preview_first", "status", "prima fai l'anteprima")
			}
			return engine.InvalidParams("validation.importer.state_not_startable", "status", map[string]any{"status": status}, "l'import non si può avviare in questo stato (%s)", status)
		}
		if strings.HasSuffix(source, "_api") {
			var m Mapping
			var raw []byte
			if err := tx.QueryRow(ctx, `select mapping from import_runs where id = $1`, runID).Scan(&raw); err != nil {
				return err
			}
			_ = json.Unmarshal(raw, &m)
			if conn != nil && conn.Token != "" {
				s.keepToken(runID, *conn)
			}
			if m.Include.Attachments && s.files != nil {
				if _, ok := s.token(runID); !ok {
					return engine.InvalidCode("validation.importer.token_for_attachments", "token", "per scaricare gli allegati serve di nuovo il token dell'API (non si salva)")
				}
			}
		}
		var running int
		if err := tx.QueryRow(ctx, `select count(*) from import_runs where target = $1 and status = 'running' and id <> $2`, Target, runID).Scan(&running); err != nil {
			return err
		}
		if running > 0 {
			return engine.Duplicate("status", "c'è già un import in corso: aspetta che finisca")
		}
		if _, err := tx.Exec(ctx, `update import_runs set status = 'running', dry_run = false, started_at = coalesce(started_at, now()),
			heartbeat_at = now(), finished_at = null, progress = $2 where id = $1`, runID, jsonb(Progress{Phase: "preparazione"})); err != nil {
			return err
		}
		if err := audit.Append(ctx, tx, audit.Event{Action: "import.start", EntityType: "import_run", EntityID: runID,
			ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"resume": interrupted || status == "failed"}}); err != nil {
			return err
		}
		out, err = s.run(ctx, tx, runID)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.wg.Add(1)
	go s.execute(tenantID, runID, actor)
	return out, nil
}

func (s *Service) execute(tenantID, runID string, actor engine.Actor) {
	defer s.wg.Done()
	start := time.Now()
	err := s.runImport(s.ctx, tenantID, runID, actor, start)
	if err == nil {
		return
	}
	if s.ctx.Err() != nil {
		s.log.Warn("import interrotto dallo spegnimento", "run", runID)
		return // resta «running»: dopo il riavvio si vede interrotto e si riprende
	}
	s.log.Error("import fallito", "run", runID, "errore", err)
	msg := err.Error()
	var ve *engine.ValidationError
	if errors.As(err, &ve) {
		msg = ve.Error()
	}
	_ = s.db.InTenant(context.Background(), tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(context.Background(), `update import_runs set status = 'failed', finished_at = now(),
			progress = progress || jsonb_build_object('message', $2::text) where id = $1`, runID, msg)
		return err
	})
}

func (s *Service) progress(ctx context.Context, tx pgx.Tx, runID string, pr Progress) error {
	_, err := tx.Exec(ctx, `update import_runs set progress = $2, heartbeat_at = now() where id = $1`, runID, jsonb(pr))
	return err
}

func (s *Service) runImport(ctx context.Context, tenantID, runID string, actor engine.Actor, start time.Time) error {
	var p *plan
	rec := &recorder{}
	inTx := func(fn func(tx pgx.Tx) error) error { return s.db.InTenant(ctx, tenantID, db.TenantOpts{}, fn) }
	// 1. Piano e preparazione.
	if err := inTx(func(tx pgx.Tx) error {
		var err error
		if p, err = s.loadPlan(ctx, tx, actor, runID); err != nil {
			return err
		}
		if err := p.prepare(ctx, tx, s.eng, runID, rec); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `update import_runs set space_id = $2 where id = $1`, runID, p.space.id); err != nil {
			return err
		}
		return s.progress(ctx, tx, runID, Progress{Phase: "elementi", Total: len(p.issues)})
	}); err != nil {
		return err
	}
	// 2. Elementi, a lotti (i genitori prima dei figli).
	for i := 0; i < len(p.issues); i += s.batch {
		j := min(i+s.batch, len(p.issues))
		if err := inTx(func(tx pgx.Tx) error {
			if err := p.writeIssues(ctx, tx, runID, p.issues[i:j], rec); err != nil {
				return err
			}
			return s.progress(ctx, tx, runID, Progress{Phase: "elementi", Done: j, Total: len(p.issues)})
		}); err != nil {
			return fmt.Errorf("elementi %d-%d: %w", i+1, j, err)
		}
	}
	// 3. Collegamenti.
	relations := 0
	for i := 0; i < len(p.links); i += 5000 {
		j := min(i+5000, len(p.links))
		if err := inTx(func(tx pgx.Tx) error {
			n, err := p.writeLinks(ctx, tx, runID, p.links[i:j], rec)
			relations += n
			if err != nil {
				return err
			}
			return s.progress(ctx, tx, runID, Progress{Phase: "collegamenti", Done: j, Total: len(p.links)})
		}); err != nil {
			return err
		}
	}
	// 4. Commenti.
	var cs []commentRow
	var ws []worklogRow
	for _, pi := range p.issues {
		for _, c := range pi.comments {
			cs = append(cs, commentRow{pi, c})
		}
		for _, w := range pi.worklogs {
			ws = append(ws, worklogRow{pi, w})
		}
	}
	for i := 0; i < len(cs); i += 2000 {
		j := min(i+2000, len(cs))
		if err := inTx(func(tx pgx.Tx) error {
			if err := importMode(ctx, tx); err != nil {
				return err
			}
			if err := p.writeComments(ctx, tx, runID, cs[i:j]); err != nil {
				return err
			}
			return s.progress(ctx, tx, runID, Progress{Phase: "commenti", Done: j, Total: len(cs)})
		}); err != nil {
			return err
		}
	}
	// 5. Ore: in work_logs; il costo lo calcola il modulo finance in una transazione di sistema.
	for i := 0; i < len(ws); i += 2000 {
		j := min(i+2000, len(ws))
		var ids []string
		if err := inTx(func(tx pgx.Tx) error {
			var err error
			if ids, err = p.writeWorklogs(ctx, tx, runID, ws[i:j]); err != nil {
				return err
			}
			return s.progress(ctx, tx, runID, Progress{Phase: "ore", Done: j, Total: len(ws)})
		}); err != nil {
			return err
		}
		if err := s.db.InTenant(ctx, tenantID, db.TenantOpts{Finance: true}, func(tx pgx.Tx) error {
			_, err := finance.CostWorkLogs(ctx, tx, actor.MemberID, ids)
			return err
		}); err != nil {
			s.log.Warn("costi delle ore importate non calcolati", "run", runID, "errore", err)
		}
	}
	// 6. Allegati (solo dall'API e con l'archivio dei file).
	atts, err := s.downloadAttachments(ctx, tenantID, runID, p, rec)
	if err != nil {
		return err
	}
	// 7. Ordine manuale, report, audit.
	return inTx(func(tx pgx.Tx) error {
		if err := work.FillRanks(ctx, tx, p.space.id); err != nil {
			return err
		}
		rep := p.report()
		rep.Relations = relations
		for _, pi := range p.issues {
			if pi.sprint >= 0 {
				rep.Relations++
			}
		}
		rep.Attachments = atts
		rep.DurationMS = time.Since(start).Milliseconds()
		errs := p.errors
		if errs == nil {
			errs = []RowError{}
		}
		if _, err := tx.Exec(ctx, `update import_runs set status = 'done', finished_at = now(), report = $2, errors = $3, rows_ok = $4,
			rows_error = $5, progress = $6, heartbeat_at = now() where id = $1`, runID, jsonb(rep), jsonb(errs), rep.Created+rep.Updated, rep.Errors,
			jsonb(Progress{Phase: "completato", Done: len(p.issues), Total: len(p.issues)})); err != nil {
			return err
		}
		s.forgetToken(runID)
		return audit.Append(ctx, tx, audit.Event{Action: "import.done", EntityType: "import_run", EntityID: runID,
			ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
			Payload: map[string]any{"created": rep.Created, "updated": rep.Updated, "comments": rep.Comments, "worklogs": rep.Worklogs,
				"relations": rep.Relations, "errors": rep.Errors, "space_id": p.space.id, "duration_ms": rep.DurationMS}})
	})
}
