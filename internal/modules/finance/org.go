package finance

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/db"
	"regia/internal/platform/rbac"
)

// Organizzazione (budget.md §5-6, passo 1): albero delle unità, persone con un ruolo
// nell'unità (responsabile, referente finanziario, membro), ricerca e import CSV con
// anteprima. La struttura la legge ogni membro (serve a indicare l'unità richiedente);
// la modificano gli amministratori del tenant e chi ha finance.write.

// Ruoli di una persona in un'unità.
const (
	RoleManager    = "manager"
	RoleFinanceRef = "finance_ref"
	RoleMember     = "member"
)

var orgKinds = map[string]bool{"legal_entity": true, "business_unit": true, "department": true, "cost_center": true}

func requireOrgAdmin(a engine.Actor) error {
	if a.Has(rbac.TenantAdmin) || a.Has(rbac.FinanceWrite) {
		return nil
	}
	return engine.ErrForbidden
}

func (s *Service) aud(ctx context.Context, tx pgx.Tx, a engine.Actor, action, entity, id string, payload map[string]any) error {
	return audit.Append(ctx, tx, audit.Event{Action: action, EntityType: entity, EntityID: id, ActorAccountID: a.AccountID,
		ActorMemberID: a.MemberID, IP: a.IP, Payload: payload})
}

// OrgUnit è una riga dell'albero, in ordine di visita (padre prima dei figli).
type OrgUnit struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	ParentID    *string `json:"parent_id"`
	Kind        string  `json:"kind"`
	Code        *string `json:"code"`
	ManagerID   *string `json:"manager_id"`
	ManagerName *string `json:"manager_name"`
	Archived    bool    `json:"archived"`
	Depth       int     `json:"depth"`
	Path        string  `json:"path"` // «Società › Area › Reparto»
	Members     int     `json:"members"`
	Children    int     `json:"children"`
}

// OrgUnits elenca le unità ad albero. q cerca nel nome e nel codice (elenco piatto con il
// percorso), limit limita le righe (predefinito 500, massimo 2000).
func (s *Service) OrgUnits(ctx context.Context, tx pgx.Tx, q string, limit int, includeArchived bool) ([]OrgUnit, error) {
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	q = strings.ToLower(strings.TrimSpace(q))
	rows, err := tx.Query(ctx, `
		with recursive t(id, depth, sort, path) as (
			select u.id, 0, array[lower(u.name) || u.id::text], u.name::text from org_units u where u.parent_id is null
			union all
			select c.id, t.depth + 1, t.sort || (lower(c.name) || c.id::text), t.path || ' › ' || c.name
			from org_units c join t on c.parent_id = t.id
		)
		select u.id, u.name, u.parent_id, u.kind, u.code, u.manager_id, m.display_name, u.archived, t.depth, t.path,
		       (select count(*) from org_unit_members x where x.org_unit_id = u.id),
		       (select count(*) from org_units c where c.parent_id = u.id and ($3 or not c.archived))
		from t join org_units u on u.id = t.id
		left join members m on m.tenant_id = u.tenant_id and m.id = u.manager_id
		where ($3 or not u.archived)
		  and ($1 = '' or lower(u.name) like '%' || $1 || '%' or lower(coalesce(u.code, '')) like '%' || $1 || '%')
		order by t.sort limit $2`, q, limit, includeArchived)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[OrgUnit])
	if out == nil {
		out = []OrgUnit{}
	}
	return out, err
}

func (s *Service) orgUnit(ctx context.Context, tx pgx.Tx, id string) (*OrgUnit, error) {
	all, err := s.OrgUnits(ctx, tx, "", 2000, true)
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

// OrgUnitInput: i campi assenti restano come sono (nome e tipo si impostano alla creazione).
type OrgUnitInput struct {
	Name      *string `json:"name"`
	ParentID  *string `json:"parent_id"` // alla creazione; per spostare si usa MoveOrgUnit
	Kind      *string `json:"kind"`
	Code      *string `json:"code"`       // "" toglie il codice
	ManagerID *string `json:"manager_id"` // "" toglie il responsabile
	Archived  *bool   `json:"archived"`
}

func (in *OrgUnitInput) clean() error {
	if in.Name != nil {
		v := strings.TrimSpace(*in.Name)
		if v == "" || len([]rune(v)) > 120 {
			return engine.TooLong("name", 120, "il nome è obbligatorio (massimo 120 caratteri)")
		}
		in.Name = &v
	}
	if in.Kind != nil && !orgKinds[*in.Kind] {
		return engine.InvalidValue("kind", "tipo non ammesso (legal_entity, business_unit, department, cost_center)")
	}
	if in.Code != nil {
		v := strings.TrimSpace(*in.Code)
		if len([]rune(v)) > 40 {
			return engine.TooLong("code", 40, "il codice è lungo al massimo 40 caratteri")
		}
		in.Code = &v
	}
	return nil
}

func (s *Service) checkMember(ctx context.Context, tx pgx.Tx, id, field string) error {
	var ok bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from members where id::text = $1 and status = 'active')", id).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return engine.NotFound(field, "member", "persona inesistente o non attiva")
	}
	return nil
}

