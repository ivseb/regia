package reviews

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Proposte dell'AI per le risposte (funzione «questionari»): restano finché chi le ha chieste non
// decide, e le vede solo lui (vengono da fonti lette con i suoi permessi). Accettare scrive la
// risposta con la sorgente «ai» e l'origine (accettata o cambiata e accettata), con i permessi
// e le regole di sempre: punteggio, prescrizioni e requisiti si aggiornano come per ogni risposta.

// Gradi di confidenza.
const (
	ConfHigh = "alta"
	ConfMid  = "media"
	ConfLow  = "bassa"
	ConfNone = "nessuna" // nessuna fonte: da compilare a mano
)

// AISource è la fonte citata da una proposta. Kind: answer (risposta confermata della stessa
// applicazione), similar (di un'applicazione simile), page (pagina Knowledge), fact (fatto del
// grafo), attachment (allegato). Key è l'elemento da aprire; Question la domanda della
// valutazione citata (per l'ancora); Detail il valore, il nome del fatto o del file.
type AISource struct {
	Kind     string `json:"kind"`
	Key      string `json:"key,omitempty"`
	Title    string `json:"title,omitempty"`
	Question string `json:"question,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// AIProposalInput è una proposta da conservare (già controllata dal livello AI).
type AIProposalInput struct {
	Question   string
	Value      any // nil = nessuna fonte
	Sources    []AISource
	Reason     string
	Confidence string
}

// AIProposal è una proposta in attesa.
type AIProposal struct {
	ID         string     `json:"id"`
	Question   string     `json:"question"`
	Value      any        `json:"value"`
	Sources    []AISource `json:"sources"`
	Reason     string     `json:"reason"`
	Confidence string     `json:"confidence"`
	CreatedAt  time.Time  `json:"created_at"`
}

func aiProposals(ctx context.Context, tx pgx.Tx, reviewID, memberID string) ([]AIProposal, error) {
	out := []AIProposal{}
	if memberID == "" {
		return out, nil
	}
	rows, err := tx.Query(ctx, `select id::text, question_key, value, sources, reason, confidence, created_at
		from questionnaire_ai_proposals where review_id = $1 and created_by = $2 and status = 'proposed' order by created_at, question_key`,
		reviewID, memberID)
	if err != nil {
		return nil, err
	}
	var p AIProposal
	var val, src []byte
	_, err = pgx.ForEachRow(rows, []any{&p.ID, &p.Question, &val, &src, &p.Reason, &p.Confidence, &p.CreatedAt}, func() error {
		x := p
		x.Value, x.Sources = nil, []AISource{}
		if len(val) > 0 {
			if err := json.Unmarshal(val, &x.Value); err != nil {
				return err
			}
		}
		if len(src) > 0 {
			if err := json.Unmarshal(src, &x.Sources); err != nil {
				return err
			}
		}
		out = append(out, x)
		return nil
	})
	return out, err
}

// SaveAIProposals conserva le proposte di una sezione: sostituiscono quelle ancora in attesa
// della stessa persona sulle stesse domande. Una domanda che nel frattempo ha una risposta, o
// che la persona non può più compilare, si salta; un valore che non vale per la domanda diventa
// «nessuna fonte» (Regia non salva mai un valore che la domanda non accetta).
func (s *Service) SaveAIProposals(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in []AIProposalInput) (*View, error) {
	if actor.MemberID == "" {
		return nil, engine.ErrForbidden
	}
	el, r, err := s.resolve(ctx, tx, actor, ref, true)
	if err != nil {
		return nil, err
	}
	p, err := s.permsOf(ctx, tx, actor, r)
	if err != nil {
		return nil, err
	}
	def, _, err := loadTemplateVersion(ctx, tx, r.TemplateID)
	if err != nil {
		return nil, err
	}
	for _, pi := range in {
		q, sec := def.question(pi.Question)
		if q == nil || q.Type == QAttachment || !p.canAnswer(r.Stage, sec.Key, sec.FilledBy == "team") {
			continue
		}
		if a, ok := r.Answers[q.Key]; ok && q.complete(a.V) {
			continue
		}
		val := pi.Value
		if val != nil {
			if nv, err := q.NormalizeValue(val); err != nil || nv == nil {
				val = nil
			} else {
				val = nv
			}
		}
		conf, sources := pi.Confidence, pi.Sources
		if val == nil {
			conf, sources = ConfNone, []AISource{}
		}
		switch conf {
		case ConfHigh, ConfMid, ConfLow, ConfNone:
		default:
			conf = ConfLow
		}
		if sources == nil {
			sources = []AISource{}
		}
		var raw []byte
		if val != nil {
			raw, _ = json.Marshal(val)
		}
		srcRaw, _ := json.Marshal(sources)
		if _, err := tx.Exec(ctx, `update questionnaire_ai_proposals set status = 'superseded', decided_at = now()
			where review_id = $1 and created_by = $2 and question_key = $3 and status = 'proposed'`, r.ID, actor.MemberID, q.Key); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `insert into questionnaire_ai_proposals (tenant_id, review_id, question_key, value, sources, reason, confidence, created_by)
			values (app_tenant(), $1, $2, $3, $4, $5, $6, $7)`, r.ID, q.Key, raw, srcRaw, clip(pi.Reason, 1000), conf, actor.MemberID); err != nil {
			return nil, err
		}
	}
	return s.View(ctx, tx, actor, el.ID)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// AIDecision è la decisione su una proposta: Accept con il valore della proposta o, se Value
// c'è, con il valore cambiato dalla persona.
type AIDecision struct {
	ID     string `json:"id"`
	Accept bool   `json:"accept"`
	Value  any    `json:"value,omitempty"`
}

// AIDecided riassume le decisioni, per il registro delle chiamate.
type AIDecided struct {
	Accepted   int      `json:"accepted"`
	Edited     int      `json:"edited"`
	Discarded  int      `json:"discarded"`
	ElementIDs []string `json:"-"`
}

// DecideAIProposals applica le decisioni della persona. Accettare è rispondere: valgono i
// permessi sulla sezione e il controllo del valore; la risposta porta la sorgente «ai».
func (s *Service) DecideAIProposals(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, ds []AIDecision) (*View, *AIDecided, error) {
	if actor.MemberID == "" {
		return nil, nil, engine.ErrForbidden
	}
	if len(ds) == 0 || len(ds) > 200 {
		return nil, nil, engine.InvalidCode("validation.reviews.ai_decisions_count", "decisions", "da 1 a 200 decisioni per volta")
	}
	el, r, err := s.resolve(ctx, tx, actor, ref, true)
	if err != nil {
		return nil, nil, err
	}
	p, err := s.permsOf(ctx, tx, actor, r)
	if err != nil {
		return nil, nil, err
	}
	def, _, err := loadTemplateVersion(ctx, tx, r.TemplateID)
	if err != nil {
		return nil, nil, err
	}
	res := &AIDecided{ElementIDs: []string{r.ID}}
	vals := map[string]any{}
	origin := map[string]*AnswerAI{}
	for _, d := range ds {
		var qkey string
		var raw []byte
		err := tx.QueryRow(ctx, `select question_key, value from questionnaire_ai_proposals
			where id::text = $1 and review_id = $2 and created_by = $3 and status = 'proposed' for update`, d.ID, r.ID, actor.MemberID).Scan(&qkey, &raw)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, engine.InvalidCode("validation.reviews.ai_proposal_gone", "decisions", "la proposta non c'è più: la domanda ha già una risposta o è stata riproposta")
		}
		if err != nil {
			return nil, nil, err
		}
		if !d.Accept {
			if _, err := tx.Exec(ctx, `update questionnaire_ai_proposals set status = 'discarded', decided_at = now() where id::text = $1`, d.ID); err != nil {
				return nil, nil, err
			}
			res.Discarded++
			continue
		}
		q, sec := def.question(qkey)
		if q == nil {
			return nil, nil, engine.InvalidCode("validation.reviews.question_unknown", "decisions", "domanda inesistente in questo modello")
		}
		if !p.canAnswer(r.Stage, sec.Key, sec.FilledBy == "team") {
			if r.Stage == StageCompile || r.Stage == StageClarification || r.Stage == StageExam {
				return nil, nil, engine.ErrForbidden
			}
			return nil, nil, engine.InvalidCode("validation.reviews.not_fillable", "decisions", "la valutazione non si compila in questa fase")
		}
		var proposed any
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &proposed); err != nil {
				return nil, nil, err
			}
		}
		v := proposed
		if d.Value != nil {
			v = d.Value
		}
		if v == nil {
			return nil, nil, engine.InvalidCode("validation.reviews.ai_proposal_empty", "decisions", "la proposta non ha un valore: rispondi a mano")
		}
		nv, err := q.NormalizeValue(v)
		if err != nil {
			return nil, nil, err
		}
		if nv == nil {
			return nil, nil, engine.InvalidCode("validation.reviews.ai_proposal_empty", "decisions", "la proposta non ha un valore: rispondi a mano")
		}
		edited := false
		if proposed != nil {
			pn, _ := q.NormalizeValue(proposed)
			edited = !jsonEqual(pn, nv)
		} else {
			edited = true
		}
		status := "accepted"
		if edited {
			status = "edited"
			res.Edited++
		} else {
			res.Accepted++
		}
		if _, err := tx.Exec(ctx, `update questionnaire_ai_proposals set status = $2, decided_at = now() where id::text = $1`, d.ID, status); err != nil {
			return nil, nil, err
		}
		vals[q.Key] = nv
		origin[q.Key] = &AnswerAI{Proposal: d.ID, Edited: edited}
	}
	if len(vals) > 0 {
		facts, err := s.contextFacts(ctx, tx, r)
		if err != nil {
			return nil, nil, err
		}
		if err := s.writeAnswersFrom(ctx, tx, actor, r, def, facts, vals, "ai", origin); err != nil {
			return nil, nil, err
		}
		if r.ElementStateCat == "todo" && r.Stage == StageCompile {
			if err := s.startCompiling(ctx, tx, actor, el); err != nil {
				return nil, nil, err
			}
		} else if _, err := tx.Exec(ctx, "update elements set version = version + 1, updated_by = $2 where id = $1", el.ID, nullable(actor.MemberID)); err != nil {
			return nil, nil, err
		}
	}
	v, err := s.View(ctx, tx, actor, el.ID)
	return v, res, err
}
