package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"regia/internal/platform/auth"
)

// Aggiornamenti in tempo reale (Server-Sent Events). Dopo ogni modifica riuscita
// nell'API si avvisano i client collegati allo stesso tenant, che ricaricano i dati
// visibili. L'evento non contiene dati, solo "qualcosa è cambiato qui": i permessi
// restano quelli delle normali letture.
//
// Il broker è in memoria: basta con un'istanza; con più istanze servirà LISTEN/NOTIFY.

type Event struct {
	Kind  string `json:"kind"`  // changed
	Path  string `json:"path"`  // risorsa modificata, es. /api/v1/elements/…/schedule
	Actor string `json:"actor"` // membro che ha fatto la modifica
	At    int64  `json:"at"`
	// Soft: modifica ad alta frequenza che le cache con ritardo tollerato (cruscotto) possono
	// non vedere subito; non viaggia verso i client.
	Soft bool `json:"-"`
}

type hub struct {
	mu     sync.Mutex
	subs   map[string]map[chan Event]struct{} // tenant → abbonati
	closed bool
	done   chan struct{}
	// gen conta le modifiche per tenant: invalida le cache in memoria (cruscotto, report.go).
	gen map[string]uint64
	// strict conta le modifiche non tollerabili con ritardo (tutte tranne quelle Soft).
	strict map[string]uint64
}

func newHub() *hub {
	return &hub{subs: map[string]map[chan Event]struct{}{}, done: make(chan struct{}), gen: map[string]uint64{}, strict: map[string]uint64{}}
}

func (h *hub) subscribe(tenant string) (chan Event, func()) {
	ch := make(chan Event, 16)
	h.mu.Lock()
	if h.subs[tenant] == nil {
		h.subs[tenant] = map[chan Event]struct{}{}
	}
	h.subs[tenant][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs[tenant], ch)
		if len(h.subs[tenant]) == 0 {
			delete(h.subs, tenant)
		}
		h.mu.Unlock()
	}
}

func (h *hub) publish(tenant string, ev Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.gen[tenant]++
	if !ev.Soft {
		h.strict[tenant]++
	}
	for ch := range h.subs[tenant] {
		select {
		case ch <- ev:
		default: // client lento: perde l'evento, ricaricherà al prossimo
		}
	}
}

// generation è il numero di modifiche pubblicate nel tenant.
func (h *hub) generation(tenant string) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.gen[tenant]
}

// strictGeneration è il numero di modifiche pubblicate nel tenant che non sono Soft.
func (h *hub) strictGeneration(tenant string) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.strict[tenant]
}

// Close chiude i flussi aperti (allo spegnimento del server).
func (h *hub) Close() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.closed {
		h.closed = true
		close(h.done)
	}
}

// Close termina i flussi di eventi aperti: da chiamare prima di Shutdown.
func (s *Server) Close() { s.events.Close() }

func (s *Server) streamEvents(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{}) // il flusso resta aperto oltre il WriteTimeout del server
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	ch, cancel := s.events.subscribe(p.TenantID)
	defer cancel()
	fmt.Fprint(w, "retry: 3000\n\n")
	_ = rc.Flush()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.events.done:
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
		case ev := <-ch:
			b, _ := json.Marshal(ev)
			fmt.Fprintf(w, "data: %s\n\n", b)
		}
		if err := rc.Flush(); err != nil {
			return
		}
	}
}

type recorder struct {
	http.ResponseWriter
	status int
}

func (r *recorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// notify pubblica un evento dopo ogni modifica riuscita nell'API di un tenant.
func (s *Server) notify(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			next.ServeHTTP(w, r)
			return
		}
		rec := &recorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		if rec.status >= 300 || !strings.HasPrefix(r.URL.Path, "/api/v1/") || strings.HasPrefix(r.URL.Path, "/api/v1/auth/") ||
			strings.HasPrefix(r.URL.Path, "/api/v1/me") || strings.HasPrefix(r.URL.Path, "/api/v1/conditions/") ||
			strings.HasSuffix(r.URL.Path, "/preview") || r.URL.Query().Get("dry_run") == "true" ||
			// Letture delle notifiche e osservatori riguardano solo chi li modifica.
			strings.HasPrefix(r.URL.Path, "/api/v1/notifications") || strings.HasSuffix(r.URL.Path, "/watch") ||
			// Server MCP (pubblica da sé dopo una conferma applicata) e domande sul grafo: letture in POST.
			r.URL.Path == MCPPath || r.URL.Path == "/api/v1/ai/graph" ||
			// Assistente nella scheda: riassunto, mancanze, bozze e analisi alla creazione sono letture.
			r.URL.Path == "/api/v1/ai/card" || r.URL.Path == "/api/v1/ai/card/triage" ||
			// Note di rilascio per il business: una proposta da leggere, non salvata.
			r.URL.Path == "/api/v1/ai/release-notes" ||
			// GRC assistito: proposte e bozze sono letture (le decisioni e i salvataggi pubblicano).
			r.URL.Path == "/api/v1/ai/grc/map" || r.URL.Path == "/api/v1/ai/grc/draft" || r.URL.Path == "/api/v1/ai/grc/notice" ||
			// Wiki personale: è della persona (solo la promozione a Knowledge cambia le viste degli altri).
			(strings.HasPrefix(r.URL.Path, "/api/v1/wiki") && !strings.HasSuffix(r.URL.Path, "/promote")) ||
			// Proposte dell'AI per un questionario: le vede solo chi le chiede (le decisioni pubblicano).
			(strings.HasPrefix(r.URL.Path, "/api/v1/reviews/") && strings.HasSuffix(r.URL.Path, "/ai-proposals")) ||
			// Salvataggio automatico dei questionari e controllo dei modelli: non cambiano le viste degli altri.
			(strings.HasPrefix(r.URL.Path, "/api/v1/reviews/") && strings.HasSuffix(r.URL.Path, "/answers")) || r.URL.Path == "/api/v1/review-config/templates/validate" {
			return
		}
		// Knowledge: il blocco di modifica si rinnova ogni minuto e non cambia le viste degli altri.
		if strings.HasPrefix(r.URL.Path, "/api/v1/kb/") && strings.HasSuffix(r.URL.Path, "/lock") {
			return
		}
		if p := principal(r.Context()); p != nil && p.TenantID != "" {
			// Creazione e passaggi di fase delle richieste sono le scritture più frequenti: il cruscotto
			// può restare indietro di pochi secondi su di esse (non su costi, budget, report, valutazioni...).
			soft := r.Method == http.MethodPost && (r.URL.Path == "/api/v1/demand/requests" ||
				(strings.HasPrefix(r.URL.Path, "/api/v1/elements/") && strings.HasSuffix(r.URL.Path, "/transitions")))
			s.events.publish(p.TenantID, Event{Kind: "changed", Path: r.URL.Path, Actor: p.MemberID, At: time.Now().UnixMilli(), Soft: soft})
		}
	})
}
