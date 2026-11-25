package api

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/devsecops"
	ghconn "regia/internal/modules/devsecops/connectors/github"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// DevSecOps, fase 3 (replica/devsecops.md §3.3 e §3.4): politica centrale della pipeline,
// aderenza per servizio e applicazione, adozione guidata tramite pull request.
//
// La lettura (politica, aderenza, «cosa manca») vale per chi ha element.read; la modifica
// della politica e l'adozione (anteprima, scarico dei file, pull request) per chi gestisce il
// catalogo (architecture.write) o l'organizzazione.

func (s *Server) policyRoutes(mux *http.ServeMux) {
	const b = "/api/v1/devsecops"
	mux.HandleFunc("GET "+b+"/policy", inTenant(s.polGet))
	mux.HandleFunc("PUT "+b+"/policy", inTenant(s.polSave))
	mux.HandleFunc("POST "+b+"/policy/impact", inTenant(s.polImpact))
	mux.HandleFunc("GET "+b+"/policy/diff", inTenant(s.polDiff))
	mux.HandleFunc("GET "+b+"/policy/versions/{n}", inTenant(s.polVersion))
	mux.HandleFunc("GET "+b+"/adherence", inTenant(s.polAdherence))
	mux.HandleFunc("GET "+b+"/elements/{ref}/pipeline", inTenant(s.polPipeline))
	mux.HandleFunc("GET "+b+"/applications/{id}/adoption", inTenant(s.polAdoptionOptions))
	mux.HandleFunc("POST "+b+"/applications/{id}/adoption/sync", inTenant(s.polAdoptionSync))
	mux.HandleFunc("GET "+b+"/adoption/repositories", inTenant(s.polAdoptionRepos))
	mux.HandleFunc("POST "+b+"/adoption/preview", inTenant(s.polAdoptionPreview))
	mux.HandleFunc("POST "+b+"/adoption/download", inTenant(s.polAdoptionDownload))
	mux.HandleFunc("POST "+b+"/adoption/pull-request", inTenant(s.polAdoptionPR))
}

// polJSON è tenantJSON con gli errori della politica (problemi di validazione).
func polJSON[T any](s *Server, w http.ResponseWriter, r *http.Request, p *auth.Principal, readOnly bool, status int, fn func(tx pgx.Tx) (T, error)) {
	var out T
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: readOnly}, func(tx pgx.Tx) error {
		var err error
		out, err = fn(tx)
		return err
	})
	if err != nil {
		var ve *devsecops.ValidationError
		if errors.As(err, &ve) {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusUnprocessableEntity)
			writeJSONBody(w, map[string]any{"type": "about:blank", "title": "Unprocessable Entity", "status": http.StatusUnprocessableEntity,
				"detail": ve.Error(), "code": "policy.invalid", "problems": ve.Problems, "request_id": requestID(r.Context())})
			return
		}
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, status, out)
}

func (s *Server) polGet(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	polJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.Policy(langCtx(r.Context(), tx, p), tx, actorOf(r, p))
	})
}

