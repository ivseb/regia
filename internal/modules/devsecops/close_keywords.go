package devsecops

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/devsecops/contract"
)

// Chiusura con le parole chiave: «fixes HL-12» in un commit del branch predefinito (o nel
// titolo di una PR unita) porta l'elemento nella fase finale. Spenta per default; si accende
// per il tenant e si forza a sì o a no per spazio. Il passaggio è quello del processo: deve
// esistere dalla fase attuale, valgono le guardie e i campi obbligatori; i passaggi di
// sistema restano esclusi. Se non è possibile, resta una nota nella storia dell'elemento.

// ReleaseSettings sono le impostazioni dei rilasci del tenant.
type ReleaseSettings struct {
	CloseOnKeywords bool        `json:"close_on_keywords"`
	CertSections    []string    `json:"cert_sections"`
	Spaces          []SpaceFlag `json:"spaces"` // eccezioni per spazio
	UpdatedAt       *time.Time  `json:"updated_at"`
	CanConfigure    bool        `json:"can_configure"`
}

// SpaceFlag è l'eccezione di uno spazio (Close: sì o no; senza riga lo spazio eredita).
type SpaceFlag struct {
	Key   string `json:"key"`
	Name  string `json:"name"`
	Close bool   `json:"close_on_keywords"`
}

// CertSections sono le sezioni della certificazione del rilascio, nell'ordine predefinito.
var CertSections = []string{"change", "releases", "items", "tests", "sonar", "security", "gate"}

func loadReleaseSettings(ctx context.Context, tx pgx.Tx) (ReleaseSettings, error) {
	st := ReleaseSettings{CertSections: append([]string(nil), CertSections...), Spaces: []SpaceFlag{}}
	err := tx.QueryRow(ctx, `select close_on_keywords, cert_sections, updated_at from devsecops_release_settings`).
		Scan(&st.CloseOnKeywords, &st.CertSections, &st.UpdatedAt)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return st, err
	}
	rows, err := tx.Query(ctx, `select sp.key, sp.name, c.close_on_keywords from devsecops_space_close c
		join spaces sp on sp.tenant_id = c.tenant_id and sp.id = c.space_id order by sp.key`)
	if err != nil {
		return st, err
	}
	var f SpaceFlag
	_, err = pgx.ForEachRow(rows, []any{&f.Key, &f.Name, &f.Close}, func() error {
		st.Spaces = append(st.Spaces, f)
		return nil
	})
	return st, err
}

// ReleaseSettingsOf restituisce le impostazioni (lettura: element.read).
func (s *Service) ReleaseSettingsOf(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*ReleaseSettings, error) {
	if !actor.Has(PermRead) {
		return nil, engine.ErrForbidden
	}
	st, err := loadReleaseSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	st.CanConfigure = CanConfigure(actor)
	return &st, nil
}

// ReleaseSettingsInput: le eccezioni per spazio si indicano tutte (sostituiscono le
// precedenti); Close nil = lo spazio eredita.
type ReleaseSettingsInput struct {
	CloseOnKeywords bool     `json:"close_on_keywords"`
	CertSections    []string `json:"cert_sections"`
	Spaces          []struct {
		Key   string `json:"key"`
		Close *bool  `json:"close_on_keywords"`
	} `json:"spaces"`
}

