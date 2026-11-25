// Test del modello dell'editor dei questionari e del punteggio: `pnpm test` (node:test).
// Il calcolo deve dare gli stessi numeri di internal/modules/reviews/scoring_test.go.
import { test } from "node:test";
import assert from "node:assert/strict";
import { setLang } from "../src/i18n/core.ts";

// Il catalogo si carica prima dei test: i testi dei modelli vengono da lì.
await setLang("it", false);
import { evaluate, points, proposedOutcome, type QDefinition } from "../src/condition.ts";
import {
  analyze,
  changeType,
  clean,
  describeDiff,
  extremeAnswers,
  factsBefore,
  keyFrom,
  moveQuestion,
  moveSection,
  newQuestion,
  scoreTable,
  usedFacts,
} from "../src/pages/questionnaireEditor/model.ts";

const def = (): QDefinition => ({
  name: "Punteggio",
  sections: [
    {
      key: "a",
      title: "A",
      questions: [
        { key: "mfa", label: "MFA?", type: "yes_no", yes_score: 10, no_score: 0 },
        {
          key: "cifratura",
          label: "Cifratura",
          type: "single",
          weight: 2,
          options: [
            { value: "nessuna", label: "Nessuna", score: 0 },
            { value: "riposo", label: "A riposo", score: 5 },
            { value: "totale", label: "Totale", score: 10 },
          ],
        },
        { key: "maturita", label: "Maturità", type: "scale", reverse: true },
        {
          key: "controlli",
          label: "Controlli",
          type: "matrix",
          rows: [
            { value: "log", label: "Log" },
            { value: "backup", label: "Backup" },
          ],
          options: [
            { value: "no", label: "No", score: 0 },
            { value: "si", label: "Sì", score: 4 },
          ],
        },
        { key: "note", label: "Note", type: "text" },
        { key: "extra", label: "Extra", type: "yes_no", yes_score: 10, visible_when: "mfa: no" },
      ],
    },
  ],
  scoring: {
    thresholds: [
      { min: 0, outcome: "non_favorevole" },
      { min: 50, outcome: "favorevole_con_prescrizioni" },
      { min: 80, outcome: "favorevole" },
    ],
  },
});

test("punteggio: stessi numeri del server", () => {
  const d = def();
  let ev = evaluate(d, {}, {});
  assert.equal(ev.scored, true);
  assert.equal(ev.max_score, 42);
  assert.equal(ev.score, 0);
  assert.equal(ev.proposed_outcome, "non_favorevole");

  ev = evaluate(d, {}, { mfa: true, cifratura: "totale", maturita: 1, controlli: { log: "si", backup: "si" } });
  assert.equal(ev.percent, 100);
  assert.equal(ev.proposed_outcome, "favorevole");

  // Con "mfa: no" la domanda extra compare e fa salire il massimo.
  ev = evaluate(d, {}, { mfa: false, cifratura: "riposo", maturita: 5, controlli: { log: "si" } });
  assert.equal(ev.score, 14);
  assert.equal(ev.max_score, 52);
  assert.equal(ev.answered, 3, "la matrice a metà non conta come risposta");

  d.sections[0].questions[1].weight = 0;
  assert.equal(evaluate(d, {}, {}).max_score, 22);
});

test("soglie: vale la più alta raggiunta", () => {
  const th = def().scoring!.thresholds;
  assert.equal(proposedOutcome(49.9, th), "non_favorevole");
  assert.equal(proposedOutcome(50, th), "favorevole_con_prescrizioni");
  assert.equal(proposedOutcome(100, th), "favorevole");
  assert.equal(proposedOutcome(10, [{ min: 20, outcome: "x" }]), "");
});

test("matrice e scala nelle condizioni", () => {
  const d: QDefinition = {
    name: "c",
    sections: [
      {
        key: "a",
        title: "A",
        questions: [
          { key: "m", label: "M", type: "matrix", rows: [{ value: "a", label: "A" }], options: [{ value: "x", label: "X" }, { value: "y", label: "Y" }] },
          { key: "s", label: "S", type: "scale" },
          { key: "dopo", label: "D", type: "text", visible_when: "m: a.x s: >=3" },
        ],
      },
    ],
  };
  assert.equal(evaluate(d, {}, { m: { a: "x" }, s: 4 }).visible.dopo, true);
  assert.equal(evaluate(d, {}, { m: { a: "y" }, s: 4 }).visible.dopo, false);
  assert.equal(evaluate(d, {}, { m: { a: "x" }, s: 2 }).visible.dopo, false);
});

