package profile

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Path è un percorso semplice dentro un documento JSON o YAML, senza un linguaggio:
//
//	release.digest                  chiavi separate da punti
//	checks[0].name                  indice di un elenco
//	annotations["ci/version"]       chiave con caratteri speciali (punti, barre)
//	=prod                           valore fisso
//
// Un Path può avere più alternative (in JSON un elenco di stringhe): vale la prima che
// porta a un valore non vuoto.
type Path []string

// UnmarshalJSON accetta una stringa o un elenco di stringhe.
func (p *Path) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		if one == "" {
			*p = nil
		} else {
			*p = Path{one}
		}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return fmt.Errorf("un percorso è una stringa o un elenco di stringhe")
	}
	*p = Path(many)
	return nil
}

// MarshalJSON scrive una stringa quando c'è una sola alternativa.
func (p Path) MarshalJSON() ([]byte, error) {
	if len(p) == 1 {
		return json.Marshal(p[0])
	}
	return json.Marshal([]string(p))
}

// segment è un passo del percorso: una chiave o un indice.
type segment struct {
	key   string
	index int
	isIdx bool
}

// parsePath scompone un'alternativa del percorso.
func parsePath(s string) ([]segment, error) {
	var out []segment
	i := 0
	n := len(s)
	expectKey := true
	for i < n {
		switch {
		case s[i] == '.':
			if expectKey {
				return nil, fmt.Errorf("punto inatteso alla posizione %d", i+1)
			}
			i++
			expectKey = true
			if i == n {
				return nil, fmt.Errorf("il percorso finisce con un punto")
			}
		case s[i] == '[':
			end := strings.IndexByte(s[i:], ']')
			if end < 0 {
				return nil, fmt.Errorf("parentesi quadra non chiusa")
			}
			inner := s[i+1 : i+end]
			if len(inner) >= 2 && (inner[0] == '"' || inner[0] == '\'') && inner[len(inner)-1] == inner[0] {
				out = append(out, segment{key: inner[1 : len(inner)-1]})
			} else {
				k, err := strconv.Atoi(inner)
				if err != nil || k < 0 {
					return nil, fmt.Errorf("tra parentesi quadre serve un indice (0, 1, …) o una chiave tra virgolette")
				}
				out = append(out, segment{index: k, isIdx: true})
			}
			i += end + 1
			expectKey = false
		default:
			if !expectKey {
				return nil, fmt.Errorf("manca un punto alla posizione %d", i+1)
			}
			j := i
			for j < n && s[j] != '.' && s[j] != '[' {
				j++
			}
			out = append(out, segment{key: s[i:j]})
			i = j
			expectKey = false
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("percorso vuoto")
	}
	return out, nil
}

// Check verifica la sintassi di tutte le alternative.
func (p Path) Check() error {
	for _, alt := range p {
		if strings.HasPrefix(alt, "=") {
			continue
		}
		if _, err := parsePath(strings.TrimSpace(alt)); err != nil {
			return fmt.Errorf("%q: %v", alt, err)
		}
	}
	return nil
}

// Lookup restituisce il valore di un'alternativa del percorso.
func lookup(doc any, alt string) (any, bool) {
	if strings.HasPrefix(alt, "=") {
		return alt[1:], true
	}
	segs, err := parsePath(strings.TrimSpace(alt))
	if err != nil {
		return nil, false
	}
	cur := doc
	for _, sg := range segs {
		switch v := cur.(type) {
		case map[string]any:
			if sg.isIdx {
				return nil, false
			}
			x, ok := v[sg.key]
			if !ok {
				return nil, false
			}
			cur = x
		case []any:
			if !sg.isIdx || sg.index >= len(v) {
				return nil, false
			}
			cur = v[sg.index]
		default:
			return nil, false
		}
	}
	return cur, cur != nil
}

// Value restituisce il primo valore non vuoto tra le alternative.
func (p Path) Value(doc any) (any, bool) {
	for _, alt := range p {
		v, ok := lookup(doc, alt)
		if !ok || isEmpty(v) {
			continue
		}
		return v, true
	}
	return nil, false
}

func isEmpty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(x) == ""
	case []any:
		return len(x) == 0
	case map[string]any:
		return len(x) == 0
	}
	return false
}

// Text restituisce il valore come testo (numeri e booleani compresi).
func (p Path) Text(doc any) string {
	v, ok := p.Value(doc)
	if !ok {
		return ""
	}
	return scalarString(v)
}

func scalarString(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case bool:
		return strconv.FormatBool(x)
	case time.Time:
		return x.UTC().Format(time.RFC3339)
	case json.Number:
		return x.String()
	}
	return ""
}

// Int restituisce il valore come intero; ok è falso se manca o non è un numero.
func (p Path) Int(doc any) (int, bool) {
	v, ok := p.Value(doc)
	if !ok {
		return 0, false
	}
	switch x := v.(type) {
	case float64:
		return int(x), true
	case int:
		return x, true
	case int64:
		return int(x), true
	case uint64:
		return int(x), true
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(x))
		return n, err == nil
	case []any:
		return len(x), true // un elenco vale per il numero dei suoi elementi
	}
	return 0, false
}

// Time restituisce il valore come istante (RFC 3339, oppure una data AAAA-MM-GG).
func (p Path) Time(doc any) (time.Time, bool) {
	v, ok := p.Value(doc)
	if !ok {
		return time.Time{}, false
	}
	if t, ok := v.(time.Time); ok {
		return t.UTC(), true
	}
	s := scalarString(v)
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), true
		}
	}
	return time.Time{}, false
}
