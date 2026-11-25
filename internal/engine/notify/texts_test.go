package notify

import (
	"strings"
	"testing"

	"regia/internal/platform/i18n"
)

// Ogni tipo di notifica si scrive nelle due lingue, senza segnaposto rimasti e senza chiavi grezze;
// e l'email si compone nella lingua di chi la riceve.
func TestDescribeInBothLanguages(t *testing.T) {
	item := func(kind string, payload map[string]any) Item {
		return Item{Kind: kind, ElementKey: "REQ-7", ActorName: "Ada", Payload: payload}
	}
	cases := []struct {
		it             Item
		wantIt, wantEn string
	}{
		{item(KindMention, map[string]any{}), "Ada ti ha menzionato in REQ-7", "Ada mentioned you in REQ-7"},
		{item(KindComment, map[string]any{"event": "inline_reply"}), "Ada ha risposto in una discussione su REQ-7", "Ada replied in a discussion on REQ-7"},
		{item(KindRole, map[string]any{"role_name": "Sponsor"}), "Ada ti ha assegnato il ruolo sponsor su REQ-7", "Ada assigned you the sponsor role on REQ-7"},
		{item(KindRole, map[string]any{"role_name": "Sponsor", "group": true}), "Ada ha assegnato al tuo gruppo il ruolo sponsor su REQ-7", "Ada assigned the sponsor role on REQ-7 to your group"},
		{item(KindPhase, map[string]any{"from": "Bozza", "to": "Inviata"}), "REQ-7 è passata da Bozza a Inviata", "REQ-7 moved from Bozza to Inviata"},
		{item(KindReview, map[string]any{"event": "opened", "team": "Sicurezza"}), "Nuova valutazione per il team Sicurezza: REQ-7", "New review for the Sicurezza team: REQ-7"},
		{item(KindSLA, map[string]any{"event": "escalation", "late_days": float64(3), "team": "X"}), "Escalation: REQ-7 è in ritardo di 3 giorni lavorativi (team X)", "Escalation: REQ-7 is 3 business days late (X team)"},
		{item(KindReport, map[string]any{"event": "submitted", "overall": "amber"}), "Nuovo status report di REQ-7 (stato giallo)", "New status report of REQ-7 (status amber)"},
		{item(KindRequirement, map[string]any{"event": "verified", "impact": "IMP-1"}), "REQ-7 è stato verificato (IMP-1)", "REQ-7 was verified (IMP-1)"},
		{item(KindDocument, map[string]any{"event": "reading_requested", "version": float64(2), "due": "2026-12-01"}),
			"Devi leggere e confermare REQ-7 (versione 2) entro il 2026-12-01", "You need to read and confirm REQ-7 (version 2) by 2026-12-01"},
		{item(KindDocument, map[string]any{"event": "reading_requested", "version": float64(2)}),
			"Devi leggere e confermare REQ-7 (versione 2)", "You need to read and confirm REQ-7 (version 2)"},
		{item(KindDocument, map[string]any{"event": "reading_overdue_summary", "missing": float64(1)}),
			"Campagna di lettura in ritardo su REQ-7: manca 1 conferma", "Reading campaign overdue on REQ-7: 1 confirmation missing"},
		{item(KindRisk, map[string]any{"event": "risk_accepted", "until": "2027-01-01"}), "Accettazione del rischio REQ-7 approvata fino al 2027-01-01", "Acceptance of risk REQ-7 approved until 2027-01-01"},
		{item(KindVulnerability, map[string]any{"event": "vuln_overdue", "count": float64(1), "step": "alta", "days": float64(9)}),
			"1 vulnerabilità su REQ-7 scadute (alta), la più vecchia da 9 giorni", "1 vulnerability on REQ-7 overdue (alta), the oldest for 9 days"},
		{item(KindVulnerability, map[string]any{"event": "vuln_due_soon", "count": float64(4), "due": "2026-12-01"}),
			"4 vulnerabilità su REQ-7 scadono entro il 2026-12-01", "4 vulnerabilities on REQ-7 are due by 2026-12-01"},
		{Item{Kind: KindMention, Payload: map[string]any{}}, "Sistema ti ha menzionato in un elemento", "System mentioned you in an item"},
	}
	for _, c := range cases {
		if got := DescribeLang(i18n.It, c.it); got != c.wantIt {
			t.Errorf("it %s: %q, voglio %q", c.it.Kind, got, c.wantIt)
		}
		if got := DescribeLang(i18n.En, c.it); got != c.wantEn {
			t.Errorf("en %s: %q, voglio %q", c.it.Kind, got, c.wantEn)
		}
		if got := Describe(c.it); got != c.wantIt {
			t.Errorf("Describe %s: %q, voglio l'italiano %q", c.it.Kind, got, c.wantIt)
		}
	}
}

func TestComposeInRecipientLanguage(t *testing.T) {
	m := &Mailer{baseURL: "https://regia.example"}
	one := Item{Kind: KindMention, ElementKey: "REQ-7", ActorName: "Ada", Title: "Registro", Payload: map[string]any{}}
	two := []Item{one, one}

	sub, body := m.compose(i18n.It, []Item{one})
	if sub != "[Regia] Ada ti ha menzionato in REQ-7" || !strings.Contains(body, "Novità su Regia:") || !strings.Contains(body, "Tutte le notifiche: https://regia.example/notifiche") {
		t.Errorf("italiano: %q\n%s", sub, body)
	}
	sub, body = m.compose(i18n.En, []Item{one})
	if sub != "[Regia] Ada mentioned you in REQ-7" || !strings.Contains(body, "What's new in Regia:") || !strings.Contains(body, "All notifications: https://regia.example/notifiche") ||
		!strings.Contains(body, "You can choose which emails you get in your notification preferences.") {
		t.Errorf("inglese: %q\n%s", sub, body)
	}
	if sub, _ = m.compose(i18n.En, two); sub != "[Regia] 2 new notifications" {
		t.Errorf("oggetto con più notifiche: %q", sub)
	}
	if sub, _ = m.compose(i18n.It, two); sub != "[Regia] 2 nuove notifiche" {
		t.Errorf("oggetto con più notifiche in italiano: %q", sub)
	}
}
