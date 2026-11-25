package privacy

import (
	"context"
	"encoding/csv"
	"errors"
	"io"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/db"
)

// AppRef è un'applicazione del catalogo con ciò che dichiara sui dati (campo «Dati»).
type AppRef struct {
	ID             string `json:"id"`
	Key            string `json:"key"`
	Name           string `json:"name"`
	Classification string `json:"classification"`
	Personal       bool   `json:"personal"`
	Credentials    bool   `json:"credentials"`
	Financial      bool   `json:"financial"`
}

// DPIAStub è lo stato della DPIA di un trattamento, per l'elenco.
type DPIAStub struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// Treatment è una riga del registro dei trattamenti (art. 30 GDPR, da verificare).
type Treatment struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Purpose           string    `json:"purpose"`
	LegalBasis        string    `json:"legal_basis"`
	LegalBasisNote    string    `json:"legal_basis_note"`
	DataSubjects      []string  `json:"data_subjects"`
	DataCategories    []string  `json:"data_categories"`
	SpecialCategories bool      `json:"special_categories"`
	LargeScale        bool      `json:"large_scale"`
	Profiling         bool      `json:"profiling"`
	HighRisk          bool      `json:"high_risk"`
	Recipients        string    `json:"recipients"`
	TransfersExtraEU  bool      `json:"transfers_extra_eu"`
	TransferDetails   string    `json:"transfer_details"`
	Retention         string    `json:"retention"`
	OwnerID           *string   `json:"owner_id"`
	Owner             *string   `json:"owner"`
	Status            string    `json:"status"`
	UpdatedAt         time.Time `json:"updated_at"`
	Applications      []AppRef  `json:"applications"`
	// DPIARequired: almeno un criterio dell'art. 35 (categorie particolari, larga scala, profilazione) o il giudizio del DPO.
	DPIARequired bool      `json:"dpia_required"`
	DPIA         *DPIAStub `json:"dpia"`
	// Issues: no_basis, no_purpose, no_application, dpia_missing.
	Issues []string `json:"issues"`
}

// TreatmentInput è il trattamento da creare o da sostituire per intero.
type TreatmentInput struct {
	Name              string   `json:"name"`
	Purpose           string   `json:"purpose"`
	LegalBasis        string   `json:"legal_basis"`
	LegalBasisNote    string   `json:"legal_basis_note"`
	DataSubjects      []string `json:"data_subjects"`
	DataCategories    []string `json:"data_categories"`
	SpecialCategories bool     `json:"special_categories"`
	LargeScale        bool     `json:"large_scale"`
	Profiling         bool     `json:"profiling"`
	HighRisk          bool     `json:"high_risk"`
	Recipients        string   `json:"recipients"`
	TransfersExtraEU  bool     `json:"transfers_extra_eu"`
	TransferDetails   string   `json:"transfer_details"`
	Retention         string   `json:"retention"`
	OwnerID           string   `json:"owner_id"`
	Status            string   `json:"status"`
	Applications      []string `json:"applications"`
}

const selectTreatment = `
	select t.id::text, t.name, t.purpose, coalesce(t.legal_basis, ''), t.legal_basis_note, t.data_subjects, t.data_categories,
	       t.special_categories, t.large_scale, t.profiling, t.high_risk, t.recipients, t.transfers_extra_eu, t.transfer_details,
	       t.retention, t.owner_id::text, m.display_name, t.status, t.updated_at,
	       d.id::text, d.status
	from privacy_treatments t
	left join members m on m.tenant_id = t.tenant_id and m.id = t.owner_id
	left join privacy_dpias d on d.tenant_id = t.tenant_id and d.treatment_id = t.id`

func scanTreatments(rows pgx.Rows) ([]Treatment, error) {
	defer rows.Close()
	out := []Treatment{}
	for rows.Next() {
		var t Treatment
		var did, dst *string
		if err := rows.Scan(&t.ID, &t.Name, &t.Purpose, &t.LegalBasis, &t.LegalBasisNote, &t.DataSubjects, &t.DataCategories,
			&t.SpecialCategories, &t.LargeScale, &t.Profiling, &t.HighRisk, &t.Recipients, &t.TransfersExtraEU, &t.TransferDetails,
			&t.Retention, &t.OwnerID, &t.Owner, &t.Status, &t.UpdatedAt, &did, &dst); err != nil {
			return nil, err
		}
		if did != nil {
			t.DPIA = &DPIAStub{ID: *did, Status: *dst}
		}
		t.Applications = []AppRef{}
		t.DPIARequired = t.SpecialCategories || t.LargeScale || t.Profiling || t.HighRisk
		out = append(out, t)
	}
	return out, rows.Err()
}

