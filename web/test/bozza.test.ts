// Differenze della bozza («Cosa cambia»): `pnpm test` (node:test).
import { test } from "node:test";
import assert from "node:assert/strict";
import { setLang } from "../src/i18n/core.ts";

await setLang("it", false);
import { diffFields, diffList, show } from "../src/casa/differenze.ts";

test("i valori si leggono in parole", () => {
  assert.equal(show(true), "sì");
  assert.equal(show(false), "no");
  assert.equal(show(""), "—");
  assert.equal(show(undefined), "—");
  assert.equal(show(["a", "b"]), "a, b");
  assert.equal(show(40), "40");
});

test("restano solo i campi che cambiano", () => {
  const d = diffFields([
    { label: "Nome", before: "A", after: "B" },
    { label: "Uguale", before: "x", after: "x" },
    { label: "Attivo", before: true, after: false },
    { label: "Vuoto", before: undefined, after: "" },
  ]);
  assert.deepEqual(d, [
    { label: "Nome", before: "A", after: "B" },
    { label: "Attivo", before: "sì", after: "no" },
  ]);
});

test("un elenco dà aggiunte, rimozioni e cambi", () => {
  type R = { id: string; on: boolean };
  const d = diffList<R>(
    "Regola",
    [{ id: "a", on: true }, { id: "b", on: true }],
    [{ id: "a", on: false }, { id: "c", on: true }],
    (r) => r.id,
    (r) => r.id.toUpperCase(),
    (r) => (r.on ? "accesa" : "spenta"),
  );
  assert.deepEqual(d, [
    { label: "Regola: A", before: "accesa", after: "spenta" },
    { label: "Regola: C", before: "non c'era", after: "accesa" },
    { label: "Regola: B", before: "accesa", after: "tolto" },
  ]);
});
