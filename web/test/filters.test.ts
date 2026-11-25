// Test della traduzione tra filtri visuali (chip) e query testuale: `pnpm test`.
import { test } from "node:test";
import assert from "node:assert/strict";
import { addClause, clausesOf, formatQuery, parseQuery, replaceClause, setClause } from "../src/views/filters.ts";

test("clausole, ordinamento e testo libero si separano e si ricompongono", () => {
  const q = 'tipo: application fase: aperta, "In produzione" -assegnatario: me aggiornata: -7g..oggi pagamenti urgenti ordina per: titolo desc';
  const parts = parseQuery(q);
  assert.deepEqual(
    parts.map((p) => p.kind),
    ["clause", "clause", "clause", "clause", "text", "sort"],
  );
  const c = clausesOf(q);
  assert.deepEqual(c[1], { kind: "clause", field: "fase", neg: false, values: ["aperta", '"In produzione"'] });
  assert.equal(c[2].neg, true);
  assert.equal(c[2].field, "assegnatario");
  assert.deepEqual(c[3].values, ["-7g..oggi"]);
  assert.equal(parts[4].kind === "text" && parts[4].raw, "pagamenti urgenti");
  assert.equal(formatQuery(parts), q);
});

test("operatori separati dal valore, campi con virgolette, valori in alternativa", () => {
  const c = clausesOf('"data go-live": >= 2026-01-01 stima: > 5 fase: Triage oppure fase: Bozza');
  assert.equal(c[0].field, "data go-live");
  assert.deepEqual(c[0].values, [">= 2026-01-01"]);
  assert.deepEqual(c[1].values, ["> 5"]);
  assert.deepEqual(c[2].values, ["Triage", "Bozza"]); // campo ripetuto = un solo chip
  assert.equal(formatQuery(parseQuery('"data go-live": >= 2026-01-01')), '"data go-live": >= 2026-01-01');
});

test("un chip cambia solo la sua clausola", () => {
  const q = "tipo: impact fase: Aperto testo libero ordina per: aggiornata desc";
  assert.equal(setClause(q, "fase", ["Chiuso", "In corso"]), 'tipo: impact fase: Chiuso, "In corso" testo libero ordina per: aggiornata desc');
  assert.equal(setClause(q, "assegnatario", ["me"]), "tipo: impact fase: Aperto testo libero assegnatario: me ordina per: aggiornata desc");
  assert.equal(setClause(q, "fase", []), "tipo: impact testo libero ordina per: aggiornata desc");
  assert.equal(setClause("tipo: x", "fase", ["In produzione"]), 'tipo: x fase: "In produzione"');
  assert.equal(replaceClause(q, 1, { kind: "clause", field: "fase", neg: true, values: ["Chiuso"] }), "tipo: impact -fase: Chiuso testo libero ordina per: aggiornata desc");
  assert.equal(replaceClause(q, 0, null), "fase: Aperto testo libero ordina per: aggiornata desc");
  assert.equal(addClause("tipo: x ordina per: titolo", { kind: "clause", field: "creata", neg: false, values: ["mese"] }), "tipo: x creata: mese ordina per: titolo");
});

test("query vuota o senza clausole", () => {
  assert.deepEqual(parseQuery(""), []);
  assert.deepEqual(clausesOf("solo testo"), []);
  assert.equal(formatQuery(parseQuery("solo testo")), "solo testo");
  assert.equal(setClause("", "tipo", ["risk"]), "tipo: risk");
});
