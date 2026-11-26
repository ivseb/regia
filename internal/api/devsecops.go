package api

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/devsecops"
	"regia/internal/modules/devsecops/contract"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// DevSecOps (piano, passo 21; replica/devsecops.md): API di push per le pipeline, con un
// token API e il permesso devsecops.write, e le letture del pannello, della panoramica e della
// configurazione. Esempi curl in docs/devsecops-api.md.

// maxPushBody è il limite di un invio: le SBOM delle immagini arrivano a qualche MB (anche
// compresse con Content-Encoding: gzip; il limite vale anche sul contenuto decompresso).
const maxPushBody = 32 << 20

func (s *Server) devsecopsRoutes(mux *http.ServeMux) {
	push := func(h func(http.ResponseWriter, *http.Request, *auth.Principal)) http.HandlerFunc {
		return can(rbac.DevsecopsWrite, h)
	}
	mux.HandleFunc("POST /api/v1/devsecops/services", inTenant(s.dsoPushService))
	mux.HandleFunc("POST /api/v1/devsecops/check-runs", push(s.dsoPushCheckRun))
	mux.HandleFunc("POST /api/v1/devsecops/releases", push(s.dsoPushRelease))
	mux.HandleFunc("POST /api/v1/devsecops/deployments", push(s.dsoPushDeployment))
	mux.HandleFunc("POST /api/v1/devsecops/sboms", push(s.dsoPushSBOM))
	mux.HandleFunc("POST /api/v1/devsecops/vuln-reports", push(s.dsoPushVulnReport))
	// Letture e configurazione (interfaccia).
	mux.HandleFunc("GET /api/v1/devsecops/overview", inTenant(s.dsoOverview))
	mux.HandleFunc("GET /api/v1/devsecops/elements/{ref}/panel", inTenant(s.dsoPanel))
	mux.HandleFunc("GET /api/v1/elements/{ref}/commits", inTenant(s.dsoElementCommits))
	mux.HandleFunc("GET /api/v1/devsecops/services/{id}", inTenant(s.dsoService))
	mux.HandleFunc("PATCH /api/v1/devsecops/services/{id}", inTenant(s.dsoUpdateService))
	mux.HandleFunc("GET /api/v1/devsecops/config", inTenant(s.dsoConfig))
	mux.HandleFunc("PUT /api/v1/devsecops/config/settings", inTenant(s.dsoSaveSettings))
	mux.HandleFunc("PATCH /api/v1/devsecops/environments/{id}", inTenant(s.dsoUpdateEnvironment))
	mux.HandleFunc("PATCH /api/v1/devsecops/changes/{id}", inTenant(s.dsoUpdateChange))
}

// decodePush legge un invio della pipeline: JSON (anche gzip), fino a maxPushBody, con il
// campo "format" del contratto. I campi sconosciuti si ignorano: il contratto cresce per
// aggiunte e una pipeline più nuova non deve fallire su una versione di Regia più vecchia.
func decodePush(w http.ResponseWriter, r *http.Request, dst any, format *string) bool {
	if ct := r.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		writeProblem(w, r, http.StatusUnsupportedMediaType, "request.content_type", "serve Content-Type: application/json")
		return false
	}
	var body io.Reader = http.MaxBytesReader(w, r.Body, maxPushBody)
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Content-Encoding")), "gzip") {
		gz, err := gzip.NewReader(body)
		if err != nil {
			writeProblem(w, r, http.StatusBadRequest, "request.invalid_gzip", "corpo gzip non valido")
			return false
		}
		defer gz.Close()
		body = io.LimitReader(gz, 2*maxPushBody+1)
	}
	raw, err := io.ReadAll(body)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeProblem(w, r, http.StatusRequestEntityTooLarge, "request.too_large", fmt.Sprintf("invio troppo grande (massimo %d MB)", maxPushBody>>20))
		} else {
			writeProblem(w, r, http.StatusBadRequest, "request.invalid_body", "corpo della richiesta illeggibile")
		}
		return false
	}
	if len(raw) > 2*maxPushBody {
		writeProblem(w, r, http.StatusRequestEntityTooLarge, "request.too_large", fmt.Sprintf("invio troppo grande (massimo %d MB decompressi)", (2*maxPushBody)>>20))
		return false
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		writeProblem(w, r, http.StatusBadRequest, "request.invalid_json", fmt.Sprintf("JSON non valido: %v", err))
		return false
	}
	if *format != contract.Version {
		writeProblem(w, r, http.StatusUnprocessableEntity, "devsecops.format", fmt.Sprintf("serve \"format\": %q", contract.Version))
		return false
	}
	return true
}

// pushJSON esegue l'invio nella transazione del tenant: 202 se ha cambiato qualcosa, 200 se
// era già stato ricevuto identico.
func (s *Server) pushJSON(w http.ResponseWriter, r *http.Request, p *auth.Principal, fn func(tx pgx.Tx) (*devsecops.Receipt, error)) {
	var rc *devsecops.Receipt
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		rc, err = fn(tx)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	status := http.StatusAccepted
	if !rc.Changed {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"format": contract.Version, "result": rc})
}

