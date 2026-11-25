package importer

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/webhooks"
	"regia/internal/modules/collab"
	"regia/internal/platform/db"
)

// Connettori in sola lettura per le API REST pubbliche di Jira (Cloud e Data Center) e di
// YouTrack, con il token dell'utente. Il token resta in memoria per l'esecuzione (lettura
// e, se richiesti, allegati) e non si salva: dopo un riavvio si inserisce di nuovo.
// L'URL passa dagli stessi controlli SSRF dei webhook.

// Connection sono i dati per collegarsi a un'API.
type Connection struct {
	Source     string `json:"source"`               // jira_api | youtrack_api
	BaseURL    string `json:"base_url"`             // https://azienda.atlassian.net, https://youtrack.azienda.it
	Deployment string `json:"deployment,omitempty"` // Jira: cloud | server
	Email      string `json:"email,omitempty"`      // Jira Cloud: email dell'account del token
	Token      string `json:"token"`
	Project    string `json:"project"`         // chiave del progetto
	Query      string `json:"query,omitempty"` // JQL o query di YouTrack aggiuntiva
	Limit      int    `json:"limit,omitempty"` // al massimo N elementi (0 = tutti)
}

const maxAPIIssues = 100_000

func (s *Service) keepToken(runID string, c Connection) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[runID] = apiCreds{conn: c, expires: time.Now().Add(24 * time.Hour)}
}

func (s *Service) token(runID string) (Connection, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.tokens[runID]
	if !ok || time.Now().After(c.expires) {
		return Connection{}, false
	}
	return c.conn, true
}

func (s *Service) forgetToken(runID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.tokens, runID)
}

// api è un client verso una delle due API.
type api struct {
	c      Connection
	base   *url.URL
	client *http.Client
}

func (s *Service) newAPI(c Connection) (*api, error) {
	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	norm, err := webhooks.CheckURL(c.BaseURL, s.allowPrivate)
	if err != nil {
		var we *webhooks.Error
		if errors.As(err, &we) {
			return nil, engine.InvalidParams("validation.importer.base_url_invalid", "base_url", map[string]any{"detail": we.Msg}, "%s", we.Msg)
		}
		return nil, engine.InvalidValue("base_url", "indirizzo non valido")
	}
	u, _ := url.Parse(norm)
	if strings.TrimSpace(c.Token) == "" {
		return nil, engine.Required("token", "serve il token dell'API")
	}
	if c.Source == SourceJiraAPI && c.Deployment != "server" && strings.TrimSpace(c.Email) == "" {
		return nil, engine.InvalidCode("validation.importer.jira_email_needed", "email", "per Jira Cloud serve l'email dell'account a cui appartiene il token")
	}
	return &api{c: c, base: u, client: webhooks.SafeClient(s.allowPrivate, 60*time.Second)}, nil
}

func (a *api) auth(req *http.Request) {
	switch {
	case a.c.Source == SourceJiraAPI && a.c.Deployment != "server":
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(strings.TrimSpace(a.c.Email)+":"+strings.TrimSpace(a.c.Token))))
	default:
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(a.c.Token))
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Regia-import")
}

// sameHost: le credenziali si mandano solo all'host configurato.
func (a *api) sameHost(raw string) (*url.URL, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, false
	}
	if !u.IsAbs() {
		u = a.base.ResolveReference(u)
	}
	return u, strings.EqualFold(u.Host, a.base.Host) && (u.Scheme == "https" || u.Scheme == a.base.Scheme)
}

