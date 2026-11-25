package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/architecture"
	"regia/internal/modules/reviews"
	"regia/internal/modules/work"
	"regia/internal/platform/audit"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
	"regia/internal/platform/i18n"
	"regia/internal/platform/rbac"
)

// Procedura guidata «Coinvolgi un team» (replica/prossimi-passi.md, ondata «semplicità»).
//
//	POST /api/v1/coinvolgi/anteprima   cosa verrebbe scritto, senza scrivere niente
//	POST /api/v1/coinvolgi             lo scrive: tipo di valutazione, regola e nuova versione del processo
//
// Nessuna area è cablata: la procedura scrive solo configurazione che l'amministratore potrebbe
// fare a mano (tipo di valutazione con il suo questionario, regola del processo, nuova versione).
// L'anteprima esegue lo stesso codice in una transazione che poi si annulla, quindi non può
// divergere da ciò che la conferma scrive. Solo tenant.admin.
func (s *Server) coinvolgiRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/coinvolgi/anteprima", can(rbac.TenantAdmin, s.coinvolgiPreview))
	mux.HandleFunc("POST /api/v1/coinvolgi", can(rbac.TenantAdmin, s.coinvolgiApply))
}

// coinvolgiInput: quale team, dove e quando entra in gioco, cosa fa.
type coinvolgiInput struct {
	TeamID  string `json:"team_id"`  // team esistente
	NewTeam string `json:"new_team"` // oppure il nome di un team da creare
	Process string `json:"process"`  // chiave del processo (di una richiesta)
	Phase   string `json:"phase"`    // fase d'ingresso; vuoto = alla creazione
	When    string `json:"when"`     // condizione facoltativa
	Review  bool   `json:"review"`   // parere: tipo di valutazione con questionario
	Work    bool   `json:"work"`     // lavoro: impatto sul team, epic quando lo accetta
	// Mandatory: il parere è obbligatorio (default sì).
	Mandatory *bool `json:"mandatory"`
}

type coinvolgiReview struct {
	TypeKey       string `json:"type_key"`
	TypeName      string `json:"type_name"`
	TemplateKey   string `json:"template_key"`
	Mandatory     bool   `json:"mandatory"`
	ReviewTeamNew bool   `json:"review_team_new"` // il team valutatore non c'era
	GroupNew      bool   `json:"group_new"`       // e il gruppo delle sue persone nemmeno: è vuoto
}

type coinvolgiRule struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Process     string `json:"process"`
	ProcessName string `json:"process_name"`
	On          string `json:"on"`
	Phase       string `json:"phase"`
	When        string `json:"when"`
}

type coinvolgiResult struct {
	Team    string           `json:"team"`
	TeamNew bool             `json:"team_new"`
	Review  *coinvolgiReview `json:"review"`
	Work    bool             `json:"work"`
	Rule    coinvolgiRule    `json:"rule"`
	// Version: la nuova versione pubblicata del processo (le richieste già avviate restano sulla loro).
	Version  int      `json:"version"`
	Warnings []string `json:"warnings"`
	Applied  bool     `json:"applied"`
}

var errCoinvolgiDry = errors.New("anteprima")

func init() {
	i18n.Register(map[string]i18n.Msg{
		"coinvolgi.review.name":     {It: "Parere {team}", En: "{team} opinion"},
		"coinvolgi.review.section":  {It: "Parere del team {team}", En: "{team} team opinion"},
		"coinvolgi.review.accept":   {It: "La richiesta è accettabile per il team?", En: "Is the request acceptable for the team?"},
		"coinvolgi.review.notes":    {It: "Note, condizioni e prescrizioni", En: "Notes, conditions and prescriptions"},
		"coinvolgi.review.template": {It: "Questionario del parere: {team}", En: "Opinion questionnaire: {team}"},
		"coinvolgi.rule.name":       {It: "Coinvolgi il team {team}", En: "Involve the {team} team"},
		"coinvolgi.notes":           {It: "Procedura «Coinvolgi un team»: {team}", En: "\"Involve a team\" wizard: {team}"},
	})
}

