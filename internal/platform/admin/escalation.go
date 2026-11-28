package admin

// Nessuno dà diritti che non ha. Chi amministra può concedere (e togliere) solo ruoli i
// cui permessi stanno tutti nei suoi permessi di tenant effettivi: ruoli diretti e di
// gruppo, con l'ereditarietà dei gruppi se è accesa. "*" lo concede solo chi ha "*".
//
// La regola vale per: assegnare e togliere ruoli, invitare con ruoli e gruppi, aggiungere
// o togliere membri da un gruppo, spostare un gruppo sotto un altro, creare, modificare e
// cancellare ruoli, mappare gruppi dell'IdP, e per le azioni su un membro più forte
// (sospensione, link di password e d'invito, MFA, cambio di tipo).

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/rbac"
)

// ErrEscalation è l'errore di base: Msg porta i permessi mancanti.
func escalation(missing []string) error {
	return &Error{Status: 403, Code: "admin.escalation",
		Msg: "operazione non consentita: servono permessi che non hai (" + strings.Join(missing, ", ") + ")"}
}

// permsSQL: permessi di tenant effettivi del membro $1 (come al login, auth.loadTenant).
const permsSQL = `select coalesce(array_agg(distinct p), '{}') from (
	select unnest(r.permissions) p from role_assignments ra
	join roles r on r.tenant_id = ra.tenant_id and r.id = ra.role_id
	where ra.space_id is null and (ra.member_id = $1 or ra.group_id in (select member_groups($1)))) x`

func memberPerms(ctx context.Context, tx pgx.Tx, memberID string) ([]string, error) {
	var out []string
	if memberID == "" {
		return []string{}, nil
	}
	err := tx.QueryRow(ctx, permsSQL, memberID).Scan(&out)
	return out, err
}

// missing restituisce i permessi di want che have non concede.
func missing(have, want []string) []string {
	if slices.Contains(have, rbac.All) {
		return nil
	}
	var out []string
	for _, p := range want {
		if !slices.Contains(have, p) && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// canGrant controlla che chi agisce abbia tutti i permessi indicati.
func (a Actor) canGrant(ctx context.Context, tx pgx.Tx, want []string) error {
	if len(want) == 0 {
		return nil
	}
	have, err := memberPerms(ctx, tx, a.MemberID)
	if err != nil {
		return err
	}
	if m := missing(have, want); len(m) > 0 {
		return escalation(m)
	}
	return nil
}

// rolePerms: unione dei permessi dei ruoli indicati.
func rolePerms(ctx context.Context, tx pgx.Tx, roleIDs []string) ([]string, error) {
	var out []string
	err := tx.QueryRow(ctx, `select coalesce(array_agg(distinct p), '{}') from (
		select unnest(permissions) p from roles where id = any($1::uuid[])) x`, roleIDs).Scan(&out)
	return out, err
}

// groupGrantSQL: permessi di tenant che porta l'appartenenza ai gruppi $1: ruoli dati ai
// gruppi e, con l'ereditarietà accesa, ai loro antenati.
const groupGrantSQL = `with recursive on_(v) as (select group_inheritance_on()),
	g(id) as (
		select unnest($1::uuid[])
		union
		select p.parent_id from groups p join g on p.id = g.id where p.parent_id is not null and (select v from on_)
	)
	select coalesce(array_agg(distinct p), '{}') from (
		select unnest(r.permissions) p from role_assignments ra
		join roles r on r.tenant_id = ra.tenant_id and r.id = ra.role_id
		where ra.space_id is null and ra.group_id in (select id from g)) x`

func groupPerms(ctx context.Context, tx pgx.Tx, groupIDs []string) ([]string, error) {
	var out []string
	err := tx.QueryRow(ctx, groupGrantSQL, groupIDs).Scan(&out)
	return out, err
}

// canGrantGroups: chi agisce può mettere (o togliere) una persona in questi gruppi.
func (a Actor) canGrantGroups(ctx context.Context, tx pgx.Tx, groupIDs []string) error {
	perms, err := groupPerms(ctx, tx, groupIDs)
	if err != nil {
		return err
	}
	return a.canGrant(ctx, tx, perms)
}

// canActOn: chi agisce ha almeno i permessi del membro su cui agisce (sospenderlo,
// mandargli un link per la password, togliergli l'MFA). Un amministratore delegato non
// prende così l'account di un amministratore pieno.
func (a Actor) canActOn(ctx context.Context, tx pgx.Tx, memberID string) error {
	perms, err := memberPerms(ctx, tx, memberID)
	if err != nil {
		return err
	}
	return a.canGrant(ctx, tx, perms)
}
