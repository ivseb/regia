// Package notify genera le notifiche in-app (e, se configurate, le email) nella stessa
// transazione dell'evento che le causa: o si salvano entrambi o nessuno.
//
// Regole comuni a tutti gli eventi:
//   - chi compie l'azione non notifica sé stesso;
//   - si notifica solo chi può leggere l'elemento (mai un titolo a chi non lo vede);
//   - le preferenze della persona possono silenziare un tipo di evento in app o per email.
//
// Gli osservatori di un elemento sono: chi lo segue esplicitamente, il segnalatore e gli
// stakeholder (anche tramite gruppo), tolti quelli che hanno scelto di non seguirlo
// (riga di watchers con muted = true). Il pacchetto non dipende dal motore: lavora
// sulle tabelle, dentro una transazione già legata al tenant.
package notify

import (
	"context"
	"sync/atomic"

	"github.com/jackc/pgx/v5"
)

// Tipi di evento.
const (
	KindMention  = "mention"  // menzione in un commento
	KindComment  = "comment"  // nuovo commento su un elemento che segui
	KindRole     = "role"     // ruolo assegnato su un elemento
	KindPhase    = "phase"    // cambio di fase di un elemento che segui
	KindApproval = "approval" // passo di approvazione attivo per te, il tuo gruppo o il tuo organo
	KindReferent = "referent" // nomina a referente di team su un impatto
	// Valutazioni con questionari (piano, passo 4).
	KindReview = "review" // valutazione aperta per il tuo team, sezione assegnata, invio, esito
	KindSLA    = "sla"    // sollecito, ritardo o escalation di una valutazione
	// Avanzamento (piano, passo 13).
	KindReport = "report" // status report da scrivere (promemoria al PM) o inviato su una richiesta che segui
	// Requisiti e deroghe (piano, passi 5 e 6).
	KindRequirement = "requirement" // requisito assegnato, da verificare, verificato o respinto; deroga decisa, in scadenza, scaduta
	// Knowledge (piano, passo 19).
	KindDocument = "document" // documento controllato da rivedere, approvato o tornato in bozza
)

// Informational sono i tipi di avviso che non chiedono un'azione: finiscono nelle Attività raggruppati per
// elemento e si possono silenziare per elemento (Silence). Gli altri chiedono qualcosa a chi li riceve.
var Informational = []string{KindComment, KindRole, KindPhase}

// IsInformational dice se il tipo di evento è solo un avviso.
func IsInformational(kind string) bool { return contains(Informational, kind) }

// Kinds sono tutti i tipi, nell'ordine in cui li mostrano le preferenze.
var Kinds = []string{KindMention, KindComment, KindRole, KindPhase, KindApproval, KindReferent, KindReview, KindSLA, KindReport, KindRequirement, KindDocument}

// ValidKind dice se k è un tipo di evento conosciuto.
func ValidKind(k string) bool {
	for _, x := range Kinds {
		if x == k {
			return true
		}
	}
	return false
}

var emailOn atomic.Bool

// EnableEmail attiva la coda delle email (outbox_events): da chiamare all'avvio solo
// se il server SMTP è configurato. Senza, le notifiche restano solo in app.
func EnableEmail(on bool) { emailOn.Store(on) }

// EmailEnabled dice se le notifiche finiscono anche nella coda delle email.
func EmailEnabled() bool { return emailOn.Load() }

// Notice è un evento da notificare. I destinatari sono l'unione di membri, membri dei
// gruppi, membri votanti degli organi e, con Followers, osservatori dell'elemento.
type Notice struct {
	Kind      string
	ElementID string
	ActorID   string // membro che ha compiuto l'azione (escluso dai destinatari)
	Members   []string
	Groups    []string
	Bodies    []string
	Followers bool
	Exclude   []string // es. i menzionati, che hanno già la loro notifica
	Payload   map[string]any
}

// followersSQL: osservatori dell'elemento $1, tolti quelli che hanno scelto di non seguirlo.
const followersSQL = `
	select x.member_id from (
		select w.member_id from watchers w where w.element_id = $1 and not w.muted
		union
		select e.reporter_id from elements e where e.id = $1 and e.reporter_id is not null
		union
		select sh.member_id from stakeholders sh where sh.element_id = $1 and sh.member_id is not null
		union
		select gm.member_id from stakeholders sh, lateral group_effective_members(sh.group_id) gm(member_id)
		where sh.element_id = $1
	) x
	where not exists (select 1 from watchers w where w.element_id = $1 and w.member_id = x.member_id and w.muted)`

