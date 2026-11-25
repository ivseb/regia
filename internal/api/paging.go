package api

import (
	"net/http"
	"strconv"
	"strings"
)

// pageParams legge i parametri facoltativi limit e offset (0 = non indicato). Chi non li
// passa ottiene la risposta completa di sempre: la paginazione non cambia i client esistenti.
func pageParams(r *http.Request) (limit, offset int) {
	q := r.URL.Query()
	limit, _ = strconv.Atoi(q.Get("limit"))
	offset, _ = strconv.Atoi(q.Get("offset"))
	return max(limit, 0), max(offset, 0)
}

// searchParams legge i parametri dei selettori: q (testo da cercare), limit (massimo 200,
// 0 = nessun limite) e ids (identificativi separati da virgole: restringe l'elenco a quelle voci, per leggere i nomi di quelle già scelte).
func searchParams(r *http.Request) (q string, limit int, ids []string) {
	v := r.URL.Query()
	q = strings.TrimSpace(v.Get("q"))
	limit, _ = strconv.Atoi(v.Get("limit"))
	limit = min(max(limit, 0), 200)
	ids = []string{} // mai nil: pgx lo passerebbe come NULL e cardinality(NULL) escluderebbe ogni riga
	for _, id := range strings.Split(v.Get("ids"), ",") {
		if id = strings.TrimSpace(id); id != "" && len(ids) < 200 {
			ids = append(ids, id)
		}
	}
	return q, limit, ids
}

// likeEscape rende letterale un testo da usare dentro un pattern ilike.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
