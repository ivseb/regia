package requirements

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/engine/notify"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// Il requisito: vista, creazione manuale, modifica del criterio, celle (pronta per la
// verifica, verifica con evidenze e firma), inbox dei verificatori.

// analystRoles possono creare requisiti sulla richiesta (come le valutazioni).
var analystRoles = []string{"demand_manager", "pmo", "project_manager", "architect", "technical_analyst"}

// canCreate: element.write, un ruolo di analisi sulla richiesta, oppure il referente del
// team sull'impatto (per i requisiti del suo impatto).
func (s *Service) canCreate(ctx context.Context, tx pgx.Tx, actor engine.Actor, req, el *engine.Element) (bool, error) {
	if actor.Has(rbac.ElementWrite) {
		return true, nil
	}
	if ok, err := engine.HasRole(ctx, tx, req.ID, actor.MemberID, analystRoles); err != nil || ok {
		return ok, err
	}
	if el != nil && el.TypeKey == ImpactType {
		return engine.HasRole(ctx, tx, el.ID, actor.MemberID, []string{"team_lead"})
	}
	return false, nil
}

// implementer: il membro realizza la cella (l'ha segnata come pronta, è referente del
// team dell'impatto, ne è responsabile o membro). Serve alla separazione dei compiti.
func implementer(ctx context.Context, tx pgx.Tx, impactID, memberID string, implementedBy *string) (bool, error) {
	if memberID == "" {
		return false, nil
	}
	if implementedBy != nil && *implementedBy == memberID {
		return true, nil
	}
	var ok bool
	err := tx.QueryRow(ctx, `select exists (select 1 from ext_impact x join teams t on t.tenant_id = x.tenant_id and t.id = x.team_id
			where x.element_id = $1 and (t.lead_id = $2 or exists (select 1 from team_members tm where tm.tenant_id = t.tenant_id and tm.team_id = t.id and tm.member_id = $2)))
		or exists (select 1 from stakeholders sh join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id
			where sh.element_id = $1 and r.key = 'team_lead'
			  and (sh.member_id = $2 or sh.group_id in (select member_groups($2))))`,
		impactID, memberID).Scan(&ok)
	return ok, err
}

// ---------------------------------------------------------------------------
// Vista del requisito

// Info sono i dati del requisito.
type Info struct {
	ID            string  `json:"id"`
	Key           string  `json:"key"`
	Title         string  `json:"title"`
	Text          string  `json:"text"`
	Scope         string  `json:"scope"`
	Origin        string  `json:"origin"`
	Category      string  `json:"category"`
	Stage         string  `json:"stage"`
	Phase         string  `json:"phase"`
	RequestID     string  `json:"request_id"`
	RequestKey    string  `json:"request_key"`
	RequestTitle  string  `json:"request_title"`
	ImpactKey     *string `json:"impact_key"`
	Application   *string `json:"application"`
	ReviewKey     *string `json:"review_key"`
	ReviewTitle   *string `json:"review_title"`
	Prescription  *string `json:"prescription"`
	CatalogKey    *string `json:"catalog_key"`
	CatalogName   *string `json:"catalog_name"`
	CatalogVer    *int    `json:"catalog_version"`
	CatalogItem   *string `json:"catalog_item"`
	BaselineKey   *string `json:"baseline_key"`
	Method        string  `json:"verification_method"`
	Check         *string `json:"verification_check"`
	Criterion     string  `json:"verification_criterion"`
	VerifierTeam  *string `json:"verifier_team"`
	VerifierTeamK *string `json:"verifier_team_key"`
	VerifierRole  *string `json:"verifier_role"`
	VerifierRoleN *string `json:"verifier_role_name"`
	CreatedAt     string  `json:"created_at"`
}

const selectInfo = `
	select x.element_id::text, s.key || '-' || e.number, e.title, e.body_text, x.scope, x.origin, x.category, x.stage, coalesce(o.name, ''),
	       x.request_id::text, rs.key || '-' || re.number, re.title,
	       (select ss.key || '-' || ie.number from elements ie join spaces ss on ss.tenant_id = ie.tenant_id and ss.id = ie.space_id where ie.id = x.impact_id),
	       (select ap.title from ext_impact xi join elements ap on ap.tenant_id = xi.tenant_id and ap.id = xi.application_id where xi.element_id = x.impact_id),
	       (select ss.key || '-' || ve.number from elements ve join spaces ss on ss.tenant_id = ve.tenant_id and ss.id = ve.space_id where ve.id = x.review_id),
	       (select ve.title from elements ve where ve.id = x.review_id),
	       (select p.text from review_prescriptions p where p.id = x.prescription_id),
	       x.catalog_key, (select c.name from requirement_catalogs c where c.key = x.catalog_key and c.version = x.catalog_version),
	       x.catalog_version, x.catalog_item_key, x.baseline_key,
	       x.verification_method, x.verification_check, x.verification_criterion,
	       t.name, t.key, x.verifier_role, (select r.name from stakeholder_roles r where r.key = x.verifier_role),
	       to_char(x.created_at at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
	from ext_requirement x
	join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
	join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
	left join options o on o.tenant_id = e.tenant_id and o.id = e.state_id
	join elements re on re.tenant_id = x.tenant_id and re.id = x.request_id
	join spaces rs on rs.tenant_id = re.tenant_id and rs.id = re.space_id
	left join review_teams t on t.tenant_id = x.tenant_id and t.id = x.verifier_team_id
	where x.element_id = $1`

