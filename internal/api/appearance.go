package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/audit"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// Aspetto dell'interfaccia (replica/design.md, "Temi e font"): l'utente sceglie tema e font
// per sé; l'amministratore del tenant sceglie il default dell'organizzazione. Ogni scelta
// vale campo per campo: chi sceglie solo il font tiene il tema dell'organizzazione.

// Chiavi ammesse: devono coincidere con web/src/appearance.ts.
var (
	appearanceThemes = []string{"atlante", "petrolio", "nebbia", "contrasto"}
	appearanceFonts  = []string{"atkinson", "plex", "source", "public"}
)

const (
	defaultTheme = "atlante"
	defaultFont  = "atkinson"
)

// appearance è una scelta, anche parziale: nil = nessuna scelta a questo livello.
type appearance struct {
	Theme *string `json:"theme"`
	Font  *string `json:"font"`
}

// effectiveAppearance è l'aspetto da applicare, sempre completo.
type effectiveAppearance struct {
	Theme string `json:"theme"`
	Font  string `json:"font"`
}

func resolveAppearance(user, tenant appearance) effectiveAppearance {
	out := effectiveAppearance{Theme: defaultTheme, Font: defaultFont}
	for _, a := range []appearance{tenant, user} {
		if a.Theme != nil && slices.Contains(appearanceThemes, *a.Theme) {
			out.Theme = *a.Theme
		}
		if a.Font != nil && slices.Contains(appearanceFonts, *a.Font) {
			out.Font = *a.Font
		}
	}
	return out
}

// normalize toglie le stringhe vuote (= nessuna scelta) e verifica le chiavi.
func (a *appearance) normalize() (field string, ok bool) {
	if a.Theme != nil && *a.Theme == "" {
		a.Theme = nil
	}
	if a.Font != nil && *a.Font == "" {
		a.Font = nil
	}
	if a.Theme != nil && !slices.Contains(appearanceThemes, *a.Theme) {
		return "theme", false
	}
	if a.Font != nil && !slices.Contains(appearanceFonts, *a.Font) {
		return "font", false
	}
	return "", true
}

func validationProblem(w http.ResponseWriter, r *http.Request, field, msg string) {
	writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"type": "about:blank", "title": "Unprocessable Entity",
		"status": http.StatusUnprocessableEntity, "detail": msg, "code": "validation", "field": field, "request_id": requestID(r.Context())})
}

// userAppearance legge la scelta personale dalle preferenze dell'account.
func (s *Server) userAppearance(ctx context.Context, accountID string) (appearance, error) {
	var a appearance
	var raw []byte
	err := s.db.Global(ctx, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "select coalesce(preferences->'appearance', '{}'::jsonb) from accounts where id = $1", accountID).Scan(&raw)
	})
	if err != nil {
		return a, err
	}
	err = json.Unmarshal(raw, &a)
	return a, err
}

type tenantAppearance struct {
	appearance
	UpdatedAt *time.Time `json:"updated_at"`
	UpdatedBy *string    `json:"updated_by"`
}

func readTenantAppearance(ctx context.Context, tx pgx.Tx) (tenantAppearance, error) {
	var t tenantAppearance
	err := tx.QueryRow(ctx, "select theme, font, updated_at, updated_by from tenant_appearance").
		Scan(&t.Theme, &t.Font, &t.UpdatedAt, &t.UpdatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return tenantAppearance{}, nil
	}
	return t, err
}

// tenantDefault è il default del tenant attivo; vuoto se non c'è un tenant attivo.
func (s *Server) tenantDefault(ctx context.Context, p *auth.Principal) (tenantAppearance, error) {
	var t tenantAppearance
	if p.TenantID == "" {
		return t, nil
	}
	err := s.db.InTenant(ctx, p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		t, err = readTenantAppearance(ctx, tx)
		return err
	})
	return t, err
}

