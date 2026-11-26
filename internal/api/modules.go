package api

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/audit"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// Moduli accendibili per tenant (replica/usabilita.md, sezione 6 punto 5).
//
// Un modulo spento sparisce da barra laterale, ⌘K e cruscotto e le sue API rispondono 404
// (codice module.disabled); i dati restano. Un modulo senza riga in tenant_modules è acceso:
// i tenant esistenti e quelli nuovi partono con tutto acceso.

// Module è un modulo del prodotto che il tenant può accendere o spegnere.
type Module struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Spegnendolo: cosa non si vede e non si raggiunge più.
	Effect string `json:"effect"`
	// prefissi di /api/v1/ che appartengono al modulo
	prefixes []string
}

// Le chiavi devono coincidere con web/src/modules.ts.
var moduleDefs = []Module{
	{
		Key: "demand", Name: "Demand e portafoglio",
		Description: "Richieste, valutazioni, graduatoria, roadmap, cruscotto, governance, finanza e portale dei richiedenti.",
		Effect:      "Spariscono richieste, cruscotto, roadmap, graduatoria, governance, finanza e portale; le API della domanda rispondono 404.",
		prefixes:    []string{"demand", "portfolio", "scorecards", "governance", "finance", "portal", "report", "status-reports", "dashboard", "reviews", "review-config", "impacts"},
	},
	{
		Key: "projects", Name: "Progetti",
		Description: "Progetti sopra le epiche: piano del PM con fasi, work package agili, a cascata o del fornitore, milestone e Panoramica.",
		Effect:      "Spariscono progetti, piani e Panoramica; le API dei progetti rispondono 404.",
		prefixes:    []string{"projects", "plan-items", "plan-phases"},
	},
	{
		Key: "work", Name: "Work e delivery",
		Description: "Board dei team, backlog e sprint, il mio lavoro, timesheet e capacità.",
		Effect:      "Spariscono board, backlog, il mio lavoro e timesheet; le API del lavoro rispondono 404.",
		prefixes:    []string{"work"},
	},
	{
		Key: "knowledge", Name: "Knowledge",
		Description: "Spazi di documentazione, pagine, documenti controllati e conferme di lettura.",
		Effect:      "Spariscono i documenti; le API di Knowledge rispondono 404.",
		prefixes:    []string{"kb"},
	},
	{
		Key: "devsecops", Name: "DevSecOps",
		Description: "Rilasci, vulnerabilità, metriche di consegna, aderenza della pipeline, connettori GitHub, GitLab e Gitea.",
		Effect:      "Spariscono pannello, vulnerabilità, metriche e aderenza; le API DevSecOps (anche quelle delle pipeline) rispondono 404.",
		prefixes:    []string{"devsecops"},
	},
	{
		Key: "grc", Name: "Conformità, rischi e incidenti",
		Description: "Cataloghi normativi, controlli ed evidenze, rischi, fornitori ICT, incidenti e notifiche alle autorità.",
		Effect:      "Spariscono conformità, rischi e incidenti; le loro API rispondono 404.",
		prefixes:    []string{"grc", "risk", "risks", "risks.csv", "risk-config", "ict", "incidents", "regulatory-notifications", "privacy"},
	},
	{
		Key: "architecture", Name: "Architettura",
		Description: "Mappa delle applicazioni, capability, dipendenze, razionalizzazione e diagrammi.",
		Effect:      "Spariscono mappa, capability e diagrammi; le loro API rispondono 404. Il catalogo delle applicazioni e degli asset resta in lettura, perché lo usano le altre aree.",
		prefixes:    []string{"architecture"},
	},
}

func moduleByKey(key string) (Module, bool) {
	for _, m := range moduleDefs {
		if m.Key == key {
			return m, true
		}
	}
	return Module{}, false
}

// moduleForRequest dice a quale modulo appartiene la richiesta ("" = a nessuno).
func moduleForRequest(method, path string) string {
	rest, ok := strings.CutPrefix(path, "/api/v1/")
	if !ok {
		return ""
	}
	seg, _, _ := strings.Cut(rest, "/")
	// Il catalogo delle applicazioni e degli asset serve a più aree (per esempio gli impatti
	// di una richiesta): resta leggibile anche con l'architettura spenta.
	if seg == "architecture" && method == http.MethodGet {
		if rest == "architecture/applications" || rest == "architecture/assets" {
			return ""
		}
	}
	for _, m := range moduleDefs {
		if slices.Contains(m.prefixes, seg) {
			return m.Key
		}
	}
	return ""
}

// ---------------------------------------------------------------------
// Lettura con cache breve
// ---------------------------------------------------------------------

type modulesEntry struct {
	off map[string]bool
	at  time.Time
}

// moduleCache evita una query per ogni richiesta; la scrittura la azzera subito.
type moduleCache struct {
	mu sync.Mutex
	m  map[string]modulesEntry
}

const moduleCacheTTL = 10 * time.Second

func (c *moduleCache) get(tenantID string) (map[string]bool, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[tenantID]
	if !ok || time.Since(e.at) > moduleCacheTTL {
		return nil, false
	}
	return e.off, true
}

func (c *moduleCache) put(tenantID string, off map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]modulesEntry{}
	}
	c.m[tenantID] = modulesEntry{off: off, at: time.Now()}
}

func (c *moduleCache) forget(tenantID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, tenantID)
}

// modulesOff è l'insieme dei moduli spenti del tenant.
func (s *Server) modulesOff(ctx context.Context, tenantID string) (map[string]bool, error) {
	if off, ok := s.mods.get(tenantID); ok {
		return off, nil
	}
	off := map[string]bool{}
	err := s.db.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, "select module from tenant_modules where not enabled")
		if err != nil {
			return err
		}
		keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
		for _, k := range keys {
			off[k] = true
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	s.mods.put(tenantID, off)
	return off, nil
}