func loadInfo(ctx context.Context, tx pgx.Tx, id string) (*Info, error) {
	rows, err := tx.Query(ctx, selectInfo, id)
	if err != nil {
		return nil, err
	}
	in, err := pgx.CollectOneRow(rows, pgx.RowToAddrOfStructByPos[Info])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, engine.ErrNotFound
	}
	return in, err
}

// CellCan sono le azioni dell'attore su una cella.
type CellCan struct {
	Ready     bool   `json:"ready"`
	Verify    bool   `json:"verify"`
	Exception bool   `json:"exception"`
	WhyNot    string `json:"why_not,omitempty"` // perché non può verificare (separazione dei compiti)
}

// Cell è una cella del requisito (un impatto) con lo stato e chi l'ha realizzata o verificata.
type Cell struct {
	ImpactID         string     `json:"impact_id"`
	ImpactKey        string     `json:"impact_key"`
	Application      string     `json:"application"`
	Team             string     `json:"team"`
	Status           string     `json:"status"`
	StatusLabel      string     `json:"status_label"`
	ExceptionKey     string     `json:"exception_key,omitempty"`
	ExpiresOn        *string    `json:"expires_on,omitempty"`
	ExceptionExpired bool       `json:"exception_expired,omitempty"`
	ImplementedBy    *string    `json:"implemented_by"`
	ImplementedAt    *time.Time `json:"implemented_at"`
	VerifiedBy       *string    `json:"verified_by"`
	VerifiedAt       *time.Time `json:"verified_at"`
	Can              CellCan    `json:"can"`
}

// VerificationView è una verifica registrata, con le evidenze e la firma.
type VerificationView struct {
	ID          string     `json:"id"`
	ImpactKey   string     `json:"impact_key"`
	Outcome     string     `json:"outcome"`
	Method      string     `json:"method"`
	Collector   *string    `json:"collector"`
	Note        string     `json:"note"`
	Links       []string   `json:"links"`
	Attachments []Evidence `json:"attachments"`
	EvidenceKey *string    `json:"evidence_key"`
	EvidenceTtl *string    `json:"evidence_title"`
	By          *string    `json:"by"`
	At          time.Time  `json:"at"`
	Signature   string     `json:"signature"`
	AttIDs      []string   `json:"-"`
}

// Evidence è un allegato citato da una verifica.
type Evidence struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
}

// ExceptionSummary è una deroga del requisito.
type ExceptionSummary struct {
	ID        string    `json:"id"`
	Key       string    `json:"key"`
	Stage     string    `json:"stage"`
	Phase     string    `json:"phase"`
	RiskLevel string    `json:"risk_level"`
	RiskName  string    `json:"risk_name"`
	ExpiresOn string    `json:"expires_on"`
	Impacts   []string  `json:"impacts"`
	CreatedAt time.Time `json:"created_at"`
}

// View è la pagina del requisito.
type View struct {
	Requirement   *Info              `json:"requirement"`
	Element       *engine.Element    `json:"element"`
	Cells         []Cell             `json:"cells"`
	Verifications []VerificationView `json:"verifications"`
	Exceptions    []ExceptionSummary `json:"exceptions"`
	Settings      *Settings          `json:"settings"`
	Can           struct {
		Edit      bool `json:"edit"`
		Verify    bool `json:"verify"` // è tra i verificatori
		Exception bool `json:"exception"`
	} `json:"can"`
}

// resolve trova il requisito per chiave o id, verificando che l'attore lo veda.
func (s *Service) resolve(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*engine.Element, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	if el.TypeKey != TypeKey {
		return nil, engine.ErrNotFound
	}
	return el, nil
}

