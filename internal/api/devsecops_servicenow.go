package api

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	ghconn "regia/internal/modules/devsecops/connectors/github"
	snconn "regia/internal/modules/devsecops/connectors/servicenow"
	"regia/internal/modules/risk"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
	"regia/internal/platform/secretbox"
)

// Configurazione → ServiceNow (DevSecOps, connettore in sola lettura). Le regole stanno in
// internal/modules/devsecops/connectors/servicenow; il job periodico parte da serve.go.
//
//   - Configurazione, servizi CMDB, sincronizzazioni: come gli altri connettori, lettura con
//     tenant.admin (anche con un token API), modifiche solo con una sessione.
//   - Incidenti e change letti: chi legge le applicazioni (element.read).
//   - Proposte di incidente di Regia e decisione sulle metriche DORA: chi può segnalare un
//     incidente (element.write o incident.manage). La creazione dell'incidente di Regia
//     passa da risk.CreateIncident, con i suoi controlli, nella stessa transazione del
//     collegamento.
//   - Webhook di ServiceNow: niente sessione, vale la firma X-Regia-Signature.

func (s *Server) servicenowInit(o Options) {
	s.sn = o.ServiceNow
	if s.sn == nil && o.DB != nil {
		box := o.SecretBox
		if box == nil {
			box = secretbox.Random()
		}
		var dora any
		if s.dso != nil {
			dora = s.dso
		}
		s.sn = snconn.New(snconn.Options{DB: o.DB, Box: box, Dora: dora, Dev: o.Dev, AllowPrivate: o.ConnectorAllowPrivate, Log: o.Log})
	}
}

func (s *Server) servicenowRoutes(mux *http.ServeMux) {
	const b = "/api/v1/devsecops/servicenow"
	mux.HandleFunc("GET "+b+"/integrations", adminOnly(s.snList))
	mux.HandleFunc("POST "+b+"/integrations", adminSession(s.snCreate))
	mux.HandleFunc("GET "+b+"/integrations/{id}", adminOnly(s.snGet))
	mux.HandleFunc("PATCH "+b+"/integrations/{id}", adminSession(s.snUpdate))
	mux.HandleFunc("DELETE "+b+"/integrations/{id}", adminSession(s.snDelete))
	mux.HandleFunc("POST "+b+"/integrations/{id}/test", adminSession(s.snTest))
	mux.HandleFunc("POST "+b+"/integrations/{id}/webhook-secret", adminSession(s.snWebhookSecret))
	mux.HandleFunc("GET "+b+"/integrations/{id}/services", adminOnly(s.snServices))
	mux.HandleFunc("PUT "+b+"/integrations/{id}/services/{service}", adminSession(s.snMapService))
	mux.HandleFunc("POST "+b+"/integrations/{id}/services/accept", adminSession(s.snAccept))
	mux.HandleFunc("POST "+b+"/integrations/{id}/discover", adminOnly(s.snDiscover))
	mux.HandleFunc("POST "+b+"/integrations/{id}/sync", adminOnly(s.snSync))
	mux.HandleFunc("GET "+b+"/integrations/{id}/runs", adminOnly(s.snRuns))
	mux.HandleFunc("GET "+b+"/incidents", inTenant(s.snIncidents))
	mux.HandleFunc("GET "+b+"/changes", inTenant(s.snChanges))
	mux.HandleFunc("GET "+b+"/incidents/{id}/proposal", inTenant(s.snProposal))
	mux.HandleFunc("POST "+b+"/incidents/{id}/promote", inTenant(s.snPromote))
	mux.HandleFunc("POST "+b+"/incidents/{id}/link", inTenant(s.snLink))
	mux.HandleFunc("POST "+b+"/incidents/{id}/decision", inTenant(s.snDecide))
	mux.HandleFunc("POST "+b+"/webhook/{integration}", s.snWebhook)
}

func snActor(r *http.Request, p *auth.Principal) snconn.Actor {
	return snconn.Actor{TenantID: p.TenantID, AccountID: p.AccountID, MemberID: p.MemberID, IP: clientIP(r)}
}

