package reviews

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Fonti per «Precompila il questionario» (funzione AI «questionari», docs/sviluppo/ai.md). Il
// modulo raccoglie, con i permessi di chi compila, ciò che il modello può citare:
//   - le risposte confermate (valutazioni concluse con un esito) della stessa applicazione e, per
//     le domande che lì non hanno risposta, delle applicazioni simili (stessa classificazione dei
//     dati e stessa criticità, prima quelle con lo stesso hosting);
//   - i fatti dell'oggetto valutato e dell'applicazione (classe, dati trattati, dipendenze,
//     servizi in pipeline, vulnerabilità…).
// Pagine Knowledge e allegati li legge il livello AI (internal/ai/questionnaire.go). Il modulo
// non chiama mai il modello: AIContext è una lettura.

// Limiti delle fonti.
const (
	aiMaxQuestions   = 40 // domande per chiamata (le altre alla prossima)
	aiMaxPastReviews = 40 // valutazioni passate lette
	aiMaxPerQuestion = 3  // risposte passate per domanda
	aiMaxSimilarApps = 8
	aiMaxFacts       = 60
)

// AIQuestion è una domanda da proporre, con la definizione (per controllare il valore).
type AIQuestion struct {
	Question
	// Highlights: score | rules (vedi Definition.Highlights).
	Highlights []string
	// PrefillValue: il valore che il fatto di `prefill` darebbe (nil = nessuno).
	PrefillValue any
}

// AIPastAnswer è una risposta confermata di un'altra valutazione, già valida per la domanda.
type AIPastAnswer struct {
	Question       string // domanda di questa valutazione
	SourceQuestion string // domanda nella valutazione passata (per il link)
	ReviewKey      string
	ReviewTitle    string
	ReviewID       string
	AppKey         string
	AppTitle       string
	// Similar: vuoto per la stessa applicazione, altrimenti perché l'applicazione è simile.
	Similar   string
	Value     any
	Outcome   string
	DecidedAt time.Time
}

// AIFact è un fatto del grafo, con l'elemento da cui viene.
type AIFact struct {
	Key, Name string
	Values    []any
	RefKey    string
	RefTitle  string
}

// AIContext è ciò che serve per proporre le risposte di una sezione.
type AIContext struct {
	ReviewID, ReviewKey, ReviewTitle string
	TypeName, TemplateName           string
	SectionKey, SectionTitle         string
	Request                          Ref
	Impact                           *Ref
	App                              *Ref
	Questions                        []AIQuestion
	// Remaining: domande proponibili oltre il limite di una chiamata.
	Remaining  int
	Past       []AIPastAnswer
	Facts      []AIFact
	ElementIDs []string
}

func normLabel(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), " ")
}