var accentBase = map[rune]byte{'à': 'a', 'á': 'a', 'è': 'e', 'é': 'e', 'ì': 'i', 'í': 'i', 'ò': 'o', 'ó': 'o', 'ù': 'u', 'ú': 'u'}

// slugOf rende un nome una chiave (minuscole, cifre e _), sempre valida come chiave.
func slugOf(name string) string {
	var b []byte
	for _, r := range strings.ToLower(name) {
		c, ok := accentBase[r]
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			c = byte(r)
		case !ok:
			c = '_'
		}
		if c == '_' && (len(b) == 0 || b[len(b)-1] == '_') {
			continue
		}
		b = append(b, c)
	}
	out := strings.Trim(string(b), "_")
	if out == "" || out[0] < 'a' || out[0] > 'z' {
		out = "x" + out
	}
	if len(out) > 36 {
		out = strings.TrimRight(out[:36], "_")
	}
	return out
}

// freeKey aggiunge _2, _3… finché la chiave non esiste (exists dice se è già usata).
func freeKey(base string, exists func(string) (bool, error)) (string, error) {
	for i := 1; i < 100; i++ {
		k := base
		if i > 1 {
			k = fmt.Sprintf("%s_%d", base, i)
		}
		used, err := exists(k)
		if err != nil || !used {
			return k, err
		}
	}
	return "", engine.InvalidValue("key", "troppe chiavi simili")
}

func (s *Server) coinvolgiPreview(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.coinvolgiDo(w, r, p, true)
}

func (s *Server) coinvolgiApply(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	s.coinvolgiDo(w, r, p, false)
}

