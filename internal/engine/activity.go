package engine

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/i18n"
)

// Activity è una voce della storia di un elemento.
type Activity struct {
	ID        int64     `json:"id"`
	ActorID   *string   `json:"actor_id"`
	ActorName string    `json:"actor_name"`
	Kind      string    `json:"kind"`
	FieldKey  *string   `json:"field_key"`
	Added     any       `json:"added"`
	Removed   any       `json:"removed"`
	CreatedAt time.Time `json:"created_at"`
	// Per i collegamenti: il nome leggibile della relazione nel verso della voce («usa»,
	// «usata da») e l'altro elemento («ARC-3 Backend API clienti»), se chi legge può vederlo.
	RelationLabel string `json:"relation_label,omitempty"`
	Other         string `json:"other,omitempty"`
}

func addActivity(ctx context.Context, tx pgx.Tx, elementID, actorID, kind, field string, added, removed any) error {
	_, err := tx.Exec(ctx, `insert into activities (tenant_id, element_id, actor_id, kind, field_key, added, removed)
		values (app_tenant(), $1, $2, $3, $4, $5, $6)`,
		elementID, nullable(actorID), kind, nullable(field), jsonOrNil(added), jsonOrNil(removed))
	return err
}

// AddActivity è la versione per i moduli (es. decisioni di approvazione nella storia).
func AddActivity(ctx context.Context, tx pgx.Tx, elementID, actorID, kind, field string, added, removed any) error {
	return addActivity(ctx, tx, elementID, actorID, kind, field, added, removed)
}

// jsonOrNil prepara un valore per le colonne jsonb della storia. I valori semplici (testi,
// numeri, elenchi dei campi personalizzati) si codificano qui: pgx passerebbe un testo Go
// così com'è, come JSON già scritto.
func jsonOrNil(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte, json.RawMessage:
		return x
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

// History restituisce la storia dell'elemento, dalla voce più vecchia; after = ultimo id già letto.
func History(ctx context.Context, tx pgx.Tx, elementID string, after int64, limit int) ([]Activity, error) {
	rows, err := tx.Query(ctx, `
		select a.id, a.actor_id, coalesce(m.display_name, ''), a.kind, a.field_key, a.added, a.removed, a.created_at
		from activities a left join members m on m.tenant_id = a.tenant_id and m.id = a.actor_id
		where a.element_id = $1 and a.id > $2
		order by a.id limit $3`, elementID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Activity{}
	system := i18n.Tc(ctx, "engine.system")
	for rows.Next() {
		var a Activity
		var added, removed []byte
		if err := rows.Scan(&a.ID, &a.ActorID, &a.ActorName, &a.Kind, &a.FieldKey, &added, &removed, &a.CreatedAt); err != nil {
			return nil, err
		}
		if a.ActorName == "" {
			a.ActorName = system
		}
		if len(added) > 0 {
			_ = json.Unmarshal(added, &a.Added)
		}
		if len(removed) > 0 {
			_ = json.Unmarshal(removed, &a.Removed)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// Describe arricchisce le voci di collegamento con il nome leggibile della relazione e
// dell'altro elemento: la storia non deve mostrare chiavi interne come «uses_app.inverse».
// L'altro elemento si nomina solo se actor può leggerlo.
func (s *Service) Describe(ctx context.Context, tx pgx.Tx, actor Actor, items []Activity) error {
	lang := i18n.Lang(ctx)
	names := map[string][2]string{}
	rows, err := tx.Query(ctx, "select key, outward_name, inward_name, is_system from relation_types")
	if err != nil {
		return err
	}
	for rows.Next() {
		var k, out, in string
		var system bool
		if err := rows.Scan(&k, &out, &in, &system); err != nil {
			rows.Close()
			return err
		}
		if system { // i nomi delle relazioni di sistema hanno la traduzione; quelli del tenant restano suoi
			out, in = i18n.Name(lang, out), i18n.Name(lang, in)
		}
		names[k] = [2]string{out, in}
	}
	rows.Close()
	refs := map[string]string{}
	ref := func(id string) (string, error) {
		if v, ok := refs[id]; ok {
			return v, nil
		}
		refs[id] = ""
		if ok, err := s.CanRead(ctx, tx, actor, id); err != nil || !ok {
			return "", err
		}
		var v string
		err := tx.QueryRow(ctx, `select coalesce(s.key || '-' || e.number, '') || ' ' || e.title from elements e
			join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id where e.id = $1`, id).Scan(&v)
		if err == pgx.ErrNoRows {
			return "", nil
		}
		refs[id] = v
		return v, err
	}
	for i := range items {
		a := &items[i]
		if a.Kind != "relation" || a.FieldKey == nil {
			continue
		}
		key, inverse := *a.FieldKey, false
		if k, ok := strings.CutSuffix(key, ".inverse"); ok {
			key, inverse = k, true
		}
		key = strings.TrimSuffix(key, ".attrs")
		if n, ok := names[key]; ok {
			a.RelationLabel = n[0]
			if inverse {
				a.RelationLabel = n[1]
			}
		}
		for _, side := range []any{a.Added, a.Removed} {
			m, _ := side.(map[string]any)
			for _, f := range []string{"target", "source"} {
				if id, _ := m[f].(string); id != "" && a.Other == "" {
					other, err := ref(id)
					if err != nil {
						return err
					}
					a.Other = other
				}
			}
		}
	}
	return nil
}
