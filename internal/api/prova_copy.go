package api

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/db"
)

// Copia della configurazione di un tenant in un altro (ambiente di prova, migrazione 0065).
//
// La copia mantiene gli stessi id: la chiave primaria è (tenant_id, id), quindi lo stesso id può
// stare in due tenant. Così i riferimenti interni (tipi, campi, insiemi di valori, spazi) e quelli
// dentro i JSON (processi, regole) restano validi senza rimappare niente, e «promuovere» un oggetto
// nuovo nel principale non crea conflitti. Si rimappano solo i riferimenti a persone (tutti sulla
// persona che amministra la prova) e al gruppo «tutti i membri».
//
// Ogni lettura e ogni scrittura avviene dopo aver legato la transazione al tenant giusto
// (set_config su app.tenant_id): la RLS vale sempre.

// copySpec è una tabella di configurazione da copiare; l'ordine rispetta le chiavi esterne.
type copySpec struct {
	table string
	where string // filtro sulle righe da copiare («true» = tutte)
}

var provaTables = []copySpec{
	{"option_sets", "true"},
	{"options", "true"},
	{"workflows", "true"},
	{"element_types", "true"},
	{"field_defs", "true"},
	{"groups", "system_kind is null"},
	{"spaces", "true"},
	{"space_types", "true"},
	{"type_fields", "true"},
	{"relation_types", "true"},
	{"stakeholder_roles", "true"},
	{"governance_bodies", "true"},
	{"approval_policies", "true"},
	{"review_teams", "true"},
	{"review_types", "true"},
	{"questionnaire_templates", "status = 'published'"},
	{"process_definitions", "true"},
	{"process_versions", "status = 'published'"},
	{"gate_policies", "true"},
	{"tenant_modules", "true"},
	{"requirement_catalogs", "status = 'published'"},
	{"scorecards", "true"},
	{"grc_settings", "true"},
	{"risk_settings", "true"},
	{"ea_settings", "true"},
	{"portfolio_settings", "true"},
	{"devsecops_settings", "true"},
	{"time_settings", "true"},
	{"report_settings", "true"},
	{"dora_settings", "true"},
	{"tenant_appearance", "true"},
}

// provaAppend: tabelle di versioni pubblicate immutabili (non si cancellano): la copia aggiunge una
// versione nuova se il contenuto differisce, e la pulizia toglie solo le bozze.
var provaAppend = map[string]bool{"questionnaire_templates": true, "requirement_catalogs": true}

// sampleLimit: quanti elementi per tipo entrano nel campione.
const sampleLimit = 6

// bindTo lega la transazione a un tenant: da qui in poi la RLS mostra solo i suoi dati.
func bindTo(ctx context.Context, tx pgx.Tx, tenantID string) error {
	_, err := tx.Exec(ctx, "select set_config('app.tenant_id', $1, true)", tenantID)
	return err
}

// readRows legge, nel tenant corrente, le righe della tabella come JSON.
func readRows(ctx context.Context, tx pgx.Tx, table, where string) (string, error) {
	var raw []byte
	err := tx.QueryRow(ctx, fmt.Sprintf("select coalesce(jsonb_agg(to_jsonb(t)), '[]'::jsonb) from %s t where %s", table, where)).Scan(&raw)
	return string(raw), err
}

// writeRows scrive nel tenant corrente le righe lette da readRows. Con skipExisting una riga già
// presente (stessa chiave) resta com'è.
func writeRows(ctx context.Context, tx pgx.Tx, table, rows string, skipExisting bool) error {
	if rows == "[]" {
		return nil
	}
	conflict := ""
	if skipExisting {
		conflict = " on conflict do nothing"
	}
	_, err := tx.Exec(ctx, fmt.Sprintf("insert into %[1]s select * from jsonb_populate_recordset(null::%[1]s, $1::jsonb)%[2]s", table, conflict), rows)
	return err
}

// remapper sostituisce gli id (tenant, persone, gruppi) nel testo JSON delle righe.
type remapper map[string]string

