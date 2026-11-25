package api

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// Cache breve per le viste pesanti che si calcolano su tutto il tenant (cruscotto di
// portafoglio, roadmap): su 300.000 elementi costano da 0,3 a 1 s e non rientrerebbero nel
// budget di 100 ms.
//
// La voce vale al massimo swrTTL (i job SLA, earned value e promemoria non passano
// dall'API). Due modi, scelti da chi chiama con il parametro lag:
//   - lag = 0 (cruscotto): la voce vale finché nel tenant non arriva una modifica dall'API
//     (hub.generation), poi si ricalcola; un solo ricalcolo per chiave (singleflight), gli altri
//     lo aspettano. Chi scrive e subito dopo guarda il cruscotto lo vede aggiornato.
//   - lag > 0 (roadmap): dopo una modifica la voce resta servibile per lag e poi, fino a
//     swrTTL, si serve ancora subito mentre un solo ricalcolo la rinnova in background:
//     non si ricalcola a ogni scrittura e nessuno aspetta.
//
// La chiave contiene tenant e permessi di chi legge.

const (
	swrTTL = 60 * time.Second
	swrMax = 500
)

type swrEntry struct {
	gen    uint64
	strict uint64 // generazione delle sole modifiche non tollerate (vedi hub.strictGeneration)
	at     time.Time
	out    any
}

type swrCache struct {
	mu      sync.Mutex
	entries map[string]swrEntry
	flights map[string]chan struct{}
}

// get restituisce la voce se servibile e se va rinnovata in background.
func (c *swrCache) get(key string, gen uint64, lag time.Duration) (out any, refresh bool) {
	return c.getStrict(key, gen, 0, false, lag)
}

// getStrict è get con due generazioni: se useStrict, una modifica «non tollerata» (strict
// diverso da quello della voce) la rende subito non servibile, qualunque sia lag; le
// modifiche tollerate (gen) restano servibili per lag.
func (c *swrCache) getStrict(key string, gen, strict uint64, useStrict bool, lag time.Duration) (out any, refresh bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	age := time.Since(e.at)
	if !ok || age > swrTTL {
		return nil, false
	}
	if useStrict && e.strict != strict {
		return nil, false
	}
	if e.gen == gen {
		return e.out, false
	}
	if lag <= 0 {
		return nil, false
	}
	return e.out, age > lag
}

func (c *swrCache) put(key string, gen uint64, out any) { c.putStrict(key, gen, 0, out) }

func (c *swrCache) putStrict(key string, gen, strict uint64, out any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil || len(c.entries) > swrMax {
		c.entries = map[string]swrEntry{}
	}
	c.entries[key] = swrEntry{gen: gen, strict: strict, at: time.Now(), out: out}
}

// begin segna il ricalcolo di key: se ne è già in corso uno restituisce il suo canale, che
// si chiude a ricalcolo finito.
func (c *swrCache) begin(key string) (done chan struct{}, leader bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ch, ok := c.flights[key]; ok {
		return ch, false
	}
	if c.flights == nil {
		c.flights = map[string]chan struct{}{}
	}
	ch := make(chan struct{})
	c.flights[key] = ch
	return ch, true
}

func (c *swrCache) end(key string, ch chan struct{}) {
	c.mu.Lock()
	delete(c.flights, key)
	c.mu.Unlock()
	close(ch)
}

// serve risponde con la voce in cache o la calcola in una transazione del tenant.
func (c *swrCache) serve(s *Server, w http.ResponseWriter, r *http.Request, p *auth.Principal, key string, readOnly bool, lag time.Duration,
	compute func(ctx context.Context, tx pgx.Tx) (any, error)) {
	c.serveMode(s, w, r, p, key, readOnly, lag, false, compute)
}

// serveMode è serve; con soft il ritardo lag vale solo per le modifiche tollerate (le sole
// che il hub non conta come strict): ogni altra modifica invalida subito, come lag = 0.
func (c *swrCache) serveMode(s *Server, w http.ResponseWriter, r *http.Request, p *auth.Principal, key string, readOnly bool, lag time.Duration, soft bool,
	compute func(ctx context.Context, tx pgx.Tx) (any, error)) {
	gen := s.events.generation(p.TenantID)
	strict := s.events.strictGeneration(p.TenantID)
	get := func() (any, bool) { return c.getStrict(key, gen, strict, soft, lag) }
	put := func(out any) { c.putStrict(key, gen, strict, out) }
	if out, refresh := get(); out != nil {
		if refresh {
			if ch, leader := c.begin(key); leader {
				go func() {
					defer c.end(key, ch)
					ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
					defer cancel()
					err := s.db.InTenant(ctx, p.TenantID, db.TenantOpts{ReadOnly: readOnly}, func(tx pgx.Tx) error {
						out, err := compute(ctx, tx)
						if err == nil {
							put(out)
						}
						return err
					})
					if err != nil {
						s.log.Warn("rinnovo della vista in cache fallito", "errore", err)
					}
				}()
			}
		}
		w.Header().Set("X-Cache", "hit")
		writeJSON(w, http.StatusOK, out)
		return
	}
	// Nessuna voce: un solo ricalcolo per chiave, gli altri aspettano e leggono la cache.
	ch, leader := c.begin(key)
	if !leader {
		select {
		case <-ch:
		case <-r.Context().Done():
			return
		}
		if out, _ := get(); out != nil {
			w.Header().Set("X-Cache", "hit")
			writeJSON(w, http.StatusOK, out)
			return
		}
		ch, leader = c.begin(key)
	}
	if leader {
		defer c.end(key, ch)
	}
	tenantJSON(s, w, r, p, readOnly, http.StatusOK, func(tx pgx.Tx) (any, error) {
		out, err := compute(r.Context(), tx)
		if err == nil {
			put(out)
		}
		return out, err
	})
}
