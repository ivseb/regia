package devsecops

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/platform/db"
)

// Collegamento commit → elemento (tabella element_commits). I connettori consegnano i
// commit che citano una chiave («HL-12»); qui si tengono solo le coppie per cui esiste uno
// spazio con quella chiave e un elemento con quel numero, non cancellato.

var _ contract.CommitIngestor = (*Service)(nil)
var _ contract.DoraRecomputer = (*Service)(nil)

const (
	maxCommitTitle  = 200
	maxCommitAuthor = 120
)

// IngestCommits salva i collegamenti dei commit con gli elementi citati. Idempotente: la
// stessa coppia elemento+sha si registra una volta. Restituisce i collegamenti nuovi.
func (s *Service) IngestCommits(ctx context.Context, tx pgx.Tx, actor engine.Actor, svc contract.Service, commits []contract.Commit) (int, error) {
	if len(commits) == 0 {
		return 0, nil
	}
	name := clip(svc.Name, 200)
	if name == "" {
		name = clip(svc.Repository, 200)
	}
	var serviceID string
	if err := tx.QueryRow(ctx, "select id::text from services where lower(name) = lower($1)", name).Scan(&serviceID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, nil // servizio non (ancora) registrato: niente da legare
		}
		return 0, err
	}
	repo := clip(svc.Repository, 300)
	elements := map[contract.ElementRef]string{} // chiave → id dell'elemento ("" = non esiste)
	n := 0
	for _, c := range commits {
		sha := strings.ToLower(strings.TrimSpace(c.SHA))
		if sha == "" || c.At.IsZero() {
			continue
		}
		for _, ref := range contract.CommitKeys(c.Message, c.Branch) {
			id, seen := elements[ref]
			if !seen {
				err := tx.QueryRow(ctx, `select e.id::text from elements e join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
					where sp.key = $1 and e.number = $2 and e.deleted_at is null`, ref.Space, ref.Number).Scan(&id)
				if err != nil && !errors.Is(err, pgx.ErrNoRows) {
					return n, err
				}
				elements[ref] = id
			}
			if id == "" {
				continue
			}
			source := c.Source
			if source != "pull_request" {
				source = "commit"
			}
			var pr any
			if c.PRNumber > 0 {
				pr = c.PRNumber
			}
			tag, err := tx.Exec(ctx, `insert into element_commits (tenant_id, id, element_id, service_id, repository, sha, title, author, committed_at, url, source, pr_number)
				values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
				on conflict (tenant_id, element_id, sha) do nothing`,
				db.NewID(), id, serviceID, repo, clip(sha, 64), contract.CommitTitle(c.Message, maxCommitTitle), clip(c.Author, maxCommitAuthor),
				c.At.UTC(), clip(cleanURL(c.URL), 500), source, pr)
			if err != nil {
				return n, err
			}
			n += int(tag.RowsAffected())
		}
	}
	return n, nil
}

// RecomputeDoraIfEmpty ricalcola le metriche DORA se non ce n'è ancora nessuna (la prima
// sincronizzazione di un connettore: altrimenti la pagina resterebbe vuota fino al prossimo
// giro orario).
func (s *Service) RecomputeDoraIfEmpty(ctx context.Context, tx pgx.Tx) (bool, error) {
	var found bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from metric_points where metric_key like 'dora.%')`).Scan(&found); err != nil {
		return false, err
	}
	if found {
		return false, nil
	}
	if _, err := s.recompute(ctx, tx, true); err != nil {
		return false, err
	}
	return true, nil
}

// ---------------------------------------------------------------------------
// Lettura per la scheda dell'elemento

// ElementCommit è un commit legato a un elemento.
type ElementCommit struct {
	SHA         string    `json:"sha"`
	Title       string    `json:"title"`
	Author      string    `json:"author"`
	At          time.Time `json:"at"`
	Repository  string    `json:"repository"`
	URL         string    `json:"url"`
	Source      string    `json:"source"`
	PullRequest *int      `json:"pr_number"`
	Service     string    `json:"service"`
}

// CommitRelease è il rilascio in produzione che contiene i commit di un elemento.
type CommitRelease struct {
	Service  string    `json:"service"`
	Version  string    `json:"version"`
	At       time.Time `json:"at"`
	LeadDays float64   `json:"lead_days"`
}

// ElementCommits è la risposta di GET /api/v1/elements/{key}/commits.
type ElementCommits struct {
	Commits []ElementCommit `json:"commits"`
	// Release: il primo rilascio in produzione, dello stesso servizio, successivo all'ultimo
	// commit dell'elemento. Assente se non ancora rilasciato.
	Release *CommitRelease `json:"release"`
	// FirstAt: data del primo commit dell'elemento (inizio del lead time dell'elemento).
	FirstAt *time.Time `json:"first_at"`
}

// ElementCommitsOf elenca i commit legati a un elemento (il chiamante ha già verificato che
// l'elemento sia visibile) e il rilascio che li contiene.
func (s *Service) ElementCommitsOf(ctx context.Context, tx pgx.Tx, elementID string) (*ElementCommits, error) {
	out := &ElementCommits{Commits: []ElementCommit{}}
	rows, err := tx.Query(ctx, `select c.sha, c.title, c.author, c.committed_at, c.repository, c.url, c.source, c.pr_number, sv.name, c.service_id::text
		from element_commits c join services sv on sv.tenant_id = c.tenant_id and sv.id = c.service_id
		where c.element_id = $1 order by c.committed_at desc, c.sha limit 200`, elementID)
	if err != nil {
		return nil, err
	}
	var first, last time.Time
	var lastSvc string
	for rows.Next() {
		var c ElementCommit
		var svcID string
		if err := rows.Scan(&c.SHA, &c.Title, &c.Author, &c.At, &c.Repository, &c.URL, &c.Source, &c.PullRequest, &c.Service, &svcID); err != nil {
			rows.Close()
			return nil, err
		}
		c.At = c.At.UTC()
		if len(out.Commits) == 0 { // il più recente: da qui si cerca il rilascio
			last, lastSvc = c.At, svcID
		}
		if first.IsZero() || c.At.Before(first) {
			first = c.At
		}
		out.Commits = append(out.Commits, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out.Commits) == 0 {
		return out, nil
	}
	out.FirstAt = &first
	// Il primo deploy di produzione (o, se mancano, la prima release) del servizio dell'ultimo
	// commit, con data ≥ dell'ultimo commit.
	var rel CommitRelease
	err = tx.QueryRow(ctx, `select sv.name, d.version, d.started_at from deployments d
		join environments e on e.tenant_id = d.tenant_id and e.id = d.environment_id and e.is_production
		join services sv on sv.tenant_id = d.tenant_id and sv.id = d.service_id
		where d.service_id = $1 and d.status = 'succeeded' and d.started_at >= $2 order by d.started_at limit 1`, lastSvc, last).
		Scan(&rel.Service, &rel.Version, &rel.At)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `select sv.name, r.version, r.released_at from releases r
			join environments e on e.tenant_id = r.tenant_id and e.id = r.environment_id and e.is_production
			join services sv on sv.tenant_id = r.tenant_id and sv.id = r.service_id
			where r.service_id = $1 and r.released_at >= $2 order by r.released_at limit 1`, lastSvc, last).
			Scan(&rel.Service, &rel.Version, &rel.At)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	rel.At = rel.At.UTC()
	rel.LeadDays = rel.At.Sub(first).Hours() / 24
	if rel.LeadDays < 0 {
		rel.LeadDays = 0
	}
	out.Release = &rel
	return out, nil
}
