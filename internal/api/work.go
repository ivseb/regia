package api

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/finance"
	"regia/internal/modules/work"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// Work management di base (piano, passi 10 e 18; replica/work.md): spazi dei team, board,
// backlog e sprint, ore lavorate, il mio lavoro, epic dei team su impatti e richieste.

func (s *Server) workRoutes(mux *http.ServeMux) {
	s.workBoardTeamRoutes(mux)
	mux.HandleFunc("GET /api/v1/work/spaces", inTenant(s.workSpaces))
	mux.HandleFunc("POST /api/v1/work/spaces", inTenant(s.createWorkSpace))
	mux.HandleFunc("GET /api/v1/work/spaces/{key}/board", inTenant(s.workBoard))
	mux.HandleFunc("PUT /api/v1/work/spaces/{key}/board/settings", inTenant(s.workBoardSettings))
	mux.HandleFunc("POST /api/v1/work/spaces/{key}/board/move", inTenant(s.workMove))
	mux.HandleFunc("POST /api/v1/work/spaces/{key}/items", inTenant(s.workCreateItem))
	mux.HandleFunc("GET /api/v1/work/spaces/{key}/backlog", inTenant(s.workBacklog))
	mux.HandleFunc("POST /api/v1/work/spaces/{key}/sprints", inTenant(s.workCreateSprint))
	mux.HandleFunc("PATCH /api/v1/work/sprints/{ref}", inTenant(s.workUpdateSprint))
	mux.HandleFunc("POST /api/v1/work/sprints/{ref}/start", inTenant(s.workStartSprint))
	mux.HandleFunc("POST /api/v1/work/sprints/{ref}/close", inTenant(s.workCloseSprint))
	mux.HandleFunc("GET /api/v1/work/sprints/{ref}/burndown", inTenant(s.workBurndown))
	mux.HandleFunc("PUT /api/v1/work/elements/{ref}/rank", inTenant(s.workRank))
	mux.HandleFunc("PUT /api/v1/work/elements/{ref}/assignee", inTenant(s.workAssign))
	mux.HandleFunc("PUT /api/v1/work/elements/{ref}/sprint", inTenant(s.workPlan))
	mux.HandleFunc("GET /api/v1/work/elements/{ref}/time", inTenant(s.workTime))
	mux.HandleFunc("POST /api/v1/work/elements/{ref}/time", inTenant(s.workLogTime))
	mux.HandleFunc("DELETE /api/v1/work/time/{id}", inTenant(s.workDeleteTime))
	mux.HandleFunc("GET /api/v1/work/elements/{ref}/delivery", inTenant(s.workDelivery))
	mux.HandleFunc("POST /api/v1/work/impacts/{ref}/epic", inTenant(s.workCreateEpic))
	mux.HandleFunc("GET /api/v1/work/me", inTenant(s.workMine))
}

// writeJSONCompressed comprime con gzip le risposte grandi (board e backlog con migliaia di
// card) se il client lo accetta.
func writeJSONCompressed(w http.ResponseWriter, r *http.Request, v any) {
	if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		writeJSON(w, http.StatusOK, v)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Encoding", "gzip")
	w.Header().Add("Vary", "Accept-Encoding")
	w.WriteHeader(http.StatusOK)
	gz := getGzip()
	gz.Reset(w)
	_ = json.NewEncoder(gz).Encode(v)
	_ = gz.Close()
	putGzip(gz)
}

// I compressori si riusano: ogni gzip.NewWriter alloca circa 1,2 MB di stato (con 8 richieste in
// parallelo sul backlog erano il 10% di tutta la memoria allocata, benchmark 2026-11-24). Una
// sync.Pool non basta: con il limite di memoria il garbage collector gira decine di volte al
// secondo e la svuota ogni volta; questa lista di 4 non si svuota.
var gzipFree = make(chan *gzip.Writer, 4)

func getGzip() *gzip.Writer {
	select {
	case gz := <-gzipFree:
		return gz
	default:
		gz, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		return gz
	}
}

func putGzip(gz *gzip.Writer) {
	gz.Reset(io.Discard) // non trattiene la risposta (e la connessione) finché aspetta
	select {
	case gzipFree <- gz:
	default:
	}
}

func (s *Server) workSpaces(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		spaces, without, err := s.work.Spaces(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": spaces, "teams_without_space": without}, err
	})
}

