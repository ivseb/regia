package engine

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/db"
)

// Export e import della configurazione dei campi in JSON, sul modello dei pacchetti dei
// processi: i riferimenti sono per chiave e per nome (tipi, opzioni, gruppi), mai per id,
// così il file passa da un tenant all'altro (collaudo → produzione).

// FieldsBundle è il file di export e import.
type FieldsBundle struct {
	Format     string        `json:"format"`
	ExportedAt string        `json:"exported_at,omitempty"`
	Fields     []BundleField `json:"fields"`
}

// BundleField è un campo nel file, con opzioni e associazioni.
type BundleField struct {
	Key      string        `json:"key"`
	Name     string        `json:"name"`
	DataType string        `json:"data_type"`
	Multi    bool          `json:"multi,omitempty"`
	Help     string        `json:"help,omitempty"`
	Default  any           `json:"default,omitempty"` // opzioni per nome, gruppi per nome, elementi per chiave
	Settings FieldSettings `json:"settings"`
	Aliases  []string      `json:"aliases,omitempty"`
	Archived bool          `json:"archived,omitempty"`
	// OptionSet: nome dell'insieme di valori; campi con lo stesso nome lo condividono.
	OptionSet string         `json:"option_set,omitempty"`
	Options   []BundleOption `json:"options,omitempty"`
	Types     []FieldAssoc   `json:"types,omitempty"`
}

// BundleOption è un'opzione nel file.
type BundleOption struct {
	Name     string            `json:"name"`
	Color    string            `json:"color,omitempty"`
	Archived bool              `json:"archived,omitempty"`
	Labels   map[string]string `json:"labels,omitempty"`
}

// ImportReport è l'esito di un import (anche di prova).
type ImportReport struct {
	Created      []string `json:"created"`
	Updated      []string `json:"updated"`
	Associations int      `json:"associations"`
	Warnings     []string `json:"warnings"`
}

// ExportFields restituisce la configurazione dei campi del tenant.
func (s *Service) ExportFields(ctx context.Context, tx pgx.Tx, actor Actor) (*FieldsBundle, error) {
	list, err := s.ListFields(ctx, tx, actor)
	if err != nil {
		return nil, err
	}
	b := &FieldsBundle{Format: FieldsBundleFormat, ExportedAt: time.Now().UTC().Format(time.RFC3339), Fields: []BundleField{}}
	for _, c := range list {
		bf := BundleField{Key: c.Key, Name: c.Name, DataType: c.DataType, Multi: c.Multi, Help: c.Help, Settings: c.Settings,
			Aliases: c.Aliases, Archived: c.Archived, OptionSet: c.OptionSet}
		for _, o := range c.Options {
			bf.Options = append(bf.Options, BundleOption{Name: o.Name, Color: o.Color, Archived: o.Archived, Labels: o.Labels})
		}
		for _, a := range c.Types {
			a.TypeName, a.Problem = "", ""
			bf.Types = append(bf.Types, a)
		}
		// Il default in forma portabile: le opzioni per nome; persone, gruppi ed elementi
		// non si esportano (gli id non valgono in un altro tenant).
		switch {
		case c.Default == nil:
		case choiceType(c.DataType):
			names := []any{}
			for _, id := range stringsOf(c.Default) {
				for _, o := range c.Options {
					if o.ID == id {
						names = append(names, o.Name)
					}
				}
			}
			if c.Multi {
				bf.Default = names
			} else if len(names) > 0 {
				bf.Default = names[0]
			}
		case c.DataType == "member" || c.DataType == "group" || c.DataType == "element":
		default:
			bf.Default = c.Default
		}
		b.Fields = append(b.Fields, bf)
	}
	return b, nil
}

