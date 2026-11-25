package demand

import "regia/internal/engine"

// Campi del linguaggio di query offerti dal demand (piano, passo 16): le date della
// domanda, con gli indici della migrazione 0015.
func registerQueryFields(eng *engine.Service) {
	eng.QueryField(engine.QueryField{
		Key: "deadline", Names: []string{"scadenza", "scadenza_normativa", "deadline"},
		Label: "Scadenza normativa della richiesta (oggi, +30g, 2027-04-30…)",
		Kind:  "date", DateOnly: true, Types: []string{TypeKey},
		Source: `select element_id, regulatory_deadline as v from ext_demand where regulatory_deadline is not null`,
	})
	eng.QueryField(engine.QueryField{
		Key: "desired", Names: []string{"desiderata", "data_desiderata", "desired"},
		Label: "Data desiderata dal richiedente",
		Kind:  "date", DateOnly: true, Types: []string{TypeKey},
		Source: `select element_id, desired_date as v from ext_demand where desired_date is not null`,
	})
}
