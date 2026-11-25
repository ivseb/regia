package api

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/architecture"
	"regia/internal/modules/work"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// Catalogo di architettura (asset logici, applicazioni, team) e analisi tecnica delle
// richieste (asset citati, impatti per applicazione): sdlc.md §3-4.

// tenantJSON esegue fn nel tenant e risponde con il risultato in JSON.
func tenantJSON[T any](s *Server, w http.ResponseWriter, r *http.Request, p *auth.Principal, readOnly bool, status int, fn func(tx pgx.Tx) (T, error)) {
	var out T
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: readOnly}, func(tx pgx.Tx) error {
		var err error
		out, err = fn(tx)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	if status == http.StatusNoContent {
		w.WriteHeader(status)
		return
	}
	writeJSON(w, status, out)
}

func (s *Server) listTeams(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		q, limit, ids := searchParams(r)
		items, err := s.arch.TeamsSearch(r.Context(), tx, likeEscape(q), limit, ids)
		return map[string]any{"items": items}, err
	})
}

func (s *Server) createTeam(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in architecture.TeamInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (any, error) {
		id, err := s.arch.CreateTeam(r.Context(), tx, actorOf(r, p), in)
		if err != nil {
			return nil, err
		}
		// Work management: il team nuovo riceve subito il suo spazio di delivery (chiave proposta).
		spaceID, err := work.EnsureTeamSpace(r.Context(), tx, actorOf(r, p), work.SpaceInput{TeamID: id})
		return map[string]string{"id": id, "delivery_space_id": spaceID}, err
	})
}

func (s *Server) updateTeam(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in architecture.TeamInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.arch.UpdateTeam(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) listAssets(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.arch.Assets(r.Context(), tx)
		return map[string]any{"items": items}, err
	})
}

func (s *Server) createAsset(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in architecture.AssetInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (any, error) {
		id, err := s.arch.CreateAsset(r.Context(), tx, actorOf(r, p), in)
		return map[string]string{"id": id}, err
	})
}

func (s *Server) addComponent(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		ApplicationID string `json:"application_id"`
		Relation      string `json:"relation"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.arch.AddComponent(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.ApplicationID, in.Relation)
	})
}

func (s *Server) removeComponent(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.arch.RemoveComponent(r.Context(), tx, actorOf(r, p), r.PathValue("id"), r.PathValue("app"))
	})
}

func (s *Server) listApplications(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		q, limit, ids := searchParams(r)
		if limit == 0 {
			limit = 500
		}
		items, err := s.arch.ApplicationsSearch(r.Context(), tx, q, limit, ids)
		return map[string]any{"items": items}, err
	})
}

func (s *Server) createApplication(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in architecture.ApplicationInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*architecture.Application, error) {
		return s.arch.CreateApplication(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) updateApplication(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in architecture.ApplicationInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*architecture.Application, error) {
		return s.arch.UpdateApplication(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) importApplications(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		CSV string `json:"csv"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	dry := r.URL.Query().Get("dry_run") == "true"
	var rep *architecture.ImportReport
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		rep, err = s.arch.ImportApplications(r.Context(), tx, actorOf(r, p), in.CSV, dry)
		return err
	})
	if err != nil && !(errors.Is(err, architecture.ErrDryRun) && rep != nil) {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

func (s *Server) analysis(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*architecture.Analysis, error) {
		return s.arch.Analysis(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) citeAsset(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		AssetID string `json:"asset_id"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.arch.CiteAsset(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.AssetID)
	})
}

func (s *Server) unciteAsset(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.arch.UnciteAsset(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), r.PathValue("asset"))
	})
}

func (s *Server) createImpact(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in architecture.ImpactInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*architecture.Impact, error) {
		return s.arch.CreateImpact(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) updateImpact(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in architecture.ImpactInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*architecture.Impact, error) {
		return s.arch.UpdateImpact(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}
