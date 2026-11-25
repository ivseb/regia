// Il pulsante principale di un elemento è il passaggio in avanti: `pnpm test`.
import { test } from "node:test";
import assert from "node:assert/strict";
import { primaryFirst } from "../src/transitions.ts";

const t = (to: string, forward?: boolean) => ({ to, label: to, comment_required: false, gates: [], blocked: [], forward });

test("in Triage «Rimanda» non passa davanti al passaggio in avanti", () => {
  const ts = [t("Bozza", false), t("Valutazione", true), t("Respinta", false)];
  assert.deepEqual(primaryFirst(ts).map((x) => x.to), ["Valutazione", "Bozza", "Respinta"]);
});

test("senza passaggio in avanti l'ordine resta quello del processo", () => {
  const ts = [t("Bozza", false), t("Respinta", false)];
  assert.deepEqual(primaryFirst(ts), ts);
  assert.deepEqual(primaryFirst([]), []);
});