// fill aggiunge le applicazioni e le segnalazioni a una lista di trattamenti (una sola query per le applicazioni).
func fill(ctx context.Context, tx pgx.Tx, list []Treatment) error {
	if len(list) == 0 {
		return nil
	}
	idx := map[string]int{}
	ids := make([]string, len(list))
	for i, t := range list {
		idx[t.ID], ids[i] = i, t.ID
	}
	rows, err := tx.Query(ctx, `select ta.treatment_id::text, e.id::text, coalesce(s.key || '-' || e.number, ''), e.title, coalesce(a.data_classification, '')
		from privacy_treatment_apps ta
		join elements e on e.tenant_id = ta.tenant_id and e.id = ta.application_id and e.deleted_at is null
		join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
		left join ext_application a on a.tenant_id = e.tenant_id and a.element_id = e.id
		where ta.treatment_id = any($1::uuid[]) order by e.title`, ids)
	if err != nil {
		return err
	}
	for rows.Next() {
		var tid string
		var a AppRef
		if err := rows.Scan(&tid, &a.ID, &a.Key, &a.Name, &a.Classification); err != nil {
			rows.Close()
			return err
		}
		a.Personal, a.Credentials, a.Financial = dataKinds(a.Classification)
		list[idx[tid]].Applications = append(list[idx[tid]].Applications, a)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for i := range list {
		t := &list[i]
		t.Issues = []string{}
		if t.LegalBasis == "" {
			t.Issues = append(t.Issues, "no_basis")
		}
		if strings.TrimSpace(t.Purpose) == "" {
			t.Issues = append(t.Issues, "no_purpose")
		}
		if len(t.Applications) == 0 {
			t.Issues = append(t.Issues, "no_application")
		}
		if t.DPIARequired && t.DPIA == nil {
			t.Issues = append(t.Issues, "dpia_missing")
		}
	}
	return nil
}

// Treatments elenca il registro (privacy.read); archived = comprende anche i trattamenti archiviati.
func (s *Service) Treatments(ctx context.Context, tx pgx.Tx, actor engine.Actor, archived bool) ([]Treatment, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, selectTreatment+` where ($1 or t.status = 'active') order by lower(t.name)`, archived)
	if err != nil {
		return nil, err
	}
	list, err := scanTreatments(rows)
	if err != nil {
		return nil, err
	}
	return list, fill(ctx, tx, list)
}

// Treatment legge un trattamento.
func (s *Service) Treatment(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) (*Treatment, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	return loadTreatment(ctx, tx, id)
}

func loadTreatment(ctx context.Context, tx pgx.Tx, id string) (*Treatment, error) {
	if !isUUID(id) {
		return nil, engine.ErrNotFound
	}
	rows, err := tx.Query(ctx, selectTreatment+` where t.id = $1`, id)
	if err != nil {
		return nil, err
	}
	list, err := scanTreatments(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, engine.ErrNotFound
	}
	if err := fill(ctx, tx, list); err != nil {
		return nil, err
	}
	return &list[0], nil
}

