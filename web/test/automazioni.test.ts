// Frasi delle regole con gli eventi e le azioni delle automazioni (docs/sviluppo/automazioni.md):
// eventPhrase, actionPhrase, ruleSentence in italiano e in inglese.
import { test } from "node:test";
import assert from "node:assert/strict";
import { setLang, tr } from "../src/i18n/core.ts";
import { actionPhrase, changeFacts, eventPhrase, formatEvent, isTimed, parseEvent, ruleSentence, type Names, type RuleRefs } from "../src/pages/processEditor/rulePhrases.ts";

const refs: RuleRefs = { reviewTypes: [], catalogs: [], loaded: true };
const names: Names = {
  roles: [{ key: "owner", name: "Responsabile" }],
  facts: { priorita: "Priorità" },
  fields: [
    { key: "priorita", name: "Priorità" },
    { key: "fine", name: "Fine" },
    { key: "note", name: "Note" },
  ],
  dates: [
    { key: "scadenza", name: "Scadenza" },
    { key: "campo.fine", name: "Fine" },
  ],
  types: [{ key: "task", name: "Attività" }],
  relations: [{ key: "blocks", name: "blocca" }],
  spaces: [{ key: "ops", name: "Operazioni" }],
};

test("gli eventi si leggono e tornano uguali", () => {
  for (const on of ["create", "enter:Valutazione", "exit:Bozza", "change:priorita", "change:titolo", "at:scadenza:-30", "at:campo.fine:7", "at:scadenza:0", "every:day", "every:week:7"]) {
    assert.equal(formatEvent(parseEvent(on)), on, on);
  }
  assert.deepEqual(parseEvent("at:campo.fine:+7"), { kind: "at", date: "campo.fine", offset: 7 });
  for (const bad of ["at:scadenza", "at:scadenza:x", "at:scadenza:4000", "every:week:8", "every:month", "change:", "change:Bad Key", "boh"]) {
    assert.equal(parseEvent(bad).kind, "unknown", bad);
  }
  assert.ok(isTimed("at:scadenza:-30") && isTimed("every:day") && isTimed("every:week:1"));
  assert.ok(!isTimed("create") && !isTimed("change:priorita"));
});

test("eventi e regole in italiano", async () => {
  await setLang("it", false);
  assert.equal(eventPhrase(tr, "change:priorita", names), "cambia il campo «Priorità»");
  assert.equal(eventPhrase(tr, "change:titolo", names), "cambia il campo «Titolo»");
  assert.equal(eventPhrase(tr, "at:scadenza:-30", names), "mancano 30 giorni a «Scadenza»");
  assert.equal(eventPhrase(tr, "at:scadenza:-1", names), "manca 1 giorno a «Scadenza»");
  assert.equal(eventPhrase(tr, "at:campo.fine:7", names), "sono passati 7 giorni da «Fine»");
  assert.equal(eventPhrase(tr, "at:scadenza:0", names), "è il giorno di «Scadenza»");
  assert.equal(eventPhrase(tr, "every:day"), "ogni giorno");
  assert.equal(eventPhrase(tr, "every:week:1"), "ogni lunedì");
  // Un evento sconosciuto resta com'è.
  assert.equal(eventPhrase(tr, "boh:x"), "boh:x");

  const comment = { kind: "add_comment", params: { text: "Scade il {{campo.fine}}" } };
  assert.equal(ruleSentence(tr, { on: "at:scadenza:-30", actions: [comment] }, refs, names), "Quando mancano 30 giorni a «Scadenza», allora aggiungi il commento «Scade il {{campo.fine}}».");
  assert.equal(ruleSentence(tr, { on: "every:week:5", when: "priorita: alta", actions: [comment] }, refs, names), "Ogni venerdì, solo se Priorità è alta, allora aggiungi il commento «Scade il {{campo.fine}}».");
  assert.equal(
    ruleSentence(tr, { on: "change:priorita", when: "prima.priorita: bassa", actions: [{ kind: "notify", group: "Ops", params: { message: "Priorità cambiata" } }] }, refs, names),
    "Quando cambia il campo «Priorità», solo se Priorità (prima) è bassa, allora avvisa il gruppo «Ops»: «Priorità cambiata».",
  );
});

