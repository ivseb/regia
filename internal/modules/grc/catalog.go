package grc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/db"
)

// Cataloghi normativi del tenant. Un catalogo (framework) ha versioni: al massimo una bozza
// modificabile e una pubblicata; le pubblicate non cambiano più (trigger della migrazione
// 0028) e alla pubblicazione di una nuova versione le mappature dei controlli, le decisioni
// di applicabilità e le corrispondenze passano ai requisiti con lo stesso riferimento.
// L'abilitazione per tenant è la riga di tenant_frameworks della versione corrente.

// CatalogFile è il formato JSON di scambio di un catalogo (regia.grc.catalog/v1).
type CatalogFile struct {
	Format    string        `json:"format"`
	Framework FrameworkMeta `json:"framework"`
	Version   VersionMeta   `json:"version"`
	Items     []CatalogItem `json:"items"`
}

// FrameworkMeta descrive la normativa.
type FrameworkMeta struct {
	Key          string `json:"key"`
	Name         string `json:"name"`
	Kind         string `json:"kind"` // regulation | directive | standard | guideline | internal
	Jurisdiction string `json:"jurisdiction,omitempty"`
	Publisher    string `json:"publisher,omitempty"`
	Description  string `json:"description,omitempty"`
}

// VersionMeta descrive la versione del catalogo.
type VersionMeta struct {
	Version       string `json:"version"`
	EffectiveFrom string `json:"effective_from,omitempty"` // AAAA-MM-GG
	SourceURL     string `json:"source_url,omitempty"`
	LicenseNote   string `json:"license_note,omitempty"`
	Notes         string `json:"notes,omitempty"`
}

// CatalogItem è una voce: capo, articolo, paragrafo, requisito, controllo o allegato.
// Summary è una sintesi scritta dal tenant o da Regia, mai il testo protetto di una norma.
type CatalogItem struct {
	Ref      string         `json:"ref"`
	Parent   string         `json:"parent,omitempty"`
	Kind     string         `json:"kind"`
	Title    string         `json:"title"`
	Summary  string         `json:"summary,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

var (
	keyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,62}$`)
	refRe = regexp.MustCompile(`^[^\s:][^:]{0,63}$`)
)

var frameworkKinds = []string{"regulation", "directive", "standard", "guideline", "internal"}
var itemKinds = []string{"chapter", "article", "paragraph", "requirement", "control", "annex"}

// KindNames sono i nomi italiani dei tipi di voce (CSV e interfaccia).
var KindNames = map[string]string{"chapter": "capo", "article": "articolo", "paragraph": "paragrafo",
	"requirement": "requisito", "control": "controllo", "annex": "allegato"}

// groupingKind: i capi e gli allegati raggruppano, non si valutano.
func groupingKind(k string) bool { return k == "chapter" || k == "annex" }

// DecodeCatalog legge un catalogo JSON rifiutando i campi sconosciuti.
func DecodeCatalog(raw []byte) (*CatalogFile, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	c := &CatalogFile{}
	if err := dec.Decode(c); err != nil {
		return nil, engine.InvalidValue("catalog", "JSON non valido: %v", err)
	}
	if c.Format != CatalogFormat {
		return nil, engine.InvalidValue("format", "formato %q non riconosciuto (atteso %s)", c.Format, CatalogFormat)
	}
	return c, nil
}