// AIContext raccoglie le domande ancora senza risposta della sezione e le fonti che la persona
// può leggere. Serve poter compilare la sezione adesso.
func (s *Service) AIContext(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, section string) (*AIContext, error) {
	if actor.MemberID == "" {
		return nil, engine.ErrForbidden
	}
	_, r, err := s.resolve(ctx, tx, actor, ref, false)
	if err != nil {
		return nil, err
	}
	def, _, err := loadTemplateVersion(ctx, tx, r.TemplateID)
	if err != nil {
		return nil, err
	}
	var sec *Section
	for i := range def.Sections {
		if def.Sections[i].Key == section {
			sec = &def.Sections[i]
		}
	}
	if sec == nil {
		return nil, engine.InvalidCode("validation.reviews.section_unknown", "section", "sezione inesistente in questo modello")
	}
	p, err := s.permsOf(ctx, tx, actor, r)
	if err != nil {
		return nil, err
	}
	if !p.canAnswer(r.Stage, sec.Key, sec.FilledBy == "team") {
		if r.Stage == StageCompile || r.Stage == StageClarification || r.Stage == StageExam {
			return nil, engine.ErrForbidden
		}
		return nil, engine.InvalidCode("validation.reviews.not_fillable", "section", "la valutazione non si compila in questa fase")
	}
	facts, err := s.contextFacts(ctx, tx, r)
	if err != nil {
		return nil, err
	}
	answers := r.answerValues()
	ev := Evaluate(def, facts, answers)
	if st, ok := ev.Sections[sec.Key]; !ok || !st.Visible {
		return nil, engine.InvalidCode("validation.reviews.section_hidden", "section", "la sezione non si vede con le risposte date")
	}
	out := &AIContext{ReviewID: r.ID, ReviewKey: r.Key, ReviewTitle: r.Title, TypeName: r.TypeName, TemplateName: def.Name,
		SectionKey: sec.Key, SectionTitle: sec.Title, ElementIDs: []string{r.ID, r.RequestID}}
	hl := def.Highlights()
	for _, q := range sec.Questions {
		if !ev.Visible[q.Key] || q.Type == QAttachment {
			continue
		}
		if v, ok := answers[q.Key]; ok && q.complete(v) {
			continue
		}
		if len(out.Questions) == aiMaxQuestions {
			out.Remaining++
			continue
		}
		aq := AIQuestion{Question: q, Highlights: hl[q.Key]}
		if q.Prefill != "" {
			aq.PrefillValue = q.prefill(facts[strings.ToLower(q.Prefill)])
		}
		out.Questions = append(out.Questions, aq)
	}

	// Oggetto valutato e applicazione.
	if err := tx.QueryRow(ctx, `select e.id, s.key || '-' || e.number, e.title from elements e
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id where e.id = $1`, r.RequestID).
		Scan(&out.Request.ID, &out.Request.Key, &out.Request.Title); err != nil {
		return nil, err
	}
	var appID string
	if r.ImpactID != nil {
		imp, app := Ref{}, Ref{}
		if err := tx.QueryRow(ctx, `select e.id, s.key || '-' || e.number, e.title, ap.id, aps.key || '-' || ap.number, ap.title
			from elements e join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
			join ext_impact x on x.tenant_id = e.tenant_id and x.element_id = e.id
			join elements ap on ap.tenant_id = x.tenant_id and ap.id = x.application_id
			join spaces aps on aps.tenant_id = ap.tenant_id and aps.id = ap.space_id
			where e.id = $1`, *r.ImpactID).Scan(&imp.ID, &imp.Key, &imp.Title, &app.ID, &app.Key, &app.Title); err != nil {
			return nil, err
		}
		out.Impact = &imp
		out.ElementIDs = append(out.ElementIDs, imp.ID)
		if ok, err := s.eng.CanRead(ctx, tx, actor, app.ID); err != nil {
			return nil, err
		} else if ok {
			out.App, appID = &app, app.ID
			out.ElementIDs = append(out.ElementIDs, app.ID)
		}
	}
	if len(out.Questions) == 0 {
		return out, nil
	}

	// Fatti: dell'oggetto valutato (come li vedono le condizioni) e dell'applicazione.
	if err := s.aiFacts(ctx, tx, actor, out, facts, appID); err != nil {
		return nil, err
	}

	// Risposte confermate: stessa applicazione, poi applicazioni simili per le domande scoperte.
	if appID == "" {
		return out, nil
	}
	tplKey := ""
	if err := tx.QueryRow(ctx, `select key from questionnaire_templates where id = $1`, r.TemplateID).Scan(&tplKey); err != nil {
		return nil, err
	}
	found, err := s.aiPast(ctx, tx, actor, out, []string{appID}, map[string]string{appID: ""}, r.ID, tplKey, nil)
	if err != nil {
		return nil, err
	}
	var missing []string
	for _, q := range out.Questions {
		if found[q.Key] == 0 {
			missing = append(missing, q.Key)
		}
	}
	if len(missing) == 0 {
		return out, nil
	}
	similar, err := s.similarApps(ctx, tx, actor, appID)
	if err != nil || len(similar) == 0 {
		return out, err
	}
	ids := make([]string, 0, len(similar))
	for id := range similar {
		ids = append(ids, id)
	}
	if _, err := s.aiPast(ctx, tx, actor, out, ids, similar, r.ID, tplKey, missing); err != nil {
		return nil, err
	}
	return out, nil
}

