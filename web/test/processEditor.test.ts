// Test del modello dell'editor dei processi: `pnpm test` (node:test, nessuna dipendenza).
import { test } from "node:test";
import assert from "node:assert/strict";
import { setLang } from "../src/i18n/core.ts";

// Il catalogo si carica prima dei test: i testi dei modelli vengono da lì.
await setLang("it", false);
import {
  conditionError,
  describeCondition,
  describeDiff,
  fromModel,
  keyFrom,
  layout,
  movePhase,
  normalize,
  problemTarget,
  removePhase,
  renamePhase,
  toModel,
  unreachable,
} from "../src/pages/processEditor/model.ts";

const def = () =>
  normalize({
    phases: [
      { name: "Bozza", category: "todo" },
      { name: "Valutazione", category: "in_progress" },
      { name: "Approvazione", category: "in_progress" },
      { name: "Chiusa", category: "done" },
      { name: "Sospesa", category: "in_progress", off_path: true },
      { name: "Respinta", category: "cancelled", off_path: true },
    ],
    initial: "Bozza",
    transitions: [
      { from: ["Bozza"], to: "Valutazione", label: "Invia", roles: ["requester"] },
      { from: ["Valutazione"], to: "Approvazione", label: "Chiedi l'approvazione", gates: ["stima"], approval: "stage_gate" },
      { from: ["Approvazione"], to: "Chiusa", system: true },
      { from: ["Approvazione"], to: "Respinta", system: true },
      { from: ["Valutazione", "Approvazione"], to: "Bozza", label: "Rimanda" },
      { from: ["*"], to: "Sospesa", label: "Sospendi" },
      { from: ["Sospesa"], to: "Valutazione", label: "Riprendi" },
    ],
    gates: [{ key: "stima", name: "Stima", condition: "stima_max: presente" }],
    rules: [{ key: "comp", name: "Compliance", on: "enter:Valutazione", actions: [{ kind: "assign_role", role: "compliance", group: "Compliance" }] }],
  });

test("le condizioni semplici diventano righe e tornano uguali", () => {
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
  ]) {
    const m = toModel(src);
    assert.ok(m, src);
    assert.equal(fromModel(m), src);
  }
  assert.deepEqual(toModel(""), { join: "all", rows: [] });
  assert.equal(toModel("driver: normativo oppure")?.rows, undefined);
  // "tutte" dentro "almeno una": non è esprimibile con le righe, resta testo.
  assert.equal(toModel("a: 1 b: 2 oppure c: 3"), null);
  assert.equal(toModel("-stima_max: >10"), null);
});

test("il testo generato mette tra virgolette i valori con spazi o parole riservate", () => {
  assert.equal(fromModel({ join: "all", rows: [{ fact: "unita", op: "is", values: ["Area Clienti", "IT"] }] }), 'unita: "Area Clienti", IT');
  assert.equal(fromModel({ join: "all", rows: [{ fact: "nota", op: "is", values: ["presente"] }] }), 'nota: "presente"');
  assert.equal(fromModel({ join: "any", rows: [{ fact: "x", op: "is", values: [] }, { fact: "y", op: "absent", values: [] }] }), "y: assente");
  assert.equal(fromModel({ join: "all", rows: [{ fact: "stima_max", op: "le", values: ["50 000"] }] }), "stima_max: <=50000");
});

test("gli errori di sintassi sono quelli del motore", () => {
  assert.equal(conditionError("stima_max: >50000 driver: normativo"), null);
  for (const bad of ["stima_max", "stima_max:", "oppure stato: X", "stato: X oppure", 'stato: "aperta', "stima_max: >abc", "non", "stato: X Y"]) {
    assert.ok(conditionError(bad), bad);
  }
});

test("rinominare una fase aggiorna passaggi, fase iniziale e regole", () => {
  const d = def();
  renamePhase(d, "Valutazione", "Analisi");
  assert.equal(d.transitions[0].to, "Analisi");
  assert.deepEqual(d.transitions[4].from, ["Analisi", "Approvazione"]);
  assert.equal(d.rules![0].on, "enter:Analisi");
  renamePhase(d, "Bozza", "Proposta");
  assert.equal(d.initial, "Proposta");
});

test("togliere e spostare fasi", () => {
  const d = def();
  removePhase(d, "Approvazione");
  assert.ok(!d.transitions.some((t) => t.to === "Approvazione" || t.from.includes("Approvazione")));
  assert.deepEqual(d.transitions.find((t) => t.label === "Rimanda")?.from, ["Valutazione"]);
  const e = def();
  movePhase(e, "Chiusa", 1);
  assert.deepEqual(e.phases.map((p) => p.name).slice(0, 4), ["Bozza", "Chiusa", "Valutazione", "Approvazione"]);
  movePhase(e, "Bozza", 0, true);
  assert.ok(e.phases.find((p) => p.name === "Bozza")?.off_path);
  assert.equal(e.phases.find((p) => p.off_path)?.name, "Bozza");
});

test("fasi irraggiungibili e path dei problemi", () => {
  const d = def();
  assert.equal(unreachable(d).size, 0);
  d.phases.push({ name: "Isolata", category: "in_progress" });
  assert.deepEqual([...unreachable(d)], ["Isolata"]);
  assert.deepEqual(problemTarget("transitions[3].gates"), { kind: "transition", index: 3, field: "gates" });
  assert.deepEqual(problemTarget("rules[1].actions[0].role"), { kind: "rule", index: 1, field: "actions[0].role" });
  assert.equal(problemTarget("initial").kind, "initial");
});

