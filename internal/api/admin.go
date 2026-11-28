package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine/notify"
	"regia/internal/engine/webhooks"
	"regia/internal/platform/admin"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/i18n"
	"regia/internal/platform/rbac"
	"regia/internal/platform/secretbox"
)

// Configurazione → Persone e accessi (amministrazione da interfaccia), Registro di audit,
// MFA (mfa.go) e webhook (webhooks.go). Le regole stanno in internal/platform/admin.

// adminState sono i servizi dell'amministrazione, creati da New con adminInit.
type adminState struct {
	admin      *admin.Service
	hooks      *webhooks.Service
	inviteMail notify.Sender
	baseURL    string
}

func (s *Server) adminInit(o Options) {
	off := func() bool { return false }
	if o.Auth != nil {
		off = o.Auth.PasswordLoginDisabled
	}
	s.adm.admin = admin.New(o.DB, admin.Options{PasswordOff: off})
	if o.Auth != nil {
		// Login OIDC: creazione al primo accesso e gruppi dall'IdP (admin/idp.go).
		o.Auth.SetOIDCHook(s.adm.admin)
	}
	s.adm.hooks = o.Webhooks
	if s.adm.hooks == nil {
		box := o.SecretBox
		if box == nil {
			box = secretbox.Random()
		}
		s.adm.hooks = webhooks.New(webhooks.Options{DB: o.DB, Eng: o.Engine, Box: box, AllowPrivate: o.Dev || o.WebhookAllowPrivate,
			BaseURL: o.BaseURL, Log: o.Log})
	}
	s.adm.inviteMail = o.InviteMail
	s.adm.baseURL = strings.TrimRight(o.BaseURL, "/")
}

// adminOnly: tenant.admin, per le letture (anche con un token API con quel permesso).
func adminOnly(h func(http.ResponseWriter, *http.Request, *auth.Principal)) http.HandlerFunc {
	return can(rbac.TenantAdmin, h)
}

// adminSession: tenant.admin e una sessione con cookie, per ogni modifica di persone,
// accessi e integrazioni (un token API rubato non crea amministratori né link).
func adminSession(h func(http.ResponseWriter, *http.Request, *auth.Principal)) http.HandlerFunc {
	return can(rbac.TenantAdmin, func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		if p.TokenID != "" {
			writeProblem(w, r, http.StatusForbidden, "auth.session_required", "operazione non consentita con un token API")
			return
		}
		h(w, r, p)
	})
}

func (s *Server) adminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/admin/members", adminOnly(s.adminMembers))
	mux.HandleFunc("POST /api/v1/admin/members", adminSession(s.adminAddMember))
	mux.HandleFunc("GET /api/v1/admin/members/{id}", adminOnly(s.adminMember))
	mux.HandleFunc("GET /api/v1/admin/members/{id}/access", adminOnly(s.adminMemberAccess))
	mux.HandleFunc("PATCH /api/v1/admin/members/{id}", adminSession(s.adminUpdateMember))
	mux.HandleFunc("POST /api/v1/admin/members/{id}/suspend", adminSession(s.adminSuspend))
	mux.HandleFunc("POST /api/v1/admin/members/{id}/reactivate", adminSession(s.adminReactivate))
	mux.HandleFunc("POST /api/v1/admin/members/{id}/invitation", adminSession(s.adminResendInvite))
	mux.HandleFunc("POST /api/v1/admin/members/{id}/password-link", adminSession(s.adminPasswordLink))
	mux.HandleFunc("POST /api/v1/admin/members/{id}/mfa-reset", adminSession(s.adminResetMFA))

	mux.HandleFunc("GET /api/v1/admin/roles", adminOnly(s.adminRoles))
	mux.HandleFunc("POST /api/v1/admin/roles", adminSession(s.adminCreateRole))
	mux.HandleFunc("PATCH /api/v1/admin/roles/{id}", adminSession(s.adminUpdateRole))
	mux.HandleFunc("DELETE /api/v1/admin/roles/{id}", adminSession(s.adminDeleteRole))
	mux.HandleFunc("POST /api/v1/admin/roles/{id}/assignments", adminSession(s.adminAssign))
	mux.HandleFunc("DELETE /api/v1/admin/role-assignments/{id}", adminSession(s.adminUnassign))

	mux.HandleFunc("GET /api/v1/admin/groups", adminOnly(s.adminGroups))
	mux.HandleFunc("POST /api/v1/admin/groups", adminSession(s.adminCreateGroup))
	mux.HandleFunc("PATCH /api/v1/admin/groups/{id}", adminSession(s.adminUpdateGroup))
	mux.HandleFunc("DELETE /api/v1/admin/groups/{id}", adminSession(s.adminDeleteGroup))
	mux.HandleFunc("POST /api/v1/admin/groups/{id}/members", adminSession(s.adminAddGroupMember))
	mux.HandleFunc("DELETE /api/v1/admin/groups/{id}/members/{member}", adminSession(s.adminRemoveGroupMember))

	mux.HandleFunc("GET /api/v1/admin/stakeholder-roles", adminOnly(s.adminStakeholderRoles))
	mux.HandleFunc("POST /api/v1/admin/stakeholder-roles", adminSession(s.adminCreateStakeholderRole))
	mux.HandleFunc("PATCH /api/v1/admin/stakeholder-roles/{id}", adminSession(s.adminRenameStakeholderRole))

	s.idpRoutes(mux)

	mux.HandleFunc("GET /api/v1/admin/security", adminOnly(s.adminSecurity))
	mux.HandleFunc("PUT /api/v1/admin/security", adminSession(s.adminSetSecurity))

	mux.HandleFunc("GET /api/v1/audit/verify", can(rbac.AuditRead, s.auditVerify))

	s.mfaRoutes(mux)
	s.webhookRoutes(mux)
}

