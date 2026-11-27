// Package textextract estrae il testo semplice dai documenti allegati, perché l'assistente del
// GRC possa leggerli come dato (replica/ai.md §2 punto 5): testo (txt, md, csv, json), documenti
// Word e OpenDocument (docx, odt: zip con XML) e PDF con testo. Solo libreria standard: niente
// OCR (un PDF scansionato non ha testo) e, per i PDF, un'estrazione semplice dei flussi di
// contenuto con le tabelle ToUnicode; un PDF cifrato o con caratteri senza tabella resta non
// letto. Tutto è limitato (byte letti, testo restituito) e nessun errore blocca il chiamante.
package textextract

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"encoding/hex"
	"encoding/xml"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// MaxInput è quanto si legge di un file al massimo.
const MaxInput = 16 << 20

// Kind dice se un file si sa leggere, dal tipo MIME o dall'estensione: «text», «docx», «odt»,
// «pdf» o vuoto.
func Kind(name, mime string) string {
	ext := strings.ToLower(path.Ext(name))
	mime = strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0]))
	switch {
	case mime == "application/pdf" || ext == ".pdf":
		return "pdf"
	case mime == "application/vnd.openxmlformats-officedocument.wordprocessingml.document" || ext == ".docx":
		return "docx"
	case mime == "application/vnd.oasis.opendocument.text" || ext == ".odt":
		return "odt"
	case strings.HasPrefix(mime, "text/") || mime == "application/json" || ext == ".txt" || ext == ".md" || ext == ".csv" || ext == ".json" || ext == ".log":
		return "text"
	}
	return ""
}

// Extract legge il testo del file (al massimo MaxInput byte) e ne restituisce al massimo max
// caratteri. Un file che non si sa leggere dà testo vuoto.
func Extract(r io.Reader, name, mime string, max int) string {
	data, err := io.ReadAll(io.LimitReader(r, MaxInput))
	if err != nil {
		return ""
	}
	var out string
	switch Kind(name, mime) {
	case "text":
		out = strings.ToValidUTF8(string(data), "")
	case "docx":
		out = zipXML(data, "word/document.xml", "p")
	case "odt":
		out = zipXML(data, "content.xml", "p", "h")
	case "pdf":
		out = PDF(data)
	}
	return clip(tidy(out), max)
}

func clip(s string, max int) string {
	if max <= 0 || utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "…"
}

var blankLines = regexp.MustCompile(`\n{3,}`)

// tidy toglie i caratteri di controllo, gli spazi in fondo alle righe e le righe vuote ripetute.
func tidy(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			b.WriteRune(r)
		}
	}
	lines := strings.Split(b.String(), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	return strings.TrimSpace(blankLines.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}

// zipXML legge il file XML dentro lo zip e restituisce il testo, un paragrafo per riga.
// paragraphs sono i nomi locali degli elementi che chiudono un paragrafo.
func zipXML(data []byte, member string, paragraphs ...string) string {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return ""
	}
	for _, f := range zr.File {
		if f.Name != member || f.UncompressedSize64 > MaxInput {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return ""
		}
		defer rc.Close()
		dec := xml.NewDecoder(io.LimitReader(rc, MaxInput))
		var b strings.Builder
		for {
			tok, err := dec.Token()
			if err != nil {
				break
			}
			switch t := tok.(type) {
			case xml.CharData:
				b.Write(t)
			case xml.StartElement:
				switch t.Name.Local {
				case "tab":
					b.WriteByte('\t')
				case "br", "line-break":
					b.WriteByte('\n')
				}
			case xml.EndElement:
				for _, p := range paragraphs {
					if t.Name.Local == p {
						b.WriteByte('\n')
					}
				}
			}
		}
		return b.String()
	}
	return ""
}

// ---------------------------------------------------------------------
// PDF
// ---------------------------------------------------------------------

var (
	streamRe  = regexp.MustCompile(`(?s)<<(.{0,1000}?)>>\s*stream\r?\n`)
	cmapStart = []byte("begincmap")
)

