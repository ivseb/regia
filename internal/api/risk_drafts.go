package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/risk"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// Storia ed esportazione delle bozze delle notifiche normative (ondata 5 dell'AI). La notifica
// non si invia da Regia: ogni versione salvata si scarica in Markdown o in JSON (incident.manage).

func (s *Server) notificationDrafts(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	type out struct {
		Notification *risk.Notification  `json:"notification"`
		Items        []risk.DraftVersion `json:"items"`
	}
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*out, error) {
		n, vs, err := s.risk.DraftVersions(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
		if err != nil {
			return nil, err
		}
		return &out{Notification: n, Items: vs}, nil
	})
}

func (s *Server) notificationDraftExport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	version, err := strconv.Atoi(r.PathValue("version"))
	if err != nil || version < 1 {
		writeProblem(w, r, http.StatusNotFound, "element.not_found", "versione inesistente")
		return
	}
	var n *risk.Notification
	var v *risk.DraftVersion
	if err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		n, v, err = s.risk.DraftVersionOf(r.Context(), tx, actorOf(r, p), r.PathValue("id"), version)
		return err
	}); err != nil {
		s.engineError(w, r, err)
		return
	}
	name := strings.ToLower(n.IncidentKey + "-" + n.SchemeKey + "-" + n.StageKey + "-v" + strconv.Itoa(v.Version))
	if r.URL.Query().Get("format") == "json" {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.json"`)
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(map[string]any{
			"format": "regia.notification-draft/v1", "incident": n.IncidentKey, "incident_title": n.Incident,
			"scheme": n.SchemeKey, "scheme_name": n.SchemeName, "stage": n.StageKey, "stage_name": n.StageName,
			"version": v.Version, "source": v.Source, "created_by": v.CreatedBy, "created_at": v.CreatedAt,
			"submitted": false, "sources": risk.DoraSources, "fields": v.Fields, "content": v.Content,
		})
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`.md"`)
	_, _ = w.Write([]byte(v.Content))
}