// ParseFieldsBundle legge il file JSON, rifiutando i campi sconosciuti.
func ParseFieldsBundle(raw []byte) (*FieldsBundle, error) {
	var head struct {
		Format string `json:"format"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return nil, InvalidValue("bundle", "JSON non valido: %v", err)
	}
	if head.Format != FieldsBundleFormat {
		return nil, InvalidValue("format", "formato %q non riconosciuto (atteso %s)", head.Format, FieldsBundleFormat)
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	b := &FieldsBundle{}
	if err := dec.Decode(b); err != nil {
		return nil, InvalidValue("bundle", "JSON non valido: %v", err)
	}
	if len(b.Fields) > 2000 {
		return nil, invalid("fields", "al massimo 2000 campi per file")
	}
	return b, nil
}

// ImportFields porta la configurazione nel tenant: crea i campi mancanti, aggiorna quelli
// esistenti (stessa chiave e stesso tipo di dato), aggiunge le opzioni mancanti per nome e
// applica le associazioni ai tipi esistenti. Non elimina niente.
func (s *Service) ImportFields(ctx context.Context, tx pgx.Tx, actor Actor, b *FieldsBundle) (*ImportReport, error) {
	if err := requireFieldsAdmin(actor); err != nil {
		return nil, err
	}
	rep := &ImportReport{Created: []string{}, Updated: []string{}, Warnings: []string{}}
	sets := map[string]string{} // nome dell'insieme → id, per condividerlo tra i campi del file
	for _, bf := range b.Fields {
		path := func(msg string) string { return "campo " + bf.Key + ": " + msg }
		var defRaw json.RawMessage
		if bf.Default != nil {
			defRaw, _ = json.Marshal(bf.Default)
		}
		settings := bf.Settings
		aliases := bf.Aliases
		if aliases == nil {
			aliases = []string{}
		}
		existing, err := loadFieldRow(ctx, tx, bf.Key, false)
		switch {
		case errors.Is(err, ErrNotFound):
			name, help, multi := bf.Name, bf.Help, bf.Multi
			in := FieldInput{Key: &bf.Key, Name: &name, DataType: bf.DataType, Multi: &multi, Help: &help,
				Settings: &settings, Aliases: &aliases}
			if choiceType(bf.DataType) {
				if id, ok := sets[strings.ToLower(bf.OptionSet)]; ok && bf.OptionSet != "" {
					in.OptionSetID = &id
				} else {
					for _, o := range bf.Options {
						in.Options = append(in.Options, OptionInput{Name: o.Name, Color: o.Color, Archived: o.Archived, Labels: o.Labels})
					}
				}
			}
			c, err := s.CreateField(ctx, tx, actor, in)
			if err != nil {
				return nil, prefixed(err, path)
			}
			if c.OptionSetID != nil && bf.OptionSet != "" {
				sets[strings.ToLower(bf.OptionSet)] = *c.OptionSetID
			}
			if len(defRaw) > 0 || bf.Archived {
				up := FieldInput{Default: defRaw}
				if bf.Archived {
					up.Archived = &bf.Archived
				}
				if _, err := s.UpdateField(ctx, tx, actor, c.ID, up); err != nil {
					return nil, prefixed(err, path)
				}
			}
			rep.Created = append(rep.Created, bf.Key)
		case err != nil:
			return nil, err
		default:
			if existing.dataType != bf.DataType {
				return nil, invalid("fields", "%s", path("nel tenant è di tipo "+existing.dataType+", nel file "+bf.DataType+": non si converte"))
			}
			name, help := bf.Name, bf.Help
			up := FieldInput{Name: &name, Help: &help, Settings: &settings, Aliases: &aliases, Default: defRaw, Archived: &bf.Archived}
			if bf.Multi != existing.multi {
				up.Multi = &bf.Multi
			}
			if _, err := s.UpdateField(ctx, tx, actor, existing.id, up); err != nil {
				return nil, prefixed(err, path)
			}
			if existing.optionSetID != nil {
				if err := mergeOptions(ctx, tx, *existing.optionSetID, bf.Options); err != nil {
					return nil, prefixed(err, path)
				}
				if bf.OptionSet != "" {
					sets[strings.ToLower(bf.OptionSet)] = *existing.optionSetID
				}
			}
			rep.Updated = append(rep.Updated, bf.Key)
		}
		for _, a := range bf.Types {
			var exists bool
			if err := tx.QueryRow(ctx, `select exists (select 1 from element_types where key = $1)`, a.Type).Scan(&exists); err != nil {
				return nil, err
			}
			if !exists {
				rep.Warnings = append(rep.Warnings, path("tipo «"+a.Type+"» inesistente in questo tenant: associazione saltata"))
				continue
			}
			ord := a.Ordinal
			if bf.Archived {
				rep.Warnings = append(rep.Warnings, path("archiviato: associazione a «"+a.Type+"» saltata"))
				continue
			}
			if _, err := s.AssociateField(ctx, tx, actor, bf.Key, a.Type, AssocInput{Required: a.Required, RequiredFrom: a.RequiredFrom,
				VisibleWhen: a.VisibleWhen, Section: a.Section, Ordinal: &ord}); err != nil {
				return nil, prefixed(err, path)
			}
			rep.Associations++
		}
	}
	if err := auditField(ctx, tx, actor, "fields.import", "", map[string]any{"created": rep.Created, "updated": rep.Updated,
		"associations": rep.Associations}); err != nil {
		return nil, err
	}
	return rep, nil
}

// mergeOptions aggiunge le opzioni mancanti (per nome) e aggiorna colore, ordine e
// archiviazione di quelle esistenti; le opzioni del tenant assenti dal file restano.
func mergeOptions(ctx context.Context, tx pgx.Tx, setID string, in []BundleOption) error {
	for i, o := range in {
		name := strings.Join(strings.Fields(o.Name), " ")
		if name == "" {
			return invalid("options", "opzione senza nome")
		}
		if !validColor(o.Color) {
			return InvalidValue("options", "colore %q non ammesso", o.Color)
		}
		lb, err := CleanLabels("options", o.Labels, 100)
		if err != nil {
			return err
		}
		// Le etichette del file si aggiungono a quelle già presenti (quelle del tenant non si perdono).
		tag, err := tx.Exec(ctx, `update options set color = nullif($3, ''), ordinal = $4, archived = $5, labels = labels || $6::jsonb
			where option_set_id = $1 and lower(name) = lower($2)`, setID, name, o.Color, i, o.Archived, string(labelsJSON(lb)))
		if err != nil {
			return err
		}
		if tag.RowsAffected() > 0 {
			continue
		}
		if _, err := tx.Exec(ctx, `insert into options (tenant_id, id, option_set_id, name, color, ordinal, archived, labels)
			values (app_tenant(), $1, $2, $3, nullif($4, ''), $5, $6, $7)`, db.NewID(), setID, name, o.Color, i, o.Archived, string(labelsJSON(lb))); err != nil {
			return err
		}
	}
	return nil
}

// prefixed aggiunge il contesto (il campo del file) a un errore di validazione.
func prefixed(err error, path func(string) string) error {
	var ve *ValidationError
	if errors.As(err, &ve) {
		return &ValidationError{Field: "fields", Msg: path(ve.Error())}
	}
	return err
}
