package api

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/finance"
	"regia/internal/modules/work"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// Tempo e team (piano, passo 18; replica/work.md «Tempo e team»): timesheet settimanale,
// report del tempo, comandi in blocco, membri dei team con capacità e assenze.

func (s *Server) tempoRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/work/timesheet", inTenant(s.tsGet))
	mux.HandleFunc("PUT /api/v1/work/timesheet/cells", inTenant(s.tsCells))
	mux.HandleFunc("POST /api/v1/work/timesheet/copy", inTenant(s.tsCopy))
	mux.HandleFunc("POST /api/v1/work/timesheet/submit", inTenant(s.tsSubmit))
	mux.HandleFunc("POST /api/v1/work/timesheet/withdraw", inTenant(s.tsWithdraw))
	mux.HandleFunc("POST /api/v1/work/timesheet/decide", inTenant(s.tsDecide))
	mux.HandleFunc("POST /api/v1/work/timesheet/reopen", inTenant(s.tsReopen))
	mux.HandleFunc("GET /api/v1/work/timesheet/pending", inTenant(s.tsPending))
	mux.HandleFunc("GET /api/v1/work/time/settings", inTenant(s.timeSettings))
	mux.HandleFunc("PUT /api/v1/work/time/settings", inTenant(s.saveTimeSettings))
	mux.HandleFunc("GET /api/v1/work/time/report", inTenant(s.timeReport))
	mux.HandleFunc("GET /api/v1/work/time/records", inTenant(s.timeRecords))

	mux.HandleFunc("GET /api/v1/work/bulk/context", inTenant(s.bulkContext))
	mux.HandleFunc("POST /api/v1/work/bulk", inTenant(s.bulkRun))
	mux.HandleFunc("GET /api/v1/work/bulk/last", inTenant(s.bulkLast))
	mux.HandleFunc("POST /api/v1/work/bulk/undo", inTenant(s.bulkUndo))

	mux.HandleFunc("GET /api/v1/work/teams", inTenant(s.workTeams))
	mux.HandleFunc("PUT /api/v1/work/teams/{id}/members", inTenant(s.saveTeamMember))
	mux.HandleFunc("DELETE /api/v1/work/teams/{id}/members/{member}", inTenant(s.removeTeamMember))
	mux.HandleFunc("GET /api/v1/work/absences", inTenant(s.listAbsences))
	mux.HandleFunc("POST /api/v1/work/absences", inTenant(s.createAbsence))
	mux.HandleFunc("DELETE /api/v1/work/absences/{id}", inTenant(s.deleteAbsence))
}

// ---------------------------------------------------------------------------
// Timesheet

func (s *Server) tsGet(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*work.Timesheet, error) {
		return s.work.GetTimesheet(r.Context(), tx, actorOf(r, p), q.Get("week"), q.Get("member"))
	})
}

// costLogs calcola il costo delle registrazioni toccate, in una transazione separata con
// app.finance (chi registra non vede tariffe); se fallisce, il ricalcolo della Finanza le sistema.
func (s *Server) costLogs(r *http.Request, p *auth.Principal, ids []string) {
	if len(ids) == 0 {
		return
	}
	if err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{Finance: true}, func(tx pgx.Tx) error {
		_, err := finance.CostWorkLogs(r.Context(), tx, p.MemberID, ids)
		return err
	}); err != nil {
		s.log.Warn("costo delle ore non calcolato", "errore", err, "request_id", requestID(r.Context()))
	}
}

func (s *Server) tsCells(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Week  string           `json:"week"`
		Cells []work.CellInput `json:"cells"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	var res *work.CellsResult
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		res, err = s.work.SetCells(r.Context(), tx, actorOf(r, p), in.Cells)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	s.costLogs(r, p, res.Logs)
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*work.Timesheet, error) {
		return s.work.GetTimesheet(r.Context(), tx, actorOf(r, p), in.Week, "")
	})
}

func (s *Server) tsCopy(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Week string `json:"week"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	var res *work.CellsResult
	var n int
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var err error
		res, n, err = s.work.CopyWeek(r.Context(), tx, actorOf(r, p), in.Week)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	s.costLogs(r, p, res.Logs)
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		ts, err := s.work.GetTimesheet(r.Context(), tx, actorOf(r, p), in.Week, "")
		return map[string]any{"copied": n, "timesheet": ts}, err
	})
}