func (m remapper) apply(rows string) string {
	if len(m) == 0 {
		return rows
	}
	pairs := make([]string, 0, len(m)*2)
	for from, to := range m {
		pairs = append(pairs, from, to)
	}
	return strings.NewReplacer(pairs...).Replace(rows)
}

// copyTable porta le righe di una tabella da un tenant all'altro.
func copyTable(ctx context.Context, tx pgx.Tx, from, to string, spec copySpec, remap remapper, skipExisting bool) error {
	if err := bindTo(ctx, tx, from); err != nil {
		return err
	}
	rows, err := readRows(ctx, tx, spec.table, spec.where)
	if err != nil {
		return fmt.Errorf("lettura di %s: %w", spec.table, err)
	}
	if err := bindTo(ctx, tx, to); err != nil {
		return err
	}
	if err := writeRows(ctx, tx, spec.table, remap.apply(rows), skipExisting); err != nil {
		return fmt.Errorf("copia di %s: %w", spec.table, err)
	}
	return nil
}

// copyRemap prepara le sostituzioni da principale a prova: tenant, persone → l'amministratore della
// prova, gruppo «tutti i membri» → quello della prova.
func copyRemap(ctx context.Context, tx pgx.Tx, mainID, sbxID, sbxAdminMember string) (remapper, error) {
	remap := remapper{mainID: sbxID}
	if err := bindTo(ctx, tx, mainID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, "select id from members")
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		remap[id] = sbxAdminMember
	}
	var mainAll, sbxAll string
	if err := tx.QueryRow(ctx, "select id from groups where system_kind = 'all_members'").Scan(&mainAll); err != nil {
		return nil, err
	}
	if err := bindTo(ctx, tx, sbxID); err != nil {
		return nil, err
	}
	if err := tx.QueryRow(ctx, "select id from groups where system_kind = 'all_members'").Scan(&sbxAll); err != nil {
		return nil, err
	}
	remap[mainAll] = sbxAll
	return remap, nil
}

// copyConfig copia la configurazione del principale nella prova (vuota o appena svuotata).
func copyConfig(ctx context.Context, tx pgx.Tx, mainID, sbxID, sbxAdminMember string, withSample bool) error {
	remap, err := copyRemap(ctx, tx, mainID, sbxID, sbxAdminMember)
	if err != nil {
		return err
	}
	for _, spec := range provaTables {
		var err error
		if provaAppend[spec.table] {
			err = copyAppendOnly(ctx, tx, mainID, sbxID, spec, remap)
		} else {
			err = copyTable(ctx, tx, mainID, sbxID, spec, remap, false)
		}
		if err != nil {
			return err
		}
	}
	if withSample {
		if err := copySample(ctx, tx, mainID, sbxID, sbxAdminMember, remap); err != nil {
			return err
		}
	}
	return bindTo(ctx, tx, sbxID)
}

