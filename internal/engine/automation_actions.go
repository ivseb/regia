package engine

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine/notify"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Azioni delle automazioni (docs/sviluppo/automazioni.md). Sono del motore, come assign_role:
// non dipendono da un modulo. Agiscono con l'autorità della versione pubblicata del processo
// (chi pubblica ha process.admin), non con i permessi di chi causa l'evento; nelle esecuzioni
// a tempo l'attore è «Automazione» (nessun membro).
const (
	ActionSetField     = "set_field"
	ActionNotify       = "notify"
	ActionComment      = "add_comment"
	ActionCreateLinked = "create_linked"
	ActionWebhook      = "webhook"
)

var automationKinds = map[string]bool{ActionSetField: true, ActionNotify: true, ActionComment: true, ActionCreateLinked: true, ActionWebhook: true}

// automationSchemas descrivono le azioni all'editor (che le mostra con i suoi testi tradotti).
var automationSchemas = []ActionSchema{
	{Kind: ActionSetField, Name: "Imposta un campo", Description: "Imposta un campo dell'elemento: un valore (anche da modello), oggi ± N giorni, oppure lo svuota.",
		Fields: []ActionField{
			{Key: "field", Name: "Campo (chiave, o «titolo»)", Kind: "text", Required: true},
			{Key: "mode", Name: "Come", Kind: "text", Help: "value (predefinito), today, clear"},
			{Key: "value", Name: "Valore o modello", Kind: "text"},
			{Key: "days", Name: "Giorni da oggi", Kind: "text"},
		}},
	{Kind: ActionNotify, Name: "Manda una notifica", Description: "Notifica un membro, un gruppo, chi ha un ruolo sull'elemento o chi lo segue, con un testo da modello.",
		Fields: []ActionField{
			{Key: "message", Name: "Testo (modello)", Kind: "text", Required: true},
			{Key: "member", Name: "Membro", Kind: "member"},
			{Key: "group", Name: "Gruppo", Kind: "group"},
			{Key: "role", Name: "Ruolo sull'elemento", Kind: "role"},
			{Key: "followers", Name: "Chi segue l'elemento", Kind: "bool"},
		}},
	{Kind: ActionComment, Name: "Aggiungi un commento", Description: "Aggiunge un commento all'elemento, con un testo da modello.",
		Fields: []ActionField{{Key: "text", Name: "Testo (modello)", Kind: "text", Required: true}}},
	{Kind: ActionCreateLinked, Name: "Crea un elemento collegato", Description: "Crea un elemento di un tipo, con il titolo da modello, collegato a questo con una relazione.",
		Fields: []ActionField{
			{Key: "type", Name: "Tipo (chiave)", Kind: "text", Required: true},
			{Key: "title", Name: "Titolo (modello)", Kind: "text", Required: true},
			{Key: "relation", Name: "Relazione (chiave)", Kind: "text", Required: true},
			{Key: "direction", Name: "Verso", Kind: "text", Help: "out: questo → nuovo (predefinito); in: nuovo → questo"},
			{Key: "space", Name: "Spazio (chiave)", Kind: "text", Help: "Vuoto = lo spazio dell'elemento."},
		}},
	{Kind: ActionWebhook, Name: "Invia un webhook", Description: "Invia l'evento automation.rule a uno dei webhook configurati dall'amministratore.",
		Fields: []ActionField{{Key: "webhook", Name: "Webhook (nome)", Kind: "text", Required: true}}},
}

// SystemReader legge ogni elemento del tenant (escluse le bozze personali): lo usano il
// motore e i lavoratori per rileggere un elemento dopo un'azione di sistema.
var SystemReader = Actor{MemberID: "00000000-0000-0000-0000-000000000000", Permissions: []string{"*"}}

func (s *Service) runAutomation(ctx context.Context, tx pgx.Tx, actor Actor, el *Element, a Action) (string, error) {
	run, _ := RunOf(ctx)
	if run == nil {
		run = &RuleRun{}
	}
	f := run.Facts
	if f == nil {
		var err error
		if f, err = s.ElementFacts(ctx, tx, el); err != nil {
			return "", err
		}
	}
	switch a.Kind {
	case ActionSetField:
		return s.actSetField(ctx, tx, actor, el, a, f, run)
	case ActionNotify:
		return s.actNotify(ctx, tx, actor, el, a, f, run)
	case ActionComment:
		return s.actComment(ctx, tx, actor, el, a, f, run)
	case ActionCreateLinked:
		return s.actCreateLinked(ctx, tx, actor, el, a, f, run)
	case ActionWebhook:
		return s.actWebhook(ctx, tx, actor, el, a, f, run)
	}
	return "azione " + a.Kind + " non disponibile", nil
}