// aiFacts aggiunge i fatti con un nome (quelli che l'editor dei processi descrive), senza i
// fatti di base e senza i ruoli (sono persone, non servono a rispondere).
func (s *Service) aiFacts(ctx context.Context, tx pgx.Tx, actor engine.Actor, out *AIContext, facts engine.Facts, appID string) error {
	// I nomi dei fatti sono configurazione (il catalogo dell'editor dei processi, riservato a chi
	// amministra i processi): si leggono con un attore di servizio e se ne usano solo chiave e
	// nome. I valori vengono dagli elementi che la persona può leggere.
	catalog := engine.Actor{MemberID: actor.MemberID, Permissions: []string{engine.PermProcessAdmin, "element.read"}}
	add := func(typeKey string, f engine.Facts, ref Ref, prefix string) error {
		infos, err := s.eng.FactsOfType(ctx, tx, catalog, typeKey)
		if err != nil {
			return err
		}
		for _, fi := range infos {
			if len(out.Facts) >= aiMaxFacts {
				return nil
			}
			switch fi.Key {
			case "tipo", "stato", "fase", "categoria", "spazio":
				continue
			}
			if strings.HasPrefix(fi.Key, "ruolo.") || fi.Source == "ruolo" {
				continue
			}
			vals := f[prefix+fi.Key]
			if len(vals) == 0 {
				continue
			}
			out.Facts = append(out.Facts, AIFact{Key: prefix + fi.Key, Name: fi.Name, Values: vals, RefKey: ref.Key, RefTitle: ref.Title})
		}
		return nil
	}
	if out.Impact != nil {
		if err := add(ImpactType, facts, *out.Impact, ""); err != nil {
			return err
		}
		if err := add(RequestType, facts, out.Request, "richiesta."); err != nil {
			return err
		}
	} else if err := add(RequestType, facts, out.Request, ""); err != nil {
		return err
	}
	if appID == "" {
		return nil
	}
	app, err := s.eng.Get(ctx, tx, actor, appID)
	if err != nil {
		return nil // l'applicazione non si legge più: niente fatti suoi
	}
	af, err := s.eng.ElementFacts(ctx, tx, app)
	if err != nil {
		return err
	}
	return add(app.TypeKey, af, *out.App, "")
}

// similarApps sono le applicazioni con la stessa classificazione dei dati e la stessa criticità
// (prima quelle con lo stesso hosting) che la persona può leggere: id → motivo.
func (s *Service) similarApps(ctx context.Context, tx pgx.Tx, actor engine.Actor, appID string) (map[string]string, error) {
	var dc, crit, host *string
	err := tx.QueryRow(ctx, `select data_classification, criticality, hosting from ext_application where element_id = $1`, appID).Scan(&dc, &crit, &host)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if dc == nil || crit == nil {
		return nil, nil
	}
	rows, err := tx.Query(ctx, `select a.element_id::text, coalesce(a.hosting, '') = coalesce($4, '') and $4 is not null
		from ext_application a join elements e on e.tenant_id = a.tenant_id and e.id = a.element_id and e.deleted_at is null
		where a.element_id <> $1 and a.data_classification = $2 and a.criticality = $3
		order by 2 desc, e.updated_at desc limit 40`, appID, *dc, *crit, host)
	if err != nil {
		return nil, err
	}
	type cand struct {
		id   string
		host bool
	}
	var cs []cand
	var c cand
	if _, err := pgx.ForEachRow(rows, []any{&c.id, &c.host}, func() error { cs = append(cs, c); return nil }); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, c := range cs {
		if len(out) == aiMaxSimilarApps {
			break
		}
		ok, err := s.eng.CanRead(ctx, tx, actor, c.id)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		why := "stessa classificazione dei dati e stessa criticità"
		if c.host {
			why += ", stesso hosting"
		}
		out[c.id] = why
	}
	return out, nil
}