func adminActor(r *http.Request, p *auth.Principal) admin.Actor {
	return admin.Actor{TenantID: p.TenantID, AccountID: p.AccountID, MemberID: p.MemberID, SessionID: p.SessionID, IP: clientIP(r)}
}

// adminError traduce gli errori di amministrazione e webhook in risposte.
func (s *Server) adminError(w http.ResponseWriter, r *http.Request, err error) {
	var ae *admin.Error
	var we *webhooks.Error
	switch {
	case errors.As(err, &ae):
		problemField(w, r, ae.Status, ae.Code, ae.Msg, ae.Field)
	case errors.As(err, &we):
		problemField(w, r, we.Status, we.Code, we.Msg, we.Field)
	default:
		s.internal(w, r, err)
	}
}

// problemField è writeProblem con il campo dell'errore (per evidenziarlo nel form).
func problemField(w http.ResponseWriter, r *http.Request, status int, code, detail, field string) {
	body := map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "detail": detail,
		"code": code, "request_id": requestID(r.Context())}
	if field != "" {
		body["field"] = field
	}
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	writeJSONBody(w, body)
}

// linkView è un link monouso da consegnare: percorso dell'interfaccia e, con
// REGIA_BASE_URL, l'indirizzo completo. Non è mai salvato in chiaro.
func (s *Server) linkView(l *admin.Link) map[string]any {
	if l == nil {
		return nil
	}
	path := "/invito/" + l.Token
	if l.Kind == "password" {
		path = "/password/" + l.Token
	}
	out := map[string]any{"kind": l.Kind, "path": path, "expires_at": l.ExpiresAt}
	if s.adm.baseURL != "" {
		out["url"] = s.adm.baseURL + path
	}
	return out
}

// mailLink prova a inviare il link per email (solo con SMTP e REGIA_BASE_URL), nella lingua
// della persona invitata (la sua scelta, poi quella del tenant).
func (s *Server) mailLink(ctx context.Context, p *auth.Principal, memberID, email string, l *admin.Link) bool {
	if s.adm.inviteMail == nil || s.adm.baseURL == "" || l == nil || email == "" {
		return false
	}
	lang := i18n.Default
	_ = s.db.InTenant(ctx, p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		lang, err = i18n.MemberLang(ctx, tx, memberID)
		return err
	})
	view := s.linkView(l)
	expires := l.ExpiresAt.Format("02/01/2006 15:04")
	if lang == i18n.En {
		expires = l.ExpiresAt.Format("Jan 2, 2006 15:04")
	}
	params := i18n.P{"tenant": tenantName(p), "expires": expires, "url": view["url"]}
	subject, body := i18n.T(lang, "mail.invite.subject", params), i18n.T(lang, "mail.invite.body", params)
	if l.Kind == "password" {
		subject, body = i18n.T(lang, "mail.password.subject", params), i18n.T(lang, "mail.password.body", params)
	}
	if err := s.adm.inviteMail.Send(ctx, email, subject, body); err != nil {
		s.log.Warn("invio del link per email", "errore", err, "request_id", requestID(ctx))
		return false
	}
	return true
}

func tenantName(p *auth.Principal) string {
	for _, m := range p.Tenants {
		if m.TenantID == p.TenantID {
			return m.Name
		}
	}
	return "Regia"
}

// ---------------------------------------------------------------------
// Membri
// ---------------------------------------------------------------------

func (s *Server) adminMembers(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	items, err := s.adm.admin.ListMembers(r.Context(), p.TenantID, r.URL.Query().Get("q"))
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "me": p.MemberID, "sso_only": s.auth.PasswordLoginDisabled(),
		"email_available": s.adm.inviteMail != nil && s.adm.baseURL != ""})
}