// --- Imposta un campo ---

func (s *Service) actSetField(ctx context.Context, tx pgx.Tx, actor Actor, el *Element, a Action, f Facts, run *RuleRun) (string, error) {
	key := a.Params["field"]
	mode := a.Params["mode"]
	if key == "titolo" {
		t := strings.TrimSpace(Render(a.Params["value"], el, f, run))
		if t == "" || t == el.Title {
			return "", ErrNoEffect
		}
		if _, err := s.update(ctx, tx, actor, el.ID, UpdateInput{Title: &t}, true); err != nil {
			return "", err
		}
		return "titolo: " + t, nil
	}
	defs, err := loadTypeFields(ctx, tx, el.TypeID)
	if err != nil {
		return "", err
	}
	var def *FieldDef
	for i := range defs {
		if defs[i].Key == key {
			def = &defs[i]
		}
	}
	if def == nil {
		return "", invalid(key, "il campo %q non è del tipo %s", key, el.TypeName)
	}
	var v any
	switch mode {
	case "clear":
		v = nil
	case "today":
		days, _ := strconv.Atoi(strings.TrimSpace(a.Params["days"]))
		day, err := time.Parse(time.DateOnly, todayOf(run))
		if err != nil {
			return "", err
		}
		day = day.AddDate(0, 0, days)
		if def.DataType == "datetime" {
			v = day.Format(time.RFC3339)
		} else {
			v = day.Format(time.DateOnly)
		}
	default:
		v, err = fieldInput(*def, Render(a.Params["value"], el, f, run))
		if err != nil {
			return "", err
		}
	}
	if v == nil && isEmptyValue(el.Fields[key]) {
		return "", ErrNoEffect
	}
	after, err := s.update(ctx, tx, actor, el.ID, UpdateInput{Fields: map[string]any{key: v}}, true)
	if err != nil {
		return "", err
	}
	if sameValue(el.Fields[key], after.Fields[key]) {
		return "", ErrNoEffect
	}
	el.Fields, el.Version = after.Fields, after.Version
	if v == nil {
		return def.Name + ": vuoto", nil
	}
	return fmt.Sprintf("%s: %v", def.Name, v), nil
}

// fieldInput converte il testo del modello nel valore che il campo accetta.
func fieldInput(d FieldDef, text string) (any, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil
	}
	one := func(s string) (any, error) {
		switch d.DataType {
		case "boolean":
			switch strings.ToLower(s) {
			case "sì", "si", "vero", "true", "yes", "1":
				return true, nil
			case "no", "falso", "false", "0":
				return false, nil
			}
			return nil, Required(d.Key, "serve sì o no")
		case "integer", "decimal":
			n, err := number(s)
			if err != nil {
				return nil, Required(d.Key, "serve un numero")
			}
			return n, nil
		}
		return s, nil
	}
	if !d.Multi {
		return one(text)
	}
	var out []any
	for _, p := range strings.Split(text, ",") {
		if p = strings.TrimSpace(p); p != "" {
			v, err := one(p)
			if err != nil {
				return nil, err
			}
			out = append(out, v)
		}
	}
	return out, nil
}

func todayOf(run *RuleRun) string {
	if run != nil && run.Today != "" {
		return run.Today
	}
	return time.Now().In(defaultLocation()).Format(time.DateOnly)
}

// --- Notifica ---

func (s *Service) actNotify(ctx context.Context, tx pgx.Tx, actor Actor, el *Element, a Action, f Facts, run *RuleRun) (string, error) {
	n := notify.Notice{Kind: notify.KindAutomation, ElementID: el.ID, ActorID: actor.MemberID, Followers: a.Params["followers"] == "true"}
	if a.Member != "" {
		n.Members = append(n.Members, a.Member)
	}
	if a.Group != "" {
		var id string
		err := tx.QueryRow(ctx, "select id::text from groups where lower(name) = lower($1)", a.Group).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return "gruppo " + a.Group + " non trovato", nil
		}
		if err != nil {
			return "", err
		}
		n.Groups = append(n.Groups, id)
	}
	if a.Role != "" {
		rows, err := tx.Query(ctx, `select sh.member_id::text, sh.group_id::text from stakeholders sh
			join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id
			where sh.element_id = $1 and r.key = $2`, el.ID, a.Role)
		if err != nil {
			return "", err
		}
		var m, g *string
		if _, err := pgx.ForEachRow(rows, []any{&m, &g}, func() error {
			if m != nil {
				n.Members = append(n.Members, *m)
			}
			if g != nil {
				n.Groups = append(n.Groups, *g)
			}
			return nil
		}); err != nil {
			return "", err
		}
	}
	msg := strings.TrimSpace(Render(a.Params["message"], el, f, run))
	if r := []rune(msg); len(r) > 1000 {
		msg = string(r[:1000])
	}
	n.Payload = map[string]any{"rule": run.Rule.Name, "message": msg}
	to, err := notify.Emit(ctx, tx, n)
	if err != nil {
		return "", err
	}
	if len(to) == 0 {
		return "nessun destinatario", nil
	}
	return fmt.Sprintf("notifica a %d persone", len(to)), nil
}

