package architecture

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/audit"
)

// Import del catalogo applicativo da CSV (export di un CMDB o foglio di calcolo).
// Colonne riconosciute (intestazione obbligatoria, italiano o inglese, ordine libero):
//
//	codice, nome, descrizione, criticita, funzione_critica, classificazione_dati,
//	hosting, rto_minuti, rpo_minuti, team, asset
//
// Il codice identifica l'applicazione: se esiste si aggiorna, altrimenti si crea.
// "team" crea il team se manca; "asset" (più nomi separati da "|") crea gli asset
// mancanti e li collega con "composto da". Separatore ";" o "," rilevato dalla prima riga.

// ErrDryRun fa annullare la transazione della prova senza errore per l'utente.
var ErrDryRun = errors.New("prova: nessuna modifica salvata")

type ImportRow struct {
	Line    int    `json:"line"`
	Code    string `json:"code"`
	Name    string `json:"name"`
	Outcome string `json:"outcome"` // created | updated | error
	Message string `json:"message,omitempty"`
}

type ImportReport struct {
	DryRun        bool        `json:"dry_run"`
	Created       int         `json:"created"`
	Updated       int         `json:"updated"`
	Errors        int         `json:"errors"`
	TeamsCreated  []string    `json:"teams_created"`
	AssetsCreated []string    `json:"assets_created"`
	Rows          []ImportRow `json:"rows"`
}

var headerAliases = map[string]string{
	"codice": "code", "code": "code", "id": "code",
	"nome": "name", "name": "name", "applicazione": "name", "application": "name",
	"descrizione": "description", "description": "description",
	"criticita": "criticality", "criticità": "criticality", "criticality": "criticality",
	"funzione_critica": "critical", "critical_function": "critical", "funzione critica": "critical",
	"classificazione_dati": "data", "data_classification": "data", "classificazione dati": "data",
	"hosting":    "hosting",
	"rto_minuti": "rto", "rto": "rto", "rto_minutes": "rto",
	"rpo_minuti": "rpo", "rpo": "rpo", "rpo_minutes": "rpo",
	"team": "team", "team_owner": "team", "owner": "team",
	"asset": "asset", "assets": "asset", "asset_logici": "asset",
}

const maxImportRows = 5000

func (s *Service) ImportApplications(ctx context.Context, tx pgx.Tx, actor engine.Actor, data string, dryRun bool) (*ImportReport, error) {
	if !actor.Has(PermWrite) {
		return nil, engine.ErrForbidden
	}
	data = strings.TrimPrefix(data, "\ufeff") // BOM di Excel
	first, _, _ := strings.Cut(data, "\n")
	r := csv.NewReader(strings.NewReader(data))
	if strings.Count(first, ";") > strings.Count(first, ",") {
		r.Comma = ';'
	}
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	header, err := r.Read()
	if err != nil {
		return nil, engine.InvalidParams("validation.architecture.csv_unreadable", "csv", map[string]any{"detail": err}, "file vuoto o non leggibile: %v", err)
	}
	cols := map[string]int{}
	for i, h := range header {
		key, ok := headerAliases[strings.ToLower(strings.TrimSpace(h))]
		if !ok {
			return nil, engine.InvalidValue("csv", "colonna %q non riconosciuta (codice, nome, descrizione, criticita, funzione_critica, classificazione_dati, hosting, rto_minuti, rpo_minuti, team, asset)", h)
		}
		cols[key] = i
	}
	if _, ok := cols["name"]; !ok {
		return nil, engine.Required("csv", "manca la colonna \"nome\"")
	}
	rep := &ImportReport{DryRun: dryRun, TeamsCreated: []string{}, AssetsCreated: []string{}, Rows: []ImportRow{}}
	line := 1
	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		line++
		if err != nil {
			rep.Rows = append(rep.Rows, ImportRow{Line: line, Outcome: "error", Message: err.Error()})
			rep.Errors++
			continue
		}
		if len(rep.Rows) >= maxImportRows {
			return nil, engine.InvalidParams("validation.architecture.import_too_many_rows", "csv", map[string]any{"max": maxImportRows}, "troppe righe (massimo %d per import)", maxImportRows)
		}
		get := func(k string) string {
			if i, ok := cols[k]; ok && i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		if strings.Join(rec, "") == "" {
			continue
		}
		row := ImportRow{Line: line, Code: get("code"), Name: get("name")}
		// Ogni riga in un savepoint: una riga sbagliata non annulla le altre.
		sp, err := tx.Begin(ctx)
		if err != nil {
			return nil, err
		}
		outcome, teams, assets, err := s.importRow(ctx, sp, actor, get)
		if err != nil {
			_ = sp.Rollback(ctx)
			var ve *engine.ValidationError
			if !errors.As(err, &ve) {
				return nil, err
			}
			row.Outcome, row.Message = "error", ve.Error()
			rep.Errors++
		} else {
			if err := sp.Commit(ctx); err != nil {
				return nil, err
			}
			row.Outcome = outcome
			rep.TeamsCreated = append(rep.TeamsCreated, teams...)
			rep.AssetsCreated = append(rep.AssetsCreated, assets...)
			if outcome == "created" {
				rep.Created++
			} else {
				rep.Updated++
			}
		}
		rep.Rows = append(rep.Rows, row)
	}
	if err := audit.Append(ctx, tx, audit.Event{Action: "application.import", EntityType: AppType,
		ActorAccountID: actor.AccountID, ActorMemberID: actor.MemberID, IP: actor.IP,
		Payload: map[string]any{"created": rep.Created, "updated": rep.Updated, "errors": rep.Errors, "dry_run": dryRun}}); err != nil {
		return nil, err
	}
	if dryRun {
		return rep, ErrDryRun
	}
	return rep, nil
}