func (s *Server) dsoPushService(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Format string `json:"format"`
		contract.Service
	}
	if !decodePush(w, r, &in, &in.Format) {
		return
	}
	a := actorOf(r, p)
	switch {
	case p.TokenID == "" && devsecops.CanConfigure(a):
		// Dall'interfaccia: servizio censito, non ancora sulla pipeline; associazione manuale.
		tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (any, error) {
			return s.dso.CreatePlannedService(r.Context(), tx, a, in.Service)
		})
	case p.Can(rbac.DevsecopsWrite):
		tenantJSON(s, w, r, p, false, http.StatusAccepted, func(tx pgx.Tx) (any, error) {
			v, err := s.dso.RegisterService(r.Context(), tx, a, in.Service, "pipeline")
			return map[string]any{"format": contract.Version, "result": v}, err
		})
	default:
		writeProblemParams(w, r, http.StatusForbidden, "auth.forbidden", "permesso mancante: "+rbac.DevsecopsWrite, map[string]any{"perm": rbac.DevsecopsWrite})
	}
}

func (s *Server) dsoPushCheckRun(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Format string `json:"format"`
		contract.CheckRun
	}
	if !decodePush(w, r, &in, &in.Format) {
		return
	}
	s.pushJSON(w, r, p, func(tx pgx.Tx) (*devsecops.Receipt, error) {
		return s.dso.PushCheckRun(r.Context(), tx, actorOf(r, p), in.CheckRun)
	})
}

func (s *Server) dsoPushRelease(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Format string `json:"format"`
		contract.Release
	}
	if !decodePush(w, r, &in, &in.Format) {
		return
	}
	s.pushJSON(w, r, p, func(tx pgx.Tx) (*devsecops.Receipt, error) {
		return s.dso.PushRelease(r.Context(), tx, actorOf(r, p), in.Release)
	})
}

func (s *Server) dsoPushDeployment(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Format string `json:"format"`
		contract.Deployment
	}
	if !decodePush(w, r, &in, &in.Format) {
		return
	}
	s.pushJSON(w, r, p, func(tx pgx.Tx) (*devsecops.Receipt, error) {
		return s.dso.PushDeployment(r.Context(), tx, actorOf(r, p), in.Deployment)
	})
}

// dsoPushSBOM: "format" è quello del contratto; il formato della SBOM (cyclonedx, spdx) si
// riconosce dal documento o si indica con "sbom_format".
func (s *Server) dsoPushSBOM(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Format     string           `json:"format"`
		SBOMFormat string           `json:"sbom_format"`
		Service    contract.Service `json:"service"`
		Version    string           `json:"version"`
		Digest     string           `json:"digest"`
		Document   json.RawMessage  `json:"document"`
	}
	if !decodePush(w, r, &in, &in.Format) {
		return
	}
	s.pushJSON(w, r, p, func(tx pgx.Tx) (*devsecops.Receipt, error) {
		return s.dso.PushSBOM(r.Context(), tx, actorOf(r, p), contract.SBOM{Service: in.Service, Version: in.Version, Digest: in.Digest,
			Format: in.SBOMFormat, Document: in.Document})
	})
}

// dsoPushVulnReport: le vulnerabilità del contratto, oppure "report" con l'output JSON di
// grype o di trivy così com'è.
func (s *Server) dsoPushVulnReport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Format string          `json:"format"`
		Report json.RawMessage `json:"report"`
		contract.VulnReport
	}
	if !decodePush(w, r, &in, &in.Format) {
		return
	}
	if len(in.Report) > 0 && string(in.Report) != "null" {
		vs, scanner, err := devsecops.ParseScannerReport(strings.ToLower(in.Scanner), in.Report)
		if err != nil {
			writeProblem(w, r, http.StatusUnprocessableEntity, "devsecops.report", err.Error())
			return
		}
		in.Vulnerabilities = append(in.Vulnerabilities, vs...)
		if in.Scanner == "" {
			in.Scanner = scanner
		}
	}
	s.pushJSON(w, r, p, func(tx pgx.Tx) (*devsecops.Receipt, error) {
		return s.dso.PushVulnReport(r.Context(), tx, actorOf(r, p), in.VulnReport)
	})
}

func (s *Server) dsoOverview(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.Overview(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) dsoPanel(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.Panel(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

// dsoElementCommits: i commit che citano l'elemento (chiave nel messaggio o nella PR) e il rilascio che
// li contiene. Vale la visibilità dell'elemento: chi non lo vede, per lui non esiste (404).
func (s *Server) dsoElementCommits(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		if s.dso == nil {
			return &devsecops.ElementCommits{Commits: []devsecops.ElementCommit{}}, nil
		}
		el, err := s.eng.Get(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
		if err != nil {
			return nil, err
		}
		return s.dso.ElementCommitsOf(r.Context(), tx, el.ID)
	})
}

func (s *Server) dsoService(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.ServiceHistory(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) dsoUpdateService(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in devsecops.ServiceInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.UpdateService(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) dsoConfig(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.Config(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) dsoSaveSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in devsecops.Settings
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.SaveSettings(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) dsoUpdateEnvironment(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in devsecops.EnvInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.dso.UpdateEnvironment(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) dsoUpdateChange(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in devsecops.ChangeInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.UpdateChange(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}
