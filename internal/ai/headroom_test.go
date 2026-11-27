package ai

import "testing"

// Il ragionamento conta nei token di uscita: le richieste brevi hanno un margine, quelle
// approfondite uno più largo, senza superare il tetto delle chiamate senza streaming.
func TestThinkingHeadroom(t *testing.T) {
	for _, c := range []struct {
		in   int
		deep bool
		want int
	}{
		{0, true, 0},
		{2000, false, 4000},
		{2000, true, 10000},
		{8000, false, 10000},
		{8000, true, 16000},
		{16000, true, 16000},
	} {
		if got := thinkingHeadroom(c.in, c.deep); got != c.want {
			t.Errorf("thinkingHeadroom(%d, %v) = %d, voluto %d", c.in, c.deep, got, c.want)
		}
	}
}
