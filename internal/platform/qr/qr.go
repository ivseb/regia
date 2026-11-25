// Package qr è un piccolo encoder di codici QR (ISO/IEC 18004) per l'attivazione del
// TOTP: modalità byte, correzione d'errore M, versioni da 1 a 15 (fino a 412 byte),
// scelta della maschera con le penalità dello standard. Niente servizi esterni: il QR
// del segreto non deve mai lasciare il server.
package qr

import (
	"errors"
	"fmt"
	"strings"
)

// Code è la matrice dei moduli: Modules[y][x] true = scuro.
type Code struct {
	Size    int
	Modules [][]bool
}

// ErrTooLong: il testo non entra nella versione 15.
var ErrTooLong = errors.New("testo troppo lungo per il codice QR")

const maxVersion = 15

// Livello M: codeword di correzione per blocco e numero di blocchi, per versione.
var eccPerBlock = [maxVersion + 1]int{0, 10, 16, 26, 18, 24, 16, 18, 22, 22, 26, 30, 22, 22, 24, 24}
var numBlocks = [maxVersion + 1]int{0, 1, 1, 1, 2, 2, 4, 4, 4, 5, 5, 5, 8, 9, 9, 10}

// Bit del livello M nel formato.
const eclBits = 0

// Encode codifica i byte in un codice QR.
func Encode(data []byte) (*Code, error) {
	ver := 0
	for v := 1; v <= maxVersion; v++ {
		if 4+ccBits(v)+8*len(data) <= dataCodewords(v)*8 {
			ver = v
			break
		}
	}
	if ver == 0 {
		return nil, ErrTooLong
	}
	// Flusso di bit: modalità byte (0100), lunghezza, dati, terminatore, riempimento.
	var bb bitBuf
	bb.add(0b0100, 4)
	bb.add(len(data), ccBits(ver))
	for _, b := range data {
		bb.add(int(b), 8)
	}
	capBits := dataCodewords(ver) * 8
	bb.add(0, min(4, capBits-len(bb)))
	bb.add(0, (8-len(bb)%8)%8)
	for pad := 0xEC; len(bb) < capBits; pad ^= 0xEC ^ 0x11 {
		bb.add(pad, 8)
	}
	words := make([]byte, len(bb)/8)
	for i, bit := range bb {
		if bit {
			words[i/8] |= 1 << (7 - i%8)
		}
	}
	all := addECC(words, ver)

	c := newMatrix(ver)
	c.drawCodewords(all)
	// Maschera con la penalità più bassa.
	best, bestScore := 0, -1
	for m := 0; m < 8; m++ {
		c.applyMask(m)
		c.drawFormat(m)
		if s := c.penalty(); bestScore < 0 || s < bestScore {
			best, bestScore = m, s
		}
		c.applyMask(m) // XOR: la seconda applicazione la toglie
	}
	c.applyMask(best)
	c.drawFormat(best)
	return &Code{Size: c.size, Modules: c.mod}, nil
}

// SVG disegna il codice con 4 moduli di margine bianco (scuro su chiaro in ogni tema:
// i lettori di QR non leggono i colori invertiti).
func (c *Code) SVG(moduleSize int) string {
	n := c.Size + 8
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" width="%d" height="%d" shape-rendering="crispEdges">`, n, n, n*moduleSize, n*moduleSize)
	fmt.Fprintf(&b, `<rect width="%d" height="%d" fill="#ffffff"/><path fill="#000000" d="`, n, n)
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if c.Modules[y][x] {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+4, y+4)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return b.String()
}

func ccBits(ver int) int {
	if ver <= 9 {
		return 8
	}
	return 16
}

func rawModules(ver int) int {
	r := (16*ver+128)*ver + 64
	if ver >= 2 {
		na := ver/7 + 2
		r -= (25*na-10)*na - 55
		if ver >= 7 {
			r -= 36
		}
	}
	return r
}

func dataCodewords(ver int) int {
	return rawModules(ver)/8 - eccPerBlock[ver]*numBlocks[ver]
}

type bitBuf []bool

func (b *bitBuf) add(v, n int) {
	for i := n - 1; i >= 0; i-- {
		*b = append(*b, (v>>i)&1 == 1)
	}
}

