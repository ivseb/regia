package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/audit"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/i18n"
	"regia/internal/platform/rbac"
)

// Lingua dell'interfaccia (replica/lingua.md). Precedenza: scelta del membro, poi default del
// tenant, poi Accept-Language del browser, poi l'italiano. Le chiavi ammesse coincidono con le
// cartelle di web/src/i18n e con il controllo della migrazione 0051.

var supportedLangs = []string{"it", "en"}

const defaultLang = "it"

// languageChoice è una scelta, anche assente: nil o stringa vuota = nessuna scelta.
type languageChoice struct {
	Locale *string `json:"locale"`
}

// languageView è la lingua effettiva di chi chiede, con da dove viene.
type languageView struct {
	Effective string   `json:"effective"`
	Source    string   `json:"source"` // member, tenant, browser, default
	Member    *string  `json:"member"`
	Tenant    *string  `json:"tenant"`
	Options   []string `json:"options"`
}

func okLang(v string) bool { return slices.Contains(supportedLangs, v) }

// parseAcceptLanguage sceglie la prima lingua supportata per preferenza (q) tra quelle
// dell'intestazione, anche come sottolingua ("en-GB" → "en"). Vuoto se nessuna è supportata.
func parseAcceptLanguage(h string) string {
	type tag struct {
		lang string
		q    float64
	}
	var tags []tag
	for _, part := range strings.Split(h, ",") {
		fields := strings.Split(strings.TrimSpace(part), ";")
		name := strings.ToLower(strings.TrimSpace(fields[0]))
		if name == "" || name == "*" {
			continue
		}
		q := 1.0
		for _, f := range fields[1:] {
			if v, ok := strings.CutPrefix(strings.TrimSpace(f), "q="); ok {
				if x, err := strconv.ParseFloat(v, 64); err == nil {
					q = x
				}
			}
		}
		if q <= 0 {
			continue
		}
		base, _, _ := strings.Cut(name, "-")
		tags = append(tags, tag{base, q})
	}
	sort.SliceStable(tags, func(i, j int) bool { return tags[i].q > tags[j].q })
	for _, t := range tags {
		if okLang(t.lang) {
			return t.lang
		}
	}
	return ""
}

// resolveLanguage applica la precedenza; member e tenant nil = nessuna scelta.
func resolveLanguage(member, tenant *string, accept string) languageView {
	v := languageView{Effective: defaultLang, Source: "default", Member: member, Tenant: tenant, Options: supportedLangs}
	switch {
	case member != nil && okLang(*member):
		v.Effective, v.Source = *member, "member"
	case tenant != nil && okLang(*tenant):
		v.Effective, v.Source = *tenant, "tenant"
	default:
		if l := parseAcceptLanguage(accept); l != "" {
			v.Effective, v.Source = l, "browser"
		}
	}
	return v
}

func readTenantLanguage(ctx context.Context, tx pgx.Tx) (*string, error) {
	var l string
	err := tx.QueryRow(ctx, "select locale from tenant_language").Scan(&l)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &l, err
}

// languageFor è la lingua effettiva dell'utente nel tenant attivo (o solo dal browser).
func (s *Server) languageFor(r *http.Request, p *auth.Principal) (languageView, error) {
	var member, tenant *string
	if p.TenantID != "" {
		err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
			var err error
			if tenant, err = readTenantLanguage(r.Context(), tx); err != nil {
				return err
			}
			return tx.QueryRow(r.Context(), "select locale from members where id = $1", p.MemberID).Scan(&member)
		})
		if err != nil {
			return languageView{}, err
		}
	}
	return resolveLanguage(member, tenant, r.Header.Get("Accept-Language")), nil
}

func (s *Server) getLanguage(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	v, err := s.languageFor(r, p)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// normalizeLocale: stringa vuota o assente = nessuna scelta; il resto deve essere supportato.
func normalizeLocale(in *string) (*string, bool) {
	if in == nil || *in == "" {
		return nil, true
	}
	return in, okLang(*in)
}

func (s *Server) setMyLanguage(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in languageChoice
	if !decodeJSON(w, r, &in) {
		return
	}
	loc, ok := normalizeLocale(in.Locale)
	if !ok {
		validationProblem(w, r, "locale", "lingua non supportata")
		return
	}
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(), "update members set locale = $2, updated_at = now() where id = $1", p.MemberID, loc)
		return err
	})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.getLanguage(w, r, p)
}

func (s *Server) setTenantLanguage(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in languageChoice
	if !decodeJSON(w, r, &in) {
		return
	}
	loc, ok := normalizeLocale(in.Locale)
	if !ok {
		validationProblem(w, r, "locale", "lingua non supportata")
		return
	}
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		ctx := r.Context()
		if loc == nil {
			if _, err := tx.Exec(ctx, "delete from tenant_language"); err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, `insert into tenant_language (tenant_id, locale, updated_by)
			values (app_tenant(), $1, $2)
			on conflict (tenant_id) do update set locale = excluded.locale, updated_by = excluded.updated_by, updated_at = now()`,
			*loc, p.MemberID); err != nil {
			return err
		}
		return audit.Append(ctx, tx, audit.Event{Action: "tenant.language.update", EntityType: "tenant", EntityID: p.TenantID,
			Payload: map[string]any{"locale": loc}, ActorAccountID: p.AccountID, ActorMemberID: p.MemberID, IP: clientIP(r)})
	})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.getLanguage(w, r, p)
}

// langCtx lega al contesto la lingua effettiva di chi chiama (scelta del membro, poi tenant, poi
// italiano): il motore e i moduli la leggono con i18n.Lang per i testi che compongono (nomi dei
// fatti e delle azioni, «Sistema» nella storia). Un errore di lettura lascia l'italiano.
func langCtx(ctx context.Context, tx pgx.Tx, p *auth.Principal) context.Context {
	lang, err := i18n.MemberLang(ctx, tx, p.MemberID)
	if err != nil {
		lang = i18n.Default
	}
	return i18n.WithLang(ctx, lang)
}

// languageRoutes registra le rotte della lingua (chiamata da Handler).
func (s *Server) languageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/me/language", inTenant(s.getLanguage))
	mux.HandleFunc("PUT /api/v1/me/language", inTenant(s.setMyLanguage))
	mux.HandleFunc("PUT /api/v1/tenant/language", can(rbac.TenantAdmin, s.setTenantLanguage))
}
