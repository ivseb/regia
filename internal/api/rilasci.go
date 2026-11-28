package api

import (
	"net/http"

	"github.com/jackc/pgx/v5"

	"regia/internal/ai"
	"regia/internal/engine"
	"regia/internal/modules/devsecops"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// Rilasci (docs/sviluppo/commit-elementi.md): gli elementi di un rilascio, lo stato di
// produzione di un elemento e dei suoi discendenti, le impostazioni dei rilasci (chiusura
// con le parole chiave, sezioni della certificazione) e la certificazione del rilascio.

func (s *Server) releaseRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/devsecops/releases/{id}", inTenant(s.dsoRelease))
	mux.HandleFunc("GET /api/v1/elements/{ref}/production", inTenant(s.dsoElementProduction))
	mux.HandleFunc("GET /api/v1/devsecops/release-settings", inTenant(s.dsoReleaseSettings))
	mux.HandleFunc("PUT /api/v1/devsecops/release-settings", inTenant(s.dsoSaveReleaseSettings))
	mux.HandleFunc("GET /api/v1/devsecops/changes/{number}/certification", inTenant(s.dsoCertification))
	mux.HandleFunc("POST /api/v1/devsecops/changes/{number}/certification", inTenant(s.dsoGenerateCertification))
	mux.HandleFunc("POST /api/v1/ai/release-notes", inTenant(s.aiReleaseNotes))
}

func (s *Server) dsoRelease(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		if s.dso == nil {
			return nil, engine.ErrNotFound
		}
		return s.dso.ReleaseDetailOf(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

// dsoElementProduction: vale la visibilità dell'elemento (404 per chi non lo vede).
func (s *Server) dsoElementProduction(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		if s.dso == nil {
			return &devsecops.Production{Items: []devsecops.ProductionItem{}}, nil
		}
		el, err := s.eng.Get(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
		if err != nil {
			return nil, err
		}
		return s.dso.ProductionOf(r.Context(), tx, actorOf(r, p), el.ID)
	})
}

func (s *Server) dsoReleaseSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.ReleaseSettingsOf(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) dsoSaveReleaseSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in devsecops.ReleaseSettingsInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.SaveReleaseSettings(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) dsoCertification(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.CertificationOf(r.Context(), tx, actorOf(r, p), r.PathValue("number"), r.URL.Query().Get("version"))
	})
}

func (s *Server) dsoGenerateCertification(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (any, error) {
		return s.dso.GenerateCertification(r.Context(), tx, actorOf(r, p), r.PathValue("number"))
	})
}

// aiReleaseNotes: «Riscrivi per il business» (funzione Bozze). Gli elementi si leggono qui,
// con la visibilità di chi chiede; il client manda solo il rilascio e le indicazioni.
func (s *Server) aiReleaseNotes(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Release string `json:"release"`
		Hint    string `json:"hint"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	var notes ai.ReleaseNotesInput
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		if s.dso == nil {
			return engine.ErrNotFound
		}
		d, err := s.dso.ReleaseDetailOf(r.Context(), tx, actorOf(r, p), in.Release)
		if err != nil {
			return err
		}
		notes.Release = d.Release.Service + " " + d.Release.Version + " (" + d.Release.Environment + ")"
		for _, it := range d.Items {
			notes.Items = append(notes.Items, ai.ReleaseNoteItem{Key: it.Key, Title: it.Title, Type: it.TypeName, Group: it.Group})
			notes.ElementIDs = append(notes.ElementIDs, it.ID)
		}
		return nil
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	notes.Hint = in.Hint
	res, err := s.ai.ReleaseNotes(r.Context(), p.TenantID, actorOf(r, p), s.memberLang(r, p), notes)
	if err != nil {
		s.aiError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}
