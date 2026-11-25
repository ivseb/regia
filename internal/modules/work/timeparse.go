package work

import (
	"errors"
	"math"
	"regexp"
	"strconv"
	"strings"

	"regia/internal/engine"
)

var (
	hmRe    = regexp.MustCompile(`^(\d+)\s*[h:]\s*(\d{1,2})\s*m?$`)
	hOnlyRe = regexp.MustCompile(`^(\d+(?:[.,]\d+)?)\s*h$`)
	mOnlyRe = regexp.MustCompile(`^(\d+)\s*m(?:in)?$`)
)

// ParseMinutes legge una durata scritta a mano: «1,5» e «1.5» sono ore, «1h30», «1h 30m»,
// «1:30», «45m», «2h» sono quelle che sembrano, «1g» è un giorno di otto ore. «0», «-» e il
// vuoto valgono zero (svuotare una cella del timesheet).
func ParseMinutes(s string) (int, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || s == "0" || s == "-" {
		return 0, nil
	}
	if h, err := strconv.ParseFloat(strings.Replace(s, ",", ".", 1), 64); err == nil {
		if h < 0 {
			return 0, errors.New("la durata non può essere negativa")
		}
		return int(math.Round(h * 60)), nil
	}
	if m := hmRe.FindStringSubmatch(s); m != nil {
		h, _ := strconv.Atoi(m[1])
		mm, _ := strconv.Atoi(m[2])
		if mm >= 60 {
			return 0, errors.New("i minuti vanno da 0 a 59")
		}
		return h*60 + mm, nil
	}
	if m := hOnlyRe.FindStringSubmatch(s); m != nil {
		h, _ := strconv.ParseFloat(strings.Replace(m[1], ",", ".", 1), 64)
		return int(math.Round(h * 60)), nil
	}
	if m := mOnlyRe.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n, nil
	}
	v, err := engine.ParseDuration(s, 8)
	if err != nil {
		return 0, err
	}
	return int(v), nil
}
