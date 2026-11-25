package engine

import "testing"

func TestNumRange(t *testing.T) {
	cases := []struct {
		name         string
		col          string
		vals         []bval
		present      bool
		ok           bool
		lo, hi       any
		loInc, hiInc bool
	}{
		{"maggiore", "v.v_num", []bval{{op: "gt", a: 990}}, false, true, 990, nil, false, false},
		{"minore o uguale", "v.v_num", []bval{{op: "le", a: 5}}, false, true, nil, 5, false, true},
		{"intervallo", "v.v_num", []bval{{op: "range", a: 100, bb: 120, hasA: true, hasB: true}}, false, true, 100, 120, true, true},
		{"intervallo aperto a destra", "v.v_num", []bval{{op: "range", a: 100, hasA: true}}, false, true, 100, nil, true, false},
		{"uguale", "v.v_num", []bval{{op: "eq", a: 7}}, false, true, 7, 7, true, true},
		{"altra colonna", "v.v_ts", []bval{{op: "gt", a: 1}}, false, false, nil, nil, false, false},
		{"due condizioni", "v.v_num", []bval{{op: "gt", a: 1}, {op: "lt", a: 9}}, false, false, nil, nil, false, false},
		{"presente", "v.v_num", []bval{{op: "gt", a: 1}}, true, false, nil, nil, false, false},
	}
	for _, c := range cases {
		lo, loInc, hi, hiInc, ok := numRange(c.col, c.vals, c.present)
		if ok != c.ok || (ok && (lo != c.lo || hi != c.hi || loInc != c.loInc || hiInc != c.hiInc)) {
			t.Errorf("%s: trovato lo=%v(%v) hi=%v(%v) ok=%v", c.name, lo, loInc, hi, hiInc, ok)
		}
	}
}
