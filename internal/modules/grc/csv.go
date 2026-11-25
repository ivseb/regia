package grc

import (
	"bytes"
	"encoding/csv"
	"io"
	"strings"

	"regia/internal/engine"
)

// CSV dei cataloghi e delle corrispondenze: separatore «;» e BOM all'export (Excel in
// italiano), all'import «;» o «,» secondo l'intestazione. Colonne di un catalogo:
// riferimento;padre;tipo;titolo;sintesi (anche in inglese: ref;parent;kind;title;summary).
// Colonne delle corrispondenze: da;a;relazione (riferimenti "chiave:riferimento").

const bom = "\xef\xbb\xbf" // BOM UTF-8

func newCSVWriter(w io.Writer) *csv.Writer {
	_, _ = io.WriteString(w, bom)
	cw := csv.NewWriter(w)
	cw.Comma = ';'
	return cw
}

// WriteCatalogCSV scrive le voci del catalogo.
func WriteCatalogCSV(w io.Writer, c *CatalogFile) error {
	cw := newCSVWriter(w)
	_ = cw.Write([]string{"riferimento", "padre", "tipo", "titolo", "sintesi"})
	for _, it := range c.Items {
		_ = cw.Write([]string{it.Ref, it.Parent, KindNames[it.Kind], it.Title, it.Summary})
	}
	cw.Flush()
	return cw.Error()
}

// readCSV legge un CSV con intestazione: righe come mappe colonna → valore.
func readCSV(raw string) ([]map[string]string, error) {
	raw = strings.TrimPrefix(raw, bom)
	first, _, _ := strings.Cut(raw, "\n")
	r := csv.NewReader(strings.NewReader(raw))
	r.Comma = ';'
	if strings.Count(first, ",") > strings.Count(first, ";") {
		r.Comma = ','
	}
	r.FieldsPerRecord = -1
	r.TrimLeadingSpace = true
	recs, err := r.ReadAll()
	if err != nil {
		return nil, engine.InvalidValue("csv", "CSV non valido: %v", err)
	}
	if len(recs) < 1 {
		return nil, engine.InvalidCode("validation.grc.csv_empty", "csv", "CSV vuoto")
	}
	head := make([]string, len(recs[0]))
	for i, h := range recs[0] {
		head[i] = strings.ToLower(strings.TrimSpace(h))
	}
	out := []map[string]string{}
	for _, rec := range recs[1:] {
		row := map[string]string{}
		empty := true
		for i, v := range rec {
			if i < len(head) {
				row[head[i]] = strings.TrimSpace(v)
				if row[head[i]] != "" {
					empty = false
				}
			}
		}
		if !empty {
			out = append(out, row)
		}
	}
	return out, nil
}

func col(row map[string]string, names ...string) string {
	for _, n := range names {
		if v, ok := row[n]; ok {
			return v
		}
	}
	return ""
}

func kindFromCSV(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	for k, it := range KindNames {
		if s == it || s == k {
			return k
		}
	}
	return s
}

// CatalogFromCSV legge le voci di un catalogo da CSV; la normativa e la versione arrivano a parte.
func CatalogFromCSV(raw string, meta FrameworkMeta, version VersionMeta) (*CatalogFile, error) {
	rows, err := readCSV(raw)
	if err != nil {
		return nil, err
	}
	c := &CatalogFile{Format: CatalogFormat, Framework: meta, Version: version}
	for _, row := range rows {
		c.Items = append(c.Items, CatalogItem{
			Ref:     col(row, "riferimento", "ref", "id"),
			Parent:  col(row, "padre", "parent"),
			Kind:    kindFromCSV(col(row, "tipo", "kind")),
			Title:   col(row, "titolo", "title"),
			Summary: col(row, "sintesi", "summary"),
		})
	}
	return c, c.Validate()
}

// WriteMappingsCSV scrive le corrispondenze.
func WriteMappingsCSV(w io.Writer, ms []MappingView) error {
	cw := newCSVWriter(w)
	_ = cw.Write([]string{"da", "titolo da", "a", "titolo a", "relazione", "fonte"})
	for _, m := range ms {
		_ = cw.Write([]string{m.From, m.FromTitle, m.To, m.ToTitle, RelationNames[m.Relation], m.Source})
	}
	cw.Flush()
	return cw.Error()
}

// MappingsFromCSV legge corrispondenze da CSV (da;a;relazione).
func MappingsFromCSV(raw string) ([]MappingInput, error) {
	rows, err := readCSV(raw)
	if err != nil {
		return nil, err
	}
	out := []MappingInput{}
	for _, row := range rows {
		rel := strings.ToLower(col(row, "relazione", "relation"))
		for k, n := range RelationNames {
			if rel == n {
				rel = k
			}
		}
		out = append(out, MappingInput{From: col(row, "da", "from"), To: col(row, "a", "to"), Relation: rel, Source: col(row, "fonte", "source")})
	}
	return out, nil
}

func csvBytes(fn func(w io.Writer) error) ([]byte, error) {
	var b bytes.Buffer
	err := fn(&b)
	return b.Bytes(), err
}
