package github

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/devsecops/connectors/github/ghapi"
	"regia/internal/modules/devsecops/connectors/profile"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/platform/db"
)

// Schema dei tag dei rilasci (pilota home lab, difetto 5). Alla scoperta, per i repository che
// pubblicano più schemi di tag (chart-1.4.0 e v1.4.0), Regia chiede quale è il rilascio vero.
// La risposta è il tag_filter del profilo dell'integrazione per quel repository: se
// l'integrazione non ha profilo se ne crea uno. I rilasci già letti dello schema scartato si
// tolgono e le metriche DORA si ricalcolano. Lo usa anche il connettore Gitea.

// maxTagProbes è il numero massimo di repository di cui la scoperta legge i tag (una chiamata
// ciascuno): prima gli associati, poi gli altri non ignorati.
const maxTagProbes = 30

// TagQuestion è la domanda su un repository con più schemi di tag.
type TagQuestion struct {
	RepositoryID string              `json:"repository_id"`
	FullName     string              `json:"full_name"`
	Schemes      []profile.TagScheme `json:"schemes"`
}

// TagCandidate è un repository di cui leggere i tag recenti.
type TagCandidate struct {
	ID       string
	FullName string
}

// TagCandidates elenca i repository dell'integrazione da sondare: non ignorati, non spariti,
// senza una scelta già fatta nel profilo.
func TagCandidates(ctx context.Context, tx pgx.Tx, integrationID string, def *profile.Definition) ([]TagCandidate, error) {
	rows, err := tx.Query(ctx, `select id, full_name from connector_repositories
		where integration_id = $1 and missing_since is null and mapping_status <> 'ignored' and not archived
		order by mapping_status = 'confirmed' desc, lower(full_name)`, integrationID)
	if err != nil {
		return nil, err
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (TagCandidate, error) {
		var c TagCandidate
		err := r.Scan(&c.ID, &c.FullName)
		return c, err
	})
	if err != nil {
		return nil, err
	}
	out := list[:0]
	for _, c := range list {
		if !def.HasTagFilter(c.FullName) && len(out) < maxTagProbes {
			out = append(out, c)
		}
	}
	return out, nil
}

// QuestionFor restituisce la domanda sul repository se i suoi tag (già filtrati come li
// leggerebbe la sincronizzazione) seguono più di uno schema; nil altrimenti.
func QuestionFor(c TagCandidate, tags []string) *TagQuestion {
	schemes := profile.TagSchemes(tags)
	if len(schemes) < 2 {
		return nil
	}
	return &TagQuestion{RepositoryID: c.ID, FullName: c.FullName, Schemes: schemes}
}

// tagQuestions legge la prima pagina delle Release dei repository candidati. Un repository
// che non risponde si salta: la scoperta non fallisce per questo.
func (s *Service) tagQuestions(ctx context.Context, tenantID string, it *Integration, cl *ghapi.Client) ([]TagQuestion, error) {
	var def *profile.Definition
	var cands []TagCandidate
	if err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		if def, _, err = definitionFor(ctx, tx, it.Config.ProfileID); err != nil {
			return err
		}
		cands, err = TagCandidates(ctx, tx, it.ID, def)
		return err
	}); err != nil {
		return nil, err
	}
	out := []TagQuestion{}
	for _, c := range cands {
		var tags []string
		err := cl.Releases(ctx, c.FullName, 1, func(r ghapi.Release) (bool, error) {
			if !r.Draft && def.AcceptsRepoTag(c.FullName, r.TagName, r.Prerelease) {
				tags = append(tags, r.TagName)
			}
			return false, nil
		})
		if err != nil {
			if _, limited := ghapi.IsRateLimited(err); limited || ctx.Err() != nil {
				break
			}
			continue
		}
		if q := QuestionFor(c, tags); q != nil {
			out = append(out, *q)
		}
	}
	return out, nil
}

// TagFilterInput è la risposta alla domanda: il modello dello schema scelto (vuoto = tutti i
// tag sono rilasci, non chiedere più).
type TagFilterInput struct {
	Pattern string `json:"pattern"`
}

// TagFilterResult dice dove è finita la scelta.
type TagFilterResult struct {
	ProfileID      string `json:"profile_id"`
	ProfileName    string `json:"profile_name"`
	ProfileVersion int    `json:"profile_version"`
	ProfileCreated bool   `json:"profile_created"`
	Pruned         int    `json:"pruned"` // rilasci già letti dello schema scartato, tolti
}

// TagFilterTarget è il repository a cui si applica la scelta.
type TagFilterTarget struct {
	IntegrationID   string
	IntegrationName string
	ProfileID       string
	Kind            string // github | gitea: nell'audit
	FullName        string
	Service         contract.Service
	Confirmed       bool
}

