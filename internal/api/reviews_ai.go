package api

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"regia/internal/ai"
	"regia/internal/modules/reviews"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// Questionari precompilati dall'AI (funzione «questionari», docs/ai.md §11):
//   - POST /api/v1/reviews/{ref}/ai-proposals {section}: propone le risposte della sezione e le
//     conserva per chi le ha chieste (le vede solo lui); risponde con la valutazione aggiornata;
//   - POST /api/v1/reviews/{ref}/ai-proposals/decide {decisions:[{id, accept, value?}]}: accetta
//     (anche con il valore cambiato) o scarta; accettare è rispondere, con i permessi di sempre.

func (s *Server) reviewsAIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/reviews/{ref}/ai-proposals", inTenant(s.reviewAIPropose))
	mux.HandleFunc("POST /api/v1/reviews/{ref}/ai-proposals/decide", inTenant(s.reviewAIDecide))
}

type reviewAIOut struct {
	View   *reviews.View           `json:"view"`
	Result *ai.QuestionnaireResult `json:"result,omitempty"`
}

func (s *Server) reviewAIPropose(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Section string `json:"section"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	actor := actorOf(r, p)
	ref := r.PathValue("ref")
	var qc *reviews.AIContext
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true, Member: p.MemberID}, func(tx pgx.Tx) error {
		// Prima la funzione: con l'AI spenta niente letture inutili.
		if _, _, err := s.ai.Resolve(r.Context(), tx, ai.FeatureQuestionnaire); err != nil {
			return err
		}
		var err error
		qc, err = s.reviews.AIContext(r.Context(), tx, actor, ref, in.Section)
		return err
	})
	if err != nil {
		s.aiReviewsError(w, r, err)
		return
	}
	res, err := s.ai.Questionnaire(r.Context(), p.TenantID, actor, s.memberLang(r, p), qc)
	if err != nil {
		s.aiError(w, r, err)
		return
	}
	reviewJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*reviewAIOut, error) {
		v, err := s.reviews.SaveAIProposals(r.Context(), tx, actor, ref, res.Items)
		return &reviewAIOut{View: v, Result: res}, err
	})
}

func (s *Server) reviewAIDecide(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var in struct {
		Decisions []reviews.AIDecision `json:"decisions"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	actor := actorOf(r, p)
	var d *reviews.AIDecided
	reviewJSON(s, w, r, p, false, http.StatusOK, func(tx pgx.Tx) (*reviewAIOut, error) {
		v, res, err := s.reviews.DecideAIProposals(r.Context(), tx, actor, r.PathValue("ref"), in.Decisions)
		if err == nil {
			d = res
		}
		return &reviewAIOut{View: v}, err
	})
	if d != nil {
		s.ai.QuestionnaireDecided(r.Context(), p.TenantID, actor, d)
	}
}

// aiReviewsError: gli errori dell'AI (spenta, budget) come le altre funzioni, gli altri come le valutazioni.
func (s *Server) aiReviewsError(w http.ResponseWriter, r *http.Request, err error) {
	var ae *ai.Error
	if errors.As(err, &ae) {
		s.aiError(w, r, err)
		return
	}
	s.reviewsError(w, r, err)
}
