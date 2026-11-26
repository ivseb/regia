package finance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/modules/work"
)

// Import CSV delle ore (target work_logs): stesso flusso dei costi (profilo, prova, conferma,
// annullamento), righe diverse. Ogni riga è una registrazione di ore di una persona
// su un elemento, con le stesse regole delle ore inserite a mano: elemento visibile, persona
// del tenant, settimana del timesheet ancora aperta.

// maxDayMinutes: una riga non supera le 24 ore (come l'inserimento normale).
const maxDayMinutes = 24 * 60

// validateHours controlla la mappatura delle ore; seen sono i campi con una colonna.
func (m *Mapping) validateHours(seen map[string]bool) error {
	if !seen["work_date"] {
		return engine.Required("mapping.columns", "manca la colonna per il campo %q", hoursFields["work_date"])
	}
	if !seen["element"] && m.Defaults["element"] == "" {
		return engine.Required("mapping.columns", "serve la colonna dell'attività o dell'elemento o una chiave di default")
	}
	if !seen["member"] && m.Defaults["member"] == "" {
		return engine.Required("mapping.columns", "serve la colonna della persona o un'email di default")
	}
	if seen["hours"] == seen["minutes"] {
		return engine.InvalidCode("validation.finance.mapping_hours_or_minutes", "mapping.columns", "mappa una sola colonna tra ore e minuti")
	}
	return nil
}

// parseHours legge le ore ("1,5", "0.25", "1h30", "1:30") e restituisce i minuti.
// decimal è il separatore decimale del profilo ("," di default).
func parseHours(v, decimal string) (int, error) {
	orig := v
	if strings.ContainsAny(v, ":hHmMgG") { // durata scritta a mano
		n, err := work.ParseMinutes(v)
		if err != nil {
			return 0, fmt.Errorf("ore %q non valide", orig)
		}
		return n, nil
	}
	if decimal == "" {
		decimal = ","
	}
	v = strings.NewReplacer(" ", "", " ", "", "'", "").Replace(v)
	if decimal == "," {
		v = strings.ReplaceAll(strings.ReplaceAll(v, ".", ""), ",", ".")
	} else {
		v = strings.ReplaceAll(v, ",", "")
	}
	r, ok := new(big.Rat).SetString(v)
	if v == "" || !ok {
		return 0, fmt.Errorf("ore %q non valide", orig)
	}
	r.Mul(r, big.NewRat(60, 1))
	f, _ := r.Float64()
	if f > 1e9 || f < -1e9 {
		return 0, fmt.Errorf("ore %q fuori misura", orig)
	}
	if f < 0 {
		return int(f - 0.5), nil
	}
	return int(f + 0.5), nil
}

