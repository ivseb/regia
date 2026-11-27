// Package tools è l'insieme di strumenti dell'AI (replica/ai.md §2): cerca, leggi, query,
// crea, aggiorna, collega, commenta, registra ore. Lo usano l'assistente di Regia e, così
// com'è, il server MCP dell'ondata 2.
//
// Regole:
//   - ogni strumento passa dai servizi esistenti (motore, collaborazione, lavoro, architettura)
//     con l'attore della persona, in una transazione del suo tenant: RLS ed engine.CanRead
//     valgono come per l'interfaccia, mai SQL diretto sugli elementi;
//   - le letture restituiscono il contenuto degli elementi delimitato come dato (Data), da non
//     seguire come istruzione;
//   - le scritture sui dati condivisi hanno due tempi: Prepare controlla e descrive la
//     proposta senza scrivere, Apply la esegue quando la persona conferma.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"regia/internal/ai/wiki"
	"regia/internal/engine"
	"regia/internal/modules/architecture"
	"regia/internal/modules/collab"
	"regia/internal/modules/finance"
	"regia/internal/modules/work"
	"regia/internal/platform/db"
)

// Services sono i servizi che gli strumenti usano.
type Services struct {
	DB     *db.DB
	Engine *engine.Service
	Collab *collab.Service
	Work   *work.Service
	Arch   *architecture.Service
}

// Env è chi usa lo strumento: il tenant e l'attore della persona.
type Env struct {
	TenantID string
	Actor    engine.Actor
}

// Ref è un elemento citato da uno strumento (per i link nella risposta).
type Ref struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

// Result è l'esito di uno strumento.
type Result struct {
	// Text è ciò che legge il modello: il contenuto degli elementi è delimitato con Data.
	Text string
	// Refs sono gli elementi citati, per i link nella risposta.
	Refs []Ref
	// Summary descrive la proposta alla persona (scritture).
	Summary string
	// Input è l'input normalizzato della proposta, da passare ad Apply (scritture).
	Input json.RawMessage
	// Data è l'esito dell'applicazione (es. l'elemento creato).
	Data any
}

// Tool è uno strumento.
type Tool struct {
	Name        string
	Description string
	Schema      map[string]any
	// Write: scrittura sui dati condivisi, solo come proposta da confermare.
	Write   bool
	prepare func(ctx context.Context, s *Services, tx pgx.Tx, env Env, in json.RawMessage) (*Result, error)
	apply   func(ctx context.Context, s *Services, env Env, in json.RawMessage) (*Result, error)
}

// InputError è un input dello strumento non valido: il modello lo legge e può correggersi.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

func bad(format string, a ...any) error { return &InputError{Msg: fmt.Sprintf(format, a...)} }

// List sono tutti gli strumenti, prima le letture.
func (s *Services) List() []Tool { return all }

// ReadOnly sono gli strumenti di sola lettura.
func (s *Services) ReadOnly() []Tool {
	var out []Tool
	for _, t := range all {
		if !t.Write {
			out = append(out, t)
		}
	}
	return out
}

// Get trova uno strumento per nome.
func (s *Services) Get(name string) (Tool, bool) {
	i := slices.IndexFunc(all, func(t Tool) bool { return t.Name == name })
	if i < 0 {
		return Tool{}, false
	}
	return all[i], true
}

// Prepare esegue una lettura, o prepara (senza scrivere) una proposta di scrittura.
func (s *Services) Prepare(ctx context.Context, env Env, name string, in json.RawMessage) (*Result, error) {
	t, ok := s.Get(name)
	if !ok {
		return nil, bad("strumento sconosciuto: %s", name)
	}
	if len(in) == 0 {
		in = json.RawMessage("{}")
	}
	var res *Result
	err := s.DB.InTenant(ctx, env.TenantID, db.TenantOpts{ReadOnly: true, Member: env.Actor.MemberID}, func(tx pgx.Tx) error {
		var err error
		res, err = t.prepare(ctx, s, tx, env, in)
		return err
	})
	return res, err
}