test("punti per tipo", () => {
  assert.deepEqual(points({ key: "q", label: "", type: "scale", min: 1, max: 5 }, 4), [3, 4]);
  assert.deepEqual(points({ key: "q", label: "", type: "scale", min: 1, max: 5, reverse: true }, 4), [1, 4]);
  assert.deepEqual(points({ key: "q", label: "", type: "text" }, "x"), [0, 0]);
  const multi = { key: "q", label: "", type: "multi" as const, options: [{ value: "a", label: "A", score: 2 }, { value: "b", label: "B", score: 3 }] };
  assert.deepEqual(points(multi, ["a", "b"]), [5, 5]);
});

test("analisi: domande irraggiungibili, condizioni in avanti, sezioni vuote", () => {
  const d: QDefinition = {
    name: "x",
    sections: [
      {
        key: "a",
        title: "A",
        questions: [
          { key: "auth", label: "Autenticazione", type: "single", options: [{ value: "sso", label: "SSO" }, { value: "locale", label: "Locale" }] },
          { key: "mai", label: "Mai", type: "text", visible_when: "auth: ldap" },
          { key: "figlia", label: "Figlia", type: "text", visible_when: "mai: presente", required: true },
          { key: "avanti", label: "Avanti", type: "text", visible_when: "dopo: sì" },
          { key: "dopo", label: "Dopo", type: "yes_no" },
        ],
      },
      { key: "vuota", title: "Vuota", questions: [] },
    ],
    derivations: [{ key: "r", when: "mai: presente", text: "x" }],
  };
  const f = analyze(d, ["criticita"]);
  const has = (code: string, path: string) => f.some((x) => x.code === code && x.path === path);
  assert.ok(has("unreachable", "sections[0].questions[1].visible_when"), "valore impossibile");
  assert.ok(has("unreachable", "sections[0].questions[2].visible_when"), "dipende da una domanda mai mostrata");
  assert.ok(has("required", "sections[0].questions[2].required"));
  assert.ok(has("forward", "sections[0].questions[3].visible_when"), "domanda successiva");
  assert.ok(has("empty", "sections[1].questions"));
  assert.ok(has("unreachable", "derivations[0].when"));
  // Un questionario sano non ha segnalazioni.
  assert.equal(analyze(def(), []).filter((x) => x.level === "error").length, 0);
});

test("fatti per il costruttore: solo le domande che vengono prima", () => {
  const d = def();
  const f = factsBefore(d, [{ key: "criticita", name: "Criticità", kind: "text", source: "base" }], { question: "controlli" });
  assert.deepEqual(f.map((x) => x.key), ["mfa", "cifratura", "maturita", "criticita"]);
  assert.equal(f[0].kind, "bool");
  assert.deepEqual(f[1].values, ["nessuna", "riposo", "totale"]);
  const all = factsBefore(d, [], { section: 0 });
  assert.equal(all.length, 0, "una sezione vede solo ciò che sta prima");
});

test("spostamenti", () => {
  const d: QDefinition = {
    name: "x",
    sections: [
      { key: "a", title: "A", questions: [newQuestion("text", [], "Uno"), newQuestion("text", ["uno"], "Due")] },
      { key: "b", title: "B", questions: [newQuestion("yes_no", [], "Tre")] },
    ],
  };
  moveQuestion(d, "uno", 0, 2);
  assert.deepEqual(d.sections[0].questions.map((q) => q.key), ["due", "uno"]);
  moveQuestion(d, "due", 1);
  assert.deepEqual(d.sections[1].questions.map((q) => q.key), ["tre", "due"]);
  moveQuestion(d, "due", 1, 0);
  assert.deepEqual(d.sections[1].questions.map((q) => q.key), ["due", "tre"]);
  moveSection(d, 0, 1);
  assert.deepEqual(d.sections.map((s) => s.key), ["b", "a"]);
});

