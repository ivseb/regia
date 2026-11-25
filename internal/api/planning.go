package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/planning"
	"regia/internal/platform/auth"
)

// Pianificazione: piano della richiesta (Gantt), spostamenti, roadmap e carico dei team.

func (s *Server) getPlan(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*planning.Plan, error) {
		return s.plan.GetPlan(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) autoPlan(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Replan bool   `json:"replan"`
		From   string `json:"from"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		ch, err := s.plan.AutoPlan(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.Replan, in.From)
		return map[string]any{"changes": ch}, err
	})
}

func (s *Server) schedule(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Start   string `json:"start"`
		End     string `json:"end"`
		Cascade *bool  `json:"cascade"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	cascade := in.Cascade == nil || *in.Cascade
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		ch, err := s.plan.Schedule(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.Start, in.End, cascade)
		return map[string]any{"changes": ch}, err
	})
}

func (s *Server) shiftRequest(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Start string `json:"start"`
		End   string `json:"end"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		ch, err := s.plan.ShiftRequest(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.Start, in.End)
		return map[string]any{"changes": ch}, err
	})
}

func (s *Server) roadmap(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	actor, closed := actorOf(r, p), r.URL.Query().Get("closed") == "true"
	// La roadmap ordina tutte le richieste aperte del tenant: cache breve (swr.go).
	key := strings.Join([]string{"roadmap", p.TenantID, fmt.Sprint(actor.Has("element.read")), actor.MemberID, fmt.Sprint(closed)}, "|")
	s.swr.serve(s, w, r, p, key, true, 5*time.Second, func(ctx context.Context, tx pgx.Tx) (any, error) {
		items, err := s.plan.Roadmap(ctx, tx, actor, closed)
		return map[string]any{"items": items}, err
	})
}

func (s *Server) teamLoad(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.plan.Load(r.Context(), tx, actorOf(r, p), q.Get("from"), q.Get("to"))
		return map[string]any{"items": items}, err
	})
}
