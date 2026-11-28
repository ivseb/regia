package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/projects"
	"regia/internal/platform/db"
)

// ---------------------------------------------------------------------
// aggiorna: titolo, testo, assegnatario, responsabile, campi del tipo, fase
// ---------------------------------------------------------------------

// updateIn è l'input di «aggiorna». Il modello indica le persone per nome o email e i campi per
// chiave o nome; Prepare li risolve, e l'input normalizzato che arriva ad Apply ha gli id dei
// membri, le chiavi vere dei campi, la fase di arrivo e la versione letta.
type updateIn struct {
	Element  string   `json:"elemento"`
	Title    *string  `json:"titolo,omitempty"`
	Body     *string  `json:"testo,omitempty"`
	Assignee *string  `json:"assegnatario,omitempty"`
	Manager  *string  `json:"responsabile,omitempty"`
	Fields   fieldsIn `json:"campi,omitempty"`
	Phase    string   `json:"fase,omitempty"`
	Comment  string   `json:"commento,omitempty"`
	Version  int      `json:"versione,omitempty"`
}

// fieldsIn sono i valori dei campi: un oggetto, accettato anche come testo JSON (alcuni modelli
// passano così gli oggetti annidati).
type fieldsIn map[string]any

func (f *fieldsIn) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		if strings.TrimSpace(s) == "" {
			*f = nil
			return nil
		}
		b = []byte(s)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return errors.New("«campi» va dato come oggetto, per esempio {\"priorita\": \"Alta\"}")
	}
	*f = m
	return nil
}

// Come si cambia l'assegnatario: voce di piano dei progetti, lavoro dei team e richieste.
const (
	assignPlanItem = "voce"
	assignWork     = "lavoro"
)

func assignKind(ctx context.Context, s *Services, tx pgx.Tx, el *engine.Element) (string, error) {
	if s.Projects != nil {
		if ok, err := s.Projects.IsPlanItem(ctx, tx, el.ID); err != nil || ok {
			return assignPlanItem, err
		}
	}
	if s.Work != nil {
		if ok, err := s.Work.Assignable(ctx, tx, el); err != nil || ok {
			return assignWork, err
		}
	}
	return "", nil
}