func (s *Service) importRow(ctx context.Context, tx pgx.Tx, actor engine.Actor, get func(string) string) (outcome string, teams, assets []string, err error) {
	fail := func(e error) (string, []string, []string, error) { return "", nil, nil, e }
	name := get("name")
	if name == "" {
		return fail(engine.Required("nome", "manca il nome"))
	}
	in := ApplicationInput{Name: &name}
	set := func(k string, dst **string) {
		if v := get(k); v != "" {
			*dst = &v
		}
	}
	set("description", &in.Description)
	set("code", &in.Code)
	set("criticality", &in.Criticality)
	set("data", &in.DataClassification)
	set("hosting", &in.Hosting)
	if v := strings.ToLower(get("critical")); v != "" {
		b := v == "sì" || v == "si" || v == "yes" || v == "true" || v == "1" || v == "x"
		if !b && v != "no" && v != "false" && v != "0" {
			return fail(engine.InvalidValue("funzione_critica", "valore %q non valido (sì o no)", v))
		}
		in.CriticalFunction = &b
	}
	for k, dst := range map[string]**int{"rto": &in.RTOMinutes, "rpo": &in.RPOMinutes} {
		if v := get(k); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fail(engine.InvalidParams("validation.architecture.minutes_not_number", k, map[string]any{"value": v}, "%q non è un numero di minuti", v))
			}
			*dst = &n
		}
	}
	if t := get("team"); t != "" {
		id, err := teamByName(ctx, tx, t)
		if err != nil {
			return fail(err)
		}
		if id == "" {
			if id, err = createTeam(ctx, tx, actor, TeamInput{Name: t}); err != nil {
				return fail(err)
			}
			teams = append(teams, t)
		}
		in.TeamID = &id
	}
	var appID string
	if in.Code != nil {
		err := tx.QueryRow(ctx, "select element_id from ext_application where lower(code) = lower($1)", *in.Code).Scan(&appID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fail(err)
		}
	}
	if appID == "" {
		app, err := s.CreateApplication(ctx, tx, actor, in)
		if err != nil {
			return fail(err)
		}
		appID, outcome = app.ID, "created"
	} else {
		if _, err := s.UpdateApplication(ctx, tx, actor, appID, in); err != nil {
			return fail(err)
		}
		outcome = "updated"
	}
	for _, an := range strings.Split(get("asset"), "|") {
		an = strings.TrimSpace(an)
		if an == "" {
			continue
		}
		id, err := assetByName(ctx, tx, an)
		if err != nil {
			return fail(err)
		}
		if id == "" {
			if id, err = s.createAsset(ctx, tx, actor, AssetInput{Name: an}); err != nil {
				return fail(fmt.Errorf("asset %q: %w", an, err))
			}
			assets = append(assets, an)
		}
		if err := s.AddComponent(ctx, tx, actor, id, appID, RelComposedOf); err != nil {
			return fail(err)
		}
	}
	return outcome, teams, assets, nil
}
