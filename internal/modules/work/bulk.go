package work

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Comandi in blocco (piano, passo 18; replica/work.md «Tempo e team»): la stessa azione su
// più elementi di lavoro, dalla board, dal backlog e dalle liste. Ogni elemento è una
// unità: se un comando è bloccato (gate del processo, ruoli, motivazione, campo non valido)
// quell'elemento resta com'era, con il motivo, e gli altri proseguono. Il tutto in una
// transazione, con audit, anteprima (dry_run, annullata a fine corsa) e annullamento
// dell'ultimo comando di ciascuno.

// Tipi di comando.
const (
	BulkPhase  = "phase"  // value = nome della fase
	BulkAssign = "assign" // value = id del membro ("" = nessuno)
	BulkField  = "field"  // field = chiave del campo, raw = valore (vuoto = svuota)
	BulkLabels = "labels" // add e remove
	BulkSprint = "sprint" // value = chiave dello sprint ("" o "backlog" = backlog)
	BulkDelete = "delete"
)

// BulkCommand è un comando. Label è il testo per il riepilogo (nome della persona, dello sprint…).
type BulkCommand struct {
	Kind    string   `json:"kind"`
	Value   string   `json:"value"`
	Field   string   `json:"field"`
	Raw     any      `json:"raw"`
	Add     []string `json:"add"`
	Remove  []string `json:"remove"`
	Comment string   `json:"comment"` // motivazione, per i passaggi che la chiedono
	Label   string   `json:"label"`
}

// BulkInput: elementi (id o chiavi) e comandi.
type BulkInput struct {
	IDs          []string      `json:"ids"`
	Commands     []BulkCommand `json:"commands"`
	DryRun       bool          `json:"dry_run"`
	AllOrNothing bool          `json:"all_or_nothing"` // un solo bloccato annulla tutto
	Text         string        `json:"text"`           // il comando scritto, per il riepilogo
}

// BulkItem è l'esito su un elemento.
type BulkItem struct {
	ID      string   `json:"id"`
	Key     string   `json:"key"`
	Title   string   `json:"title"`
	Status  string   `json:"status"` // ok | unchanged | blocked
	Changes []string `json:"changes,omitempty"`
	Reason  string   `json:"reason,omitempty"`
}

// BulkResult è l'esito del comando.
type BulkResult struct {
	DryRun    bool       `json:"dry_run"`
	Applied   int        `json:"applied"`
	Unchanged int        `json:"unchanged"`
	Blocked   int        `json:"blocked"`
	Items     []BulkItem `json:"items"`
	Summary   string     `json:"summary"`
	CommandID string     `json:"command_id,omitempty"`
}

type bulkChange struct {
	Element string `json:"element"`
	Key     string `json:"key"`
	Kind    string `json:"kind"`
	Field   string `json:"field,omitempty"`
	Before  any    `json:"before"`
	After   any    `json:"after"`
}

const maxBulk = 500

// kindOrder: l'ordine in cui si applicano i comandi di un elemento (prima chi assegna, poi
// i campi, per soddisfare i gate di una fase che chiede un assegnatario o un campo).
var kindOrder = map[string]int{BulkAssign: 0, BulkField: 1, BulkLabels: 2, BulkSprint: 3, BulkPhase: 4, BulkDelete: 5}

