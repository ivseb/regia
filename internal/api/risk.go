package api

import (
	"bytes"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/modules/risk"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// Rischi ICT, fornitori e registro delle informazioni, incidenti con classificazione e
// notifiche normative, cruscotto e configurazione (piano, passo 20).

func (s *Server) riskRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/risk/dashboard", inTenant(s.riskDashboard))
	mux.HandleFunc("GET /api/v1/risks", inTenant(s.listRisks))
	mux.HandleFunc("GET /api/v1/risks.csv", inTenant(s.risksCSV))
	mux.HandleFunc("POST /api/v1/risks", inTenant(s.createRisk))
	mux.HandleFunc("GET /api/v1/risks/{ref}", inTenant(s.getRisk))
	mux.HandleFunc("PATCH /api/v1/risks/{ref}", inTenant(s.updateRisk))
	mux.HandleFunc("POST /api/v1/risks/{ref}/links", inTenant(s.linkRisk))
	mux.HandleFunc("DELETE /api/v1/risks/{ref}/links/{target}", inTenant(s.unlinkRisk))
	mux.HandleFunc("POST /api/v1/risks/{ref}/acceptance", inTenant(s.requestAcceptance))

	mux.HandleFunc("GET /api/v1/ict/providers", inTenant(s.listProviders))
	mux.HandleFunc("POST /api/v1/ict/providers", inTenant(s.createProvider))
	mux.HandleFunc("GET /api/v1/ict/providers/{ref}", inTenant(s.getProvider))
	mux.HandleFunc("PATCH /api/v1/ict/providers/{ref}", inTenant(s.updateProvider))
	mux.HandleFunc("POST /api/v1/ict/providers/{ref}/applications", inTenant(s.linkProviderApp))
	mux.HandleFunc("DELETE /api/v1/ict/providers/{ref}/applications/{app}", inTenant(s.unlinkProviderApp))
	mux.HandleFunc("POST /api/v1/ict/providers/{ref}/contracts", inTenant(s.createContract))
	mux.HandleFunc("GET /api/v1/ict/contracts", inTenant(s.listContracts))
	mux.HandleFunc("PATCH /api/v1/ict/contracts/{id}", inTenant(s.updateContract))
	mux.HandleFunc("DELETE /api/v1/ict/contracts/{id}", inTenant(s.deleteContract))
	mux.HandleFunc("GET /api/v1/ict/register.csv", inTenant(s.registerCSV))

	mux.HandleFunc("GET /api/v1/incidents", inTenant(s.listIncidents))
	mux.HandleFunc("POST /api/v1/incidents", inTenant(s.createIncident))
	mux.HandleFunc("GET /api/v1/incidents/{ref}", inTenant(s.getIncident))
	mux.HandleFunc("GET /api/v1/incidents/{ref}/crisis", inTenant(s.incidentCrisis))
	mux.HandleFunc("PATCH /api/v1/incidents/{ref}", inTenant(s.updateIncident))
	mux.HandleFunc("POST /api/v1/incidents/{ref}/links", inTenant(s.linkIncident))
	mux.HandleFunc("DELETE /api/v1/incidents/{ref}/links/{target}", inTenant(s.unlinkIncident))
	mux.HandleFunc("POST /api/v1/incidents/{ref}/events", inTenant(s.addIncidentEvent))
	mux.HandleFunc("POST /api/v1/incidents/{ref}/classify", inTenant(s.classifyIncident))
	mux.HandleFunc("PUT /api/v1/incidents/{ref}/postmortem", inTenant(s.savePostmortem))
	mux.HandleFunc("POST /api/v1/incidents/{ref}/actions", inTenant(s.addCorrectiveAction))
	mux.HandleFunc("GET /api/v1/regulatory-notifications", inTenant(s.openNotifications))
	mux.HandleFunc("PUT /api/v1/regulatory-notifications/{id}/draft", inTenant(s.notificationDraft))
	mux.HandleFunc("POST /api/v1/regulatory-notifications/{id}/submit", inTenant(s.notificationSubmit))
	mux.HandleFunc("POST /api/v1/regulatory-notifications/{id}/not-required", inTenant(s.notificationNotRequired))

	mux.HandleFunc("GET /api/v1/risk-config", inTenant(s.riskConfig))
	mux.HandleFunc("PUT /api/v1/risk-config/matrices/{key}", inTenant(s.saveMatrix))
	mux.HandleFunc("PUT /api/v1/risk-config/settings", inTenant(s.saveRiskSettings))
	mux.HandleFunc("PUT /api/v1/risk-config/acceptance-policy", inTenant(s.saveAcceptancePolicy))
	mux.HandleFunc("PUT /api/v1/risk-config/schemes/{key}", inTenant(s.saveScheme))
	mux.HandleFunc("POST /api/v1/risk-config/schemes/preview", inTenant(s.previewScheme))
	mux.HandleFunc("GET /api/v1/risk-config/export", inTenant(s.exportRiskConfig))
	mux.HandleFunc("POST /api/v1/risk-config/import", inTenant(s.importRiskConfig))
}

