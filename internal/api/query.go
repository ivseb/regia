package api

import (
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
	"regia/internal/platform/auth"
	"regia/internal/platform/db"
)

// Linguaggio di query (piano, passo 16): autocompletamento e catalogo dei campi.

// querySuggest: GET /api/v1/query/suggest?q=…&pos=… (pos in unità UTF-16, come
// selectionStart del browser; assente = fine della query).
func (s *Server) querySuggest(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	q := r.URL.Query().Get("q")
	if len(q) > 4000 || !utf8.ValidString(q) {
		writeProblem(w, r, http.StatusBadRequest, "request.invalid_param", "query troppo lunga o non valida")
		return
	}
	pos := -1
	if v := r.URL.Query().Get("pos"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeProblem(w, r, http.StatusBadRequest, "request.invalid_param", "pos deve essere un numero non negativo")
			return
		}
		pos = n
	}
	var out *engine.Suggestions
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		out, err = s.eng.Suggest(r.Context(), tx, actorOf(r, p), q, pos)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	if out != nil && out.Items == nil {
		out.Items = []engine.Suggestion{} // mai null: il client legge items.length
	}
	writeJSON(w, http.StatusOK, out)
}

// queryFields: GET /api/v1/query/fields, i campi interrogabili (colonne della tabella).
func (s *Server) queryFields(w http.ResponseWriter, r *http.Request, p *auth.Principal) {
	var items []engine.FieldInfo
	err := s.db.InTenant(r.Context(), p.TenantID, db.TenantOpts{ReadOnly: true}, func(tx pgx.Tx) error {
		var err error
		items, err = s.eng.QueryFields(r.Context(), tx)
		return err
	})
	if err != nil {
		s.engineError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