// View restituisce il requisito con celle, verifiche, deroghe e permessi dell'attore.
func (s *Service) View(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string) (*View, error) {
	el, err := s.resolve(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	info, err := loadInfo(ctx, tx, el.ID)
	if err != nil {
		return nil, err
	}
	v := &View{Requirement: info, Element: el, Cells: []Cell{}, Verifications: []VerificationView{}, Exceptions: []ExceptionSummary{}}
	if v.Settings, err = loadSettings(ctx, tx); err != nil {
		return nil, err
	}
	rows, err := s.loadCells(ctx, tx, "x.element_id = $1", el.ID)
	if err != nil {
		return nil, err
	}
	owner, err := engine.HasRole(ctx, tx, el.ID, actor.MemberID, []string{RoleOwner})
	if err != nil {
		return nil, err
	}
	verifier, err := engine.HasRole(ctx, tx, el.ID, actor.MemberID, []string{RoleVerifier})
	if err != nil {
		return nil, err
	}
	pending, err := pendingExceptionImpacts(ctx, tx, el.ID, s.today())
	if err != nil {
		return nil, err
	}
	today := s.today()
	writer := actor.Has(rbac.ElementWrite)
	for _, r := range rows {
		if !r.hasCell() || r.ImpactCat == "cancelled" {
			continue
		}
		st := r.status(today)
		c := Cell{ImpactID: r.ImpactID, ImpactKey: r.ImpactKey, Application: r.Application, Team: r.Team, Status: st,
			StatusLabel: StatusLabels[st], ExceptionKey: r.ExceptionKey, ExceptionExpired: r.exceptionExpired(today),
			ImplementedBy: r.ImplementedBy, ImplementedAt: r.ImplementedAt, VerifiedBy: r.VerifiedBy, VerifiedAt: r.VerifiedAt}
		if r.ExpiresOn != nil {
			d := r.ExpiresOn.Format(time.DateOnly)
			c.ExpiresOn = &d
		}
		impl, err := implementer(ctx, tx, r.ImpactID, actor.MemberID, r.ImplementedByID)
		if err != nil {
			return nil, err
		}
		teamSide := owner || writer || impl
		c.Can.Ready = teamSide && st == "open" && slices.Contains([]string{StageAccepted, StageImplementing, StageToVerify, StageVerified, StageWaived}, r.Stage)
		c.Can.Exception = teamSide && (st == "proposed" || st == "open" || st == "to_verify") && !pending[r.ImpactID]
		if st == "to_verify" && verifier {
			if impl {
				c.Can.WhyNot = "hai realizzato o fai parte del team che realizza: la verifica spetta a un altro verificatore"
			} else {
				c.Can.Verify = true
			}
		}
		v.Can.Exception = v.Can.Exception || c.Can.Exception
		v.Cells = append(v.Cells, c)
	}
	v.Can.Edit = (owner || writer) && slices.Contains([]string{StageProposed, StageAccepted}, info.Stage)
	v.Can.Verify = verifier
	if v.Verifications, err = verifications(ctx, tx, el.ID); err != nil {
		return nil, err
	}
	if v.Exceptions, err = exceptionsOf(ctx, tx, el.ID, v.Settings); err != nil {
		return nil, err
	}
	return v, nil
}

func verifications(ctx context.Context, tx pgx.Tx, requirementID string) ([]VerificationView, error) {
	rows, err := tx.Query(ctx, `select v.id::text, s.key || '-' || ie.number, v.outcome, v.method, v.collector, v.note, v.links, v.attachment_ids::text[],
		(select ss.key || '-' || ee.number from elements ee join spaces ss on ss.tenant_id = ee.tenant_id and ss.id = ee.space_id where ee.id = v.evidence_element_id),
		(select ee.title from elements ee where ee.id = v.evidence_element_id),
		m.display_name, v.verified_at, v.signature
		from requirement_verifications v
		join elements ie on ie.tenant_id = v.tenant_id and ie.id = v.impact_id
		join spaces s on s.tenant_id = ie.tenant_id and s.id = ie.space_id
		left join members m on m.tenant_id = v.tenant_id and m.id = v.verified_by
		where v.requirement_id = $1 order by v.verified_at desc, v.id`, requirementID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (VerificationView, error) {
		var v VerificationView
		err := r.Scan(&v.ID, &v.ImpactKey, &v.Outcome, &v.Method, &v.Collector, &v.Note, &v.Links, &v.AttIDs, &v.EvidenceKey, &v.EvidenceTtl, &v.By, &v.At, &v.Signature)
		return v, err
	})
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Attachments = []Evidence{}
		if len(out[i].AttIDs) == 0 {
			continue
		}
		rows, err := tx.Query(ctx, `select id::text, name, encode(sha256, 'hex') from attachments where id = any($1::uuid[]) order by name`, out[i].AttIDs)
		if err != nil {
			return nil, err
		}
		if out[i].Attachments, err = pgx.CollectRows(rows, pgx.RowToStructByPos[Evidence]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// Creazione manuale e modifica

// CreateInput è un requisito inserito a mano.
type CreateInput struct {
	Title        string `json:"title"`
	Text         string `json:"text"`
	Category     string `json:"category"`
	Scope        string `json:"scope"`  // project | application
	Impact       string `json:"impact"` // per l'ambito di applicazione da una richiesta
	Method       string `json:"verification_method"`
	Check        string `json:"verification_check"`
	Criterion    string `json:"verification_criterion"`
	VerifierTeam string `json:"verifier_team"` // chiave o id del team valutatore
	VerifierRole string `json:"verifier_role"`
}

func (in *CreateInput) verifier(ctx context.Context, tx pgx.Tx) (string, error) {
	v := Verification{Method: in.Method, Check: in.Check, Criterion: in.Criterion, Team: in.VerifierTeam, Role: in.VerifierRole}
	if v.Method == "" {
		v.Method = "manual"
		in.Method = "manual"
	}
	var msgs []string
	v.validate("verification", func(_, msg string) { msgs = append(msgs, msg) })
	if len(msgs) > 0 {
		return "", engine.InvalidParams("validation.requirements.verification_error", "verification", map[string]any{"detail": msgs[0]}, "%s", msgs[0])
	}
	if in.VerifierRole != "" {
		var ok bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from stakeholder_roles where key = $1)", in.VerifierRole).Scan(&ok); err != nil {
			return "", err
		}
		if !ok {
			return "", engine.InvalidCode("validation.role_not_found", "verifier_role", "ruolo inesistente")
		}
		return "", nil
	}
	var id string
	err := tx.QueryRow(ctx, "select id::text from review_teams where (key = $1 or id::text = $1) and not archived", in.VerifierTeam).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", engine.NotFound("verifier_team", "team", "team valutatore inesistente")
	}
	return id, err
}

