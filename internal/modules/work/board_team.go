package work

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
)

// Board per team (ondata 3, replica/usabilita/configurazione.md §3.14): colonne proprie di ogni
// team con più fasi per colonna (anche diverse per tipo), limite WIP, modelli (Scrum, Kanban,
// Supporto) e il percorso «modifica → anteprima → pubblica» con una bozza per spazio.

// targetPhase sceglie la fase di arrivo di una card spostata in una colonna: la prima, nell'ordine
// della colonna, che il processo del tipo ammette dalla fase attuale; se nessuna è raggiungibile
// si tenta la prima che il tipo ha, così il messaggio del motore spiega perché non si può.
func (s *Service) targetPhase(ctx context.Context, tx pgx.Tx, actor engine.Actor, el *engine.Element, col BoardColumn) (string, error) {
	t, err := engine.LoadType(ctx, tx, el.TypeKey)
	if err != nil {
		return "", err
	}
	var own []string
	for _, p := range col.phasesFor(el.TypeKey) {
		if slices.ContainsFunc(t.States, func(x engine.State) bool { return strings.EqualFold(x.Name, p) }) {
			own = append(own, p)
		}
	}
	if len(own) == 0 {
		return "", engine.InvalidParams("validation.board.type_lacks_phase", "column", map[string]any{"type": el.TypeName, "column": col.Name}, "un elemento %s non ha fasi nella colonna «%s»", el.TypeName, col.Name)
	}
	if len(own) > 1 {
		av, err := s.eng.Available(ctx, tx, actor, el)
		if err != nil {
			return "", err
		}
		for _, p := range own {
			if slices.ContainsFunc(av, func(v engine.TransitionView) bool { return strings.EqualFold(v.To, p) }) {
				return p, nil
			}
		}
	}
	return own[0], nil
}

// PhaseInfo è una fase dei processi dei tipi della board.
type PhaseInfo struct {
	Name     string   `json:"name"`
	Category string   `json:"category"`
	Types    []string `json:"types"`
}