// PDF estrae il testo dei flussi di contenuto: stringhe degli operatori Tj, TJ, ' e ", con a
// capo sugli spostamenti di riga; le stringhe esadecimali dei caratteri composti si traducono
// con le tabelle ToUnicode del documento. Un PDF cifrato non si legge.
func PDF(data []byte) string {
	if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("%PDF")) || bytes.Contains(data, []byte("/Encrypt")) {
		return ""
	}
	var contents [][]byte
	uni := map[string]string{}
	for _, m := range streamRe.FindAllSubmatchIndex(data, -1) {
		dict := data[m[2]:m[3]]
		start := m[1]
		end := bytes.Index(data[start:], []byte("endstream"))
		if end < 0 {
			continue
		}
		raw := data[start : start+end]
		if bytes.Contains(dict, []byte("/Image")) || bytes.Contains(dict, []byte("/DCTDecode")) || bytes.Contains(dict, []byte("/FontFile")) {
			continue
		}
		body := raw
		if bytes.Contains(dict, []byte("/FlateDecode")) {
			zr, err := zlib.NewReader(bytes.NewReader(raw))
			if err != nil {
				continue
			}
			body, _ = io.ReadAll(io.LimitReader(zr, MaxInput))
			zr.Close()
		} else if bytes.Contains(dict, []byte("/Filter")) {
			continue // altri filtri (LZW, ASCII85...) non si leggono
		}
		if bytes.Contains(body, cmapStart) {
			parseCMap(body, uni)
			continue
		}
		contents = append(contents, body)
	}
	var b strings.Builder
	for _, c := range contents {
		textOps(c, uni, &b)
		b.WriteByte('\n')
	}
	return b.String()
}

var (
	bfcharRe  = regexp.MustCompile(`(?s)beginbfchar(.*?)endbfchar`)
	bfrangeRe = regexp.MustCompile(`(?s)beginbfrange(.*?)endbfrange`)
	hexTokRe  = regexp.MustCompile(`<([0-9A-Fa-f\s]*)>|\[([^\]]*)\]`)
)

// parseCMap legge le corrispondenze codice → Unicode di una tabella ToUnicode.
func parseCMap(body []byte, uni map[string]string) {
	for _, m := range bfcharRe.FindAllSubmatch(body, -1) {
		toks := hexTokRe.FindAllSubmatch(m[1], -1)
		for i := 0; i+1 < len(toks); i += 2 {
			uni[cleanHex(toks[i][1])] = utf16Hex(cleanHex(toks[i+1][1]))
		}
	}
	for _, m := range bfrangeRe.FindAllSubmatch(body, -1) {
		toks := hexTokRe.FindAllSubmatch(m[1], -1)
		for i := 0; i+2 < len(toks); i += 3 {
			lo, hi := cleanHex(toks[i][1]), cleanHex(toks[i+1][1])
			a, err1 := strconv.ParseUint(lo, 16, 32)
			z, err2 := strconv.ParseUint(hi, 16, 32)
			if err1 != nil || err2 != nil || z < a || z-a > 5000 {
				continue
			}
			if toks[i+2][2] != nil { // [ <dst> <dst> ... ]
				dsts := hexTokRe.FindAllSubmatch(toks[i+2][2], -1)
				for k := uint64(0); k <= z-a && int(k) < len(dsts); k++ {
					uni[codeHex(a+k, len(lo))] = utf16Hex(cleanHex(dsts[k][1]))
				}
				continue
			}
			base := []rune(utf16Hex(cleanHex(toks[i+2][1])))
			if len(base) == 0 {
				continue
			}
			for k := uint64(0); k <= z-a; k++ {
				r := append([]rune(nil), base...)
				r[len(r)-1] += rune(k)
				uni[codeHex(a+k, len(lo))] = string(r)
			}
		}
	}
}

func cleanHex(b []byte) string {
	return strings.ToUpper(strings.Join(strings.Fields(string(b)), ""))
}

func codeHex(v uint64, width int) string {
	s := strings.ToUpper(strconv.FormatUint(v, 16))
	for len(s) < width {
		s = "0" + s
	}
	return s
}

// utf16Hex decodifica l'esadecimale UTF-16BE di una destinazione ToUnicode.
func utf16Hex(h string) string {
	b, err := hex.DecodeString(h)
	if err != nil || len(b)%2 != 0 {
		return ""
	}
	var r []rune
	for i := 0; i+1 < len(b); i += 2 {
		u := rune(b[i])<<8 | rune(b[i+1])
		if u >= 0xD800 && u < 0xDC00 && i+3 < len(b) {
			l := rune(b[i+2])<<8 | rune(b[i+3])
			r = append(r, 0x10000+(u-0xD800)<<10+(l-0xDC00))
			i += 2
			continue
		}
		r = append(r, u)
	}
	return string(r)
}