// ---------------------------------------------------------------------------
// Cruscotto e rischi

func (s *Server) riskDashboard(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*risk.Dashboard, error) {
		return s.risk.DashboardView(r.Context(), tx, actorOf(r, p))
	})
}

func riskFilter(r *http.Request) risk.RiskFilter {
	q := r.URL.Query()
	return risk.RiskFilter{Q: q.Get("q"), Phase: q.Get("fase"), Band: q.Get("livello"), Treatment: q.Get("trattamento"),
		Category: q.Get("categoria"), Over: q.Get("oltre_soglia") == "true", Owner: q.Get("titolare"), Target: q.Get("collegato")}
}

func (s *Server) listRisks(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*risk.RiskRegister, error) {
		return s.risk.Risks(r.Context(), tx, actorOf(r, p), riskFilter(r))
	})
}

func (s *Server) csvDownload(w http.ResponseWriter, r *http.Request, p *auth.Principal, name string, fn func(tx pgx.Tx, buf io.Writer) error) {
	var buf bytes.Buffer
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error { return fn(tx, &buf) })
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`-`+time.Now().Format(time.DateOnly)+`.csv"`)
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) risksCSV(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.csvDownload(w, r, p, "registro-rischi", func(tx pgx.Tx, buf io.Writer) error {
		reg, err := s.risk.Risks(r.Context(), tx, actorOf(r, p), riskFilter(r))
		if err != nil {
			return err
		}
		return reg.WriteCSV(buf)
	})
}

func (s *Server) createRisk(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in risk.RiskInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*risk.RiskView, error) {
		return s.risk.CreateRisk(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) getRisk(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*risk.RiskView, error) {
		return s.risk.RiskView(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) updateRisk(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in risk.RiskInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*risk.RiskView, error) {
		return s.risk.UpdateRisk(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

type targetBody struct {
	Target string `json:"target"`
}

func (s *Server) linkRisk(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in targetBody
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*risk.RiskView, error) {
		return s.risk.LinkRiskRef(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.Target)
	})
}

func (s *Server) unlinkRisk(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*risk.RiskView, error) {
		a := actorOf(r, p)
		if err := s.risk.UnlinkRisk(r.Context(), tx, a, r.PathValue("ref"), r.PathValue("target")); err != nil {
			return nil, err
		}
		return s.risk.RiskView(r.Context(), tx, a, r.PathValue("ref"))
	})
}

func (s *Server) requestAcceptance(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Until   string `json:"until"`
		Comment string `json:"comment"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*risk.RiskView, error) {
		return s.risk.RequestAcceptance(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.Until, in.Comment)
	})
}

// ---------------------------------------------------------------------------
// Fornitori e contratti

func (s *Server) listProviders(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		q, limit, ids := searchParams(r)
		items, err := s.risk.Providers(r.Context(), tx, actorOf(r, p), q, r.URL.Query().Get("critici") == "true")
		if err != nil {
			return nil, err
		}
		// ids restringe a quelle voci (per leggere i nomi già scelti in un selettore); limit taglia l'elenco.
		if len(ids) > 0 {
			keep := items[:0:0]
			for _, it := range items {
				if slices.Contains(ids, it.ID) {
					keep = append(keep, it)
				}
			}
			items = keep
		}
		if limit > 0 && len(items) > limit {
			items = items[:limit]
		}
		return map[string]any{"items": items}, nil
	})
}

func (s *Server) createProvider(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in risk.ProviderInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*risk.ProviderView, error) {
		return s.risk.CreateProvider(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) getProvider(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*risk.ProviderView, error) {
		return s.risk.ProviderView(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) updateProvider(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in risk.ProviderInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*risk.ProviderView, error) {
		return s.risk.UpdateProvider(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) linkProviderApp(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Application string `json:"application"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*risk.ProviderView, error) {
		return s.risk.LinkApplication(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.Application)
	})
}

func (s *Server) unlinkProviderApp(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*risk.ProviderView, error) {
		a := actorOf(r, p)
		if err := s.risk.UnlinkApplication(r.Context(), tx, a, r.PathValue("ref"), r.PathValue("app")); err != nil {
			return nil, err
		}
		return s.risk.ProviderView(r.Context(), tx, a, r.PathValue("ref"))
	})
}

func (s *Server) createContract(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in risk.ContractInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*risk.Contract, error) {
		return s.risk.SaveContract(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), "", in)
	})
}

func (s *Server) listContracts(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.risk.Contracts(r.Context(), tx, actorOf(r, p), "", r.URL.Query().Get("in_scadenza") == "true")
		return map[string]any{"items": items}, err
	})
}