test("tipi: cambio e pulizia", () => {
  const q = newQuestion("single", [], "Scelta");
  const m = changeType(q, "matrix");
  assert.equal(m.type, "matrix");
  assert.equal(m.options?.length, 2);
  assert.ok(m.rows?.length);
  const t = changeType(m, "text");
  assert.equal(t.options, undefined);
  const d = clean({ name: "x", description: "", sections: [{ key: "a", title: "A", description: "", questions: [{ ...newQuestion("text", [], "Q"), help: "" }] }], derivations: [] });
  assert.equal("description" in d, false);
  assert.equal("derivations" in d, false);
  assert.equal("help" in d.sections[0].questions[0], false);
});

test("chiavi", () => {
  assert.equal(keyFrom("Dove sono i dati?", []), "dove_sono_i_dati");
  assert.equal(keyFrom("Perché è così", ["perche_e_cosi"]), "perche_e_cosi_2");
  assert.equal(keyFrom("123", []), "voce");
});

test("fatti usati e tabella dei punteggi", () => {
  const d = def();
  d.sections[0].questions.push({ key: "q9", label: "Q9", type: "text", visible_when: "criticita: alta mfa: no", prefill: "classificazione_dati" });
  assert.deepEqual(usedFacts(d), ["classificazione_dati", "criticita"]);
  const t = scoreTable(d);
  assert.equal(t.length, 5);
  assert.ok(Math.abs(t.reduce((a, r) => a + r.share, 0) - 1) < 1e-9);
});

test("risposte di prova: migliori e peggiori", () => {
  const d = def();
  assert.equal(evaluate(d, {}, extremeAnswers(d)).percent, 100);
  assert.equal(evaluate(d, {}, extremeAnswers(d, true)).percent, 0);
});

test("differenze tra versioni in italiano", () => {
  const a = def();
  const b = structuredClone(a);
  b.sections[0].questions[0].required = true;
  b.sections[0].questions[1].options![1].score = 6;
  b.sections[0].questions.splice(4, 1);
  b.sections[0].questions.push(newQuestion("date", ["x"], "Scadenza"));
  b.scoring!.thresholds[1].min = 60;
  b.sections.push({ key: "n", title: "Nuova", filled_by: "team", questions: [newQuestion("text", [], "Altro")] });
  const out = describeDiff(a, b, (k) => k);
  const all = out.join("\n");
  assert.match(all, /«MFA\?» diventa obbligatoria/);
  assert.match(all, /«A riposo» vale 6 punti \(prima 5\)/);
  assert.match(all, /Tolta la domanda «Note»/);
  assert.match(all, /Nuova domanda «Scadenza»/);
  assert.match(all, /Soglie del punteggio: .*da 60%/);
  assert.match(all, /Nuova sezione «Nuova»/);
  assert.equal(describeDiff(a, structuredClone(a)).length, 0);
  assert.match(describeDiff(null, a)[0], /Prima versione/);
});

test("differenze tra versioni in inglese", async () => {
  await setLang("en", false);
  try {
    const a = def();
    const b = structuredClone(a);
    b.sections[0].questions[0].required = true;
    b.sections[0].questions[1].options![1].score = 6;
    b.sections[0].questions.splice(4, 1);
    b.sections[0].questions.push(newQuestion("date", ["x"], "Scadenza"));
    b.scoring!.thresholds[1].min = 60;
    b.sections.push({ key: "n", title: "Nuova", filled_by: "team", questions: [newQuestion("text", [], "Altro")] });
    const all = describeDiff(a, b, (k) => k).join("\n");
    assert.match(all, /The question “MFA\?” becomes required/);
    assert.match(all, /“A riposo” is worth 6 points \(was 5\)/);
    assert.match(all, /Removed question “Note”/);
    assert.match(all, /New question “Scadenza”/);
    assert.match(all, /Score thresholds: .*from 60%/);
    assert.match(all, /New section “Nuova”/);
    assert.match(describeDiff(null, a)[0], /First version/);
    assert.equal(newQuestion("single", []).options?.[0].label, "First choice");
  } finally {
    await setLang("it", false);
  }
});