// Bulk esegue i comandi sugli elementi.
func (s *Service) Bulk(ctx context.Context, tx pgx.Tx, actor engine.Actor, in BulkInput) (*BulkResult, error) {
	if !actor.Has("element.write") {
		return nil, engine.ErrForbidden
	}
	if len(in.IDs) == 0 {
		return nil, engine.Required("ids", "scegli almeno un elemento")
	}
	if len(in.IDs) > maxBulk {
		return nil, engine.InvalidParams("validation.work.bulk_too_many", "ids", map[string]any{"max": maxBulk}, "al massimo %d elementi per volta", maxBulk)
	}
	if len(in.Commands) == 0 {
		return nil, engine.Required("commands", "scrivi almeno un comando")
	}
	cmds := slices.Clone(in.Commands)
	for _, c := range cmds {
		if _, ok := kindOrder[c.Kind]; !ok {
			return nil, engine.InvalidValue("commands", "comando sconosciuto: %q", c.Kind)
		}
		if c.Kind == BulkDelete && len(cmds) > 1 {
			return nil, engine.InvalidCode("validation.work.bulk_delete_alone", "commands", "«elimina» va da solo: non si combina con altri comandi")
		}
		if c.Kind == BulkField && strings.TrimSpace(c.Field) == "" {
			return nil, engine.Required("commands", "indica il campo da impostare")
		}
		if c.Kind == BulkPhase && strings.TrimSpace(c.Value) == "" {
			return nil, engine.Required("commands", "indica la fase")
		}
	}
	sort.SliceStable(cmds, func(i, j int) bool { return kindOrder[cmds[i].Kind] < kindOrder[cmds[j].Kind] })

	// Un punto di salvataggio esterno: l'anteprima e «tutto o niente» lo annullano.
	outer, err := tx.Begin(ctx)
	if err != nil {
		return nil, err
	}
	res := &BulkResult{DryRun: in.DryRun, Items: make([]BulkItem, 0, len(in.IDs))}
	var changes []bulkChange
	seen := map[string]bool{}
	for _, ref := range in.IDs {
		ref = strings.TrimSpace(ref)
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		item, ch := s.bulkOne(ctx, outer, actor, ref, cmds)
		switch item.Status {
		case "ok":
			res.Applied++
			changes = append(changes, ch...)
		case "unchanged":
			res.Unchanged++
		default:
			res.Blocked++
		}
		res.Items = append(res.Items, item)
	}
	res.Summary = bulkSummary(cmds, res.Applied)
	if in.DryRun || (in.AllOrNothing && res.Blocked > 0) {
		if err := outer.Rollback(ctx); err != nil {
			return nil, err
		}
		if in.AllOrNothing && res.Blocked > 0 && !in.DryRun {
			res.Applied = 0
			return res, engine.InvalidParams("validation.work.bulk_blocked", "commands", map[string]any{"count": res.Blocked}, "%d elementi bloccati: non è stato cambiato niente", res.Blocked)
		}
		return res, nil
	}
	if res.Applied == 0 {
		return res, outer.Commit(ctx)
	}
	id := db.NewID()
	rawCmds, _ := json.Marshal(cmds)
	rawChanges, _ := json.Marshal(changes)
	summary := bulkSummary(cmds, res.Applied)
	if _, err := outer.Exec(ctx, `insert into bulk_commands (tenant_id, id, member_id, summary, commands, changes)
		values (app_tenant(), $1, $2::uuid, $3, $4, $5)`, id, actor.MemberID, summary, rawCmds, rawChanges); err != nil {
		_ = outer.Rollback(ctx)
		return nil, err
	}
	if err := audit.Append(ctx, outer, audit.Event{Action: "work.bulk", EntityType: "bulk_command", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"summary": summary, "text": in.Text, "applied": res.Applied, "blocked": res.Blocked, "unchanged": res.Unchanged}}); err != nil {
		_ = outer.Rollback(ctx)
		return nil, err
	}
	res.CommandID = id
	return res, outer.Commit(ctx)
}

func bulkSummary(cmds []BulkCommand, n int) string {
	parts := make([]string, 0, len(cmds))
	for _, c := range cmds {
		parts = append(parts, describeBulk(c))
	}
	what := "elemento"
	if n != 1 {
		what = "elementi"
	}
	return fmt.Sprintf("%s su %d %s", strings.Join(parts, ", "), n, what)
}

func describeBulk(c BulkCommand) string {
	lab := firstNonEmpty(c.Label, c.Value)
	switch c.Kind {
	case BulkPhase:
		return "fase " + lab
	case BulkAssign:
		if strings.TrimSpace(c.Value) == "" {
			return "senza assegnatario"
		}
		return "assegna a " + lab
	case BulkField:
		if c.Raw == nil || fmt.Sprint(c.Raw) == "" {
			return "svuota " + firstNonEmpty(c.Label, c.Field)
		}
		return firstNonEmpty(c.Label, c.Field) + " " + fmt.Sprint(c.Raw)
	case BulkLabels:
		var p []string
		for _, a := range c.Add {
			p = append(p, "+"+a)
		}
		for _, r := range c.Remove {
			p = append(p, "-"+r)
		}
		return "etichette " + strings.Join(p, " ")
	case BulkSprint:
		if v := strings.TrimSpace(c.Value); v == "" || strings.EqualFold(v, "backlog") {
			return "sprint: backlog"
		}
		return "sprint " + lab
	case BulkDelete:
		return "elimina"
	}
	return c.Kind
}