// managerRole è il ruolo del responsabile sul tipo (progetti e programmi), se il modulo c'è.
func managerRole(s *Services, el *engine.Element) string {
	if s.Projects == nil {
		return ""
	}
	return projects.ManagerRole(el.TypeKey)
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
	key := ref(el).Key
	x.Phase, x.Comment = strings.TrimSpace(x.Phase), strings.TrimSpace(x.Comment)
	if x.Title == nil && x.Body == nil && x.Assignee == nil && x.Manager == nil && len(x.Fields) == 0 && x.Phase == "" {
		return nil, bad("indica che cosa cambiare: titolo, testo, assegnatario, responsabile, campi o fase")
	}
	if x.Title != nil && (strings.TrimSpace(*x.Title) == "" || utf8.RuneCountInString(*x.Title) > 500) {
		return nil, bad("titolo vuoto o troppo lungo")
	}
	if x.Body != nil && utf8.RuneCountInString(*x.Body) > 20000 {
		return nil, bad("testo troppo lungo")
	}
	if utf8.RuneCountInString(x.Comment) > 10000 {
		return nil, bad("commento troppo lungo")
	}
	var defs []engine.FieldDef
	loaded := false
	loadDefs := func() ([]engine.FieldDef, error) {
		if !loaded {
			d, err := engine.TypeFieldDefs(ctx, tx, el.TypeID)
			if err != nil {
				return nil, err
			}
			defs, loaded = d, true
		}
		return defs, nil
	}

	var what []string
	if x.Title != nil {
		what = append(what, fmt.Sprintf("titolo «%s»", clip(*x.Title, 120)))
	}
	if x.Body != nil {
		what = append(what, "nuovo testo")
	}

	// Persone: sul lavoro dei team e sulle richieste il responsabile è l'assegnatario; su
	// progetti e programmi è il project (program) manager.
	kind, err := assignKind(ctx, s, tx, el)
	if err != nil {
		return nil, err
	}
	role := managerRole(s, el)
	if x.Manager != nil && role == "" && kind != "" {
		if x.Assignee != nil && !strings.EqualFold(strings.TrimSpace(*x.Assignee), strings.TrimSpace(*x.Manager)) {
			return nil, bad("su %s responsabile e assegnatario sono la stessa persona: indicane una sola", key)
		}
		x.Assignee, x.Manager = x.Manager, nil
	}
	if x.Assignee != nil {
		if kind == "" {
			if role != "" {
				return nil, bad("%s è un %s: la persona che lo guida è il responsabile, usa «responsabile»", key, el.TypeName)
			}
			d, err := loadDefs()
			if err != nil {
				return nil, err
			}
			return nil, bad("%s (%s) non ha un assegnatario%s", key, el.TypeName, memberFieldsHint(d))
		}
		id, name := "", "nessuno"
		if strings.TrimSpace(*x.Assignee) != "" {
			if id, name, err = resolveMember(ctx, tx, env, *x.Assignee); err != nil {
				return nil, err
			}
		}
		if kind == assignPlanItem {
			if ok, err := s.Projects.CanAssignItem(ctx, tx, env.Actor, el.ID); err != nil {
				return nil, err
			} else if !ok {
				return nil, bad("non hai il permesso di cambiare l'assegnatario di %s", key)
			}
		}
		x.Assignee = &id
		what = append(what, "assegnatario: "+name+before(memberName(ctx, tx, deref(el.AssigneeID)), name))
	}
	if x.Manager != nil {
		if role == "" {
			d, err := loadDefs()
			if err != nil {
				return nil, err
			}
			return nil, bad("%s (%s) non ha un responsabile da cambiare%s", key, el.TypeName, memberFieldsHint(d))
		}
		if strings.TrimSpace(*x.Manager) == "" {
			return nil, bad("il responsabile di %s è obbligatorio: indica una persona", key)
		}
		if ok, err := s.Projects.CanSetManager(ctx, tx, env.Actor, el); err != nil {
			return nil, err
		} else if !ok {
			return nil, bad("non hai il permesso di cambiare il responsabile di %s", key)
		}
		id, name, err := resolveMember(ctx, tx, env, *x.Manager)
		if err != nil {
			return nil, err
		}
		x.Manager = &id
		what = append(what, "responsabile: "+name+before(currentManager(ctx, tx, el.ID, role), name))
	}

	if len(x.Fields) > 0 {
		d, err := loadDefs()
		if err != nil {
			return nil, err
		}
		fields, labels, err := resolveFields(ctx, tx, env, el, d, x.Fields)
		if err != nil {
			return nil, err
		}
		x.Fields = fields
		what = append(what, labels...)
	}
	// Il motore controlla senza scrivere permesso, valori e obbligatori, come farà alla conferma.
	if x.Title != nil || x.Body != nil || len(x.Fields) > 0 || (x.Assignee != nil && kind == assignWork) {
		if err := s.Engine.CheckUpdate(ctx, tx, env.Actor, el, engine.UpdateInput{Version: el.Version, Fields: x.Fields}); err != nil {
			return nil, engineInputError(err, key)
		}
	}

	if x.Phase != "" {
		label, err := checkPhase(ctx, s, tx, env, el, &x)
		if err != nil {
			return nil, err
		}
		what = append(what, label)
	} else if x.Comment != "" {
		return nil, bad("il commento accompagna un passaggio di fase: per un commento e basta usa commenta")
	}

	// La versione letta adesso: se l'elemento cambia prima della conferma, la proposta non si applica.
	x.Element, x.Version = el.ID, el.Version
	return &Result{Refs: []Ref{ref(el)}, Input: normalized(x),
		Summary: fmt.Sprintf("Aggiornare %s: %s", key, strings.Join(what, "; "))}, nil
}

