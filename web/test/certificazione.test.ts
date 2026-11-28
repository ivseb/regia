// Certificazione del rilascio e impostazioni dei rilasci: la logica non visiva (src/certificazione.ts).
import { test } from "node:test";
import assert from "node:assert/strict";
import { defectCounts, groupItems, moveRow, runExtras, runNumbers, sectionRows, settingsInput, shortHash, spaceKey, testTotals, visibleSections } from "../src/certificazione.ts";

const ALL = ["change", "releases", "items", "tests", "sonar", "security", "gate"] as const;
const item = (key: string, group: string) => ({ id: key, key, title: key, type: "story", type_name: "Story", state: "Fatto", state_category: "done", group, commits: 1, continues: false }) as never;

test("gli elementi si dividono in novità, correzioni e altro, senza gruppi vuoti", () => {
  const g = groupItems([item("A-1", "fix"), item("A-2", "new"), item("A-3", "fix"), item("A-4", "boh")]);
  assert.deepEqual(g.map((x) => x.group), ["new", "fix", "other"]);
  assert.deepEqual(g[1].items.map((i: { key: string }) => i.key), ["A-1", "A-3"]);
  assert.equal(g[2].items.length, 1); // un gruppo sconosciuto finisce in «altro»
  assert.deepEqual(groupItems([item("A-5", "fix")]).map((x) => x.group), ["fix"]);
});

test("le sezioni visibili seguono l'ordine delle impostazioni, senza doppioni né sconosciute", () => {
  assert.deepEqual(visibleSections(["gate", "change", "gate", "boh"], ALL), ["gate", "change"]);
  assert.deepEqual(visibleSections([], ALL), []);
});

test("l'editor mostra prima le sezioni accese, poi le spente; le frecce scambiano due righe", () => {
  const rows = sectionRows(["tests", "change"], ALL);
  assert.deepEqual(rows.map((r) => `${r.key}:${r.on ? 1 : 0}`), ["tests:1", "change:1", "releases:0", "items:0", "sonar:0", "security:0", "gate:0"]);
  const up = moveRow(rows, 1, -1);
  assert.deepEqual(up.slice(0, 2).map((r) => r.key), ["change", "tests"]);
  assert.equal(moveRow(rows, 0, -1), rows); // fuori dai bordi non cambia niente
  assert.equal(moveRow(rows, rows.length - 1, 1), rows);
});

test("il salvataggio manda le sezioni accese in ordine e le eccezioni pulite", () => {
  const rows = moveRow(sectionRows(["change", "releases"], ALL), 0, 1);
  const body = settingsInput(true, rows, [
    { key: " hl ", close: true },
    { key: "", close: false },
    { key: "OPS", close: false },
    { key: "hl", close: false },
  ]);
  assert.equal(body.close_on_keywords, true);
  assert.deepEqual(body.cert_sections, ["releases", "change"]);
  assert.deepEqual(body.spaces, [
    { key: "OPS", close_on_keywords: false },
    { key: "HL", close_on_keywords: false }, // vale l'ultima riga
  ]);
  assert.equal(spaceKey(" ab c "), "ABC");
});

test("impronta abbreviata e numeri dei test", () => {
  assert.equal(shortHash("0123456789abcdef0123"), "0123456789ab");
  assert.equal(shortHash(null), "");
  assert.equal(testTotals({ executed: 1, passed: 1, failed: 0, skipped: 0, counted: false }), null);
  // Istantanea vecchia: niente pianificati, bloccati, non eseguiti (valgono zero); i saltati a zero non compaiono.
  assert.deepEqual(testTotals({ executed: 1200, passed: 1198, failed: 2, skipped: 0, counted: true }, (n) => `n${n}`), {
    executed: "n1200",
    passed: "n1198",
    failed: "n2",
    blocked: "n0",
    notRun: "n0",
    planned: null,
    skipped: null,
  });
  // Test set di ALM: pianificati, bloccati e non eseguiti.
  assert.deepEqual(testTotals({ planned: 12, executed: 8, passed: 7, failed: 1, skipped: 1, blocked: 1, not_run: 2, counted: true }), {
    executed: "8",
    passed: "7",
    failed: "1",
    blocked: "1",
    notRun: "2",
    planned: "12",
    skipped: "1",
  });
  assert.equal(runNumbers({ executed: 120, passed: 118 }), "118/120");
  assert.equal(runNumbers({ executed: 120, passed: null }), "120");
  assert.equal(runNumbers({ executed: null, passed: null }), "");
});

test("numeri in più dei test set e difetti aperti per gravità", () => {
  assert.deepEqual(runExtras({ planned: 5, blocked: 0, not_run: 1 }), [
    { k: "planned", n: 5 },
    { k: "not_run", n: 1 },
  ]);
  assert.deepEqual(runExtras({}), []); // la pipeline non li dà
  assert.equal(defectCounts(undefined), null); // istantanea vecchia
  assert.equal(defectCounts({ open: 0, by_severity: {}, items: [] }), null);
  assert.deepEqual(defectCounts({ open: 3, by_severity: { high: 2, low: 1 }, items: [] }), [
    { severity: "critical", n: 0 },
    { severity: "high", n: 2 },
    { severity: "medium", n: 0 },
    { severity: "low", n: 1 },
  ]);
  assert.deepEqual(defectCounts({ open: 1, by_severity: { unknown: 1 }, items: [] })?.at(-1), { severity: "unknown", n: 1 });
});
