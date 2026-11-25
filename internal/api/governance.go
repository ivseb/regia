package api

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/governance"
	"regia/internal/platform/auth"
)

// Governance (demand.md §7; piano, passo 9): organi, sedute, ordine del giorno,
// decisioni che chiudono i passi di approvazione, presenze e verbale.

func (s *Server) listBodies(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.gov.Bodies(r.Context(), tx, actorOf(r, p), r.URL.Query().Get("archived") == "true")
		if err != nil {
			return nil, err
		}
		// Gruppi tra cui scegliere (con i membri effettivi di ciascuno, sottogruppi compresi).
		rows, err := tx.Query(r.Context(), `select g.id, g.name, (select count(*) from group_effective_members(g.id))::int
			from groups g where g.system_kind is null order by lower(g.name)`)
		if err != nil {
			return nil, err
		}
		groups, err := pgx.CollectRows(rows, pgx.RowToStructByPos[groupView])
		return map[string]any{"items": items, "kinds": governance.Kinds, "groups": groups}, err
	})
}

func (s *Server) createBody(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in governance.BodyInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*governance.Body, error) {
		return s.gov.CreateBody(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) updateBody(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in governance.BodyInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*governance.Body, error) {
		return s.gov.UpdateBody(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) listMeetings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	parse := func(name string) (*time.Time, bool) {
		v := q.Get(name)
		if v == "" {
			return nil, true
		}
		t, err := time.Parse(time.DateOnly, v)
		if err != nil {
			writeProblem(w, r, http.StatusBadRequest, "request.invalid_param", name+" deve essere una data AAAA-MM-GG")
			return nil, false
		}
		return &t, true
	}
	from, ok := parse("from")
	if !ok {
		return
	}
	to, ok := parse("to")
	if !ok {
		return
	}
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.gov.Meetings(r.Context(), tx, actorOf(r, p), from, to, q.Get("body"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) createMeeting(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in governance.MeetingInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*governance.Meeting, error) {
		return s.gov.CreateMeeting(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) getMeeting(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*governance.Meeting, error) {
		return s.gov.Meeting(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}

func (s *Server) updateMeeting(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in governance.MeetingInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*governance.Meeting, error) {
		return s.gov.UpdateMeeting(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in)
	})
}

func (s *Server) meetingStatus(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Status string `json:"status"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*governance.Meeting, error) {
		return s.gov.SetStatus(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.Status)
	})
}

func (s *Server) addMeetingItem(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Element string `json:"element"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*governance.Meeting, error) {
		return s.gov.AddItem(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.Element)
	})
}

func (s *Server) removeMeetingItem(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*governance.Meeting, error) {
		return s.gov.RemoveItem(r.Context(), tx, actorOf(r, p), r.PathValue("id"), r.PathValue("element"))
	})
}

func (s *Server) meetingAttendance(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Attendance []governance.Presence `json:"attendance"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*governance.Meeting, error) {
		return s.gov.SetAttendance(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.Attendance)
	})
}

func (s *Server) meetingDecision(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in governance.DecisionInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*governance.Meeting, error) {
		return s.gov.Decide(r.Context(), tx, actorOf(r, p), r.PathValue("id"), r.PathValue("element"), in)
	})
}

func (s *Server) saveMinutes(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Text string `json:"text"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*governance.Meeting, error) {
		return s.gov.SaveMinutes(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in.Text)
	})
}

func (s *Server) approveMinutes(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*governance.Meeting, error) {
		return s.gov.ApproveMinutes(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}
