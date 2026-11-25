package tenant

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/i18n"
)

// Provisioning nella lingua scelta (replica/lingua.md, passo 3). I moduli scrivono i dati di
// partenza in italiano; per un tenant che nasce in inglese Localize porta in inglese i nomi
// che si vedono (tipi, ruoli, ruoli sugli elementi, spazi, relazioni, campi, processi,
// politiche, modelli…), con la traduzione di i18n.NamesEn.
//
// Si traduce solo il testo mostrato, mai ciò che il codice e i processi cercano per nome:
//   - le fasi (i processi, le regole e il codice le citano per nome), le opzioni dei campi (le
//     condizioni le citano per valore), i gruppi (le regole e il provisioning li cercano per nome)
//     e i team valutatori restano come i moduli li creano;
//   - gli oggetti versionati e immutabili una volta pubblicati (scorecard, modelli di questionario) e le
//     descrizioni dei pacchetti di partenza restano in italiano.
//
// È idempotente: cambia solo i nomi che sono ancora quelli italiani di partenza, quindi si può
// ripetere dopo un nuovo provisioning senza toccare ciò che il tenant ha già rinominato.

// localizedColumns sono le colonne di nomi mostrati che si traducono, per tabella.
var localizedColumns = []struct {
	table string
	cols  []string
}{
	{"roles", []string{"name"}},
	{"stakeholder_roles", []string{"name"}},
	{"spaces", []string{"name"}},
	{"relation_types", []string{"outward_name", "inward_name"}},
	{"field_defs", []string{"name", "help"}},
	{"process_definitions", []string{"name"}},
	{"approval_policies", []string{"name"}},
	{"review_types", []string{"name"}},
	{"risk_matrices", []string{"name"}},
	{"incident_schemes", []string{"name"}},
	{"governance_bodies", []string{"name"}},
	{"kb_templates", []string{"name"}},
}

// Localize applica la lingua ai nomi di sistema del tenant della transazione. Con l'italiano non
// fa nulla (i dati di partenza sono già italiani).
func Localize(ctx context.Context, tx pgx.Tx, lang string) error {
	if lang == "" || lang == i18n.It {
		return nil
	}
	names := i18n.NamesEn()
	its := make([]string, 0, len(names))
	for it := range names {
		its = append(its, it)
	}
	slices.Sort(its)
	ens := make([]string, len(its))
	for i, it := range its {
		ens[i] = names[it]
	}

	// Tipi di sistema: l'inglese diventa il nome, l'italiano passa in labels.it, così chi usa
	// l'interfaccia in italiano continua a vedere i nomi italiani (/type-names?lang=it).
	if _, err := tx.Exec(ctx, `update element_types
		set labels = labels || jsonb_build_object('it', jsonb_build_object('name', name, 'plural', coalesce(plural_name, name)))
		where is_system and not labels ? 'it' and name = any($1)`, its); err != nil {
		return fmt.Errorf("tipi: %w", err)
	}
	for _, col := range []string{"name", "plural_name"} {
		if _, err := tx.Exec(ctx, fmt.Sprintf(`update element_types t set %[1]s = d.en
			from unnest($1::text[], $2::text[]) d(it, en) where t.is_system and t.%[1]s = d.it`, col), its, ens); err != nil {
			return fmt.Errorf("tipi: %w", err)
		}
	}
	// Fasi e opzioni dei campi: il nome resta italiano (lo citano codice, condizioni e processi), l'inglese
	// va nell'etichetta della lingua, che l'interfaccia mostra al posto del nome.
	if _, err := tx.Exec(ctx, `update options o set labels = o.labels || jsonb_build_object('en', d.en)
		from unnest($1::text[], $2::text[]) d(it, en)
		where o.name = d.it and d.en <> d.it and not o.labels ? 'en'`, its, ens); err != nil {
		return fmt.Errorf("opzioni: %w", err)
	}
	for _, t := range localizedColumns {
		for _, col := range t.cols {
			if _, err := tx.Exec(ctx, fmt.Sprintf(`update %[1]s t set %[2]s = d.en
				from unnest($1::text[], $2::text[]) d(it, en) where t.%[2]s = d.it`, t.table, col), its, ens); err != nil {
				return fmt.Errorf("%s.%s: %w", t.table, col, err)
			}
		}
	}
	return nil
}