// addECC divide i dati in blocchi, aggiunge la correzione Reed-Solomon e li intercala.
func addECC(data []byte, ver int) []byte {
	nb, ecLen := numBlocks[ver], eccPerBlock[ver]
	raw := rawModules(ver) / 8
	numShort := nb - raw%nb
	shortLen := raw / nb
	div := rsDivisor(ecLen)
	blocks := make([][]byte, nb)
	k := 0
	for i := 0; i < nb; i++ {
		n := shortLen - ecLen
		if i >= numShort {
			n++
		}
		dat := data[k : k+n]
		k += n
		block := make([]byte, shortLen+1)
		copy(block, dat)
		copy(block[len(block)-ecLen:], rsRemainder(dat, div))
		blocks[i] = block
	}
	out := make([]byte, 0, raw)
	for i := 0; i < len(blocks[0]); i++ {
		for j := range blocks {
			// Nei blocchi corti la posizione shortLen-ecLen è solo riempimento.
			if i != shortLen-ecLen || j >= numShort {
				out = append(out, blocks[j][i])
			}
		}
	}
	return out
}

func gfMul(x, y int) int {
	z := 0
	for i := 7; i >= 0; i-- {
		z = (z << 1) ^ ((z >> 7) * 0x11D)
		z ^= ((y >> i) & 1) * x
	}
	return z
}

func rsDivisor(degree int) []byte {
	res := make([]byte, degree)
	res[degree-1] = 1
	root := 1
	for i := 0; i < degree; i++ {
		for j := range res {
			res[j] = byte(gfMul(int(res[j]), root))
			if j+1 < len(res) {
				res[j] ^= res[j+1]
			}
		}
		root = gfMul(root, 0x02)
	}
	return res
}

func rsRemainder(data, div []byte) []byte {
	res := make([]byte, len(div))
	for _, b := range data {
		f := int(b ^ res[0])
		copy(res, res[1:])
		res[len(res)-1] = 0
		for i := range res {
			res[i] ^= byte(gfMul(int(div[i]), f))
		}
	}
	return res
}

type matrix struct {
	ver  int
	size int
	mod  [][]bool
	fn   [][]bool // moduli di funzione (non dati, non mascherati)
}

func newMatrix(ver int) *matrix {
	size := ver*4 + 17
	m := &matrix{ver: ver, size: size, mod: make([][]bool, size), fn: make([][]bool, size)}
	for i := range m.mod {
		m.mod[i] = make([]bool, size)
		m.fn[i] = make([]bool, size)
	}
	// Temporizzazione.
	for i := 0; i < size; i++ {
		m.set(6, i, i%2 == 0)
		m.set(i, 6, i%2 == 0)
	}
	// Riquadri di posizione con il separatore.
	m.finder(3, 3)
	m.finder(size-4, 3)
	m.finder(3, size-4)
	// Allineamento.
	pos := alignPositions(ver, size)
	for i := range pos {
		for j := range pos {
			last := len(pos) - 1
			if (i == 0 && j == 0) || (i == 0 && j == last) || (i == last && j == 0) {
				continue
			}
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					m.set(pos[i]+dx, pos[j]+dy, max(abs(dx), abs(dy)) != 1)
				}
			}
		}
	}
	m.drawFormat(0) // riserva le posizioni; il valore vero arriva con la maschera
	if ver >= 7 {
		rem := ver
		for i := 0; i < 12; i++ {
			rem = (rem << 1) ^ ((rem >> 11) * 0x1F25)
		}
		bits := ver<<12 | rem
		for i := 0; i < 18; i++ {
			bit := (bits>>i)&1 == 1
			a, b := size-11+i%3, i/3
			m.set(a, b, bit)
			m.set(b, a, bit)
		}
	}
	return m
}

func (m *matrix) set(x, y int, dark bool) {
	m.mod[y][x] = dark
	m.fn[y][x] = true
}

func (m *matrix) finder(cx, cy int) {
	for dy := -4; dy <= 4; dy++ {
		for dx := -4; dx <= 4; dx++ {
			x, y := cx+dx, cy+dy
			if x >= 0 && x < m.size && y >= 0 && y < m.size {
				d := max(abs(dx), abs(dy))
				m.set(x, y, d != 2 && d != 4)
			}
		}
	}
}

func alignPositions(ver, size int) []int {
	if ver == 1 {
		return nil
	}
	n := ver/7 + 2
	step := (ver*4 + n*2 + 1) / (n*2 - 2) * 2
	out := make([]int, n)
	out[0] = 6
	for i, p := n-1, size-7; i >= 1; i, p = i-1, p-step {
		out[i] = p
	}
	return out
}

