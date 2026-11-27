package textextract

import (
	"archive/zip"
	"bytes"
	"compress/zlib"
	"fmt"
	"strings"
	"testing"
)

// pdfWith costruisce un PDF minimo con i flussi dati (compressi se flate).
func pdfWith(flate bool, streams ...string) []byte {
	var b bytes.Buffer
	b.WriteString("%PDF-1.7\n")
	for i, s := range streams {
		body := []byte(s)
		dict := fmt.Sprintf("<< /Length %d >>", len(body))
		if flate {
			var z bytes.Buffer
			w := zlib.NewWriter(&z)
			_, _ = w.Write(body)
			_ = w.Close()
			body = z.Bytes()
			dict = fmt.Sprintf("<< /Length %d /Filter /FlateDecode >>", len(body))
		}
		fmt.Fprintf(&b, "%d 0 obj\n%s\nstream\n", i+1, dict)
		b.Write(body)
		b.WriteString("\nendstream\nendobj\n")
	}
	b.WriteString("trailer\n<< >>\n%%EOF\n")
	return b.Bytes()
}

func TestPDF(t *testing.T) {
	content := `BT /F1 12 Tf 72 720 Td (Politica di backup \(v2\)) Tj 0 -14 Td [(Copia) -250 (giornaliera)] TJ T* (Prova di ripristino: trimestrale) Tj ET`
	got := Extract(bytes.NewReader(pdfWith(true, content)), "politica.pdf", "application/pdf", 0)
	want := "Politica di backup (v2)\nCopia giornaliera\nProva di ripristino: trimestrale"
	if got != want {
		t.Fatalf("testo del PDF:\n%q", got)
	}
	// Caratteri composti (Identity-H) tradotti con la tabella ToUnicode, anche a intervalli.
	cmap := `/CIDInit /ProcSet findresource begin 12 dict begin begincmap
2 beginbfchar <0001> <0043> <0002> <00E8> endbfchar
1 beginbfrange <0010> <0012> <0061> endbfrange
endcmap CMapName currentdict /CMap defineresource pop end end`
	got = PDF(pdfWith(false, cmap, `BT /F2 10 Tf 10 10 Td <00010010001100120002> Tj ET`))
	if strings.TrimSpace(got) != "Cabcè" {
		t.Fatalf("ToUnicode: %q", got)
	}
	if PDF([]byte("%PDF-1.7\n1 0 obj << /Encrypt 2 0 R >> endobj")) != "" || PDF([]byte("non un pdf")) != "" {
		t.Fatal("cifrato o non PDF letto")
	}
}

func TestDocxOdtText(t *testing.T) {
	mk := func(name, xml string) []byte {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		w, _ := z.Create(name)
		_, _ = w.Write([]byte(xml))
		_ = z.Close()
		return b.Bytes()
	}
	docx := mk("word/document.xml", `<w:document xmlns:w="x"><w:body><w:p><w:r><w:t>Runbook di</w:t></w:r><w:r><w:t xml:space="preserve"> ripristino</w:t></w:r></w:p><w:p><w:r><w:t>Passo 1</w:t><w:tab/><w:t>fermare</w:t></w:r></w:p></w:body></w:document>`)
	if got := Extract(bytes.NewReader(docx), "r.docx", "", 0); got != "Runbook di ripristino\nPasso 1\tfermare" {
		t.Fatalf("docx: %q", got)
	}
	odt := mk("content.xml", `<office:document-content xmlns:office="o" xmlns:text="t"><office:body><text:h>Titolo</text:h><text:p>Testo<text:line-break/>a capo</text:p></office:body></office:document-content>`)
	if got := Extract(bytes.NewReader(odt), "r.odt", "application/vnd.oasis.opendocument.text", 0); got != "Titolo\nTesto\na capo" {
		t.Fatalf("odt: %q", got)
	}
	if got := Extract(strings.NewReader("riga uno\n\n\n\nriga due  \n"), "nota.md", "", 12); got != "riga uno\n\nri…" {
		t.Fatalf("testo accorciato: %q", got)
	}
	if Kind("foto.png", "image/png") != "" || Extract(strings.NewReader("x"), "foto.png", "image/png", 0) != "" {
		t.Fatal("immagine letta")
	}
	if Extract(bytes.NewReader([]byte("PK rotto")), "x.docx", "", 0) != "" {
		t.Fatal("zip rotto")
	}
}