func (s *Service) analyzeHours(ctx context.Context, tx pgx.Tx, actor engine.Actor, m Mapping, content string) (*analysis, error) {
	sh, err := openSheet(m, content)
	if err != nil {
		return nil, err
	}
	// Persone del tenant, per email.
	rows, err := tx.Query(ctx, "select id::text, display_name, lower(email) from members where email is not null and status <> 'anonymized'")
	if err != nil {
		return nil, err
	}
	type person struct{ id, name string }
	people := map[string]person{}
	for rows.Next() {
		var id, name, email string
		if err := rows.Scan(&id, &name, &email); err != nil {
			rows.Close()
			return nil, err
		}
		people[email] = person{id, name}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	elements := map[string]*engine.Element{}
	roles := map[string]*string{} // spazio|persona → ruolo nel team
	open := map[string]string{}   // persona|settimana → errore ("" = aperta)
	layout := dateLayouts[m.DateFormat]
	a := &analysis{}
	refs := map[string]bool{}
	err = sh.each(a, func(line int, get func(f string) string, fail func(field, msg string)) error {
		var p parsedRow
		p.Row = line
		ok := true
		// Attività o elemento.
		key := strings.ToUpper(get("element"))
		var el *engine.Element
		if key != "" {
			var seen bool
			if el, seen = elements[key]; !seen {
				if e, err := s.eng.Get(ctx, tx, actor, key); err == nil {
					el = e
				} else if !errors.Is(err, engine.ErrNotFound) {
					return err
				}
				elements[key] = el
			}
		}
		if el == nil {
			fail("element", fmt.Sprintf("attività o elemento %q inesistente o non visibile", key))
			ok = false
		} else {
			p.elementID, p.ElementKey = el.ID, el.Key
		}
		// Persona.
		email := strings.ToLower(get("member"))
		if pe, found := people[email]; found {
			p.memberID, p.Person = pe.id, pe.name
		} else {
			fail("member", fmt.Sprintf("la persona %q non è un membro dell'organizzazione", email))
			ok = false
		}
		// Data.
		if d, err := time.Parse(layout, get("work_date")); err != nil {
			fail("work_date", fmt.Sprintf("data %q non valida (formato %s)", get("work_date"), dateFormatName(m.DateFormat)))
			ok = false
		} else {
			p.date, p.EntryDate = d, d.Format(time.DateOnly)
		}
		// Ore (decimali) o minuti: una sola delle due colonne è mappata.
		mf := "hours"
		if _, isMin := sh.fieldCol["minutes"]; isMin {
			mf = "minutes"
		}
		switch n, err := parseDuration(mf, get(mf), m.Decimal); {
		case err != nil:
			fail(mf, err.Error())
			ok = false
		case n <= 0:
			fail(mf, "le ore devono essere maggiori di zero")
			ok = false
		case n > maxDayMinutes:
			fail(mf, "una riga non supera le 24 ore")
			ok = false
		default:
			p.Minutes = n
		}
		// Tipo di attività, capitalizzabile, testo.
		p.ActivityType = get("activity_type")
		if p.ActivityType == "" {
			p.ActivityType = "work"
		}
		if len(p.ActivityType) > 60 {
			fail("activity_type", "tipo di attività troppo lungo (massimo 60 caratteri)")
			ok = false
		}
		switch strings.ToLower(get("capitalizable")) {
		case "", "no", "n", "false", "0":
		case "si", "sì", "s", "yes", "y", "true", "1":
			p.capitalizable = true
		default:
			fail("capitalizable", "valore non riconosciuto (sì o no)")
			ok = false
		}
		p.Description = get("text")
		if len(p.Description) > 1000 {
			fail("text", "testo troppo lungo (massimo 1000 caratteri)")
			ok = false
		}
		if !ok {
			return nil
		}
		// Settimana del timesheet ancora aperta?
		wk := p.memberID + "|" + p.EntryDate
		msg, done := open[wk]
		if !done {
			if err := work.CheckDayOpen(ctx, tx, p.memberID, p.date); err != nil {
				var ve *engine.ValidationError
				if !errors.As(err, &ve) {
					return err
				}
				msg = ve.Msg
			}
			open[wk] = msg
		}
		if msg != "" {
			fail("work_date", msg)
			return nil
		}
		// Ruolo con cui ha lavorato (quello nel team dello spazio), fotografato come nelle ore normali.
		rk := el.SpaceID + "|" + p.memberID
		role, seen := roles[rk]
		if !seen {
			if err := tx.QueryRow(ctx, `select tm.role_key from team_members tm join teams t on t.tenant_id = tm.tenant_id and t.id = tm.team_id
				where t.delivery_space_id = $1 and tm.member_id = $2 limit 1`, el.SpaceID, p.memberID).Scan(&role); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			roles[rk] = role
		}
		p.roleKey = role
		// Riferimento per l'idempotenza.
		if ref := get("source_ref"); ref != "" {
			p.SourceRef = "ref:" + ref
		} else {
			sum := sha256.Sum256([]byte(strings.Join([]string{p.elementID, p.memberID, p.EntryDate, fmt.Sprint(p.Minutes),
				p.ActivityType, p.Description}, "\x1f")))
			p.SourceRef = "sha:" + hex.EncodeToString(sum[:16])
		}
		if refs[p.SourceRef] {
			p.Duplicate = true
		}
		refs[p.SourceRef] = true
		a.rows = append(a.rows, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Ore già importate (anche da file precedenti).
	if len(a.rows) > 0 {
		all := make([]string, 0, len(refs))
		for r := range refs {
			all = append(all, r)
		}
		rows, err := tx.Query(ctx, "select source_ref from work_logs where source_ref = any($1)", all)
		if err != nil {
			return nil, err
		}
		found, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return nil, err
		}
		existing := map[string]bool{}
		for _, r := range found {
			existing[r] = true
		}
		for i := range a.rows {
			if existing[a.rows[i].SourceRef] {
				a.rows[i].Duplicate = true
			}
		}
	}
	for _, r := range a.rows {
		if r.Duplicate {
			a.duplicates++
		}
	}
	return a, nil
}

// parseDuration legge la durata di una riga: ore (decimali o scritte a mano) o minuti interi.
func parseDuration(field, v, decimal string) (int, error) {
	if field == "hours" {
		return parseHours(v, decimal)
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("minuti %q non validi", v)
	}
	return n, nil
}

// insertHours scrive le ore nuove in un solo insert; i riferimenti già presenti sono
// ignorati. Poi calcola il costo delle ore appena scritte, se le impostazioni hanno la
// categoria dei costi del lavoro.
func (s *Service) insertHours(ctx context.Context, tx pgx.Tx, actor engine.Actor, runID string, rows []parsedRow) (int64, error) {
	var (
		elems, members, refs, acts, texts []string
		roles                             []*string
		dates                             []time.Time
		minutes                           []int32
		caps                              []bool
	)
	for _, r := range rows {
		if r.Duplicate {
			continue
		}
		elems, members, refs, acts = append(elems, r.elementID), append(members, r.memberID), append(refs, r.SourceRef), append(acts, r.ActivityType)
		texts, roles, dates = append(texts, r.Description), append(roles, r.roleKey), append(dates, r.date)
		minutes, caps = append(minutes, int32(r.Minutes)), append(caps, r.capitalizable)
	}
	if len(elems) == 0 {
		return 0, nil
	}
	rs, err := tx.Query(ctx, `insert into work_logs (tenant_id, id, element_id, member_id, work_date, minutes, role_key, activity_type, capitalizable,
		text, source_ref, import_run_id, created_by)
		select app_tenant(), gen_random_uuid(), e, m, d, n, ro, ac, cap, nullif(te, ''), r, $10, $11
		from unnest($1::uuid[], $2::uuid[], $3::date[], $4::int[], $5::text[], $6::text[], $7::bool[], $8::text[], $9::text[])
		     as t(e, m, d, n, ro, ac, cap, te, r)
		on conflict (tenant_id, source_ref) where source_ref is not null do nothing
		returning id::text`,
		elems, members, dates, minutes, roles, acts, caps, texts, refs, runID, nullable(actor.MemberID))
	if err != nil {
		return 0, err
	}
	ids, err := pgx.CollectRows(rs, pgx.RowTo[string])
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if st, err := GetSettings(ctx, tx); err != nil {
		return 0, err
	} else if st.LaborCategoryID != nil {
		if _, err := s.recompute(ctx, tx, actor, workLogScope{ids: ids}); err != nil {
			return 0, err
		}
	}
	return int64(len(ids)), nil
}