// SaveReleaseSettings salva le impostazioni dei rilasci (catalogo o amministratore).
func (s *Service) SaveReleaseSettings(ctx context.Context, tx pgx.Tx, actor engine.Actor, in ReleaseSettingsInput) (*ReleaseSettings, error) {
	if !CanConfigure(actor) {
		return nil, engine.ErrForbidden
	}
	sections := []string{}
	seen := map[string]bool{}
	for _, k := range in.CertSections {
		k = strings.ToLower(strings.TrimSpace(k))
		if seen[k] {
			continue
		}
		known := false
		for _, c := range CertSections {
			known = known || c == k
		}
		if !known {
			return nil, engine.InvalidValue("cert_sections", "sezione sconosciuta: %s", k)
		}
		seen[k] = true
		sections = append(sections, k)
	}
	if in.CertSections == nil {
		sections = append(sections, CertSections...)
	}
	if _, err := tx.Exec(ctx, `insert into devsecops_release_settings (tenant_id, close_on_keywords, cert_sections, updated_by, updated_at)
		values (app_tenant(), $1, $2, $3, now())
		on conflict (tenant_id) do update set close_on_keywords = excluded.close_on_keywords, cert_sections = excluded.cert_sections,
		updated_by = excluded.updated_by, updated_at = now()`, in.CloseOnKeywords, sections, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	if in.Spaces != nil {
		if _, err := tx.Exec(ctx, `delete from devsecops_space_close`); err != nil {
			return nil, err
		}
		for _, sp := range in.Spaces {
			if sp.Close == nil {
				continue
			}
			tag, err := tx.Exec(ctx, `insert into devsecops_space_close (tenant_id, space_id, close_on_keywords)
				select app_tenant(), id, $2 from spaces where lower(key) = lower($1)
				on conflict (tenant_id, space_id) do update set close_on_keywords = excluded.close_on_keywords, updated_at = now()`,
				strings.TrimSpace(sp.Key), *sp.Close)
			if err != nil {
				return nil, err
			}
			if tag.RowsAffected() == 0 {
				return nil, engine.InvalidValue("spaces", "spazio sconosciuto: %s", sp.Key)
			}
		}
	}
	if err := auditEvent(ctx, tx, actor, "devsecops.release_settings", "devsecops_release_settings", "", map[string]any{
		"close_on_keywords": in.CloseOnKeywords, "cert_sections": sections, "spaces": len(in.Spaces)}); err != nil {
		return nil, err
	}
	return s.ReleaseSettingsOf(ctx, tx, actor)
}

// closeEnabled: la chiusura con le parole chiave vale per lo spazio indicato?
func closeEnabled(ctx context.Context, tx pgx.Tx, spaceID string) (bool, error) {
	var on bool
	err := tx.QueryRow(ctx, `select coalesce((select close_on_keywords from devsecops_space_close where space_id = $1),
		(select close_on_keywords from devsecops_release_settings), false)`, spaceID).Scan(&on)
	return on, err
}

// closeByCommit porta l'elemento nella fase finale per un commit con «fixes KEY». Non
// restituisce mai un errore di processo: un passaggio non possibile lascia una nota nella
// storia. Gli errori del database sì.
func (s *Service) closeByCommit(ctx context.Context, tx pgx.Tx, actor engine.Actor, elementID string, c contract.Commit, sha string, ck contract.CloseKey) error {
	el, err := s.eng.Get(ctx, tx, actor, elementID)
	if err != nil {
		if errors.Is(err, engine.ErrNotFound) || errors.Is(err, engine.ErrForbidden) {
			return nil
		}
		return err
	}
	if el.StateCategory == "done" || el.StateCategory == "cancelled" {
		return nil
	}
	short := sha
	if len(short) > 7 {
		short = short[:7]
	}
	note := map[string]any{"sha": sha, "url": cleanURL(c.URL), "keyword": ck.Keyword, "key": ck.Ref.String(), "source": c.Source}
	if c.PRNumber > 0 {
		note["pr_number"] = c.PRNumber
	}
	avail, err := s.eng.Available(ctx, tx, actor, el)
	if err != nil {
		return err
	}
	var done, forward []string
	for _, tv := range avail {
		if len(tv.Blocked) > 0 {
			continue
		}
		var cat string
		err := tx.QueryRow(ctx, `select coalesce(o.category, '') from options o join options cur on cur.tenant_id = o.tenant_id and cur.option_set_id = o.option_set_id
			where cur.id = $1 and lower(o.name) = lower($2) order by o.archived, o.ordinal limit 1`, el.StateID, tv.To).Scan(&cat)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if cat != "done" {
			continue
		}
		done = append(done, tv.To)
		if tv.Forward {
			forward = append(forward, tv.To)
		}
	}
	to := ""
	switch {
	case len(done) == 1:
		to = done[0]
	case len(forward) == 1:
		to = forward[0]
	}
	if to == "" {
		note["reason"] = "not_allowed"
		if len(done) > 1 {
			note["reason"] = "ambiguous"
		}
		return engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "commit", "close_skipped", note, nil)
	}
	// Il passaggio in un savepoint: un errore del motore (guardia, campo) non deve annullare
	// la sincronizzazione.
	sp, err := tx.Begin(ctx)
	if err != nil {
		return err
	}
	comment := "Chiuso dal commit " + short + " (" + ck.Keyword + " " + ck.Ref.String() + ")"
	if _, err := s.eng.Transition(ctx, sp, actor, el.ID, to, comment, engine.TransitionOpts{}); err != nil {
		if rbErr := sp.Rollback(ctx); rbErr != nil {
			return rbErr
		}
		var ve *engine.ValidationError
		if errors.As(err, &ve) || errors.Is(err, engine.ErrForbidden) {
			note["reason"] = "not_allowed"
			return engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "commit", "close_skipped", note, nil)
		}
		return err
	}
	if err := sp.Commit(ctx); err != nil {
		return err
	}
	note["to"] = to
	return engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "commit", "closed", note, nil)
}
