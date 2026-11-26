package work

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
)

// Spazi dei team: ogni team ha uno spazio di delivery (teams.delivery_space_id) con la sua
// chiave (WEB, INT...), dove stanno epic, story, task, bug e sprint.

// TeamSpace è uno spazio di delivery con il suo team.
type TeamSpace struct {
	SpaceID      string  `json:"space_id"`
	Key          string  `json:"key"`
	Name         string  `json:"name"`
	TeamID       string  `json:"team_id"`
	Team         string  `json:"team"`
	LeadID       *string `json:"lead_id"`
	Lead         *string `json:"lead"`
	Open         int     `json:"open"`          // elementi aperti (story, task, bug)
	ActiveSprint *string `json:"active_sprint"` // chiave dello sprint attivo
	SprintTitle  *string `json:"active_sprint_title"`
}

// TeamWithoutSpace è un team che non ha ancora uno spazio di delivery.
type TeamWithoutSpace struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Proposed string `json:"proposed_key"`
}

// Spaces elenca gli spazi dei team e i team ancora senza spazio (con la chiave proposta).
func (s *Service) Spaces(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]TeamSpace, []TeamWithoutSpace, error) {
	rows, err := tx.Query(ctx, `
		select s.id, s.key, s.name, t.id, t.name, t.lead_id, m.display_name,
		       (select count(*) from elements e join element_types et on et.tenant_id = e.tenant_id and et.id = e.type_id
		         where e.space_id = s.id and e.deleted_at is null and e.resolved_at is null and et.module = 'work' and et.key not in ('sprint', 'epic'))::int,
		       sp.key, sp.title
		from teams t
		join spaces s on s.tenant_id = t.tenant_id and s.id = t.delivery_space_id
		left join members m on m.tenant_id = t.tenant_id and m.id = t.lead_id
		left join lateral (
			select ss.key || '-' || e.number as key, e.title from elements e
			join spaces ss on ss.tenant_id = e.tenant_id and ss.id = e.space_id
			join element_types et on et.tenant_id = e.tenant_id and et.id = e.type_id and et.key = 'sprint'
			join options o on o.tenant_id = e.tenant_id and o.id = e.state_id and o.category = 'in_progress'
			where e.space_id = s.id and e.deleted_at is null order by e.start_at nulls last limit 1) sp on true
		where not t.archived and not s.archived
		order by t.name`)
	if err != nil {
		return nil, nil, err
	}
	spaces, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (TeamSpace, error) {
		var t TeamSpace
		err := r.Scan(&t.SpaceID, &t.Key, &t.Name, &t.TeamID, &t.Team, &t.LeadID, &t.Lead, &t.Open, &t.ActiveSprint, &t.SprintTitle)
		return t, err
	})
	if err != nil {
		return nil, nil, err
	}
	rows, err = tx.Query(ctx, `select id, name from teams where delivery_space_id is null and not archived order by name`)
	if err != nil {
		return nil, nil, err
	}
	without, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (TeamWithoutSpace, error) {
		var t TeamWithoutSpace
		err := r.Scan(&t.ID, &t.Name)
		return t, err
	})
	if err != nil {
		return nil, nil, err
	}
	for i := range without {
		if without[i].Proposed, err = freeKey(ctx, tx, ProposeKey(without[i].Name)); err != nil {
			return nil, nil, err
		}
	}
	if spaces == nil {
		spaces = []TeamSpace{}
	}
	if without == nil {
		without = []TeamWithoutSpace{}
	}
	return spaces, without, nil
}

var spaceKeyRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,19}$`)

// ProposeKey propone la chiave dello spazio dal nome del team: con una parola le prime tre
// lettere (Web → WEB, Integrazioni → INT), con più parole le iniziali delle parole
// significative (Dati e analisi → DA, Customer Experience → CE), senza accenti.
func ProposeKey(name string) string {
	var words []string
	for _, w := range strings.FieldsFunc(name, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		clean := strings.Map(func(r rune) rune {
			if b, ok := accents[unicode.ToLower(r)]; ok {
				r = b
			}
			if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
				return unicode.ToUpper(r)
			}
			return -1
		}, w)
		if clean == "" {
			continue
		}
		switch strings.ToLower(clean) {
		case "e", "di", "del", "della", "dei", "degli", "delle", "la", "il", "lo", "le", "gli", "and", "of", "the", "per":
			continue
		}
		words = append(words, clean)
	}
	key := ""
	switch {
	case len(words) == 0:
		key = "TEAM"
	case len(words) == 1:
		key = words[0]
		if len(key) > 3 {
			key = key[:3]
		}
	default:
		for _, w := range words {
			if len(key) < 4 {
				key += w[:1]
			}
		}
	}
	if key[0] >= '0' && key[0] <= '9' {
		key = "T" + key
	}
	return key
}

// accents: lettere accentate più comuni → lettera di base (le chiavi sono ASCII).
var accents = map[rune]rune{'à': 'a', 'á': 'a', 'â': 'a', 'ä': 'a', 'è': 'e', 'é': 'e', 'ê': 'e', 'ë': 'e', 'ì': 'i', 'í': 'i', 'î': 'i', 'ï': 'i',
	'ò': 'o', 'ó': 'o', 'ô': 'o', 'ö': 'o', 'ù': 'u', 'ú': 'u', 'û': 'u', 'ü': 'u', 'ç': 'c', 'ñ': 'n'}

// freeKey restituisce la chiave stessa o, se è già usata, la prima libera con un numero.
func freeKey(ctx context.Context, tx pgx.Tx, key string) (string, error) {
	for i := 1; i < 100; i++ {
		cand := key
		if i > 1 {
			cand = key + strconv.Itoa(i)
		}
		var used bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from spaces where key = $1)", cand).Scan(&used); err != nil {
			return "", err
		}
		if !used {
			return cand, nil
		}
	}
	return "", engine.InvalidParams("validation.work.space_key_exhausted", "key", map[string]any{"key": key}, "nessuna chiave libera per %s", key)
}

// SpaceInput crea lo spazio di delivery di un team.
type SpaceInput struct {
	TeamID string `json:"team_id"`
	Key    string `json:"key"`   // vuota = proposta dal nome del team
	Name   string `json:"name"`  // vuoto = nome del team
	Model  string `json:"model"` // modello di board (scrum, kanban, supporto) da applicare alla creazione
}

// CreateTeamSpace crea lo spazio di delivery di un team (gestori dei team o amministratori).
func (s *Service) CreateTeamSpace(ctx context.Context, tx pgx.Tx, actor engine.Actor, in SpaceInput) (*TeamSpace, error) {
	if !actor.Has(permArch) && !actor.Has("tenant.admin") {
		return nil, engine.ErrForbidden
	}
	id, err := EnsureTeamSpace(ctx, tx, actor, in)
	if err != nil {
		return nil, err
	}
	if in.Model != "" {
		if err := s.ApplyModelToSpace(ctx, tx, id, in.Model); err != nil {
			return nil, err
		}
	}
	list, _, err := s.Spaces(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].TeamID == in.TeamID {
			return &list[i], nil
		}
	}
	return nil, engine.ErrNotFound
}

// EnsureTeamSpace restituisce l'id dello spazio di delivery del team, creandolo se manca:
// chiave proposta (o indicata), tipi del work management ammessi, referente del team come
// responsabile. Si usa anche alla creazione del team e dall'azione create_epic.
func EnsureTeamSpace(ctx context.Context, tx pgx.Tx, actor engine.Actor, in SpaceInput) (string, error) {
	var name string
	var lead, spaceID *string
	err := tx.QueryRow(ctx, "select name, lead_id, delivery_space_id from teams where id::text = $1 for update", in.TeamID).Scan(&name, &lead, &spaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", engine.InvalidCode("validation.team_not_found", "team_id", "team inesistente")
	}
	if err != nil {
		return "", err
	}
	if spaceID != nil {
		return *spaceID, nil
	}
	key := strings.ToUpper(strings.TrimSpace(in.Key))
	if key == "" {
		if key, err = freeKey(ctx, tx, ProposeKey(name)); err != nil {
			return "", err
		}
	} else if !spaceKeyRe.MatchString(key) {
		return "", engine.TooLong("key", 20, "chiave non valida: lettere maiuscole e cifre, inizia con una lettera, al massimo 20 caratteri")
	} else {
		var used bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from spaces where key = $1)", key).Scan(&used); err != nil {
			return "", err
		}
		if used {
			return "", engine.Duplicate("key", "la chiave %s è già usata da un altro spazio", key)
		}
	}
	title := strings.TrimSpace(in.Name)
	if title == "" {
		title = name
	}
	id := db.NewID()
	if _, err := tx.Exec(ctx, `insert into spaces (tenant_id, id, key, name, kind, lead_id, settings)
		values (app_tenant(), $1, $2, $3, 'project', $4, jsonb_build_object('team_id', $5::text))`, id, key, title, lead, in.TeamID); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, `insert into space_types (tenant_id, space_id, type_id)
		select app_tenant(), $1, id from element_types where key = any($2) on conflict do nothing`, id, typeKeys()); err != nil {
		return "", err
	}
	if _, err := tx.Exec(ctx, "update teams set delivery_space_id = $2 where id::text = $1", in.TeamID, id); err != nil {
		return "", err
	}
	return id, audit.Append(ctx, tx, audit.Event{Action: "space.create", EntityType: "space", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"key": key, "team_id": in.TeamID}})
}