func (a *api) get(ctx context.Context, path string, q url.Values, out any) error {
	u := *a.base
	u.Path = strings.TrimRight(a.base.Path, "/") + path
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	a.auth(req)
	var resp *http.Response
	for attempt := 0; ; attempt++ {
		resp, err = a.client.Do(req)
		if err != nil {
			return fmt.Errorf("connessione a %s non riuscita: %v", a.base.Host, err)
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt < 4 {
			resp.Body.Close()
			wait := time.Duration(attempt+1) * 2 * time.Second
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			continue
		}
		break
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return engine.InvalidParams("validation.importer.credentials_rejected", "token", map[string]any{"host": a.base.Host, "status": resp.StatusCode}, "%s ha rifiutato le credenziali (HTTP %d): controlla token, email e permessi di lettura", a.base.Host, resp.StatusCode)
	case resp.StatusCode == http.StatusNotFound:
		return engine.NotFound("base_url", "", "risorsa non trovata su %s (%s): controlla l'indirizzo e il progetto", a.base.Host, path)
	case resp.StatusCode >= 300:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return engine.InvalidParams("validation.importer.server_http", "base_url", map[string]any{"host": a.base.Host, "status": resp.StatusCode, "detail": strings.TrimSpace(string(b))}, "%s ha risposto HTTP %d: %s", a.base.Host, resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(out)
}

// Connect verifica le credenziali e avvia la lettura dell'API in background: l'esecuzione
// resta «validating» finché i dati non sono letti e analizzati.
func (s *Service) Connect(ctx context.Context, tenantID string, actor engine.Actor, c Connection) (*Run, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	if c.Source != SourceJiraAPI && c.Source != SourceYouTrackAPI {
		return nil, engine.InvalidValue("source", "origine non valida: jira_api o youtrack_api")
	}
	c.Project = strings.TrimSpace(c.Project)
	if c.Project == "" && strings.TrimSpace(c.Query) == "" {
		return nil, engine.Required("project", "indica la chiave del progetto")
	}
	if c.Limit < 0 || c.Limit > maxAPIIssues {
		return nil, engine.InvalidParams("validation.importer.api_limit", "limit", map[string]any{"max": maxAPIIssues}, "al massimo %d elementi per import", maxAPIIssues)
	}
	a, err := s.newAPI(c)
	if err != nil {
		return nil, err
	}
	c.BaseURL = a.base.String()
	// Prova delle credenziali, subito.
	var me map[string]any
	if c.Source == SourceJiraAPI {
		err = a.get(ctx, "/rest/api/2/myself", nil, &me)
	} else {
		err = a.get(ctx, "/api/users/me", url.Values{"fields": {"login,fullName"}}, &me)
	}
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(c.Source + "|" + c.BaseURL + "|" + c.Project + "|" + c.Query))
	name := a.base.Host + " · " + c.Project
	var out *Run
	err = s.db.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		id, err := s.createRun(ctx, tx, actor, c.Source, name, sum[:], "validating", nil, nil)
		if err != nil {
			return err
		}
		saved := c
		saved.Token = ""
		if _, err := tx.Exec(ctx, `update import_runs set progress = $2, connection = $3 where id = $1`, id, jsonb(Progress{Phase: "lettura"}), jsonb(saved)); err != nil {
			return err
		}
		out, err = s.run(ctx, tx, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.keepToken(out.ID, c)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.fetch(tenantID, out.ID, a)
	}()
	return out, nil
}

func (s *Service) fetch(tenantID, runID string, a *api) {
	ctx := s.ctx
	report := func(n int) {
		_ = s.db.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
			return s.progress(ctx, tx, runID, Progress{Phase: "lettura", Done: n})
		})
	}
	var ds *Dataset
	var err error
	if a.c.Source == SourceJiraAPI {
		ds, err = fetchJira(ctx, a, report)
	} else {
		ds, err = fetchYouTrack(ctx, a, report)
	}
	if err == nil && len(ds.Issues) == 0 {
		err = engine.InvalidCode("validation.importer.project_empty", "project", "nessun elemento trovato: controlla la chiave del progetto e i permessi del token")
	}
	err = s.db.InTenant(context.Background(), tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		if err != nil {
			msg := err.Error()
			var ve *engine.ValidationError
			if errors.As(err, &ve) {
				msg = ve.Msg
			}
			_, e := tx.Exec(ctx, `update import_runs set status = 'failed', finished_at = now(), progress = $2 where id = $1`,
				runID, jsonb(Progress{Phase: "lettura", Message: msg}))
			return e
		}
		issues, cols, warns := issuesOf(ds, nil)
		an := analyze(ds, issues, cols, warns)
		m, e := suggest(ctx, tx, an)
		if e != nil {
			return e
		}
		if e := saveDataset(ctx, tx, runID, ds); e != nil {
			return e
		}
		_, e = tx.Exec(ctx, `update import_runs set status = 'pending', analysis = $2, mapping = $3, rows_total = $4, progress = $5 where id = $1`,
			runID, jsonb(an), jsonb(m), an.Issues, jsonb(Progress{Phase: "lettura", Done: an.Issues, Total: an.Issues}))
		return e
	})
	if err != nil {
		s.log.Error("lettura dell'API per l'import", "run", runID, "errore", err)
	}
}

// ---------------------------------------------------------------------
// Allegati dall'API

