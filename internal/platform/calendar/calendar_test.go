package calendar

import (
	"testing"
	"time"
)

func d(s string) time.Time { t, _ := Parse(s); return t }

func TestCalendar(t *testing.T) {
	if got := easter(2027); !got.Equal(d("2027-03-28")) {
		t.Fatalf("Pasqua 2027: %v", got)
	}
	for _, s := range []string{"2026-12-08", "2026-12-25", "2027-03-29", "2027-06-02"} {
		if Workday(d(s)) {
			t.Errorf("%s dovrebbe essere festivo", s)
		}
	}
	// Venerdì 4 dicembre 2026 + 3 giorni: 4, 7, (8 festivo) 9.
	if got := EndAfter(d("2026-12-04"), 3); !got.Equal(d("2026-12-09")) {
		t.Fatalf("EndAfter: %v", got)
	}
	if n := Workdays(d("2026-12-04"), d("2026-12-09")); n != 3 {
		t.Fatalf("Workdays: %d", n)
	}
	if got := AfterEnd(d("2026-12-24")); !got.Equal(d("2026-12-28")) {
		t.Fatalf("AfterEnd: %v", got)
	}
}

func TestAddWorkdaysAndLate(t *testing.T) {
	// Venerdì 4 dicembre 2026 + 5 giorni lavorativi: 7, 9, 10, 11, 14 (l'8 è festivo).
	if got := AddWorkdays(d("2026-12-04"), 5); !got.Equal(d("2026-12-14")) {
		t.Fatalf("AddWorkdays: %v", got)
	}
	// Consegna di sabato: si conta da lunedì.
	if got := AddWorkdays(d("2026-12-05"), 1); !got.Equal(d("2026-12-07")) {
		t.Fatalf("AddWorkdays dal sabato: %v", got)
	}
	// Scadenza il 23 dicembre: il 28 il ritardo è di 2 giorni, il 24 e il 28.
	if n := Late(d("2026-12-23"), d("2026-12-28")); n != 2 {
		t.Fatalf("Late: %d", n)
	}
	if n := Late(d("2026-12-23"), d("2026-12-23")); n != 0 {
		t.Fatalf("Late il giorno della scadenza: %d", n)
	}
}
