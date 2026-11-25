import { test } from "node:test";
import assert from "node:assert/strict";
import {
  RECENT_MAX,
  buildSections,
  filterOpts,
  flatten,
  fold,
  loadRecents,
  moveActive,
  pushRecent,
  typeahead,
  type Opt,
} from "../src/selectLogic.ts";

const o = (value: string, label: string, extra: Partial<Opt> = {}): Opt => ({ value, label, ...extra });
const people = [o("1", "Anna Moretti"), o("2", "Marco Rossi"), o("3", "Àlvaro Núñez"), o("4", "Rossana Bianchi", { detail: "richiedente" }), o("5", "Giulia Marchi")];

test("la ricerca ignora maiuscole e accenti e vuole tutte le parole", () => {
  assert.equal(fold("Àlvaro"), "alvaro");
  assert.deepEqual(filterOpts(people, "alvaro").map((x) => x.value), ["3"]);
  assert.deepEqual(filterOpts(people, "ross").map((x) => x.value), ["4", "2"]);
  assert.deepEqual(filterOpts(people, "anna mor").map((x) => x.value), ["1"]);
  assert.deepEqual(filterOpts(people, "moretti anna").map((x) => x.value), ["1"]);
  assert.deepEqual(filterOpts(people, "zzz"), []);
});

test("l'inizio della parola vale più del testo in mezzo; il dettaglio conta poco", () => {
  const r = filterOpts(people, "mar").map((x) => x.value);
  assert.deepEqual(r, ["2", "5"]); // Marco, Marchi: entrambe all'inizio; resta l'ordine di partenza
  assert.deepEqual(filterOpts(people, "richiedente").map((x) => x.value), ["4"]);
});

test("senza ricerca: nessuno, io, recenti e attuale in cima, il resto dopo senza doppioni", () => {
  const s = buildSections({
    opts: people,
    query: "",
    none: o("", "Nessuno"),
    me: o("2", "Marco Rossi (io)"),
    recents: [o("5", "Giulia Marchi"), o("2", "Marco Rossi")],
    current: [o("3", "Àlvaro Núñez")],
    serverFiltered: true,
  });
  assert.deepEqual(flatten(s).map((x) => x.value), ["", "2", "5", "3", "1", "4"]);
  assert.deepEqual(s.map((x) => x.title), [undefined, undefined, "Recenti", "Attuale", "Tutti"]);
});

test("con la ricerca restano solo le corrispondenze", () => {
  const s = buildSections({ opts: people, query: "gi", none: o("", "Nessuno"), me: o("2", "io") });
  assert.deepEqual(flatten(s).map((x) => x.value), ["5"]);
});

test("scelta multipla: le voci già scelte non si ripropongono", () => {
  const s = buildSections({ opts: people, query: "", multiple: true, selected: ["1", "3"], me: o("1", "io") });
  assert.deepEqual(flatten(s).map((x) => x.value), ["2", "4", "5"]);
});

test("i gruppi (optgroup) restano raggruppati", () => {
  const s = buildSections({ opts: [o("a", "A", { group: "Gruppi" }), o("b", "B", { group: "Gruppi" }), o("c", "C", { group: "Persone" })], query: "" });
  assert.deepEqual(s.map((x) => [x.title, x.opts.length]), [["Gruppi", 2], ["Persone", 1]]);
});

test("frecce, Home, End e Pagina saltano le voci disabilitate", () => {
  const l = [o("a", "A"), o("b", "B", { disabled: true }), o("c", "C"), o("d", "D")];
  assert.equal(moveActive(l, 0, "ArrowDown"), 2);
  assert.equal(moveActive(l, 2, "ArrowUp"), 0);
  assert.equal(moveActive(l, 0, "ArrowUp"), 0);
  assert.equal(moveActive(l, 3, "ArrowDown"), 3);
  assert.equal(moveActive(l, 2, "Home"), 0);
  assert.equal(moveActive(l, 0, "End"), 3);
  assert.equal(moveActive(l, -1, "ArrowDown"), 0);
  assert.equal(moveActive([o("x", "X", { disabled: true })], -1, "ArrowDown"), -1);
});

test("ricerca per iniziale: la stessa lettera scorre, più lettere affinano", () => {
  const l = [o("1", "Alfa"), o("2", "Beta"), o("3", "Bravo"), o("4", "Delta")];
  assert.equal(typeahead(l, "b", 0), 1);
  assert.equal(typeahead(l, "b", 1), 2);
  assert.equal(typeahead(l, "b", 2), 1);
  assert.equal(typeahead(l, "br", 0), 2);
  assert.equal(typeahead(l, "x", 0), -1);
});

test("recenti: in testa, senza doppioni, al massimo RECENT_MAX, e resistono a una memoria rotta", () => {
  const mem = new Map<string, string>();
  const store = { getItem: (k: string) => mem.get(k) ?? null, setItem: (k: string, v: string) => void mem.set(k, v) };
  for (const id of ["a", "b", "c", "a", "d", "e", "f"]) pushRecent(store, "campo", id);
  const r = loadRecents(store, "campo");
  assert.equal(r.length, RECENT_MAX);
  assert.deepEqual(r, ["f", "e", "d", "a", "c"]);
  assert.deepEqual(loadRecents(store, "altro"), []);
  mem.set("regia.selettore.recenti.rotto", "{non json");
  assert.deepEqual(loadRecents(store, "rotto"), []);
  assert.deepEqual(loadRecents(undefined, "x"), []);
  assert.deepEqual(pushRecent({ getItem: () => null, setItem: () => { throw new Error("pieno"); } }, "x", "1"), ["1"]);
});