// withModules risponde 404 alle API dei moduli spenti del tenant attivo.
func (s *Server) withModules(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mod := moduleForRequest(r.Method, r.URL.Path); mod != "" {
			if p := principal(r.Context()); p != nil && p.TenantID != "" {
				off, err := s.modulesOff(r.Context(), p.TenantID)
				switch {
				case err != nil:
					// Non è un controllo di sicurezza (i permessi restano): se la lettura fallisce si lascia passare.
					s.log.Warn("moduli del tenant non disponibili", "errore", err, "request_id", requestID(r.Context()))
				case off[mod]:
					m, _ := moduleByKey(mod)
					writeProblem(w, r, http.StatusNotFound, "module.disabled", "il modulo «"+m.Name+"» è spento per questa organizzazione")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ---------------------------------------------------------------------
// Profilo di menu del revisore
// ---------------------------------------------------------------------

// menuProfile è il profilo di menu legato al ruolo: «auditor» (revisore) per chi ha il ruolo
// auditor, direttamente o da un gruppo, e non può scrivere; chiunque abbia anche un ruolo di
// scrittura vede il menu intero. Vuoto = menu normale.
func (s *Server) menuProfile(ctx context.Context, p *auth.Principal) string {
	if p.TenantID == "" || p.Can(rbac.ElementWrite) || slices.Contains(p.Permissions, rbac.All) {
		return ""
	}
	has := p.MemberType == "auditor"
	if !has {
		err := s.db.InTenant(ctx, p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
			return tx.QueryRow(ctx, `select exists (
				select 1 from role_assignments ra
				join roles r on r.tenant_id = ra.tenant_id and r.id = ra.role_id
				where ra.space_id is null and r.key = 'auditor'
				  and (ra.member_id = $1 or ra.group_id in (select member_groups($1))))`, p.MemberID).Scan(&has)
		})
		if err != nil {
			s.log.Warn("profilo di menu non disponibile", "errore", err, "request_id", requestID(ctx))
			return ""
		}
	}
	if has {
		return "auditor"
	}
	return ""
}

// decorateMe aggiunge a /me i moduli accesi e il profilo di menu.
func (s *Server) decorateMe(ctx context.Context, p *auth.Principal, out *me) {
	if out.Active == nil {
		return
	}
	on := map[string]bool{}
	for _, m := range moduleDefs {
		on[m.Key] = true
	}
	if off, err := s.modulesOff(ctx, p.TenantID); err != nil {
		s.log.Warn("moduli del tenant non disponibili", "errore", err, "request_id", requestID(ctx))
	} else {
		for k := range off {
			on[k] = false
		}
	}
	out.Active.Modules = on
	out.Active.MenuProfile = s.menuProfile(ctx, p)
}

// ---------------------------------------------------------------------
// API di amministrazione
// ---------------------------------------------------------------------

type moduleState struct {
	Module
	Enabled   bool       `json:"enabled"`
	UpdatedAt *time.Time `json:"updated_at"`
	UpdatedBy *string    `json:"updated_by"`
}

func readModules(ctx context.Context, tx pgx.Tx) ([]moduleState, error) {
	rows, err := tx.Query(ctx, "select module, enabled, updated_at, updated_by from tenant_modules")
	if err != nil {
		return nil, err
	}
	type row struct {
		enabled bool
		at      time.Time
		by      *string
	}
	stored := map[string]row{}
	for rows.Next() {
		var k string
		var v row
		if err := rows.Scan(&k, &v.enabled, &v.at, &v.by); err != nil {
			rows.Close()
			return nil, err
		}
		stored[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]moduleState, 0, len(moduleDefs))
	for _, m := range moduleDefs {
		st := moduleState{Module: m, Enabled: true}
		if v, ok := stored[m.Key]; ok {
			at := v.at
			st.Enabled, st.UpdatedAt, st.UpdatedBy = v.enabled, &at, v.by
		}
		out = append(out, st)
	}
	return out, nil
}

func (s *Server) getModules(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := readModules(r.Context(), tx)
		return map[string]any{"items": items}, err
	})
}

func (s *Server) setModules(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Modules map[string]bool `json:"modules"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	for k := range in.Modules {
		if _, ok := moduleByKey(k); !ok {
			validationProblem(w, r, "modules."+k, "modulo sconosciuto: "+k)
			return
		}
	}
	defer s.mods.forget(p.TenantID)
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		ctx := r.Context()
		keys := make([]string, 0, len(in.Modules))
		for k := range in.Modules {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			if _, err := tx.Exec(ctx, `insert into tenant_modules (tenant_id, module, enabled, updated_by)
				values (app_tenant(), $1, $2, $3)
				on conflict (tenant_id, module) do update set enabled = excluded.enabled,
				  updated_by = excluded.updated_by, updated_at = now()`, k, in.Modules[k], p.MemberID); err != nil {
				return nil, err
			}
		}
		if len(keys) > 0 {
			if err := audit.Append(ctx, tx, audit.Event{Action: "tenant.modules.update", EntityType: "tenant", EntityID: p.TenantID,
				Payload: map[string]any{"modules": in.Modules}, ActorAccountID: p.AccountID, ActorMemberID: p.MemberID, IP: clientIP(r)}); err != nil {
				return nil, err
			}
		}
		items, err := readModules(ctx, tx)
		return map[string]any{"items": items}, err
	})
}

// moduleRoutes registra le rotte dei moduli (chiamata da Handler).
func (s *Server) moduleRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/tenant/modules", inTenant(s.getModules))
	mux.HandleFunc("PUT /api/v1/tenant/modules", can(rbac.TenantAdmin, s.setModules))
}
