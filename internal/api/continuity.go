package api

import (
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/continuity"
	"regia/internal/platform/auth"
)

// Continuità operativa (docs/sviluppo/continuita.md): BIA, RTO/RPO richiesti contro dichiarati,
// incoerenze sul grafo, piani di continuità e test di resilienza. Modulo «continuity».
//
//	GET  /api/v1/continuity/settings              configurazione (scale, orizzonti, tipi di test)
//	PUT  /api/v1/continuity/settings              versione nuova della configurazione
//	GET  /api/v1/continuity/settings/versions     storico
//	POST /api/v1/continuity/setup                 prepara il modulo in un tenant esistente
//	GET  /api/v1/continuity/overview              incoerenze («cosa manca») e riepilogo
//	GET  /api/v1/continuity/bia                   capability con la loro BIA
//	POST /api/v1/continuity/bia                   avvia la BIA di una capability
//	GET  /api/v1/continuity/bia/{ref}             dettaglio della BIA
//	PUT  /api/v1/continuity/bia/{ref}             modifica in bozza
//	POST /api/v1/continuity/bia/{ref}/approve     approvazione (versione congelata)
//	GET  /api/v1/continuity/applications/{ref}    blocco dell'applicazione
//	GET  /api/v1/continuity/capabilities/{ref}    blocco della capability
//	GET  /api/v1/continuity/graph                 livello «Continuità» della mappa
//	GET/POST/DELETE /api/v1/continuity/plans      piani di continuità collegati
//	GET  /api/v1/continuity/tests?year=           piano annuale dei test
//	POST /api/v1/continuity/tests                 pianifica un test
//	GET  /api/v1/continuity/tests/{ref}           dettaglio del test
//	PUT  /api/v1/continuity/tests/{ref}           modifica, esito e rilievi

// continuityInit registra il modulo sul motore e collega le evidenze della conformità.
func (s *Server) continuityInit(o Options) {
	if o.Engine == nil {
		return
	}
	s.cont = continuity.Register(o.Engine)
	s.cont.UseGRC(s.grc)
}

func (s *Server) continuityRoutes(mux *http.ServeMux) {
	const base = "/api/v1/continuity"
	mux.HandleFunc("GET "+base+"/settings", inTenant(s.contSettings))
	mux.HandleFunc("PUT "+base+"/settings", inTenant(s.contSaveSettings))
	mux.HandleFunc("GET "+base+"/settings/versions", inTenant(s.contSettingsVersions))
	mux.HandleFunc("POST "+base+"/setup", inTenant(s.contSetup))
	mux.HandleFunc("GET "+base+"/overview", inTenant(s.contOverview))
	mux.HandleFunc("GET "+base+"/bia", inTenant(s.contBIAList))
	mux.HandleFunc("POST "+base+"/bia", inTenant(s.contCreateBIA))
	mux.HandleFunc("GET "+base+"/bia/{ref}", inTenant(s.contBIA))
	mux.HandleFunc("PUT "+base+"/bia/{ref}", inTenant(s.contUpdateBIA))
	mux.HandleFunc("POST "+base+"/bia/{ref}/approve", inTenant(s.contApproveBIA))
	mux.HandleFunc("GET "+base+"/applications/{ref}", inTenant(s.contApplication))
	mux.HandleFunc("GET "+base+"/capabilities/{ref}", inTenant(s.contCapability))
	mux.HandleFunc("GET "+base+"/graph", inTenant(s.contGraph))
	mux.HandleFunc("GET "+base+"/plans", inTenant(s.contPlans))
	mux.HandleFunc("POST "+base+"/plans", inTenant(s.contLinkPlan))
	mux.HandleFunc("DELETE "+base+"/plans", inTenant(s.contUnlinkPlan))
	mux.HandleFunc("GET "+base+"/tests", inTenant(s.contTests))
	mux.HandleFunc("POST "+base+"/tests", inTenant(s.contCreateTest))
	mux.HandleFunc("GET "+base+"/tests/{ref}", inTenant(s.contTest))
	mux.HandleFunc("PUT "+base+"/tests/{ref}", inTenant(s.contUpdateTest))
}

func (s *Server) contSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*continuity.SettingsView, error) {
		return s.cont.Settings(langCtx(r.Context(), tx, p), tx, actorOf(r, p))
	})
}

func (s *Server) contSaveSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in continuity.SettingsInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*continuity.SettingsView, error) {
		return s.cont.SaveSettings(langCtx(r.Context(), tx, p), tx, actorOf(r, p), in)
	})
}

func (s *Server) contSettingsVersions(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.cont.SettingsVersions(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) contSetup(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (map[string]bool, error) {
		return s.cont.Setup(langCtx(r.Context(), tx, p), tx, actorOf(r, p))
	})
}

func (s *Server) contOverview(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	f := continuity.OverviewFilter{Capability: q.Get("capability"), Team: q.Get("team"), Code: q.Get("code")}
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*continuity.Overview, error) {
		return s.cont.Overview(langCtx(r.Context(), tx, p), tx, actorOf(r, p), f)
	})
}

func (s *Server) contBIAList(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (map[string]any, error) {
		return s.cont.BIAList(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) contCreateBIA(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in continuity.BIAInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*continuity.BIAView, error) {
		return s.cont.CreateBIA(langCtx(r.Context(), tx, p), tx, actorOf(r, p), in)
	})
}

func (s *Server) contBIA(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*continuity.BIAView, error) {
		return s.cont.BIA(langCtx(r.Context(), tx, p), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) contUpdateBIA(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in continuity.BIAUpdate
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*continuity.BIAView, error) {
		return s.cont.UpdateBIA(langCtx(r.Context(), tx, p), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) contApproveBIA(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in continuity.ApproveInput
	if r.ContentLength != 0 && !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*continuity.BIAView, error) {
		return s.cont.ApproveBIA(langCtx(r.Context(), tx, p), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) contApplication(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*continuity.AppView, error) {
		return s.cont.Application(langCtx(r.Context(), tx, p), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) contCapability(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*continuity.CapView, error) {
		return s.cont.Capability(langCtx(r.Context(), tx, p), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) contGraph(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (map[string]any, error) {
		return s.cont.Graph(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) contPlans(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.cont.Plans(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) contLinkPlan(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in continuity.PlanInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*continuity.PlanRef, error) {
		return s.cont.LinkPlan(langCtx(r.Context(), tx, p), tx, actorOf(r, p), in)
	})
}

func (s *Server) contUnlinkPlan(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	in := continuity.PlanInput{Page: r.URL.Query().Get("page"), Target: r.URL.Query().Get("target")}
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.cont.UnlinkPlan(langCtx(r.Context(), tx, p), tx, actorOf(r, p), in)
	})
}

func (s *Server) contTests(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	year, _ := strconv.Atoi(r.URL.Query().Get("year"))
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*continuity.TestPlan, error) {
		return s.cont.TestsPlan(langCtx(r.Context(), tx, p), tx, actorOf(r, p), year)
	})
}

func (s *Server) contCreateTest(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in continuity.TestInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*continuity.TestView, error) {
		return s.cont.CreateTest(langCtx(r.Context(), tx, p), tx, actorOf(r, p), in)
	})
}

func (s *Server) contTest(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*continuity.TestView, error) {
		return s.cont.Test(langCtx(r.Context(), tx, p), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) contUpdateTest(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in continuity.TestUpdate
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*continuity.TestView, error) {
		return s.cont.UpdateTest(langCtx(r.Context(), tx, p), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}
