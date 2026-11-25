// Package i18n è il piccolo catalogo dei testi del server (replica/lingua.md, passo 3):
// notifiche ed email, nomi leggibili dei fatti e delle azioni dei processi, dati dei tenant
// nuovi. Niente dipendenze esterne. Le lingue sono quelle del client (it, en); l'italiano è la
// lingua di riserva, quindi un testo senza traduzione resta leggibile.
//
// Un testo ha una chiave (notify.mention), le due lingue e segnaposto {nome}. Un plurale ha la
// forma del singolare (OneIt, OneEn) e quella degli altri casi, scelte dal parametro count. La
// lingua di una persona è la sua scelta, poi la predefinita del tenant, poi l'italiano
// (Resolve, MemberLang).
package i18n

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

const (
	It      = "it"
	En      = "en"
	Default = It
)

// Langs sono le lingue supportate (le stesse di web/src/i18n e della migrazione 0051).
var Langs = []string{It, En}

// OK dice se l è una lingua supportata.
func OK(l string) bool { return slices.Contains(Langs, l) }

// Norm porta una lingua qualsiasi a una supportata ("en-GB" diventa "en"); il resto è l'italiano.
func Norm(l string) string {
	l = strings.ToLower(strings.TrimSpace(l))
	if base, _, ok := strings.Cut(l, "-"); ok {
		l = base
	}
	if OK(l) {
		return l
	}
	return Default
}

// Resolve applica la precedenza: scelta del membro, poi predefinita del tenant, poi italiano.
func Resolve(member, tenant *string) string {
	for _, c := range []*string{member, tenant} {
		if c != nil && OK(*c) {
			return *c
		}
	}
	return Default
}

type ctxKey struct{}

// WithLang lega la lingua al contesto: chi compone testi per la persona (motore, moduli) la legge
// con Lang, senza portare un parametro in più in ogni funzione.
func WithLang(ctx context.Context, lang string) context.Context {
	return context.WithValue(ctx, ctxKey{}, Norm(lang))
}

// Lang è la lingua del contesto (italiano se non c'è).
func Lang(ctx context.Context) string {
	if l, ok := ctx.Value(ctxKey{}).(string); ok {
		return l
	}
	return Default
}

// MemberLang è la lingua effettiva di un membro nel tenant della transazione.
func MemberLang(ctx context.Context, tx pgx.Tx, memberID string) (string, error) {
	var member, tenant *string
	if memberID != "" {
		err := tx.QueryRow(ctx, "select locale from members where id = $1", memberID).Scan(&member)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Default, err
		}
	}
	if member == nil || !OK(*member) {
		if err := tx.QueryRow(ctx, "select locale from tenant_language").Scan(&tenant); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return Default, err
		}
	}
	return Resolve(member, tenant), nil
}

// TenantLang è la lingua predefinita del tenant della transazione (italiano se non scelta).
func TenantLang(ctx context.Context, tx pgx.Tx) (string, error) {
	var l *string
	if err := tx.QueryRow(ctx, "select locale from tenant_language").Scan(&l); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Default, err
	}
	return Resolve(nil, l), nil
}

// ---------------------------------------------------------------------------
// Catalogo

// Msg è un testo nelle due lingue. Per un plurale, OneIt e OneEn sono la forma del singolare
// (parametro count uguale a 1); It e En sono le altre.
type Msg struct {
	It, En       string
	OneIt, OneEn string
}

// P sono i parametri di un testo: {nome} diventa il valore.
type P map[string]any

var catalog = map[string]Msg{}

// Register aggiunge testi al catalogo. Una chiave doppia o un testo senza una delle due
// lingue è un errore di programmazione e ferma l'avvio.
func Register(m map[string]Msg) {
	for k, v := range m {
		if _, dup := catalog[k]; dup {
			panic("i18n: chiave doppia " + k)
		}
		if v.It == "" || v.En == "" {
			panic("i18n: manca una lingua per " + k)
		}
		catalog[k] = v
	}
}

// Has dice se la chiave esiste.
func Has(key string) bool { _, ok := catalog[key]; return ok }

// Keys elenca le chiavi del catalogo, in ordine (per i test).
func Keys() []string {
	out := make([]string, 0, len(catalog))
	for k := range catalog {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// Entry restituisce il testo grezzo della chiave.
func Entry(key string) (Msg, bool) { m, ok := catalog[key]; return m, ok }

// T è il testo della chiave nella lingua, con i segnaposto sostituiti. Se p ha count uguale a 1
// e il testo ha la forma del singolare, usa quella. Una chiave sconosciuta restituisce la
// chiave: si vede e non rompe nulla.
func T(lang, key string, p ...P) string {
	m, ok := catalog[key]
	if !ok {
		return key
	}
	var params P
	if len(p) > 0 {
		params = p[0]
	}
	en := lang == En
	text := m.It
	if en {
		text = m.En
	}
	if n, ok := params["count"]; ok && fmt.Sprint(n) == "1" {
		if !en && m.OneIt != "" {
			text = m.OneIt
		} else if en && m.OneEn != "" {
			text = m.OneEn
		}
	}
	return fill(text, params)
}

// Tc è T con la lingua presa dal contesto.
func Tc(ctx context.Context, key string, p ...P) string { return T(Lang(ctx), key, p...) }

func fill(text string, p P) string {
	if len(p) == 0 || !strings.Contains(text, "{") {
		return text
	}
	var b strings.Builder
	for i := 0; i < len(text); {
		if text[i] == '{' {
			if j := strings.IndexByte(text[i:], '}'); j > 0 {
				if v, ok := p[text[i+1:i+j]]; ok {
					fmt.Fprint(&b, v)
					i += j + 1
					continue
				}
			}
		}
		b.WriteByte(text[i])
		i++
	}
	return b.String()
}

// Name è il nome leggibile di un dato di sistema (fatto, azione, controllo, tipo, ruolo…) nella
// lingua: la sorgente è il testo italiano, l'inglese sta in namesEn (names_en.go). Senza
// traduzione resta l'italiano.
func Name(lang, it string) string {
	if lang == En {
		if en, ok := namesEn[it]; ok {
			return en
		}
	}
	return it
}

// NameEn è la traduzione inglese di un nome di sistema, se c'è.
func NameEn(it string) (string, bool) { en, ok := namesEn[it]; return en, ok }

// NamesEn elenca le coppie italiano-inglese dei nomi di sistema (per il provisioning e i test).
func NamesEn() map[string]string { return namesEn }