func (s *Service) checkUnit(ctx context.Context, tx pgx.Tx, id, field string) error {
	var ok bool
	if err := tx.QueryRow(ctx, "select exists (select 1 from org_units where id::text = $1)", id).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return engine.NotFound(field, "unit", "unità inesistente")
	}
	return nil
}

// CreateOrgUnit aggiunge un'unità sotto un padre (o in cima).
func (s *Service) CreateOrgUnit(ctx context.Context, tx pgx.Tx, a engine.Actor, in OrgUnitInput) (*OrgUnit, error) {
	if err := requireOrgAdmin(a); err != nil {
		return nil, err
	}
	if in.Name == nil {
		return nil, engine.Required("name", "il nome è obbligatorio")
	}
	if err := in.clean(); err != nil {
		return nil, err
	}
	kind := "department"
	if in.Kind != nil {
		kind = *in.Kind
	}
	var parent any
	if in.ParentID != nil && *in.ParentID != "" {
		if err := s.checkUnit(ctx, tx, *in.ParentID, "parent_id"); err != nil {
			return nil, err
		}
		parent = *in.ParentID
	}
	var manager any
	if in.ManagerID != nil && *in.ManagerID != "" {
		if err := s.checkMember(ctx, tx, *in.ManagerID, "manager_id"); err != nil {
			return nil, err
		}
		manager = *in.ManagerID
	}
	id := db.NewID()
	if _, err := tx.Exec(ctx, `insert into org_units (tenant_id, id, name, parent_id, kind, code, manager_id)
		values (app_tenant(), $1, $2, $3, $4, $5, $6)`, id, *in.Name, parent, kind, nullablePtr(in.Code), manager); err != nil {
		if isUnique(err) {
			return nil, engine.Duplicate("code", "il codice è già usato da un'altra unità")
		}
		return nil, err
	}
	if err := s.aud(ctx, tx, a, "org.unit.create", "org_unit", id, map[string]any{"parent_id": parent, "kind": kind}); err != nil {
		return nil, err
	}
	return s.orgUnit(ctx, tx, id)
}