// Create inserisce a mano un requisito su una richiesta (di progetto, o di applicazione
// indicando l'impatto) o su un impatto (di applicazione).
func (s *Service) Create(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in CreateInput) (*View, error) {
	el, err := s.eng.Get(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	sys := reader(actor.MemberID)
	var req, imp *engine.Element
	switch el.TypeKey {
	case RequestType:
		req = el
		if in.Scope == "application" {
			if in.Impact == "" {
				return nil, engine.Required("impact", "indica l'impatto (applicazione) del requisito")
			}
			if imp, err = s.eng.Get(ctx, tx, actor, in.Impact); err != nil {
				return nil, err
			}
			if imp.TypeKey != ImpactType || deref(imp.ParentID) != req.ID {
				return nil, engine.InvalidCode("validation.requirements.impact_not_of_request", "impact", "l'impatto non è di questa richiesta")
			}
		} else if in.Scope != "" && in.Scope != "project" {
			return nil, engine.InvalidValue("scope", "ambito non ammesso (project, application)")
		}
	case ImpactType:
		imp = el
		if req, err = s.eng.Get(ctx, tx, sys, deref(el.ParentID)); err != nil {
			return nil, err
		}
		if in.Scope == "project" {
			imp = nil
		}
	default:
		return nil, engine.InvalidCode("validation.requirements.element_kind", "element", "i requisiti si creano su richieste e impatti")
	}
	ok, err := s.canCreate(ctx, tx, actor, req, el)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, engine.ErrForbidden
	}
	in.Title, in.Text = strings.TrimSpace(in.Title), strings.TrimSpace(in.Text)
	if in.Title == "" && in.Text != "" {
		in.Title = titleOf(in.Text)
	}
	if in.Title == "" || len(in.Title) > 500 {
		return nil, engine.TooLong("title", 500, "titolo obbligatorio (massimo 500 caratteri)")
	}
	if len(in.Text) > 10000 || len(in.Category) > 120 {
		return nil, engine.InvalidCode("validation.requirements.text_too_long", "text", "testo o categoria troppo lunghi")
	}
	teamID, err := in.verifier(ctx, tx)
	if err != nil {
		return nil, err
	}
	r, err := s.create(ctx, tx, actor, req, imp, NewRequirement{Title: in.Title, Text: in.Text, Category: in.Category, Origin: "manual",
		Method: in.Method, Check: in.Check, Criterion: in.Criterion, VerifierTeamID: teamID, VerifierRole: in.VerifierRole})
	if err != nil {
		return nil, err
	}
	return s.View(ctx, tx, actor, r.ID)
}

// UpdateInput modifica categoria e criterio di verifica (titolo e testo si modificano
// sull'elemento). Solo finché il requisito è proposto o accettato.
type UpdateInput struct {
	Category     *string `json:"category"`
	Method       *string `json:"verification_method"`
	Check        *string `json:"verification_check"`
	Criterion    *string `json:"verification_criterion"`
	VerifierTeam *string `json:"verifier_team"`
	VerifierRole *string `json:"verifier_role"`
}