// SaveTagFilter scrive la scelta nel profilo (una nuova versione, o un profilo nuovo collegato
// all'integrazione) e toglie i rilasci già letti che non la rispettano.
func SaveTagFilter(ctx context.Context, tx pgx.Tx, a Actor, ing any, t TagFilterTarget, pattern string) (*TagFilterResult, error) {
	pattern = strings.TrimSpace(pattern)
	if len(pattern) > 200 {
		return nil, invalid("pattern", "modello troppo lungo (al massimo 200 caratteri)")
	}
	def, _, err := definitionFor(ctx, tx, t.ProfileID)
	if err != nil {
		return nil, err
	}
	next, err := def.WithTagFilter(t.FullName, pattern)
	if err != nil {
		return nil, invalid("pattern", "modello non valido")
	}
	if ps := next.Validate(); len(ps) > 0 {
		return nil, profileInvalid(ps)
	}
	res := &TagFilterResult{}
	note := "Schema dei tag di " + t.FullName + ": " + describePattern(pattern)
	if def != nil {
		var cur Profile
		if err := tx.QueryRow(ctx, "select id, name, current_version from pipeline_profiles where id = $1", t.ProfileID).
			Scan(&cur.ID, &cur.Name, &cur.Version); err != nil {
			return nil, err
		}
		v, err := saveVersionTx(ctx, tx, a, &cur, next, note)
		if err != nil {
			return nil, err
		}
		res.ProfileID, res.ProfileName, res.ProfileVersion = cur.ID, cur.Name, v
	} else {
		name := truncate("Rilasci di "+t.IntegrationName, 120)
		key, err := uniqueKey(ctx, tx, slugify(name))
		if err != nil {
			return nil, err
		}
		id := db.NewID()
		if _, err := tx.Exec(ctx, `insert into pipeline_profiles (tenant_id, id, key, name, description, current_version, created_by)
			values (app_tenant(), $1, $2, $3, $4, 1, $5)`, id, key, name, "Creato alla scoperta dei repository: lo schema dei tag che vale come rilascio.", nullable(a.MemberID)); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `insert into pipeline_profile_versions (tenant_id, profile_id, version, definition, note, created_by)
			values (app_tenant(), $1, 1, $2, $3, $4)`, id, next.Canonical(), note, nullable(a.MemberID)); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `update integrations set config = jsonb_set(config, '{profile_id}', to_jsonb($2::text)), updated_at = now() where id = $1`,
			t.IntegrationID, id); err != nil {
			return nil, err
		}
		res.ProfileID, res.ProfileName, res.ProfileVersion, res.ProfileCreated = id, name, 1, true
	}
	if pr, ok := ing.(contract.ReleasePruner); ok && t.Confirmed {
		n, err := pr.PruneReleases(ctx, tx, systemActor(a), t.Service, func(u string) bool {
			tag := profile.TagOfReleaseURL(u)
			return tag == "" || profile.MatchTag(pattern, tag)
		})
		if err != nil {
			return nil, err
		}
		res.Pruned = n
	}
	return res, audited(ctx, tx, a, "devsecops."+t.Kind+".tag_filter", "integration", t.IntegrationID, map[string]any{
		"repository": t.FullName, "pattern": pattern, "profile": res.ProfileID, "version": res.ProfileVersion, "pruned": res.Pruned})
}

func describePattern(p string) string {
	if p == "" {
		return "tutti i tag"
	}
	return p
}

// SetTagFilter risponde alla domanda per un repository dell'integrazione GitHub.
func (s *Service) SetTagFilter(ctx context.Context, a Actor, integrationID, repoID string, in TagFilterInput) (*TagFilterResult, error) {
	if !validID(repoID) {
		return nil, ErrRepoNotFound
	}
	var out *TagFilterResult
	err := s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		it, _, err := s.load(ctx, tx, integrationID)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, selectRepo+" and r.integration_id = $1 and r.id = $2", integrationID, repoID)
		if err != nil {
			return err
		}
		list, err := scanRepos(rows)
		if err != nil {
			return err
		}
		if len(list) == 0 {
			return ErrRepoNotFound
		}
		r := list[0].Repository
		out, err = SaveTagFilter(ctx, tx, a, s.o.Ingestor, TagFilterTarget{
			IntegrationID: it.ID, IntegrationName: it.Name, ProfileID: it.Config.ProfileID, Kind: "github",
			FullName: r.FullName, Service: r.service(), Confirmed: r.Status == "confirmed",
		}, in.Pattern)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrRepoNotFound
	}
	return out, err
}
