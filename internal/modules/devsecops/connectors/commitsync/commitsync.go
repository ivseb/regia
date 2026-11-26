// Package commitsync legge i commit e le pull request unite di un repository per i
// connettori (GitHub, Gitea) e ne ricava i commit che citano la chiave di un elemento
// («HL-12»). La logica dei cursori è una sola; i connettori forniscono solo la lettura.
//
//   - Cursore per repository: istante e sha dell'ultimo commit letto del branch predefinito,
//     più l'istante dell'ultima PR vista. Chi lo salva lo fa nella stessa transazione degli
//     oggetti: se la consegna fallisce il cursore non avanza e si rilegge.
//   - Alla prima lettura (o con Full) si parte da 90 giorni fa.
//   - Un tetto per giro (MaxCommits, MaxPulls): se lo si raggiunge, i commit più vecchi del
//     tetto non si leggono e c'è un avviso.
package commitsync

import (
	"context"
	"fmt"
	"time"

	"regia/internal/modules/devsecops/connectors/github/ghapi"
	"regia/internal/modules/devsecops/contract"
)

const (
	// Lookback è quanto si risale alla prima lettura.
	Lookback = 90 * 24 * time.Hour
	// MaxCommits e MaxPulls sono i tetti di elementi letti per repository a ogni giro.
	MaxCommits = 1000
	MaxPulls   = 250
)

// Cursor è il cursore dei commit di un repository (dentro i cursori del repository).
type Cursor struct {
	At    time.Time `json:"at,omitzero"`     // data dell'ultimo commit letto
	SHA   string    `json:"sha,omitempty"`   // ...e il suo sha
	PRsAt time.Time `json:"prs_at,omitzero"` // data di modifica dell'ultima PR vista
}

// Source è la lettura di un repository: la implementa ogni connettore sul proprio client.
type Source interface {
	// Commits: dal più recente, dalla data since in poi; each può fermare (stop).
	Commits(ctx context.Context, since time.Time, each func(ghapi.Commit) (stop bool, err error)) error
	// ClosedPulls: dalla modificata più di recente; each può fermare (stop).
	ClosedPulls(ctx context.Context, each func(ghapi.PullRequest) (stop bool, err error)) error
}

// Result è ciò che si è letto: i commit che citano una chiave, il cursore nuovo, gli avvisi.
type Result struct {
	Commits  []contract.Commit
	Next     Cursor
	Read     int // commit letti in tutto (con o senza chiave)
	Warnings []string
}

// Read legge i commit del branch predefinito e le PR unite dopo il cursore. In caso di errore
// il risultato è vuoto e il cursore non cambia (chi chiama tiene il vecchio).
func Read(ctx context.Context, src Source, svc contract.Service, cur Cursor, full bool, now time.Time) (Result, error) {
	res := Result{Next: cur}
	floor := now.Add(-Lookback)
	since, prSince := cur.At, cur.PRsAt
	if full || since.IsZero() || since.Before(floor) {
		since = floor
	}
	if full || prSince.IsZero() || prSince.Before(floor) {
		prSince = floor
	}

	first := true
	truncated := false
	err := src.Commits(ctx, since, func(c ghapi.Commit) (bool, error) {
		res.Read++
		at := c.When()
		if first {
			first = false
			if at.After(cur.At) {
				res.Next.At, res.Next.SHA = at, c.SHA
			}
		}
		if keys := contract.CommitKeys(c.Commit.Message); len(keys) > 0 {
			res.Commits = append(res.Commits, contract.Commit{Service: svc, SHA: c.SHA, Message: c.Commit.Message, Author: c.Who(),
				At: at, URL: c.HTMLURL, Source: "commit"})
		}
		if res.Read >= MaxCommits {
			truncated = true
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return Result{Next: cur}, fmt.Errorf("commit: %w", err)
	}
	if truncated {
		res.Warnings = append(res.Warnings, fmt.Sprintf("più di %d commit dall'ultima lettura: i più vecchi non si leggono", MaxCommits))
	}

	firstPR := true
	n := 0
	err = src.ClosedPulls(ctx, func(p ghapi.PullRequest) (bool, error) {
		if p.UpdatedAt.Before(prSince) {
			return true, nil // sono in ordine di modifica: il resto è storia nota
		}
		n++
		if firstPR {
			firstPR = false
			if p.UpdatedAt.After(cur.PRsAt) {
				res.Next.PRsAt = p.UpdatedAt.UTC()
			}
		}
		merged := p.MergedAt != nil && !p.MergedAt.IsZero()
		if merged && p.MergeCommitSHA != "" {
			if keys := contract.CommitKeys(p.Title, p.Head.Ref); len(keys) > 0 {
				res.Commits = append(res.Commits, contract.Commit{Service: svc, SHA: p.MergeCommitSHA, Message: p.Title, Branch: p.Head.Ref,
					Author: p.User.Login, At: p.MergedAt.UTC(), URL: p.HTMLURL, Source: "pull_request", PRNumber: p.Number})
			}
		}
		return n >= MaxPulls, nil
	})
	if err != nil {
		return Result{Next: cur}, fmt.Errorf("pull request: %w", err)
	}
	return res, nil
}