// Apply esegue una scrittura confermata dalla persona.
func (s *Services) Apply(ctx context.Context, env Env, name string, in json.RawMessage) (*Result, error) {
	t, ok := s.Get(name)
	if !ok || !t.Write {
		return nil, bad("strumento sconosciuto: %s", name)
	}
	return t.apply(ctx, s, env, in)
}

// ---------------------------------------------------------------------
// Contenuto come dato (difesa dal prompt injection)
// ---------------------------------------------------------------------

var dataTagRe = regexp.MustCompile(`(?i)<\s*/?\s*dati`)

// Data delimita il contenuto degli elementi: tutto ciò che sta fra <dati> e </dati> è un
// dato da leggere, non un'istruzione. Un tag simile dentro il contenuto si neutralizza.
func Data(origin, content string) string {
	content = dataTagRe.ReplaceAllStringFunc(content, func(m string) string { return "‹" + m[1:] })
	origin = strings.NewReplacer(`"`, "", "<", "", ">", "").Replace(origin)
	return "<dati origine=\"" + origin + "\">\n" + content + "\n</dati>"
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

func decode(in json.RawMessage, v any) error {
	if err := json.Unmarshal(in, v); err != nil {
		return bad("input non valido: %v", err)
	}
	return nil
}

func ref(el *engine.Element) Ref {
	key := el.Key
	if key == "" {
		key = el.ID
	}
	return Ref{ID: el.ID, Key: key, Title: el.Title, Type: el.TypeKey}
}

// ---------------------------------------------------------------------
// Letture: cerca, query, leggi
// ---------------------------------------------------------------------

type searchIn struct {
	Text  string `json:"testo"`
	Query string `json:"query"`
	Limit int    `json:"limite"`
}

func list(ctx context.Context, s *Services, tx pgx.Tx, env Env, q string, limit int, origin string) (*Result, error) {
	if limit <= 0 || limit > 25 {
		limit = 10
	}
	page, err := s.Engine.Search(ctx, tx, env.Actor, q, limit, "")
	if err != nil {
		var qe *engine.QueryError
		if errors.As(err, &qe) {
			return nil, bad("query non valida: %s", qe.Error())
		}
		return nil, err
	}
	res := &Result{}
	var b strings.Builder
	for _, el := range page.Items {
		r := ref(el)
		res.Refs = append(res.Refs, r)
		fmt.Fprintf(&b, "%s · %s · %s · %s\n", r.Key, el.TypeName, el.State, clip(el.Title, 200))
	}
	if len(page.Items) == 0 {
		b.WriteString("nessun elemento trovato\n")
	} else if page.Next != "" {
		b.WriteString("(altri risultati: restringi la ricerca)\n")
	}
	res.Text = Data(origin, strings.TrimSpace(b.String()))
	return res, nil
}

func prepareSearch(ctx context.Context, s *Services, tx pgx.Tx, env Env, in json.RawMessage) (*Result, error) {
	var x searchIn
	if err := decode(in, &x); err != nil {
		return nil, err
	}
	x.Text = strings.TrimSpace(x.Text)
	if x.Text == "" || utf8.RuneCountInString(x.Text) > 300 {
		return nil, bad("serve il testo da cercare (al massimo 300 caratteri)")
	}
	// Le parole si cercano come testo libero, senza la sintassi della query.
	q := strings.NewReplacer(":", " ", "=", " ", "(", " ", ")", " ", "\"", " ").Replace(x.Text)
	return list(ctx, s, tx, env, q, x.Limit, "ricerca")
}

func prepareQuery(ctx context.Context, s *Services, tx pgx.Tx, env Env, in json.RawMessage) (*Result, error) {
	var x searchIn
	if err := decode(in, &x); err != nil {
		return nil, err
	}
	x.Query = strings.TrimSpace(x.Query)
	if x.Query == "" || utf8.RuneCountInString(x.Query) > 1000 {
		return nil, bad("serve la query (al massimo 1000 caratteri)")
	}
	return list(ctx, s, tx, env, x.Query, x.Limit, "query")
}

type readIn struct {
	Element string `json:"elemento"`
}

func prepareRead(ctx context.Context, s *Services, tx pgx.Tx, env Env, in json.RawMessage) (*Result, error) {
	var x readIn
	if err := decode(in, &x); err != nil {
		return nil, err
	}
	el, err := s.Engine.Get(ctx, tx, env.Actor, strings.TrimSpace(x.Element))
	if errors.Is(err, engine.ErrNotFound) {
		return nil, bad("elemento %q inesistente o non visibile", x.Element)
	}
	if err != nil {
		return nil, err
	}
	r := ref(el)
	res := &Result{Refs: []Ref{r}}
	var b strings.Builder
	fmt.Fprintf(&b, "chiave: %s\ntipo: %s\nstato: %s\ntitolo: %s\n", r.Key, el.TypeName, el.State, el.Title)
	if el.DueAt != nil {
		fmt.Fprintf(&b, "scadenza: %s\n", el.DueAt.Format("2006-01-02"))
	}
	if len(el.Fields) > 0 {
		fj, _ := json.Marshal(el.Fields)
		fmt.Fprintf(&b, "campi: %s\n", clip(string(fj), 2000))
	}
	if strings.TrimSpace(el.BodyText) != "" {
		fmt.Fprintf(&b, "descrizione:\n%s\n", clip(el.BodyText, 6000))
	}
	if links, err := s.Engine.ElementLinks(ctx, tx, env.Actor, el); err == nil {
		if links.Parent != nil {
			fmt.Fprintf(&b, "genitore: %s %s\n", links.Parent.Key, clip(links.Parent.Title, 200))
			res.Refs = append(res.Refs, Ref{ID: links.Parent.ID, Key: links.Parent.Key, Title: links.Parent.Title, Type: links.Parent.Type})
		}
		for i, c := range links.Children {
			if i >= 20 {
				fmt.Fprintf(&b, "(altri %d figli)\n", len(links.Children)-20)
				break
			}
			fmt.Fprintf(&b, "figlio: %s %s (%s)\n", c.Key, clip(c.Title, 200), c.State)
			res.Refs = append(res.Refs, Ref{ID: c.ID, Key: c.Key, Title: c.Title, Type: c.Type})
		}
		for i, c := range links.Relations {
			if i >= 20 {
				break
			}
			fmt.Fprintf(&b, "relazione: %s %s %s\n", c.Relation, c.Key, clip(c.Title, 200))
			res.Refs = append(res.Refs, Ref{ID: c.ID, Key: c.Key, Title: c.Title, Type: c.Type})
		}
	} else if !errors.Is(err, engine.ErrForbidden) {
		return nil, err
	}
	if s.Collab != nil {
		if cs, err := s.Collab.Comments(ctx, tx, env.Actor, el.ID); err == nil {
			if len(cs) > 5 {
				cs = cs[len(cs)-5:]
			}
			for _, c := range cs {
				fmt.Fprintf(&b, "commento di %s del %s: %s\n", c.AuthorName, c.CreatedAt.Format("2006-01-02"), clip(c.Body, 500))
			}
		} else if !errors.Is(err, engine.ErrForbidden) {
			return nil, err
		}
	}
	res.Text = Data(r.Key, strings.TrimSpace(b.String()))
	return res, nil
}

// ---------------------------------------------------------------------
// Scritture (proposte): crea, aggiorna, collega, commenta, registra ore
// ---------------------------------------------------------------------

func normalized(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func readable(ctx context.Context, s *Services, tx pgx.Tx, env Env, key string) (*engine.Element, error) {
	el, err := s.Engine.Get(ctx, tx, env.Actor, strings.TrimSpace(key))
	if errors.Is(err, engine.ErrNotFound) {
		return nil, bad("elemento %q inesistente o non visibile", key)
	}
	return el, err
}

type createIn struct {
	Space  string `json:"spazio"`
	Type   string `json:"tipo"`
	Title  string `json:"titolo"`
	Body   string `json:"testo"`
	Parent string `json:"genitore"`
}

func prepareCreate(ctx context.Context, s *Services, tx pgx.Tx, env Env, in json.RawMessage) (*Result, error) {
	var x createIn
	if err := decode(in, &x); err != nil {
		return nil, err
	}
	x.Space, x.Type, x.Title, x.Parent = strings.ToUpper(strings.TrimSpace(x.Space)), strings.TrimSpace(x.Type), strings.TrimSpace(x.Title), strings.TrimSpace(x.Parent)
	if x.Type == "" {
		x.Type = "task"
	}
	switch {
	case x.Space == "":
		return nil, bad("serve la chiave dello spazio di lavoro (es. APP)")
	case x.Title == "" || utf8.RuneCountInString(x.Title) > 500:
		return nil, bad("serve un titolo (al massimo 500 caratteri)")
	case utf8.RuneCountInString(x.Body) > 20000:
		return nil, bad("testo troppo lungo")
	}
	res := &Result{Input: normalized(x)}
	if x.Parent != "" {
		p, err := readable(ctx, s, tx, env, x.Parent)
		if err != nil {
			return nil, err
		}
		res.Refs = append(res.Refs, ref(p))
		x.Parent = p.Key
		res.Input = normalized(x)
	}
	res.Summary = fmt.Sprintf("Creare %s «%s» nello spazio %s", x.Type, x.Title, x.Space)
	if x.Parent != "" {
		res.Summary += " sotto " + x.Parent
	}
	return res, nil
}

func applyCreate(ctx context.Context, s *Services, env Env, in json.RawMessage) (*Result, error) {
	var x createIn
	if err := decode(in, &x); err != nil {
		return nil, err
	}
	var el *engine.Element
	err := s.DB.InTenant(ctx, env.TenantID, db.TenantOpts{Member: env.Actor.MemberID}, func(tx pgx.Tx) error {
		var err error
		el, err = s.Work.CreateItem(ctx, tx, env.Actor, x.Space, work.ItemInput{Type: x.Type, Title: x.Title, Body: x.Body, ParentID: x.Parent})
		return err
	})
	if err != nil {
		return nil, err
	}
	r := ref(el)
	return &Result{Refs: []Ref{r}, Data: r, Summary: "Creato " + r.Key}, nil
}

type updateIn struct {
	Element string  `json:"elemento"`
	Title   *string `json:"titolo"`
	Body    *string `json:"testo"`
	Version int     `json:"versione,omitempty"`
}

func prepareUpdate(ctx context.Context, s *Services, tx pgx.Tx, env Env, in json.RawMessage) (*Result, error) {
	var x updateIn
	if err := decode(in, &x); err != nil {
		return nil, err
	}
	el, err := readable(ctx, s, tx, env, x.Element)
	if err != nil {
		return nil, err
	}
	if x.Title == nil && x.Body == nil {
		return nil, bad("indica il nuovo titolo o il nuovo testo")
	}
	if x.Title != nil && (strings.TrimSpace(*x.Title) == "" || utf8.RuneCountInString(*x.Title) > 500) {
		return nil, bad("titolo vuoto o troppo lungo")
	}
	if x.Body != nil && utf8.RuneCountInString(*x.Body) > 20000 {
		return nil, bad("testo troppo lungo")
	}
	// La versione letta adesso: se l'elemento cambia prima della conferma, la proposta non si applica.
	x.Element, x.Version = el.ID, el.Version
	var what []string
	if x.Title != nil {
		what = append(what, fmt.Sprintf("titolo «%s»", clip(*x.Title, 120)))
	}
	if x.Body != nil {
		what = append(what, "nuovo testo")
	}
	return &Result{Refs: []Ref{ref(el)}, Input: normalized(x),
		Summary: fmt.Sprintf("Aggiornare %s: %s", ref(el).Key, strings.Join(what, ", "))}, nil
}

func applyUpdate(ctx context.Context, s *Services, env Env, in json.RawMessage) (*Result, error) {
	var x updateIn
	if err := decode(in, &x); err != nil {
		return nil, err
	}
	var el *engine.Element
	err := s.DB.InTenant(ctx, env.TenantID, db.TenantOpts{Member: env.Actor.MemberID}, func(tx pgx.Tx) error {
		var err error
		el, err = s.Engine.Update(ctx, tx, env.Actor, x.Element, engine.UpdateInput{Version: x.Version, Title: x.Title, BodyText: x.Body})
		return err
	})
	if err != nil {
		return nil, err
	}
	r := ref(el)
	return &Result{Refs: []Ref{r}, Data: r, Summary: "Aggiornato " + r.Key}, nil
}

type linkIn struct {
	Type   string `json:"tipo"`
	Source string `json:"origine"`
	Target string `json:"destinazione"`
}

func prepareLink(ctx context.Context, s *Services, tx pgx.Tx, env Env, in json.RawMessage) (*Result, error) {
	var x linkIn
	if err := decode(in, &x); err != nil {
		return nil, err
	}
	x.Type = strings.TrimSpace(x.Type)
	src, err := readable(ctx, s, tx, env, x.Source)
	if err != nil {
		return nil, err
	}
	tgt, err := readable(ctx, s, tx, env, x.Target)
	if err != nil {
		return nil, err
	}
	types, err := s.Arch.RelationTypes(ctx, tx, env.Actor)
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(types, func(t architecture.RelationType) bool { return t.Key == x.Type && !t.Archived })
	if i < 0 {
		var keys []string
		for _, t := range types {
			if !t.Archived && t.Category != "" {
				keys = append(keys, t.Key+" ("+t.OutwardName+")")
			}
		}
		return nil, bad("tipo di relazione %q sconosciuto; tipi disponibili: %s", x.Type, strings.Join(keys, ", "))
	}
	x.Source, x.Target = src.ID, tgt.ID
	return &Result{Refs: []Ref{ref(src), ref(tgt)}, Input: normalized(x),
		Summary: fmt.Sprintf("Collegare %s %s %s", ref(src).Key, types[i].OutwardName, ref(tgt).Key)}, nil
}

func applyLink(ctx context.Context, s *Services, env Env, in json.RawMessage) (*Result, error) {
	var x linkIn
	if err := decode(in, &x); err != nil {
		return nil, err
	}
	err := s.DB.InTenant(ctx, env.TenantID, db.TenantOpts{Member: env.Actor.MemberID}, func(tx pgx.Tx) error {
		_, err := s.Arch.CreateRelation(ctx, tx, env.Actor, architecture.RelationInput{Type: x.Type, Source: x.Source, Target: x.Target})
		return err
	})
	if err != nil {
		return nil, err
	}
	return &Result{Summary: "Collegamento creato"}, nil
}

type commentIn struct {
	Element string `json:"elemento"`
	Text    string `json:"testo"`
}

func prepareComment(ctx context.Context, s *Services, tx pgx.Tx, env Env, in json.RawMessage) (*Result, error) {
	var x commentIn
	if err := decode(in, &x); err != nil {
		return nil, err
	}
	x.Text = strings.TrimSpace(x.Text)
	if x.Text == "" || utf8.RuneCountInString(x.Text) > 10000 {
		return nil, bad("serve il testo del commento (al massimo 10000 caratteri)")
	}
	el, err := readable(ctx, s, tx, env, x.Element)
	if err != nil {
		return nil, err
	}
	if !env.Actor.Has("comment.write") {
		return nil, bad("non hai il permesso di commentare")
	}
	x.Element = el.ID
	return &Result{Refs: []Ref{ref(el)}, Input: normalized(x),
		Summary: fmt.Sprintf("Commentare %s: «%s»", ref(el).Key, clip(x.Text, 300))}, nil
}

func applyComment(ctx context.Context, s *Services, env Env, in json.RawMessage) (*Result, error) {
	var x commentIn
	if err := decode(in, &x); err != nil {
		return nil, err
	}
	var c *collab.Comment
	err := s.DB.InTenant(ctx, env.TenantID, db.TenantOpts{Member: env.Actor.MemberID}, func(tx pgx.Tx) error {
		var err error
		c, err = s.Collab.AddComment(ctx, tx, env.Actor, x.Element, x.Text)
		return err
	})
	if err != nil {
		return nil, err
	}
	return &Result{Data: map[string]any{"comment_id": c.ID}, Summary: "Commento aggiunto"}, nil
}

type timeIn struct {
	Element  string `json:"elemento"`
	Duration string `json:"durata"`
	Date     string `json:"data"`
	Note     string `json:"nota"`
}

func prepareTime(ctx context.Context, s *Services, tx pgx.Tx, env Env, in json.RawMessage) (*Result, error) {
	var x timeIn
	if err := decode(in, &x); err != nil {
		return nil, err
	}
	x.Duration, x.Date, x.Note = strings.TrimSpace(x.Duration), strings.TrimSpace(x.Date), strings.TrimSpace(x.Note)
	m, err := work.ParseMinutes(x.Duration)
	if err != nil || m <= 0 || m > 24*60 {
		return nil, bad("durata non valida: usa per esempio 1h30, 2h o 45m (al massimo 24 ore)")
	}
	if utf8.RuneCountInString(x.Note) > 2000 {
		return nil, bad("nota troppo lunga")
	}
	el, err := readable(ctx, s, tx, env, x.Element)
	if err != nil {
		return nil, err
	}
	x.Element = el.ID
	when := "oggi"
	if x.Date != "" {
		when = "il " + x.Date
	}
	return &Result{Refs: []Ref{ref(el)}, Input: normalized(x),
		Summary: fmt.Sprintf("Registrare %s su %s %s", x.Duration, ref(el).Key, when)}, nil
}

func applyTime(ctx context.Context, s *Services, env Env, in json.RawMessage) (*Result, error) {
	var x timeIn
	if err := decode(in, &x); err != nil {
		return nil, err
	}
	var id string
	err := s.DB.InTenant(ctx, env.TenantID, db.TenantOpts{Member: env.Actor.MemberID}, func(tx pgx.Tx) error {
		var err error
		id, err = s.Work.LogTime(ctx, tx, env.Actor, x.Element, work.TimeInput{Duration: x.Duration, Date: x.Date, Text: x.Note})
		return err
	})
	if err != nil {
		return nil, err
	}
	// Il costo si calcola come dall'interfaccia: transazione di sistema, chi registra non vede tariffe.
	_ = s.DB.InTenant(ctx, env.TenantID, db.TenantOpts{Finance: true}, func(tx pgx.Tx) error {
		_, err := finance.CostWorkLogs(ctx, tx, env.Actor.MemberID, []string{id})
		return err
	})
	return &Result{Data: map[string]any{"work_log_id": id}, Summary: "Ore registrate"}, nil
}

// ---------------------------------------------------------------------
// Catalogo
// ---------------------------------------------------------------------

// obj è lo schema JSON di un oggetto. «required» c'è solo se non è vuoto: un elenco nullo
// («required»: null) non è JSON Schema valido e Gemini rifiuta la richiesta.
func obj(props map[string]any, required ...string) map[string]any {
	o := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}

func str(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }

var all = []Tool{
	{Name: "cerca", Description: "Cerca elementi di Regia (richieste, progetti, applicazioni, attività, rischi, documenti...) con parole libere. Restituisce chiave, tipo, stato e titolo.",
		Schema:  obj(map[string]any{"testo": str("parole da cercare"), "limite": map[string]any{"type": "integer", "description": "numero massimo di risultati (1-25)"}}, "testo"),
		prepare: prepareSearch},
	{Name: "query", Description: "Cerca elementi con il linguaggio di query di Regia, per esempio: «tipo: business_request fase: Triage,Valutazione urgenza: Alta aggiornata: -7g» oppure «-assegnatario: me ordina per: fase». Usa cerca per le parole libere.",
		Schema:  obj(map[string]any{"query": str("query nel linguaggio di Regia"), "limite": map[string]any{"type": "integer", "description": "numero massimo di risultati (1-25)"}}, "query"),
		prepare: prepareQuery},
	{Name: "leggi", Description: "Legge un elemento per chiave (DEM-12) o codice (APP-MOB): campi, descrizione, collegamenti e ultimi commenti.",
		Schema:  obj(map[string]any{"elemento": str("chiave o codice dell'elemento")}, "elemento"),
		prepare: prepareRead},
	{Name: "wiki", Description: "Legge la wiki personale della persona (le sue pagine di appunti su progetti, comitati, priorità e decisioni). Senza input elenca le pagine; con «pagina» (titolo) ne legge il testo; con «testo» cerca nelle pagine. Solo la wiki della persona, mai quella degli altri.",
		Schema:  obj(map[string]any{"pagina": str("titolo della pagina da leggere"), "testo": str("parole da cercare nelle pagine")}),
		prepare: prepareWiki},
	{Name: "crea", Write: true, Description: "Propone di creare un elemento di lavoro (task, story, bug, epic) in uno spazio. La persona conferma prima che sia creato.",
		Schema: obj(map[string]any{"spazio": str("chiave dello spazio di lavoro"), "tipo": str("task, story, bug o epic"), "titolo": str("titolo"),
			"testo": str("descrizione"), "genitore": str("chiave dell'elemento genitore, facoltativa")}, "spazio", "titolo"),
		prepare: prepareCreate, apply: applyCreate},
	{Name: "aggiorna", Write: true, Description: "Propone di cambiare titolo o testo di un elemento. La persona conferma prima che sia applicato.",
		Schema:  obj(map[string]any{"elemento": str("chiave dell'elemento"), "titolo": str("nuovo titolo"), "testo": str("nuovo testo")}, "elemento"),
		prepare: prepareUpdate, apply: applyUpdate},
	{Name: "collega", Write: true, Description: "Propone una relazione tra due elementi dell'architettura (per esempio dipendenza tra applicazioni). La persona conferma.",
		Schema:  obj(map[string]any{"tipo": str("chiave del tipo di relazione"), "origine": str("chiave o codice dell'origine"), "destinazione": str("chiave o codice della destinazione")}, "tipo", "origine", "destinazione"),
		prepare: prepareLink, apply: applyLink},
	{Name: "commenta", Write: true, Description: "Propone un commento su un elemento. La persona conferma prima che sia pubblicato.",
		Schema:  obj(map[string]any{"elemento": str("chiave dell'elemento"), "testo": str("testo del commento")}, "elemento", "testo"),
		prepare: prepareComment, apply: applyComment},
	{Name: "registra_ore", Write: true, Description: "Propone di registrare ore della persona su un elemento. La persona conferma.",
		Schema: obj(map[string]any{"elemento": str("chiave dell'elemento"), "durata": str("durata, per esempio 1h30, 2h, 45m"),
			"data": str("giorno AAAA-MM-GG, vuoto = oggi"), "nota": str("nota facoltativa")}, "elemento", "durata"),
		prepare: prepareTime, apply: applyTime},
}

// ---------------------------------------------------------------------
// Wiki personale (ondata 4): sola lettura, solo le pagine della persona
// ---------------------------------------------------------------------

type wikiIn struct {
	Page string `json:"pagina"`
	Text string `json:"testo"`
}

func prepareWiki(ctx context.Context, s *Services, tx pgx.Tx, env Env, in json.RawMessage) (*Result, error) {
	var x wikiIn
	if err := decode(in, &x); err != nil {
		return nil, err
	}
	if env.Actor.MemberID == "" {
		return nil, bad("la wiki è della persona: serve un membro")
	}
	x.Page, x.Text = strings.TrimSpace(x.Page), strings.TrimSpace(x.Text)
	if x.Page != "" {
		p, err := wiki.FindByTitle(ctx, tx, env.Actor.MemberID, x.Page)
		if errors.Is(err, wiki.ErrNotFound) {
			return nil, bad("nella wiki non c'è una pagina «%s»: chiama wiki senza input per l'elenco", clip(x.Page, 120))
		}
		if err != nil {
			return nil, err
		}
		head := fmt.Sprintf("titolo: %s\nversione: %d\nultima modifica: %s, %s\n\n", p.Title, p.Version, p.UpdatedBy, p.UpdatedAt.Format("2006-01-02"))
		return &Result{Text: Data("wiki: "+p.Title, head+p.Body)}, nil
	}
	pages, err := wiki.All(ctx, tx, env.Actor.MemberID)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	n := 0
	for _, p := range pages {
		if x.Text != "" && len(wiki.Pertinent([]wiki.Page{p}, x.Text, nil, 1<<20)) == 0 {
			continue
		}
		n++
		fmt.Fprintf(&b, "%s · v%d · %s · %s\n", p.Title, p.Version, p.UpdatedAt.Format("2006-01-02"), wiki.Excerpt(p.Body, 160))
	}
	if n == 0 {
		b.WriteString("nessuna pagina\n")
	}
	return &Result{Text: Data("wiki", strings.TrimSpace(b.String()))}, nil
}
