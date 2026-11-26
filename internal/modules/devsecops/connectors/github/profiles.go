package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"regia/internal/modules/devsecops/connectors/profile"
	"regia/internal/platform/db"
)

// Profili di pipeline: configurazione versionata per tenant (tabelle pipeline_profiles e
// pipeline_profile_versions, migrazione 0025). Ogni salvataggio con una definizione
// diversa è una nuova versione; le integrazioni usano sempre la versione corrente.

// ExportKind identifica il file di export di un profilo.
const ExportKind = "regia.pipeline-profile.export"

var ErrProfileNotFound = &Error{Status: 404, Code: "profile.not_found", Msg: "profilo inesistente"}

// Profile è un profilo con la definizione della versione corrente.
type Profile struct {
	ID          string          `json:"id"`
	Key         string          `json:"key"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Preset      *string         `json:"preset"`
	Version     int             `json:"version"`
	Definition  json.RawMessage `json:"definition"`
	UsedBy      []string        `json:"used_by"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

// ProfileVersion è una versione nello storico.
type ProfileVersion struct {
	Version   int             `json:"version"`
	Note      string          `json:"note"`
	CreatedAt time.Time       `json:"created_at"`
	CreatedBy *string         `json:"created_by"`
	Def       json.RawMessage `json:"definition,omitempty"`
}

// ProfileInput crea o modifica un profilo. Con Preset (solo alla creazione) la definizione
// parte dal preset.
type ProfileInput struct {
	Key         string          `json:"key"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Preset      string          `json:"preset"`
	Definition  json.RawMessage `json:"definition"`
	Note        string          `json:"note"`
}

// ProfileExport è il file JSON di export (e di import).
type ProfileExport struct {
	Kind        string             `json:"kind"`
	Key         string             `json:"key"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Version     int                `json:"version"`
	ExportedAt  time.Time          `json:"exported_at"`
	Definition  profile.Definition `json:"definition"`
}

var keyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ValidateDefinition legge e valida una definizione.
func ValidateDefinition(raw json.RawMessage) (*profile.Definition, []profile.Problem) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, []profile.Problem{{Path: "", Message: "manca la definizione"}}
	}
	if len(raw) > 256<<10 {
		return nil, []profile.Problem{{Path: "", Message: "definizione troppo grande (al massimo 256 KB)"}}
	}
	def, err := profile.Parse(raw)
	if err != nil {
		return nil, []profile.Problem{{Path: "", Message: err.Error()}}
	}
	if ps := def.Validate(); len(ps) > 0 {
		return def, ps
	}
	return def, nil
}

func profileInvalid(ps []profile.Problem) error {
	msg := "profilo non valido"
	if len(ps) > 0 {
		msg += ": " + ps[0].String()
		if len(ps) > 1 {
			msg += " (e altri " + itoa(len(ps)-1) + ")"
		}
	}
	return &Error{Status: 422, Code: "profile.invalid", Field: "definition", Msg: msg, Problems: ps}
}

func itoa(n int) string { return strconv.Itoa(n) }

func slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		r = foldAccent(r)
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 60 {
		out = strings.Trim(out[:60], "-")
	}
	if out == "" {
		out = "profilo"
	}
	return out
}

const selectProfile = `
	select p.id, p.key, p.name, p.description, p.preset, p.current_version, v.definition, p.created_at, p.updated_at,
	  coalesce((select array_agg(i.name order by i.name) from integrations i where i.kind = 'github' and i.config->>'profile_id' = p.id::text), '{}')
	from pipeline_profiles p
	join pipeline_profile_versions v on v.tenant_id = p.tenant_id and v.profile_id = p.id and v.version = p.current_version
	where true`

func scanProfiles(rows pgx.Rows) ([]Profile, error) {
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (Profile, error) {
		var p Profile
		var def []byte
		err := r.Scan(&p.ID, &p.Key, &p.Name, &p.Description, &p.Preset, &p.Version, &def, &p.CreatedAt, &p.UpdatedAt, &p.UsedBy)
		p.Definition = def
		return p, err
	})
}

// Profiles elenca i profili del tenant.
func (s *Service) Profiles(ctx context.Context, tenantID string) ([]Profile, error) {
	out := []Profile{}
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, selectProfile+" order by lower(p.name)")
		if err != nil {
			return err
		}
		out, err = scanProfiles(rows)
		return err
	})
	if out == nil {
		out = []Profile{}
	}
	return out, err
}

