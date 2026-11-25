package notify

import (
	"testing"

	"regia/internal/platform/i18n"
)

// Le notifiche del timesheet si scrivono nelle due lingue, con una o più settimane.
func TestDescribeTimesheet(t *testing.T) {
	cases := []struct {
		p              map[string]any
		wantIt, wantEn string
	}{
		{map[string]any{"event": "submitted", "member": "Dev", "week": "2026-11-16", "count": float64(1)},
			"Dev ha inviato il timesheet della settimana del 2026-11-16 da approvare", "Dev sent the timesheet for the week of 2026-11-16 for approval"},
		{map[string]any{"event": "submitted", "member": "Dev", "week": "2026-11-23", "count": float64(3)},
			"Dev ha inviato 3 settimane di timesheet da approvare", "Dev sent 3 timesheet weeks for approval"},
		{map[string]any{"event": "approved", "actor": "Lea", "week": "2026-11-16"},
			"Lea ha approvato il tuo timesheet della settimana del 2026-11-16", "Lea approved your timesheet for the week of 2026-11-16"},
		{map[string]any{"event": "rejected", "actor": "Lea", "week": "2026-11-16", "comment": "Manca il giovedì"},
			"Lea ha respinto il tuo timesheet della settimana del 2026-11-16: Manca il giovedì", "Lea rejected your timesheet for the week of 2026-11-16: Manca il giovedì"},
		{map[string]any{"event": "reopened", "actor": "Ada", "week": "2026-11-16"},
			"Ada ha riaperto il tuo timesheet della settimana del 2026-11-16: puoi modificarlo e reinviarlo", "Ada reopened your timesheet for the week of 2026-11-16: you can edit and resubmit it"},
	}
	for _, c := range cases {
		it := Item{Kind: KindTimesheet, Payload: c.p}
		if got := DescribeLang(i18n.It, it); got != c.wantIt {
			t.Errorf("it %v: %q, voglio %q", c.p["event"], got, c.wantIt)
		}
		if got := DescribeLang(i18n.En, it); got != c.wantEn {
			t.Errorf("en %v: %q, voglio %q", c.p["event"], got, c.wantEn)
		}
	}
	if !ValidKind(KindTimesheet) {
		t.Error("il tipo timesheet non è tra i tipi validi")
	}
}