// UpdateOrgUnit modifica nome, tipo, codice, responsabile e archiviazione. Un'unità con
// stanziamenti non si cancella: si archivia.
func (s *Service) UpdateOrgUnit(ctx context.Context, tx pgx.Tx, a engine.Actor, id string, in OrgUnitInput) (*OrgUnit, error) {
	if err := requireOrgAdmin(a); err != nil {
		return nil, err
	}
	if err := in.clean(); err != nil {
		return nil, err
	}
	var cur string
	if err := tx.QueryRow(ctx, "select name from org_units where id::text = $1 for update", id).Scan(&cur); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, engine.ErrNotFound
		}
		return nil, err
	}
	if in.ManagerID != nil && *in.ManagerID != "" {
		if err := s.checkMember(ctx, tx, *in.ManagerID, "manager_id"); err != nil {
			return nil, err
		}
	}
	if in.Archived != nil && *in.Archived {
		var kids int
		if err := tx.QueryRow(ctx, "select count(*) from org_units where parent_id::text = $1 and not archived", id).Scan(&kids); err != nil {
			return nil, err
		}
		if kids > 0 {
			return nil, engine.InvalidParams("validation.finance.unit_has_children", "archived", map[string]any{"n": kids}, "archivia prima le unità che stanno sotto (%d)", kids)
		}
	}
	set := []string{}
	args := []any{id}
	add := func(col string, v any) {
		args = append(args, v)
		set = append(set, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	if in.Name != nil {
		add("name", *in.Name)
	}
	if in.Kind != nil {
		add("kind", *in.Kind)
	}
	if in.Code != nil {
		add("code", nullablePtr(in.Code))
	}
	if in.ManagerID != nil {
		add("manager_id", nullablePtr(in.ManagerID))
	}
	if in.Archived != nil {
		add("archived", *in.Archived)
	}
	if len(set) > 0 {
		if _, err := tx.Exec(ctx, "update org_units set "+strings.Join(set, ", ")+" where id::text = $1", args...); err != nil {
			if isUnique(err) {
				return nil, engine.Duplicate("code", "il codice è già usato da un'altra unità")
			}
			return nil, err
		}
		if err := s.aud(ctx, tx, a, "org.unit.update", "org_unit", id, map[string]any{"fields": len(set)}); err != nil {
			return nil, err
		}
	}
	return s.orgUnit(ctx, tx, id)
}

// MoveOrgUnit sposta un'unità sotto un altro padre (vuoto = in cima). Lo spostamento sotto
// sé stessa o un suo discendente è rifiutato.
func (s *Service) MoveOrgUnit(ctx context.Context, tx pgx.Tx, a engine.Actor, id, parentID string) (*OrgUnit, error) {
	if err := requireOrgAdmin(a); err != nil {
		return nil, err
	}
	if err := s.checkUnit(ctx, tx, id, "id"); err != nil {
		return nil, engine.ErrNotFound
	}
	var parent any
	if parentID != "" {
		if err := s.checkUnit(ctx, tx, parentID, "parent_id"); err != nil {
			return nil, err
		}
		var cycle bool
		if err := tx.QueryRow(ctx, "select $2::uuid in (select org_subtree($1::uuid))", id, parentID).Scan(&cycle); err != nil {
			return nil, err
		}
		if cycle {
			return nil, engine.InvalidCode("validation.finance.unit_move_into_self", "parent_id", "non si può spostare un'unità sotto sé stessa o sotto una delle sue unità")
		}
		parent = parentID
	}
	if _, err := tx.Exec(ctx, "update org_units set parent_id = $2 where id::text = $1", id, parent); err != nil {
		return nil, err
	}
	if err := s.aud(ctx, tx, a, "org.unit.move", "org_unit", id, map[string]any{"parent_id": parent}); err != nil {
		return nil, err
	}
	return s.orgUnit(ctx, tx, id)
}

// ---------------------------------------------------------------------
// Persone nelle unità
// ---------------------------------------------------------------------

type OrgMember struct {
	MemberID string `json:"member_id"`
	Name     string `json:"name"`
	Email    string `json:"email"`
	Role     string `json:"role"`
}

func (s *Service) OrgUnitMembers(ctx context.Context, tx pgx.Tx, unitID string) ([]OrgMember, error) {
	rows, err := tx.Query(ctx, `select m.id, m.display_name, coalesce(m.email, ''), x.role
		from org_unit_members x join members m on m.tenant_id = x.tenant_id and m.id = x.member_id
		where x.org_unit_id::text = $1 and m.status <> 'anonymized'
		order by case x.role when 'manager' then 0 when 'finance_ref' then 1 else 2 end, lower(m.display_name)`, unitID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[OrgMember])
	if out == nil {
		out = []OrgMember{}
	}
	return out, err
}

// SetOrgMember aggiunge una persona a un'unità o ne cambia il ruolo.
func (s *Service) SetOrgMember(ctx context.Context, tx pgx.Tx, a engine.Actor, unitID, memberID, role string) error {
	if err := requireOrgAdmin(a); err != nil {
		return err
	}
	if role == "" {
		role = RoleMember
	}
	if role != RoleManager && role != RoleFinanceRef && role != RoleMember {
		return engine.InvalidValue("role", "ruolo non ammesso (manager, finance_ref, member)")
	}
	if err := s.checkUnit(ctx, tx, unitID, "unit"); err != nil {
		return engine.ErrNotFound
	}
	if err := s.checkMember(ctx, tx, memberID, "member_id"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `insert into org_unit_members (tenant_id, org_unit_id, member_id, role) values (app_tenant(), $1, $2, $3)
		on conflict (tenant_id, org_unit_id, member_id) do update set role = excluded.role`, unitID, memberID, role); err != nil {
		return err
	}
	return s.aud(ctx, tx, a, "org.member.set", "org_unit", unitID, map[string]any{"member_id": memberID, "role": role})
}

func (s *Service) RemoveOrgMember(ctx context.Context, tx pgx.Tx, a engine.Actor, unitID, memberID string) error {
	if err := requireOrgAdmin(a); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, "delete from org_unit_members where org_unit_id::text = $1 and member_id::text = $2", unitID, memberID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return engine.ErrNotFound
	}
	return s.aud(ctx, tx, a, "org.member.remove", "org_unit", unitID, map[string]any{"member_id": memberID})
}

