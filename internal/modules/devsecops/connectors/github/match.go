package github

import (
	"fmt"
	"strings"
	"unicode"
)

// Proposta di associazione repository → applicazione del catalogo, con un confronto
// semplice e spiegabile (la conferma resta a una persona):
//
//	1,00  il nome del repository è il codice dell'applicazione          (crm → CRM)
//	0,97  stesso nome a meno di maiuscole e separatori                  (Ordini API → ordini-api)
//	0,92  il codice dell'applicazione è una parola del nome             (crm-orders-api → CRM)
//	0,90  stesso nome a meno delle parole tecniche (api, web, service…) (ordini-api → Ordini)
//	0,60…0,90  parole in comune o nomi molto simili (errori di battitura)
//
// Sotto 0,60 non si propone nulla.

// App è un'applicazione del catalogo, per il confronto.
type App struct {
	ID   string
	Key  string
	Name string
	Code string
}

// Ref è il riferimento all'applicazione nel contratto: il codice, altrimenti la chiave.
func (a App) Ref() string {
	if strings.TrimSpace(a.Code) != "" {
		return strings.TrimSpace(a.Code)
	}
	return a.Key
}

// Suggestion è la proposta per un repository.
type Suggestion struct {
	App    App
	Score  float64
	Reason string
}

const minScore = 0.6

// Parole tecniche che non distinguono un'applicazione da un'altra.
var techWords = map[string]bool{
	"api": true, "apis": true, "service": true, "services": true, "svc": true, "app": true, "web": true, "ui": true, "fe": true,
	"be": true, "backend": true, "frontend": true, "server": true, "client": true, "core": true, "lib": true, "ms": true,
	"microservice": true, "worker": true, "job": true, "jobs": true, "batch": true, "portal": true, "site": true, "mobile": true,
	"applicazione": true, "servizio": true, "il": true, "la": true, "di": true, "the": true, "of": true,
}

func tokens(s string) []string {
	var out []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	prevLower := false
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			// camelCase: ordiniApi → ordini, api
			if unicode.IsUpper(r) && prevLower {
				flush()
			}
			cur.WriteRune(unicode.ToLower(foldAccent(r)))
			prevLower = unicode.IsLower(r) || unicode.IsDigit(r)
		default:
			flush()
			prevLower = false
		}
	}
	flush()
	return out
}

func foldAccent(r rune) rune {
	switch r {
	case 'à', 'á', 'À', 'Á':
		return 'a'
	case 'è', 'é', 'È', 'É':
		return 'e'
	case 'ì', 'í', 'Ì', 'Í':
		return 'i'
	case 'ò', 'ó', 'Ò', 'Ó':
		return 'o'
	case 'ù', 'ú', 'Ù', 'Ú':
		return 'u'
	}
	return r
}

func core(ts []string) []string {
	var out []string
	for _, t := range ts {
		if !techWords[t] {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return ts
	}
	return out
}

// Suggest propone l'applicazione più vicina a un repository (nil se nessuna supera la soglia).
func Suggest(repoName string, apps []App) *Suggestion {
	rt := tokens(repoName)
	if len(rt) == 0 {
		return nil
	}
	rJoined := strings.Join(rt, "")
	rCore := core(rt)
	rCoreJoined := strings.Join(rCore, "")
	var best *Suggestion
	tie := false
	for _, a := range apps {
		score, reason := 0.0, ""
		consider := func(sc float64, why string) {
			if sc > score {
				score, reason = sc, why
			}
		}
		code := strings.Join(tokens(a.Code), "")
		if len(code) >= 2 {
			if code == rJoined {
				consider(1.0, fmt.Sprintf("il nome coincide con il codice «%s»", a.Code))
			}
			for _, t := range rt {
				if t == code {
					consider(0.92, fmt.Sprintf("il codice «%s» è nel nome", a.Code))
				}
			}
		}
		at := tokens(a.Name)
		if len(at) > 0 {
			if strings.Join(at, "") == rJoined {
				consider(0.97, "stesso nome")
			}
			aCore := core(at)
			aCoreJoined := strings.Join(aCore, "")
			if aCoreJoined == rCoreJoined {
				consider(0.9, "stesso nome, a meno delle parole tecniche")
			}
			if j := jaccard(rCore, aCore); j >= 0.5 {
				consider(0.6+0.3*j, fmt.Sprintf("parole in comune con «%s»", a.Name))
			}
			if len(aCoreJoined) >= 4 && len(rCoreJoined) >= 4 {
				if r := similarity(aCoreJoined, rCoreJoined); r >= 0.75 {
					consider(0.6+0.25*(r-0.75)/0.25, fmt.Sprintf("nome simile a «%s» (%.0f%%)", a.Name, r*100))
				}
				if strings.Contains(rCoreJoined, aCoreJoined) && len(aCoreJoined) >= 5 {
					consider(0.75, fmt.Sprintf("il nome contiene «%s»", a.Name))
				}
			}
		}
		if score < minScore {
			continue
		}
		switch {
		case best == nil || score > best.Score+1e-9:
			best = &Suggestion{App: a, Score: score, Reason: reason}
			tie = false
		case abs(score-best.Score) < 1e-9:
			tie = true
		}
	}
	if best == nil {
		return nil
	}
	if tie {
		best.Score -= 0.1 // più candidati con lo stesso punteggio: da guardare con attenzione
		best.Reason += " (più applicazioni simili)"
		if best.Score < minScore {
			return nil
		}
	}
	best.Score = float64(int(best.Score*100+0.5)) / 100
	return best
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func jaccard(a, b []string) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	set := map[string]int{}
	for _, x := range a {
		set[x] |= 1
	}
	for _, x := range b {
		set[x] |= 2
	}
	inter, union := 0, 0
	for _, v := range set {
		union++
		if v == 3 {
			inter++
		}
	}
	return float64(inter) / float64(union)
}

// similarity è 1 - distanza di Levenshtein / lunghezza massima.
func similarity(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	if len(ra) == 0 || len(rb) == 0 {
		return 0
	}
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	d := prev[len(rb)]
	return 1 - float64(d)/float64(max(len(ra), len(rb)))
}