// Validate controlla il catalogo senza il database. Le voci figlie seguono il padre.
func (c *CatalogFile) Validate() error {
	f := &c.Framework
	f.Key = strings.TrimSpace(f.Key)
	f.Name = strings.TrimSpace(f.Name)
	if !keyRe.MatchString(f.Key) {
		return engine.InvalidValue("framework.key", "chiave non valida (minuscole, cifre e _, es. dora)")
	}
	if f.Name == "" || len(f.Name) > 200 {
		return engine.TooLong("framework.name", 200, "nome obbligatorio (massimo 200 caratteri)")
	}
	if f.Kind == "" {
		f.Kind = "regulation"
	}
	if !slices.Contains(frameworkKinds, f.Kind) {
		return engine.InvalidValue("framework.kind", "tipo non ammesso (%s)", strings.Join(frameworkKinds, ", "))
	}
	if len(f.Description) > 2000 || len(f.Publisher) > 200 || len(f.Jurisdiction) > 20 {
		return engine.InvalidCode("validation.grc.catalog_text_too_long", "framework", "descrizione, editore o giurisdizione troppo lunghi")
	}
	v := &c.Version
	v.Version = strings.TrimSpace(v.Version)
	if v.Version == "" || len(v.Version) > 40 {
		return engine.TooLong("version.version", 40, "versione obbligatoria (massimo 40 caratteri)")
	}
	if v.EffectiveFrom != "" {
		if _, err := time.Parse(time.DateOnly, v.EffectiveFrom); err != nil {
			return engine.InvalidCode("validation.date_invalid", "version.effective_from", "data non valida (AAAA-MM-GG)")
		}
	}
	if len(v.SourceURL) > 500 || len(v.LicenseNote) > 2000 || len(v.Notes) > 2000 {
		return engine.InvalidCode("validation.grc.version_text_too_long", "version", "fonte, nota di licenza o note troppo lunghe")
	}
	if len(c.Items) > 5000 {
		return engine.InvalidCode("validation.grc.catalog_items_too_many", "items", "al massimo 5000 voci per catalogo")
	}
	seen := map[string]string{}
	for i := range c.Items {
		it := &c.Items[i]
		p := fmt.Sprintf("items[%d]", i)
		it.Ref, it.Parent, it.Title = strings.TrimSpace(it.Ref), strings.TrimSpace(it.Parent), strings.TrimSpace(it.Title)
		if !refRe.MatchString(it.Ref) {
			return engine.InvalidParams("validation.grc.item_ref_invalid", p+".ref", map[string]any{"ref": it.Ref}, "riferimento %q non valido (1-64 caratteri, senza «:»)", it.Ref)
		}
		if _, dup := seen[it.Ref]; dup {
			return engine.InvalidParams("validation.grc.item_ref_repeated", p+".ref", map[string]any{"ref": it.Ref}, "riferimento %q ripetuto", it.Ref)
		}
		if it.Kind == "" {
			it.Kind = "requirement"
		}
		if !slices.Contains(itemKinds, it.Kind) {
			return engine.InvalidParams("validation.grc.item_kind_invalid", p+".kind", map[string]any{"kinds": strings.Join(itemKinds, ", ")}, "tipo di voce non ammesso (%s)", strings.Join(itemKinds, ", "))
		}
		if it.Title == "" || len(it.Title) > 300 {
			return engine.InvalidParams("validation.grc.item_title_required", p+".title", map[string]any{"ref": it.Ref}, "titolo obbligatorio per %s (massimo 300 caratteri)", it.Ref)
		}
		if len(it.Summary) > 2000 {
			return engine.InvalidParams("validation.grc.item_summary_too_long", p+".summary", map[string]any{"ref": it.Ref}, "sintesi di %s troppo lunga (massimo 2000 caratteri)", it.Ref)
		}
		if it.Parent != "" {
			if _, ok := seen[it.Parent]; !ok {
				return engine.InvalidParams("validation.grc.item_parent_order", p+".parent", map[string]any{"parent": it.Parent, "ref": it.Ref}, "il padre %q di %s deve comparire prima", it.Parent, it.Ref)
			}
		}
		seen[it.Ref] = it.Kind
	}
	return nil
}

// Assessable dice quali voci si valutano: le foglie che non sono capi o allegati.
func (c *CatalogFile) Assessable() map[string]bool {
	parents := map[string]bool{}
	for _, it := range c.Items {
		if it.Parent != "" {
			parents[it.Parent] = true
		}
	}
	out := map[string]bool{}
	for _, it := range c.Items {
		if !groupingKind(it.Kind) && !parents[it.Ref] {
			out[it.Ref] = true
		}
	}
	return out
}

// ---------------------------------------------------------------------
// Lettura

// VersionInfo è una versione di un catalogo.
type VersionInfo struct {
	ID            string     `json:"id"`
	Version       string     `json:"version"`
	Status        string     `json:"status"`
	EffectiveFrom *time.Time `json:"effective_from"`
	SourceURL     string     `json:"source_url"`
	LicenseNote   string     `json:"license_note"`
	Notes         string     `json:"notes"`
	PublishedAt   *time.Time `json:"published_at"`
	Items         int        `json:"items"`
	Assessable    int        `json:"assessable"`
}

