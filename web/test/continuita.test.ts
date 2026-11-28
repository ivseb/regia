// Continuità operativa: funzioni pure del modello (web/src/continuityModel.ts). Minuti in forma
// leggibile, proposta della BIA (stessa regola del backend), frasi delle incoerenze, raggruppamento
// per gravità, azioni dirette. `pnpm test` (node:test).
import { test } from "node:test";
import assert from "node:assert/strict";
import { setLang } from "../src/i18n/core.ts";

await setLang("it", false);
import {
  actionLink,
  fmtMinutes,
  gridComplete,
  groupBySeverity,
  issueText,
  proposeBia,
  splitMinutes,
  testMonth,
  toMinutes,
  withinLimit,
  type Config,
  type Issue,
} from "../src/continuityModel.ts";

test("fmtMinutes: giorni, ore e minuti nella lingua corrente", async () => {
  assert.equal(fmtMinutes(90), "1 h 30 min");
  assert.equal(fmtMinutes(1440), "1 g");
  assert.equal(fmtMinutes(60), "1 h");
  assert.equal(fmtMinutes(45), "45 min");
  assert.equal(fmtMinutes(0), "0 min");
  assert.equal(fmtMinutes(1440 + 120 + 5), "1 g 2 h 5 min");
  assert.equal(fmtMinutes(10080), "7 g");
  assert.equal(fmtMinutes(null), "—");
  assert.equal(fmtMinutes(undefined), "—");
  await setLang("en", false);
  try {
    assert.equal(fmtMinutes(1440), "1 d");
    assert.equal(fmtMinutes(90), "1 h 30 min");
  } finally {
    await setLang("it", false);
  }
});

test("splitMinutes e toMinutes: l'unità più comoda e ritorno in minuti", () => {
  assert.deepEqual(splitMinutes(2880), { value: "2", unit: "d" });
  assert.deepEqual(splitMinutes(240), { value: "4", unit: "h" });
  assert.deepEqual(splitMinutes(90), { value: "90", unit: "min" });
  assert.deepEqual(splitMinutes(null), { value: "", unit: "h" });
  assert.equal(toMinutes("4", "h"), 240);
  assert.equal(toMinutes("1,5", "h"), 90);
  assert.equal(toMinutes("3", "d"), 4320);
  assert.equal(toMinutes("", "h"), null);
  assert.equal(toMinutes("abc", "h"), null);
  assert.equal(toMinutes("-1", "h"), null);
  for (const m of [15, 60, 90, 240, 1440, 4320, 10080]) {
    const s = splitMinutes(m);
    assert.equal(toMinutes(s.value, s.unit), m, String(m));
  }
});

// Pacchetto di partenza: 4 categorie su 5 livelli (intollerabile 4), orizzonti 1 h … 1 settimana.
const cfg: Config = {
  categories: ["economico", "normativo", "reputazionale", "clienti"].map((key) => ({
    key,
    name: key,
    intolerable: 4,
    levels: [1, 2, 3, 4, 5].map((value) => ({ value, label: `L${value}` })),
  })),
  // In disordine apposta: la regola li ordina per minuti.
  horizons: [
    { key: "1g", name: "1 giorno", minutes: 1440 },
    { key: "1h", name: "1 ora", minutes: 60 },
    { key: "4h", name: "4 ore", minutes: 240 },
    { key: "1w", name: "1 settimana", minutes: 10080 },
    { key: "3g", name: "3 giorni", minutes: 4320 },
  ],
  test_types: [],
  test_max_age_days: 365,
  bia_review_months: 12,
  critical_mtpd_minutes: 4320,
};

test("proposta: MTPD = primo orizzonte intollerabile, RTO = quello prima", () => {
  const p = proposeBia(cfg, { economico: { "1h": 1, "4h": 2, "1g": 3, "3g": 4, "1w": 5 }, clienti: { "1h": 1, "4h": 2, "1g": 4 } });
  assert.deepEqual(p, { mtpd_minutes: 1440, rto_minutes: 240, critical: true, category: "clienti", horizon: "1g" });
});

