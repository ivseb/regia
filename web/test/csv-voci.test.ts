// Importazione CSV delle voci del catalogo: `pnpm test` (node:test).
import { test } from "node:test";
import assert from "node:assert/strict";
import { csvToItems, mergeItems, parseCsv } from "../src/pages/configEditors/csvVoci.ts";

test("virgolette, separatore e righe vuote", () => {
  assert.deepEqual(parseCsv('a;b\n"x;y";"ha ""virgolette"""\n\n'), [["a", "b"], ["x;y", 'ha "virgolette"']]);
});

test("intestazione e colonne in qualsiasi ordine", () => {
  const rows = csvToItems("title,key,method,category\nCifratura,REQ-1,evidence,Dati\nSenza chiave,,manual,");
  assert.equal(rows[0].item.key, "REQ-1");
  assert.equal(rows[0].item.verification.method, "evidence");
  assert.equal(rows[0].item.category, "Dati");
  assert.equal(rows[1].error, "key");
  assert.equal(rows[1].line, 3);
});

test("senza intestazione vale l'ordine delle colonne; metodo sconosciuto segnalato", () => {
  const rows = csvToItems("R1,Titolo,Testo,Cat,boh");
  assert.equal(rows[0].item.text, "Testo");
  assert.equal(rows[0].error, "method");
});

test("l'unione aggiorna per chiave e conserva la verifica", () => {
  const cur = [{ key: "R1", title: "Vecchio", verification: { method: "manual" as const, team: "sicurezza" } }];
  const out = mergeItems(cur, csvToItems("key,title,method\nR1,Nuovo,evidence\nR2,Altro,manual"));
  assert.equal(out.length, 2);
  assert.equal(out[0].title, "Nuovo");
  assert.equal(out[0].verification.team, "sicurezza");
  assert.equal(out[0].verification.method, "evidence");
  assert.equal(out[1].verification.team, "sicurezza");
});
