// Editor a blocchi del Knowledge: il Markdown letto in blocchi e riscritto non cambia (`pnpm test`).
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { convertBlock, duplicateBlock, moveBlock, newBlock, parseBlocks, removeBlock, render, renumber, toMarkdown, insertBlocks, shortcut } from "../src/kbBlocks.ts";

const roundtrip = (md: string) => toMarkdown(parseBlocks(md));
/** Riscrittura canonica: ogni blocco perde il testo originale e le distanze tornano automatiche. */
const canonical = (md: string) => {
  const d = parseBlocks(md);
  return toMarkdown({ ...d, blocks: d.blocks.map((b) => ({ ...b, src: undefined, pre: undefined })) });
};

// Pagine di esempio: i modelli del Knowledge (internal/modules/knowledge/templates.go) e testi scritti per la prova.
const templates = [...readFileSync(resolve(import.meta.dirname, "..", "..", "internal", "modules", "knowledge", "templates.go"), "utf8").matchAll(/Body: `([^`]*)`/g)].map((m) => m[1]);

const SAMPLES = [
  "# Titolo\n\nUn paragrafo con **grassetto**, *corsivo*, `codice` e [un link](https://esempio.it).\nSeconda riga dello stesso paragrafo.\n\n## Sezione\n\n- uno\n- due\n  - annidato\n- tre\n\n1. primo\n2. secondo\n\n- [ ] da fare\n- [x] fatto\n\n> citazione\n> su due righe\n\n> [!ATTENZIONE] Fermare **prima** il job\n> seconda riga\n\n```sh\nmake build\n```\n\n| a | b |\n|---|---|\n| DEM-12 | [link](https://x.it) |\n\n![schema](/api/v1/attachments/0a1b2c3d-0000-4000-8000-0123456789ab/content)\n\n[contratto.pdf](/api/v1/attachments/0a1b2c3d-0000-4000-8000-0123456789ab/content?download=1)\n\nDEM-12\n\n---\n\nFine.\n",
  "Senza titolo e senza a capo finale",
  "\n\nInizia con righe vuote\n\n\n\nTre vuote sopra\n\n\n",
  "- a\n- b\n\n\n- c\n",
  "1. uno\n1. uno\n1. uno\n",
  "# T\r\n\r\nTesto con CRLF\r\n- voce\r\n",
  "> [!NOTA]\n> senza testo sulla prima riga\n\n> citazione con elenco\n> - voce\n\n```\nnon chiuso\n",
  "Riga uno\n---\nRiga due\n\n***\n\n| solo | testa |\n|:--|--:|\n",
  "",
  "   \n\n",
  ...templates,
];

test("un giro Markdown → blocchi → Markdown non cambia il testo", () => {
  assert.ok(templates.length >= 3, "pagine di esempio trovate nei modelli");
  for (const md of SAMPLES) assert.equal(roundtrip(md), md);
});

test("la riscrittura canonica è stabile e rilegge gli stessi blocchi", () => {
  for (const md of SAMPLES) {
    const once = canonical(md);
    assert.equal(canonical(once), once, "la forma canonica è un punto fisso");
    assert.equal(roundtrip(once), once);
  }
});

test("le pagine già in forma canonica si riscrivono uguali anche senza il testo originale", () => {
  assert.equal(canonical(SAMPLES[0]).trimEnd(), SAMPLES[0].trimEnd());
});

test("tipi di blocco riconosciuti", () => {
  const kinds = parseBlocks(SAMPLES[0]).blocks.map((b) => b.kind);
  assert.deepEqual(kinds, ["heading", "paragraph", "heading", "bullet", "bullet", "bullet", "bullet", "number", "number", "task", "task", "quote", "callout", "code", "table", "image", "file", "ref", "divider", "paragraph"]);
  const nested = parseBlocks("> citazione con elenco\n> - voce");
  assert.equal(nested.blocks[0].kind, "raw");
});

test("spostare, duplicare ed eliminare blocchi riscrive il testo con le distanze giuste", () => {
  const d = parseBlocks("# A\n\ntesto\n\n- x\n- y\n");
  const up = moveBlock(d.blocks, 1, -1);
  assert.equal(toMarkdown({ ...d, blocks: up }), "testo\n\n# A\n\n- x\n- y\n");
  const dup = duplicateBlock(d.blocks, 2);
  assert.equal(toMarkdown({ ...d, blocks: dup }), "# A\n\ntesto\n\n- x\n- x\n- y\n");
  assert.equal(toMarkdown({ ...d, blocks: removeBlock(d.blocks, 0) }), "testo\n\n- x\n- y\n");
});

test("convertire un blocco tiene il testo; l'elenco numerato si rinumera", () => {
  const d = parseBlocks("uno\n\ndue\n\ntre");
  const as = d.blocks.map((b) => convertBlock(b, "number"));
  assert.equal(toMarkdown({ ...d, blocks: renumber(as) }), "1. uno\n\n2. due\n\n3. tre".replace(/\n\n/g, "\n"));
  const h = convertBlock(d.blocks[0], "heading", { level: 3 });
  assert.equal(render(h), "### uno");
  const cal = convertBlock(d.blocks[1], "callout", { callout: "PERICOLO" });
  assert.equal(render(cal), "> [!PERICOLO] due");
});

test("una voce numerata inserita rinumera solo ciò che cambia", () => {
  const d = parseBlocks("1. a\n2. b\n3. c\n");
  const out = insertBlocks(d.blocks, 1, [newBlock("number", { text: "nuova" })]);
  assert.equal(toMarkdown({ ...d, blocks: out }), "1. a\n2. nuova\n3. b\n4. c\n");
});

test("tabella: celle con barra verticale e righe corte", () => {
  const d = parseBlocks("| a | b |\n|---|---|\n| x \\| y |\n");
  assert.deepEqual(d.blocks[0].rows, [["a", "b"], ["x | y", ""]]);
  assert.equal(render({ ...d.blocks[0], src: undefined }), "| a | b |\n|---|---|\n| x \\| y |  |");
});

test("scorciatoie di scrittura", () => {
  assert.equal(shortcut("## ")?.level, 2);
  assert.equal(shortcut("- ")?.kind, "bullet");
  assert.equal(shortcut("1. ")?.kind, "number");
  assert.equal(shortcut("> ")?.kind, "quote");
  assert.equal(shortcut("[] ")?.kind, "task");
  assert.equal(shortcut("testo"), null);
});
