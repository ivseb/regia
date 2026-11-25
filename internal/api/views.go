package api

import (
	"net/http"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/views"
	"regia/internal/platform/audit"
	"regia/internal/platform/auth"
	"regia/internal/platform/rbac"
)

// Viste salvate delle liste (piano, passo 16; modulo views).

func viewerOf(p *auth.Principal) views.Viewer {
	return views.Viewer{MemberID: p.MemberID, Admin: p.Can(rbac.TenantAdmin)}
}

func (s *Server) viewsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/views", inTenant(s.listViews))
	mux.HandleFunc("POST /api/v1/views", inTenant(s.createView))
	mux.HandleFunc("GET /api/v1/views/favorites", inTenant(s.favoriteViews))
	mux.HandleFunc("GET /api/v1/views/groups", inTenant(s.viewGroups))
	mux.HandleFunc("PUT /api/v1/views/default", inTenant(s.defaultView))
	mux.HandleFunc("GET /api/v1/views/{id}", inTenant(s.getView))
	mux.HandleFunc("PATCH /api/v1/views/{id}", inTenant(s.updateView))
	mux.HandleFunc("DELETE /api/v1/views/{id}", inTenant(s.deleteView))
	mux.HandleFunc("POST /api/v1/views/{id}/duplicate", inTenant(s.duplicateView))
	mux.HandleFunc("PUT /api/v1/views/{id}/favorite", inTenant(s.favoriteView))
}

func (s *Server) auditView(r *http.Request, tx pgx.Tx, p *auth.Principal, action, id string) error {
	return audit.Append(r.Context(), tx, audit.Event{Action: action, EntityType: "view", EntityID: id,
		ActorAccountID: p.AccountID, ActorMemberID: p.MemberID, IP: clientIP(r)})
}

func (s *Server) listViews(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := views.List(r.Context(), tx, viewerOf(p), r.URL.Query().Get("page"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) getView(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*views.View, error) {
		return views.Get(r.Context(), tx, viewerOf(p), r.PathValue("id"))
	})
}

func (s *Server) createView(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in views.Input
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*views.View, error) {
		v, err := views.Create(r.Context(), tx, viewerOf(p), in)
		if err != nil {
			return nil, err
		}
		return v, s.auditView(r, tx, p, "view.create", v.ID)
	})
}

func (s *Server) updateView(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in views.Patch
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*views.View, error) {
		v, err := views.Update(r.Context(), tx, viewerOf(p), r.PathValue("id"), in)
		if err != nil {
			return nil, err
		}
		return v, s.auditView(r, tx, p, "view.update", v.ID)
	})
}

func (s *Server) deleteView(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		id := r.PathValue("id")
		if err := views.Delete(r.Context(), tx, viewerOf(p), id); err != nil {
			return nil, err
		}
		return nil, s.auditView(r, tx, p, "view.delete", id)
	})
}

func (s *Server) duplicateView(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*views.View, error) {
		v, err := views.Duplicate(r.Context(), tx, viewerOf(p), r.PathValue("id"), in.Name)
		if err != nil {
			return nil, err
		}
		return v, s.auditView(r, tx, p, "view.create", v.ID)
	})
}

func (s *Server) favoriteView(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Favorite bool `json:"favorite"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, views.SetFavorite(r.Context(), tx, viewerOf(p), r.PathValue("id"), in.Favorite)
	})
}

// defaultView: PUT /views/default {page, view_id (vuoto = nessuna), for: "me" | "tenant"}.
func (s *Server) defaultView(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Page   string `json:"page"`
		ViewID string `json:"view_id"`
		For    string `json:"for"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		if err := views.SetDefault(r.Context(), tx, viewerOf(p), in.Page, in.ViewID, in.For == "tenant"); err != nil {
			return nil, err
		}
		return nil, s.auditView(r, tx, p, "view.default", in.ViewID)
	})
}

func (s *Server) viewGroups(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := views.ShareGroups(r.Context(), tx, viewerOf(p))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) favoriteViews(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := views.Favorites(r.Context(), tx, s.eng, actorOf(r, p), viewerOf(p))
		return map[string]any{"items": items}, err
	})
}