// checkPhase trova il passaggio verso la fase indicata fra quelli che la persona può avviare.
func checkPhase(ctx context.Context, s *Services, tx pgx.Tx, env Env, el *engine.Element, x *updateIn) (string, error) {
	key := ref(el).Key
	avail, err := s.Engine.Available(ctx, tx, env.Actor, el)
	if err != nil {
		return "", err
	}
	i := slices.IndexFunc(avail, func(t engine.TransitionView) bool {
		return strings.EqualFold(t.To, x.Phase) || (t.Label != "" && strings.EqualFold(t.Label, x.Phase))
	})
	if i < 0 {
		if len(avail) == 0 {
			return "", bad("da «%s» %s non ha passaggi di fase che tu possa avviare", el.State, key)
		}
		var names []string
		for _, t := range avail {
			names = append(names, t.To)
		}
		return "", bad("da «%s» %s non passa a «%s»; fasi possibili: %s", el.State, key, clip(x.Phase, 80), strings.Join(names, ", "))
	}
	tr := avail[i]
	var blocked []string
	for _, g := range tr.Gates {
		// I campi obbligatori della fase possono arrivare con la stessa proposta: li controlla il
		// motore alla conferma, dopo averli scritti.
		if g.Passed || (len(x.Fields) > 0 && (g.Key == "campi" || g.Key == "campi_esito")) {
			continue
		}
		blocked = append(blocked, g.Message)
	}
	if len(blocked) > 0 {
		return "", bad("il passaggio di %s a «%s» è bloccato: %s", key, tr.To, strings.Join(blocked, "; "))
	}
	if tr.CommentRequired && x.Comment == "" {
		return "", bad("il passaggio di %s a «%s» chiede un commento: indicalo in «commento»", key, tr.To)
	}
	x.Phase = tr.To
	label := fmt.Sprintf("fase: %s → %s", el.State, tr.To)
	if x.Comment != "" {
		label += fmt.Sprintf(" (commento «%s»)", clip(x.Comment, 200))
	}
	return label, nil
}