// validate controlla e normalizza l'input; verifica che persona e applicazioni esistano.
func (in *TreatmentInput) validate(ctx context.Context, tx pgx.Tx) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return engine.InvalidCode("validation.privacy.name_required", "name", "serve un nome per il trattamento")
	}
	if utf8.RuneCountInString(in.Name) > 120 {
		return engine.TooLong("name", 120, "nome troppo lungo (massimo 120 caratteri)")
	}
	in.Purpose = strings.TrimSpace(in.Purpose)
	in.LegalBasisNote = strings.TrimSpace(in.LegalBasisNote)
	in.Recipients = strings.TrimSpace(in.Recipients)
	in.TransferDetails = strings.TrimSpace(in.TransferDetails)
	in.Retention = strings.TrimSpace(in.Retention)
	for _, c := range []struct {
		f, v string
		max  int
	}{{"purpose", in.Purpose, 4000}, {"legal_basis_note", in.LegalBasisNote, 2000}, {"recipients", in.Recipients, 2000},
		{"transfer_details", in.TransferDetails, 2000}, {"retention", in.Retention, 1000}} {
		if err := checkLen(c.f, c.v, c.max); err != nil {
			return err
		}
	}
	if in.LegalBasis != "" && legalBasisName(in.LegalBasis) == "" {
		return engine.InvalidValue("legal_basis", "base giuridica non riconosciuta")
	}
	if in.Status == "" {
		in.Status = "active"
	}
	if in.Status != "active" && in.Status != "archived" {
		return engine.InvalidValue("status", "stato non ammesso")
	}
	var err error
	if in.DataSubjects, err = cleanList("data_subjects", in.DataSubjects, 30, 80); err != nil {
		return err
	}
	if in.DataCategories, err = cleanList("data_categories", in.DataCategories, 40, 80); err != nil {
		return err
	}
	if in.OwnerID != "" {
		var ok bool
		if !isUUID(in.OwnerID) {
			return engine.NotFound("owner_id", "member", "persona non trovata")
		}
		if err := tx.QueryRow(ctx, `select exists (select 1 from members where id = $1)`, in.OwnerID).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return engine.NotFound("owner_id", "member", "persona non trovata")
		}
	}
	in.Applications = slices.Compact(slices.Sorted(slices.Values(in.Applications)))
	if len(in.Applications) > 100 {
		return engine.OutOfRange("applications", 0, 100, "al massimo 100 applicazioni")
	}
	return checkElements(ctx, tx, "applications", "application", in.Applications, "application")
}

// checkElements verifica che tutti gli id siano elementi (non cancellati) del tipo indicato.
func checkElements(ctx context.Context, tx pgx.Tx, field, what string, ids []string, typeKey string) error {
	if len(ids) == 0 {
		return nil
	}
	for _, id := range ids {
		if !isUUID(id) {
			return engine.NotFound(field, what, "elemento non trovato")
		}
	}
	var n int
	if err := tx.QueryRow(ctx, `select count(*) from elements e join element_types t on t.tenant_id = e.tenant_id and t.id = e.type_id
		where e.id = any($1::uuid[]) and e.deleted_at is null and t.key = $2`, ids, typeKey).Scan(&n); err != nil {
		return err
	}
	if n != len(ids) {
		return engine.NotFound(field, what, "uno degli elementi indicati non esiste")
	}
	return nil
}

func setApps(ctx context.Context, tx pgx.Tx, id string, apps []string) error {
	if _, err := tx.Exec(ctx, `delete from privacy_treatment_apps where treatment_id = $1`, id); err != nil {
		return err
	}
	if len(apps) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `insert into privacy_treatment_apps (tenant_id, treatment_id, application_id)
		select app_tenant(), $1, a from unnest($2::uuid[]) a`, id, apps)
	return err
}

func isDuplicate(err error) bool {
	var ve interface{ SQLState() string }
	return errors.As(err, &ve) && ve.SQLState() == "23505"
}

// CreateTreatment aggiunge un trattamento al registro (privacy.write).
func (s *Service) CreateTreatment(ctx context.Context, tx pgx.Tx, actor engine.Actor, in TreatmentInput) (*Treatment, error) {
	if err := requireWrite(actor); err != nil {
		return nil, err
	}
	if err := in.validate(ctx, tx); err != nil {
		return nil, err
	}
	id := db.NewID()
	_, err := tx.Exec(ctx, `insert into privacy_treatments (tenant_id, id, name, purpose, legal_basis, legal_basis_note, data_subjects, data_categories,
		special_categories, large_scale, profiling, high_risk, recipients, transfers_extra_eu, transfer_details, retention, owner_id, status, created_by)
		values (app_tenant(), $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`,
		id, in.Name, in.Purpose, nullable(in.LegalBasis), in.LegalBasisNote, in.DataSubjects, in.DataCategories,
		in.SpecialCategories, in.LargeScale, in.Profiling, in.HighRisk, in.Recipients, in.TransfersExtraEU, in.TransferDetails, in.Retention,
		nullable(in.OwnerID), in.Status, nullable(actor.MemberID))
	if isDuplicate(err) {
		return nil, engine.Duplicate("name", "esiste già un trattamento con questo nome")
	}
	if err != nil {
		return nil, err
	}
	if err := setApps(ctx, tx, id, in.Applications); err != nil {
		return nil, err
	}
	if err := auditEv(ctx, tx, actor, "privacy.treatment.create", "privacy_treatment", id, map[string]any{"name": in.Name}); err != nil {
		return nil, err
	}
	return loadTreatment(ctx, tx, id)
}