func (s *Server) createWorkSpace(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in work.SpaceInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*work.TeamSpace, error) {
		return s.work.CreateTeamSpace(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) workBoard(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	var out *work.Board
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		bq := work.BoardQuery{Q: q.Get("q"), Sprint: q.Get("sprint"), Swimlane: q.Get("swimlane"), After: q.Get("after")}
		if v := q.Get("col"); v != "" {
			n, perr := strconv.Atoi(v)
			if perr != nil {
				return engine.InvalidValue("col", "colonna non valida")
			}
			bq.Col = &n
		}
		bq.Limit, _ = strconv.Atoi(q.Get("limit"))
		out, err = s.work.GetBoard(r.Context(), tx, actorOf(r, p), r.PathValue("key"), bq)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSONCompressed(w, r, out)
}

func (s *Server) workBoardSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in work.BoardSettings
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*work.BoardSettings, error) {
		return s.work.SaveSettings(r.Context(), tx, actorOf(r, p), r.PathValue("key"), in)
	})
}

func (s *Server) workMove(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in work.MoveInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*work.MoveResult, error) {
		return s.work.Move(r.Context(), tx, actorOf(r, p), r.PathValue("key"), in)
	})
}

func (s *Server) workCreateItem(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in work.ItemInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*engine.Element, error) {
		return s.work.CreateItem(r.Context(), tx, actorOf(r, p), r.PathValue("key"), in)
	})
}

func (s *Server) workBacklog(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var out *work.Backlog
	// limit e offset (facoltativi) paginano il solo backlog; gli sprint arrivano sempre interi.
	limit, offset := pageParams(r)
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		out, err = s.work.GetBacklogPage(r.Context(), tx, actorOf(r, p), r.PathValue("key"), r.URL.Query().Get("q"), work.BacklogPage{Limit: limit, Offset: offset})
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSONCompressed(w, r, out)
}

func (s *Server) workCreateSprint(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in work.SprintInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*engine.Element, error) {
		return s.work.CreateSprint(r.Context(), tx, actorOf(r, p), r.PathValue("key"), in)
	})
}

func (s *Server) workUpdateSprint(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in work.SprintInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*engine.Element, error) {
		return s.work.UpdateSprint(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
	})
}

func (s *Server) workStartSprint(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*engine.Element, error) {
		return s.work.StartSprint(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) workCloseSprint(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		MoveTo string `json:"move_to"` // next | backlog | chiave di uno sprint
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*engine.Element, error) {
		return s.work.CloseSprint(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.MoveTo)
	})
}

func (s *Server) workBurndown(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*work.Burndown, error) {
		return s.work.SprintBurndown(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) workRank(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		After  string `json:"after"`
		Before string `json:"before"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		rank, err := s.work.SetRank(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.After, in.Before)
		return map[string]string{"rank": rank}, err
	})
}

func (s *Server) workAssign(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		MemberID string `json:"member_id"` // vuoto = nessuno
		Version  int    `json:"version"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*engine.Element, error) {
		el, err := s.work.SetAssignee(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.MemberID, in.Version)
		if err != nil {
			return nil, err
		}
		return s.eng.Get(r.Context(), tx, actorOf(r, p), el.ID)
	})
}

func (s *Server) workPlan(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Sprint string `json:"sprint"` // chiave o id; vuoto = backlog
		After  string `json:"after"`
		Before string `json:"before"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*engine.Element, error) {
		return s.work.PlanInSprint(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in.Sprint, in.After, in.Before)
	})
}

func (s *Server) workTime(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*work.TimeSummary, error) {
		return s.work.TimeOf(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

// workLogTime registra le ore e poi, in una transazione di sistema separata (come il worker),
// ne calcola il costo: chi registra non vede tariffe né importi. Se il calcolo non riesce le
// ore restano registrate senza costo e il ricalcolo della Finanza le sistema.
func (s *Server) workLogTime(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in work.TimeInput
	if !decodeJSON(w, r, &in) {
		return
	}
	var id string
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		id, err = s.work.LogTime(r.Context(), tx, actorOf(r, p), r.PathValue("ref"), in)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	costed := false
	if err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{Finance: true}, func(tx pgx.Tx) error {
		res, err := finance.CostWorkLogs(r.Context(), tx, p.MemberID, []string{id})
		costed = res.Costed > 0
		return err
	}); err != nil {
		s.log.Warn("costo delle ore non calcolato", "errore", err, "work_log", id, "request_id", requestID(r.Context()))
	}
	tenantJSON(s, w, r, p, true, http.StatusCreated, func(tx pgx.Tx) (any, error) {
		sum, err := s.work.TimeOf(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
		return map[string]any{"id": id, "costed": costed, "time": sum}, err
	})
}

func (s *Server) workDeleteTime(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.work.DeleteTime(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) workDelivery(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*work.Delivery, error) {
		return s.work.DeliveryOf(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) workCreateEpic(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*engine.Element, error) {
		return s.work.CreateEpicFor(r.Context(), tx, actorOf(r, p), r.PathValue("ref"))
	})
}

func (s *Server) workMine(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.work.MyWork(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": items}, err
	})
}