// FrameworkInfo è un catalogo con le sue versioni e l'abilitazione nel tenant.
type FrameworkInfo struct {
	ID           string        `json:"id"`
	Key          string        `json:"key"`
	Name         string        `json:"name"`
	Kind         string        `json:"kind"`
	Jurisdiction string        `json:"jurisdiction"`
	Publisher    string        `json:"publisher"`
	Description  string        `json:"description"`
	System       bool          `json:"system"`
	Starter      *string       `json:"starter"`
	Status       string        `json:"status"` // active | paused | retired | "" (non abilitata)
	Current      *VersionInfo  `json:"current"`
	Draft        *VersionInfo  `json:"draft"`
	Versions     []VersionInfo `json:"versions"`
}

func (s *Service) loadFrameworks(ctx context.Context, tx pgx.Tx, key string) ([]*FrameworkInfo, error) {
	where, args := "", []any{}
	if key != "" {
		where, args = "where f.key = $1", []any{key}
	}
	var out []*FrameworkInfo
	seen := map[string]bool{}
	rows, err := tx.Query(ctx, `select f.id, f.key, f.name, f.kind, coalesce(f.jurisdiction, ''), coalesce(f.publisher, ''), f.description,
		f.owner_tenant_id is null, f.starter
		from frameworks f `+where+` order by f.owner_tenant_id is null, f.name`, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		f := &FrameworkInfo{Versions: []VersionInfo{}}
		if err := rows.Scan(&f.ID, &f.Key, &f.Name, &f.Kind, &f.Jurisdiction, &f.Publisher, &f.Description, &f.System, &f.Starter); err != nil {
			rows.Close()
			return nil, err
		}
		// Un catalogo del tenant prevale su quello di sistema con la stessa chiave.
		if seen[f.Key] {
			continue
		}
		seen[f.Key] = true
		out = append(out, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, f := range out {
		vr, err := tx.Query(ctx, `select v.id, v.version, v.status, v.effective_from, coalesce(v.source_url, ''), coalesce(v.license_note, ''),
			v.notes, v.published_at,
			(select count(*) from requirements r where r.version_id = v.id),
			(select count(*) from requirements r where r.version_id = v.id and r.kind not in ('chapter','annex')
			   and not exists (select 1 from requirements c where c.parent_id = r.id))
			from framework_versions v where v.framework_id = $1 order by v.created_at desc`, f.ID)
		if err != nil {
			return nil, err
		}
		vs, err := pgx.CollectRows(vr, pgx.RowToStructByPos[VersionInfo])
		if err != nil {
			return nil, err
		}
		f.Versions = vs
		for i := range vs {
			switch vs[i].Status {
			case "published":
				f.Current = &vs[i]
			case "draft":
				f.Draft = &vs[i]
			}
		}
		if f.Current != nil {
			err := tx.QueryRow(ctx, "select status from tenant_frameworks where framework_version_id = $1", f.Current.ID).Scan(&f.Status)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
		}
	}
	return out, nil
}

func (s *Service) framework(ctx context.Context, tx pgx.Tx, key string) (*FrameworkInfo, error) {
	fs, err := s.loadFrameworks(ctx, tx, key)
	if err != nil {
		return nil, err
	}
	if len(fs) == 0 {
		return nil, engine.ErrNotFound
	}
	return fs[0], nil
}

// StarterInfo è un pacchetto di partenza disponibile.
type StarterInfo struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Items       int    `json:"items"`
	Imported    bool   `json:"imported"`
}

// Library è la pagina dei cataloghi: normative del tenant e pacchetti di partenza.
type Library struct {
	Frameworks []*FrameworkInfo `json:"frameworks"`
	Starters   []StarterInfo    `json:"starters"`
	Settings   *Settings        `json:"settings"`
	CanEdit    bool             `json:"can_edit"`
}

// Library elenca i cataloghi visibili (del tenant e di sistema) e i pacchetti di partenza.
func (s *Service) Library(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*Library, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	fs, err := s.loadFrameworks(ctx, tx, "")
	if err != nil {
		return nil, err
	}
	st, err := loadSettings(ctx, tx)
	if err != nil {
		return nil, err
	}
	out := &Library{Frameworks: fs, Starters: []StarterInfo{}, Settings: st, CanEdit: actor.Has(PermAdmin)}
	if out.Frameworks == nil {
		out.Frameworks = []*FrameworkInfo{}
	}
	for _, k := range StarterKeys {
		cf, err := starterCatalog(k)
		if err != nil {
			return nil, err
		}
		imported := slices.ContainsFunc(fs, func(f *FrameworkInfo) bool { return f.Key == cf.Framework.Key && !f.System })
		out.Starters = append(out.Starters, StarterInfo{Key: k, Name: cf.Framework.Name, Description: cf.Framework.Description,
			Version: cf.Version.Version, Items: len(cf.Items), Imported: imported})
	}
	return out, nil
}

// ItemView è una voce di una versione.
type ItemView struct {
	ID         string         `json:"id"`
	Ref        string         `json:"ref"`
	Parent     string         `json:"parent"`
	Kind       string         `json:"kind"`
	Title      string         `json:"title"`
	Summary    string         `json:"summary"`
	Assessable bool           `json:"assessable"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

func loadItems(ctx context.Context, tx pgx.Tx, versionID string) ([]ItemView, error) {
	rows, err := tx.Query(ctx, `select r.id, r.ref, coalesce(p.ref, ''), r.kind, r.title, coalesce(r.body, ''),
		r.kind not in ('chapter','annex') and not exists (select 1 from requirements c where c.parent_id = r.id), r.metadata
		from requirements r left join requirements p on p.id = r.parent_id
		where r.version_id = $1 order by r.ordinal, r.ref`, versionID)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (ItemView, error) {
		var it ItemView
		var meta []byte
		if err := r.Scan(&it.ID, &it.Ref, &it.Parent, &it.Kind, &it.Title, &it.Summary, &it.Assessable, &meta); err != nil {
			return it, err
		}
		if len(meta) > 2 {
			_ = json.Unmarshal(meta, &it.Metadata)
		}
		return it, nil
	})
}

// FrameworkDetail è un catalogo con le voci della versione corrente o della bozza.
type FrameworkDetail struct {
	*FrameworkInfo
	Shown   *VersionInfo `json:"shown"`
	Items   []ItemView   `json:"items"`
	CanEdit bool         `json:"can_edit"`
}

// Framework restituisce il catalogo con le voci (draft = la bozza, se c'è).
func (s *Service) Framework(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string, draft bool) (*FrameworkDetail, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	f, err := s.framework(ctx, tx, key)
	if err != nil {
		return nil, err
	}
	d := &FrameworkDetail{FrameworkInfo: f, Items: []ItemView{}, CanEdit: actor.Has(PermAdmin) && !f.System}
	d.Shown = f.Current
	if draft && f.Draft != nil || d.Shown == nil {
		d.Shown = f.Draft
	}
	if d.Shown != nil {
		if d.Items, err = loadItems(ctx, tx, d.Shown.ID); err != nil {
			return nil, err
		}
	}
	return d, nil
}

// ---------------------------------------------------------------------
// Scrittura

// ImportOptions: pubblica subito la versione, abilita la normativa nel tenant, pacchetto di partenza.
type ImportOptions struct {
	Publish bool
	Enable  bool
	Starter string
}

// ImportCatalog porta un catalogo nel tenant: crea la normativa se manca e mette le voci in
// una bozza (sostituendo quella esistente). Con Publish la pubblica, con Enable la abilita.
func (s *Service) ImportCatalog(ctx context.Context, tx pgx.Tx, actor engine.Actor, c *CatalogFile, o ImportOptions) (*FrameworkInfo, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	f := c.Framework
	var fwID string
	err := tx.QueryRow(ctx, "select id from frameworks where owner_tenant_id = app_tenant() and key = $1", f.Key).Scan(&fwID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		fwID = db.NewID()
		if _, err := tx.Exec(ctx, `insert into frameworks (id, owner_tenant_id, key, name, kind, jurisdiction, publisher, description, starter)
			values ($1, app_tenant(), $2, $3, $4, $5, $6, $7, $8)`,
			fwID, f.Key, f.Name, f.Kind, nullable(f.Jurisdiction), nullable(f.Publisher), f.Description, nullable(o.Starter)); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	default:
		if _, err := tx.Exec(ctx, `update frameworks set name = $2, kind = $3, jurisdiction = $4, publisher = $5, description = $6, updated_at = now()
			where id = $1`, fwID, f.Name, f.Kind, nullable(f.Jurisdiction), nullable(f.Publisher), f.Description); err != nil {
			return nil, err
		}
	}
	var dup bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from framework_versions where framework_id = $1 and version = $2 and status <> 'draft')`,
		fwID, c.Version.Version).Scan(&dup); err != nil {
		return nil, err
	}
	if dup {
		return nil, engine.InvalidParams("validation.grc.version_already_published", "version.version", map[string]any{"version": c.Version.Version, "key": f.Key}, "la versione %q di %s è già pubblicata: indica un numero di versione nuovo", c.Version.Version, f.Key)
	}
	versionID, err := s.writeDraft(ctx, tx, actor, fwID, c)
	if err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "grc.catalog.import", "framework", fwID, map[string]any{
		"key": f.Key, "version": c.Version.Version, "items": len(c.Items), "starter": o.Starter}); err != nil {
		return nil, err
	}
	if o.Publish {
		if err := s.publishVersion(ctx, tx, actor, fwID, versionID); err != nil {
			return nil, err
		}
		if o.Enable {
			if err := s.setStatus(ctx, tx, actor, f.Key, "active"); err != nil {
				return nil, err
			}
		}
	}
	return s.framework(ctx, tx, f.Key)
}