test("proposta: a parità di orizzonte vince la prima categoria della configurazione", () => {
  const p = proposeBia(cfg, { normativo: { "4h": 5 }, economico: { "4h": 4 } });
  assert.equal(p.category, "economico");
  assert.equal(p.horizon, "4h");
  assert.equal(p.mtpd_minutes, 240);
  assert.equal(p.rto_minutes, 60);
});

test("proposta: intollerabile già al primo orizzonte → RTO metà del primo", () => {
  const p = proposeBia(cfg, { reputazionale: { "1h": 4 } });
  assert.equal(p.mtpd_minutes, 60);
  assert.equal(p.rto_minutes, 30);
  assert.equal(p.critical, true);
});

test("proposta: nessuna categoria intollerabile → MTPD nullo e RTO = ultimo orizzonte", () => {
  const p = proposeBia(cfg, { economico: { "1h": 1, "1w": 3 } });
  assert.deepEqual(p, { mtpd_minutes: null, rto_minutes: 10080, critical: false, category: null, horizon: null });
  assert.equal(proposeBia(cfg, {}).rto_minutes, 10080);
});

test("proposta: critica solo con MTPD entro la soglia", () => {
  assert.equal(proposeBia(cfg, { economico: { "3g": 4 } }).critical, true); // 4320 <= 4320
  assert.equal(proposeBia(cfg, { economico: { "1w": 4 } }).critical, false);
  assert.equal(proposeBia({ ...cfg, critical_mtpd_minutes: 240 }, { economico: { "1g": 4 } }).critical, false);
});

test("proposta: senza orizzonti non propone nulla", () => {
  assert.deepEqual(proposeBia({ ...cfg, horizons: [] }, { economico: { "1h": 5 } }), { mtpd_minutes: null, rto_minutes: null, critical: false, category: null, horizon: null });
});

test("griglia piena: ogni categoria ha un livello per ogni orizzonte", () => {
  const full = Object.fromEntries(cfg.categories.map((c) => [c.key, Object.fromEntries(cfg.horizons.map((h) => [h.key, 1]))]));
  assert.equal(gridComplete(cfg, full), true);
  delete full.clienti["1w"];
  assert.equal(gridComplete(cfg, full), false);
});

const ref = (id: string, title: string) => ({ id, key: id.toUpperCase(), title });
const issue = (p: Partial<Issue>): Issue => ({
  code: "rto_over_required",
  ref: "x",
  severity: "high",
  application: null,
  capability: null,
  other: null,
  declared: null,
  required: null,
  other_value: null,
  days: null,
  source: null,
  team: null,
  action: { kind: "set_rto", target: "APP-1" },
  ...p,
});

test("frasi delle incoerenze", () => {
  const app = ref("app-1", "Bonifici");
  assert.equal(
    issueText(issue({ code: "dependency_slower", application: app, other: { ...ref("app-2", "Core banking"), kind: "application" }, declared: 240, other_value: 1440 })),
    "Bonifici ha RTO 4 h ma dipende da Core banking con RTO 1 g",
  );
  // Senza RTO dichiarato conta il richiesto (RTO effettivo).
  assert.equal(
    issueText(issue({ code: "dependency_slower", application: app, other: { ...ref("p-1", "Cloud"), kind: "provider" }, required: 60, other_value: 240 })),
    "Bonifici ha RTO 1 h ma dipende da Cloud con RTO 4 h",
  );
  assert.equal(
    issueText(issue({ code: "rto_over_required", application: app, declared: 1440, required: 240, source: ref("bia-1", "Pagamenti") })),
    "Bonifici ha RTO 1 g, ma la BIA Pagamenti chiede 4 h",
  );
  assert.equal(issueText(issue({ code: "rpo_over_required", application: app, declared: 120, required: 15 })), "Bonifici ha RPO 2 h, ma serve 15 min");
  assert.equal(issueText(issue({ code: "test_missing", capability: ref("c-1", "Pagamenti") })), "Pagamenti è una funzione critica che non ha mai chiuso un test");
  assert.equal(issueText(issue({ code: "test_missing", application: app, days: 400 })), "Bonifici è una funzione critica e l'ultimo test chiuso è di 400 giorni fa");
  assert.equal(issueText(issue({ code: "bia_review_due", capability: ref("c-1", "Pagamenti"), days: 380 })), "La BIA di Pagamenti è stata approvata 380 giorni fa: va rivista");
  assert.equal(issueText(issue({ code: "codice_nuovo" })), "codice_nuovo");
});