// snError: gli errori del connettore come quelli di GitHub, gli altri come quelli del motore.
func (s *Server) snError(w http.ResponseWriter, r *http.Request, err error) {
	var ge *ghconn.Error
	if errors.As(err, &ge) {
		s.ghError(w, r, err)
		return
	}
	s.engineError(w, r, err)
}

// snRemote mostra come errore del servizio remoto ciò che ServiceNow ha risposto.
func (s *Server) snRemote(w http.ResponseWriter, r *http.Request, err error) {
	var ge *ghconn.Error
	if !errors.As(err, &ge) {
		s.ghError(w, r, &ghconn.Error{Status: http.StatusBadGateway, Code: "servicenow.error", Msg: err.Error()})
		return
	}
	s.ghError(w, r, err)
}

// snTx esegue fn nella transazione del tenant e scrive il risultato.
func snTx[T any](s *Server, w http.ResponseWriter, r *http.Request, p *auth.Principal, readOnly bool, fn func(tx pgx.Tx) (T, error)) {
	var out T
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: readOnly}, func(tx pgx.Tx) error {
		var err error
		out, err = fn(tx)
		return err
	})
	if err != nil {
		s.snError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// canReportIncident: chi può segnalare un incidente (come risk.CreateIncident).
func canReportIncident(a engine.Actor) bool {
	return a.Has(rbac.ElementWrite) || a.Has(risk.PermIncident)
}

func (s *Server) snList(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	items, err := s.sn.List(r.Context(), p.TenantID)
	if err != nil {
		s.snError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "allow_private": s.sn.AllowPrivate(), "dev": s.sn.Dev(),
		"proxy": s.sn.Proxy(), "base_url": s.adm.baseURL})
}

func (s *Server) snGet(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	it, err := s.sn.Get(r.Context(), p.TenantID, r.PathValue("id"))
	if err != nil {
		s.snError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, it)
}

func (s *Server) snCreate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in snconn.Input
	if !decodeJSON(w, r, &in) {
		return
	}
	id, err := s.sn.Create(r.Context(), snActor(r, p), in)
	if err != nil {
		s.snError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id})
}