// writeDraft sostituisce la bozza della normativa con le voci del catalogo.
func (s *Service) writeDraft(ctx context.Context, tx pgx.Tx, actor engine.Actor, fwID string, c *CatalogFile) (string, error) {
	var versionID string
	err := tx.QueryRow(ctx, "select id from framework_versions where framework_id = $1 and status = 'draft'", fwID).Scan(&versionID)
	var eff any
	if c.Version.EffectiveFrom != "" {
		eff = c.Version.EffectiveFrom
	}
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		versionID = db.NewID()
		if _, err := tx.Exec(ctx, `insert into framework_versions (id, framework_id, version, effective_from, source_url, license_note, notes, status, created_by)
			values ($1, $2, $3, $4, $5, $6, $7, 'draft', $8)`, versionID, fwID, c.Version.Version, eff, nullable(c.Version.SourceURL),
			nullable(c.Version.LicenseNote), c.Version.Notes, nullable(actor.MemberID)); err != nil {
			return "", err
		}
	case err != nil:
		return "", err
	default:
		if _, err := tx.Exec(ctx, "delete from requirements where version_id = $1", versionID); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `update framework_versions set version = $2, effective_from = $3, source_url = $4, license_note = $5, notes = $6
			where id = $1`, versionID, c.Version.Version, eff, nullable(c.Version.SourceURL), nullable(c.Version.LicenseNote), c.Version.Notes); err != nil {
			return "", err
		}
	}
	ids := map[string]string{}
	for i, it := range c.Items {
		id := db.NewID()
		ids[it.Ref] = id
		var parent any
		if it.Parent != "" {
			parent = ids[it.Parent]
		}
		meta := it.Metadata
		if meta == nil {
			meta = map[string]any{}
		}
		if _, err := tx.Exec(ctx, `insert into requirements (id, version_id, parent_id, ref, title, body, kind, ordinal, metadata)
			values ($1, $2, $3, $4, $5, $6, $7, $8, $9)`, id, versionID, parent, it.Ref, it.Title, nullable(strings.TrimSpace(it.Summary)),
			it.Kind, i, meta); err != nil {
			return "", err
		}
	}
	return versionID, nil
}