// PersonUnits: le unità di una persona, con il ruolo.
type PersonUnit struct {
	UnitID string `json:"unit_id"`
	Name   string `json:"name"`
	Path   string `json:"path"`
	Role   string `json:"role"`
}

type Person struct {
	MemberID string       `json:"member_id"`
	Name     string       `json:"name"`
	Email    string       `json:"email"`
	Units    []PersonUnit `json:"units"`
}

// People cerca una persona per nome o email e mostra le sue unità (budget.md §6). Senza q,
// le persone con almeno un'unità. Il responsabile indicato sull'unità (manager_id) conta
// come ruolo «manager».
func (s *Service) People(ctx context.Context, tx pgx.Tx, q string, limit int) ([]Person, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q = strings.ToLower(strings.TrimSpace(q))
	rows, err := tx.Query(ctx, `
		with recursive t(id, path) as (
			select u.id, u.name::text from org_units u where u.parent_id is null
			union all
			select c.id, t.path || ' › ' || c.name from org_units c join t on c.parent_id = t.id
		), rel as (
			select x.member_id, x.org_unit_id, x.role from org_unit_members x
			union
			select u.manager_id, u.id, 'manager' from org_units u where u.manager_id is not null
		)
		select m.id, m.display_name, coalesce(m.email, ''), u.id, u.name, t.path,
		       case when bool_or(r.role = 'manager') then 'manager' when bool_or(r.role = 'finance_ref') then 'finance_ref' else 'member' end
		from members m
		join rel r on r.member_id = m.id
		join org_units u on u.id = r.org_unit_id and not u.archived
		join t on t.id = u.id
		where m.status = 'active' and ($1 = '' or lower(m.display_name) like '%' || $1 || '%' or lower(coalesce(m.email, '')) like '%' || $1 || '%')
		group by m.id, m.display_name, m.email, u.id, u.name, t.path
		order by lower(m.display_name), t.path`, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Person
	idx := map[string]int{}
	for rows.Next() {
		var p Person
		var pu PersonUnit
		if err := rows.Scan(&p.MemberID, &p.Name, &p.Email, &pu.UnitID, &pu.Name, &pu.Path, &pu.Role); err != nil {
			return nil, err
		}
		i, ok := idx[p.MemberID]
		if !ok {
			if len(out) >= limit {
				continue
			}
			p.Units = []PersonUnit{}
			out = append(out, p)
			i = len(out) - 1
			idx[p.MemberID] = i
		}
		out[i].Units = append(out[i].Units, pu)
	}
	if out == nil {
		out = []Person{}
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------
// Import CSV
// ---------------------------------------------------------------------

// Colonne riconosciute (intestazione italiana o inglese): codice, nome, padre (codice
// dell'unità padre), tipo, responsabile (email), referente (email del referente finanziario).
var orgColumns = map[string]string{
	"codice": "code", "code": "code", "nome": "name", "name": "name", "padre": "parent", "parent": "parent",
	"tipo": "kind", "kind": "kind", "responsabile": "manager", "manager": "manager",
	"referente": "finance_ref", "referente finanziario": "finance_ref", "finance_ref": "finance_ref",
}

// OrgImportRow è l'esito di una riga nell'anteprima.
type OrgImportRow struct {
	Line    int    `json:"line"`
	Action  string `json:"action"` // create | update | unchanged | error
	Code    string `json:"code"`
	Name    string `json:"name"`
	Parent  string `json:"parent"`
	Message string `json:"message,omitempty"`
}

type OrgImportReport struct {
	SHA256    string         `json:"sha256"`
	Rows      []OrgImportRow `json:"rows"`
	Create    int            `json:"create"`
	Update    int            `json:"update"`
	Unchanged int            `json:"unchanged"`
	Errors    int            `json:"errors"`
	Applied   bool           `json:"applied"`
}

type orgRow struct {
	line                                          int
	code, name, parent, kind, manager, financeRef string
	hasKind, hasParent, hasManager, hasFinanceRef bool
}

func parseOrgCSV(data string, delim string) ([]orgRow, error) {
	data = strings.TrimPrefix(data, "\ufeff")
	if strings.TrimSpace(data) == "" {
		return nil, engine.InvalidCode("validation.finance.csv_empty", "csv", "il file è vuoto")
	}
	r := csv.NewReader(strings.NewReader(data))
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	switch {
	case delim != "":
		r.Comma = []rune(delim)[0]
	default:
		first, _, _ := strings.Cut(data, "\n")
		switch {
		case strings.Count(first, ";") > strings.Count(first, ","):
			r.Comma = ';'
		case strings.Count(first, "\t") > strings.Count(first, ","):
			r.Comma = '\t'
		}
	}
	head, err := r.Read()
	if err != nil {
		return nil, engine.InvalidCode("validation.finance.csv_header_unreadable", "csv", "intestazione non leggibile")
	}
	cols := map[int]string{}
	for i, h := range head {
		if f, ok := orgColumns[strings.ToLower(strings.TrimSpace(h))]; ok {
			cols[i] = f
		}
	}
	has := map[string]bool{}
	for _, f := range cols {
		has[f] = true
	}
	if !has["name"] {
		return nil, engine.Required("csv", "serve la colonna «nome»")
	}
	var out []orgRow
	line := 1
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		line++
		if err != nil {
			return nil, engine.InvalidParams("validation.finance.csv_row_unreadable", "csv", map[string]any{"row": line}, "riga %d non leggibile", line)
		}
		o := orgRow{line: line, hasKind: has["kind"], hasParent: has["parent"], hasManager: has["manager"], hasFinanceRef: has["finance_ref"]}
		empty := true
		for i, v := range rec {
			v = strings.TrimSpace(v)
			if v != "" {
				empty = false
			}
			switch cols[i] {
			case "code":
				o.code = v
			case "name":
				o.name = v
			case "parent":
				o.parent = v
			case "kind":
				o.kind = strings.ToLower(v)
			case "manager":
				o.manager = strings.ToLower(v)
			case "finance_ref":
				o.financeRef = strings.ToLower(v)
			}
		}
		if !empty {
			out = append(out, o)
		}
		if len(out) > 5000 {
			return nil, engine.InvalidParams("validation.finance.csv_too_many_rows", "csv", map[string]any{"max": 5000}, "al massimo 5000 righe per file")
		}
	}
	if len(out) == 0 {
		return nil, engine.InvalidCode("validation.finance.csv_no_rows", "csv", "il file non ha righe")
	}
	return out, nil
}

var kindWords = map[string]string{"società": "legal_entity", "societa": "legal_entity", "legal_entity": "legal_entity",
	"area": "business_unit", "business_unit": "business_unit", "reparto": "department", "department": "department",
	"ufficio": "department", "centro di costo": "cost_center", "cost_center": "cost_center"}

// ImportOrg legge il CSV e, con apply = false, restituisce l'anteprima senza scrivere nulla;
// con apply = true (e lo stesso file, verificato con l'impronta) applica le righe valide
// solo se non ci sono errori. Le unità si riconoscono per codice (o per nome, senza codice
// e solo sotto lo stesso padre); il padre si indica con il codice (o il nome) di un'unità
// già presente o del file stesso.
func (s *Service) ImportOrg(ctx context.Context, tx pgx.Tx, a engine.Actor, data, delim, sha string, apply bool) (*OrgImportReport, error) {
	if err := requireOrgAdmin(a); err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(data))
	rep := &OrgImportReport{SHA256: hex.EncodeToString(sum[:]), Rows: []OrgImportRow{}}
	if apply && sha != rep.SHA256 {
		return nil, engine.InvalidCode("validation.finance.csv_changed_preview", "sha256", "il file è diverso da quello visto nell'anteprima: rifai l'anteprima")
	}
	rows, err := parseOrgCSV(data, delim)
	if err != nil {
		return nil, err
	}
	type exist struct {
		id, name, kind string
		code, parent   *string
		manager        *string
	}
	// Stato attuale: per codice e per nome.
	cur, err := tx.Query(ctx, `select id, name, kind, code, parent_id::text, manager_id::text from org_units where not archived`)
	if err != nil {
		return nil, err
	}
	byCode, byName := map[string]*exist{}, map[string]*exist{}
	for cur.Next() {
		e := &exist{}
		if err := cur.Scan(&e.id, &e.name, &e.kind, &e.code, &e.parent, &e.manager); err != nil {
			cur.Close()
			return nil, err
		}
		if e.code != nil {
			byCode[strings.ToLower(*e.code)] = e
		}
		byName[strings.ToLower(e.name)] = e
	}
	cur.Close()
	if err := cur.Err(); err != nil {
		return nil, err
	}
	memberByEmail := func(email string) (string, bool) {
		var id string
		err := tx.QueryRow(ctx, "select id from members where lower(email) = $1 and status = 'active'", email).Scan(&id)
		return id, err == nil
	}

	// Prima passata: validazione e riconoscimento. Le unità nuove hanno un id provvisorio.
	type plan struct {
		row     orgRow
		action  string
		id      string
		known   *exist
		mgr, fr string
		msg     string
	}
	plans := make([]*plan, len(rows))
	fileByCode := map[string]*plan{}
	fileByName := map[string]*plan{}
	for i, r := range rows {
		p := &plan{row: r}
		plans[i] = p
		switch {
		case r.name == "":
			p.action, p.msg = "error", "manca il nome"
		case r.hasKind && r.kind != "" && kindWords[r.kind] == "":
			p.action, p.msg = "error", "tipo sconosciuto: "+r.kind
		}
		if p.action == "error" {
			continue
		}
		if r.code != "" {
			if prev := fileByCode[strings.ToLower(r.code)]; prev != nil {
				p.action, p.msg = "error", fmt.Sprintf("il codice %s è ripetuto (riga %d)", r.code, prev.row.line)
				continue
			}
			p.known = byCode[strings.ToLower(r.code)]
		} else {
			if prev := fileByName[strings.ToLower(r.name)]; prev != nil {
				p.action, p.msg = "error", fmt.Sprintf("il nome %s è ripetuto (riga %d)", r.name, prev.row.line)
				continue
			}
			p.known = byName[strings.ToLower(r.name)]
		}
		if p.known != nil {
			p.id = p.known.id
		} else {
			p.id = db.NewID()
		}
		if r.code != "" {
			fileByCode[strings.ToLower(r.code)] = p
		}
		fileByName[strings.ToLower(r.name)] = p
		if r.hasManager && r.manager != "" {
			id, ok := memberByEmail(r.manager)
			if !ok {
				p.action, p.msg = "error", "responsabile non trovato: "+r.manager
				continue
			}
			p.mgr = id
		}
		if r.hasFinanceRef && r.financeRef != "" {
			id, ok := memberByEmail(r.financeRef)
			if !ok {
				p.action, p.msg = "error", "referente non trovato: "+r.financeRef
				continue
			}
			p.fr = id
		}
	}
	// Risoluzione del padre (nel file o già presente) e controllo dei cicli.
	parentOf := map[string]string{} // id → id padre (stato finale ipotizzato)
	for _, e := range byCode {
		if e.parent != nil {
			parentOf[e.id] = *e.parent
		}
	}
	for _, e := range byName {
		if e.parent != nil {
			parentOf[e.id] = *e.parent
		}
	}
	resolve := func(ref string) (string, bool) {
		k := strings.ToLower(ref)
		if p := fileByCode[k]; p != nil && p.action != "error" {
			return p.id, true
		}
		if e := byCode[k]; e != nil {
			return e.id, true
		}
		if p := fileByName[k]; p != nil && p.action != "error" {
			return p.id, true
		}
		if e := byName[k]; e != nil {
			return e.id, true
		}
		return "", false
	}
	parents := map[*plan]string{}
	for _, p := range plans {
		if p.action == "error" || !p.row.hasParent {
			continue
		}
		if p.row.parent == "" {
			delete(parentOf, p.id)
			parents[p] = ""
			continue
		}
		pid, ok := resolve(p.row.parent)
		if !ok {
			p.action, p.msg = "error", "unità padre non trovata: "+p.row.parent
			continue
		}
		parentOf[p.id] = pid
		parents[p] = pid
	}
	for _, p := range plans {
		if p.action == "error" || !p.row.hasParent {
			continue
		}
		seen := map[string]bool{p.id: true}
		for at := parentOf[p.id]; at != ""; at = parentOf[at] {
			if seen[at] {
				p.action, p.msg = "error", "il padre indicato crea un ciclo"
				break
			}
			seen[at] = true
		}
	}
	for _, p := range plans {
		if p.action == "error" {
			rep.Errors++
			continue
		}
		switch {
		case p.known == nil:
			p.action = "create"
		default:
			changed := p.row.name != p.known.name
			if p.row.hasKind && p.row.kind != "" && kindWords[p.row.kind] != p.known.kind {
				changed = true
			}
			if pid, ok := parents[p]; ok {
				cur := ""
				if p.known.parent != nil {
					cur = *p.known.parent
				}
				if pid != cur {
					changed = true
				}
			}
			if p.mgr != "" && (p.known.manager == nil || *p.known.manager != p.mgr) {
				changed = true
			}
			if p.fr != "" {
				changed = true
			}
			if changed {
				p.action = "update"
			} else {
				p.action = "unchanged"
			}
		}
		switch p.action {
		case "create":
			rep.Create++
		case "update":
			rep.Update++
		default:
			rep.Unchanged++
		}
	}
	for _, p := range plans {
		rep.Rows = append(rep.Rows, OrgImportRow{Line: p.row.line, Action: p.action, Code: p.row.code, Name: p.row.name, Parent: p.row.parent, Message: p.msg})
	}
	if !apply {
		return rep, nil
	}
	if rep.Errors > 0 {
		return rep, engine.InvalidParams("validation.finance.csv_rows_with_errors", "csv", map[string]any{"n": rep.Errors}, "ci sono %d righe con errori: correggi il file e rifai l'anteprima", rep.Errors)
	}
	// Applicazione: prima si creano tutte le unità (senza padre), poi si assegnano i padri.
	for _, p := range plans {
		kind := "department"
		if p.row.kind != "" {
			kind = kindWords[p.row.kind]
		}
		switch p.action {
		case "create":
			if _, err := tx.Exec(ctx, `insert into org_units (tenant_id, id, name, kind, code, manager_id) values (app_tenant(), $1, $2, $3, $4, $5)`,
				p.id, p.row.name, kind, nullable(p.row.code), nullable(p.mgr)); err != nil {
				if isUnique(err) {
					return nil, engine.Duplicate("csv", "riga %d: il codice è già usato", p.row.line)
				}
				return nil, err
			}
		case "update":
			if _, err := tx.Exec(ctx, `update org_units set name = $2, kind = case when $3 then $4 else kind end,
				code = case when $5 <> '' then $5 else code end, manager_id = case when $6 <> '' then $6::uuid else manager_id end where id = $1`,
				p.id, p.row.name, p.row.hasKind && p.row.kind != "", kind, p.row.code, p.mgr); err != nil {
				return nil, err
			}
		}
	}
	// I padri: si applicano in ordine di profondità per non far scattare il controllo dei cicli
	// su uno stato intermedio (il risultato finale è già verificato aciclico).
	for _, p := range plans {
		if pid, ok := parents[p]; ok && (p.action == "create" || p.action == "update") {
			if _, err := tx.Exec(ctx, "update org_units set parent_id = $2 where id = $1", p.id, nullable(pid)); err != nil {
				return nil, engine.InvalidParams("validation.finance.csv_row_error", "csv", map[string]any{"row": p.row.line, "detail": err.Error()}, "riga %d: %v", p.row.line, err)
			}
		}
	}
	for _, p := range plans {
		if p.fr != "" {
			if _, err := tx.Exec(ctx, `insert into org_unit_members (tenant_id, org_unit_id, member_id, role) values (app_tenant(), $1, $2, 'finance_ref')
				on conflict (tenant_id, org_unit_id, member_id) do update set role = case when org_unit_members.role = 'manager' then 'manager' else 'finance_ref' end`, p.id, p.fr); err != nil {
				return nil, err
			}
		}
	}
	rep.Applied = true
	return rep, s.aud(ctx, tx, a, "org.import", "org_unit", "", map[string]any{"sha256": rep.SHA256, "create": rep.Create, "update": rep.Update})
}
