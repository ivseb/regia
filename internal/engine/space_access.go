package engine

import (
	"context"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"
)

// Accesso per spazio (piano, passo 19; migrazione 0026). Uno spazio può essere riservato
// (spaces.settings.access = "restricted"): allora lo leggono e lo scrivono solo i ruoli
// assegnati sullo spazio (role_assignments.space_id, diretti o tramite gruppo) e
// l'amministratore del tenant. Uno spazio aperto segue i permessi del tenant; i ruoli sullo
// spazio li estendono (un lettore del tenant che scrive solo in DOC). Vale per ogni kind di
// spazio; oggi lo usano gli spazi di documentazione.

// SpaceRights sono i diritti dell'attore su uno spazio.
type SpaceRights struct {
	Restricted bool `json:"restricted"`
	Read       bool `json:"read"`
	Write      bool `json:"write"`
	// Admin: amministratore del tenant ("*"), anche se l'attore è sintetico (stesso membro).
	Admin bool `json:"admin"`
}

func rightsFrom(actor Actor, restricted bool, perms []string) SpaceRights {
	r := SpaceRights{Restricted: restricted}
	r.Admin = actor.Has("*") || slices.Contains(perms, "*")
	spaceRead := slices.ContainsFunc(perms, func(p string) bool { return p == "element.read" || p == "element.write" })
	spaceWrite := slices.Contains(perms, "element.write")
	r.Read = r.Admin || spaceRead || (!restricted && actor.Has("element.read"))
	r.Write = r.Admin || spaceWrite || (!restricted && actor.Has("element.write"))
	// Senza membro (job di sistema con permessi espliciti) vale ciò che l'attore dichiara.
	if actor.MemberID == "" {
		r.Read = r.Read || actor.Has("element.read")
		r.Write = r.Write || actor.Has("element.write")
	}
	return r
}

// Rights calcola i diritti dell'attore sullo spazio (id).
func (s *Service) Rights(ctx context.Context, tx pgx.Tx, actor Actor, spaceID string) (SpaceRights, error) {
	var restricted bool
	var perms []string
	err := tx.QueryRow(ctx, `select coalesce(settings->>'access' = 'restricted', false), space_access(id, $2::uuid)
		from spaces where id = $1`, spaceID, nullable(actor.MemberID)).Scan(&restricted, &perms)
	if errors.Is(err, pgx.ErrNoRows) {
		return SpaceRights{}, ErrNotFound
	}
	if err != nil {
		return SpaceRights{}, err
	}
	return rightsFrom(actor, restricted, perms), nil
}

// spaceScope: spazi da nascondere all'attore (riservati senza un suo ruolo) e spazi che può
// leggere grazie a un ruolo sullo spazio. Si calcola una volta per ricerca: gli spazi
// interessati sono pochi (indice parziale sugli spazi riservati).
type spaceScope struct {
	hidden, granted []string
}

func (s *Service) scopeOf(ctx context.Context, tx pgx.Tx, actor Actor) (spaceScope, error) {
	var sc spaceScope
	if actor.Has("*") {
		return sc, nil
	}
	rows, err := tx.Query(ctx, `
		select s.id::text, coalesce(s.settings->>'access' = 'restricted', false), space_access(s.id, $1::uuid)
		from spaces s
		where s.settings->>'access' = 'restricted'
		   or exists (select 1 from role_assignments ra where ra.tenant_id = s.tenant_id and ra.space_id = s.id)`, nullable(actor.MemberID))
	if err != nil {
		return sc, err
	}
	var id string
	var restricted bool
	var perms []string
	_, err = pgx.ForEachRow(rows, []any{&id, &restricted, &perms}, func() error {
		r := rightsFrom(actor, restricted, perms)
		switch {
		case !r.Read && restricted:
			sc.hidden = append(sc.hidden, id)
		case r.Read && !actor.Has("element.read"):
			sc.granted = append(sc.granted, id)
		}
		return nil
	})
	return sc, err
}

// readBySpace applica la regola dello spazio alla lettura: ok = esito deciso dallo spazio
// (concesso o negato), decided = false quando valgono le regole del tenant.
func (s *Service) readBySpace(ctx context.Context, tx pgx.Tx, actor Actor, elementID string) (ok, decided bool, err error) {
	if actor.Has("*") {
		return true, true, nil
	}
	var restricted bool
	var perms []string
	err = tx.QueryRow(ctx, `select coalesce(sp.settings->>'access' = 'restricted', false), space_access(sp.id, $2::uuid)
		from elements e join spaces sp on sp.tenant_id = e.tenant_id and sp.id = e.space_id where e.id = $1`,
		elementID, nullable(actor.MemberID)).Scan(&restricted, &perms)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, true, nil
	}
	if err != nil {
		return false, true, err
	}
	r := rightsFrom(actor, restricted, perms)
	switch {
	case restricted:
		return r.Read, true, nil
	case r.Read && len(perms) > 0:
		return true, true, nil
	}
	return false, false, nil
}

// writeBySpace: scrittura secondo lo spazio. In uno spazio riservato serve un ruolo con
// element.write sullo spazio (o "*"); in uno aperto basta la regola del tenant (canWrite) o
// un ruolo di scrittura sullo spazio.
func (s *Service) writeBySpace(ctx context.Context, tx pgx.Tx, actor Actor, el *Element) (bool, error) {
	if actor.Has("*") {
		return true, nil
	}
	r, err := s.Rights(ctx, tx, actor, el.SpaceID)
	if err != nil {
		return false, err
	}
	if r.Restricted {
		return r.Write, nil
	}
	return canWrite(actor, el) || r.Write, nil
}
