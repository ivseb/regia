package finance

import "testing"

func TestParseAmount(t *testing.T) {
	ok := []struct {
		in, dec string
		want    int64
	}{
		{"1.234,56", ",", 123456},
		{"1234,5", "", 123450},
		{"€ 12", ",", 1200},
		{"(50,00)", ",", -5000},
		{"-7,125", ",", -713},
		{"1,234.56", ".", 123456},
		{"0.005", ".", 1},
	}
	for _, c := range ok {
		got, err := parseAmount(c.in, c.dec)
		if err != nil || got != c.want {
			t.Errorf("parseAmount(%q, %q) = %d, %v; atteso %d", c.in, c.dec, got, err, c.want)
		}
	}
	for _, in := range []string{"", "abc", "0,00", "1,2,3"} {
		if _, err := parseAmount(in, ","); err == nil {
			t.Errorf("parseAmount(%q) senza errore", in)
		}
	}
}
