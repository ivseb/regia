package api

import (
	"net/http"

	"regia/internal/engine/webhooks"
	"regia/internal/platform/auth"
)

// Configurazione → Integrazioni → Webhook (piano, passo 15). Le regole stanno in
// internal/engine/webhooks; il worker delle consegne parte da serve.go.

func (s *Server) webhookRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/admin/webhooks", adminOnly(s.listWebhooks))
	mux.HandleFunc("POST /api/v1/admin/webhooks", adminSession(s.createWebhook))
	mux.HandleFunc("PATCH /api/v1/admin/webhooks/{id}", adminSession(s.updateWebhook))
	mux.HandleFunc("DELETE /api/v1/admin/webhooks/{id}", adminSession(s.deleteWebhook))
	mux.HandleFunc("POST /api/v1/admin/webhooks/{id}/secret", adminSession(s.rotateWebhookSecret))
	mux.HandleFunc("POST /api/v1/admin/webhooks/{id}/ping", adminSession(s.pingWebhook))
	mux.HandleFunc("GET /api/v1/admin/webhooks/{id}/deliveries", adminOnly(s.webhookDeliveries))
	mux.HandleFunc("POST /api/v1/admin/webhook-deliveries/{id}/redeliver", adminSession(s.redeliverWebhook))
}

func hookActor(r *http.Request, p *auth.Principal) webhooks.Actor {
	return webhooks.Actor{TenantID: p.TenantID, AccountID: p.AccountID, MemberID: p.MemberID, IP: clientIP(r)}
}

func (s *Server) listWebhooks(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	items, err := s.adm.hooks.List(r.Context(), p.TenantID)
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "events": webhooks.Events, "version": webhooks.Version,
		"allow_private": s.adm.hooks.AllowPrivate()})
}

func (s *Server) createWebhook(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in webhooks.Input
	if !decodeJSON(w, r, &in) {
		return
	}
	id, secret, err := s.adm.hooks.Create(r.Context(), hookActor(r, p), in)
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "secret": secret})
}

func (s *Server) updateWebhook(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in webhooks.Input
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := s.adm.hooks.Update(r.Context(), hookActor(r, p), r.PathValue("id"), in); err != nil {
		s.adminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteWebhook(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := s.adm.hooks.Delete(r.Context(), hookActor(r, p), r.PathValue("id")); err != nil {
		s.adminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) rotateWebhookSecret(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	secret, err := s.adm.hooks.RotateSecret(r.Context(), hookActor(r, p), r.PathValue("id"))
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"secret": secret})
}

func (s *Server) pingWebhook(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	id, err := s.adm.hooks.Ping(r.Context(), hookActor(r, p), r.PathValue("id"))
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"delivery_id": id})
}

func (s *Server) webhookDeliveries(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	items, err := s.adm.hooks.Deliveries(r.Context(), p.TenantID, r.PathValue("id"))
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) redeliverWebhook(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	id, err := s.adm.hooks.Redeliver(r.Context(), hookActor(r, p), r.PathValue("id"))
	if err != nil {
		s.adminError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"delivery_id": id})
}