// UpdateTreatment sostituisce un trattamento (privacy.write).
func (s *Service) UpdateTreatment(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string, in TreatmentInput) (*Treatment, error) {
	if err := requireWrite(actor); err != nil {
		return nil, err
	}
	if _, err := loadTreatment(ctx, tx, id); err != nil {
		return nil, err
	}
	if err := in.validate(ctx, tx); err != nil {
		return nil, err
	}
	_, err := tx.Exec(ctx, `update privacy_treatments set name = $2, purpose = $3, legal_basis = $4, legal_basis_note = $5, data_subjects = $6,
		data_categories = $7, special_categories = $8, large_scale = $9, profiling = $10, high_risk = $11, recipients = $12,
		transfers_extra_eu = $13, transfer_details = $14, retention = $15, owner_id = $16, status = $17, updated_at = now() where id = $1`,
		id, in.Name, in.Purpose, nullable(in.LegalBasis), in.LegalBasisNote, in.DataSubjects, in.DataCategories,
		in.SpecialCategories, in.LargeScale, in.Profiling, in.HighRisk, in.Recipients, in.TransfersExtraEU, in.TransferDetails, in.Retention,
		nullable(in.OwnerID), in.Status)
	if isDuplicate(err) {
		return nil, engine.Duplicate("name", "esiste già un trattamento con questo nome")
	}
	if err != nil {
		return nil, err
	}
	if err := setApps(ctx, tx, id, in.Applications); err != nil {
		return nil, err
	}
	if err := auditEv(ctx, tx, actor, "privacy.treatment.update", "privacy_treatment", id, map[string]any{"name": in.Name}); err != nil {
		return nil, err
	}
	return loadTreatment(ctx, tx, id)
}

// DeleteTreatment elimina un trattamento con la sua DPIA (privacy.write).
func (s *Service) DeleteTreatment(ctx context.Context, tx pgx.Tx, actor engine.Actor, id string) error {
	if err := requireWrite(actor); err != nil {
		return err
	}
	if !isUUID(id) {
		return engine.ErrNotFound
	}
	tag, err := tx.Exec(ctx, `delete from privacy_treatments where id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return engine.ErrNotFound
	}
	return auditEv(ctx, tx, actor, "privacy.treatment.delete", "privacy_treatment", id, nil)
}

var dpiaStatusIt = map[string]string{"draft": "bozza", "in_review": "in revisione", "approved": "approvata"}

// WriteCSV scrive il registro dei trattamenti (art. 30) in CSV con punto e virgola, per il Garante o per l'audit.
func (s *Service) WriteCSV(ctx context.Context, tx pgx.Tx, actor engine.Actor, w io.Writer) error {
	list, err := s.Treatments(ctx, tx, actor, false)
	if err != nil {
		return err
	}
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	_ = cw.Write([]string{"nome", "finalita", "base_giuridica", "note_base_giuridica", "categorie_di_interessati", "categorie_di_dati",
		"dati_particolari", "destinatari", "trasferimenti_extra_ue", "dettaglio_trasferimenti", "conservazione", "applicazioni",
		"responsabile", "dpia_necessaria", "dpia"})
	yes := func(b bool) string {
		if b {
			return "sì"
		}
		return "no"
	}
	for _, t := range list {
		apps := make([]string, len(t.Applications))
		for i, a := range t.Applications {
			apps[i] = a.Name
		}
		dpia := ""
		if t.DPIA != nil {
			dpia = dpiaStatusIt[t.DPIA.Status]
		}
		owner := ""
		if t.Owner != nil {
			owner = *t.Owner
		}
		_ = cw.Write([]string{csvSafe(t.Name), csvSafe(t.Purpose), legalBasisName(t.LegalBasis), csvSafe(t.LegalBasisNote),
			csvSafe(strings.Join(t.DataSubjects, ", ")), csvSafe(strings.Join(t.DataCategories, ", ")), yes(t.SpecialCategories),
			csvSafe(t.Recipients), yes(t.TransfersExtraEU), csvSafe(t.TransferDetails), csvSafe(t.Retention), csvSafe(strings.Join(apps, ", ")),
			csvSafe(owner), yes(t.DPIARequired), dpia})
	}
	cw.Flush()
	return cw.Error()
}

// csvSafe neutralizza le formule dei fogli di calcolo (=, +, -, @ in testa).
func csvSafe(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}
