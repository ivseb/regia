// Package audit scrive eventi nel registro a prova di manomissione.
// La catena di hash è calcolata nel database (funzione audit_append), dentro la
// stessa transazione della modifica: o si salvano entrambe o nessuna.
package audit

import (
	"context"
	"net/netip"

	"github.com/jackc/pgx/v5"
)

type Event struct {
	Action         string // es. "tenant.create", "element.update"
	EntityType     string
	EntityID       string // vuoto se non applicabile
	Payload        map[string]any
	ActorAccountID string
	ActorMemberID  string
	IP             netip.Addr
}

// Append aggiunge l'evento alla catena del tenant della transazione (app.tenant_id),
// o a quella di piattaforma se la transazione non ha tenant.
// Regola GDPR: nel payload solo identificativi, mai dati personali in chiaro.
func Append(ctx context.Context, tx pgx.Tx, e Event) error {
	var ip any
	if e.IP.IsValid() {
		ip = e.IP.String()
	}
	payload := e.Payload
	if payload == nil {
		payload = map[string]any{}
	}
	if id := APIToken(ctx); id != "" {
		// Richiesta fatta con un token API: l'evento dice quale (copia, la mappa è del chiamante).
		p := make(map[string]any, len(payload)+1)
		for k, v := range payload {
			p[k] = v
		}
		p["api_token"] = id
		payload = p
	}
	_, err := tx.Exec(ctx, "select audit_append($1, $2, $3, $4, $5, $6, $7)",
		e.Action, e.EntityType, nullable(e.EntityID), payload, nullable(e.ActorAccountID), nullable(e.ActorMemberID), ip)
	return err
}

// LockChain prende il lock della catena di audit del tenant (lo stesso di audit_append), fino alla
// fine della transazione. Va preso PRIMA di un lock di riga che la stessa transazione prende e poi
// tiene mentre scrive un evento (la numerazione degli spazi): una transazione che ha già scritto un
// evento e poi chiede quel lock di riga, contro una che lo tiene e aspetta l'audit, andrebbe in
// deadlock. Con un solo ordine (audit, poi riga) l'attesa è sempre in una direzione sola.
func LockChain(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, "select pg_advisory_xact_lock(hashtextextended('mytrack.audit:' || coalesce(app_tenant()::text, 'platform'), 0))")
	return err
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

type ctxKey struct{}

// WithAPIToken segna il contesto di una richiesta autenticata con un token API:
// ogni evento scritto in quel contesto riporta l'id del token nel payload.
func WithAPIToken(ctx context.Context, tokenID string) context.Context {
	return context.WithValue(ctx, ctxKey{}, tokenID)
}

// APIToken è l'id del token API della richiesta, vuoto per le sessioni.
func APIToken(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)
	return id
}
