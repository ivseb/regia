package api

// Configurazione → Persone e accessi → Identità aziendale: creazione al primo accesso,
// claim dei gruppi, mappatura dei gruppi dell'IdP e token SCIM (internal/platform/admin/idp.go).

import (
	"net/http"

	"regia/internal/platform/admin"
	"regia/internal/platform/auth"
)

func (s *Server) idpRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/admin/idp", adminOnly(s.adminIdP))
	mux.HandleFunc("PUT /api/v1/admin/idp/settings", adminSession(s.adminSetIdP))
	mux.HandleFunc("POST /api/v1/admin/idp/groups", adminSession(s.adminCreateIdPGroup))
	mux.HandleFunc("DELETE /api/v1/admin/idp/groups/{id}", adminSession(s.adminDeleteIdPGroup))
	mux.HandleFunc("POST /api/v1/admin/idp/mappings", adminSession(s.adminCreateMapping))
	mux.HandleFunc("DELETE /api/v1/admin/idp/mappings/{id}", adminSession(s.adminDeleteMapping))
	mux.HandleFunc("POST /api/v1/admin/idp/scim-tokens", adminSession(s.adminCreateSCIMToken))
	mux.HandleFunc("DELETE /api/v1/admin/idp/scim-tokens/{id}", adminSession(s.adminRevokeSCIMToken))
}

func (s *Server) adminIdP(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	out, err := s.adm.admin.IdP(r.Context(), p.TenantID)
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	base := s.adm.baseURL
	if base == "" {
		scheme := "https"
		if r.TLS == nil && s.dev {
			scheme = "http"
		}
		base = scheme + "://" + r.Host
	}
	oidc := map[string]any{"enabled": s.oidc != nil, "name": ""}
	if s.oidc != nil {
		oidc["name"] = s.oidc.Name()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": out.Settings, "groups": out.Groups, "scim_tokens": out.SCIMTokens, "scim_active": out.SCIMActive,
		"scim_base_url": base + scimPrefix, "oidc": oidc,
		"all_members_roles": out.AllMembersRoles, "only_viewer": out.OnlyViewer,
	})
}

func (s *Server) adminSetIdP(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in admin.IdPSettings
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := s.adm.admin.SetIdPSettings(r.Context(), adminActor(r, p), in); err != nil {
		s.adminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminCreateIdPGroup(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		ExternalID  string `json:"external_id"`
		DisplayName string `json:"display_name"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	id, err := s.adm.admin.CreateIdPGroup(r.Context(), adminActor(r, p), in.ExternalID, in.DisplayName)
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) adminDeleteIdPGroup(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := s.adm.admin.DeleteIdPGroup(r.Context(), adminActor(r, p), r.PathValue("id")); err != nil {
		s.adminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminCreateMapping(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		IdPGroupID string `json:"idp_group_id"`
		GroupID    string `json:"group_id"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	id, err := s.adm.admin.CreateMapping(r.Context(), adminActor(r, p), in.IdPGroupID, in.GroupID)
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) adminDeleteMapping(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := s.adm.admin.DeleteMapping(r.Context(), adminActor(r, p), r.PathValue("id")); err != nil {
		s.adminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) adminCreateSCIMToken(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	secret, t, err := s.adm.admin.CreateSCIMToken(r.Context(), adminActor(r, p), in.Name)
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"token": secret, "item": t})
}

func (s *Server) adminRevokeSCIMToken(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := s.adm.admin.RevokeSCIMToken(r.Context(), adminActor(r, p), r.PathValue("id")); err != nil {
		s.adminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