func (m *matrix) drawFormat(mask int) {
	data := eclBits<<3 | mask
	rem := data
	for i := 0; i < 10; i++ {
		rem = (rem << 1) ^ ((rem >> 9) * 0x537)
	}
	bits := (data<<10 | rem) ^ 0x5412
	bit := func(i int) bool { return (bits>>i)&1 == 1 }
	for i := 0; i <= 5; i++ {
		m.set(8, i, bit(i))
	}
	m.set(8, 7, bit(6))
	m.set(8, 8, bit(7))
	m.set(7, 8, bit(8))
	for i := 9; i < 15; i++ {
		m.set(14-i, 8, bit(i))
	}
	for i := 0; i < 8; i++ {
		m.set(m.size-1-i, 8, bit(i))
	}
	for i := 8; i < 15; i++ {
		m.set(8, m.size-15+i, bit(i))
	}
	m.set(8, m.size-8, true)
}

func (m *matrix) drawCodewords(data []byte) {
	i := 0
	for right := m.size - 1; right >= 1; right -= 2 {
		if right == 6 {
			right = 5
		}
		for vert := 0; vert < m.size; vert++ {
			for j := 0; j < 2; j++ {
				x := right - j
				upward := (right+1)&2 == 0
				y := vert
				if upward {
					y = m.size - 1 - vert
				}
				if !m.fn[y][x] && i < len(data)*8 {
					m.mod[y][x] = (data[i>>3]>>(7-i&7))&1 == 1
					i++
				}
			}
		}
	}
}

func (m *matrix) applyMask(mask int) {
	for y := 0; y < m.size; y++ {
		for x := 0; x < m.size; x++ {
			if m.fn[y][x] {
				continue
			}
			var inv bool
			switch mask {
			case 0:
				inv = (x+y)%2 == 0
			case 1:
				inv = y%2 == 0
			case 2:
				inv = x%3 == 0
			case 3:
				inv = (x+y)%3 == 0
			case 4:
				inv = (x/3+y/2)%2 == 0
			case 5:
				inv = x*y%2+x*y%3 == 0
			case 6:
				inv = (x*y%2+x*y%3)%2 == 0
			case 7:
				inv = ((x+y)%2+x*y%3)%2 == 0
			}
			if inv {
				m.mod[y][x] = !m.mod[y][x]
			}
		}
	}
}

// penalty applica le quattro regole dello standard (sequenze, blocchi 2x2, falsi
// riquadri di posizione, equilibrio tra scuri e chiari).
func (m *matrix) penalty() int {
	n := m.size
	score := 0
	line := func(get func(i int) bool) {
		run, prev := 0, false
		seq := make([]bool, 0, n+8)
		for i := 0; i < 4; i++ {
			seq = append(seq, false)
		}
		for i := 0; i < n; i++ {
			c := get(i)
			seq = append(seq, c)
			if i > 0 && c == prev {
				run++
			} else {
				if run >= 5 {
					score += 3 + run - 5
				}
				run = 1
			}
			prev = c
		}
		if run >= 5 {
			score += 3 + run - 5
		}
		for i := 0; i < 4; i++ {
			seq = append(seq, false)
		}
		// 1011101 con quattro chiari prima o dopo.
		pat := []bool{true, false, true, true, true, false, true}
		for i := 0; i+11 <= len(seq); i++ {
			before := !seq[i] && !seq[i+1] && !seq[i+2] && !seq[i+3]
			after := !seq[i+7] && !seq[i+8] && !seq[i+9] && !seq[i+10]
			okA := true
			for k := 0; k < 7; k++ {
				if seq[i+4+k] != pat[k] {
					okA = false
					break
				}
			}
			okB := true
			for k := 0; k < 7; k++ {
				if seq[i+k] != pat[k] {
					okB = false
					break
				}
			}
			if okA && before {
				score += 40
			}
			if okB && after {
				score += 40
			}
		}
	}
	for y := 0; y < n; y++ {
		line(func(i int) bool { return m.mod[y][i] })
	}
	for x := 0; x < n; x++ {
		line(func(i int) bool { return m.mod[i][x] })
	}
	dark := 0
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			if m.mod[y][x] {
				dark++
			}
			if x+1 < n && y+1 < n {
				c := m.mod[y][x]
				if c == m.mod[y][x+1] && c == m.mod[y+1][x] && c == m.mod[y+1][x+1] {
					score += 3
				}
			}
		}
	}
	total := n * n
	k := (abs(dark*20-total*10)+total-1)/total - 1
	if k > 0 {
		score += k * 10
	}
	return score
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
