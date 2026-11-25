package api

import (
	"net/http"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/report"
	"regia/internal/platform/auth"
)

// Catalogo del portale: le voci con il loro modulo e l'instradamento (docs/sviluppo/catalogo-portale.md).
func (s *Server) portalCatalogRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/portal/catalog", inTenant(s.catalogConfig))
	mux.HandleFunc("POST /api/v1/portal/catalog", inTenant(s.createCatalogEntry))
	mux.HandleFunc("PUT /api/v1/portal/catalog/order", inTenant(s.reorderCatalog))
	mux.HandleFunc("PUT /api/v1/portal/catalog/{key}", inTenant(s.updateCatalogEntry))
	mux.HandleFunc("POST /api/v1/portal/catalog/{key}/publish", inTenant(s.publishCatalogEntry))
	mux.HandleFunc("DELETE /api/v1/portal/catalog/{key}/draft", inTenant(s.discardPortalEntryDraft))
	mux.HandleFunc("POST /api/v1/portal/catalog/{key}/preview", inTenant(s.previewCatalogEntry))
	mux.HandleFunc("POST /api/v1/portal/support/{ref}", inTenant(s.portalSupport))
}

func (s *Server) catalogConfig(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*report.CatalogConfig, error) {
		return s.report.CatalogConfig(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) createCatalogEntry(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in report.EntryInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*report.Entry, error) {
		return s.report.CreateEntry(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) reorderCatalog(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Keys []string `json:"keys"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.report.ReorderEntries(r.Context(), tx, actorOf(r, p), in.Keys)
	})
}

func (s *Server) updateCatalogEntry(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in report.EntryPatch
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*report.Entry, error) {
		return s.report.UpdateEntry(r.Context(), tx, actorOf(r, p), r.PathValue("key"), in)
	})
}

func (s *Server) publishCatalogEntry(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*report.Entry, error) {
		return s.report.PublishEntry(r.Context(), tx, actorOf(r, p), r.PathValue("key"))
	})
}

func (s *Server) discardPortalEntryDraft(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*report.Entry, error) {
		return s.report.DiscardEntryDraft(r.Context(), tx, actorOf(r, p), r.PathValue("key"))
	})
}

func (s *Server) previewCatalogEntry(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in report.EntryForm
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*report.EntryPreview, error) {
		return s.report.PreviewEntry(r.Context(), tx, actorOf(r, p), r.PathValue("key"), in)
	})
}

func (s *Server) portalSupport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Comment string `json:"comment"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (map[string]string, error) {
		key, err := s.report.Support(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.Comment)
		return map[string]string{"key": key}, err
	})
}