// --- Commento ---

func (s *Service) actComment(ctx context.Context, tx pgx.Tx, actor Actor, el *Element, a Action, f Facts, run *RuleRun) (string, error) {
	body := strings.TrimSpace(Render(a.Params["text"], el, f, run))
	if body == "" {
		return "", ErrNoEffect
	}
	if r := []rune(body); len(r) > 20000 {
		body = string(r[:20000])
	}
	id := db.NewID()
	if _, err := tx.Exec(ctx, `insert into comments (tenant_id, id, element_id, author_id, body, body_text)
		values (app_tenant(), $1, $2, $3, $4, $5)`, id, el.ID, nullable(actor.MemberID),
		map[string]any{"format": "markdown", "mentions": []any{}, "rule": run.Rule.Key}, body); err != nil {
		return "", err
	}
	if err := addActivity(ctx, tx, el.ID, actor.MemberID, "comment", "added", map[string]any{"comment_id": id}, nil); err != nil {
		return "", err
	}
	p := run.payload()
	p["element_id"] = el.ID
	if err := audit.Append(ctx, tx, audit.Event{Action: "comment.create", EntityType: "comment", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: p}); err != nil {
		return "", err
	}
	if _, err := notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindComment, ElementID: el.ID, ActorID: actor.MemberID,
		Followers: true, Payload: map[string]any{"comment_id": id, "excerpt": excerpt(body, 160)}}); err != nil {
		return "", err
	}
	return "commento aggiunto", nil
}

func excerpt(s string, n int) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}

// --- Elemento collegato ---

func (s *Service) actCreateLinked(ctx context.Context, tx pgx.Tx, actor Actor, el *Element, a Action, f Facts, run *RuleRun) (string, error) {
	var relID string
	err := tx.QueryRow(ctx, "select id::text from relation_types where key = $1", a.Params["relation"]).Scan(&relID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", invalid("relation", "relazione %q inesistente", a.Params["relation"])
	}
	if err != nil {
		return "", err
	}
	// Solo i tipi senza tabella di estensione: gli altri (richieste, rischi, incidenti…) hanno
	// dati del modulo che nascono con la loro pagina, non da una regola.
	var plain bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from element_types where key = $1 and extension is null)", a.Params["type"]).Scan(&plain); err != nil {
		return "", err
	}
	if !plain {
		return "", invalid("type", "il tipo %q ha dati propri del modulo: una regola non lo crea", a.Params["type"])
	}
	space := a.Params["space"]
	if space == "" {
		space = el.SpaceKey
	}
	title := strings.TrimSpace(Render(a.Params["title"], el, f, run))
	created, err := s.Create(ctx, tx, actor, CreateInput{SpaceKey: space, TypeKey: a.Params["type"], Title: title})
	if err != nil {
		return "", err
	}
	src, tgt := el.ID, created.ID
	if a.Params["direction"] == "in" {
		src, tgt = tgt, src
	}
	if _, err := tx.Exec(ctx, `insert into relations (tenant_id, type_id, source_id, target_id, created_by)
		values (app_tenant(), $1, $2, $3, $4) on conflict do nothing`, relID, src, tgt, nullable(actor.MemberID)); err != nil {
		return "", err
	}
	key := a.Params["relation"]
	if err := addActivity(ctx, tx, src, actor.MemberID, "relation", key, map[string]any{"target": tgt}, nil); err != nil {
		return "", err
	}
	if err := addActivity(ctx, tx, tgt, actor.MemberID, "relation", key+".inverse", map[string]any{"source": src}, nil); err != nil {
		return "", err
	}
	return "creato " + created.Key, nil
}

// --- Webhook ---

// WebhookEvent è l'evento che un'azione di regola invia al webhook scelto (fuori dal catalogo
// degli eventi a cui ci si iscrive: lo riceve solo il webhook indicato nella regola).
const WebhookEvent = "automation.rule"

