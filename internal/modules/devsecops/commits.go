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
	type target struct{ id, space string }
	elements := map[contract.ElementRef]target{} // chiave → elemento (id "" = non esiste)
	closing := map[string]bool{}                 // spazio → chiusura con le parole chiave accesa
	n := 0
	for _, c := range commits {
		sha := strings.ToLower(strings.TrimSpace(c.SHA))
		if sha == "" || c.At.IsZero() {
			continue
		}
		closes := map[contract.ElementRef]contract.CloseKey{}
		for _, ck := range contract.CloseKeys(c.Message) {
			closes[ck.Ref] = ck
		}
		for _, ref := range contract.CommitKeys(c.Message, c.Branch) {
			tg, seen := elements[ref]
			if !seen {
				err := tx.QueryRow(ctx, `select e.id::text, e.space_id::text from elements e join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
					where sp.key = $1 and e.number = $2 and e.deleted_at is null`, ref.Space, ref.Number).Scan(&tg.id, &tg.space)
				if err != nil && !errors.Is(err, pgx.ErrNoRows) {
					return n, err
				}
				elements[ref] = tg
			}
			id := tg.id
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
			// Chiusura con le parole chiave: solo per un collegamento nuovo (una rilettura non
			// richiude un elemento riaperto a mano).
			if ck, ok := closes[ref]; ok && tag.RowsAffected() == 1 {
				on, seen := closing[tg.space]
				if !seen {
					if on, err = closeEnabled(ctx, tx, tg.space); err != nil {
						return n, err
					}
					closing[tg.space] = on
				}
				if on {
					if err := s.closeByCommit(ctx, tx, actor, id, c, sha, ck); err != nil {
						return n, err
					}
				}
			}
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
	// ReleaseID: il rilascio registrato di quella versione (per la pagina del rilascio).
	ReleaseID *string `json:"release_id"`
}

// ElementCommits è la risposta di GET /api/v1/elements/{key}/commits.
type ElementCommits struct {
	Commits []ElementCommit `json:"commits"`
	// Release: il rilascio in produzione che completa l'elemento (per ogni servizio con commit,
	// il primo successivo all'ultimo commit; si mostra il più recente). Assente se non ancora.
	Release *CommitRelease `json:"release"`
	// FirstAt: data del primo commit dell'elemento (inizio del lead time dell'elemento).
	FirstAt *time.Time `json:"first_at"`
}

// ElementCommitsOf elenca i commit legati a un elemento (il chiamante ha già verificato che
// l'elemento sia visibile) e il rilascio che li contiene.
func (s *Service) ElementCommitsOf(ctx context.Context, tx pgx.Tx, elementID string) (*ElementCommits, error) {
	out := &ElementCommits{Commits: []ElementCommit{}}
	rows, err := tx.Query(ctx, `select c.sha, c.title, c.author, c.committed_at, c.repository, c.url, c.source, c.pr_number, sv.name
		from element_commits c join services sv on sv.tenant_id = c.tenant_id and sv.id = c.service_id
		where c.element_id = $1 order by c.committed_at desc, c.sha limit 200`, elementID)
	if err != nil {
		return nil, err
	}
	var first time.Time
	for rows.Next() {
		var c ElementCommit
		if err := rows.Scan(&c.SHA, &c.Title, &c.Author, &c.At, &c.Repository, &c.URL, &c.Source, &c.PullRequest, &c.Service); err != nil {
			rows.Close()
			return nil, err
		}
		c.At = c.At.UTC()
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
	// In produzione quando ogni servizio con commit ha un deploy (o rilascio) di produzione
	// successivo all'ultimo commit su quel servizio: si mostra quello che entra per ultimo.
	prod, err := productionOf(ctx, tx, []string{elementID})
	if err != nil {
		return nil, err
	}
	p := prod[elementID]
	if p == nil || !p.InProduction || p.At == nil {
		return out, nil
	}
	rel := CommitRelease{Service: p.Service, Version: p.Version, At: *p.At, ReleaseID: p.ReleaseID}
	rel.LeadDays = rel.At.Sub(first).Hours() / 24
	if rel.LeadDays < 0 {
		rel.LeadDays = 0
	}
	out.Release = &rel
	return out, nil
}
