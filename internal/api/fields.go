package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// Campi personalizzati (sdlc.md §10, "Campi personalizzati e pagina di dettaglio"):
// configurazione (fields.admin o tenant.admin, verificati dal motore), campi del tipo per i
// form, collegamenti e gruppi per la pagina di dettaglio.

func (s *Server) fieldsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/fields", inTenant(s.listFields))
	mux.HandleFunc("POST /api/v1/fields", inTenant(s.createField))
	mux.HandleFunc("GET /api/v1/fields/catalog", inTenant(s.fieldsCatalog))
	mux.HandleFunc("GET /api/v1/fields/export", inTenant(s.exportFields))
	mux.HandleFunc("POST /api/v1/fields/import", inTenant(s.importFields))
	mux.HandleFunc("GET /api/v1/fields/{ref}", inTenant(s.getField))
	mux.HandleFunc("PATCH /api/v1/fields/{ref}", inTenant(s.updateField))
	mux.HandleFunc("DELETE /api/v1/fields/{ref}", inTenant(s.deleteField))
	mux.HandleFunc("PUT /api/v1/fields/{ref}/options", inTenant(s.saveFieldOptions))
	mux.HandleFunc("PUT /api/v1/fields/{ref}/types/{type}", inTenant(s.associateField))
	mux.HandleFunc("DELETE /api/v1/fields/{ref}/types/{type}", inTenant(s.dissociateField))
	mux.HandleFunc("PUT /api/v1/types/{key}/fields/order", inTenant(s.reorderTypeFields))
	mux.HandleFunc("GET /api/v1/types/{key}/fields", inTenant(s.typeFields))
	mux.HandleFunc("GET /api/v1/elements/{ref}/links", inTenant(s.elementLinks))
	mux.HandleFunc("GET /api/v1/groups", inTenant(s.listGroups))
}

func (s *Server) listFields(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.eng.ListFields(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) fieldsCatalog(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*engine.FieldCatalog, error) {
		return s.eng.FieldsCatalog(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) getField(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*engine.FieldConfig, error) {
		return s.eng.GetField(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) createField(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in engine.FieldInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*engine.FieldConfig, error) {
		return s.eng.CreateField(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) updateField(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in engine.FieldInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*engine.FieldConfig, error) {
		return s.eng.UpdateField(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) deleteField(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.eng.DeleteField(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) saveFieldOptions(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Options []engine.OptionInput `json:"options"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*engine.FieldConfig, error) {
		return s.eng.SaveOptions(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.Options)
	})
}

func (s *Server) associateField(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in engine.AssocInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*engine.FieldConfig, error) {
		return s.eng.AssociateField(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), r.PathValue("type"), in)
	})
}

func (s *Server) dissociateField(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*engine.FieldConfig, error) {
		return s.eng.DissociateField(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), r.PathValue("type"))
	})
}

func (s *Server) reorderTypeFields(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Fields []string `json:"fields"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.eng.ReorderTypeFields(r.Context(), tx, actorOf(r, p), r.PathValue("key"), in.Fields)
	})
}

func (s *Server) exportFields(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var out *engine.FieldsBundle
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		out, err = s.eng.ExportFields(r.Context(), tx, actorOf(r, p))
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="campi.json"`)
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}

// errFieldsDryRun annulla la transazione dell'import di prova dopo aver calcolato l'esito.
var errFieldsDryRun = errors.New("import di prova")

func (s *Server) importFields(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var raw json.RawMessage
	if !decodeJSON(w, r, &raw) {
		return
	}
	b, err := engine.ParseFieldsBundle(raw)
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	dry := r.URL.Query().Get("dry_run") == "true"
	var rep *engine.ImportReport
	err = s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		if rep, err = s.eng.ImportFields(r.Context(), tx, actorOf(r, p), b); err != nil {
			return err
		}
		if dry {
			return errFieldsDryRun
		}
		return nil
	})
	if err != nil && !errors.Is(err, errFieldsDryRun) {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"report": rep, "dry_run": dry})
}

func (s *Server) typeFields(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.eng.TypeFields(r.Context(), tx, r.PathValue("key"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) elementLinks(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*engine.Links, error) {
		actor := actorOf(r, p)
		el, err := s.eng.Get(r.Context(), tx, actor, r.PathValue("ref"))
		if err != nil {
			return nil, err
		}
		return s.eng.ElementLinks(r.Context(), tx, actor, el)
	})
}

type groupRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Server) listGroups(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		q, limit, ids := searchParams(r)
		rows, err := tx.Query(r.Context(), `select id::text, name from groups
			where ($1 = '' or name ilike '%' || $1 || '%') and (cardinality($2::text[]) = 0 or id::text = any($2))
			order by lower(name), id limit nullif($3, 0)`, likeEscape(q), ids, limit)
		if err != nil {
			return nil, err
		}
		items, err := pgx.CollectRows(rows, pgx.RowToStructByPos[groupRef])
		return map[string]any{"items": items}, err
	})
}
