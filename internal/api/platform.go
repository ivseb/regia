package api

import (
	"errors"
	"net/http"
	"net/mail"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/admin"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/i18n"
	"regia/internal/platform/tenant"
)

// Organizzazioni (tenant) dall'interfaccia: elenco e creazione, riservati al superamministratore
// dell'istanza (accounts.is_platform_admin, che si dà con «regia platform-admin grant»).
// Le stesse regole di «regia tenant create»: stesse funzioni, stessi moduli di partenza.
// Vedi docs/sviluppo/organizzazioni.md.

var currencyRe = regexp.MustCompile(`^[A-Z]{3}$`)

// Provisioners sono le configurazioni iniziali dei moduli di un tenant nuovo: un punto solo,
// usato dal comando «regia tenant create» e dall'interfaccia.
func Provisioners() []tenant.Provisioner {
	out := make([]tenant.Provisioner, 0, 10)
	for _, p := range avvioProvisioners() {
		out = append(out, p)
	}
	return out
}

// platformAdmin richiede una sessione con cookie e il ruolo di superamministratore.
func (s *Server) platformAdmin(h func(http.ResponseWriter, *http.Request, *auth.Principal)) http.HandlerFunc {
	return session(func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		if !p.IsPlatformAdmin {
			s.log.Warn("organizzazioni: accesso negato, non è superamministratore", "account", p.AccountID, "request_id", requestID(r.Context()))
			writeProblem(w, r, http.StatusForbidden, "platform.admin_required", "riservato al superamministratore dell'istanza")
			return
		}
		h(w, r, p)
	})
}

func (s *Server) platformRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/platform/tenants", s.platformAdmin(s.platformTenants))
	mux.HandleFunc("POST /api/v1/platform/tenants", s.platformAdmin(s.platformCreateTenant))
}

func (s *Server) platformTenants(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	rows, err := tenant.List(r.Context(), s.db)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": rows})
}

type newTenantBody struct {
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	Language string `json:"language"`
	Currency string `json:"base_currency"`
	// AdminEmail è facoltativa: un'altra persona da invitare come amministratore.
	AdminEmail string `json:"admin_email"`
}

func (s *Server) platformCreateTenant(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in newTenantBody
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Name, in.Slug = strings.TrimSpace(in.Name), strings.ToLower(strings.TrimSpace(in.Slug))
	in.Currency, in.AdminEmail = strings.ToUpper(strings.TrimSpace(in.Currency)), strings.ToLower(strings.TrimSpace(in.AdminEmail))
	if in.Currency == "" {
		in.Currency = "EUR"
	}
	bad := func(code, field, msg string) {
		problemField(w, r, http.StatusUnprocessableEntity, code, msg, field)
	}
	switch {
	case in.Name == "" || utf8.RuneCountInString(in.Name) > 120:
		bad("validation.organizzazioni.name_invalid", "name", "il nome è obbligatorio (massimo 120 caratteri)")
		return
	case !tenant.ValidSlug(in.Slug):
		bad("validation.organizzazioni.slug_invalid", "slug", "identificativo non valido: minuscole, cifre e trattini, da 2 a 63 caratteri")
		return
	case in.Language != "" && !i18n.OK(in.Language):
		bad("validation.organizzazioni.language_invalid", "language", "lingua non supportata")
		return
	case !currencyRe.MatchString(in.Currency):
		bad("validation.organizzazioni.currency_invalid", "base_currency", "valuta non valida: codice di tre lettere, per esempio EUR")
		return
	}
	if in.AdminEmail != "" {
		a, err := mail.ParseAddress(in.AdminEmail)
		if err != nil || a.Address != in.AdminEmail || len(in.AdminEmail) > 254 {
			bad("validation.organizzazioni.admin_email_invalid", "admin_email", "indirizzo email non valido")
			return
		}
	}
	// Chi crea diventa amministratore con il proprio account: nessuna password nuova.
	name := p.DisplayName
	if name == "" {
		name = p.Email
	}
	out, err := tenant.Create(r.Context(), s.db, tenant.CreateInput{Slug: in.Slug, Name: in.Name, AdminEmail: p.Email, AdminName: name,
		BaseCurrency: in.Currency, Language: in.Language, Provision: Provisioners()})
	if errors.Is(err, tenant.ErrSlugTaken) {
		problemField(w, r, http.StatusConflict, "validation.organizzazioni.slug_taken", "esiste già un'organizzazione con questo identificativo", "slug")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	res := map[string]any{"tenant_id": out.TenantID, "slug": in.Slug, "name": in.Name}
	if in.AdminEmail != "" && in.AdminEmail != strings.ToLower(p.Email) {
		inv, err := s.inviteFirstAdmin(r, p, out, in)
		if err != nil {
			// L'organizzazione c'è: l'invito si può rifare da Configurazione → Persone.
			s.log.Warn("organizzazioni: invito del primo amministratore non riuscito", "errore", err, "request_id", requestID(r.Context()))
			res["invite_error"] = true
		} else {
			res["invite"] = inv
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, res)
}

// inviteFirstAdmin invita un'altra persona nell'organizzazione appena creata, con il ruolo di amministratore.
func (s *Server) inviteFirstAdmin(r *http.Request, p *auth.Principal, out tenant.Created, in newTenantBody) (map[string]any, error) {
	ctx := r.Context()
	var roleID string
	if err := s.db.InTenant(ctx, out.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "select id from roles where key = 'tenant_admin'").Scan(&roleID)
	}); err != nil {
		return nil, err
	}
	actor := admin.Actor{TenantID: out.TenantID, AccountID: p.AccountID, MemberID: out.AdminMemberID, SessionID: p.SessionID, IP: clientIP(r)}
	c, err := s.adm.admin.AddMember(ctx, actor, admin.NewMember{Email: in.AdminEmail, Mode: "invite", RoleIDs: []string{roleID}})
	if err != nil {
		return nil, err
	}
	// Il testo dell'email nomina la nuova organizzazione, non quella attiva.
	pc := *p
	pc.TenantID = out.TenantID
	pc.Tenants = append(append([]auth.Membership{}, p.Tenants...), auth.Membership{TenantID: out.TenantID, Slug: in.Slug, Name: in.Name})
	return map[string]any{"email": in.AdminEmail, "link": s.linkView(c.Link), "emailed": s.mailLink(ctx, &pc, c.MemberID, in.AdminEmail, c.Link)}, nil
}