// aiPast aggiunge le risposte confermate delle valutazioni concluse sulle applicazioni date
// (only: solo queste domande, nil = tutte). Restituisce quante risposte per domanda.
func (s *Service) aiPast(ctx context.Context, tx pgx.Tx, actor engine.Actor, out *AIContext, apps []string, why map[string]string,
	self, tplKey string, only []string) (map[string]int, error) {
	rows, err := tx.Query(ctx, `select x.element_id::text, s.key || '-' || e.number, e.title, x.answers, q.key, x.template_id::text,
		coalesce(x.outcome_label, x.outcome, ''), x.decided_at, ap.id::text, aps.key || '-' || ap.number, ap.title
		from ext_review x
		join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		join questionnaire_templates q on q.tenant_id = x.tenant_id and q.id = x.template_id
		join ext_impact i on i.tenant_id = x.tenant_id and i.element_id = x.impact_id
		join elements ap on ap.tenant_id = i.tenant_id and ap.id = i.application_id
		join spaces aps on aps.tenant_id = ap.tenant_id and aps.id = ap.space_id
		where i.application_id::text = any($1) and x.element_id <> $2 and x.outcome is not null and x.decided_at is not null
		order by x.decided_at desc limit $3`, apps, self, aiMaxPastReviews)
	if err != nil {
		return nil, err
	}
	type past struct {
		id, key, title       string
		answers              []byte
		tplKey, tplID        string
		outcome              string
		at                   time.Time
		appID, appKey, appTl string
	}
	var list []past
	var p past
	if _, err := pgx.ForEachRow(rows, []any{&p.id, &p.key, &p.title, &p.answers, &p.tplKey, &p.tplID, &p.outcome, &p.at, &p.appID, &p.appKey, &p.appTl},
		func() error { list = append(list, p); return nil }); err != nil {
		return nil, err
	}
	count := map[string]int{}
	for _, q := range out.Questions {
		for _, pa := range out.Past {
			if pa.Question == q.Key {
				count[q.Key]++
			}
		}
	}
	defs := map[string]*Definition{}
	want := func(k string) bool {
		if only == nil {
			return true
		}
		for _, o := range only {
			if o == k {
				return true
			}
		}
		return false
	}
	for _, pr := range list {
		ok, err := s.eng.CanRead(ctx, tx, actor, pr.id)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		def := defs[pr.tplID]
		if def == nil {
			if def, _, err = loadTemplateVersion(ctx, tx, pr.tplID); err != nil {
				return nil, err
			}
			defs[pr.tplID] = def
		}
		stored := map[string]storedAnswer{}
		if len(pr.answers) > 0 {
			if err := json.Unmarshal(pr.answers, &stored); err != nil {
				return nil, err
			}
		}
		for _, q := range out.Questions {
			if !want(q.Key) || count[q.Key] >= aiMaxPerQuestion {
				continue
			}
			src := ""
			if pr.tplKey == tplKey {
				if pq, _ := def.question(q.Key); pq != nil && pq.Type == q.Type {
					src = q.Key
				}
			}
			if src == "" {
				for _, sc := range def.Sections {
					for _, pq := range sc.Questions {
						if src == "" && pq.Type == q.Type && normLabel(pq.Label) == normLabel(q.Label) {
							src = pq.Key
						}
					}
				}
			}
			a, has := stored[src]
			if src == "" || !has || a.V == nil {
				continue
			}
			nv, err := q.NormalizeValue(a.V)
			if err != nil || nv == nil {
				continue // la risposta passata non vale per questa domanda (opzioni diverse)
			}
			count[q.Key]++
			out.Past = append(out.Past, AIPastAnswer{Question: q.Key, SourceQuestion: src, ReviewID: pr.id, ReviewKey: pr.key, ReviewTitle: pr.title,
				AppKey: pr.appKey, AppTitle: pr.appTl, Similar: why[pr.appID], Value: nv, Outcome: pr.outcome, DecidedAt: pr.at})
			if !containsStr(out.ElementIDs, pr.id) {
				out.ElementIDs = append(out.ElementIDs, pr.id)
			}
		}
	}
	return count, nil
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