// Update modifica il criterio di verifica del requisito.
func (s *Service) Update(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref string, in UpdateInput) (*View, error) {
	el, err := s.resolve(ctx, tx, actor, ref)
	if err != nil {
		return nil, err
	}
	info, err := loadInfo(ctx, tx, el.ID)
	if err != nil {
		return nil, err
	}
	owner, err := engine.HasRole(ctx, tx, el.ID, actor.MemberID, []string{RoleOwner})
	if err != nil {
		return nil, err
	}
	if !owner && !actor.Has(rbac.ElementWrite) {
		return nil, engine.ErrForbidden
	}
	if info.Stage != StageProposed && info.Stage != StageAccepted {
		return nil, engine.InvalidCode("validation.requirements.verification_after_stage", "stage", "il criterio di verifica si cambia solo prima della realizzazione")
	}
	ci := CreateInput{Method: info.Method, Check: deref(info.Check), Criterion: info.Criterion, VerifierTeam: deref(info.VerifierTeamK), VerifierRole: deref(info.VerifierRole)}
	set := func(dst *string, v *string) {
		if v != nil {
			*dst = strings.TrimSpace(*v)
		}
	}
	set(&ci.Method, in.Method)
	set(&ci.Check, in.Check)
	set(&ci.Criterion, in.Criterion)
	if in.VerifierTeam != nil || in.VerifierRole != nil {
		ci.VerifierTeam, ci.VerifierRole = "", ""
		set(&ci.VerifierTeam, in.VerifierTeam)
		set(&ci.VerifierRole, in.VerifierRole)
	}
	if ci.Method == "manual" {
		ci.Check = ""
	}
	teamID, err := ci.verifier(ctx, tx)
	if err != nil {
		return nil, err
	}
	category := info.Category
	set(&category, in.Category)
	if _, err := tx.Exec(ctx, `update ext_requirement set category = $2, verification_method = $3, verification_check = $4, verification_criterion = $5,
		verifier_team_id = $6, verifier_role = $7 where element_id = $1`,
		el.ID, category, ci.Method, nullable(ci.Check), ci.Criterion, nullable(teamID), nullable(ci.VerifierRole)); err != nil {
		return nil, err
	}
	// Chi verifica cambia: si riassegna il ruolo.
	if in.VerifierTeam != nil || in.VerifierRole != nil {
		if _, err := tx.Exec(ctx, `delete from stakeholders where element_id = $1 and role_id = (select id from stakeholder_roles where key = $2)`, el.ID, RoleVerifier); err != nil {
			return nil, err
		}
		if teamID != "" {
			var group string
			if err := tx.QueryRow(ctx, "select group_id::text from review_teams where id = $1", teamID).Scan(&group); err != nil {
				return nil, err
			}
			if err := setRole(ctx, tx, actor, el.ID, RoleVerifier, "", group); err != nil {
				return nil, err
			}
		} else {
			var impID *string
			if err := tx.QueryRow(ctx, "select impact_id::text from ext_requirement where element_id = $1", el.ID).Scan(&impID); err != nil {
				return nil, err
			}
			hs, err := holdersOf(ctx, tx, []string{ci.VerifierRole}, deref(impID), info.RequestID)
			if err != nil {
				return nil, err
			}
			for _, h := range hs {
				if err := setRole(ctx, tx, actor, el.ID, RoleVerifier, h.member, h.group); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "requirement", "criterion",
		map[string]any{"category": category, "method": ci.Method, "criterion": ci.Criterion}, nil); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "requirement.update", TypeKey, el.ID, map[string]any{"method": ci.Method, "verifier_team": teamID, "verifier_role": ci.VerifierRole}); err != nil {
		return nil, err
	}
	return s.View(ctx, tx, actor, el.ID)
}

// ---------------------------------------------------------------------------
// Celle: pronta per la verifica, verifica

// cell carica una cella bloccandola per la modifica.
func (s *Service) cell(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, impactRef string) (*engine.Element, *cellRow, error) {
	el, err := s.resolve(ctx, tx, actor, ref)
	if err != nil {
		return nil, nil, err
	}
	imp, err := s.eng.Get(ctx, tx, reader(actor.MemberID), impactRef)
	if err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(ctx, "select 1 from applicability where requirement_element_id = $1 and target_element_id = $2 for update", el.ID, imp.ID); err != nil {
		return nil, nil, err
	}
	rows, err := s.loadCells(ctx, tx, "x.element_id = $1 and c.target_element_id = $2", el.ID, imp.ID)
	if err != nil {
		return nil, nil, err
	}
	if len(rows) == 0 || !rows[0].hasCell() || rows[0].ImpactCat == "cancelled" {
		return nil, nil, engine.InvalidCode("validation.requirements.not_for_impact", "impact", "il requisito non si applica a questo impatto")
	}
	return el, &rows[0], nil
}

// ReadyInput segnala che la cella è realizzata e pronta per la verifica.
type ReadyInput struct {
	Note string `json:"note"`
}

// Ready: chi realizza (il team dell'impatto, il responsabile del requisito, element.write)
// segna la cella come realizzata; i verificatori ricevono la notifica.
func (s *Service) Ready(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, impactRef string, in ReadyInput) (*View, error) {
	el, c, err := s.cell(ctx, tx, actor, ref, impactRef)
	if err != nil {
		return nil, err
	}
	owner, err := engine.HasRole(ctx, tx, el.ID, actor.MemberID, []string{RoleOwner})
	if err != nil {
		return nil, err
	}
	impl, err := implementer(ctx, tx, c.ImpactID, actor.MemberID, nil)
	if err != nil {
		return nil, err
	}
	if !owner && !impl && !actor.Has(rbac.ElementWrite) {
		return nil, engine.ErrForbidden
	}
	switch st := c.status(s.today()); {
	case st == "proposed":
		return nil, engine.InvalidCode("validation.requirements.accept_first", "stage", "il requisito va prima accettato")
	case st != "open":
		return nil, engine.InvalidParams("validation.requirements.cell_already", "status", map[string]any{"status": strings.ToLower(StatusLabels[st])}, "la cella è già %s", strings.ToLower(StatusLabels[st]))
	}
	note := strings.TrimSpace(in.Note)
	if len(note) > 5000 {
		return nil, engine.InvalidCode("validation.note_too_long", "note", "nota troppo lunga")
	}
	if _, err := tx.Exec(ctx, `update applicability set progress = 'to_verify', implemented_by = $3, implemented_at = now(), implementation_note = $4, updated_at = now()
		where requirement_element_id = $1 and target_element_id = $2`, el.ID, c.ImpactID, nullable(actor.MemberID), note); err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "requirement", "ready", map[string]any{"impact": c.ImpactKey, "note": note}, nil); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "requirement.ready", TypeKey, el.ID, map[string]any{"impact": c.ImpactID}); err != nil {
		return nil, err
	}
	if err := s.syncPhase(ctx, tx, actor, el.ID); err != nil {
		return nil, err
	}
	if err := s.notifyRole(ctx, tx, actor, el.ID, RoleVerifier, map[string]any{"event": "to_verify", "impact": c.ImpactKey, "application": c.Application}); err != nil {
		return nil, err
	}
	return s.View(ctx, tx, actor, el.ID)
}