func applyUpdate(ctx context.Context, s *Services, env Env, in json.RawMessage) (*Result, error) {
	var x updateIn
	if err := decode(in, &x); err != nil {
		return nil, err
	}
	var el *engine.Element
	err := s.DB.InTenant(ctx, env.TenantID, db.TenantOpts{Member: env.Actor.MemberID}, func(tx pgx.Tx) error {
		var err error
		if el, err = s.Engine.Get(ctx, tx, env.Actor, x.Element); err != nil {
			return err
		}
		if el.Version != x.Version {
			return engine.ErrConflict
		}
		// Prima titolo, testo e campi (Update blocca la riga e ricontrolla la versione), poi le
		// persone e per ultima la fase, che vede i campi appena scritti. I permessi li
		// ricontrollano i servizi, con l'attore della persona.
		if x.Title != nil || x.Body != nil || len(x.Fields) > 0 {
			if el, err = s.Engine.Update(ctx, tx, env.Actor, el.ID, engine.UpdateInput{Version: el.Version, Title: x.Title, BodyText: x.Body, Fields: x.Fields}); err != nil {
				return err
			}
		}
		if x.Assignee != nil {
			kind, err := assignKind(ctx, s, tx, el)
			if err != nil {
				return err
			}
			switch kind {
			case assignPlanItem:
				if _, err := s.Projects.UpdateItem(ctx, tx, env.Actor, el.ID, projects.ItemPatch{AssigneeID: x.Assignee}); err != nil {
					return err
				}
			case assignWork:
				if err := s.Engine.CheckUpdate(ctx, tx, env.Actor, el, engine.UpdateInput{}); err != nil {
					return err
				}
				if _, err := s.Work.SetAssignee(ctx, tx, env.Actor, el.ID, *x.Assignee, 0); err != nil {
					return err
				}
			default:
				return bad("%s non ha un assegnatario", ref(el).Key)
			}
		}
		if x.Manager != nil {
			if s.Projects == nil {
				return bad("%s non ha un responsabile da cambiare", ref(el).Key)
			}
			if _, err := s.Projects.SetManager(ctx, tx, env.Actor, el.ID, *x.Manager, 0); err != nil {
				return err
			}
		}
		if x.Phase != "" {
			if _, err := s.Engine.Transition(ctx, tx, env.Actor, el.ID, x.Phase, x.Comment, engine.TransitionOpts{}); err != nil {
				return err
			}
		}
		el, err = s.Engine.Get(ctx, tx, env.Actor, el.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	r := ref(el)
	return &Result{Refs: []Ref{r}, Data: r, Summary: "Aggiornato " + r.Key}, nil
}

// engineInputError rende un rifiuto del motore leggibile dal modello, che può correggersi.
func engineInputError(err error, key string) error {
	var ve *engine.ValidationError
	switch {
	case errors.Is(err, engine.ErrForbidden):
		return bad("non hai il permesso di modificare %s", key)
	case errors.As(err, &ve):
		return bad("%s", ve.Error())
	}
	return err
}

// ---------------------------------------------------------------------
// Persone
// ---------------------------------------------------------------------

var meWords = []string{"me", "io", "me stesso", "me stessa", "myself"}

type person struct{ id, name, email string }

// resolveMember trova un solo membro attivo per id, nome o email (anche un pezzo del nome, se
// il nome intero non c'è). Nessuno o più di uno: un errore che il modello legge e corregge.
func resolveMember(ctx context.Context, tx pgx.Tx, env Env, who string) (string, string, error) {
	who = strings.TrimSpace(who)
	if slices.Contains(meWords, strings.ToLower(who)) {
		if env.Actor.MemberID == "" {
			return "", "", bad("«%s» vale solo per una persona", who)
		}
		return env.Actor.MemberID, memberName(ctx, tx, env.Actor.MemberID), nil
	}
	if who == "" || utf8.RuneCountInString(who) > 200 {
		return "", "", bad("indica la persona con il nome o l'email")
	}
	find := func(where string, arg string) ([]person, error) {
		rows, err := tx.Query(ctx, `select id::text, display_name, coalesce(email, '') from members
			where status = 'active' and (`+where+`) order by display_name limit 6`, arg)
		if err != nil {
			return nil, err
		}
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (person, error) {
			var p person
			return p, r.Scan(&p.id, &p.name, &p.email)
		})
	}
	found, err := find(`id::text = lower($1) or lower(display_name) = lower($1) or lower(email) = lower($1)`, who)
	if err == nil && len(found) == 0 {
		like := "%" + strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`).Replace(who) + "%"
		found, err = find(`display_name ilike $1 or email ilike $1`, like)
	}
	if err != nil {
		return "", "", err
	}
	switch len(found) {
	case 0:
		return "", "", bad("nessuna persona attiva corrisponde a «%s»: usa il nome completo o l'email", clip(who, 80))
	case 1:
		return found[0].id, found[0].name, nil
	}
	var names []string
	for i, p := range found {
		if i == 5 {
			names = append(names, "…")
			break
		}
		n := p.name
		if p.email != "" {
			n += " <" + p.email + ">"
		}
		names = append(names, n)
	}
	return "", "", bad("«%s» corrisponde a più persone (%s): indica il nome completo o l'email", clip(who, 80), strings.Join(names, ", "))
}

func memberName(ctx context.Context, tx pgx.Tx, id string) string {
	if id == "" {
		return ""
	}
	var name string
	_ = tx.QueryRow(ctx, `select display_name from members where id::text = $1`, id).Scan(&name)
	return name
}

func currentManager(ctx context.Context, tx pgx.Tx, elementID, role string) string {
	shs, err := engine.Stakeholders(ctx, tx, elementID)
	if err != nil {
		return ""
	}
	var names []string
	for _, sh := range shs {
		if sh.Role == role && sh.Name != "" {
			names = append(names, sh.Name)
		}
	}
	return strings.Join(names, ", ")
}

// before è «(prima: X)» quando il valore cambia.
func before(old, now string) string {
	if old == "" {
		old = "nessuno"
	}
	if old == now {
		return " (è già così)"
	}
	return " (prima: " + old + ")"
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// memberFieldsHint suggerisce i campi persona del tipo, se ce ne sono.
func memberFieldsHint(defs []engine.FieldDef) string {
	var keys []string
	for _, d := range defs {
		if d.DataType == "member" && !d.Archived {
			keys = append(keys, fmt.Sprintf("%s («%s»)", d.Key, d.Name))
		}
	}
	if len(keys) == 0 {
		return ""
	}
	return "; i campi persona del tipo, da indicare in «campi», sono: " + strings.Join(keys, ", ")
}

// ---------------------------------------------------------------------
// Campi del tipo
// ---------------------------------------------------------------------

// resolveFields porta le chiavi (o i nomi) dei campi alle chiavi vere del tipo e i valori alla
// forma del motore: persone e opzioni degli elenchi diventano id. Restituisce anche le righe del
// riepilogo («Priorità: Alta»). Il resto della validazione è del motore (CheckUpdate).
func resolveFields(ctx context.Context, tx pgx.Tx, env Env, el *engine.Element, defs []engine.FieldDef, in map[string]any) (map[string]any, []string, error) {
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := map[string]any{}
	var labels []string
	for _, k := range keys {
		i := slices.IndexFunc(defs, func(d engine.FieldDef) bool {
			return !d.Archived && (d.Key == k || strings.EqualFold(d.Key, strings.TrimSpace(k)) || strings.EqualFold(d.Name, strings.TrimSpace(k)))
		})
		if i < 0 {
			return nil, nil, bad("%s (%s) non ha il campo «%s»; %s", ref(el).Key, el.TypeName, clip(k, 80), fieldList(defs))
		}
		d := defs[i]
		if _, dup := out[d.Key]; dup {
			return nil, nil, bad("il campo «%s» è indicato due volte", d.Name)
		}
		v, label, err := resolveValue(ctx, tx, env, d, in[k])
		if err != nil {
			return nil, nil, err
		}
		out[d.Key] = v
		labels = append(labels, d.Name+": "+label)
	}
	return out, labels, nil
}

func fieldList(defs []engine.FieldDef) string {
	var keys []string
	for _, d := range defs {
		if !d.Archived {
			keys = append(keys, fmt.Sprintf("%s («%s», %s)", d.Key, d.Name, d.DataType))
		}
	}
	if len(keys) == 0 {
		return "il tipo non ha campi"
	}
	if len(keys) > 40 {
		keys = append(keys[:40], "…")
	}
	return "campi del tipo: " + strings.Join(keys, ", ")
}

func emptyValue(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case []any:
		return len(x) == 0
	}
	return false
}

// resolveValue normalizza un valore per il campo (uno o, per i campi multipli, un elenco).
func resolveValue(ctx context.Context, tx pgx.Tx, env Env, d engine.FieldDef, v any) (any, string, error) {
	if emptyValue(v) {
		return nil, "vuoto", nil
	}
	list, isList := v.([]any)
	if !isList {
		list = []any{v}
	}
	if isList && !d.Multi && len(list) > 1 {
		return nil, "", bad("il campo «%s» accetta un solo valore", d.Name)
	}
	vals := make([]any, 0, len(list))
	var shown []string
	for _, one := range list {
		val, label, err := resolveOne(ctx, tx, env, d, one)
		if err != nil {
			return nil, "", err
		}
		vals = append(vals, val)
		shown = append(shown, label)
	}
	if !d.Multi {
		return vals[0], shown[0], nil
	}
	return vals, strings.Join(shown, ", "), nil
}

func resolveOne(ctx context.Context, tx pgx.Tx, env Env, d engine.FieldDef, v any) (any, string, error) {
	s, isText := v.(string)
	s = strings.TrimSpace(s)
	switch d.DataType {
	case "member":
		if !isText {
			return nil, "", bad("per «%s» serve una persona (nome o email)", d.Name)
		}
		id, name, err := resolveMember(ctx, tx, env, s)
		return id, name, err
	case "enum", "version":
		if !isText || d.OptionSetID == nil {
			return nil, "", bad("per «%s» serve il nome di un'opzione", d.Name)
		}
		rows, err := tx.Query(ctx, `select id::text, name from options where option_set_id = $1 and not archived order by ordinal, name`, *d.OptionSetID)
		if err != nil {
			return nil, "", err
		}
		opts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) ([2]string, error) {
			var o [2]string
			return o, r.Scan(&o[0], &o[1])
		})
		if err != nil {
			return nil, "", err
		}
		var names []string
		for _, o := range opts {
			if o[0] == strings.ToLower(s) || strings.EqualFold(o[1], s) {
				return o[0], o[1], nil
			}
			names = append(names, o[1])
		}
		return nil, "", bad("«%s» non è un'opzione di «%s»; opzioni: %s", clip(s, 80), d.Name, strings.Join(names, ", "))
	case "boolean":
		if b, ok := v.(bool); ok {
			return b, yesNo(b), nil
		}
		switch strings.ToLower(s) {
		case "sì", "si", "vero", "true", "yes":
			return true, yesNo(true), nil
		case "no", "falso", "false":
			return false, yesNo(false), nil
		}
		return nil, "", bad("per «%s» serve sì o no", d.Name)
	}
	if isText {
		return s, "«" + clip(s, 80) + "»", nil
	}
	if m, ok := v.(map[string]any); ok {
		return v, strings.TrimSpace(fmt.Sprintf("%v %v", m["amount"], m["currency"])), nil
	}
	return v, fmt.Sprint(v), nil
}

func yesNo(b bool) string {
	if b {
		return "sì"
	}
	return "no"
}