// phaseCatalog elenca le fasi (escluse le annullate) dei processi dei tipi, per nome, nell'ordine.
func phaseCatalog(ctx context.Context, tx pgx.Tx, types []string) ([]PhaseInfo, error) {
	var out []PhaseInfo
	rank := map[string]int{"todo": 0, "in_progress": 1, "done": 2}
	for _, tk := range types {
		t, err := engine.LoadType(ctx, tx, tk)
		if errors.Is(err, engine.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, st := range t.States {
			if _, ok := rank[st.Category]; !ok {
				continue
			}
			i := slices.IndexFunc(out, func(p PhaseInfo) bool { return strings.EqualFold(p.Name, st.Name) })
			if i < 0 {
				out = append(out, PhaseInfo{Name: st.Name, Category: st.Category, Types: []string{tk}})
			} else if !slices.Contains(out[i].Types, tk) {
				out[i].Types = append(out[i].Types, tk)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b PhaseInfo) int { return rank[a.Category] - rank[b.Category] })
	return out, nil
}

// BoardModel è un modello di team.
type BoardModel struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	NoSprint    bool   `json:"no_sprint"`
	Sort        string `json:"sort"`
}

type modelColumn struct {
	name  string
	cat   string   // categoria delle fasi che raccoglie
	words []string // parole nel nome della fase; vuoto = tutte le altre della categoria
	wip   int
}

type modelDef struct {
	BoardModel
	cols []modelColumn
}

var reviewWords = []string{"revision", "review", "verific", "test", "collaud"}
var waitWords = []string{"attesa", "cliente", "waiting", "sospes", "bloccat"}

var boardModels = []modelDef{
	{BoardModel{Key: "scrum", Name: "Scrum", Description: "Sprint e backlog; colonne Da fare, In corso, In revisione, Fatto."}, []modelColumn{
		{name: "Da fare", cat: "todo"},
		{name: "In corso", cat: "in_progress"},
		{name: "In revisione", cat: "in_progress", words: reviewWords},
		{name: "Fatto", cat: "done"},
	}},
	{BoardModel{Key: "kanban", Name: "Kanban", Description: "Flusso continuo, senza sprint; limite WIP sulle colonne centrali.", NoSprint: true}, []modelColumn{
		{name: "Da fare", cat: "todo"},
		{name: "In corso", cat: "in_progress", wip: 3},
		{name: "In revisione", cat: "in_progress", words: reviewWords, wip: 2},
		{name: "Fatto", cat: "done"},
	}},
	{BoardModel{Key: "supporto", Name: "Supporto", Description: "Richieste di assistenza: Nuova, In lavorazione, In attesa del cliente, Risolta; ordinate per scadenza.", NoSprint: true, Sort: "due"}, []modelColumn{
		{name: "Nuova", cat: "todo"},
		{name: "In lavorazione", cat: "in_progress"},
		{name: "In attesa del cliente", cat: "in_progress", words: waitWords},
		{name: "Risolta", cat: "done"},
	}},
}

// Models elenca i modelli di team.
func Models() []BoardModel {
	out := make([]BoardModel, 0, len(boardModels))
	for _, m := range boardModels {
		out = append(out, m.BoardModel)
	}
	return out
}

// ModelSettings costruisce la configurazione di un modello sulle fasi dei processi del team: ogni
// fase va nella prima colonna adatta (per categoria e parole del nome), le altre della categoria
// nella colonna «generica». Il resto della configurazione (corsia, giorni dei chiusi) si conserva.
func ModelSettings(ctx context.Context, tx pgx.Tx, key string, base BoardSettings) (*BoardSettings, error) {
	i := slices.IndexFunc(boardModels, func(m modelDef) bool { return m.Key == key })
	if i < 0 {
		return nil, engine.NotFound("model", "", "modello %q inesistente", key)
	}
	def := boardModels[i]
	phases, err := phaseCatalog(ctx, tx, base.Types)
	if err != nil {
		return nil, err
	}
	out := base
	out.Custom, out.Model, out.NoSprint, out.Sort = true, def.Key, def.NoSprint, def.Sort
	out.Columns = make([]BoardColumn, len(def.cols))
	for ci, c := range def.cols {
		out.Columns[ci] = BoardColumn{Name: c.name, Phases: []string{}, WIP: c.wip}
	}
	matches := func(c modelColumn, name string) bool {
		l := strings.ToLower(name)
		return slices.ContainsFunc(c.words, func(w string) bool { return strings.Contains(l, w) })
	}
	for _, p := range phases {
		to := -1
		for ci, c := range def.cols { // prima le colonne con parole chiave
			if c.cat == p.Category && len(c.words) > 0 && matches(c, p.Name) {
				to = ci
				break
			}
		}
		if to < 0 {
			for ci, c := range def.cols {
				if c.cat == p.Category && len(c.words) == 0 {
					to = ci
					break
				}
			}
		}
		if to >= 0 {
			out.Columns[to].Phases = append(out.Columns[to].Phases, p.Name)
		}
	}
	return &out, nil
}

// BoardPreview è l'effetto di una configurazione sulle card di oggi.
type BoardPreview struct {
	Settings   *BoardSettings `json:"settings"`
	Columns    []ColumnView   `json:"columns"`
	Unassigned []string       `json:"unassigned"` // fasi senza colonna
	Empty      []string       `json:"empty"`      // colonne senza fasi
	Hidden     int            `json:"hidden"`     // card oggi in fasi senza colonna
}

// previewSettings calcola colonne, conteggi e avvisi di una configurazione senza salvarla.
func (s *Service) previewSettings(ctx context.Context, tx pgx.Tx, spaceID string, st *BoardSettings) (*BoardPreview, error) {
	out := &BoardPreview{Settings: st, Columns: []ColumnView{}, Unassigned: []string{}, Empty: []string{}}
	counts := make([]int, len(st.Columns))
	rows, err := tx.Query(ctx, `select t.key, coalesce(o.name, ''), count(*)::int from elements e
		join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
		left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
		where e.space_id = $1 and t.key = any($2) and e.deleted_at is null and not e.is_draft
		  and (e.resolved_at is null or e.resolved_at >= $3)
		group by 1, 2`, spaceID, st.Types, time.Now().AddDate(0, 0, -max(st.DoneDays, 14)))
	if err != nil {
		return nil, err
	}
	var typ, ph string
	var n int
	if _, err := pgx.ForEachRow(rows, []any{&typ, &ph, &n}, func() error {
		if c := st.columnOf(typ, ph); c >= 0 {
			counts[c] += n
		} else if ph != "" {
			out.Hidden += n
		}
		return nil
	}); err != nil {
		return nil, err
	}
	for i, c := range st.Columns {
		cv := ColumnView{BoardColumn: c, Count: counts[i], Over: c.WIP > 0 && counts[i] > c.WIP}
		if cv.Phases == nil {
			cv.Phases = []string{}
		}
		out.Columns = append(out.Columns, cv)
		if len(c.Phases) == 0 && len(c.TypePhases) == 0 {
			out.Empty = append(out.Empty, c.Name)
		}
	}
	cat, err := phaseCatalog(ctx, tx, st.Types)
	if err != nil {
		return nil, err
	}
	for _, p := range cat {
		for _, tk := range p.Types {
			if st.columnOf(tk, p.Name) < 0 {
				out.Unassigned = append(out.Unassigned, p.Name)
				break
			}
		}
	}
	return out, nil
}

// BoardConfig è tutto ciò che serve all'editor delle colonne di un team.
type BoardConfig struct {
	Space     Ref            `json:"space"`
	Published *BoardSettings `json:"published"`
	Draft     *BoardSettings `json:"draft"`
	Phases    []PhaseInfo    `json:"phases"`
	Models    []BoardModel   `json:"models"`
	Preview   *BoardPreview  `json:"preview"` // della bozza se c'è, altrimenti della pubblicata
	CanManage bool           `json:"can_manage"`
}

func (s *Service) loadDraft(ctx context.Context, tx pgx.Tx, spaceID string) (*BoardSettings, error) {
	var raw []byte
	err := tx.QueryRow(ctx, "select settings from board_drafts where space_id = $1", spaceID).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	st := &BoardSettings{}
	if err := json.Unmarshal(raw, st); err != nil {
		return nil, err
	}
	if st.Columns == nil {
		st.Columns = []BoardColumn{}
	}
	return st, nil
}

// BoardConfigOf legge la configurazione per l'editor.
func (s *Service) BoardConfigOf(ctx context.Context, tx pgx.Tx, actor engine.Actor, spaceKey string) (*BoardConfig, error) {
	if !actor.Has("element.read") {
		return nil, engine.ErrForbidden
	}
	sp, err := loadSpace(ctx, tx, spaceKey)
	if err != nil {
		return nil, err
	}
	pub, err := s.Settings(ctx, tx, sp.ID)
	if err != nil {
		return nil, err
	}
	out := &BoardConfig{Space: Ref{Key: sp.Key, Title: sp.Name}, Published: pub, Models: Models()}
	if out.CanManage, err = canManageSpace(ctx, tx, actor, sp.ID); err != nil {
		return nil, err
	}
	if out.Draft, err = s.loadDraft(ctx, tx, sp.ID); err != nil {
		return nil, err
	}
	if out.Phases, err = phaseCatalog(ctx, tx, pub.Types); err != nil {
		return nil, err
	}
	cur := pub
	if out.Draft != nil {
		cur = out.Draft
	}
	out.Preview, err = s.previewSettings(ctx, tx, sp.ID, cur)
	return out, err
}

// PreviewInput: un modello da applicare (sulla bozza o sulla pubblicata) o una configurazione.
type PreviewInput struct {
	Model    string         `json:"model"`
	Settings *BoardSettings `json:"settings"`
}

// PreviewBoard mostra l'effetto di un modello o di una configurazione senza salvare niente.
func (s *Service) PreviewBoard(ctx context.Context, tx pgx.Tx, actor engine.Actor, spaceKey string, in PreviewInput) (*BoardPreview, error) {
	sp, err := loadSpace(ctx, tx, spaceKey)
	if err != nil {
		return nil, err
	}
	if ok, err := canManageSpace(ctx, tx, actor, sp.ID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	pub, err := s.Settings(ctx, tx, sp.ID)
	if err != nil {
		return nil, err
	}
	var st *BoardSettings
	switch {
	case in.Model != "":
		base := *pub
		if d, err := s.loadDraft(ctx, tx, sp.ID); err != nil {
			return nil, err
		} else if d != nil {
			base = *d
		}
		if st, err = ModelSettings(ctx, tx, in.Model, base); err != nil {
			return nil, err
		}
	case in.Settings != nil:
		st = in.Settings
		if len(st.Types) == 0 {
			st.Types = pub.Types
		}
		if err := validateSettings(st, false); err != nil {
			return nil, err
		}
	default:
		return nil, engine.Required("model", "indica un modello o una configurazione")
	}
	return s.previewSettings(ctx, tx, sp.ID, st)
}

// SaveDraft salva la bozza delle colonne.
func (s *Service) SaveDraft(ctx context.Context, tx pgx.Tx, actor engine.Actor, spaceKey string, in BoardSettings) (*BoardConfig, error) {
	sp, err := loadSpace(ctx, tx, spaceKey)
	if err != nil {
		return nil, err
	}
	if ok, err := canManageSpace(ctx, tx, actor, sp.ID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	if len(in.Types) == 0 {
		in.Types = slices.Clone(itemTypes)
	}
	in.Custom = true
	if err := validateSettings(&in, false); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(in)
	if _, err := tx.Exec(ctx, `insert into board_drafts (tenant_id, space_id, settings, updated_by) values (app_tenant(), $1, $2, $3)
		on conflict (tenant_id, space_id) do update set settings = excluded.settings, updated_by = excluded.updated_by, updated_at = now()`,
		sp.ID, raw, nullable(actor.MemberID)); err != nil {
		return nil, err
	}
	return s.BoardConfigOf(ctx, tx, actor, spaceKey)
}

// DiscardDraft scarta la bozza.
func (s *Service) DiscardDraft(ctx context.Context, tx pgx.Tx, actor engine.Actor, spaceKey string) (*BoardConfig, error) {
	sp, err := loadSpace(ctx, tx, spaceKey)
	if err != nil {
		return nil, err
	}
	if ok, err := canManageSpace(ctx, tx, actor, sp.ID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	if _, err := tx.Exec(ctx, "delete from board_drafts where space_id = $1", sp.ID); err != nil {
		return nil, err
	}
	return s.BoardConfigOf(ctx, tx, actor, spaceKey)
}

// PublishDraft pubblica la bozza come board del team. Le colonne senza fasi sono ammesse (la
// bozza le segnala), ma nessuna card può entrarci.
func (s *Service) PublishDraft(ctx context.Context, tx pgx.Tx, actor engine.Actor, spaceKey string) (*BoardConfig, error) {
	sp, err := loadSpace(ctx, tx, spaceKey)
	if err != nil {
		return nil, err
	}
	if ok, err := canManageSpace(ctx, tx, actor, sp.ID); err != nil || !ok {
		return nil, firstErr(err, engine.ErrForbidden)
	}
	d, err := s.loadDraft(ctx, tx, sp.ID)
	if err != nil {
		return nil, err
	}
	if d == nil {
		return nil, engine.InvalidCode("validation.board.no_draft", "draft", "non c'è una bozza da pubblicare")
	}
	if len(d.Columns) == 0 {
		return nil, engine.InvalidCode("validation.board.no_columns", "columns", "la board deve avere almeno una colonna")
	}
	if err := s.storeSettings(ctx, tx, sp.ID, sp.Key, sp.Name, *d); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, "delete from board_drafts where space_id = $1", sp.ID); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "board.publish", EntityType: "space", EntityID: sp.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"model": d.Model, "columns": len(d.Columns)}}); err != nil {
		return nil, err
	}
	return s.BoardConfigOf(ctx, tx, actor, spaceKey)
}

// ApplyModelToSpace applica subito un modello alla board di uno spazio (alla creazione del team).
func (s *Service) ApplyModelToSpace(ctx context.Context, tx pgx.Tx, spaceID, model string) error {
	var key, name string
	if err := tx.QueryRow(ctx, "select key, name from spaces where id = $1", spaceID).Scan(&key, &name); err != nil {
		return err
	}
	pub, err := s.Settings(ctx, tx, spaceID)
	if err != nil {
		return err
	}
	st, err := ModelSettings(ctx, tx, model, *pub)
	if err != nil {
		return err
	}
	return s.storeSettings(ctx, tx, spaceID, key, name, *st)
}
