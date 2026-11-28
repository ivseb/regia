package ai

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"regia/internal/ai/tools"
	"regia/internal/engine"
	"regia/internal/platform/db"
)

// Note di rilascio «per il business»: con la funzione Bozze, il modello riscrive le note
// generate di un rilascio in parole semplici per chi ha chiesto il lavoro. È una proposta:
// la persona la legge, la copia o la scarta; Regia non la salva.

// ReleaseNoteItem è un elemento del rilascio (letto dal server, mai dal client).
type ReleaseNoteItem struct {
	Key   string
	Title string
	Type  string
	Group string // new | fix | other
}

// ReleaseNotesInput sono i dati del rilascio da riscrivere.
type ReleaseNotesInput struct {
	Release    string // «servizio versione (ambiente)»
	Items      []ReleaseNoteItem
	ElementIDs []string
	Hint       string
}

// ReleaseNotesResult è la proposta.
type ReleaseNotesResult struct {
	Text   string `json:"text"`
	Status Status `json:"status"`
}

const releaseNotesTask = `Compito: riscrivi le note di questo rilascio per le persone del business che hanno chiesto il lavoro. Parole semplici, niente gergo tecnico, niente nomi di repository o di commit. Raggruppa in «Novità», «Correzioni» e «Altro» (ometti un gruppo vuoto), un punto per elemento o per elementi molto simili, ognuno con la sua chiave fra parentesi alla fine. Spiega cosa cambia per chi usa il servizio, non come è stato fatto. Non aggiungere ciò che i dati non dicono. Markdown: un titolo di secondo livello per gruppo ed elenchi puntati. Solo le note, senza premesse.`

const maxReleaseNoteItems = 200

// ReleaseNotes propone le note di rilascio per il business.
func (s *Service) ReleaseNotes(ctx context.Context, tenantID string, actor engine.Actor, lang string, in ReleaseNotesInput) (*ReleaseNotesResult, error) {
	if actor.MemberID == "" {
		return nil, ErrForbidden
	}
	in.Hint = strings.TrimSpace(in.Hint)
	if utf8.RuneCountInString(in.Hint) > maxHint {
		return nil, invalid("hint", "ai.hint_invalid", "indicazioni troppo lunghe (al massimo 500 caratteri)")
	}
	if len(in.Items) == 0 {
		return nil, invalid("release", "ai.release_notes_empty", "il rilascio non contiene elementi: non ci sono note da riscrivere")
	}
	var (
		target *Target
		masker *Masker
	)
	err := s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true, Member: actor.MemberID}, func(tx pgx.Tx) error {
		var err error
		target, masker, err = s.cardTarget(ctx, tx, FeatureDrafts)
		return err
	})
	if err != nil {
		return nil, err
	}
	groups := map[string]string{"new": "novità", "fix": "correzione", "other": "altro"}
	var b strings.Builder
	fmt.Fprintf(&b, "Rilascio: %s\n", in.Release)
	for i, it := range in.Items {
		if i == maxReleaseNoteItems {
			fmt.Fprintf(&b, "(e altri %d elementi)\n", len(in.Items)-i)
			break
		}
		fmt.Fprintf(&b, "- %s · %s · %s · %s\n", it.Key, groups[it.Group], it.Type, it.Title)
	}
	user := tools.Data("rilascio", masker.Mask(b.String()))
	if in.Hint != "" {
		user += "\n\n" + tools.Data("indicazioni della persona", masker.Mask(in.Hint))
	}
	system := fmt.Sprintf(cardSystem, langName(lang)) + "\n\n" + releaseNotesTask
	ids := in.ElementIDs
	if len(ids) > 50 {
		ids = ids[:50]
	}
	resp, err := s.Call(ctx, target, Request{System: system, Messages: []Message{{Role: RoleUser, Text: user}}, MaxTokens: 3000},
		CallMeta{TenantID: tenantID, Actor: actor, ElementIDs: ids})
	if err != nil {
		return nil, err
	}
	out := &ReleaseNotesResult{Text: masker.Unmask(strings.TrimSpace(resp.Message.Text))}
	if resp.Stop == StopRefusal {
		out.Text = refusalText(lang)
	}
	err = s.o.DB.InTenant(ctx, tenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		out.Status, err = s.StatusFor(ctx, tx)
		return err
	})
	return out, err
}