// VerifyInput è la verifica di una cella: esito, evidenze e nota.
type VerifyInput struct {
	Outcome     string   `json:"outcome"` // verified | rejected
	Note        string   `json:"note"`
	Links       []string `json:"links"`
	Attachments []string `json:"attachments"`      // id degli allegati del requisito
	Evidence    string   `json:"evidence_element"` // chiave o id di un elemento (facoltativo)
}

// Verify registra la verifica firmata di una cella. La può fare solo un verificatore del
// requisito che non l'ha realizzata e non fa parte del team dell'impatto (separazione dei
// compiti). Esito positivo: la cella è verificata; negativo (con motivazione): torna da
// realizzare.
func (s *Service) Verify(ctx context.Context, tx pgx.Tx, actor engine.Actor, ref, impactRef string, in VerifyInput) (*View, error) {
	el, c, err := s.cell(ctx, tx, actor, ref, impactRef)
	if err != nil {
		return nil, err
	}
	verifier, err := engine.HasRole(ctx, tx, el.ID, actor.MemberID, []string{RoleVerifier})
	if err != nil {
		return nil, err
	}
	if !verifier {
		return nil, engine.ErrForbidden
	}
	impl, err := implementer(ctx, tx, c.ImpactID, actor.MemberID, c.ImplementedByID)
	if err != nil {
		return nil, err
	}
	if impl {
		return nil, errSoD
	}
	if st := c.status(s.today()); st != "to_verify" {
		return nil, engine.InvalidParams("validation.requirements.cell_not_ready", "status", map[string]any{"status": strings.ToLower(StatusLabels[st])}, "si verifica una cella pronta per la verifica (ora è %s)", strings.ToLower(StatusLabels[st]))
	}
	in.Note = strings.TrimSpace(in.Note)
	switch in.Outcome {
	case "verified":
	case "rejected":
		if in.Note == "" {
			return nil, engine.Required("note", "per una verifica non superata serve la motivazione")
		}
	default:
		return nil, engine.InvalidValue("outcome", "esito non ammesso (verified, rejected)")
	}
	if len(in.Note) > 10000 {
		return nil, engine.InvalidCode("validation.note_too_long", "note", "nota troppo lunga")
	}
	links := []string{}
	for _, l := range in.Links {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		u, err := url.Parse(l)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(l) > 2000 {
			return nil, engine.InvalidValue("links", "link non valido: %s (serve http o https)", l)
		}
		links = append(links, l)
	}
	if len(links) > 20 || len(in.Attachments) > 20 {
		return nil, engine.InvalidCode("validation.requirements.evidence_too_many", "evidence", "al massimo 20 link e 20 allegati per verifica")
	}
	type att struct{ id, sha string }
	var atts []att
	if len(in.Attachments) > 0 {
		rows, err := tx.Query(ctx, `select id::text, encode(sha256, 'hex') from attachments
			where element_id = $1 and id::text = any($2) and removed_at is null order by id`, el.ID, in.Attachments)
		if err != nil {
			return nil, err
		}
		var id, sha string
		if _, err := pgx.ForEachRow(rows, []any{&id, &sha}, func() error { atts = append(atts, att{id, sha}); return nil }); err != nil {
			return nil, err
		}
		if len(atts) != len(slices.Compact(slices.Sorted(slices.Values(in.Attachments)))) {
			return nil, engine.InvalidCode("validation.requirements.attachments_not_of_requirement", "attachments", "gli allegati devono essere del requisito")
		}
	}
	var evidenceID string
	if strings.TrimSpace(in.Evidence) != "" {
		ev, err := s.eng.Get(ctx, tx, actor, strings.TrimSpace(in.Evidence))
		if err != nil {
			return nil, engine.NotFound("evidence_element", "", "elemento dell'evidenza inesistente")
		}
		evidenceID = ev.ID
	}
	if in.Outcome == "verified" && in.Note == "" && len(links) == 0 && len(atts) == 0 && evidenceID == "" {
		return nil, engine.Required("evidence", "serve almeno un'evidenza: una nota, un link, un allegato o un elemento")
	}
	id := db.NewID()
	at := s.Now().UTC().Truncate(time.Microsecond)
	// Firma: SHA-256 del contenuto verificato, di chi lo firma e del momento.
	attIDs, attSHA := make([]string, len(atts)), make([]string, len(atts))
	for i, a := range atts {
		attIDs[i], attSHA[i] = a.id, a.sha
	}
	signed, _ := json.Marshal(map[string]any{"verification": id, "requirement": el.ID, "impact": c.ImpactID, "outcome": in.Outcome,
		"note": in.Note, "links": links, "attachments": attSHA, "evidence": evidenceID, "verifier": actor.MemberID,
		"at": at.Format(time.RFC3339Nano)})
	sum := sha256.Sum256(signed)
	signature := hex.EncodeToString(sum[:])
	if _, err := tx.Exec(ctx, `insert into requirement_verifications (tenant_id, id, requirement_id, impact_id, outcome, method, note, links,
		attachment_ids, evidence_element_id, verified_by, verified_at, signature)
		values (app_tenant(), $1, $2, $3, $4, 'manual', $5, $6, $7::uuid[], $8, $9, $10, $11)`,
		id, el.ID, c.ImpactID, in.Outcome, in.Note, links, attIDs, nullable(evidenceID), nullable(actor.MemberID), at, signature); err != nil {
		return nil, err
	}
	if in.Outcome == "verified" {
		_, err = tx.Exec(ctx, `update applicability set progress = 'verified', verified_by = $3, verified_at = $4, last_verification_id = $5, updated_at = now()
			where requirement_element_id = $1 and target_element_id = $2`, el.ID, c.ImpactID, nullable(actor.MemberID), at, id)
	} else {
		_, err = tx.Exec(ctx, `update applicability set progress = 'open', last_verification_id = $3, updated_at = now()
			where requirement_element_id = $1 and target_element_id = $2`, el.ID, c.ImpactID, id)
	}
	if err != nil {
		return nil, err
	}
	if err := engine.AddActivity(ctx, tx, el.ID, actor.MemberID, "verification", in.Outcome,
		map[string]any{"impact": c.ImpactKey, "signature": signature, "links": len(links), "attachments": len(atts)}, nil); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "requirement.verify", TypeKey, el.ID, map[string]any{"verification": id, "impact": c.ImpactID,
		"outcome": in.Outcome, "signature": signature, "attachments": attSHA, "evidence": evidenceID}); err != nil {
		return nil, err
	}
	if err := s.syncPhase(ctx, tx, actor, el.ID); err != nil {
		return nil, err
	}
	members := []string{deref(c.ImplementedByID)}
	if err := s.notifyRole(ctx, tx, actor, el.ID, RoleOwner, map[string]any{"event": in.Outcome, "impact": c.ImpactKey}, members...); err != nil {
		return nil, err
	}
	return s.View(ctx, tx, actor, el.ID)
}