func (s *Service) actWebhook(ctx context.Context, tx pgx.Tx, actor Actor, el *Element, a Action, f Facts, run *RuleRun) (string, error) {
	var id string
	err := tx.QueryRow(ctx, "select id::text from webhooks where lower(name) = lower($1) and active", a.Params["webhook"]).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "webhook " + a.Params["webhook"] + " non trovato o non attivo", nil
	}
	if err != nil {
		return "", err
	}
	data := run.payload()
	if run.Occurrence == "" {
		delete(data, "occurrence")
	}
	payload := map[string]any{"event": WebhookEvent, "element_id": el.ID, "activity_id": 0, "webhook_id": id,
		"added": data, "at": time.Now().UTC()}
	if actor.MemberID != "" {
		payload["actor_id"] = actor.MemberID
	}
	if _, err := tx.Exec(ctx, `insert into outbox_events (tenant_id, kind, payload) values (app_tenant(), 'webhook', $1)`, payload); err != nil {
		return "", err
	}
	return "webhook " + a.Params["webhook"], nil
}

// --- Validazione alla pubblicazione ---

// checkAutomation controlla i parametri di un'azione delle automazioni rispetto al tipo del processo.
func (s *Service) checkAutomation(ctx context.Context, tx pgx.Tx, typeID string, a Action) ([]string, error) {
	var out []string
	switch a.Kind {
	case ActionSetField:
		key := a.Params["field"]
		if key != "titolo" && typeID != "" {
			var ok bool
			if err := tx.QueryRow(ctx, `select exists (select 1 from type_fields tf join field_defs f on f.tenant_id = tf.tenant_id and f.id = tf.field_id
				where tf.type_id = $1 and tf.space_id is null and f.key = $2)`, typeID, key).Scan(&ok); err != nil {
				return nil, err
			}
			if !ok {
				out = append(out, fmt.Sprintf("il campo %q non è del tipo", key))
			}
		}
		switch a.Params["mode"] {
		case "", "value", "clear":
		case "today":
			if d := strings.TrimSpace(a.Params["days"]); d != "" {
				if _, err := strconv.Atoi(d); err != nil {
					out = append(out, "giorni non validi")
				}
			}
		default:
			out = append(out, fmt.Sprintf("modo %q non valido (value, today, clear)", a.Params["mode"]))
		}
		if (a.Params["mode"] == "" || a.Params["mode"] == "value") && strings.TrimSpace(a.Params["value"]) == "" {
			out = append(out, "manca il valore (per svuotare il campo scegli «svuota»)")
		}
	case ActionNotify:
		if a.Member == "" && a.Group == "" && a.Role == "" && a.Params["followers"] != "true" {
			out = append(out, "manca il destinatario")
		}
	case ActionCreateLinked:
		var typeOK, plain, relOK bool
		if err := tx.QueryRow(ctx, `select exists (select 1 from element_types where key = $1 and archived_at is null),
			exists (select 1 from element_types where key = $1 and extension is null),
			exists (select 1 from relation_types where key = $2)`, a.Params["type"], a.Params["relation"]).Scan(&typeOK, &plain, &relOK); err != nil {
			return nil, err
		}
		if !typeOK {
			out = append(out, fmt.Sprintf("tipo %q inesistente", a.Params["type"]))
		} else if !plain {
			out = append(out, fmt.Sprintf("il tipo %q ha dati propri del modulo: si crea solo dalla sua pagina", a.Params["type"]))
		}
		if !relOK {
			out = append(out, fmt.Sprintf("relazione %q inesistente", a.Params["relation"]))
		}
		if d := a.Params["direction"]; d != "" && d != "out" && d != "in" {
			out = append(out, "verso non valido (out, in)")
		}
		if sp := a.Params["space"]; sp != "" {
			var ok bool
			if err := tx.QueryRow(ctx, "select exists (select 1 from spaces where key = $1 and not archived)", sp).Scan(&ok); err != nil {
				return nil, err
			}
			if !ok {
				out = append(out, fmt.Sprintf("spazio %q inesistente", sp))
			}
		}
	case ActionWebhook:
		var ok bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from webhooks where lower(name) = lower($1))", a.Params["webhook"]).Scan(&ok); err != nil {
			return nil, err
		}
		if !ok {
			out = append(out, fmt.Sprintf("webhook %q inesistente", a.Params["webhook"]))
		}
	}
	return out, nil
}

// --- Fuso orario ---

// defaultLocation è il fuso predefinito dei tenant (le date senza ora valgono a mezzanotte qui).
func defaultLocation() *time.Location {
	if l, err := time.LoadLocation("Europe/Rome"); err == nil {
		return l
	}
	return time.UTC
}

// TenantLocation è il fuso del tenant della transazione (tenants.default_timezone).
func TenantLocation(ctx context.Context, tx pgx.Tx) *time.Location {
	var tz string
	if err := tx.QueryRow(ctx, `select coalesce((select default_timezone from tenants where id = app_tenant()), '')`).Scan(&tz); err == nil && tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			return l
		}
	}
	return defaultLocation()
}