// fromDB ricostruisce il file del catalogo di una versione.
func catalogFromDB(ctx context.Context, tx pgx.Tx, f *FrameworkInfo, v *VersionInfo) (*CatalogFile, error) {
	items, err := loadItems(ctx, tx, v.ID)
	if err != nil {
		return nil, err
	}
	c := &CatalogFile{Format: CatalogFormat,
		Framework: FrameworkMeta{Key: f.Key, Name: f.Name, Kind: f.Kind, Jurisdiction: f.Jurisdiction, Publisher: f.Publisher, Description: f.Description},
		Version:   VersionMeta{Version: v.Version, SourceURL: v.SourceURL, LicenseNote: v.LicenseNote, Notes: v.Notes},
		Items:     make([]CatalogItem, 0, len(items))}
	if v.EffectiveFrom != nil {
		c.Version.EffectiveFrom = v.EffectiveFrom.Format(time.DateOnly)
	}
	for _, it := range items {
		c.Items = append(c.Items, CatalogItem{Ref: it.Ref, Parent: it.Parent, Kind: it.Kind, Title: it.Title, Summary: it.Summary, Metadata: it.Metadata})
	}
	return c, nil
}

// Export restituisce il catalogo di una versione (vuota = la corrente, oppure la bozza).
func (s *Service) Export(ctx context.Context, tx pgx.Tx, actor engine.Actor, key, version string) (*CatalogFile, error) {
	if err := requireRead(actor); err != nil {
		return nil, err
	}
	f, err := s.framework(ctx, tx, key)
	if err != nil {
		return nil, err
	}
	var v *VersionInfo
	for i := range f.Versions {
		if version == "" && f.Versions[i].Status == "published" || version != "" && f.Versions[i].Version == version {
			v = &f.Versions[i]
			break
		}
	}
	if v == nil && version == "" {
		v = f.Draft
	}
	if v == nil {
		return nil, engine.ErrNotFound
	}
	return catalogFromDB(ctx, tx, f, v)
}

