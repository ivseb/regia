package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/architecture"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// Architettura enterprise (piano, passo 20; replica/architettura-ea.md): capability,
// dipendenze tipizzate, grafo e analisi d'impatto, ciclo di vita e razionalizzazione,
// diagrammi, import ed export del catalogo esteso.

func (s *Server) eaRoutes(mux *http.ServeMux) {
	const base = "/api/v1/architecture"
	mux.HandleFunc("GET "+base+"/settings", inTenant(s.eaSettings))
	mux.HandleFunc("PUT "+base+"/settings", inTenant(s.eaUpdateSettings))
	mux.HandleFunc("GET "+base+"/capabilities", inTenant(s.eaCapabilities))
	mux.HandleFunc("POST "+base+"/capabilities", inTenant(s.eaCreateCapability))
	mux.HandleFunc("PATCH "+base+"/capabilities/{ref}", inTenant(s.eaUpdateCapability))
	mux.HandleFunc("POST "+base+"/capabilities/{ref}/links", inTenant(s.eaLinkCapability))
	mux.HandleFunc("DELETE "+base+"/capabilities/{ref}/links/{element}", inTenant(s.eaUnlinkCapability))
	mux.HandleFunc("GET "+base+"/relation-types", inTenant(s.eaRelationTypes))
	mux.HandleFunc("POST "+base+"/relation-types", inTenant(s.eaCreateRelationType))
	mux.HandleFunc("PATCH "+base+"/relation-types/{key}", inTenant(s.eaUpdateRelationType))
	mux.HandleFunc("GET "+base+"/relations", inTenant(s.eaRelations))
	mux.HandleFunc("POST "+base+"/relations", inTenant(s.eaCreateRelation))
	mux.HandleFunc("PATCH "+base+"/relations/{id}", inTenant(s.eaUpdateRelation))
	mux.HandleFunc("DELETE "+base+"/relations/{id}", inTenant(s.eaDeleteRelation))
	mux.HandleFunc("GET "+base+"/graph", inTenant(s.eaGraph))
	mux.HandleFunc("GET "+base+"/impact/{ref}", inTenant(s.eaImpact))
	mux.HandleFunc("GET "+base+"/profile/{ref}", inTenant(s.eaProfile))
	mux.HandleFunc("PUT "+base+"/applications/{ref}/time", inTenant(s.eaSetTime))
	mux.HandleFunc("PUT "+base+"/applications/{ref}/technologies", inTenant(s.eaSetTechnologies))
	mux.HandleFunc("GET "+base+"/technologies", inTenant(s.eaTechnologies))
	mux.HandleFunc("POST "+base+"/technologies", inTenant(s.eaSaveTechnology))
	mux.HandleFunc("PATCH "+base+"/technologies/{id}", inTenant(s.eaSaveTechnology))
	mux.HandleFunc("DELETE "+base+"/technologies/{id}", inTenant(s.eaDeleteTechnology))
	mux.HandleFunc("GET "+base+"/rationalization", inTenant(s.eaRationalization))
	mux.HandleFunc("GET "+base+"/diagrams", inTenant(s.eaDiagrams))
	mux.HandleFunc("POST "+base+"/diagrams", inTenant(s.eaCreateDiagram))
	mux.HandleFunc("POST "+base+"/diagrams/c4", inTenant(s.eaGenerateC4))
	mux.HandleFunc("GET "+base+"/diagrams/{id}", inTenant(s.eaDiagram))
	mux.HandleFunc("PUT "+base+"/diagrams/{id}", inTenant(s.eaUpdateDiagram))
	mux.HandleFunc("DELETE "+base+"/diagrams/{id}", inTenant(s.eaDeleteDiagram))
	mux.HandleFunc("GET "+base+"/catalog/export", inTenant(s.eaExport))
	mux.HandleFunc("POST "+base+"/catalog/import", inTenant(s.eaImport))
}

