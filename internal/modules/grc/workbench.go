package grc

import (
	"context"
	"math"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Mappatura guidata requisito ↔ controllo: i requisiti senza controllo raggruppati per
// normativa, i controlli con i suggerimenti per somiglianza di testo e di categoria (il
// controllo copre già requisiti dello stesso capitolo o della stessa normativa) e
// l'associazione in blocco. I suggerimenti sono solo suggerimenti: la mappatura la fa una persona.

// WorkReq è un requisito senza controllo.
type WorkReq struct {
	ID        string `json:"id"`
	Ref       string `json:"ref"` // "dora:Art. 9"
	Framework string `json:"framework"`
	ReqRef    string `json:"req_ref"`
	Title     string `json:"title"`
	Summary   string `json:"summary"`
	Parent    string `json:"parent"`
}

// WorkFramework è una normativa con i requisiti ancora senza controllo.
type WorkFramework struct {
	Key      string    `json:"key"`
	Name     string    `json:"name"`
	Total    int       `json:"total"`   // requisiti da valutare della normativa
	Missing  int       `json:"missing"` // senza controllo
	Requests []WorkReq `json:"requirements"`
}

// WorkControl è un controllo tra cui scegliere.
type WorkControl struct {
	Key           string   `json:"key"`
	Title         string   `json:"title"`
	Text          string   `json:"text"`
	Owner         string   `json:"owner"`
	Frameworks    []string `json:"frameworks"`
	Requirements  int      `json:"requirements"`
	Effectiveness string   `json:"effectiveness"`
}

// Workbench è lo stato della mappatura guidata.
type Workbench struct {
	Frameworks []WorkFramework `json:"frameworks"`
	Controls   []WorkControl   `json:"controls"`
	Can        struct {
		Edit bool `json:"edit"`
	} `json:"can"`
}

// Workbench restituisce i requisiti senza controllo (per normativa abilitata) e i controlli.
func (s *Service) Workbench(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*Workbench, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	m, err := s.loadModel(ctx, tx)
	if err != nil {
		return nil, err
	}
	w := &Workbench{Frameworks: []WorkFramework{}, Controls: []WorkControl{}}
	w.Can.Edit = actor.Has(PermAdmin)
	for _, f := range m.fws {
		wf := WorkFramework{Key: f.Key, Name: f.Name, Requests: []WorkReq{}}
		for _, r := range f.reqs {
			if !r.Assessable {
				continue
			}
			wf.Total++
			if len(r.maps) > 0 {
				continue
			}
			wf.Missing++
			wf.Requests = append(wf.Requests, WorkReq{ID: r.ID, Ref: f.Key + ":" + r.Ref, Framework: f.Key, ReqRef: r.Ref, Title: r.Title, Summary: r.Summary, Parent: r.Parent})
		}
		w.Frameworks = append(w.Frameworks, wf)
	}
	texts, err := s.controlTexts(ctx, tx)
	if err != nil {
		return nil, err
	}
	for _, c := range m.ctrlOrder {
		wc := WorkControl{Key: c.Key, Title: c.Title, Text: clip(texts[c.ID], 240), Owner: c.Owner, Frameworks: []string{}, Requirements: len(c.reqs), Effectiveness: c.Effectiveness}
		for _, r := range c.reqs {
			if !slices.Contains(wc.Frameworks, r.fw.Key) {
				wc.Frameworks = append(wc.Frameworks, r.fw.Key)
			}
		}
		w.Controls = append(w.Controls, wc)
	}
	return w, nil
}

func (s *Service) controlTexts(ctx context.Context, tx pgx.Tx) (map[string]string, error) {
	rows, err := tx.Query(ctx, `select x.element_id::text, e.body_text from ext_control x join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id`)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	var id, t string
	_, err = pgx.ForEachRow(rows, []any{&id, &t}, func() error {
		out[id] = t
		return nil
	})
	return out, err
}

func clip(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

var stopwords = map[string]bool{}

func init() {
	for _, w := range strings.Fields(`della delle degli dello dell alla alle agli allo nell nella nelle negli nello sono essere deve devono
		dalla dalle dagli dallo come anche tutti tutte ogni quando qualsiasi presso tramite secondo sulla sulle sugli sullo
		the and for that with from shall must should have been this their which these those into over under than also each other
		art articolo comma lettera punto paragrafo`) {
		stopwords[w] = true
	}
}

// tokens: parole significative (almeno 4 lettere, senza le parole vuote), in minuscolo.
func tokens(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if len([]rune(w)) >= 4 && !stopwords[w] {
			out[w] = true
		}
	}
	return out
}

// Suggestion è un controllo suggerito, con il perché.
type Suggestion struct {
	Control  string   `json:"control"`
	Score    int      `json:"score"` // 0..100
	Words    []string `json:"words"` // parole in comune (testo)
	Category string   `json:"category,omitempty"`
}

// Suggest ordina i controlli per quanto somigliano ai requisiti indicati (media sui requisiti):
// parole in comune tra titolo e testo e, come categoria, lo stesso capitolo (+) o la stessa
// normativa (+) di requisiti che il controllo copre già.
func (s *Service) Suggest(ctx context.Context, tx pgx.Tx, actor engine.Actor, reqIDs []string) ([]Suggestion, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	if len(reqIDs) > 200 {
		return nil, engine.InvalidCode("validation.grc.requirements_too_many", "requirements", "al massimo 500 requisiti per controllo")
	}
	m, err := s.loadModel(ctx, tx)
	if err != nil {
		return nil, err
	}
	texts, err := s.controlTexts(ctx, tx)
	if err != nil {
		return nil, err
	}
	var reqs []*reqModel
	for _, id := range reqIDs {
		if r := m.reqs[id]; r != nil {
			reqs = append(reqs, r)
		}
	}
	out := []Suggestion{}
	if len(reqs) == 0 {
		return out, nil
	}
	for _, c := range m.ctrlOrder {
		ct := tokens(c.Title + " " + texts[c.ID])
		var total float64
		words := map[string]int{}
		cat := ""
		for _, r := range reqs {
			rt := tokens(r.Title + " " + r.Summary)
			common := 0
			for w := range rt {
				if ct[w] {
					common++
					words[w]++
				}
			}
			if len(rt) > 0 && len(ct) > 0 {
				total += float64(common) / math.Sqrt(float64(len(rt)*len(ct)))
			}
			bonus, label := 0.0, ""
			for _, cr := range c.reqs {
				if cr.fw != r.fw {
					continue
				}
				if bonus < 0.08 {
					bonus, label = 0.08, "framework"
				}
				if r.Parent != "" && cr.Parent == r.Parent {
					bonus, label = 0.2, "chapter"
					break
				}
			}
			total += bonus
			if label == "chapter" || (label == "framework" && cat == "") {
				cat = label
			}
		}
		score := int(math.Round(100 * total / float64(len(reqs)) * 2.2))
		if score > 100 {
			score = 100
		}
		if score <= 0 {
			continue
		}
		type wc struct {
			w string
			n int
		}
		var ws []wc
		for w, n := range words {
			ws = append(ws, wc{w, n})
		}
		sort.Slice(ws, func(i, j int) bool {
			if ws[i].n != ws[j].n {
				return ws[i].n > ws[j].n
			}
			return ws[i].w < ws[j].w
		})
		sg := Suggestion{Control: c.Key, Score: score, Words: []string{}, Category: cat}
		for i := 0; i < len(ws) && i < 4; i++ {
			sg.Words = append(sg.Words, ws[i].w)
		}
		out = append(out, sg)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	if len(out) > 12 {
		out = out[:12]
	}
	return out, nil
}

// BulkMapInput associa più requisiti a un controllo, senza togliere quelli già coperti.
type BulkMapInput struct {
	Control      string   `json:"control"`
	Requirements []string `json:"requirements"` // "dora:Art. 9"
	Coverage     string   `json:"coverage"`
}

// MapBulk aggiunge i requisiti al controllo (grc.admin) e restituisce il controllo.
func (s *Service) MapBulk(ctx context.Context, tx pgx.Tx, actor engine.Actor, in BulkMapInput) (*ControlView, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	if len(in.Requirements) == 0 {
		return nil, engine.Required("requirements", "scegli almeno un requisito")
	}
	id, err := s.controlRef(ctx, tx, in.Control)
	if err != nil {
		return nil, engine.NotFound("control", "control", "controllo %q inesistente", in.Control)
	}
	rows, err := tx.Query(ctx, `select f.key || ':' || r.ref, cr.coverage, coalesce(cr.note, '')
		from control_requirements cr join requirements r on r.id = cr.requirement_id
		join framework_versions v on v.id = r.version_id and v.status = 'published' join frameworks f on f.id = v.framework_id
		where cr.control_element_id = $1 order by f.key, r.ordinal`, id)
	if err != nil {
		return nil, err
	}
	var items []MappedInput
	var mi MappedInput
	if _, err := pgx.ForEachRow(rows, []any{&mi.Ref, &mi.Coverage, &mi.Note}, func() error {
		items = append(items, mi)
		return nil
	}); err != nil {
		return nil, err
	}
	have := map[string]bool{}
	for _, it := range items {
		have[normRef(it.Ref)] = true
	}
	for _, r := range in.Requirements {
		if !have[normRef(r)] {
			have[normRef(r)] = true
			items = append(items, MappedInput{Ref: r, Coverage: in.Coverage})
		}
	}
	return s.SetControlRequirements(ctx, tx, actor, id, items)
}