func (s *Server) snUpdate(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in snconn.Input
	if !decodeJSON(w, r, &in) {
		return
	}
	if err := s.sn.Update(r.Context(), snActor(r, p), r.PathValue("id"), in); err != nil {
		s.snError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) snDelete(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if err := s.sn.Delete(r.Context(), snActor(r, p), r.PathValue("id")); err != nil {
		s.snError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) snTest(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	res, err := s.sn.Test(r.Context(), snActor(r, p), r.PathValue("id"))
	if err != nil {
		s.snError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) snWebhookSecret(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	secret, err := s.sn.GenerateWebhookSecret(r.Context(), snActor(r, p), r.PathValue("id"))
	if err != nil {
		s.snError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"secret": secret})
}

func (s *Server) snServices(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	items, err := s.sn.Services(r.Context(), p.TenantID, r.PathValue("id"))
	if err != nil {
		s.snError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) snMapService(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in snconn.MapInput
	if !decodeJSON(w, r, &in) {
		return
	}
	out, err := s.sn.MapService(r.Context(), snActor(r, p), r.PathValue("id"), r.PathValue("service"), in)
	if err != nil {
		s.snError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) snAccept(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		IDs []string `json:"ids"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	n, err := s.sn.AcceptSuggestions(r.Context(), snActor(r, p), r.PathValue("id"), in.IDs)
	if err != nil {
		s.snError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"confirmed": n})
}

func (s *Server) snDiscover(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	res, err := s.sn.Discover(r.Context(), snActor(r, p), r.PathValue("id"))
	if err != nil {
		s.snRemote(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) snSync(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	run, err := s.sn.SyncNow(r.Context(), snActor(r, p), r.PathValue("id"), r.URL.Query().Get("full") == "true")
	if err != nil {
		s.snError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"run_id": run})
}

func (s *Server) snRuns(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	items, err := s.sn.Runs(r.Context(), p.TenantID, r.PathValue("id"))
	if err != nil {
		s.snError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func snLimit(r *http.Request) int {
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	return n
}

// snIncidents: ?application=<id> (gli incidenti di un'applicazione), ?integration=<id>,
// ?proposals=true (le proposte di incidente di Regia: serve poter segnalare un incidente).
func (s *Server) snIncidents(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	a := actorOf(r, p)
	q := r.URL.Query()
	f := snconn.IncidentFilter{IntegrationID: q.Get("integration"), ApplicationID: q.Get("application"), Proposals: q.Get("proposals") == "true", Limit: snLimit(r)}
	if !a.Has(rbac.ElementRead) || (f.Proposals && !canReportIncident(a)) {
		s.engineError(w, r, engine.ErrForbidden)
		return
	}
	snTx(s, w, r, p, true, func(tx pgx.Tx) (any, error) {
		items, err := snconn.Incidents(r.Context(), tx, f)
		return map[string]any{"items": items}, err
	})
}

func (s *Server) snChanges(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if !actorOf(r, p).Has(rbac.ElementRead) {
		s.engineError(w, r, engine.ErrForbidden)
		return
	}
	q := r.URL.Query()
	f := snconn.ChangeFilter{IntegrationID: q.Get("integration"), ApplicationID: q.Get("application"), Limit: snLimit(r)}
	snTx(s, w, r, p, true, func(tx pgx.Tx) (any, error) {
		items, err := snconn.Changes(r.Context(), tx, f)
		return map[string]any{"items": items}, err
	})
}

func (s *Server) snProposal(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if !canReportIncident(actorOf(r, p)) {
		s.engineError(w, r, engine.ErrForbidden)
		return
	}
	snTx(s, w, r, p, true, func(tx pgx.Tx) (*snconn.Proposal, error) {
		return snconn.ProposalFor(r.Context(), tx, r.PathValue("id"))
	})
}

// snPromote crea l'incidente di Regia dalla proposta, come l'ha confermata la persona
// (titolo, rilevazione, applicazioni, descrizione), e lo collega all'incidente ServiceNow.
func (s *Server) snPromote(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in risk.IncidentInput
	if !decodeJSON(w, r, &in) {
		return
	}
	a := actorOf(r, p)
	var view *risk.IncidentView
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		if _, err := snconn.ProposalFor(r.Context(), tx, r.PathValue("id")); err != nil {
			return err // inesistente o già collegato
		}
		v, err := s.risk.CreateIncident(r.Context(), tx, a, in)
		if err != nil {
			return err
		}
		if _, err := snconn.LinkRegiaIncident(r.Context(), tx, a, r.PathValue("id"), v.Element.ID); err != nil {
			return err
		}
		view = v
		return nil
	})
	if err != nil {
		s.snError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, view)
}

// snLink collega l'incidente ServiceNow a un incidente di Regia già esistente.
func (s *Server) snLink(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Incident string `json:"incident"` // chiave o id dell'incidente di Regia
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	a := actorOf(r, p)
	if !canReportIncident(a) {
		s.engineError(w, r, engine.ErrForbidden)
		return
	}
	snTx(s, w, r, p, false, func(tx pgx.Tx) (*snconn.Incident, error) {
		v, err := s.risk.IncidentView(r.Context(), tx, a, in.Incident)
		if err != nil {
			return nil, err
		}
		return snconn.LinkRegiaIncident(r.Context(), tx, a, r.PathValue("id"), v.Element.ID)
	})
}

func (s *Server) snDecide(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	a := actorOf(r, p)
	if !canReportIncident(a) {
		s.engineError(w, r, engine.ErrForbidden)
		return
	}
	snTx(s, w, r, p, false, func(tx pgx.Tx) (*snconn.Incident, error) {
		return snconn.DecideIncident(r.Context(), tx, a, r.PathValue("id"), in.Status)
	})
}

func (s *Server) snWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, snconn.MaxWebhookBody))
	if err != nil {
		writeProblem(w, r, http.StatusRequestEntityTooLarge, "request.too_large", "corpo del webhook troppo grande")
		return
	}
	res := s.sn.HandleWebhook(r.Context(), r.PathValue("integration"), r.Header, body, true)
	writeJSON(w, res.Status, res)
}