test("ogni code ha la sua frase in italiano e in inglese", async () => {
  const codes = [
    "rto_over_required",
    "rpo_over_required",
    "rto_missing",
    "dependency_slower",
    "dependency_rto_missing",
    "critical_not_declared",
    "plan_missing",
    "test_missing",
    "test_failed",
    "bia_missing",
    "bia_review_due",
  ];
  const full = (code: string) =>
    issue({
      code,
      application: ref("a", "A"),
      capability: ref("c", "C"),
      other: { ...ref("o", "O"), kind: "application" },
      declared: 60,
      required: 30,
      other_value: 120,
      days: 10,
      source: ref("b", "B"),
    });
  for (const lang of ["it", "en"] as const) {
    await setLang(lang, false);
    for (const c of codes) {
      const s = issueText(full(c));
      assert.notEqual(s, c, `${lang}: manca la frase di ${c}`);
      assert.doesNotMatch(s, /\{\w+\}/, `${lang}: segnaposto rimasto in ${c}`);
    }
  }
  await setLang("it", false);
});

test("raggruppamento per gravità: prima le alte, ordine conservato, niente gruppi vuoti", () => {
  const items = [issue({ ref: "1", severity: "medium" }), issue({ ref: "2", severity: "high" }), issue({ ref: "3", severity: "medium" }), issue({ ref: "4", severity: "high" })];
  const g = groupBySeverity(items);
  assert.deepEqual(
    g.map((x) => [x.severity, x.items.map((i) => i.ref)]),
    [
      ["high", ["2", "4"]],
      ["medium", ["1", "3"]],
    ],
  );
  assert.deepEqual(groupBySeverity([issue({ severity: "medium" })]).map((x) => x.severity), ["medium"]);
  assert.deepEqual(groupBySeverity([]), []);
});

test("azioni dirette: dove porta ogni tipo", () => {
  assert.deepEqual(actionLink({ kind: "set_rto", target: "APP-1" }), { to: "/e/$key", params: { key: "APP-1" } });
  assert.deepEqual(actionLink({ kind: "set_provider_rto", target: "FOR-3" }), { to: "/e/$key", params: { key: "FOR-3" } });
  assert.deepEqual(actionLink({ kind: "open_test", target: "ARC-9" }), { to: "/e/$key", params: { key: "ARC-9" } });
  assert.deepEqual(actionLink({ kind: "plan_test", target: "APP-1" }, 2026), { to: "/architettura/continuita/test", search: { anno: 2026, riga: "APP-1" } });
  assert.deepEqual(actionLink({ kind: "start_bia", target: "CAP-2" }), { to: "/architettura/continuita/bia", search: { capability: "CAP-2" } });
  assert.deepEqual(actionLink({ kind: "write_plan", target: "APP-1" }), { to: "/documenti" });
});

test("confronti e mesi del piano", () => {
  assert.equal(withinLimit(60, 240), true);
  assert.equal(withinLimit(240, 240), true);
  assert.equal(withinLimit(300, 240), false);
  assert.equal(withinLimit(null, 240), null);
  assert.equal(withinLimit(60, null), null);
  assert.equal(testMonth({ planned_on: "2026-03-10", executed_on: null }, 2026), 2);
  assert.equal(testMonth({ planned_on: "2026-03-10", executed_on: "2026-04-02" }, 2026), 3);
  assert.equal(testMonth({ planned_on: "2025-12-31", executed_on: null }, 2026), null);
  assert.equal(testMonth({ planned_on: null, executed_on: null }, 2026), null);
});
