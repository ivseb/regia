package continuity

import (
	"context"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Fatti per le condizioni di processi, regole e automazioni. Si calcolano solo se il modulo è
// preparato: altrimenti non aggiungono niente (e non costano query).

func (s *Service) readyModel(ctx context.Context, tx pgx.Tx) (*model, error) {
	ok, err := isReady(ctx, tx)
	if err != nil || !ok {
		return nil, err
	}
	return loadModel(ctx, tx, s.Now())
}

func (s *Service) appFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	m, err := s.readyModel(ctx, tx)
	if err != nil || m == nil {
		return err
	}
	a, ok := m.apps[el.ID]
	if !ok {
		return nil
	}
	r := m.required(a)
	setInt(f, "rto_richiesto_minuti", r.RTO)
	setInt(f, "rpo_richiesto_minuti", r.RPO)
	issues := m.appIssues(a)
	incoherent := false
	for _, i := range issues {
		switch i.Code {
		case IssueRTOOver, IssueRPOOver, IssueDepSlower:
			incoherent = true
		}
	}
	f.Set("rto_incoerente", incoherent)
	f.Set("continuita_problemi", len(issues))
	if t := m.lastDone(append([]string{a.ID}, r.Caps...)...); t != nil {
		f.Set("ultimo_test_giorni", m.daysSince(*t.Executed))
	}
	f.Set("piano_continuita", m.approvedPlan(append([]string{a.ID}, r.Caps...)...))
	f.Set("funzione_critica_bia", r.Critical)
	return nil
}

func (s *Service) capabilityFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	m, err := s.readyModel(ctx, tx)
	if err != nil || m == nil {
		return err
	}
	bn := m.bias[el.ID]
	if bn == nil || bn.Approved == nil {
		return nil
	}
	f.Set("bia_critica", bn.Approved.Critical)
	setInt(f, "mtpd_minuti", bn.Approved.MTPD)
	setInt(f, "rto_richiesto_minuti", bn.Approved.RTO)
	f.Set("bia_giorni", m.daysSince(bn.Approved.At))
	return nil
}

func (s *Service) biaFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	var crit *bool
	var mtpd, rto, rpo *int
	err := tx.QueryRow(ctx, `select critical, mtpd_minutes, rto_minutes, rpo_minutes from ext_bia where element_id = $1`, el.ID).Scan(&crit, &mtpd, &rto, &rpo)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil
		}
		return err
	}
	f.Set("critica", crit)
	setInt(f, "mtpd_minuti", mtpd)
	setInt(f, "rto_minuti", rto)
	setInt(f, "rpo_minuti", rpo)
	return nil
}

func (s *Service) testFacts(ctx context.Context, tx pgx.Tx, el *engine.Element, f engine.Facts) error {
	var typ string
	var outcome *string
	var rto, rpo *int
	err := tx.QueryRow(ctx, `select test_type, outcome, rto_measured_minutes, rpo_measured_minutes from ext_resilience_test where element_id = $1`, el.ID).
		Scan(&typ, &outcome, &rto, &rpo)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil
		}
		return err
	}
	f.Set("tipo_test", typ)
	f.Set("esito", outcome)
	setInt(f, "rto_misurato_minuti", rto)
	setInt(f, "rpo_misurato_minuti", rpo)
	return nil
}

func describeFacts(eng *engine.Service) {
	yesNo := []string{"sì", "no"}
	eng.DescribeFacts(AppType,
		engine.FactInfo{Key: "rto_richiesto_minuti", Name: "RTO richiesto dalle BIA (minuti)", Kind: "number", Example: "240"},
		engine.FactInfo{Key: "rpo_richiesto_minuti", Name: "RPO richiesto dalle BIA (minuti)", Kind: "number", Example: "60"},
		engine.FactInfo{Key: "rto_incoerente", Name: "RTO o RPO incoerente con BIA o dipendenze", Kind: "bool", Values: yesNo},
		engine.FactInfo{Key: "continuita_problemi", Name: "Incoerenze della continuità", Kind: "number", Example: "0"},
		engine.FactInfo{Key: "ultimo_test_giorni", Name: "Giorni dall'ultimo test di resilienza", Kind: "number", Example: "365"},
		engine.FactInfo{Key: "piano_continuita", Name: "Piano di continuità approvato", Kind: "bool", Values: yesNo},
		engine.FactInfo{Key: "funzione_critica_bia", Name: "Funzione critica secondo la BIA", Kind: "bool", Values: yesNo},
	)
	eng.DescribeFacts(CapabilityType,
		engine.FactInfo{Key: "bia_critica", Name: "Funzione critica o importante (BIA)", Kind: "bool", Values: yesNo},
		engine.FactInfo{Key: "mtpd_minuti", Name: "MTPD della BIA (minuti)", Kind: "number", Example: "1440"},
		engine.FactInfo{Key: "rto_richiesto_minuti", Name: "RTO richiesto dalla BIA (minuti)", Kind: "number", Example: "240"},
		engine.FactInfo{Key: "bia_giorni", Name: "Giorni dall'approvazione della BIA", Kind: "number", Example: "200"},
	)
	eng.DescribeFacts(BIAType,
		engine.FactInfo{Key: "critica", Name: "Funzione critica o importante", Kind: "bool", Values: yesNo},
		engine.FactInfo{Key: "mtpd_minuti", Name: "MTPD (minuti)", Kind: "number", Example: "1440"},
		engine.FactInfo{Key: "rto_minuti", Name: "RTO (minuti)", Kind: "number", Example: "240"},
		engine.FactInfo{Key: "rpo_minuti", Name: "RPO (minuti)", Kind: "number", Example: "60"},
	)
	eng.DescribeFacts(ResTestType,
		engine.FactInfo{Key: "tipo_test", Name: "Tipo di test", Kind: "text"},
		engine.FactInfo{Key: "esito", Name: "Esito del test", Kind: "text", Values: outcomes},
		engine.FactInfo{Key: "rto_misurato_minuti", Name: "RTO misurato (minuti)", Kind: "number", Example: "180"},
		engine.FactInfo{Key: "rpo_misurato_minuti", Name: "RPO misurato (minuti)", Kind: "number", Example: "30"},
	)
}

// setInt imposta un fatto numerico solo se il valore c'è (Set non conosce *int).
func setInt(f engine.Facts, key string, p *int) {
	if p != nil {
		f.Set(key, *p)
	}
}
