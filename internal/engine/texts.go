package engine

import (
	"context"
	"strings"

	"regia/internal/platform/i18n"
)

// Testi del motore nelle due lingue. La lingua è quella di chi chiama l'API, legata al
// contesto con i18n.WithLang (vedi internal/api: langCtx); senza, vale l'italiano.
func init() {
	i18n.Register(map[string]i18n.Msg{
		"engine.system":      {It: "Sistema", En: "System"},
		"engine.role_prefix": {It: "Ruolo", En: "Role"},
	})
}

// localizeFacts porta nella lingua del contesto i nomi dei fatti descritti dal motore e dai
// moduli. I campi personalizzati e i nomi dei ruoli sono dati del tenant: restano come sono
// (un ruolo di sistema ha invece il suo nome inglese, se il tenant non l'ha già cambiato).
func localizeFacts(ctx context.Context, facts []FactInfo) []FactInfo {
	lang := i18n.Lang(ctx)
	if lang == i18n.Default {
		return facts
	}
	for i := range facts {
		f := &facts[i]
		switch f.Source {
		case "campo", "elemento":
			continue
		case "ruolo":
			rest, ok := strings.CutPrefix(f.Name, "Ruolo ")
			if !ok {
				continue
			}
			base, suffix, _ := strings.Cut(rest, " (")
			if suffix != "" {
				suffix = " (" + suffix
			}
			f.Name = i18n.T(lang, "engine.role_prefix") + " " + i18n.Name(lang, base) + suffix
		default:
			f.Name = i18n.Name(lang, f.Name)
		}
	}
	return facts
}

// localizeActions porta nella lingua del contesto nomi, descrizioni e parametri delle azioni.
func localizeActions(ctx context.Context, in []ActionSchema) []ActionSchema {
	lang := i18n.Lang(ctx)
	if lang == i18n.Default {
		return in
	}
	out := make([]ActionSchema, len(in))
	for i, a := range in {
		a.Name = i18n.Name(lang, a.Name)
		a.Description = i18n.Name(lang, a.Description)
		fields := make([]ActionField, len(a.Fields))
		for j, f := range a.Fields {
			f.Name = i18n.Name(lang, f.Name)
			f.Help = i18n.Name(lang, f.Help)
			fields[j] = f
		}
		a.Fields = fields
		out[i] = a
	}
	return out
}