func (s *Server) eaSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*architecture.Settings, error) {
		return s.arch.Settings(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) eaUpdateSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in architecture.Settings
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*architecture.Settings, error) {
		return s.arch.UpdateSettings(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) eaCapabilities(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.arch.Capabilities(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) eaCreateCapability(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in architecture.CapabilityInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*architecture.Capability, error) {
		return s.arch.CreateCapability(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) eaUpdateCapability(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in architecture.CapabilityInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*architecture.Capability, error) {
		return s.arch.UpdateCapability(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) eaLinkCapability(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		ElementID string `json:"element_id"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.arch.LinkCapability(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.ElementID)
	})
}

func (s *Server) eaUnlinkCapability(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.arch.UnlinkCapability(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), r.PathValue("element"))
	})
}

func (s *Server) eaRelationTypes(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.arch.RelationTypes(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) eaCreateRelationType(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in architecture.RelationTypeInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*architecture.RelationType, error) {
		return s.arch.CreateRelationType(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) eaUpdateRelationType(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in architecture.RelationTypeInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*architecture.RelationType, error) {
		return s.arch.UpdateRelationType(r.Context(), tx, actorOf(r, p), r.PathValue("key"), in)
	})
}

func (s *Server) eaRelations(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.arch.Relations(r.Context(), tx, actorOf(r, p), q.Get("category"), q.Get("element"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) eaCreateRelation(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in architecture.RelationInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*architecture.Relation, error) {
		return s.arch.CreateRelation(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) eaUpdateRelation(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Attrs map[string]any `json:"attrs"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*architecture.Relation, error) {
		return s.arch.UpdateRelation(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.Attrs)
	})
}

func (s *Server) eaDeleteRelation(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.arch.DeleteRelation(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) eaGraph(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*architecture.Graph, error) {
		return s.arch.Graph(r.Context(), tx, actorOf(r, p))
	})
}

// eaImpact: ?direction=dependents|dependencies&depth=N&format=csv
func (s *Server) eaImpact(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	depth, _ := strconv.Atoi(q.Get("depth"))
	var res *architecture.ImpactResult
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		res, err = s.arch.Impact(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), q.Get("direction"), depth)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	if q.Get("format") == "csv" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="impatto-`+res.Root.Key+`-`+time.Now().Format(time.DateOnly)+`.csv"`)
		_, _ = w.Write(architecture.ImpactCSV(res))
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) eaProfile(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*architecture.Profile, error) {
		return s.arch.Profile(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) eaSetTime(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in architecture.TimeInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.arch.SetTime(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) eaSetTechnologies(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		IDs []string `json:"technology_ids"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.IDs == nil {
		in.IDs = []string{}
	}
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.arch.SetTechnologies(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.IDs)
	})
}

func (s *Server) eaTechnologies(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.arch.Technologies(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) eaSaveTechnology(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in architecture.TechnologyInput
	if !decodeJSON(w, r, &in) {
		return
	}
	status := http.StatusOK
	if r.Method == http.MethodPost {
		status = http.StatusCreated
	}
	tenantJSON(s, w, r, p, false, status, func(tx pgx.Tx) (*architecture.Technology, error) {
		return s.arch.SaveTechnology(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) eaDeleteTechnology(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.arch.DeleteTechnology(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) eaRationalization(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*architecture.Rationalization, error) {
		return s.arch.Rationalization(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) eaDiagrams(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.arch.Diagrams(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) eaDiagram(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*architecture.DiagramView, error) {
		return s.arch.DiagramView(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) eaCreateDiagram(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in architecture.DiagramInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*architecture.DiagramView, error) {
		return s.arch.CreateDiagram(r.Context(), tx, actorOf(r, p), in)
	})
}

// eaGenerateC4 genera (o riapre, se esiste già) il diagramma C4 di un'applicazione o di un asset.
func (s *Server) eaGenerateC4(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in architecture.C4Input
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*architecture.DiagramView, error) {
		v, _, err := s.arch.GenerateC4(r.Context(), tx, actorOf(r, p), in)
		return v, err
	})
}

func (s *Server) eaUpdateDiagram(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in architecture.DiagramInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*architecture.DiagramView, error) {
		return s.arch.UpdateDiagram(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) eaDeleteDiagram(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.arch.DeleteDiagram(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

// eaExport: ?format=json (catalogo completo) oppure ?format=csv&what=capabilities|dependencies
func (s *Server) eaExport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	var body []byte
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		if q.Get("format") == "csv" {
			var err error
			body, err = s.arch.ExportCSV(r.Context(), tx, actorOf(r, p), q.Get("what"))
			return err
		}
		b, err := s.arch.ExportCatalog(r.Context(), tx, actorOf(r, p))
		if err != nil {
			return err
		}
		body, err = json.MarshalIndent(b, "", "  ")
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	day := time.Now().Format(time.DateOnly)
	if q.Get("format") == "csv" {
		name := "capability"
		if q.Get("what") == "dependencies" {
			name = "dipendenze"
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`-`+day+`.csv"`)
	} else {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition", `attachment; filename="architettura-`+day+`.json"`)
	}
	_, _ = w.Write(body)
}

func (s *Server) eaImport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in architecture.EAImportInput
	if !decodeJSON(w, r, &in) {
		return
	}
	dry := r.URL.Query().Get("dry_run") == "true"
	var rep *architecture.EAImportReport
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		rep, err = s.arch.ImportCatalog(r.Context(), tx, actorOf(r, p), in, dry)
		return err
	})
	if err != nil && !(errors.Is(err, architecture.ErrDryRun) && rep != nil) {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}