// copySample porta nella prova un campione anonimizzato di elementi: solo i tipi senza estensione
// di dominio (le tabelle dei moduli non si copiano), titoli generici, nessun testo, nessun valore di
// campo, nessuna persona; stati e spazi sono quelli veri. Si rinumerano per spazio.
func copySample(ctx context.Context, tx pgx.Tx, mainID, sbxID, sbxAdminMember string, remap remapper) error {
	if err := bindTo(ctx, tx, mainID); err != nil {
		return err
	}
	var raw []byte
	err := tx.QueryRow(ctx, `
		select coalesce(jsonb_agg(
			to_jsonb(e) || jsonb_build_object(
				'number', n, 'title', tname || ' di prova ' || n, 'body', null, 'body_text', '', 'fields', '{}'::jsonb,
				'assignee_id', null, 'reporter_id', null, 'parent_id', null, 'created_by', null, 'updated_by', null,
				'process_version_id', null, 'visibility_limited', false, 'search', null, 'version', 1)
		), '[]'::jsonb)
		from (
			select x.*, row_number() over (partition by x.space_id order by x.created_at, x.id) n
			from (
				select e.*, t.name tname, row_number() over (partition by e.type_id order by e.created_at desc, e.id) rk
				from elements e join element_types t on t.id = e.type_id
				where not e.is_draft and e.deleted_at is null and t.extension is null and e.state_id is not null
			) x where x.rk <= $1
		) e`, sampleLimit).Scan(&raw)
	if err != nil {
		return fmt.Errorf("lettura del campione: %w", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		return err
	}
	if len(rows) == 0 {
		return bindTo(ctx, tx, sbxID)
	}
	// «tname» e «rk» sono colonne d'appoggio della query: non esistono nella tabella.
	for _, r := range rows {
		delete(r, "tname")
		delete(r, "rk")
		delete(r, "n")
	}
	out, err := json.Marshal(rows)
	if err != nil {
		return err
	}
	if err := bindTo(ctx, tx, sbxID); err != nil {
		return err
	}
	if err := writeRows(ctx, tx, "elements", remap.apply(string(out)), false); err != nil {
		return fmt.Errorf("copia del campione: %w", err)
	}
	// Il prossimo numero di ogni spazio riparte dopo l'ultimo del campione.
	_, err = tx.Exec(ctx, `update spaces s set next_number = coalesce((select max(number) from elements e where e.space_id = s.id), 0) + 1`)
	return err
}

// clearConfig svuota la prova dalla configurazione e dagli elementi, per ricopiarla.
func clearConfig(ctx context.Context, tx pgx.Tx, sbxID string) error {
	if err := bindTo(ctx, tx, sbxID); err != nil {
		return err
	}
	// Prima gli elementi (e quel che ne dipende), poi la configurazione in ordine inverso.
	if _, err := tx.Exec(ctx, "delete from elements"); err != nil {
		return fmt.Errorf("pulizia degli elementi: %w", err)
	}
	for i := len(provaTables) - 1; i >= 0; i-- {
		spec := provaTables[i]
		if spec.table == "process_versions" {
			continue // le versioni pubblicate non si cancellano: spariscono con il loro processo (cascata)
		}
		where := spec.where
		if provaAppend[spec.table] {
			where = "status = 'draft'"
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf("delete from %s where %s", spec.table, where)); err != nil {
			return fmt.Errorf("pulizia di %s: %w", spec.table, err)
		}
	}
	return nil
}

// copyAppendOnly porta le versioni pubblicate di una tabella immutabile: se la prova ha già un contenuto
// uguale non fa niente; altrimenti supera la versione pubblicata della prova e ne aggiunge una nuova (il
// numero di versione della prova è suo: il confronto guarda il contenuto, non il numero).
func copyAppendOnly(ctx context.Context, tx pgx.Tx, from, to string, spec copySpec, remap remapper) error {
	if err := bindTo(ctx, tx, from); err != nil {
		return err
	}
	raw, err := readRows(ctx, tx, spec.table, spec.where)
	if err != nil {
		return fmt.Errorf("lettura di %s: %w", spec.table, err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(remap.apply(raw)), &rows); err != nil {
		return err
	}
	if err := bindTo(ctx, tx, to); err != nil {
		return err
	}
	for _, r := range rows {
		var same bool
		err := tx.QueryRow(ctx, fmt.Sprintf(`select exists (select 1 from %s where key = $1 and status = 'published' and name = $2 and description = $3 and definition = $4::jsonb)`, spec.table),
			r["key"], r["name"], r["description"], r["definition"]).Scan(&same)
		if err != nil {
			return err
		}
		if same {
			continue
		}
		if _, err := tx.Exec(ctx, fmt.Sprintf("update %s set status = 'superseded' where key = $1 and status = 'published'", spec.table), r["key"]); err != nil {
			return err
		}
		var next int
		if err := tx.QueryRow(ctx, fmt.Sprintf("select coalesce(max(version), 0) + 1 from %s where key = $1", spec.table), r["key"]).Scan(&next); err != nil {
			return err
		}
		r["version"] = next
		r["id"] = db.NewID()
		one, _ := json.Marshal([]map[string]any{r})
		if err := writeRows(ctx, tx, spec.table, string(one), false); err != nil {
			return fmt.Errorf("copia di %s: %w", spec.table, err)
		}
	}
	return nil
}