func (s *Service) profileByID(ctx context.Context, tx pgx.Tx, id string) (*Profile, error) {
	if !validID(id) {
		return nil, ErrProfileNotFound
	}
	rows, err := tx.Query(ctx, selectProfile+" and p.id = $1", id)
	if err != nil {
		return nil, err
	}
	list, err := scanProfiles(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrProfileNotFound
	}
	return &list[0], nil
}

// GetProfile restituisce un profilo con lo storico delle versioni.
func (s *Service) GetProfile(ctx context.Context, tenantID, id string) (*Profile, []ProfileVersion, error) {
	var p *Profile
	var versions []ProfileVersion
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		if p, err = s.profileByID(ctx, tx, id); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `select v.version, v.note, v.created_at, m.display_name from pipeline_profile_versions v
			left join members m on m.tenant_id = v.tenant_id and m.id = v.created_by
			where v.profile_id = $1 order by v.version desc limit 100`, id)
		if err != nil {
			return err
		}
		versions, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (ProfileVersion, error) {
			var v ProfileVersion
			err := r.Scan(&v.Version, &v.Note, &v.CreatedAt, &v.CreatedBy)
			return v, err
		})
		return err
	})
	return p, versions, err
}

// GetProfileVersion restituisce la definizione di una versione.
func (s *Service) GetProfileVersion(ctx context.Context, tenantID, id string, version int) (*ProfileVersion, error) {
	if !validID(id) {
		return nil, ErrProfileNotFound
	}
	var v ProfileVersion
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var def []byte
		err := tx.QueryRow(ctx, `select v.version, v.note, v.created_at, m.display_name, v.definition from pipeline_profile_versions v
			left join members m on m.tenant_id = v.tenant_id and m.id = v.created_by
			where v.profile_id = $1 and v.version = $2`, id, version).Scan(&v.Version, &v.Note, &v.CreatedAt, &v.CreatedBy, &def)
		if errors.Is(err, pgx.ErrNoRows) {
			return &Error{Status: 404, Code: "profile.version_not_found", Msg: "versione inesistente"}
		}
		v.Def = def
		return err
	})
	return &v, err
}

func cleanProfileMeta(in *ProfileInput) error {
	in.Name = strings.Join(strings.Fields(in.Name), " ")
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 120 {
		return invalid("name", "serve un nome (al massimo 120 caratteri)")
	}
	in.Description = strings.TrimSpace(in.Description)
	if utf8.RuneCountInString(in.Description) > 2000 {
		return invalid("description", "descrizione troppo lunga")
	}
	in.Note = strings.TrimSpace(in.Note)
	if utf8.RuneCountInString(in.Note) > 500 {
		return invalid("note", "nota troppo lunga")
	}
	return nil
}

// CreateProfile crea un profilo (versione 1), da un preset o da una definizione.
func (s *Service) CreateProfile(ctx context.Context, a Actor, in ProfileInput) (string, error) {
	var preset *string
	if in.Preset != "" {
		p := profile.PresetByKey(in.Preset)
		if p == nil {
			return "", invalid("preset", "preset inesistente")
		}
		if len(bytes.TrimSpace(in.Definition)) == 0 {
			in.Definition = p.Definition.Canonical()
		}
		if in.Name == "" {
			in.Name = p.Name
		}
		if in.Description == "" {
			in.Description = p.Description
		}
		preset = &p.Key
	}
	if err := cleanProfileMeta(&in); err != nil {
		return "", err
	}
	def, ps := ValidateDefinition(in.Definition)
	if len(ps) > 0 {
		return "", profileInvalid(ps)
	}
	if in.Key == "" {
		in.Key = slugify(in.Name)
	}
	if !keyRe.MatchString(in.Key) {
		return "", invalid("key", "chiave non valida (minuscole, cifre e trattini)")
	}
	id := db.NewID()
	err := s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		key, err := uniqueKey(ctx, tx, in.Key)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into pipeline_profiles (tenant_id, id, key, name, description, preset, current_version, created_by)
			values (app_tenant(), $1, $2, $3, $4, $5, 1, $6)`, id, key, in.Name, in.Description, preset, nullable(a.MemberID)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into pipeline_profile_versions (tenant_id, profile_id, version, definition, note, created_by)
			values (app_tenant(), $1, 1, $2, $3, $4)`, id, def.Canonical(), in.Note, nullable(a.MemberID)); err != nil {
			return err
		}
		return audited(ctx, tx, a, "devsecops.profile.create", "pipeline_profile", id, map[string]any{"key": key, "preset": in.Preset})
	})
	return id, err
}

