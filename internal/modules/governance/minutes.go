package governance

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var months = []string{"gennaio", "febbraio", "marzo", "aprile", "maggio", "giugno", "luglio", "agosto", "settembre", "ottobre", "novembre", "dicembre"}

// longDate: "12 gennaio 2027, ore 15:00" (ora locale del tenant).
func longDate(t time.Time) string {
	return fmt.Sprintf("%d %s %d, ore %02d:%02d", t.Day(), months[t.Month()-1], t.Year(), t.Hour(), t.Minute())
}

func euro(c *int64) string {
	if c == nil {
		return ""
	}
	v := float64(*c) / 100
	if v >= 1000 {
		return strings.ReplaceAll(fmt.Sprintf("%.0f k€", v/1000), ".", ",")
	}
	return fmt.Sprintf("%.0f €", v)
}

// draftMinutes genera la bozza del verbale dalle presenze e dalle decisioni registrate.
// Il segretario la può correggere prima che il presidente la approvi.
func draftMinutes(m *Meeting) string {
	var b strings.Builder
	fmt.Fprintf(&b, "VERBALE — %s\n", m.Title)
	fmt.Fprintf(&b, "%s · seduta del %s\n", m.Body, longDate(m.localTime))
	if m.Location != "" {
		fmt.Fprintf(&b, "Luogo: %s\n", m.Location)
	}
	if m.Link != "" {
		fmt.Fprintf(&b, "Collegamento: %s\n", m.Link)
	}
	b.WriteString("\nPRESENZE\n")
	var present, absent []string
	for _, a := range m.Attendance {
		name := a.Name
		switch {
		case m.ChairID != nil && a.MemberID == *m.ChairID:
			name += " (presidente)"
		case m.SecretaryID != nil && a.MemberID == *m.SecretaryID:
			name += " (segretario)"
		case !a.Voting:
			name += " (senza diritto di voto)"
		}
		if a.Present {
			present = append(present, name)
		} else {
			absent = append(absent, name)
		}
	}
	fmt.Fprintf(&b, "Presenti: %s\n", orNone(present))
	fmt.Fprintf(&b, "Assenti: %s\n", orNone(absent))
	verdict := "raggiunto"
	if !m.Quorum.Reached {
		verdict = "NON raggiunto: la seduta non può deliberare"
	}
	fmt.Fprintf(&b, "Quorum: %d votanti presenti su %d, richiesti %d — %s.\n", m.Quorum.Present, m.Quorum.Voting, m.Quorum.Required, verdict)

	b.WriteString("\nORDINE DEL GIORNO E DECISIONI\n")
	if len(m.Items) == 0 {
		b.WriteString("Nessun punto all'ordine del giorno.\n")
	}
	for i, it := range m.Items {
		amount := ""
		if it.AmountCents != nil {
			amount = " (" + euro(it.AmountCents) + ")"
		}
		fmt.Fprintf(&b, "%d. %s %s%s\n", i+1, it.Key, it.Title, amount)
		if it.Outcome == nil {
			b.WriteString("   Non discusso: torna alla prossima seduta.\n")
			continue
		}
		fmt.Fprintf(&b, "   Decisione: %s.\n", Outcomes[*it.Outcome])
		if it.Conditions != nil && *it.Conditions != "" {
			fmt.Fprintf(&b, "   Condizioni: %s\n", *it.Conditions)
		}
		if it.Note != nil && *it.Note != "" {
			fmt.Fprintf(&b, "   Motivazione e note: %s\n", *it.Note)
		}
	}
	b.WriteString("\n")
	if m.Chair != nil {
		fmt.Fprintf(&b, "Il presidente: %s\n", *m.Chair)
	}
	if m.Secretary != nil {
		fmt.Fprintf(&b, "Il segretario: %s\n", *m.Secretary)
	}
	return b.String()
}

func orNone(xs []string) string {
	if len(xs) == 0 {
		return "nessuno"
	}
	return strings.Join(xs, ", ")
}