// bulkOne applica i comandi a un elemento in un punto di salvataggio proprio.
func (s *Service) bulkOne(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, cmds []BulkCommand) (BulkItem, []bulkChange) {
	item := BulkItem{ID: ref, Key: ref, Status: "blocked"}
	el, err := s.workItem(ctx, tx, actor, ref, assignable...)
	if err != nil {
		item.Reason = reasonOf(err)
		return item, nil
	}
	item.ID, item.Key, item.Title = el.ID, el.Key, el.Title
	sp, err := tx.Begin(ctx)
	if err != nil {
		item.Reason = reasonOf(err)
		return item, nil
	}
	var changes []bulkChange
	for _, c := range cmds {
		ch, desc, err := s.bulkApply(ctx, sp, actor, el, c)
		if err != nil {
			_ = sp.Rollback(ctx)
			item.Reason, item.Changes = reasonOf(err), nil
			return item, nil
		}
		if desc != "" {
			item.Changes = append(item.Changes, desc)
			changes = append(changes, ch...)
		}
		// Ricarica: ogni comando può avere cambiato versione e fase.
		if c.Kind != BulkDelete {
			if el, err = s.eng.Get(ctx, sp, actor, el.ID); err != nil {
				_ = sp.Rollback(ctx)
				item.Reason, item.Changes = reasonOf(err), nil
				return item, nil
			}
		}
	}
	if err := sp.Commit(ctx); err != nil {
		item.Reason = reasonOf(err)
		return item, nil
	}
	for i := range changes {
		changes[i].Key = item.Key
	}
	item.Status = "ok"
	if len(item.Changes) == 0 {
		item.Status = "unchanged"
	}
	return item, changes
}

// reasonOf traduce un errore in un motivo leggibile.
func reasonOf(err error) string {
	var ve *engine.ValidationError
	switch {
	case errors.As(err, &ve):
		return ve.Msg
	case errors.Is(err, engine.ErrForbidden):
		return "non hai il permesso di farlo su questo elemento"
	case errors.Is(err, engine.ErrNotFound):
		return "elemento non trovato"
	case errors.Is(err, engine.ErrConflict):
		return "modificato da qualcun altro nel frattempo"
	}
	return "errore imprevisto: " + err.Error()
}