func (s *Service) ownFramework(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string) (*FrameworkInfo, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	f, err := s.framework(ctx, tx, key)
	if err != nil {
		return nil, err
	}
	if f.System {
		return nil, engine.InvalidParams("validation.grc.system_catalog_readonly", "framework", map[string]any{"key": key}, "il catalogo di sistema %s non si modifica: importane una copia", key)
	}
	return f, nil
}

// NewDraft apre una nuova versione in bozza a partire dalla corrente.
func (s *Service) NewDraft(ctx context.Context, tx pgx.Tx, actor engine.Actor, key, version string) (*FrameworkInfo, error) {
	f, err := s.ownFramework(ctx, tx, actor, key)
	if err != nil {
		return nil, err
	}
	if f.Draft != nil {
		return nil, engine.Duplicate("draft", "c'è già una bozza (%s): pubblicala o scartala", f.Draft.Version)
	}
	if f.Current == nil {
		return nil, engine.InvalidCode("validation.grc.no_published_version", "draft", "nessuna versione pubblicata da cui partire")
	}
	c, err := catalogFromDB(ctx, tx, f, f.Current)
	if err != nil {
		return nil, err
	}
	c.Version.Version = strings.TrimSpace(version)
	c.Version.Notes = ""
	return s.ImportCatalog(ctx, tx, actor, c, ImportOptions{})
}