func uniqueKey(ctx context.Context, tx pgx.Tx, key string) (string, error) {
	cand := key
	for i := 2; i < 100; i++ {
		var exists bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from pipeline_profiles where key = $1)", cand).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return cand, nil
		}
		suffix := "-" + itoa(i)
		base := key
		if len(base)+len(suffix) > 63 {
			base = base[:63-len(suffix)]
		}
		cand = base + suffix
	}
	return "", invalid("key", "chiave già usata")
}

// UpdateProfile modifica nome e descrizione e, se la definizione cambia, crea una nuova
// versione. Restituisce la versione corrente.
func (s *Service) UpdateProfile(ctx context.Context, a Actor, id string, in ProfileInput) (int, error) {
	if err := cleanProfileMeta(&in); err != nil {
		return 0, err
	}
	def, ps := ValidateDefinition(in.Definition)
	if len(ps) > 0 {
		return 0, profileInvalid(ps)
	}
	version := 0
	err := s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		cur, err := s.profileByID(ctx, tx, id)
		if err != nil {
			return err
		}
		version, err = s.saveVersion(ctx, tx, a, cur, def, in.Note)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "update pipeline_profiles set name = $2, description = $3, updated_at = now() where id = $1", id, in.Name, in.Description); err != nil {
			return err
		}
		return audited(ctx, tx, a, "devsecops.profile.update", "pipeline_profile", id, map[string]any{"version": version, "new_version": version != cur.Version})
	})
	return version, err
}

// saveVersion aggiunge una versione se la definizione è cambiata.
func (s *Service) saveVersion(ctx context.Context, tx pgx.Tx, a Actor, cur *Profile, def *profile.Definition, note string) (int, error) {
	return saveVersionTx(ctx, tx, a, cur, def, note)
}

// saveVersionTx salva una nuova versione del profilo se la definizione è cambiata.
func saveVersionTx(ctx context.Context, tx pgx.Tx, a Actor, cur *Profile, def *profile.Definition, note string) (int, error) {
	var same bool
	if err := tx.QueryRow(ctx, "select definition = $2::jsonb from pipeline_profile_versions where profile_id = $1 and version = $3",
		cur.ID, def.Canonical(), cur.Version).Scan(&same); err != nil {
		return 0, err
	}
	if same {
		return cur.Version, nil
	}
	next := cur.Version + 1
	if _, err := tx.Exec(ctx, `insert into pipeline_profile_versions (tenant_id, profile_id, version, definition, note, created_by)
		values (app_tenant(), $1, $2, $3, $4, $5)`, cur.ID, next, def.Canonical(), note, nullable(a.MemberID)); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, "update pipeline_profiles set current_version = $2, updated_at = now() where id = $1", cur.ID, next); err != nil {
		return 0, err
	}
	return next, nil
}

// DeleteProfile cancella un profilo non usato da nessuna integrazione.
func (s *Service) DeleteProfile(ctx context.Context, a Actor, id string) error {
	return s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		cur, err := s.profileByID(ctx, tx, id)
		if err != nil {
			return err
		}
		if len(cur.UsedBy) > 0 {
			return &Error{Status: 409, Code: "profile.in_use", Msg: "il profilo è usato da: " + strings.Join(cur.UsedBy, ", ")}
		}
		if _, err := tx.Exec(ctx, "delete from pipeline_profiles where id = $1", id); err != nil {
			return err
		}
		return audited(ctx, tx, a, "devsecops.profile.delete", "pipeline_profile", id, map[string]any{"key": cur.Key})
	})
}