// bulkApply esegue un comando su un elemento e restituisce ciò che è cambiato; desc vuota =
// niente da cambiare.
func (s *Service) bulkApply(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, c BulkCommand) ([]bulkChange, string, error) {
	switch c.Kind {
	case BulkPhase:
		to := strings.TrimSpace(c.Value)
		if strings.EqualFold(el.State, to) {
			return nil, "", nil
		}
		t, err := engine.LoadType(ctx, tx, el.TypeKey)
		if err != nil {
			return nil, "", err
		}
		name := ""
		for _, st := range t.States {
			if strings.EqualFold(st.Name, to) {
				name = st.Name
			}
		}
		if name == "" {
			return nil, "", engine.InvalidParams("validation.work.type_lacks_phase", "phase", map[string]any{"type": strings.ToLower(el.TypeName), "phase": to}, "un elemento %s non ha la fase «%s»", strings.ToLower(el.TypeName), to)
		}
		before := el.State
		if _, err := s.eng.Transition(ctx, tx, actor, el.ID, name, c.Comment, engine.TransitionOpts{}); err != nil {
			return nil, "", err
		}
		return []bulkChange{{Element: el.ID, Kind: BulkPhase, Before: before, After: name}}, "fase " + before + " → " + name, nil

	case BulkAssign:
		member := strings.TrimSpace(c.Value)
		if deref(el.AssigneeID) == member {
			return nil, "", nil
		}
		before := deref(el.AssigneeID)
		if err := s.setAssignee(ctx, tx, actor, el, member, 0); err != nil {
			return nil, "", err
		}
		return []bulkChange{{Element: el.ID, Kind: BulkAssign, Before: before, After: member}}, "assegnato a " + firstNonEmpty(c.Label, nameOrNobody(ctx, tx, member)), nil

	case BulkField:
		key := strings.TrimSpace(c.Field)
		before := el.Fields[key]
		fresh, err := s.eng.Update(ctx, tx, actor, el.ID, engine.UpdateInput{Version: el.Version, Fields: map[string]any{key: c.Raw}})
		if err != nil {
			return nil, "", err
		}
		after := fresh.Fields[key]
		if reflect.DeepEqual(normalizeJSON(before), normalizeJSON(after)) {
			return nil, "", nil
		}
		return []bulkChange{{Element: el.ID, Kind: BulkField, Field: key, Before: before, After: after}},
			firstNonEmpty(c.Label, key) + " " + shown(after), nil

	case BulkLabels:
		return s.bulkLabels(ctx, tx, actor, el, c)

	case BulkSprint:
		if ok, err := matchesTypes(ctx, tx, el.TypeID, el.TypeKey, []string{anyItem}); err != nil {
			return nil, "", err
		} else if !ok {
			return nil, "", engine.InvalidCode("validation.work.bulk_sprint_types", "sprint", "solo gli elementi di lavoro (non epic né sprint) si pianificano in uno sprint")
		}
		target := strings.TrimSpace(c.Value)
		if strings.EqualFold(target, "backlog") {
			target = ""
		}
		before, err := currentSprint(ctx, tx, el.ID)
		if err != nil {
			return nil, "", err
		}
		if err := s.planIn(ctx, tx, actor, el, target); err != nil {
			return nil, "", err
		}
		after, err := currentSprint(ctx, tx, el.ID)
		if err != nil {
			return nil, "", err
		}
		if before == after {
			return nil, "", nil
		}
		desc := "nel backlog"
		if after != "" {
			desc = "sprint " + firstNonEmpty(c.Label, c.Value)
		}
		return []bulkChange{{Element: el.ID, Kind: BulkSprint, Before: before, After: after}}, desc, nil

	case BulkDelete:
		if el.TypeKey == RequestType {
			return nil, "", engine.InvalidCode("validation.work.bulk_delete_requests", "delete", "le richieste non si eliminano in blocco")
		}
		var kids, logs int
		if err := tx.QueryRow(ctx, `select (select count(*) from elements where parent_id = $1 and deleted_at is null),
			(select count(*) from work_logs where element_id = $1)`, el.ID).Scan(&kids, &logs); err != nil {
			return nil, "", err
		}
		if kids > 0 {
			return nil, "", engine.InvalidParams("validation.work.bulk_delete_children", "delete", map[string]any{"count": kids}, "ha %d elementi figli: eliminali o spostali prima", kids)
		}
		if logs > 0 {
			return nil, "", engine.InvalidCode("validation.work.bulk_delete_logged", "delete", "ha ore registrate: i costi non si perdono, annullalo invece di eliminarlo")
		}
		if _, err := tx.Exec(ctx, "update elements set deleted_at = now(), version = version + 1, updated_by = $2::uuid where id = $1", el.ID, nullable(actor.MemberID)); err != nil {
			return nil, "", err
		}
		if err := audit.Append(ctx, tx, audit.Event{Action: "element.delete", EntityType: el.TypeKey, EntityID: el.ID,
			ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: map[string]any{"key": el.Key}}); err != nil {
			return nil, "", err
		}
		return []bulkChange{{Element: el.ID, Kind: BulkDelete}}, "eliminato", nil
	}
	return nil, "", engine.InvalidValue("commands", "comando sconosciuto: %q", c.Kind)
}

func nameOrNobody(ctx context.Context, tx pgx.Tx, id string) string {
	if id == "" {
		return "nessuno"
	}
	var n string
	_ = tx.QueryRow(ctx, "select display_name from members where id::text = $1", id).Scan(&n)
	return n
}

