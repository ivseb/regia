// Processi guidati: «Aggiungi un passo», parità di priorità, accettazione del rischio e scadenze a righe.
import { test } from "node:test";
import assert from "node:assert/strict";
import { setLang } from "../src/i18n/core.ts";

await setLang("it", false);
import { addStep, normalize, priorityTies } from "../src/pages/processEditor/model.ts";
import { approverKind, approverOf, cleanSteps, dueOf, dueText, ordered, parseDue, parseSteps, stepsText, timeline } from "../src/pages/riskConfig/model.ts";

const def = () =>
  normalize({
    phases: [
      { name: "Bozza", category: "todo" },
      { name: "Valutazione", category: "in_progress" },
      { name: "Approvazione", category: "in_progress" },
      { name: "Chiusa", category: "done" },
      { name: "Sospesa", category: "in_progress", off_path: true },
    ],
    initial: "Bozza",
    transitions: [
      { from: ["Bozza"], to: "Valutazione", label: "Invia", roles: ["requester"] },
      { from: ["Valutazione"], to: "Approvazione", label: "Chiedi l'approvazione", gates: ["stima"], approval: "stage_gate" },
      { from: ["Approvazione"], to: "Chiusa", system: true },
      { from: ["*"], to: "Sospesa", label: "Sospendi" },
    ],
    gates: [{ key: "stima", name: "Stima", condition: "stima_max: presente" }],
  });

test("aggiungere un passo: fase, passaggio con ruoli e condizione, gate e rilancio verso la fase che seguiva", () => {
  const d = def();
  const name = addStep(d, { after: "Valutazione", name: "Collaudo", roles: ["pmo"], when: "stima_max: >1000", gate: { name: "Collaudo fatto", condition: "stima_max: presente", message: "Manca la stima" } });
  assert.equal(name, "Collaudo");
  assert.deepEqual(d.phases.filter((p) => !p.off_path).map((p) => p.name), ["Bozza", "Valutazione", "Collaudo", "Approvazione", "Chiusa"]);
  const entering = d.transitions.find((t) => t.to === "Collaudo")!;
  assert.deepEqual(entering.from, ["Valutazione"]);
  assert.deepEqual(entering.roles, ["pmo"]);
  assert.equal(entering.when, "stima_max: >1000");
  assert.deepEqual(entering.gates, ["collaudo_fatto"]);
  assert.ok(d.gates!.some((g) => g.key === "collaudo_fatto" && g.condition === "stima_max: presente"));
  // Il vecchio passaggio Valutazione → Approvazione (con gate e approvazione) ora parte dal passo nuovo.
  assert.ok(!d.transitions.some((t) => t.to === "Approvazione" && t.from.includes("Valutazione")));
  const out = d.transitions.find((t) => t.to === "Approvazione" && t.from.includes("Collaudo"))!;
  assert.equal(out.label, "Chiedi l'approvazione");
  assert.deepEqual(out.gates, ["stima"]);
  assert.equal(out.approval, "stage_gate");
});

test("aggiungere un passo: nome già usato e senza fase successiva", () => {
  const d = def();
  assert.equal(addStep(d, { after: "Chiusa", name: "Bozza", roles: [], when: "" }), "Bozza 2");
  const e = normalize({ phases: [{ name: "Aperto", category: "todo" }], initial: "Aperto", transitions: [] });
  addStep(e, { after: "Aperto", name: "Fatto", roles: [], when: "" });
  assert.deepEqual(e.transitions.map((t) => [t.from, t.to]), [[["Aperto"], "Fatto"]]);
});

test("priorità a parità: chi vale per tutto, chi ha la stessa condizione, chi forse", () => {
  const others = [
    { key: "a", name: "Standard", type: "bug", priority: 100, applies_when: "", archived: false },
    { key: "b", name: "Critico", type: "bug", priority: 100, applies_when: "gravita: alta", archived: false },
    { key: "c", name: "Vecchio", type: "bug", priority: 100, applies_when: "", archived: true },
    { key: "d", name: "Altro tipo", type: "task", priority: 100, applies_when: "", archived: false },
    { key: "e", name: "Diversa", type: "bug", priority: 90, applies_when: "", archived: false },
  ];
  assert.deepEqual(priorityTies(others, { key: "me", type: "bug", priority: 100, when: "gravita: alta" }), [
    { name: "Standard", kind: "all" },
    { name: "Critico", kind: "same" },
  ]);
  assert.deepEqual(priorityTies(others, { key: "me", type: "bug", priority: 100, when: "area: web" }).map((x) => x.kind), ["all", "maybe"]);
});

test("accettazione del rischio: il testo grezzo e le righe sono la stessa cosa", () => {
  const steps = parseSteps("risk_owner + organo:cda → membro:m1");
  assert.deepEqual(steps, [{ approvers: [{ role: "risk_owner" }, { body: "cda" }] }, { approvers: [{ member: "m1" }] }]);
  assert.equal(stepsText(steps), "risk_owner + organo:cda → membro:m1");
  assert.deepEqual(steps[0].approvers.map(approverKind), ["role", "body"]);
  assert.deepEqual(approverOf("member", "x"), { member: "x" });
  // Le righe lasciate vuote non si salvano.
  assert.deepEqual(cleanSteps([{ approvers: [{ role: "" }] }, { approvers: [{ role: "pmo" }, { body: " " }] }]), [{ approvers: [{ role: "pmo" }] }]);
});

test("scadenze degli incidenti: formato compatibile e linea del tempo", () => {
  const due = parseDue("detected + 24h, classified + 3g");
  assert.deepEqual(due, [{ from: "detected", hours: 24 }, { from: "classified", days: 3 }]);
  assert.equal(dueText(due), "detected + 24h, classified + 3g");
  assert.deepEqual(dueOf("detected", 2, "days"), { from: "detected", days: 2 });
  const stages = [
    { key: "pre", name: "Preavviso", due: [{ from: "detected", hours: 24 }] },
    { key: "int", name: "Intermedia", due: [{ from: "stage:pre", hours: 48 }, { from: "detected", days: 5 }] },
    { key: "fin", name: "Finale", due: [{ from: "stage:zzz", days: 30 }] },
  ];
  const t = timeline(stages);
  assert.deepEqual(t.map((x) => x.hours), [24, 72, null]);
  assert.deepEqual(ordered(t).map((x) => x.key), ["pre", "int"]);
});