func (s *Server) updateContract(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in risk.ContractInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*risk.Contract, error) {
		return s.risk.SaveContract(r.Context(), tx, actorOf(r, p), "", r.PathValue("id"), in)
	})
}

func (s *Server) deleteContract(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.risk.DeleteContract(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) registerCSV(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var cols []string
	if v := strings.TrimSpace(r.URL.Query().Get("columns")); v != "" {
		cols = strings.Split(v, ",")
	}
	s.csvDownload(w, r, p, "registro-informazioni", func(tx pgx.Tx, buf io.Writer) error {
		return s.risk.RegisterExport(r.Context(), tx, actorOf(r, p), buf, cols, r.URL.Query().Get("grain"))
	})
}

// ---------------------------------------------------------------------------
// Incidenti e notifiche normative

func (s *Server) listIncidents(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.risk.Incidents(r.Context(), tx, actorOf(r, p), q.Get("q"), q.Get("aperti") == "true", q.Get("gravi") == "true")
		return map[string]any{"items": items}, err
	})
}

func (s *Server) createIncident(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in risk.IncidentInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*risk.IncidentView, error) {
		return s.risk.CreateIncident(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) getIncident(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*risk.IncidentView, error) {
		return s.risk.IncidentView(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) incidentCrisis(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*risk.CrisisView, error) {
		return s.risk.Crisis(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) updateIncident(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in risk.IncidentInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*risk.IncidentView, error) {
		return s.risk.UpdateIncident(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) linkIncident(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in targetBody
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*risk.IncidentView, error) {
		return s.risk.LinkIncident(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.Target, false)
	})
}

func (s *Server) unlinkIncident(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*risk.IncidentView, error) {
		return s.risk.LinkIncident(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), r.PathValue("target"), true)
	})
}

func (s *Server) addIncidentEvent(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		At   string `json:"at"`
		Kind string `json:"kind"`
		Text string `json:"text"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*risk.IncidentView, error) {
		return s.risk.AddEvent(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.At, in.Kind, in.Text)
	})
}

func (s *Server) classifyIncident(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	dry := r.URL.Query().Get("dry_run") == "true"
	tenantJSON(s, w, r, p, dry, http.StatusOK, func(tx pgx.Tx) (*risk.Classification, error) {
		return s.risk.Classify(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), dry)
	})
}

func (s *Server) savePostmortem(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in risk.Postmortem
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*risk.IncidentView, error) {
		return s.risk.SavePostmortem(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) addCorrectiveAction(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in risk.ActionInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*risk.IncidentView, error) {
		return s.risk.AddAction(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) openNotifications(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		a := actorOf(r, p)
		if !a.Has("element.read") {
			return nil, engine.ErrForbidden
		}
		items, err := s.risk.OpenNotifications(r.Context(), tx)
		return map[string]any{"items": items}, err
	})
}

func (s *Server) notificationDraft(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in risk.NotificationInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*risk.Notification, error) {
		return s.risk.SaveDraft(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) notificationSubmit(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in risk.NotificationInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*risk.Notification, error) {
		return s.risk.Submit(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) notificationNotRequired(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in risk.NotificationInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*risk.Notification, error) {
		return s.risk.NotRequired(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

// ---------------------------------------------------------------------------
// Configurazione

func (s *Server) riskConfig(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*risk.Config, error) {
		return s.risk.GetConfig(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) saveMatrix(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in risk.Matrix
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Key = r.PathValue("key")
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*risk.Matrix, error) {
		return s.risk.SaveMatrix(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) saveRiskSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in risk.Settings
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*risk.Settings, error) {
		return s.risk.UpdateSettings(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) saveAcceptancePolicy(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Rules []approval.Rule `json:"rules"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*risk.Policy, error) {
		return s.risk.UpdatePolicy(r.Context(), tx, actorOf(r, p), in.Rules)
	})
}

func (s *Server) saveScheme(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in risk.Scheme
	if !decodeJSON(w, r, &in) {
		return
	}
	in.Key = r.PathValue("key")
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*risk.Scheme, error) {
		return s.risk.SaveScheme(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) previewScheme(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Scheme   risk.Scheme `json:"scheme"`
		Incident string      `json:"incident"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		res, err := s.risk.PreviewScheme(r.Context(), tx, actorOf(r, p), in.Scheme, in.Incident)
		return map[string]any{"valid": err == nil, "result": res}, err
	})
}

func (s *Server) exportRiskConfig(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*risk.Package, error) {
		return s.risk.Export(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) importRiskConfig(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		writeProblem(w, r, http.StatusBadRequest, "request.invalid_body", "corpo non leggibile")
		return
	}
	pkg, err := risk.ParsePackage(raw)
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*risk.ImportResult, error) {
		return s.risk.Import(r.Context(), tx, actorOf(r, p), pkg)
	})
}
