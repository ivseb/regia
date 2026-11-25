// Package governance è la governance del portafoglio (demand.md §7; piano, passo 9):
// organi collegiali (comitato investimenti, steering, CAB...) con presidente, segretario,
// membri e quorum; sedute con ordine del giorno proposto dalle approvazioni attive,
// decisioni che chiudono i passi di approvazione dell'organo, presenze e verbale immutabile.
package governance

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/approval"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

type Service struct {
	eng       *engine.Service
	approvals *approval.Service
}

func New(eng *engine.Service, ap *approval.Service) *Service {
	return &Service{eng: eng, approvals: ap}
}

// Kinds sono i tipi di organo del pacchetto di partenza, con il nome italiano. Il tenant
// può usarne altri: le politiche di approvazione cercano l'organo per tipo.
var Kinds = map[string]string{
	"investment_committee": "Comitato investimenti",
	"steering":             "Steering committee",
	"architecture_board":   "Board architetturale",
	"cab":                  "Change advisory board",
	"other":                "Altro organo",
}

var kindRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,40}$`)

// Member è un membro dell'organo.
type Member struct {
	MemberID string `json:"member_id"`
	Name     string `json:"name"`
	Voting   bool   `json:"voting"`
}

// BodyGroup è un gruppo membro dell'organo: i suoi membri (e quelli dei sottogruppi, se il
// tenant lo consente) siedono nell'organo; Voting vale per tutti.
type BodyGroup struct {
	GroupID string `json:"group_id"`
	Name    string `json:"name"`
	Voting  bool   `json:"voting"`
	Members int    `json:"members"` // membri effettivi del gruppo adesso
}

// EffectiveMember è chi siede davvero nell'organo adesso, direttamente o tramite un gruppo.
type EffectiveMember struct {
	MemberID string  `json:"member_id"`
	Name     string  `json:"name"`
	Voting   bool    `json:"voting"`
	ViaGroup *string `json:"via_group"` // null = membro diretto
}

// Body è un organo di governance.
type Body struct {
	ID                 string  `json:"id"`
	Name               string  `json:"name"`
	Kind               string  `json:"kind"`
	KindName           string  `json:"kind_name"`
	Description        string  `json:"description"`
	ChairID            *string `json:"chair_id"`
	Chair              *string `json:"chair"`
	SecretaryID        *string `json:"secretary_id"`
	Secretary          *string `json:"secretary"`
	Quorum             *int    `json:"quorum"`          // votanti presenti richiesti; nil = maggioranza
	QuorumRequired     int     `json:"quorum_required"` // valore effettivo
	ApprovalLimitCents *int64  `json:"approval_limit_cents"`
	// InlineDecision: i membri possono decidere dall'elenco senza aprire il pacchetto della richiesta.
	InlineDecision bool        `json:"inline_decision"`
	Archived       bool        `json:"archived"`
	Members        []Member    `json:"members"` // membri diretti (modificabili)
	Groups         []BodyGroup `json:"groups"`
	// Effective: membri effettivi, calcolati ora (diretti più gruppi); il quorum li usa.
	Effective   []EffectiveMember `json:"effective"`
	Pending     int               `json:"pending"` // richieste con un passo attivo assegnato all'organo
	NextMeeting *time.Time        `json:"next_meeting"`
	CanManage   bool              `json:"can_manage"`
}

// quorumRequired: il quorum configurato, altrimenti la maggioranza dei votanti.
func quorumRequired(q *int, voting int) int {
	if q != nil {
		return *q
	}
	return voting/2 + 1
}

func requireAdmin(actor engine.Actor) error {
	if !actor.Has(rbac.PortfolioAdmin) {
		return engine.ErrForbidden
	}
	return nil
}

// Bodies elenca gli organi con membri, richieste in attesa e prossima seduta.
func (s *Service) Bodies(ctx context.Context, tx pgx.Tx, actor engine.Actor, archived bool) ([]Body, error) {
	if !actor.Has(rbac.ElementRead) {
		return nil, engine.ErrForbidden
	}
	rows, err := tx.Query(ctx, `
		select b.id, b.name, b.kind, b.description, b.chair_id, c.display_name, b.secretary_id, sec.display_name,
		       b.quorum, b.approval_limit_cents, b.inline_decision, b.archived,
		       (select count(distinct rq.element_id) from approval_step_assignees a
		          join approval_steps st on st.tenant_id = a.tenant_id and st.id = a.step_id and st.decision = 'pending'
		          join approval_requests rq on rq.tenant_id = st.tenant_id and rq.id = st.request_id and rq.status = 'pending'
		          where a.tenant_id = b.tenant_id and a.body_id = b.id),
		       (select min(m.scheduled_at) from meetings m where m.tenant_id = b.tenant_id and m.body_id = b.id
		          and m.status in ('planned','convened') and m.scheduled_at >= now() - interval '12 hours')
		from governance_bodies b
		left join members c on c.tenant_id = b.tenant_id and c.id = b.chair_id
		left join members sec on sec.tenant_id = b.tenant_id and sec.id = b.secretary_id
		where $1 or not b.archived
		order by b.archived, b.name`, archived)
	if err != nil {
		return nil, err
	}
	var out []Body
	for rows.Next() {
		var b Body
		if err := rows.Scan(&b.ID, &b.Name, &b.Kind, &b.Description, &b.ChairID, &b.Chair, &b.SecretaryID, &b.Secretary,
			&b.Quorum, &b.ApprovalLimitCents, &b.InlineDecision, &b.Archived, &b.Pending, &b.NextMeeting); err != nil {
			rows.Close()
			return nil, err
		}
		b.KindName = Kinds[b.Kind]
		if b.KindName == "" {
			b.KindName = b.Kind
		}
		out = append(out, b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Members, err = bodyMembers(ctx, tx, out[i].ID); err != nil {
			return nil, err
		}
		if out[i].Groups, err = bodyGroups(ctx, tx, out[i].ID); err != nil {
			return nil, err
		}
		if out[i].Effective, err = effectiveMembers(ctx, tx, out[i].ID); err != nil {
			return nil, err
		}
		voting := 0
		for _, m := range out[i].Effective {
			if m.Voting {
				voting++
			}
		}
		out[i].QuorumRequired = quorumRequired(out[i].Quorum, voting)
		out[i].CanManage = actor.Has(rbac.PortfolioAdmin) || isOfficer(out[i], actor.MemberID)
	}
	if out == nil {
		out = []Body{}
	}
	return out, nil
}

func isOfficer(b Body, memberID string) bool {
	return (b.ChairID != nil && *b.ChairID == memberID) || (b.SecretaryID != nil && *b.SecretaryID == memberID)
}

func bodyMembers(ctx context.Context, tx pgx.Tx, bodyID string) ([]Member, error) {
	rows, err := tx.Query(ctx, `select bm.member_id, m.display_name, bm.voting from governance_body_members bm
		join members m on m.tenant_id = bm.tenant_id and m.id = bm.member_id
		where bm.body_id = $1 order by m.display_name`, bodyID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Member])
	if out == nil {
		out = []Member{}
	}
	return out, err
}

func bodyGroups(ctx context.Context, tx pgx.Tx, bodyID string) ([]BodyGroup, error) {
	rows, err := tx.Query(ctx, `select bg.group_id, g.name, bg.voting,
			(select count(*) from group_effective_members(bg.group_id))::int
		from governance_body_groups bg join groups g on g.tenant_id = bg.tenant_id and g.id = bg.group_id
		where bg.body_id = $1 order by lower(g.name)`, bodyID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[BodyGroup])
	if out == nil {
		out = []BodyGroup{}
	}
	return out, err
}

func effectiveMembers(ctx context.Context, tx pgx.Tx, bodyID string) ([]EffectiveMember, error) {
	rows, err := tx.Query(ctx, `select e.member_id, m.display_name, e.voting, g.name
		from body_effective_members($1) e
		join members m on m.id = e.member_id and m.status = 'active'
		left join groups g on g.id = e.via_group
		order by m.display_name`, bodyID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[EffectiveMember])
	if out == nil {
		out = []EffectiveMember{}
	}
	return out, err
}

func (s *Service) body(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*Body, error) {
	all, err := s.Bodies(ctx, tx, actor, true)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == id {
			return &all[i], nil
		}
	}
	return nil, engine.ErrNotFound
}

// BodyInput crea o modifica un organo. Members sostituisce l'elenco dei membri; il
// presidente è sempre membro votante.
type BodyInput struct {
	Name               string `json:"name"`
	Kind               string `json:"kind"`
	Description        string `json:"description"`
	ChairID            string `json:"chair_id"`
	SecretaryID        string `json:"secretary_id"`
	Quorum             *int   `json:"quorum"`
	ApprovalLimitCents *int64 `json:"approval_limit_cents"`
	// InlineDecision: assente = invariato in modifica, «sì» alla creazione.
	InlineDecision *bool         `json:"inline_decision"`
	Archived       bool          `json:"archived"`
	Members        []MemberInput `json:"members"`
	// Groups sostituisce i gruppi dell'organo; assente = invariati.
	Groups *[]GroupInput `json:"groups"`
}

// GroupInput è un gruppo membro dell'organo.
type GroupInput struct {
	GroupID string `json:"group_id"`
	Voting  *bool  `json:"voting"`
}

type MemberInput struct {
	MemberID string `json:"member_id"`
	Voting   *bool  `json:"voting"`
}

func (in *BodyInput) normalize() error {
	in.Name, in.Kind, in.Description = strings.TrimSpace(in.Name), strings.TrimSpace(in.Kind), strings.TrimSpace(in.Description)
	if in.Name == "" {
		return engine.InvalidCode("validation.name_required", "name", "il nome è obbligatorio")
	}
	if in.Kind == "" {
		in.Kind = "other"
	}
	if !kindRe.MatchString(in.Kind) {
		return engine.InvalidValue("kind", "tipo non valido (minuscole, cifre e _)")
	}
	if in.ChairID == "" {
		return engine.Required("chair_id", "serve un presidente")
	}
	if in.ApprovalLimitCents != nil && *in.ApprovalLimitCents < 0 {
		return engine.InvalidCode("validation.not_negative", "approval_limit_cents", "non può essere negativo")
	}
	seen := map[string]bool{}
	var members []MemberInput
	for _, m := range in.Members {
		if m.MemberID == "" || seen[m.MemberID] {
			continue
		}
		seen[m.MemberID] = true
		members = append(members, m)
	}
	if !seen[in.ChairID] {
		t := true
		members = append(members, MemberInput{MemberID: in.ChairID, Voting: &t})
	}
	voting := 0
	for i := range members {
		if members[i].MemberID == in.ChairID {
			t := true
			members[i].Voting = &t
		}
		if members[i].Voting == nil || *members[i].Voting {
			voting++
		}
	}
	in.Members = members
	if in.Quorum != nil && *in.Quorum < 1 {
		return engine.InvalidCode("validation.governance.quorum_min", "quorum", "il quorum è almeno 1")
	}
	// Con i gruppi i votanti dipendono dai loro membri: il limite si controlla dopo la scrittura.
	if in.Quorum != nil && (in.Groups == nil || len(*in.Groups) == 0) && *in.Quorum > voting {
		return engine.OutOfRange("quorum", 1, voting, "il quorum va da 1 al numero dei membri votanti (%d)", voting)
	}
	return nil
}

func (s *Service) writeMembers(ctx context.Context, tx pgx.Tx, bodyID string, in BodyInput) error {
	for _, id := range append([]string{in.ChairID, in.SecretaryID}, memberIDs(in.Members)...) {
		if id == "" {
			continue
		}
		var ok bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from members where id::text = $1 and status = 'active')", id).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return engine.InvalidCode("validation.member_not_found", "members", "membro inesistente o non attivo")
		}
	}
	if _, err := tx.Exec(ctx, "delete from governance_body_members where body_id = $1", bodyID); err != nil {
		return err
	}
	for _, m := range in.Members {
		voting := m.Voting == nil || *m.Voting
		if _, err := tx.Exec(ctx, `insert into governance_body_members (tenant_id, body_id, member_id, voting) values (app_tenant(), $1, $2, $3)`,
			bodyID, m.MemberID, voting); err != nil {
			return err
		}
	}
	return nil
}

func memberIDs(ms []MemberInput) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.MemberID
	}
	return out
}

// CreateBody crea un organo (permesso portfolio.admin).
func (s *Service) CreateBody(ctx context.Context, tx pgx.Tx, actor engine.Actor, in BodyInput) (*Body, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	if err := in.normalize(); err != nil {
		return nil, err
	}
	id := db.NewID()
	if err := s.writeBody(ctx, tx, id, in, true); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "governance.body.create", EntityType: "governance_body", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"kind": in.Kind, "members": len(in.Members), "quorum": in.Quorum}}); err != nil {
		return nil, err
	}
	return s.body(ctx, tx, actor, id)
}

// UpdateBody modifica un organo (permesso portfolio.admin).
func (s *Service) UpdateBody(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in BodyInput) (*Body, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	if err := in.normalize(); err != nil {
		return nil, err
	}
	var ok bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from governance_bodies where id::text = $1)", id).Scan(&ok); err != nil {
		return nil, err
	}
	if !ok {
		return nil, engine.ErrNotFound
	}
	if err := s.writeBody(ctx, tx, id, in, false); err != nil {
		return nil, err
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "governance.body.update", EntityType: "governance_body", EntityID: id,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"kind": in.Kind, "members": len(in.Members), "quorum": in.Quorum, "archived": in.Archived, "inline_decision": in.InlineDecision}}); err != nil {
		return nil, err
	}
	return s.body(ctx, tx, actor, id)
}

func (s *Service) writeBody(ctx context.Context, tx pgx.Tx, id string, in BodyInput, create bool) error {
	q := `update governance_bodies set name = $2, kind = $3, description = $4, chair_id = $5, secretary_id = $6, quorum = $7,
		approval_limit_cents = $8, archived = $9, inline_decision = coalesce($10, inline_decision) where id = $1`
	if create {
		q = `insert into governance_bodies (tenant_id, id, name, kind, description, chair_id, secretary_id, quorum, approval_limit_cents, archived, inline_decision)
			values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, coalesce($10, true))`
	}
	if _, err := tx.Exec(ctx, q, id, in.Name, in.Kind, in.Description, in.ChairID, nullable(in.SecretaryID), in.Quorum,
		in.ApprovalLimitCents, in.Archived, in.InlineDecision); err != nil {
		if strings.Contains(err.Error(), "foreign key") {
			return engine.NotFound("chair_id", "", "presidente o segretario inesistente")
		}
		return err
	}
	if err := s.writeMembers(ctx, tx, id, in); err != nil {
		return err
	}
	if in.Groups != nil {
		if _, err := tx.Exec(ctx, "delete from governance_body_groups where body_id = $1", id); err != nil {
			return err
		}
		seen := map[string]bool{}
		for _, g := range *in.Groups {
			if g.GroupID == "" || seen[g.GroupID] {
				continue
			}
			seen[g.GroupID] = true
			voting := g.Voting == nil || *g.Voting
			if _, err := tx.Exec(ctx, `insert into governance_body_groups (tenant_id, body_id, group_id, voting)
				select app_tenant(), $1, g.id, $3 from groups g where g.id::text = $2`, id, g.GroupID, voting); err != nil {
				return err
			}
		}
		if len(seen) > 0 {
			var n int
			if err := tx.QueryRow(ctx, "select count(*) from governance_body_groups where body_id = $1", id).Scan(&n); err != nil {
				return err
			}
			if n != len(seen) {
				return engine.NotFound("groups", "group", "gruppo inesistente")
			}
		}
	}
	if in.Quorum != nil {
		var voting int
		if err := tx.QueryRow(ctx, "select count(*) from body_effective_members($1) where voting", id).Scan(&voting); err != nil {
			return err
		}
		if *in.Quorum > voting {
			return engine.OutOfRange("quorum", 1, voting, "il quorum va da 1 al numero dei membri votanti (%d)", voting)
		}
	}
	return nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