func (s *Server) tsSubmit(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Week string `json:"week"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*work.Timesheet, error) {
		return s.work.SubmitWeek(r.Context(), tx, actorOf(r, p), in.Week)
	})
}

func (s *Server) tsWithdraw(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Week string `json:"week"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*work.Timesheet, error) {
		return s.work.WithdrawWeek(r.Context(), tx, actorOf(r, p), in.Week)
	})
}

func (s *Server) tsDecide(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in work.DecideInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*work.Timesheet, error) {
		return s.work.DecideWeek(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) tsReopen(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Member string `json:"member"`
		Week   string `json:"week"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.work.ReopenWeek(r.Context(), tx, actorOf(r, p), in.Member, in.Week)
	})
}

func (s *Server) tsPending(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.work.PendingWeeks(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) timeSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*work.TimeSettings, error) {
		return s.work.TimeSettings(r.Context(), tx, actorOf(r, p))
	})
}

func (s *Server) saveTimeSettings(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in work.TimeSettingsInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*work.TimeSettings, error) {
		return s.work.SaveTimeSettings(r.Context(), tx, actorOf(r, p), in)
	})
}

// ---------------------------------------------------------------------------
// Report

func timeFilter(r *http.Request) work.TimeFilter {
	q := r.URL.Query()
	today := time.Now()
	from, to := q.Get("from"), q.Get("to")
	if from == "" {
		from = time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC).Format(time.DateOnly)
	}
	if to == "" {
		to = today.Format(time.DateOnly)
	}
	return work.TimeFilter{From: from, To: to, Person: q.Get("person"), Team: q.Get("team"), Space: q.Get("space"),
		Epic: q.Get("epic"), Request: q.Get("request"), Element: q.Get("element"), Week: q.Get("week"), Month: q.Get("month")}
}

func (s *Server) timeReport(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	f := timeFilter(r)
	d1, d2, _ := strings.Cut(q.Get("group"), ",")
	var rep *work.TimeReport
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		rep, err = s.work.Report(r.Context(), tx, actorOf(r, p), f, d1, d2)
		return err
	})
	if err == nil && rep.CanCost {
		// I costi stanno nelle tabelle riservate: una seconda transazione, solo con finance.read.
		err = s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true, Finance: true}, func(tx pgx.Tx) error {
			costs, err := s.work.ReportCosts(r.Context(), tx, actorOf(r, p), f, rep.Dim1, rep.Dim2)
			if err == nil {
				work.MergeCosts(rep, costs)
			}
			return err
		})
	}
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	if q.Get("format") == "csv" {
		writeReportCSV(w, rep)
		return
	}
	writeJSON(w, http.StatusOK, rep)
}

var dimNames = map[string]string{"person": "Persona", "team": "Team", "space": "Spazio", "epic": "Epic", "request": "Richiesta",
	"element": "Elemento", "week": "Settimana", "month": "Mese"}

func hoursCSV(m int64) string {
	return strings.Replace(fmt.Sprintf("%.2f", float64(m)/60), ".", ",", 1)
}

func writeReportCSV(w http.ResponseWriter, rep *work.TimeReport) {
	var b bytes.Buffer
	b.WriteString("\xef\xbb\xbf") // Excel riconosce l'UTF-8
	cw := csv.NewWriter(&b)
	cw.Comma = ';'
	head := []string{dimNames[rep.Dim1]}
	if rep.Dim2 != "" {
		head = append(head, dimNames[rep.Dim2])
	}
	head = append(head, "Ore", "Stima (ore)", "Capacità (ore)")
	if rep.CanCost {
		head = append(head, "Costo")
	}
	_ = cw.Write(head)
	row := func(a, c string, g work.ReportGroup) {
		out := []string{a}
		if rep.Dim2 != "" {
			out = append(out, c)
		}
		out = append(out, hoursCSV(g.Minutes), hoursCSV(g.Estimate))
		if g.Capacity != nil {
			out = append(out, hoursCSV(*g.Capacity))
		} else {
			out = append(out, "")
		}
		if rep.CanCost {
			if g.CostCents != nil {
				out = append(out, strings.Replace(fmt.Sprintf("%.2f", float64(*g.CostCents)/100), ".", ",", 1))
			} else {
				out = append(out, "")
			}
		}
		_ = cw.Write(out)
	}
	for _, g := range rep.Groups {
		if len(g.Children) == 0 {
			row(g.Label, "", g)
			continue
		}
		for _, c := range g.Children {
			row(g.Label, c.Label, c)
		}
	}
	row("Totale", "", rep.Total)
	cw.Flush()
	download(w, "text/csv; charset=utf-8", "tempo-"+rep.From+"-"+rep.To+".csv", b.Bytes())
}

func (s *Server) timeRecords(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	f := timeFilter(r)
	csvOut := r.URL.Query().Get("format") == "csv"
	limit := 500
	if csvOut {
		limit = 20000
	}
	var out *work.TimeRecords
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		out, err = s.work.Records(r.Context(), tx, actorOf(r, p), f, limit)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	if !csvOut {
		writeJSON(w, http.StatusOK, out)
		return
	}
	var b bytes.Buffer
	b.WriteString("\xef\xbb\xbf")
	cw := csv.NewWriter(&b)
	cw.Comma = ';'
	_ = cw.Write([]string{"Data", "Persona", "Elemento", "Titolo", "Spazio", "Epic", "Ore", "Nota"})
	for _, x := range out.Items {
		epic, text := "", ""
		if x.Epic != nil {
			epic = *x.Epic
		}
		if x.Text != nil {
			text = *x.Text
		}
		_ = cw.Write([]string{x.Date, x.Member, x.ElementKey, x.Title, x.Space, epic, hoursCSV(int64(x.Minutes)), text})
	}
	cw.Flush()
	download(w, "text/csv; charset=utf-8", "registrazioni-"+f.From+"-"+f.To+".csv", b.Bytes())
}

// ---------------------------------------------------------------------------
// Comandi in blocco

func (s *Server) bulkContext(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (*work.BulkContext, error) {
		return s.work.BulkContext(r.Context(), tx, actorOf(r, p), r.URL.Query().Get("space"), r.URL.Query().Get("type") == work.RequestType)
	})
}

func (s *Server) bulkRun(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in work.BulkInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*work.BulkResult, error) {
		return s.work.Bulk(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) bulkLast(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		l, err := s.work.LastBulk(r.Context(), tx, actorOf(r, p))
		return map[string]any{"last": l}, err
	})
}

func (s *Server) bulkUndo(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*work.UndoResult, error) {
		return s.work.UndoBulk(r.Context(), tx, actorOf(r, p))
	})
}

// ---------------------------------------------------------------------------
// Team e assenze

func (s *Server) workTeams(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.work.Teams(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) saveTeamMember(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in work.TeamMemberInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		if err := s.work.SaveTeamMember(r.Context(), tx, actorOf(r, p), r.PathValue("id"), in); err != nil {
			return nil, err
		}
		items, err := s.work.Teams(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) removeTeamMember(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (any, error) {
		if err := s.work.RemoveTeamMember(r.Context(), tx, actorOf(r, p), r.PathValue("id"), r.PathValue("member")); err != nil {
			return nil, err
		}
		items, err := s.work.Teams(r.Context(), tx, actorOf(r, p))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) listAbsences(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query()
	tenantJSON(s, w, r, p, true, http.StatusOK, func(tx pgx.Tx) (any, error) {
		items, err := s.work.ListAbsences(r.Context(), tx, actorOf(r, p), q.Get("member"), q.Get("from"), q.Get("to"))
		return map[string]any{"items": items}, err
	})
}

func (s *Server) createAbsence(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in work.AbsenceInput
	if !decodeJSON(w, r, &in) {
		return
	}
	tenantJSON(s, w, r, p, false, http.StatusCreated, func(tx pgx.Tx) (*work.Absence, error) {
		return s.work.CreateAbsence(r.Context(), tx, actorOf(r, p), in)
	})
}

func (s *Server) deleteAbsence(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	tenantJSON(s, w, r, p, false, http.StatusNoContent, func(tx pgx.Tx) (any, error) {
		return nil, s.work.DeleteAbsence(r.Context(), tx, actorOf(r, p), r.PathValue("id"))
	})
}
