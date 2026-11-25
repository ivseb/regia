package grc

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"regia/internal/engine"
)

// Pacchetto per l'audit: uno zip con
//   - LEGGIMI.txt: cosa contiene, quando è stato prodotto e lo SHA-256 di ogni file;
//   - copertura.json: cruscotto e copertura voce per voce, perimetro per perimetro;
//   - mappature.csv: requisito → perimetro → controllo → evidenza, una riga per legame;
//   - evidenze.csv: le evidenze con stato, validità, chi le ha validate e la firma;
//   - evidenze/<CHIAVE>/<file>: gli allegati delle evidenze (fino a 200 MB in tutto).

// FileSource apre un allegato per SHA-256 (collab.FileStore).
type FileSource interface {
	Open(sha string) (*os.File, error)
}

const maxPackFiles = 200 << 20

type hashedZip struct {
	z     *zip.Writer
	sums  []string
	stamp time.Time
}

func (h *hashedZip) create(name string) (io.Writer, func(), error) {
	w, err := h.z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: h.stamp})
	if err != nil {
		return nil, nil, err
	}
	sum := sha256.New()
	return io.MultiWriter(w, sum), func() { h.sums = append(h.sums, hex.EncodeToString(sum.Sum(nil))+"  "+name) }, nil
}

func (h *hashedZip) file(name string, body []byte) error {
	w, done, err := h.create(name)
	if err != nil {
		return err
	}
	if _, err := w.Write(body); err != nil {
		return err
	}
	done()
	return nil
}

// packFilter restringe il pacchetto al perimetro e al periodo di un incarico di audit e aggiunge
// i suoi file (campioni, richieste, rilievi).
type packFilter struct {
	Controls map[string]bool
	From, To *time.Time
	Extra    map[string][]byte
	Header   string
}

func (f *packFilter) keepEvidence(ev *evModel) bool {
	if f == nil || f.From == nil || f.To == nil {
		return true
	}
	return !ev.ValidFrom.After(f.To.AddDate(0, 0, 1)) && (ev.ValidUntil == nil || !ev.ValidUntil.Before(*f.From))
}

// WriteAuditPack scrive il pacchetto per l'audit (framework vuoto = tutte le normative abilitate).
func (s *Service) WriteAuditPack(ctx context.Context, tx pgx.Tx, actor engine.Actor, w io.Writer, framework string, files FileSource) error {
	return s.writeAuditPack(ctx, tx, actor, w, framework, files, nil)
}