// errSoD: separazione dei compiti sulla verifica.
var errSoD = fmt.Errorf("%w: separazione dei compiti, chi realizza il requisito (o fa parte del team dell'impatto) non lo verifica", engine.ErrForbidden)

// notifyRole avvisa i titolari di un ruolo sull'elemento (più i membri indicati).
func (s *Service) notifyRole(ctx context.Context, tx pgx.Tx, actor engine.Actor, elementID, role string, payload map[string]any, members ...string) error {
	hs, err := holdersOf(ctx, tx, []string{role}, elementID)
	if err != nil {
		return err
	}
	var groups []string
	for _, h := range hs {
		members, groups = append(members, h.member), append(groups, h.group)
	}
	_, err = notify.Emit(ctx, tx, notify.Notice{Kind: notify.KindRequirement, ElementID: elementID, ActorID: actor.MemberID,
		Members: members, Groups: groups, Payload: payload})
	return err
}

// ---------------------------------------------------------------------------
// Inbox dei verificatori

// InboxItem è una cella da verificare.
type InboxItem struct {
	RequirementID  string    `json:"requirement_id"`
	RequirementKey string    `json:"requirement_key"`
	Title          string    `json:"title"`
	Category       string    `json:"category"`
	Scope          string    `json:"scope"`
	ImpactKey      string    `json:"impact_key"`
	Application    string    `json:"application"`
	Team           string    `json:"team"`
	RequestKey     string    `json:"request_key"`
	RequestTitle   string    `json:"request_title"`
	ImplementedBy  *string   `json:"implemented_by"`
	Since          time.Time `json:"since"`
}