func (s *Server) adminMember(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	m, err := s.adm.admin.GetMember(r.Context(), p.TenantID, r.PathValue("id"))
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// adminMemberAccess spiega da dove arrivano i permessi effettivi di un membro.
func (s *Server) adminMemberAccess(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	out, err := s.adm.admin.ExplainAccess(r.Context(), p.TenantID, r.PathValue("id"))
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) adminAddMember(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in admin.NewMember
	if !decodeJSON(w, r, &in) {
		return
	}
	out, err := s.adm.admin.AddMember(r.Context(), adminActor(r, p), in)
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"member_id": out.MemberID, "new_account": out.NewAccount,
		"link": s.linkView(out.Link), "emailed": s.mailLink(r.Context(), p, out.MemberID, strings.ToLower(strings.TrimSpace(in.Email)), out.Link)})
}

func (s *Server) adminUpdateMember(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		DisplayName *string `json:"display_name"`
		MemberType  *string `json:"member_type"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := s.adm.admin.UpdateMember(r.Context(), adminActor(r, p), r.PathValue("id"), in.DisplayName, in.MemberType); err != nil {
		s.adminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminSuspend(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := s.adm.admin.Suspend(r.Context(), adminActor(r, p), r.PathValue("id")); err != nil {
		s.adminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminReactivate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := s.adm.admin.Reactivate(r.Context(), adminActor(r, p), r.PathValue("id")); err != nil {
		s.adminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) memberEmail(r *http.Request, p *auth.Principal, id string) string {
	m, err := s.adm.admin.GetMember(r.Context(), p.TenantID, id)
	if err != nil {
		return ""
	}
	return m.Email
}

func (s *Server) adminResendInvite(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	l, err := s.adm.admin.ResendInvitation(r.Context(), adminActor(r, p), r.PathValue("id"))
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"link": s.linkView(l), "emailed": s.mailLink(r.Context(), p, r.PathValue("id"), s.memberEmail(r, p, r.PathValue("id")), l)})
}

func (s *Server) adminPasswordLink(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	l, err := s.adm.admin.PasswordLink(r.Context(), adminActor(r, p), r.PathValue("id"))
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"link": s.linkView(l), "emailed": s.mailLink(r.Context(), p, r.PathValue("id"), s.memberEmail(r, p, r.PathValue("id")), l)})
}

func (s *Server) adminResetMFA(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := s.adm.admin.ResetMFA(r.Context(), adminActor(r, p), r.PathValue("id")); err != nil {
		s.adminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------
// Ruoli
// ---------------------------------------------------------------------

func (s *Server) adminRoles(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	items, err := s.adm.admin.ListRoles(r.Context(), p.TenantID)
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "permissions": rbac.Catalog()})
}

func (s *Server) adminCreateRole(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in admin.RoleInput
	if !decodeJSON(w, r, &in) {
		return
	}
	id, err := s.adm.admin.CreateRole(r.Context(), adminActor(r, p), in)
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) adminUpdateRole(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in admin.RoleInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := s.adm.admin.UpdateRole(r.Context(), adminActor(r, p), r.PathValue("id"), in); err != nil {
		s.adminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminDeleteRole(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := s.adm.admin.DeleteRole(r.Context(), adminActor(r, p), r.PathValue("id")); err != nil {
		s.adminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminAssign(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		MemberID string `json:"member_id"`
		GroupID  string `json:"group_id"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	id, err := s.adm.admin.Assign(r.Context(), adminActor(r, p), r.PathValue("id"), in.MemberID, in.GroupID)
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) adminUnassign(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := s.adm.admin.Unassign(r.Context(), adminActor(r, p), r.PathValue("id")); err != nil {
		s.adminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------
// Gruppi e ruoli sugli oggetti
// ---------------------------------------------------------------------

func (s *Server) adminGroups(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	items, err := s.adm.admin.ListGroups(r.Context(), p.TenantID)
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) adminCreateGroup(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in admin.GroupInput
	if !decodeJSON(w, r, &in) {
		return
	}
	id, err := s.adm.admin.CreateGroup(r.Context(), adminActor(r, p), in)
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) adminUpdateGroup(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in admin.GroupInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := s.adm.admin.UpdateGroup(r.Context(), adminActor(r, p), r.PathValue("id"), in); err != nil {
		s.adminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminDeleteGroup(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := s.adm.admin.DeleteGroup(r.Context(), adminActor(r, p), r.PathValue("id")); err != nil {
		s.adminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminAddGroupMember(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		MemberID string `json:"member_id"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := s.adm.admin.AddGroupMember(r.Context(), adminActor(r, p), r.PathValue("id"), in.MemberID); err != nil {
		s.adminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminRemoveGroupMember(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := s.adm.admin.RemoveGroupMember(r.Context(), adminActor(r, p), r.PathValue("id"), r.PathValue("member")); err != nil {
		s.adminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminStakeholderRoles(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	items, err := s.adm.admin.ListStakeholderRoles(r.Context(), p.TenantID)
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) adminCreateStakeholderRole(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	id, err := s.adm.admin.CreateStakeholderRole(r.Context(), adminActor(r, p), in.Key, in.Name)
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) adminRenameStakeholderRole(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := s.adm.admin.RenameStakeholderRole(r.Context(), adminActor(r, p), r.PathValue("id"), in.Name); err != nil {
		s.adminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---------------------------------------------------------------------
// Sicurezza e audit
// ---------------------------------------------------------------------

func (s *Server) adminSecurity(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	sec, err := s.adm.admin.GetSecurity(r.Context(), p.TenantID)
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, sec)
}

func (s *Server) adminSetSecurity(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		MFAPolicy        string `json:"mfa_policy"`
		GroupInheritance *bool  `json:"group_inheritance"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	var closed int64
	var err error
	if in.GroupInheritance != nil {
		if err = s.adm.admin.SetGroupInheritance(r.Context(), adminActor(r, p), *in.GroupInheritance); err != nil {
			s.adminError(w, r, err)
			return
		}
	}
	if in.MFAPolicy != "" || in.GroupInheritance == nil {
		if closed, err = s.adm.admin.SetSecurity(r.Context(), adminActor(r, p), in.MFAPolicy); err != nil {
			s.adminError(w, r, err)
			return
		}
	}
	sec, err := s.adm.admin.GetSecurity(r.Context(), p.TenantID)
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"security": sec, "sessions_closed": closed})
}

// auditVerify ricalcola la catena di hash del tenant e dice se è integra.
func (s *Server) auditVerify(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var out struct {
		Entries   int64   `json:"entries"`
		BrokenSeq *int64  `json:"broken_seq"`
		LastSeq   *int64  `json:"last_seq"`
		LastHash  *string `json:"last_hash"`
		Intact    bool    `json:"intact"`
	}
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		return tx.QueryRow(r.Context(), "select entries, broken_seq, last_seq, last_hash from audit_chain_status()").
			Scan(&out.Entries, &out.BrokenSeq, &out.LastSeq, &out.LastHash)
	})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out.Intact = out.BrokenSeq == nil
	writeJSON(w, http.StatusOK, out)
}

type auditFilter struct {
	action, entityType, entity, actor any
	from, to                          any
}

// auditFilters legge i filtri del Registro di audit: action, entity_type, entity_id,
// actor (id del membro), from e to (date AAAA-MM-GG o istanti RFC 3339; to escluso).
func auditFilters(w http.ResponseWriter, r *http.Request) (auditFilter, bool) {
	q := r.URL.Query()
	f := auditFilter{}
	str := func(k string, max int) any {
		v := strings.TrimSpace(q.Get(k))
		if v == "" || len(v) > max {
			return nil
		}
		return v
	}
	f.action, f.entityType = str("action", 80), str("entity_type", 80)
	for _, k := range []string{"entity_id", "actor"} {
		v := strings.TrimSpace(q.Get(k))
		if v == "" {
			continue
		}
		if !admin.ValidID(v) {
			writeProblem(w, r, http.StatusBadRequest, "request.invalid_param", k+" deve essere un identificativo")
			return f, false
		}
		if k == "entity_id" {
			f.entity = v
		} else {
			f.actor = v
		}
	}
	for _, k := range []string{"from", "to"} {
		v := strings.TrimSpace(q.Get(k))
		if v == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			d, derr := time.ParseInLocation("2006-01-02", v, time.Local)
			if derr != nil {
				writeProblem(w, r, http.StatusBadRequest, "request.invalid_param", k+": data non valida (AAAA-MM-GG)")
				return f, false
			}
			t = d
			if k == "to" {
				t = t.AddDate(0, 0, 1) // "fino al" comprende tutto il giorno
			}
		}
		if k == "from" {
			f.from = t
		} else {
			f.to = t
		}
	}
	return f, true
}

// auditActors risolve i nomi dei membri che compaiono in una pagina di audit.
func auditActors(ctx context.Context, tx pgx.Tx, items []auditEntry) (map[string]string, error) {
	ids := []string{}
	seen := map[string]bool{}
	for _, it := range items {
		for _, id := range []*string{it.ActorMemberID} {
			if id != nil && !seen[*id] {
				seen[*id] = true
				ids = append(ids, *id)
			}
		}
	}
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := tx.Query(ctx, "select id::text, display_name from members where id = any($1::uuid[])", ids)
	if err != nil {
		return nil, err
	}
	var id, name string
	_, err = pgx.ForEachRow(rows, []any{&id, &name}, func() error {
		out[id] = name
		return nil
	})
	return out, err
}