func (s *Service) writeAuditPack(ctx context.Context, tx pgx.Tx, actor engine.Actor, w io.Writer, framework string, files FileSource, flt *packFilter) error {
	if err := requireRead(actor); err != nil {
		return err
	}
	m, err := s.loadModel(ctx, tx)
	if err != nil {
		return err
	}
	if framework != "" && !containsFw(m.fws, framework) {
		return engine.InvalidParams("validation.grc.framework_disabled", "framework", map[string]any{"framework": framework}, "normativa %q non abilitata", framework)
	}
	d := m.dashboardFor(framework)
	covs := []*FrameworkCoverage{}
	for _, f := range m.fws {
		if framework != "" && f.Key != framework {
			continue
		}
		fc, err := s.FrameworkCoverage(ctx, tx, actor, f.Key)
		if err != nil {
			return err
		}
		fc.Dashboard = nil
		covs = append(covs, fc)
	}
	hz := &hashedZip{z: zip.NewWriter(w), stamp: m.now}
	body, err := json.MarshalIndent(map[string]any{"generated_at": m.now, "framework": framework, "dashboard": d, "coverage": covs}, "", "  ")
	if err != nil {
		return err
	}
	if err := hz.file("copertura.json", body); err != nil {
		return err
	}

	// mappature.csv
	evidence := map[string]*evModel{}
	evControls := map[string][]string{}
	mw, done, err := hz.create("mappature.csv")
	if err != nil {
		return err
	}
	_, _ = io.WriteString(mw, bom)
	cw := csv.NewWriter(mw)
	cw.Comma = ';'
	_ = cw.Write([]string{"normativa", "riferimento", "titolo", "perimetro", "stato", "controllo", "titolo del controllo", "responsabile",
		"copertura", "efficacia", "evidenza", "tipo di evidenza", "valida fino al", "validata da", "firma"})
	for _, f := range m.fws {
		if framework != "" && f.Key != framework {
			continue
		}
		for _, r := range f.reqs {
			for _, p := range m.pairs(r) {
				sc := m.scopes[p.scope]
				scope := sc.Name
				if sc.Key != "" {
					scope = sc.Key + " " + sc.Name
				}
				base := []string{f.Key, r.Ref, r.Title, scope, StatusNames[p.status]}
				if flt != nil && len(p.controls) == 0 {
					continue
				}
				if len(p.controls) == 0 {
					_ = cw.Write(append(base, "", "", "", "", "", "", "", "", "", ""))
					continue
				}
				for _, pc := range p.controls {
					if flt != nil && !flt.Controls[pc.c.ID] {
						continue
					}
					cb := append(append([]string{}, base...), pc.c.Key, pc.c.Title, pc.c.Owner, pc.coverage, effectivenessNames[pc.c.Effectiveness])
					if len(pc.evidence) == 0 {
						_ = cw.Write(append(cb, "", "", "", "", ""))
						continue
					}
					for _, ev := range pc.evidence {
						if !flt.keepEvidence(ev) {
							continue
						}
						evidence[ev.ID] = ev
						if !containsStr(evControls[ev.ID], pc.c.Key) {
							evControls[ev.ID] = append(evControls[ev.ID], pc.c.Key)
						}
						until := ""
						if ev.ValidUntil != nil {
							until = ev.ValidUntil.Format(time.DateOnly)
						}
						_ = cw.Write(append(cb, ev.Key, KindNamesEvidence[ev.Kind], until, ev.ValidatedBy, ev.Signature))
					}
				}
			}
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	done()

	// evidenze.csv
	ids := make([]string, 0, len(evidence))
	for id := range evidence {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return evidence[ids[i]].Key < evidence[ids[j]].Key })
	ew, done, err := hz.create("evidenze.csv")
	if err != nil {
		return err
	}
	_, _ = io.WriteString(ew, bom)
	cw = csv.NewWriter(ew)
	cw.Comma = ';'
	_ = cw.Write([]string{"evidenza", "titolo", "tipo", "perimetro", "stato", "valida dal", "valida fino al", "validata da", "validata il", "firma", "controlli"})
	manual := []string{}
	for _, id := range ids {
		ev := evidence[id]
		ref := m.evRef(ev)
		until, at := "", ""
		if ev.ValidUntil != nil {
			until = ev.ValidUntil.Format(time.DateOnly)
		}
		if ev.ValidatedAt != nil {
			at = ev.ValidatedAt.UTC().Format(time.RFC3339)
		}
		scope := ref.Scope.Name
		if ref.Scope.Key != "" {
			scope = ref.Scope.Key + " " + ref.Scope.Name
		}
		_ = cw.Write([]string{ev.Key, ev.Title, KindNamesEvidence[ev.Kind], scope, ref.Status, ev.ValidFrom.Format(time.DateOnly), until,
			ev.ValidatedBy, at, ev.Signature, strings.Join(evControls[id], ", ")})
		if !ev.Derived {
			manual = append(manual, id)
		}
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		return err
	}
	done()

	if flt != nil {
		names := make([]string, 0, len(flt.Extra))
		for n := range flt.Extra {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			if err := hz.file(n, flt.Extra[n]); err != nil {
				return err
			}
		}
	}

	// Allegati delle evidenze.
	skipped := 0
	if files != nil && len(manual) > 0 {
		rows, err := tx.Query(ctx, `select s.key || '-' || e.number, a.name, encode(a.sha256, 'hex'), a.size_bytes
			from attachments a join elements e on e.tenant_id = a.tenant_id and e.id = a.element_id
			join spaces s on s.tenant_id = e.tenant_id and s.id = e.space_id
			where a.element_id = any($1) and a.removed_at is null order by e.number, a.created_at`, manual)
		if err != nil {
			return err
		}
		type att struct {
			key, name, sha string
			size           int64
		}
		var list []att
		var a att
		if _, err := pgx.ForEachRow(rows, []any{&a.key, &a.name, &a.sha, &a.size}, func() error { list = append(list, a); return nil }); err != nil {
			return err
		}
		var total int64
		used := map[string]bool{}
		for _, a := range list {
			if total+a.size > maxPackFiles {
				skipped++
				continue
			}
			f, err := files.Open(a.sha)
			if err != nil {
				skipped++
				continue
			}
			name := path.Join("evidenze", a.key, safeName(a.name))
			for i := 2; used[name]; i++ {
				name = path.Join("evidenze", a.key, fmt.Sprintf("%d-%s", i, safeName(a.name)))
			}
			used[name] = true
			fw, done, err := hz.create(name)
			if err != nil {
				f.Close()
				return err
			}
			_, err = io.Copy(fw, f)
			f.Close()
			if err != nil {
				return err
			}
			done()
			total += a.size
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Pacchetto per l'audit della conformità (Regia)\n\nProdotto il: %s\n", m.now.UTC().Format(time.RFC3339))
	if framework != "" {
		fmt.Fprintf(&b, "Normativa: %s\n", framework)
	} else {
		b.WriteString("Normative: tutte quelle abilitate\n")
	}
	if flt != nil {
		b.WriteString(flt.Header)
	}
	for _, f := range d.Frameworks {
		fmt.Fprintf(&b, "- %s (%s), versione %s: copertura %d%% (%d su %d), senza controllo %d, controlli senza evidenza valida %d\n",
			f.Name, f.Key, f.Version, f.Percent, f.Covered, f.Applicable, f.WithoutControl, f.ControlsWithoutEvidence)
	}
	if flt != nil {
		b.WriteString("\nFile dell'incarico: campioni.csv (campioni con seme ed esiti), richieste.csv (richieste di evidenza), rilievi.csv\n")
	}
	b.WriteString("\nContenuto:\n- copertura.json: cruscotto e copertura voce per voce e perimetro per perimetro\n" +
		"- mappature.csv: requisito, perimetro, controllo ed evidenza, una riga per legame\n" +
		"- evidenze.csv: evidenze con stato, validità, chi le ha validate e firma (SHA-256 del contenuto, di chi firma e di quando)\n" +
		"- evidenze/<chiave>/: allegati delle evidenze\n")
	if skipped > 0 {
		fmt.Fprintf(&b, "\nAllegati non inclusi (oltre 200 MB o non disponibili): %d\n", skipped)
	}
	b.WriteString("\nSHA-256 dei file:\n")
	for _, s := range hz.sums {
		b.WriteString(s + "\n")
	}
	if err := hz.file("LEGGIMI.txt", []byte(b.String())); err != nil {
		return err
	}
	if err := hz.z.Close(); err != nil {
		return err
	}
	return audited(ctx, tx, actor, "grc.audit_pack.export", "grc", "", map[string]any{"framework": framework, "evidence": len(ids)})
}

func containsFw(fws []*fwModel, key string) bool {
	for _, f := range fws {
		if f.Key == key {
			return true
		}
	}
	return false
}

func safeName(n string) string {
	n = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r < 32 {
			return '_'
		}
		return r
	}, n)
	n = strings.TrimLeft(n, ".")
	if n == "" {
		n = "file"
	}
	return n
}