// Inbox: le celle da verificare per cui l'attore è verificatore e che non ha realizzato
// (né il suo team), dalla più vecchia.
func (s *Service) Inbox(ctx context.Context, tx pgx.Tx, actor engine.Actor) ([]InboxItem, error) {
	items, _, err := s.InboxN(ctx, tx, actor, InboxLimit)
	return items, err
}

// InboxLimit: celle della coda per risposta se il client non chiede altro; InboxMax è il tetto.
// Oltre il limite la risposta porta more e l'interfaccia chiede il resto.
const (
	InboxLimit = 500
	InboxMax   = 5000
)

// InboxN è Inbox con il numero di voci (0 = InboxLimit) e se la coda ne ha altre.
func (s *Service) InboxN(ctx context.Context, tx pgx.Tx, actor engine.Actor, limit int) ([]InboxItem, bool, error) {
	if limit <= 0 {
		limit = InboxLimit
	}
	limit = min(limit, InboxMax)
	// Il menu chiede questa coda a ogni pagina: chi non è verificatore di nulla (quasi tutti) non
	// paga il join a 13 tabelle.
	// Il ruolo si risolve prima: con il join Postgres leggeva tutte le assegnazioni dell'attore
	// (243.000 nel benchmark, 50 ms) invece di partire dall'indice (tenant, ruolo, persona).
	var roleID string
	if err := tx.QueryRow(ctx, `select id::text from stakeholder_roles where key = $1`, RoleVerifier).Scan(&roleID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return []InboxItem{}, false, nil
		}
		return nil, false, err
	}
	var verifier bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from stakeholders sh where sh.role_id = $2::uuid and sh.member_id = $1)
			or exists (select 1 from stakeholders sh where sh.role_id = $2::uuid
			           and sh.group_id in (select gm.group_id from group_members gm where gm.member_id = $1))`,
		actor.MemberID, roleID).Scan(&verifier); err != nil {
		return nil, false, err
	}
	if !verifier {
		return []InboxItem{}, false, nil
	}
	rows, err := tx.Query(ctx, `
		with mine as (
			select sh.element_id from stakeholders sh
			join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id and r.key = $2
			where sh.member_id = $1 or sh.group_id in (select member_groups($1))
		)
		select x.element_id::text, s.key || '-' || e.number, e.title, x.category, x.scope, isp.key || '-' || ie.number, ap.title, coalesce(t.name, ''),
		       rs.key || '-' || re.number, re.title, im.display_name, coalesce(c.implemented_at, c.updated_at)
		from applicability c
		join mine on mine.element_id = c.requirement_element_id
		join ext_requirement x on x.tenant_id = c.tenant_id and x.element_id = c.requirement_element_id and x.stage <> 'not_applicable'
		join elements e on e.tenant_id = x.tenant_id and e.id = x.element_id and e.deleted_at is null
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		join elements ie on ie.tenant_id = c.tenant_id and ie.id = c.target_element_id and ie.deleted_at is null
		join spaces isp on isp.tenant_id = ie.tenant_id and isp.id = ie.space_id
		left join options io on io.tenant_id = ie.tenant_id and io.id = ie.state_id
		join ext_impact xi on xi.tenant_id = c.tenant_id and xi.element_id = c.target_element_id
		join elements ap on ap.tenant_id = xi.tenant_id and ap.id = xi.application_id
		left join teams t on t.tenant_id = xi.tenant_id and t.id = xi.team_id
		join elements re on re.tenant_id = x.tenant_id and re.id = x.request_id
		join spaces rs on rs.tenant_id = re.tenant_id and rs.id = re.space_id
		left join members im on im.tenant_id = c.tenant_id and im.id = c.implemented_by
		where c.progress = 'to_verify' and c.status = 'applicable' and coalesce(io.category, '') <> 'cancelled'
		  and c.implemented_by is distinct from $1
		  and not (t.lead_id is not distinct from $1::uuid)
		  and not exists (select 1 from team_members tm where tm.tenant_id = t.tenant_id and tm.team_id = t.id and tm.member_id = $1)
		  and not exists (select 1 from stakeholders sh join stakeholder_roles r on r.tenant_id = sh.tenant_id and r.id = sh.role_id and r.key = 'team_lead'
		                  where sh.element_id = c.target_element_id
		                    and (sh.member_id = $1 or sh.group_id in (select member_groups($1))))
		order by coalesce(c.implemented_at, c.updated_at), e.number
		limit `+strconv.Itoa(limit+1), actor.MemberID, RoleVerifier)
	if err != nil {
		return nil, false, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[InboxItem])
	if out == nil {
		out = []InboxItem{}
	}
	more := len(out) > limit
	if more {
		out = out[:limit]
	}
	return out, more, err
}