func (s *Server) coinvolgiDo(w http.ResponseWriter, r *http.Request, p *auth.Principal, dry bool) {
	var in coinvolgiInput
	if !decodeJSON(w, r, &in) {
		return
	}
	var out *coinvolgiResult
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		res, err := s.coinvolgi(r.Context(), tx, actorOf(r, p), in)
		if err != nil {
			return err
		}
		res.Applied = !dry
		out = res
		if dry {
			return errCoinvolgiDry // annulla la transazione: l'anteprima non scrive
		}
		return nil
	})
	if err != nil && !(dry && errors.Is(err, errCoinvolgiDry)) {
		s.reviewsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// coinvolgi esegue tutta la procedura nella transazione data.
func (s *Server) coinvolgi(ctx context.Context, tx pgx.Tx, actor engine.Actor, in coinvolgiInput) (*coinvolgiResult, error) {
	if s.reviews == nil || s.arch == nil {
		return nil, engine.ErrForbidden
	}
	if !in.Review && !in.Work {
		return nil, engine.InvalidCode("validation.coinvolgi.nothing", "review", "scegli cosa fa il team: un parere, del lavoro o tutti e due")
	}
	adm := actor
	adm.Permissions = append(slices.Clone(actor.Permissions), engine.PermProcessAdmin, "element.read", "element.write")
	lang, _ := i18n.MemberLang(ctx, tx, actor.MemberID)
	res := &coinvolgiResult{Work: in.Work, Warnings: []string{}}

	// 1. Il processo e la fase: prima di scrivere qualunque cosa.
	pi, err := s.eng.GetProcess(ctx, tx, adm, strings.TrimSpace(in.Process))
	if err != nil {
		return nil, err
	}
	if pi.TypeKey != architecture.RequestType {
		return nil, engine.InvalidCode("validation.coinvolgi.process_type", "process", "il processo deve essere quello di un tipo di richiesta")
	}
	if pi.Draft != nil {
		return nil, engine.InvalidCode("validation.coinvolgi.draft_open", "process", "il processo ha una bozza non pubblicata: pubblicala o scartala, poi riprova")
	}
	if pi.Current == nil {
		return nil, engine.InvalidCode("validation.coinvolgi.no_version", "process", "il processo non ha una versione pubblicata")
	}
	on := "create"
	if ph := strings.TrimSpace(in.Phase); ph != "" {
		if !slices.ContainsFunc(pi.Current.Phases, func(x engine.Phase) bool { return x.Name == ph }) {
			return nil, engine.InvalidParams("validation.coinvolgi.phase_unknown", "phase", map[string]any{"phase": ph}, "la fase %q non esiste nel processo", ph)
		}
		on = "enter:" + ph
		res.Rule.Phase = ph
	}
	if c := strings.TrimSpace(in.When); c != "" {
		if _, err := engine.ParseCondition(c); err != nil {
			return nil, engine.InvalidCode("validation.rule.condition_invalid", "when", "condizione non valida: %v", err)
		}
		res.Rule.When = c
	}

	// 2. Il team: uno esistente oppure uno nuovo (con il suo spazio di delivery, come dalla pagina dei team).
	var teamID, teamName string
	var leadID *string
	switch {
	case strings.TrimSpace(in.TeamID) != "":
		err := tx.QueryRow(ctx, "select id::text, name, lead_id::text from teams where id::text = $1", strings.TrimSpace(in.TeamID)).Scan(&teamID, &teamName, &leadID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.InvalidCode("validation.team_not_found", "team_id", "team inesistente")
		}
		if err != nil {
			return nil, err
		}
	case strings.TrimSpace(in.NewTeam) != "":
		id, err := s.arch.CreateTeam(ctx, tx, adm, architecture.TeamInput{Name: in.NewTeam})
		if err != nil {
			return nil, err
		}
		if _, err := work.EnsureTeamSpace(ctx, tx, adm, work.SpaceInput{TeamID: id}); err != nil {
			return nil, err
		}
		teamID, teamName, res.TeamNew = id, strings.TrimSpace(in.NewTeam), true
	default:
		return nil, engine.Required("team_id", "scegli un team o dai il nome di uno nuovo")
	}
	res.Team = teamName
	slug := slugOf(teamName)

	// 3. Il parere: team valutatore (stesso nome), questionario minimo e tipo di valutazione.
	var actions []engine.Action
	if in.Review {
		rv, err := s.coinvolgiReview(ctx, tx, adm, lang, teamName, slug, leadID, in.Mandatory)
		if err != nil {
			return nil, err
		}
		res.Review = rv
		actions = append(actions, engine.Action{Kind: reviews.ActionOpen, Ref: rv.TypeKey})
		if rv.GroupNew {
			res.Warnings = append(res.Warnings, "group_empty")
		}
	}
	if in.Work {
		actions = append(actions, engine.Action{Kind: architecture.ActionCreateImpact, Ref: teamName})
	}

	// 4. La regola nella bozza del processo e la nuova versione.
	raw, _ := json.Marshal(pi.Current)
	w, err := engine.DecodeDefinition(raw)
	if err != nil {
		return nil, err
	}
	ruleKey, err := freeKey("coinvolgi_"+slug, func(k string) (bool, error) {
		return slices.ContainsFunc(w.Rules, func(x engine.Rule) bool { return x.Key == k }), nil
	})
	if err != nil {
		return nil, err
	}
	rule := engine.Rule{Key: ruleKey, Name: i18n.T(lang, "coinvolgi.rule.name", i18n.P{"team": teamName}), On: on, When: res.Rule.When, Actions: actions}
	w.Rules = append(w.Rules, rule)
	notes := i18n.T(lang, "coinvolgi.notes", i18n.P{"team": teamName})
	def, _ := json.Marshal(w)
	if _, err := s.eng.SaveDraft(ctx, tx, adm, pi.Key, def, notes); err != nil {
		return nil, err
	}
	pub, _, err := s.eng.Publish(ctx, tx, adm, pi.Key, notes)
	if err != nil {
		return nil, err
	}
	for _, v := range pub.Versions {
		if v.Status == "published" {
			res.Version = v.Number
		}
	}
	res.Rule.Key, res.Rule.Name, res.Rule.Process, res.Rule.ProcessName, res.Rule.On = ruleKey, rule.Name, pi.Key, pi.Name, on

	payload := map[string]any{"team": teamName, "team_id": teamID, "process": pi.Key, "rule": ruleKey, "version": res.Version, "review": in.Review, "work": in.Work}
	if res.Review != nil {
		payload["review_type"] = res.Review.TypeKey
	}
	err = audit.Append(ctx, tx, audit.Event{Action: "coinvolgi.apply", EntityType: "process", EntityID: pi.ID,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP, Payload: payload})
	return res, err
}

// coinvolgiReview prepara il parere: il team valutatore col nome del team (si riusa se c'è già,
// altrimenti si crea con un gruppo omonimo), il modello di questionario minimo e il tipo di valutazione.
func (s *Server) coinvolgiReview(ctx context.Context, tx pgx.Tx, actor engine.Actor, lang, teamName, slug string, leadID *string, mandatory *bool) (*coinvolgiReview, error) {
	rv := &coinvolgiReview{Mandatory: mandatory == nil || *mandatory}
	teams, err := s.reviews.Teams(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	var teamKey string
	for _, t := range teams {
		if !t.Archived && strings.EqualFold(t.Name, teamName) {
			teamKey = t.Key
		}
	}
	exists := func(table string) func(string) (bool, error) {
		return func(k string) (bool, error) {
			var u bool
			err := tx.QueryRow(ctx, "select exists (select 1 from "+table+" where key = $1)", k).Scan(&u)
			return u, err
		}
	}
	if teamKey == "" {
		rv.ReviewTeamNew = true
		if teamKey, err = freeKey(slug, exists("review_teams")); err != nil {
			return nil, err
		}
		in := reviews.TeamInput{Key: teamKey, Name: &teamName, Group: &teamName, LeadID: leadID}
		if _, err = s.reviews.CreateTeam(ctx, tx, actor, in); err != nil {
			var ve *engine.ValidationError
			if !errors.As(err, &ve) || ve.Field != "group" {
				return nil, err
			}
			// Il gruppo delle persone del team non esiste: si crea vuoto (lo si riempie da «Persone»).
			if _, err = tx.Exec(ctx, "insert into groups (tenant_id, id, name) values (app_tenant(), $1, $2)", db.NewID(), teamName); err != nil {
				return nil, err
			}
			rv.GroupNew = true
			if _, err = s.reviews.CreateTeam(ctx, tx, actor, in); err != nil {
				return nil, err
			}
		}
	}

	// Il modello di questionario: una sola schermata, modificabile dopo come ogni altro.
	if rv.TemplateKey, err = freeKey("parere_"+slug, exists("questionnaire_templates")); err != nil {
		return nil, err
	}
	tp := i18n.P{"team": teamName}
	def := reviews.Definition{Name: i18n.T(lang, "coinvolgi.review.template", tp), Sections: []reviews.Section{{
		Key: "parere", Title: i18n.T(lang, "coinvolgi.review.section", tp),
		Questions: []reviews.Question{
			{Key: "accettabile", Label: i18n.T(lang, "coinvolgi.review.accept"), Type: reviews.QYesNo, Required: true},
			{Key: "note", Label: i18n.T(lang, "coinvolgi.review.notes"), Type: reviews.QText, Multiline: true},
		}}}}
	raw, _ := json.Marshal(def)
	if _, err := s.reviews.SaveTemplateDraft(ctx, tx, actor, rv.TemplateKey, raw, i18n.T(lang, "coinvolgi.notes", tp)); err != nil {
		return nil, err
	}
	if _, err := s.reviews.PublishTemplate(ctx, tx, actor, rv.TemplateKey, ""); err != nil {
		return nil, err
	}

	if rv.TypeKey, err = freeKey("parere_"+slug, exists("review_types")); err != nil {
		return nil, err
	}
	rv.TypeName = i18n.T(lang, "coinvolgi.review.name", tp)
	scope := "request"
	_, err = s.reviews.CreateType(ctx, tx, actor, reviews.TypeInput{Key: rv.TypeKey, Name: &rv.TypeName, Team: &teamKey, Scope: &scope,
		TemplateKey: &rv.TemplateKey, Mandatory: &rv.Mandatory})
	return rv, err
}