// appearanceFor è l'aspetto effettivo dell'utente nel tenant attivo, per /me.
func (s *Server) appearanceFor(ctx context.Context, p *auth.Principal) (*effectiveAppearance, error) {
	u, err := s.userAppearance(ctx, p.AccountID)
	if err != nil {
		return nil, err
	}
	t, err := s.tenantDefault(ctx, p)
	if err != nil {
		return nil, err
	}
	e := resolveAppearance(u, t.appearance)
	return &e, nil
}

type preferencesView struct {
	Appearance    appearance          `json:"appearance"`
	TenantDefault *appearance         `json:"tenant_default"`
	Effective     effectiveAppearance `json:"effective"`
	Options       map[string][]string `json:"options"`
}

func (s *Server) preferencesView(ctx context.Context, p *auth.Principal) (preferencesView, error) {
	u, err := s.userAppearance(ctx, p.AccountID)
	if err != nil {
		return preferencesView{}, err
	}
	t, err := s.tenantDefault(ctx, p)
	if err != nil {
		return preferencesView{}, err
	}
	out := preferencesView{Appearance: u, Effective: resolveAppearance(u, t.appearance),
		Options: map[string][]string{"themes": appearanceThemes, "fonts": appearanceFonts}}
	if p.TenantID != "" {
		out.TenantDefault = &t.appearance
	}
	return out, nil
}

func (s *Server) getPreferences(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	out, err := s.preferencesView(r.Context(), p)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) setPreferences(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Appearance appearance `json:"appearance"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if field, ok := in.Appearance.normalize(); !ok {
		validationProblem(w, r, field, "valore non ammesso per "+field)
		return
	}
	// Si salvano solo le chiavi scelte: una chiave assente segue il default del tenant.
	stored := map[string]string{}
	if in.Appearance.Theme != nil {
		stored["theme"] = *in.Appearance.Theme
	}
	if in.Appearance.Font != nil {
		stored["font"] = *in.Appearance.Font
	}
	err := s.db.Global(r.Context(), func(tx pgx.Tx) error {
		_, err := tx.Exec(r.Context(),
			"update accounts set preferences = jsonb_set(preferences, '{appearance}', $2::jsonb), updated_at = now() where id = $1",
			p.AccountID, stored)
		return err
	})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.getPreferences(w, r, p)
}

func (s *Server) getTenantAppearance(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (tenantAppearance, error) {
		return readTenantAppearance(r.Context(), tx)
	})
}

func (s *Server) setTenantAppearance(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in appearance
	if !decodeJSON(w, r, &in) {
		return
	}
	if field, ok := in.normalize(); !ok {
		validationProblem(w, r, field, "valore non ammesso per "+field)
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (tenantAppearance, error) {
		ctx := r.Context()
		if _, err := tx.Exec(ctx, `insert into tenant_appearance (tenant_id, theme, font, updated_by)
			values (app_tenant(), $1, $2, $3)
			on conflict (tenant_id) do update set theme = excluded.theme, font = excluded.font,
			  updated_by = excluded.updated_by, updated_at = now()`, in.Theme, in.Font, p.MemberID); err != nil {
			return tenantAppearance{}, err
		}
		if err := audit.Append(ctx, tx, audit.Event{Action: "tenant.appearance.update", EntityType: "tenant", EntityID: p.TenantID,
			Payload: map[string]any{"theme": in.Theme, "font": in.Font}, ActorAccountID: p.AccountID, ActorMemberID: p.MemberID, IP: clientIP(r)}); err != nil {
			return tenantAppearance{}, err
		}
		return readTenantAppearance(ctx, tx)
	})
}

// appearanceRoutes registra le rotte dell'aspetto (chiamata da Handler).
func (s *Server) appearanceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/me/preferences", authed(s.getPreferences))
	mux.HandleFunc("PUT /api/v1/me/preferences", authed(s.setPreferences))
	mux.HandleFunc("GET /api/v1/tenant/appearance", inTenant(s.getTenantAppearance))
	mux.HandleFunc("PUT /api/v1/tenant/appearance", can(rbac.TenantAdmin, s.setTenantAppearance))
}