// ExportProfile restituisce il file di export della versione corrente.
func (s *Service) ExportProfile(ctx context.Context, tenantID, id string) (*ProfileExport, error) {
	var out *ProfileExport
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		p, err := s.profileByID(ctx, tx, id)
		if err != nil {
			return err
		}
		def, err := profile.Parse(p.Definition)
		if err != nil {
			return err
		}
		out = &ProfileExport{Kind: ExportKind, Key: p.Key, Name: p.Name, Description: p.Description, Version: p.Version,
			ExportedAt: s.o.Now().UTC().Truncate(time.Second), Definition: *def}
		return nil
	})
	return out, err
}

// ImportResult dice cosa ha fatto un import.
type ImportResult struct {
	ID      string `json:"id"`
	Key     string `json:"key"`
	Version int    `json:"version"`
	Created bool   `json:"created"`
}

// ImportProfile importa un file di export: un profilo con la stessa chiave riceve una
// nuova versione (se la definizione è diversa), altrimenti se ne crea uno nuovo.
func (s *Service) ImportProfile(ctx context.Context, a Actor, raw json.RawMessage) (*ImportResult, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var ex struct {
		Kind        string          `json:"kind"`
		Key         string          `json:"key"`
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Version     int             `json:"version"`
		ExportedAt  *time.Time      `json:"exported_at"`
		Definition  json.RawMessage `json:"definition"`
	}
	if err := dec.Decode(&ex); err != nil {
		return nil, invalid("document", "file non valido: serve l'export JSON di un profilo di Regia")
	}
	if ex.Kind != ExportKind {
		return nil, invalid("document", "file non valido: il campo kind deve valere "+ExportKind)
	}
	if ex.Key == "" || !keyRe.MatchString(ex.Key) {
		return nil, invalid("key", "chiave del profilo mancante o non valida")
	}
	in := ProfileInput{Key: ex.Key, Name: ex.Name, Description: ex.Description, Definition: ex.Definition, Note: "importato"}
	if err := cleanProfileMeta(&in); err != nil {
		return nil, err
	}
	def, ps := ValidateDefinition(in.Definition)
	if len(ps) > 0 {
		return nil, profileInvalid(ps)
	}
	res := &ImportResult{Key: ex.Key}
	err := s.o.DB.InTenant(ctx, a.TenantID, db.TenantOpts{}, func(tx pgx.Tx) error {
		var id string
		err := tx.QueryRow(ctx, "select id from pipeline_profiles where key = $1", ex.Key).Scan(&id)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			res.ID, res.Version, res.Created = db.NewID(), 1, true
			if _, err := tx.Exec(ctx, `insert into pipeline_profiles (tenant_id, id, key, name, description, current_version, created_by)
				values (app_tenant(), $1, $2, $3, $4, 1, $5)`, res.ID, ex.Key, in.Name, in.Description, nullable(a.MemberID)); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `insert into pipeline_profile_versions (tenant_id, profile_id, version, definition, note, created_by)
				values (app_tenant(), $1, 1, $2, $3, $4)`, res.ID, def.Canonical(), in.Note, nullable(a.MemberID)); err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			cur, err := s.profileByID(ctx, tx, id)
			if err != nil {
				return err
			}
			res.ID = id
			if res.Version, err = s.saveVersion(ctx, tx, a, cur, def, in.Note); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "update pipeline_profiles set name = $2, description = $3, updated_at = now() where id = $1", id, in.Name, in.Description); err != nil {
				return err
			}
		}
		return audited(ctx, tx, a, "devsecops.profile.import", "pipeline_profile", res.ID, map[string]any{"key": ex.Key, "version": res.Version, "created": res.Created})
	})
	return res, err
}

// definitionFor legge la definizione corrente del profilo di un'integrazione (nil se
// l'integrazione non ha profilo).
func definitionFor(ctx context.Context, tx pgx.Tx, profileID string) (*profile.Definition, int, error) {
	if profileID == "" {
		return nil, 0, nil
	}
	var raw []byte
	var version int
	err := tx.QueryRow(ctx, `select v.definition, v.version from pipeline_profiles p
		join pipeline_profile_versions v on v.tenant_id = p.tenant_id and v.profile_id = p.id and v.version = p.current_version
		where p.id = $1`, profileID).Scan(&raw, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, 0, nil // profilo cancellato: si ripiega sui dati base
	}
	if err != nil {
		return nil, 0, err
	}
	def, err := profile.Parse(raw)
	if err != nil {
		return nil, 0, err
	}
	return def, version, nil
}
