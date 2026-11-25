// Package privacy è la Vista DPO (ondata 3, replica/usabilita/governo.md, C8): registro dei
// trattamenti (art. 30 GDPR), DPIA (art. 35), violazioni dei dati personali (art. 33) e flussi di
// dati dalle applicazioni del catalogo. I riferimenti normativi sono da verificare da una persona.
//
// Le violazioni non hanno una tabella propria: sono gli incidenti che toccano dati personali, letti
// dal modulo dei rischi; qui stanno solo il segno del DPO, la notifica al Garante e i trattamenti coinvolti.
package privacy

import (
	"context"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
	"regia/internal/platform/rbac"
)

// Service è il modulo; non tiene stato (tutto sta nel database del tenant).
type Service struct {
	now func() time.Time
}

// New crea il servizio.
func New() *Service { return &Service{now: time.Now} }

// BreachHours: ore entro cui notificare una violazione all'autorità (art. 33 GDPR, da verificare).
const BreachHours = 72

// LegalBases sono le basi giuridiche (art. 6 GDPR, da verificare): chiave e nome.
var LegalBases = []struct{ Key, Name string }{
	{"consent", "Consenso"},
	{"contract", "Esecuzione di un contratto"},
	{"legal_obligation", "Obbligo di legge"},
	{"vital_interests", "Interessi vitali"},
	{"public_task", "Compito di interesse pubblico"},
	{"legitimate_interests", "Legittimo interesse"},
}

func legalBasisName(k string) string {
	for _, b := range LegalBases {
		if b.Key == k {
			return b.Name
		}
	}
	return ""
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isUUID(s string) bool { return uuidRe.MatchString(s) }

func requireRead(a engine.Actor) error {
	if !a.Has(rbac.PrivacyRead) && !a.Has(rbac.PrivacyWrite) {
		return engine.ErrForbidden
	}
	return nil
}

func requireWrite(a engine.Actor) error {
	if !a.Has(rbac.PrivacyWrite) {
		return engine.ErrForbidden
	}
	return nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func auditEv(ctx context.Context, tx pgx.Tx, a engine.Actor, action, entity, id string, payload map[string]any) error {
	return audit.Append(ctx, tx, audit.Event{Action: action, EntityType: entity, EntityID: id,
		ActorAccountID: a.AccountID, ActorMemberID: a.MemberID, IP: a.IP, Payload: payload})
}

// cleanList toglie vuoti e doppioni, taglia gli spazi e verifica la lunghezza.
func cleanList(field string, in []string, maxItems, maxLen int) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[strings.ToLower(s)] {
			continue
		}
		if utf8.RuneCountInString(s) > maxLen {
			return nil, engine.TooLong(field, maxLen, "voce troppo lunga (massimo %d caratteri)", maxLen)
		}
		seen[strings.ToLower(s)] = true
		out = append(out, s)
	}
	if len(out) > maxItems {
		return nil, engine.OutOfRange(field, 0, maxItems, "al massimo %d voci", maxItems)
	}
	return out, nil
}

func checkLen(field, s string, max int) error {
	if utf8.RuneCountInString(s) > max {
		return engine.TooLong(field, max, "testo troppo lungo (massimo %d caratteri)", max)
	}
	return nil
}

// dataKinds dice quali tipi di dati dichiara la classificazione di un'applicazione (campo «Dati»
// del catalogo): personali (anche particolari e sanitari), credenziali, finanziari.
func dataKinds(class string) (personal, credentials, financial bool) {
	c := strings.ToLower(class)
	for _, w := range []string{"personal", "particolar", "sanitar", "special", "health"} {
		if strings.Contains(c, w) {
			personal = true
		}
	}
	credentials = strings.Contains(c, "credenzial") || strings.Contains(c, "credential")
	financial = strings.Contains(c, "finanziar") || strings.Contains(c, "financial")
	return
}