func (s *Service) downloadAttachments(ctx context.Context, tenantID, runID string, p *plan, rec *recorder) (int, error) {
	if s.files == nil || !p.m.Include.Attachments || p.ds.Format != "api" {
		return 0, nil
	}
	conn, ok := s.token(runID)
	if !ok {
		return 0, engine.InvalidCode("validation.importer.token_lost", "token", "il token dell'API non è più in memoria: inseriscilo di nuovo e riprendi l'import")
	}
	a, err := s.newAPI(conn)
	if err != nil {
		return 0, err
	}
	type job struct {
		pi *pIssue
		at Attachment
	}
	var jobs []job
	for _, pi := range p.issues {
		for _, at := range pi.atts {
			jobs = append(jobs, job{pi, at})
		}
	}
	saved := 0
	for i := 0; i < len(jobs); i += 20 {
		chunk := jobs[i:min(i+20, len(jobs))]
		type got struct {
			pi   *pIssue
			at   Attachment
			blob collab.Blob
		}
		var ok []got
		for _, j := range chunk {
			u, same := a.sameHost(j.at.URL)
			if !same {
				p.warnings = append(p.warnings, fmt.Sprintf("%s: allegato %s su un altro host, non scaricato", j.pi.src.Key, j.at.Name))
				continue
			}
			b, err := s.fetchFile(ctx, a, u.String(), j.at.Name)
			if err != nil {
				if ctx.Err() != nil {
					return saved, ctx.Err()
				}
				p.warnings = append(p.warnings, fmt.Sprintf("%s: allegato %s non scaricato (%v)", j.pi.src.Key, j.at.Name, err))
				continue
			}
			ok = append(ok, got{j.pi, j.at, b})
		}
		if err := s.db.InTenant(ctx, tenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
			for _, g := range ok {
				var exists bool
				if err := tx.QueryRow(ctx, `select exists (select 1 from attachments where element_id = $1 and name = $2 and size_bytes = $3 and removed_at is null)`,
					g.pi.id, collab.CleanName(g.at.Name), g.blob.Size).Scan(&exists); err != nil {
					return err
				}
				if exists {
					continue
				}
				sum, _ := hexDecode(g.blob.SHA256)
				id := db.NewID()
				created := g.pi.created
				if t, err := parseDate(g.at.Created, "auto", p.loc); err == nil {
					created = t
				}
				var author *string
				if g.at.Author != nil {
					author = p.memberOnly(g.at.Author)
				}
				if _, err := tx.Exec(ctx, `insert into attachments (tenant_id, id, element_id, author_id, name, mime_type, size_bytes, sha256, storage_key, width, height, created_at)
					values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`, id, g.pi.id, author, collab.CleanName(g.at.Name), g.blob.Mime, g.blob.Size,
					sum, "sha256/"+g.blob.SHA256, g.blob.Width, g.blob.Height, created); err != nil {
					return err
				}
				rec.add("attachment", id)
				saved++
			}
			if err := rec.flush(ctx, tx, runID); err != nil {
				return err
			}
			return s.progress(ctx, tx, runID, Progress{Phase: "allegati", Done: min(i+20, len(jobs)), Total: len(jobs)})
		}); err != nil {
			return saved, err
		}
	}
	return saved, nil
}

func (s *Service) fetchFile(ctx context.Context, a *api, raw, name string) (collab.Blob, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return collab.Blob{}, err
	}
	a.auth(req)
	req.Header.Set("Accept", "*/*")
	resp, err := a.client.Do(req)
	if err != nil {
		return collab.Blob{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return collab.Blob{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := s.files.Put(resp.Body, s.maxAtt, name)
	if errors.Is(err, collab.ErrTooLarge) {
		return b, fmt.Errorf("oltre %d MB", s.maxAtt>>20)
	}
	return b, err
}

// Reconnect ricompone la connessione di un'esecuzione con il token inserito di nuovo
// (dopo un riavvio il token non c'è più: non si salva).
func (s *Service) Reconnect(ctx context.Context, tx pgx.Tx, actor engine.Actor, runID, token, email string) (*Connection, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	var raw []byte
	err := tx.QueryRow(ctx, `select connection from import_runs where id::text = $1 and target = $2`, runID, Target).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, engine.InvalidCode("validation.importer.token_unused", "token", "questa esecuzione non usa un'API")
	}
	var c Connection
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, err
	}
	c.Token = strings.TrimSpace(token)
	if strings.TrimSpace(email) != "" {
		c.Email = strings.TrimSpace(email)
	}
	if _, err := s.newAPI(c); err != nil {
		return nil, err
	}
	return &c, nil
}

// AllowPrivateFromEnv legge REGIA_IMPORT_ALLOW_PRIVATE: i connettori possono raggiungere
// indirizzi interni (Jira Data Center o YouTrack nella rete aziendale).
func AllowPrivateFromEnv() bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv("REGIA_IMPORT_ALLOW_PRIVATE")))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func hexDecode(s string) ([]byte, error) {
	out := make([]byte, len(s)/2)
	for i := 0; i+1 < len(s); i += 2 {
		var v byte
		for _, c := range s[i : i+2] {
			v <<= 4
			switch {
			case c >= '0' && c <= '9':
				v |= byte(c - '0')
			case c >= 'a' && c <= 'f':
				v |= byte(c-'a') + 10
			default:
				return nil, errors.New("hex non valido")
			}
		}
		out[i/2] = v
	}
	return out, nil
}
