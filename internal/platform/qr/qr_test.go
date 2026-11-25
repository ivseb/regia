package qr

import (
	"strings"
	"testing"
)

// La decodifica è stata verificata con OpenCV (QRCodeDetectorAruco) per lunghezze da 28 a
// 403 byte, versioni da 2 a 15; qui si controllano versione, struttura e formato.
func TestEncodeStructure(t *testing.T) {
	cases := map[int]int{14: 1, 26: 2, 120: 7, 212: 10, 400: 15}
	for n, ver := range cases {
		c, err := Encode([]byte(strings.Repeat("x", n)))
		if err != nil {
			t.Fatal(err)
		}
		if c.Size != ver*4+17 {
			t.Errorf("%d byte: lato %d, atteso %d (versione %d)", n, c.Size, ver*4+17, ver)
		}
		// Riquadri di posizione: anello esterno scuro, separatore chiaro, centro scuro.
		for _, o := range [][2]int{{0, 0}, {c.Size - 7, 0}, {0, c.Size - 7}} {
			x, y := o[0], o[1]
			if !c.Modules[y][x] || !c.Modules[y+3][x+3] || c.Modules[y+1][x+1] {
				t.Errorf("%d byte: riquadro di posizione in %v errato", n, o)
			}
		}
		// Modulo sempre scuro accanto al riquadro in basso a sinistra.
		if !c.Modules[c.Size-8][8] {
			t.Errorf("%d byte: manca il modulo scuro fisso", n)
		}
		// Le due copie del formato devono coincidere.
		var a, b []bool
		for i := 0; i <= 5; i++ {
			a = append(a, c.Modules[i][8])
		}
		for i := 0; i < 6; i++ {
			b = append(b, c.Modules[8][c.Size-1-i])
		}
		for i := range a {
			if a[i] != b[i] {
				t.Fatalf("%d byte: copie del formato diverse", n)
			}
		}
	}
	if _, err := Encode(make([]byte, 500)); err != ErrTooLong {
		t.Fatalf("testo troppo lungo: %v", err)
	}
	svg := mustSVG(t)
	if !strings.HasPrefix(svg, "<svg") || !strings.Contains(svg, `fill="#000000"`) {
		t.Fatalf("SVG: %.80s", svg)
	}
}

func mustSVG(t *testing.T) string {
	c, err := Encode([]byte("otpauth://totp/Regia:prova?secret=ABC"))
	if err != nil {
		t.Fatal(err)
	}
	return c.SVG(4)
}
