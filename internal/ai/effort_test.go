package ai

import "testing"

// Il ragionamento della funzione vince; poi le richieste brevi ragionano poco; poi quello del
// livello; in automatico il rapido poco e l'approfondito molto, o quanto dice la funzione.
func TestEffortFor(t *testing.T) {
	for _, c := range []struct {
		route, tier, feature string
		deep, brief          bool
		want                 string
	}{
		{"", "", "", false, false, EffortLow},
		{"", "", "", true, false, EffortHigh},
		{"", "", "", true, true, EffortLow},
		{"", EffortMedium, "", true, false, EffortMedium},
		{"", EffortHigh, "", true, true, EffortLow},
		{EffortHigh, "", "", true, true, EffortHigh},
		{EffortMedium, EffortLow, "", false, false, EffortMedium},
		{"", "", EffortMedium, true, false, EffortMedium},
		{"", "", EffortMedium, false, false, EffortLow},
		{"", EffortHigh, EffortMedium, true, false, EffortHigh},
	} {
		if got := effortFor(c.route, c.tier, c.feature, c.deep, c.brief); got != c.want {
			t.Errorf("effortFor(%q, %q, %q, %v, %v) = %q, voluto %q", c.route, c.tier, c.feature, c.deep, c.brief, got, c.want)
		}
	}
	if EffortOf(Request{Deep: true}) != EffortHigh || EffortOf(Request{Deep: true, Effort: EffortMedium}) != EffortMedium {
		t.Fatal("EffortOf")
	}
	for _, v := range []string{"", "low", "medium", "high"} {
		if !ValidEffort(v) {
			t.Errorf("%q dovrebbe essere valido", v)
		}
	}
	if ValidEffort("max") || ValidEffort("alto") {
		t.Error("valori non ammessi accettati")
	}
}

// La finestra tiene gli ultimi historyKeep messaggi; si riassumono solo quelli usciti dalla
// finestra e non ancora coperti dal riassunto.
func TestSummaryWindow(t *testing.T) {
	mk := func(n int) []ChatMessage {
		out := make([]ChatMessage, n)
		for i := range out {
			out[i] = ChatMessage{Role: RoleUser, Text: "m"}
		}
		return out
	}
	if older, window, _ := summaryWindow(mk(historyKeep), 0); older != nil || len(window) != historyKeep {
		t.Fatalf("finestra piena: %d %d", len(older), len(window))
	}
	older, window, cut := summaryWindow(mk(historyKeep+6), 0)
	if len(older) != 6 || len(window) != historyKeep || cut != 6 {
		t.Fatalf("prima volta: %d %d %d", len(older), len(window), cut)
	}
	if older, _, _ = summaryWindow(mk(historyKeep+6), 4); len(older) != 2 {
		t.Fatalf("a pezzi: %d", len(older))
	}
	if older, _, _ = summaryWindow(mk(historyKeep+6), 6); len(older) != 0 {
		t.Fatalf("già coperti: %d", len(older))
	}
	msgs := withSummary([]Message{{Role: RoleUser, Text: "domanda"}}, "riassunto")
	if msgs[0].Text == "domanda" || len(msgs) != 1 {
		t.Fatalf("riassunto in testa: %v", msgs)
	}
}
