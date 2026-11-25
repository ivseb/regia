package importer

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Date degli export. Gli strumenti scrivono le date nel formato dell'utente che esporta
// (Jira di default «24/Nov/26 10:30 AM», YouTrack «2026-11-24 10:30»), senza fuso orario:
// si interpretano nel fuso dell'organizzazione. Il formato si riconosce sui valori di tutto
// il file (DateFormats, «auto») e si può scegliere nella mappatura.

// DateFormat è una famiglia di formati con lo stesso ordine di giorno, mese e anno.
type DateFormat struct {
	Key     string   `json:"key"`
	Name    string   `json:"name"`
	Example string   `json:"example"`
	layouts []string // formati Go, provati nell'ordine
}

// DateFormats sono i formati riconosciuti, nell'ordine in cui si provano con «auto».
var DateFormats = []DateFormat{
	{Key: "iso", Name: "ISO (AAAA-MM-GG)", Example: "2026-11-24 10:30", layouts: []string{
		time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000-0700", "2006-01-02T15:04:05-0700", "2006-01-02T15:04:05",
		"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"}},
	{Key: "jira", Name: "Jira (GG/Mmm/AA)", Example: "24/Nov/26 10:30 AM", layouts: []string{
		"2/Jan/06 3:04 PM", "2/Jan/06 15:04", "2/Jan/2006 3:04 PM", "2/Jan/2006 15:04", "2/Jan/06", "2/Jan/2006"}},
	{Key: "dmy", Name: "Europeo (GG/MM/AAAA)", Example: "24/11/2026 10:30", layouts: []string{
		"2/1/2006 15:04:05", "2/1/2006 15:04", "2/1/2006 3:04 PM", "2/1/2006", "2/1/06 15:04", "2/1/06"}},
	{Key: "dmy_dot", Name: "Con i punti (GG.MM.AAAA)", Example: "24.11.2026 10:30", layouts: []string{
		"2.1.2006 15:04:05", "2.1.2006 15:04", "2.1.2006"}},
	{Key: "mdy", Name: "Americano (MM/GG/AAAA)", Example: "11/24/2026 10:30 AM", layouts: []string{
		"1/2/2006 3:04 PM", "1/2/2006 15:04", "1/2/2006", "1/2/06 3:04 PM", "1/2/06"}},
	{Key: "dmony", Name: "Esteso (GG Mmm AAAA)", Example: "24 Nov 2026 10:30", layouts: []string{
		"2 Jan 2006 15:04:05", "2 Jan 2006 15:04", "2 Jan 2006 3:04 PM", "2 Jan 2006", "Jan 2, 2006 3:04 PM", "Jan 2, 2006"}},
}

// Mesi abbreviati in altre lingue (le esportazioni localizzate) → inglese.
var monthAliases = map[string]string{
	"gen": "Jan", "mag": "May", "giu": "Jun", "lug": "Jul", "ago": "Aug", "set": "Sep", "ott": "Oct", "dic": "Dec",
	"ene": "Jan", "abr": "Apr", "dez": "Dec", "mai": "May", "okt": "Oct", "janv": "Jan", "févr": "Feb", "avr": "Apr",
	"juin": "Jun", "juil": "Jul", "août": "Aug", "déc": "Dec", "mär": "Mar", "märz": "Mar",
}

var monthWordRe = regexp.MustCompile(`\p{L}+\.?`)
var epochRe = regexp.MustCompile(`^\d{12,13}$`)

func normalizeDate(s string) string {
	s = strings.TrimSpace(s)
	s = monthWordRe.ReplaceAllStringFunc(s, func(w string) string {
		k := strings.ToLower(strings.TrimSuffix(w, "."))
		if en, ok := monthAliases[k]; ok {
			return en
		}
		return w
	})
	// AM e PM: Go li vuole maiuscoli (i nomi dei mesi sono già indifferenti alle maiuscole).
	up := strings.ToUpper(s)
	if strings.HasSuffix(up, " AM") || strings.HasSuffix(up, " PM") {
		s = s[:len(s)-2] + up[len(up)-2:]
	}
	return s
}

var errDate = errors.New("data non riconosciuta")

// parseDate legge una data con il formato indicato ("" o "auto" = tutti) nel fuso loc.
func parseDate(s, format string, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, errDate
	}
	if epochRe.MatchString(s) {
		n, _ := strconv.ParseInt(s, 10, 64)
		if len(s) == 13 {
			return time.UnixMilli(n).UTC(), nil
		}
		return time.Unix(n, 0).UTC(), nil
	}
	n := normalizeDate(s)
	for _, f := range DateFormats {
		if format != "" && format != "auto" && f.Key != format && f.Key != "iso" {
			continue
		}
		for _, l := range f.layouts {
			if t, err := time.ParseInLocation(l, n, loc); err == nil {
				return t.UTC(), nil
			}
		}
	}
	return time.Time{}, errDate
}

// detectDateFormat sceglie il primo formato che legge tutti i valori di prova (al massimo
// 2.000). Con giorno e mese ambigui (tutti ≤ 12) vince l'ordine europeo, come in Italia.
func detectDateFormat(values []string) string {
	if len(values) == 0 {
		return "iso"
	}
	if len(values) > 2000 {
		values = values[:2000]
	}
	for _, f := range DateFormats {
		ok := true
		for _, v := range values {
			if strings.TrimSpace(v) == "" || epochRe.MatchString(strings.TrimSpace(v)) {
				continue
			}
			n := normalizeDate(v)
			parsed := false
			for _, l := range f.layouts {
				if _, err := time.Parse(l, n); err == nil {
					parsed = true
					break
				}
			}
			if !parsed && f.Key != "iso" {
				// I valori ISO (es. dall'API) si leggono con ogni formato.
				for _, l := range DateFormats[0].layouts {
					if _, err := time.Parse(l, n); err == nil {
						parsed = true
						break
					}
				}
			}
			if !parsed {
				ok = false
				break
			}
		}
		if ok {
			return f.Key
		}
	}
	return "auto"
}

// dayOnly: la data senza ora (scadenze, inizio) come mezzanotte UTC del giorno nel fuso loc.
func dayOnly(t time.Time, loc *time.Location) time.Time {
	l := t.In(loc)
	return time.Date(l.Year(), l.Month(), l.Day(), 0, 0, 0, 0, time.UTC)
}
