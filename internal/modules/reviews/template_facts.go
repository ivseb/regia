package reviews

import (
	"context"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/i18n"
)

// TemplateFacts sono i fatti usabili nelle condizioni di un questionario: quelli della
// richiesta e, per le valutazioni su un impatto, quelli dell'impatto (la richiesta vi
// compare con il prefisso «richiesta.»).
type TemplateFacts struct {
	Request []engine.FactInfo `json:"request"`
	Impact  []engine.FactInfo `json:"impact"`
}

// TemplateFacts elenca i fatti per il costruttore di condizioni dell'editor dei modelli.
func (s *Service) TemplateFacts(ctx context.Context, tx pgx.Tx, actor engine.Actor) (*TemplateFacts, error) {
	if err := requireAdmin(actor); err != nil {
		return nil, err
	}
	req, err := s.eng.FactsOfType(ctx, tx, actor, RequestType)
	if err != nil {
		return nil, err
	}
	imp, err := s.eng.FactsOfType(ctx, tx, actor, ImpactType)
	if err != nil {
		return nil, err
	}
	lang := i18n.Lang(ctx)
	for _, f := range req {
		f.Key, f.Name = "richiesta."+f.Key, i18n.Name(lang, "Richiesta")+" · "+f.Name
		imp = append(imp, f)
	}
	return &TemplateFacts{Request: req, Impact: imp}, nil
}