// DraftItem aggiunge o sostituisce una voce della bozza (per completare un catalogo a mano).
func (s *Service) DraftItem(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string, it CatalogItem, remove bool) (*FrameworkDetail, error) {
	f, err := s.ownFramework(ctx, tx, actor, key)
	if err != nil {
		return nil, err
	}
	if f.Draft == nil {
		return nil, engine.InvalidCode("validation.grc.open_draft_first", "draft", "apri prima una nuova versione in bozza")
	}
	c, err := catalogFromDB(ctx, tx, f, f.Draft)
	if err != nil {
		return nil, err
	}
	idx := slices.IndexFunc(c.Items, func(x CatalogItem) bool { return x.Ref == strings.TrimSpace(it.Ref) })
	switch {
	case remove && idx < 0:
		return nil, engine.ErrNotFound
	case remove:
		ref := c.Items[idx].Ref
		if slices.ContainsFunc(c.Items, func(x CatalogItem) bool { return x.Parent == ref }) {
			return nil, engine.InvalidParams("validation.grc.item_has_children", "ref", map[string]any{"ref": ref}, "%s ha voci figlie: toglile prima", ref)
		}
		c.Items = slices.Delete(c.Items, idx, idx+1)
	case idx >= 0:
		c.Items[idx] = it
	default:
		// La nuova voce va dopo l'ultima del suo padre (o in fondo).
		pos := len(c.Items)
		if it.Parent != "" {
			for i, x := range c.Items {
				if x.Ref == it.Parent || x.Parent == it.Parent {
					pos = i + 1
				}
			}
		}
		c.Items = slices.Insert(c.Items, pos, it)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if _, err := s.writeDraft(ctx, tx, actor, f.ID, c); err != nil {
		return nil, err
	}
	if err := audited(ctx, tx, actor, "grc.catalog.draft_item", "framework", f.ID, map[string]any{"ref": it.Ref, "removed": remove}); err != nil {
		return nil, err
	}
	return s.Framework(ctx, tx, actor, key, true)
}

// DiscardDraft scarta la bozza.
func (s *Service) DiscardDraft(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string) error {
	f, err := s.ownFramework(ctx, tx, actor, key)
	if err != nil {
		return err
	}
	if f.Draft == nil {
		return engine.ErrNotFound
	}
	if _, err := tx.Exec(ctx, "delete from requirements where version_id = $1", f.Draft.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "delete from framework_versions where id = $1", f.Draft.ID); err != nil {
		return err
	}
	if f.Current == nil {
		// Normativa mai pubblicata: sparisce con la sua unica bozza.
		if _, err := tx.Exec(ctx, "delete from frameworks where id = $1", f.ID); err != nil {
			return err
		}
	}
	return audited(ctx, tx, actor, "grc.catalog.discard", "framework", f.ID, map[string]any{"key": key, "version": f.Draft.Version})
}

// Publish pubblica la bozza.
func (s *Service) Publish(ctx context.Context, tx pgx.Tx, actor engine.Actor, key string) (*FrameworkInfo, error) {
	f, err := s.ownFramework(ctx, tx, actor, key)
	if err != nil {
		return nil, err
	}
	if f.Draft == nil {
		return nil, engine.InvalidCode("validation.grc.no_draft", "draft", "nessuna bozza da pubblicare")
	}
	if err := s.publishVersion(ctx, tx, actor, f.ID, f.Draft.ID); err != nil {
		return nil, err
	}
	return s.framework(ctx, tx, key)
}

func (s *Service) publishVersion(ctx context.Context, tx pgx.Tx, actor engine.Actor, fwID, versionID string) error {
	var n int
	if err := tx.QueryRow(ctx, `select count(*) from requirements r where r.version_id = $1 and r.kind not in ('chapter','annex')
		and not exists (select 1 from requirements c where c.parent_id = r.id)`, versionID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return engine.InvalidCode("validation.grc.catalog_no_requirements", "items", "il catalogo non ha requisiti da valutare: aggiungili prima di pubblicare")
	}
	var oldID string
	err := tx.QueryRow(ctx, "select id from framework_versions where framework_id = $1 and status = 'published'", fwID).Scan(&oldID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if oldID != "" {
		if _, err := tx.Exec(ctx, "update framework_versions set status = 'superseded' where id = $1", oldID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, "update framework_versions set status = 'published', published_at = now(), published_by = $2 where id = $1",
		versionID, nullable(actor.MemberID)); err != nil {
		return err
	}
	carried := map[string]int64{}
	if oldID != "" {
		// Ciò che il tenant ha costruito sui requisiti passa alla nuova versione per riferimento.
		const m = `with m as (select o.id as old_id, n.id as new_id from requirements o join requirements n on n.ref = o.ref
			where o.version_id = $1 and n.version_id = $2) `
		steps := []struct{ name, sql string }{
			{"controls", m + `update control_requirements cr set requirement_id = m.new_id from m where cr.requirement_id = m.old_id`},
			{"applicability", m + `update applicability a set requirement_id = m.new_id from m where a.requirement_id = m.old_id and a.rule_key is null`},
			{"mappings_from", m + `update requirement_mappings x set from_requirement_id = m.new_id from m where x.from_requirement_id = m.old_id
				and x.owner_tenant_id = app_tenant()`},
			{"mappings_to", m + `update requirement_mappings x set to_requirement_id = m.new_id from m where x.to_requirement_id = m.old_id
				and x.owner_tenant_id = app_tenant()`},
		}
		for _, st := range steps {
			tag, err := tx.Exec(ctx, st.sql, oldID, versionID)
			if err != nil {
				return fmt.Errorf("passaggio di %s alla nuova versione: %w", st.name, err)
			}
			carried[st.name] = tag.RowsAffected()
		}
		if _, err := tx.Exec(ctx, "delete from applicability where requirement_id in (select id from requirements where version_id = $1)", oldID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `insert into tenant_frameworks (tenant_id, framework_version_id, status)
			select app_tenant(), $2, status from tenant_frameworks where framework_version_id = $1 on conflict do nothing`, oldID, versionID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "update tenant_frameworks set status = 'retired' where framework_version_id = $1", oldID); err != nil {
			return err
		}
	}
	if err := audited(ctx, tx, actor, "grc.catalog.publish", "framework", fwID, map[string]any{
		"version_id": versionID, "previous": oldID, "carried": carried}); err != nil {
		return err
	}
	_, err = s.Recompute(ctx, tx, actor)
	return err
}

// SetStatus abilita (active), sospende (paused) o ritira (retired) la normativa nel tenant.
func (s *Service) SetStatus(ctx context.Context, tx pgx.Tx, actor engine.Actor, key, status string) (*FrameworkInfo, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	if err := s.setStatus(ctx, tx, actor, key, status); err != nil {
		return nil, err
	}
	if _, err := s.Recompute(ctx, tx, actor); err != nil {
		return nil, err
	}
	return s.framework(ctx, tx, key)
}

func (s *Service) setStatus(ctx context.Context, tx pgx.Tx, actor engine.Actor, key, status string) error {
	if !slices.Contains([]string{"active", "paused", "retired"}, status) {
		return engine.InvalidValue("status", "stato non ammesso (active, paused, retired)")
	}
	f, err := s.framework(ctx, tx, key)
	if err != nil {
		return err
	}
	if f.Current == nil {
		return engine.InvalidParams("validation.grc.no_published_for", "status", map[string]any{"key": key}, "%s non ha una versione pubblicata", key)
	}
	if _, err := tx.Exec(ctx, `insert into tenant_frameworks (tenant_id, framework_version_id, status) values (app_tenant(), $1, $2)
		on conflict (tenant_id, framework_version_id) do update set status = excluded.status,
		  activated_at = case when excluded.status = 'active' and tenant_frameworks.status <> 'active' then now() else tenant_frameworks.activated_at end`,
		f.Current.ID, status); err != nil {
		return err
	}
	return audited(ctx, tx, actor, "grc.catalog.status", "framework", f.ID, map[string]any{"key": key, "status": status, "version_id": f.Current.ID})
}

// ---------------------------------------------------------------------
// Riferimenti "chiave:riferimento"

// SplitRef separa "dora:Art. 9" in chiave e riferimento.
func SplitRef(s string) (fw, ref string, ok bool) {
	fw, ref, ok = strings.Cut(strings.TrimSpace(s), ":")
	fw, ref = strings.TrimSpace(fw), strings.TrimSpace(ref)
	return fw, ref, ok && keyRe.MatchString(fw) && refRe.MatchString(ref)
}

// reqRef è un requisito di una versione pubblicata.
type reqRef struct {
	ID, Framework, FrameworkName, Ref, Title, Kind string
	Assessable                                     bool
}

// resolveRefs trova i requisiti delle versioni pubblicate per "chiave:riferimento".
func resolveRefs(ctx context.Context, tx pgx.Tx, refs []string) (map[string]reqRef, error) {
	out := map[string]reqRef{}
	if len(refs) == 0 {
		return out, nil
	}
	keys, rs := make([]string, 0, len(refs)), make([]string, 0, len(refs))
	for _, r := range refs {
		fw, ref, ok := SplitRef(r)
		if !ok {
			return nil, engine.InvalidValue("ref", "riferimento %q non valido: chiave:riferimento (es. dora:Art. 9)", r)
		}
		keys, rs = append(keys, fw), append(rs, ref)
	}
	rows, err := tx.Query(ctx, `select r.id, f.key, f.name, r.ref, r.title, r.kind,
		r.kind not in ('chapter','annex') and not exists (select 1 from requirements c where c.parent_id = r.id)
		from unnest($1::text[], $2::text[]) as x(fw, ref)
		join frameworks f on f.key = x.fw
		join framework_versions v on v.framework_id = f.id and v.status = 'published'
		join requirements r on r.version_id = v.id and r.ref = x.ref
		order by f.owner_tenant_id is null`, keys, rs)
	if err != nil {
		return nil, err
	}
	var x reqRef
	if _, err := pgx.ForEachRow(rows, []any{&x.ID, &x.Framework, &x.FrameworkName, &x.Ref, &x.Title, &x.Kind, &x.Assessable}, func() error {
		k := x.Framework + ":" + x.Ref
		if _, ok := out[k]; !ok {
			out[k] = x
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return out, nil
}