func shown(v any) string {
	switch x := v.(type) {
	case nil:
		return "svuotato"
	case string:
		return x
	case float64:
		return fmt.Sprint(x)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// normalizeJSON porta un valore alla forma che ha dopo il giro in JSON, per confrontarlo.
func normalizeJSON(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

func currentSprint(ctx context.Context, tx pgx.Tx, elementID string) (string, error) {
	var id *string
	err := tx.QueryRow(ctx, `select r.target_id::text from relations r join relation_types rt on rt.tenant_id = r.tenant_id and rt.id = r.type_id and rt.key = $2
		where r.source_id = $1 limit 1`, elementID, RelPlannedIn).Scan(&id)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	return deref(id), nil
}

// bulkLabels aggiunge e toglie etichette (tag condivisi del tenant).
func (s *Service) bulkLabels(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, c BulkCommand) ([]bulkChange, string, error) {
	var added, removed []string
	for _, name := range c.Add {
		name = strings.TrimSpace(strings.TrimPrefix(name, "#"))
		if name == "" {
			continue
		}
		if len(name) > 50 {
			return nil, "", engine.TooLong("labels", 50, "etichetta troppo lunga (massimo 50 caratteri)")
		}
		id, err := tagID(ctx, tx, name, true)
		if err != nil {
			return nil, "", err
		}
		tag, err := tx.Exec(ctx, `insert into element_tags (tenant_id, element_id, tag_id) values (app_tenant(), $1, $2::uuid) on conflict do nothing`, el.ID, id)
		if err != nil {
			return nil, "", err
		}
		if tag.RowsAffected() > 0 {
			added = append(added, name)
		}
	}
	for _, name := range c.Remove {
		name = strings.TrimSpace(strings.TrimPrefix(name, "#"))
		id, err := tagID(ctx, tx, name, false)
		if err != nil {
			return nil, "", err
		}
		if id == "" {
			continue
		}
		tag, err := tx.Exec(ctx, "delete from element_tags where element_id = $1 and tag_id::text = $2", el.ID, id)
		if err != nil {
			return nil, "", err
		}
		if tag.RowsAffected() > 0 {
			removed = append(removed, name)
		}
	}
	if len(added) == 0 && len(removed) == 0 {
		return nil, "", nil
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "work", "labels", added, removed); err != nil {
		return nil, "", err
	}
	var parts []string
	for _, a := range added {
		parts = append(parts, "+"+a)
	}
	for _, r := range removed {
		parts = append(parts, "-"+r)
	}
	return []bulkChange{{Element: el.ID, Kind: BulkLabels, Before: removed, After: added}}, "etichette " + strings.Join(parts, " "), nil
}

// tagID cerca (o crea) l'etichetta condivisa con quel nome.
func tagID(ctx context.Context, tx pgx.Tx, name string, create bool) (string, error) {
	var id string
	err := tx.QueryRow(ctx, "select id::text from tags where owner_id is null and lower(name) = lower($1)", name).Scan(&id)
	if err == nil {
		return id, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if !create {
		return "", nil
	}
	nid := db.NewID()
	if _, err := tx.Exec(ctx, "insert into tags (tenant_id, id, name) values (app_tenant(), $1, $2)", nid, name); err != nil {
		return "", err
	}
	return nid, nil
}

// ---------------------------------------------------------------------------
// Annullamento

// LastBulk è l'ultimo comando in blocco dell'attore ancora annullabile.
type LastBulk struct {
	ID        string `json:"id"`
	Summary   string `json:"summary"`
	CreatedAt string `json:"created_at"`
	Elements  int    `json:"elements"`
}

// LastBulk restituisce l'ultimo comando annullabile (non annullato, di meno di un giorno).
func (s *Service) LastBulk(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*LastBulk, error) {
	var l LastBulk
	var raw []byte
	var at time.Time
	err := tx.QueryRow(ctx, `select id::text, summary, created_at, changes from bulk_commands
		where member_id = $1::uuid order by created_at desc limit 1`, actor.MemberID).Scan(&l.ID, &l.Summary, &at, &raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var undone *time.Time
	if err := tx.QueryRow(ctx, "select undone_at from bulk_commands where id::text = $1", l.ID).Scan(&undone); err != nil {
		return nil, err
	}
	if undone != nil || time.Since(at) > 24*time.Hour {
		return nil, nil
	}
	var ch []bulkChange
	_ = json.Unmarshal(raw, &ch)
	els := map[string]bool{}
	for _, c := range ch {
		els[c.Element] = true
	}
	l.Elements, l.CreatedAt = len(els), at.Format(time.RFC3339)
	return &l, nil
}

// UndoResult è l'esito dell'annullamento.
type UndoResult struct {
	Summary  string     `json:"summary"`
	Restored int        `json:"restored"`
	Skipped  []BulkItem `json:"skipped"`
}

// UndoBulk annulla l'ultimo comando dell'attore. Ciò che è cambiato nel frattempo (un
// altro ha toccato lo stesso valore) non si tocca e si elenca fra i saltati.
func (s *Service) UndoBulk(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*UndoResult, error) {
	last, err := s.LastBulk(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	if last == nil {
		return nil, engine.InvalidCode("validation.work.undo_nothing", "command", "non c'è un comando da annullare")
	}
	var raw []byte
	if err := tx.QueryRow(ctx, "select changes from bulk_commands where id::text = $1 for update", last.ID).Scan(&raw); err != nil {
		return nil, err
	}
	var changes []bulkChange
	if err := json.Unmarshal(raw, &changes); err != nil {
		return nil, err
	}
	res := &UndoResult{Summary: last.Summary, Skipped: []BulkItem{}}
	for i := len(changes) - 1; i >= 0; i-- {
		ch := changes[i]
		sp, err := tx.Begin(ctx)
		if err != nil {
			return nil, err
		}
		reason, err := s.undoOne(ctx, sp, actor, ch)
		if err != nil || reason != "" {
			_ = sp.Rollback(ctx)
			if err != nil {
				reason = reasonOf(err)
			}
			res.Skipped = append(res.Skipped, BulkItem{ID: ch.Element, Key: ch.Key, Status: "blocked", Reason: reason})
			continue
		}
		if err := sp.Commit(ctx); err != nil {
			return nil, err
		}
		res.Restored++
	}
	if _, err := tx.Exec(ctx, "update bulk_commands set undone_at = now() where id::text = $1", last.ID); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "work.bulk.undo", EntityType: "bulk_command", EntityID: last.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"restored": res.Restored, "skipped": len(res.Skipped)}}); err != nil {
		return nil, err
	}
	return res, nil
}

// undoOne riporta un valore com'era; un motivo non vuoto = saltato.
func (s *Service) undoOne(ctx context.Context, tx pgx.Tx, actor engine.Actor, ch bulkChange) (string, error) {
	if ch.Kind == BulkDelete {
		tag, err := tx.Exec(ctx, "update elements set deleted_at = null, version = version + 1, updated_by = $2::uuid where id::text = $1 and deleted_at is not null", ch.Element, nullable(actor.MemberID))
		if err != nil {
			return "", err
		}
		if tag.RowsAffected() == 0 {
			return "già ripristinato", nil
		}
		return "", nil
	}
	el, err := s.workItem(ctx, tx, actor, ch.Element, assignable...)
	if err != nil {
		return "", err
	}
	switch ch.Kind {
	case BulkAssign:
		if deref(el.AssigneeID) != toStr(ch.After) {
			return "l'assegnatario è cambiato nel frattempo", nil
		}
		return "", s.setAssignee(ctx, tx, actor, el, toStr(ch.Before), 0)
	case BulkPhase:
		if !strings.EqualFold(el.State, toStr(ch.After)) {
			return "la fase è cambiata nel frattempo", nil
		}
		_, err := s.eng.Transition(ctx, tx, actor, el.ID, toStr(ch.Before), "Annullato il comando in blocco", engine.TransitionOpts{})
		return "", err
	case BulkField:
		if !reflect.DeepEqual(normalizeJSON(el.Fields[ch.Field]), normalizeJSON(ch.After)) {
			return "il campo è cambiato nel frattempo", nil
		}
		_, err := s.eng.Update(ctx, tx, actor, el.ID, engine.UpdateInput{Version: el.Version, Fields: map[string]any{ch.Field: ch.Before}})
		return "", err
	case BulkSprint:
		cur, err := currentSprint(ctx, tx, el.ID)
		if err != nil {
			return "", err
		}
		if cur != toStr(ch.After) {
			return "lo sprint è cambiato nel frattempo", nil
		}
		return "", s.planIn(ctx, tx, actor, el, toStr(ch.Before))
	case BulkLabels:
		_, _, err := s.bulkLabels(ctx, tx, actor, el, BulkCommand{Kind: BulkLabels, Add: toStrs(ch.Before), Remove: toStrs(ch.After)})
		return "", err
	}
	return "comando sconosciuto", nil
}

func toStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func toStrs(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, x := range arr {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Contesto per la barra dei comandi

// BulkContext sono i valori che la barra dei comandi propone: persone, fasi, sprint, campi, etichette.
type BulkContext struct {
	Members []CtxMember `json:"members"`
	Phases  []string    `json:"phases"`
	Sprints []CtxSprint `json:"sprints"`
	Fields  []CtxField  `json:"fields"`
	Labels  []string    `json:"labels"`
}

type CtxMember struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type CtxSprint struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Title string `json:"title"`
	State string `json:"state"`
}

type CtxField struct {
	Key      string              `json:"key"`
	Name     string              `json:"name"`
	DataType string              `json:"data_type"`
	Multi    bool                `json:"multi"`
	Options  []engine.OptionView `json:"options,omitempty"`
}

// BulkContext raccoglie i valori ammessi (di uno spazio, se indicato per gli sprint).
//
// requests = true dà il contesto per le richieste (fasi e campi del tipo richiesta, nessuno sprint): serve alla
// selezione multipla della lista richieste.
func (s *Service) BulkContext(ctx context.Context, tx pgx.Tx, actor engine.Actor, spaceKey string, requests bool) (*BulkContext, error) {
	if !actor.Has("element.read") {
		return nil, engine.ErrForbidden
	}
	out := &BulkContext{Members: []CtxMember{}, Phases: []string{}, Sprints: []CtxSprint{}, Fields: []CtxField{}, Labels: []string{}}
	rows, err := tx.Query(ctx, `select id::text, display_name from members where status = 'active' and member_type in ('standard', 'guest') order by display_name`)
	if err != nil {
		return nil, err
	}
	if out.Members, err = pgx.CollectRows(rows, pgx.RowToStructByPos[CtxMember]); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	fieldIdx := map[string]int{}
	types := []string{RequestType}
	if !requests {
		var err error
		if types, err = workItemTypes(ctx, tx, true); err != nil {
			return nil, err
		}
	}
	for _, tk := range types {
		t, err := engine.LoadType(ctx, tx, tk)
		if err != nil {
			continue
		}
		for _, st := range t.States {
			if !seen[strings.ToLower(st.Name)] {
				seen[strings.ToLower(st.Name)] = true
				out.Phases = append(out.Phases, st.Name)
			}
		}
		fs, err := s.eng.TypeFields(ctx, tx, tk)
		if err != nil {
			return nil, err
		}
		for _, f := range fs {
			if f.Archived || slices.Contains([]string{"text", "element", "group"}, f.DataType) {
				continue
			}
			if _, ok := fieldIdx[f.Key]; ok {
				continue
			}
			fieldIdx[f.Key] = len(out.Fields)
			out.Fields = append(out.Fields, CtxField{Key: f.Key, Name: f.Name, DataType: f.DataType, Multi: f.Multi, Options: f.Options})
		}
	}
	args := []any{}
	where := ""
	if spaceKey != "" {
		args = append(args, strings.ToUpper(spaceKey))
		where = " and sp.key = $1"
	}
	if requests {
		where += " and false" // le richieste non stanno negli sprint
	}
	srows, err := tx.Query(ctx, `select e.id::text, sp.key || '-' || e.number, e.title, coalesce(o.name, '')
		from elements e join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id and t.key = 'sprint'
		join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where e.deleted_at is null and coalesce(o.category, '') not in ('done', 'cancelled')`+where+`
		order by coalesce(o.category, '') = 'in_progress' desc, e.start_at nulls last, e.number limit 100`, args...)
	if err != nil {
		return nil, err
	}
	if out.Sprints, err = pgx.CollectRows(srows, pgx.RowToStructByPos[CtxSprint]); err != nil {
		return nil, err
	}
	lrows, err := tx.Query(ctx, `select name from tags where owner_id is null order by lower(name) limit 300`)
	if err != nil {
		return nil, err
	}
	if out.Labels, err = pgx.CollectRows(lrows, pgx.RowTo[string]); err != nil {
		return nil, err
	}
	return out, nil
}