// canReadSQL: il membro m può leggere l'elemento $1 (stessa regola di engine.CanRead:
// element.read a livello di tenant, oppure segnalatore o stakeholder). Prima decide lo
// spazio (migrazione 0026): uno spazio riservato senza un ruolo del membro nega, un ruolo
// sullo spazio concede.
const canReadSQL = `(element_space_read($1, m.id) = 1 or element_space_read($1, m.id) = 0 and (
	exists (select 1 from role_assignments ra
		join roles ro on ro.tenant_id = ra.tenant_id and ro.id = ra.role_id
		where ra.space_id is null
		  and (ra.member_id = m.id or ra.group_id in (select member_groups(m.id)))
		  and ro.permissions && array['*', 'element.read'])
	or exists (select 1 from elements e where e.id = $1 and e.reporter_id = m.id)
	or exists (select 1 from stakeholders sh where sh.element_id = $1
		and (sh.member_id = m.id or sh.group_id in (select member_groups(m.id))))))`

// Emit scrive una riga di notifica per ogni destinatario e, se le email sono attive e la
// persona non le ha silenziate, un evento nell'outbox. Restituisce i destinatari.
func Emit(ctx context.Context, tx pgx.Tx, n Notice) ([]string, error) {
	if n.ElementID == "" || !ValidKind(n.Kind) {
		return nil, nil
	}
	if len(n.Members) == 0 && len(n.Groups) == 0 && len(n.Bodies) == 0 && !n.Followers {
		return nil, nil
	}
	payload := n.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	rows, err := tx.Query(ctx, `
		with cand as (
			select unnest($2::uuid[]) as member_id
			union
			select gm.member_id from unnest($3::uuid[]) g(id), lateral group_effective_members(g.id) gm(member_id)
			union
			select bm.member_id from unnest($4::uuid[]) b(id), lateral body_effective_members(b.id) bm where bm.voting
			union
			select f.member_id from (`+followersSQL+`) f where $5
		),
		rcpt as (
			select distinct m.id as member_id
			from cand c
			join members m on m.id = c.member_id and m.status = 'active'
			left join notification_prefs p on p.member_id = m.id
			where m.id is distinct from $6::uuid
			  and not (m.id = any($7::uuid[]))
			  and not ($8 = any(coalesce(p.muted_kinds, '{}')))
			  and not ($8 = any($11::text[]) and exists (select 1 from notification_silences z where z.member_id = m.id and z.element_id = $1))
			  and `+canReadSQL+`
		),
		ins as (
			insert into notifications (tenant_id, member_id, element_id, actor_id, kind, payload)
			select app_tenant(), member_id, $1, $6::uuid, $8, $9 from rcpt
			returning id, member_id
		),
		mail as (
			insert into outbox_events (tenant_id, kind, payload)
			select app_tenant(), 'notification.email', jsonb_build_object('notification_id', ins.id, 'member_id', ins.member_id)
			from ins left join notification_prefs p on p.member_id = ins.member_id
			where $10 and not ($8 = any(coalesce(p.email_muted_kinds, '{}')))
			returning 1
		)
		select member_id::text from ins`,
		n.ElementID, ids(n.Members), ids(n.Groups), ids(n.Bodies), n.Followers, nullable(n.ActorID), ids(n.Exclude),
		n.Kind, payload, EmailEnabled(), Informational)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// ApprovalActive notifica gli assegnatari dei passi diventati attivi in una richiesta di
// approvazione (membri, gruppi, organi e i loro delegati). prevOrdinal è l'ordinale attivo
// prima dell'operazione: se non è cambiato, nessuno riceve una seconda notifica.
func ApprovalActive(ctx context.Context, tx pgx.Tx, actorID, requestID string, prevOrdinal int) error {
	var active *int
	var elementID, stage string
	err := tx.QueryRow(ctx, `select (select min(ordinal) from approval_steps where request_id = rq.id and decision = 'pending'),
		rq.element_id, rq.stage from approval_requests rq where rq.id = $1 and rq.status = 'pending'`, requestID).Scan(&active, &elementID, &stage)
	if err == pgx.ErrNoRows || (err == nil && (active == nil || *active <= prevOrdinal)) {
		return nil
	}
	if err != nil {
		return err
	}
	rows, err := tx.Query(ctx, `
		with a as (
			select a.member_id, a.group_id, a.body_id from approval_step_assignees a
			join approval_steps s on s.tenant_id = a.tenant_id and s.id = a.step_id
			where s.request_id = $1 and s.ordinal = $2 and s.decision = 'pending'
		),
		who as (
			select member_id from a where member_id is not null
			union
			select gm.member_id from a, lateral group_effective_members(a.group_id) gm(member_id)
			union
			select bm.member_id from a, lateral body_effective_members(a.body_id) bm where bm.voting
		)
		select member_id::text from who
		union
		select d.to_member_id::text from delegations d
		where d.from_member_id in (select member_id from who) and d.scope in ('approvals', 'all')
		  and now() between d.valid_from and d.valid_to`, requestID, *active)
	if err != nil {
		return err
	}
	members, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return err
	}
	_, err = Emit(ctx, tx, Notice{Kind: KindApproval, ElementID: elementID, ActorID: actorID, Members: members,
		Payload: map[string]any{"request_id": requestID, "stage": stage}})
	return err
}

func ids(v []string) []string {
	out := make([]string, 0, len(v))
	for _, s := range v {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