test("differenze leggibili", () => {
  const before = def();
  const after = def();
  after.phases.splice(3, 0, { name: "Pianificazione", category: "in_progress" });
  after.transitions[1].gates = ["stima", "sponsor"];
  after.gates!.push({ key: "sponsor", name: "Sponsor indicato", condition: "ruolo.sponsor: presente" });
  after.transitions[0].roles = ["requester", "sponsor"];
  renamePhase(after, "Sospesa", "In attesa");
  const diff = describeDiff(before, after, { role: (k) => (k === "sponsor" ? "Sponsor" : k) });
  assert.ok(diff.includes("Aggiunta la fase «Pianificazione»."), diff.join("\n"));
  assert.ok(diff.includes("Il passaggio «Chiedi l'approvazione» verso Approvazione ora richiede il gate «Sponsor indicato»."), diff.join("\n"));
  assert.ok(diff.includes("Il passaggio «Invia» verso Valutazione ora può eseguirlo anche «Sponsor»."), diff.join("\n"));
  assert.ok(diff.includes("Rinominata la fase «Sospesa» in «In attesa»."), diff.join("\n"));
  assert.ok(diff.includes("Nuovo gate «Sponsor indicato»."), diff.join("\n"));
  assert.deepEqual(describeDiff(before, def()), []);
});

test("chiavi dai nomi", () => {
  assert.equal(keyFrom("Analisi tecnica completa", []), "analisi_tecnica_completa");
  assert.equal(keyFrom("Più è meglio", []), "piu_e_meglio");
  assert.equal(keyFrom("Stima", ["stima"]), "stima_2");
  assert.equal(keyFrom("1° controllo", []), "k_1_controllo");
});

test("disposizione del diagramma", () => {
  const l = layout(def());
  const main = l.nodes.filter((n) => !n.offPath);
  assert.equal(main.length, 4);
  assert.ok(main.every((n, i) => i === 0 || n.x > main[i - 1].x), "fasi principali in fila");
  const off = l.nodes.filter((n) => n.offPath);
  assert.deepEqual(off.map((n) => n.name).sort(), ["*", "Respinta", "Sospesa"]);
  assert.ok(off.every((n) => n.y > main[0].y), "fuori percorso sotto");
  // Nessuna sovrapposizione tra blocchi della stessa riga.
  for (const row of [main, off]) {
    const xs = row.map((n) => n.x).sort((a, b) => a - b);
    xs.forEach((x, i) => i === 0 || assert.ok(x - xs[i - 1] >= 140));
  }
  // Invia in avanti sopra la fila, Rimanda all'indietro sotto; i passaggi di sistema marcati.
  const byT = (t: number) => l.edges.filter((e) => e.transition === t);
  assert.equal(byT(0)[0].lane, "top");
  assert.equal(byT(4)[0].lane, "mid");
  assert.ok(byT(2)[0].system && byT(3)[0].system);
  assert.ok(l.width > 4 * 140 && l.height > 0);
});

test("la condizione si legge in parole, senza chiavi nude", () => {
  assert.equal(describeCondition("valutazioni_obbligatorie_aperte: >0"), "Valutazioni obbligatorie aperte maggiore di 0");
  assert.equal(
    describeCondition("classificazione_dati: personali, particolari", { classificazione_dati: "Classificazione dei dati" }),
    "Classificazione dei dati è personali, particolari",
  );
  assert.equal(describeCondition("-figli_aperti: presente stima_max: <10000"), "Figli aperti è assente e Stima max minore di 10000");
  assert.equal(describeCondition("driver: normativo oppure driver: rischio"), "Driver è normativo oppure Driver è rischio");
  assert.equal(
    describeCondition("-valutazioni_obbligatorie_aperte: >0 -valutazioni_non_favorevoli: >0"),
    "Valutazioni obbligatorie aperte non maggiore di 0 e Valutazioni non favorevoli non maggiore di 0",
  );
  assert.equal(describeCondition(""), "");
});

test("la condizione si legge in parole anche in inglese", async () => {
  await setLang("en", false);
  try {
    assert.equal(describeCondition("valutazioni_obbligatorie_aperte: >0"), "Valutazioni obbligatorie aperte is greater than 0");
    assert.equal(describeCondition("-figli_aperti: presente stima_max: <10000"), "Figli aperti is absent and Stima max is less than 10000");
    assert.equal(describeCondition("driver: normativo oppure driver: rischio"), "Driver is normativo or Driver is rischio");
    assert.equal(
      describeCondition("-valutazioni_obbligatorie_aperte: >0 -valutazioni_non_favorevoli: >0"),
      "Valutazioni obbligatorie aperte is not greater than 0 and Valutazioni non favorevoli is not greater than 0",
    );
    assert.equal(describeCondition("mfa: sì"), "Mfa is yes");
    assert.equal(conditionError("stima_max: >"), "stima_max: missing value after \">\"");
  } finally {
    await setLang("it", false);
  }
});

test("le differenze tra versioni del processo si leggono anche in inglese", async () => {
  await setLang("en", false);
  try {
    const before = def();
    const after = structuredClone(before);
    after.phases.push({ name: "Nuova", category: "in_progress" });
    const out = describeDiff(before, after).join("\n");
    assert.match(out, /Added stage “Nuova”\./);
    assert.match(describeDiff(undefined, after)[0], /First version of the process/);
  } finally {
    await setLang("it", false);
  }
});