func (s *Server) polSave(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in devsecops.PolicyInput
	if !decodeJSON(w, r, &in) {
		return
	}
	polJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.SavePolicy(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) polImpact(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Definition devsecops.PolicyDef `json:"definition"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	polJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.PolicyImpact(r.Context(), tx, actorOf(r, p), in.Definition)
	})
}

func (s *Server) polDiff(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	a, _ := strconv.Atoi(r.URL.Query().Get("from"))
	b, err := strconv.Atoi(r.URL.Query().Get("to"))
	if err != nil || b < 1 || a < 0 {
		writeProblem(w, r, http.StatusBadRequest, "request.invalid_param", "servono le versioni from (facoltativa) e to")
		return
	}
	polJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		ch, err := s.dso.PolicyDiff(r.Context(), tx, actorOf(r, p), a, b)
		return map[string]any{"from": a, "to": b, "changes": ch}, err
	})
}

func (s *Server) polVersion(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 1 {
		writeProblem(w, r, http.StatusBadRequest, "request.invalid_param", "numero di versione non valido")
		return
	}
	polJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.PolicyVersionOf(r.Context(), tx, actorOf(r, p), n)
	})
}

func (s *Server) polAdherence(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	polJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		return s.dso.Adherence(r.Context(), tx, actorOf(r, p))
	})
}

// polPipeline: la scheda «Pipeline» di un'applicazione o di un asset, con l'elenco guidato
// «cosa manca» completato con ciò che sa il connettore GitHub.
func (s *Server) polPipeline(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var v *devsecops.PipelineView
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		v, err = s.dso.PipelineOf(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	ids := make([]string, len(v.Applications))
	for i, a := range v.Applications {
		ids[i] = a.Application.ID
	}
	adoptions := map[string][]ghconn.AdoptionPR{}
	if s.gh != nil {
		brief, err := s.gh.AdoptionSummary(r.Context(), p.TenantID, ids)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		adoptions = brief.Adoptions
		for i := range v.Applications {
			a := &v.Applications[i]
			a.Items = append(a.Items, connectorItems(brief, adoptions[a.Application.ID], a.Items)...)
		}
		s.gh.RefreshAdoptionsAsync(p.TenantID, ids)
	}
	writeJSON(w, http.StatusOK, map[string]any{"pipeline": v, "adoptions": adoptions})
}

// connectorItems sono le voci dell'elenco guidato che dipendono da GitHub.
func connectorItems(b *ghconn.AdoptionBrief, prs []ghconn.AdoptionPR, existing []devsecops.ChecklistItem) []devsecops.ChecklistItem {
	var out []devsecops.ChecklistItem
	add := func(key, label, state, detail, action string) {
		out = append(out, devsecops.ChecklistItem{Key: key, Label: label, State: state, Detail: detail, Action: action})
	}
	if b.Integrations == 0 {
		add("github", "GitHub è collegato a Regia", "todo", "Nessuna integrazione GitHub attiva: serve per leggere i repository e aprire le pull request.", "github")
	} else {
		add("github", "GitHub è collegato a Regia", "ok", fmt.Sprintf("%d integrazioni attive.", b.Integrations), "")
	}
	if !b.Templates {
		add("template", "I template della pipeline sono configurati", "todo", "Nessun profilo ha template di adozione: si configurano da Politica della pipeline → Adozione.", "template")
	} else {
		add("template", "I template della pipeline sono configurati", "ok", "", "")
	}
	if len(prs) > 0 {
		pr := prs[0]
		switch pr.State {
		case "open":
			add("pr", "La pull request di adozione è unita", "info", fmt.Sprintf("#%d su %s è aperta, in attesa di revisione.", pr.Number, pr.Repository), "")
		case "merged":
			add("pr", "La pull request di adozione è unita", "ok", fmt.Sprintf("#%d su %s è stata unita.", pr.Number, pr.Repository), "")
		default:
			add("pr", "La pull request di adozione è unita", "todo", fmt.Sprintf("#%d su %s è stata chiusa senza essere unita.", pr.Number, pr.Repository), "propose")
		}
	}
	return out
}

// requireConfigure: l'adozione è di chi gestisce il catalogo o l'organizzazione.
func (s *Server) requireConfigure(w http.ResponseWriter, r *http.Request, p *auth.Principal) bool {
	if !devsecops.CanConfigure(actorOf(r, p)) {
		writeProblem(w, r, http.StatusForbidden, "auth.forbidden", "serve il permesso di gestire il catalogo (architecture.write) o l'amministrazione")
		return false
	}
	if s.gh == nil {
		writeProblem(w, r, http.StatusServiceUnavailable, "github.unavailable", "il connettore GitHub non è attivo")
		return false
	}
	return true
}

func (s *Server) polAdoptionOptions(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if !s.requireConfigure(w, r, p) {
		return
	}
	o, err := s.gh.AdoptionOptions(r.Context(), p.TenantID, r.PathValue("id"))
	if err != nil {
		s.ghError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

func (s *Server) polAdoptionSync(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if !s.requireConfigure(w, r, p) {
		return
	}
	n, err := s.gh.SyncAdoptions(r.Context(), p.TenantID, r.PathValue("id"), true)
	if err != nil {
		s.ghError(w, r, err)
		return
	}
	list, err := s.gh.AdoptionSummary(r.Context(), p.TenantID, []string{r.PathValue("id")})
	if err != nil {
		s.ghError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checked": n, "adoptions": list.Adoptions[r.PathValue("id")]})
}

func (s *Server) polAdoptionRepos(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	if !s.requireConfigure(w, r, p) {
		return
	}
	items, err := s.gh.RepositorySearch(r.Context(), p.TenantID, r.URL.Query().Get("q"))
	if err != nil {
		s.ghError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) polAdoptionPreview(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in ghconn.AdoptionInput
	if !decodeJSON(w, r, &in) || !s.requireConfigure(w, r, p) {
		return
	}
	pv, err := s.gh.PreviewAdoption(r.Context(), p.TenantID, in)
	if err != nil {
		s.ghError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, pv)
}

// polAdoptionDownload: i file generati in un archivio zip, per chi non dà alla App i
// permessi di scrittura o per aprire la pull request a mano.
func (s *Server) polAdoptionDownload(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in ghconn.AdoptionInput
	if !decodeJSON(w, r, &in) || !s.requireConfigure(w, r, p) {
		return
	}
	files, err := s.gh.AdoptionFiles(r.Context(), p.TenantID, in)
	if err != nil {
		s.ghError(w, r, err)
		return
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		fw, err := zw.Create(f.Path)
		if err != nil {
			s.internal(w, r, err)
			return
		}
		_, _ = fw.Write([]byte(f.Content))
	}
	if err := zw.Close(); err != nil {
		s.internal(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="pipeline-`+safeName(in.ApplicationID)+`.zip"`)
	_, _ = w.Write(buf.Bytes())
}

func safeName(s string) string {
	if len(s) > 8 {
		s = s[:8]
	}
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, strings.ToLower(s))
}

func (s *Server) polAdoptionPR(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in ghconn.AdoptionInput
	if !decodeJSON(w, r, &in) || !s.requireConfigure(w, r, p) {
		return
	}
	pr, err := s.gh.OpenAdoptionPR(r.Context(), ghActor(r, p), in)
	if err != nil {
		s.ghError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, pr)
}
