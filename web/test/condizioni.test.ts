// Test del costruttore unico di condizioni: conversione da e verso la sintassi testuale,
// senza perdite. `pnpm test` (node:test).
import { test } from "node:test";
import assert from "node:assert/strict";
import { setLang } from "../src/i18n/core.ts";

await setLang("it", false);
import { completeRows, fromGroups, phrase, toGroups } from "../src/casa/gruppi.ts";
import { conditionError } from "../src/pages/processEditor/model.ts";

test("ogni condizione a righe semplici torna uguale dopo testo → gruppi → testo", () => {
  for (const src of [
    "stima_max: >50000",
    "driver: normativo, strategico",
    "-driver: operativo",
    "ruolo.sponsor: presente",
    "ruolo.sponsor: assente",
    "obbligatoria: sì",
    'unita: "Area Clienti"',
    "driver: normativo oppure obbligatoria: sì",
    "stima_max: >=10000 stima_max: <250000",
    "scadenza_normativa: <2027-01-01",
    // «e» dentro «oppure»: prima non si esprimeva con le righe, ora sono due gruppi.
    "funzione_critica: sì criticita: alta, critica oppure esposto_internet: sì",
    "a: 1 b: 2 oppure c: 3 d: 4 oppure e: assente",
  ]) {
    const g = toGroups(src);
    assert.ok(g, src);
    assert.equal(fromGroups(g), src, src);
    assert.equal(conditionError(fromGroups(g)), null, src);
  }
});

test("i gruppi sono le alternative, le righe di un gruppo sono tutte vere", () => {
  const g = toGroups("a: 1 b: 2 oppure c: 3")!;
  assert.equal(g.length, 2);
  assert.equal(g[0].length, 2);
  assert.equal(g[1].length, 1);
  assert.deepEqual(g[1][0], { fact: "c", op: "is", values: ["3"] });
});

test("la condizione vuota è un solo gruppo vuoto", () => {
  assert.deepEqual(toGroups(""), [[]]);
  assert.equal(fromGroups([[]]), "");
  assert.equal(completeRows([[]]), 0);
});

test("una condizione che le righe non esprimono resta testo (null) e non si perde", () => {
  assert.equal(toGroups("-stima_max: >10"), null);
  assert.equal(toGroups("driver normativo"), null);
  assert.equal(toGroups("x: >5, presente"), null);
});

test("righe incomplete e gruppi vuoti non finiscono nel testo", () => {
  assert.equal(fromGroups([[{ fact: "x", op: "is", values: [] }], [{ fact: "y", op: "absent", values: [] }]]), "y: assente");
  assert.equal(fromGroups([[], [{ fact: "y", op: "yes", values: [] }]]), "y: sì");
  assert.equal(completeRows([[{ fact: "x", op: "is", values: [] }, { fact: "y", op: "no", values: [] }]]), 1);
});

test("valori con spazi o parole riservate si virgolettano e tornano uguali", () => {
  const src = 'unita: "Area Clienti", IT oppure nota: "presente"';
  const g = toGroups(src)!;
  assert.equal(fromGroups(g), src);
  assert.deepEqual(g[0][0].values, ["Area Clienti", "IT"]);
});

test("la frase in parole usa i nomi dei dati e «e» / «oppure»", () => {
  const names = { funzione_critica: "Funzione critica", criticita: "Criticità" };
  assert.equal(phrase("funzione_critica: sì criticita: alta, critica", names), "Funzione critica è sì e Criticità è alta, critica");
  assert.equal(phrase("funzione_critica: sì oppure criticita: alta", names), "Funzione critica è sì oppure Criticità è alta");
  assert.equal(phrase("", names), "");
});
