// Calcoli della cabina di portafoglio: linea di taglio, sovraccarico dei team, proposta di spostamento.
import { test } from "node:test";
import assert from "node:assert/strict";
import { autoCut, evalCut, suggestMove, whyHere } from "../src/pages/cabina-calc.ts";

const item = (n: number, o: Record<string, unknown> = {}) =>
  ({ id: `i${n}`, key: `DEM-${n}`, position: n, estimate_cents: 100_000_00, score: 90 - n, is_mandatory: false, pinned: false, pin_reason: null, regulatory_deadline: null, can_move: true, ...o }) as never;

const items = [item(1), item(2), item(3), item(4)];
// Un team con capacità 5 gg/settimana e già 1 giorno occupato; ogni richiesta ne chiede 2 nella prima settimana.
const cab = {
  teams: [{ id: "t1", name: "Integrazioni", capacity: [5, 5], base: [1, 0] }],
  rows: Object.fromEntries(items.map((i: { id: string }) => [i.id, { efforts: [{ team_id: "t1", days: 2, weeks: [2, 0] }] }])),
  windows: { i1: { end: "2027-01-10" }, i2: { end: "2027-03-01" }, i3: { end: "2027-02-01" } },
} as never;

test("la linea di taglio segue il budget", () => {
  assert.equal(autoCut(items, 250_000_00), 2);
  assert.equal(autoCut(items, 0), 0);
  assert.equal(autoCut(items, null), 4);
});

test("residuo, sovraccarico e fine prevista dipendono dalla linea", () => {
  const two = evalCut(cab, items, 2, 250_000_00);
  assert.equal(two.committed, 200_000_00);
  assert.equal(two.residual, 50_000_00);
  assert.equal(two.overloaded.length, 0); // 1 + 2 + 2 = 5 gg su 5
  assert.equal(two.end, "2027-03-01");
  const three = evalCut(cab, items, 3, 250_000_00);
  assert.equal(three.overloaded.length, 1); // 7 gg su 5
  assert.equal(Math.round(three.overloaded[0].peak * 100), 140);
  assert.ok(three.residual! < 0);
});

test("propone la richiesta a punteggio più basso che libera la capacità", () => {
  const s = suggestMove(cab, items, 3, "t1", true)!;
  assert.equal(s.item.key, "DEM-3");
  assert.equal(s.after?.key, "DEM-4");
  assert.equal(s.frees, true);
  assert.equal(s.to, 4);
});

test("il motivo della posizione", () => {
  const f = { score: String, date: (d: string) => d };
  const list = [item(1, { is_mandatory: true, regulatory_deadline: "2027-01-17" }), item(2), item(3, { score: 70 })];
  assert.equal(whyHere(list, 0, true, f).k, "mandatoryDeadline");
  assert.equal(whyHere(list, 1, true, f).k, "afterMandatory");
  assert.equal(whyHere(list, 2, true, f).k, "lowerScore");
});