test("azioni delle automazioni in italiano", async () => {
  await setLang("it", false);
  const a = (x: Record<string, unknown>) => actionPhrase(tr, x as never, refs, names);
  assert.equal(a({ kind: "set_field", params: { field: "priorita", value: "alta" } }), "imposta «Priorità» a «alta»");
  assert.equal(a({ kind: "set_field", params: { field: "fine", mode: "today", days: "7" } }), "imposta «Fine» a oggi + 7 giorni");
  assert.equal(a({ kind: "set_field", params: { field: "fine", mode: "today", days: "-1" } }), "imposta «Fine» a oggi − 1 giorno");
  assert.equal(a({ kind: "set_field", params: { field: "fine", mode: "today" } }), "imposta «Fine» a oggi");
  assert.equal(a({ kind: "set_field", params: { field: "note", mode: "clear" } }), "svuota «Note»");
  assert.equal(a({ kind: "notify", role: "owner", params: { message: "Ciao", followers: "true" } }), "avvisa chi ha il ruolo «Responsabile» e chi segue l'elemento: «Ciao»");
  assert.equal(a({ kind: "notify", member: "m1", group: "Ops", role: "owner", params: { message: "x" } }), "avvisa un membro, il gruppo «Ops» e chi ha il ruolo «Responsabile»: «x»");
  assert.equal(a({ kind: "notify", params: {} }), "avvisa …: «…»");
  assert.equal(a({ kind: "add_comment", params: { text: "Ricorda la scadenza" } }), "aggiungi il commento «Ricorda la scadenza»");
  assert.equal(
    a({ kind: "create_linked", params: { type: "task", title: "Verifica {{titolo}}", relation: "blocks" } }),
    "crea un elemento «Attività» con titolo «Verifica {{titolo}}», collegato con «blocca» (da questo al nuovo)",
  );
  assert.equal(
    a({ kind: "create_linked", params: { type: "task", title: "T", relation: "blocks", direction: "in", space: "ops" } }),
    "crea un elemento «Attività» con titolo «T», collegato con «blocca» (dal nuovo a questo) nello spazio «Operazioni»",
  );
  assert.equal(a({ kind: "webhook", params: { webhook: "Ticketing" } }), "invia il webhook «Ticketing»");
  // Un testo lungo si accorcia nella frase.
  assert.ok(a({ kind: "add_comment", params: { text: "x".repeat(100) } }).endsWith("…»"));
});

test("fatti «prima.» per gli eventi change", async () => {
  await setLang("it", false);
  const facts = [{ key: "priorita", name: "Priorità", kind: "text" as const, source: "campo" as const, values: ["alta", "bassa"] }];
  assert.deepEqual(changeFacts(tr, "change:priorita", facts, names), [{ key: "prima.priorita", name: "Priorità (prima)", kind: "text", values: ["alta", "bassa"], source: "campo" }]);
  assert.deepEqual(
    changeFacts(tr, "change:titolo", facts, names).map((f) => f.key),
    ["titolo", "prima.titolo"],
  );
  assert.deepEqual(changeFacts(tr, "create", facts, names), []);
});

test("eventi, azioni e regole in inglese", async () => {
  await setLang("en", false);
  try {
    assert.equal(eventPhrase(tr, "change:priorita", names), "the field “Priorità” changes");
    assert.equal(eventPhrase(tr, "at:scadenza:-30", names), "it is 30 days before “Scadenza”");
    assert.equal(eventPhrase(tr, "at:campo.fine:1", names), "it is 1 day after “Fine”");
    assert.equal(eventPhrase(tr, "at:scadenza:0", names), "it is the day of “Scadenza”");
    assert.equal(eventPhrase(tr, "every:week:1"), "every Monday");
    const a = (x: Record<string, unknown>) => actionPhrase(tr, x as never, refs, names);
    assert.equal(a({ kind: "set_field", params: { field: "priorita", value: "alta" } }), "set “Priorità” to “alta”");
    assert.equal(a({ kind: "set_field", params: { field: "fine", mode: "today", days: "7" } }), "set “Fine” to today + 7 days");
    assert.equal(a({ kind: "set_field", params: { field: "note", mode: "clear" } }), "clear “Note”");
    assert.equal(a({ kind: "notify", params: { message: "Hi", followers: "true" } }), "notify the item's followers: “Hi”");
    assert.equal(a({ kind: "webhook", params: { webhook: "Ticketing" } }), "send the webhook “Ticketing”");
    assert.equal(ruleSentence(tr, { on: "every:day", actions: [{ kind: "add_comment", params: { text: "Check" } }] }, refs, names), "Every day, then add the comment “Check”.");
    assert.equal(ruleSentence(tr, { on: "at:scadenza:-30", actions: [{ kind: "webhook", params: { webhook: "T" } }] }, refs, names), "When it is 30 days before “Scadenza”, then send the webhook “T”.");
  } finally {
    await setLang("it", false);
  }
});
