package api

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/audit"
	"regia/internal/platform/auth"
	"regia/internal/platform/rbac"
)

// Primo avvio guidato (usabilità, proposta 7B; migrazione 0061).
//
//	GET    /api/v1/avvio                                      i passi di «Inizia da qui», con lo stato calcolato
//	POST   /api/v1/avvio/chiudi    DELETE ...                 richiude / riapre la pagina
//	GET    /api/v1/avvio/modelli                              i modelli per tipo di azienda
//	GET    /api/v1/avvio/modelli/{key}                        anteprima: cosa crea, senza scrivere
//	POST   /api/v1/avvio/modelli/{key}                        applica il modello
//	GET    /api/v1/avvio/applicato                            cosa è applicato e in che stato
//	GET    /api/v1/avvio/applicato/processi/{key}             anteprima del ripristino di un processo
//	POST   /api/v1/avvio/applicato/processi/{key}/ripristina  riporta il processo al pacchetto di partenza
//	POST   /api/v1/avvio/applicato/cataloghi/{key}/verifica   conferma che i testi sono stati verificati
//
// Tutto richiede tenant.admin: i passi sono per chi configura l'organizzazione.
func (s *Server) avvioRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/avvio", can(rbac.TenantAdmin, s.avvioSteps))
	mux.HandleFunc("POST /api/v1/avvio/chiudi", can(rbac.TenantAdmin, s.avvioClose(true)))
	mux.HandleFunc("DELETE /api/v1/avvio/chiudi", can(rbac.TenantAdmin, s.avvioClose(false)))
	mux.HandleFunc("GET /api/v1/avvio/modelli", can(rbac.TenantAdmin, s.avvioModels))
	mux.HandleFunc("GET /api/v1/avvio/modelli/{key}", can(rbac.TenantAdmin, s.avvioPreview))
	mux.HandleFunc("POST /api/v1/avvio/modelli/{key}", can(rbac.TenantAdmin, s.avvioApply))
	mux.HandleFunc("GET /api/v1/avvio/applicato", can(rbac.TenantAdmin, s.avvioApplied))
	mux.HandleFunc("GET /api/v1/avvio/applicato/processi/{key}", can(rbac.TenantAdmin, s.avvioRestorePreview))
	mux.HandleFunc("POST /api/v1/avvio/applicato/processi/{key}/ripristina", can(rbac.TenantAdmin, s.avvioRestore))
	mux.HandleFunc("POST /api/v1/avvio/applicato/cataloghi/{key}/verifica", can(rbac.TenantAdmin, s.avvioVerify))
}

// avvioStep è un passo della checklist; lo stato lo calcola il sistema da ciò che c'è nel tenant.
type avvioStep struct {
	Key string `json:"key"`
	// Done: il passo è fatto (o non serve, con NotNeeded).
	Done      bool `json:"done"`
	NotNeeded bool `json:"not_needed,omitempty"`
	// Count: quanti oggetti lo soddisfano (persone, team, organi…), per la frase sotto il titolo.
	Count int `json:"count"`
}

type avvioState struct {
	Steps  []avvioStep `json:"steps"`
	Done   int         `json:"done"`
	Total  int         `json:"total"`
	Closed bool        `json:"closed"`
	// Model: il modello applicato per ultimo (vuoto = nessuno).
	Model string `json:"model"`
}

func (s *Server) computeAvvio(ctx context.Context, tx pgx.Tx, tenantID string) (*avvioState, error) {
	off, err := s.modulesOff(ctx, tenantID)
	if err != nil {
		off = map[string]bool{}
	}
	one := func(sql string) (int, error) {
		var n int
		err := tx.QueryRow(ctx, sql).Scan(&n)
		return n, err
	}
	out := &avvioState{Steps: []avvioStep{}}
	if err := tx.QueryRow(ctx, `select coalesce((select key from avvio_applicazioni where kind = 'modello' order by applied_at desc limit 1), '')`).Scan(&out.Model); err != nil {
		return nil, err
	}
	hasModel := out.Model != ""
	add := func(key string, count int, done, notNeeded bool) {
		out.Steps = append(out.Steps, avvioStep{Key: key, Count: count, Done: done || notNeeded, NotNeeded: notNeeded})
	}

	n, err := one(`select count(*) from tenant_language`)
	if err != nil {
		return nil, err
	}
	add("organizzazione", n, n > 0, false)

	n, err = one(`select count(*) from tenant_modules`)
	if err != nil {
		return nil, err
	}
	add("moduli", n, n > 0 || hasModel, false)

	n, err = one(`select (select count(*) from members) + (select count(*) from invitations)`)
	if err != nil {
		return nil, err
	}
	add("persone", n-1, n > 1, false)

	n, err = one(`select count(*) from teams where not archived`)
	if err != nil {
		return nil, err
	}
	add("team", n, n > 0, false)

	n, err = one(`select count(*) from process_versions v join process_definitions d on d.id = v.process_id
		join element_types t on t.id = d.type_id where t.key = 'business_request' and v.status = 'published' and v.number > 1`)
	if err != nil {
		return nil, err
	}
	add("processo", n, n > 0 || hasModel, off["demand"])

	n, err = one(`select count(*) from governance_bodies where not archived`)
	if err != nil {
		return nil, err
	}
	more, err := one(`select count(*) from (select body_id from governance_body_members group by body_id having count(*) > 1) x`)
	if err != nil {
		return nil, err
	}
	add("organi", n, n > 1 || more > 0, off["demand"])

	n, err = one(`select count(distinct f.id) from frameworks f join framework_versions v on v.framework_id = f.id
		where f.owner_tenant_id = app_tenant() and v.status = 'published'`)
	if err != nil {
		return nil, err
	}
	add("norme", n, n > 0, off["grc"])

	n, err = one(`select count(*) from elements e join element_types t on t.id = e.type_id where t.key = 'business_request'`)
	if err != nil {
		return nil, err
	}
	add("richiesta", n, n > 0, off["demand"])

	for _, st := range out.Steps {
		out.Total++
		if st.Done {
			out.Done++
		}
	}
	err = tx.QueryRow(ctx, `select exists (select 1 from avvio_stato)`).Scan(&out.Closed)
	return out, err
}

func (s *Server) avvioSteps(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*avvioState, error) {
		return s.computeAvvio(r.Context(), tx, p.TenantID)
	})
}

func (s *Server) avvioClose(closed bool) func(http.ResponseWriter, *http.Request, *auth.Principal) {
	return func(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
		tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*avvioState, error) {
			ctx := r.Context()
			var err error
			if closed {
				_, err = tx.Exec(ctx, `insert into avvio_stato (tenant_id, closed_by) values (app_tenant(), $1)
					on conflict (tenant_id) do update set closed_by = excluded.closed_by, closed_at = now()`, p.MemberID)
			} else {
				_, err = tx.Exec(ctx, `delete from avvio_stato`)
			}
			if err != nil {
				return nil, err
			}
			return s.computeAvvio(ctx, tx, p.TenantID)
		})
	}
}

// avvioAudit registra nel registro di audit un'azione del primo avvio.
func avvioAudit(ctx context.Context, tx pgx.Tx, r *http.Request, p *auth.Principal, action string, payload map[string]any) error {
	return audit.Append(ctx, tx, audit.Event{Action: action, EntityType: "avvio", Payload: payload,
		ActorAccountID: p.AccountID, ActorMemberID: p.MemberID, IP: clientIP(r)})
}