// textOps scorre il flusso di contenuto e scrive le stringhe di testo.
func textOps(c []byte, uni map[string]string, b *strings.Builder) {
	var pending []string // stringhe lette dopo l'ultimo operatore
	inArray := false     // dentro l'array di TJ: uno spostamento ampio è uno spazio fra parole
	flushTo := func(nl bool) {
		if nl && b.Len() > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteByte('\n')
		}
	}
	i := 0
	for i < len(c) {
		ch := c[i]
		switch {
		case ch == '(':
			s, n := literal(c[i:])
			pending = append(pending, latin1(s))
			i += n
			continue
		case ch == '<' && i+1 < len(c) && c[i+1] != '<':
			j := bytes.IndexByte(c[i:], '>')
			if j < 0 {
				return
			}
			pending = append(pending, hexString(cleanHex(c[i+1:i+j]), uni))
			i += j + 1
			continue
		case ch == '%':
			j := bytes.IndexAny(c[i:], "\r\n")
			if j < 0 {
				return
			}
			i += j
			continue
		case isRegular(ch):
			j := i
			for j < len(c) && isRegular(c[j]) {
				j++
			}
			switch string(c[i:j]) {
			case "Tj", "TJ":
				b.WriteString(strings.Join(pending, ""))
			case "'", "\"":
				flushTo(true)
				b.WriteString(strings.Join(pending, ""))
			case "Td", "TD", "T*", "Tm":
				flushTo(true)
			case "ET":
				flushTo(true)
			}
			if isNumber(c[i:j]) {
				if v, _ := strconv.ParseFloat(string(c[i:j]), 64); inArray && v < -180 && len(pending) > 0 {
					pending = append(pending, " ")
				}
			} else {
				pending = pending[:0]
			}
			i = j
			continue
		}
		switch ch {
		case '[':
			inArray = true
		case ']':
			inArray = false
		}
		if ch == '\'' || ch == '"' {
			flushTo(true)
			b.WriteString(strings.Join(pending, ""))
			pending = pending[:0]
		}
		i++
	}
}

func isRegular(ch byte) bool {
	return !(ch <= ' ' || strings.IndexByte("()<>[]{}/%'\"", ch) >= 0)
}

func isNumber(t []byte) bool {
	_, err := strconv.ParseFloat(string(t), 64)
	return err == nil
}

// literal legge una stringa letterale «( … )» con le parentesi annidate e le sequenze di escape.
func literal(c []byte) ([]byte, int) {
	var out []byte
	depth := 0
	for i := 0; i < len(c); i++ {
		ch := c[i]
		switch ch {
		case '(':
			if depth > 0 {
				out = append(out, ch)
			}
			depth++
		case ')':
			depth--
			if depth == 0 {
				return out, i + 1
			}
			out = append(out, ch)
		case '\\':
			if i+1 >= len(c) {
				return out, len(c)
			}
			i++
			switch e := c[i]; e {
			case 'n':
				out = append(out, '\n')
			case 'r':
				out = append(out, '\r')
			case 't':
				out = append(out, '\t')
			case 'b', 'f':
			case '\r', '\n':
			default:
				if e >= '0' && e <= '7' {
					v, n := 0, 0
					for n < 3 && i < len(c) && c[i] >= '0' && c[i] <= '7' {
						v = v*8 + int(c[i]-'0')
						i++
						n++
					}
					i--
					out = append(out, byte(v))
				} else {
					out = append(out, e)
				}
			}
		default:
			out = append(out, ch)
		}
	}
	return out, len(c)
}

// latin1 legge i byte come Latin-1 (vicino alla codifica WinAnsi dei PDF semplici).
func latin1(b []byte) string {
	r := make([]rune, 0, len(b))
	for _, x := range b {
		r = append(r, rune(x))
	}
	return string(r)
}

// hexString traduce una stringa esadecimale: con le tabelle ToUnicode a gruppi di 4 (o 2)
// cifre, altrimenti come byte Latin-1 se sono stampabili.
func hexString(h string, uni map[string]string) string {
	if len(uni) > 0 {
		var b strings.Builder
		for _, w := range []int{4, 2} {
			if len(h)%w != 0 {
				continue
			}
			b.Reset()
			ok := true
			for i := 0; i+w <= len(h); i += w {
				s, found := uni[h[i:i+w]]
				if !found {
					ok = false
					break
				}
				b.WriteString(s)
			}
			if ok {
				return b.String()
			}
		}
	}
	raw, err := hex.DecodeString(h)
	if err != nil {
		return ""
	}
	for _, x := range raw {
		if x < 0x20 && x != '\n' && x != '\t' {
			return ""
		}
	}
	return latin1(raw)
}
