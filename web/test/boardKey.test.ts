// Chiave proposta per la board di un team: `pnpm test` (node:test). Deve dare le stesse chiavi
// di ProposeKey in internal/modules/work/spaces.go.
import { test } from "node:test";
import assert from "node:assert/strict";
import { BOARD_KEY_RE, proposeKey } from "../src/boardKey.ts";

test("la chiave proposta segue quella del server", () => {
  assert.equal(proposeKey("Web"), "WEB");
  assert.equal(proposeKey("Integrazioni"), "INT");
  assert.equal(proposeKey("Dati e analisi"), "DA");
  assert.equal(proposeKey("Customer Experience"), "CE");
  assert.equal(proposeKey("Città"), "CIT");
  assert.equal(proposeKey("Home lab"), "HL");
  assert.equal(proposeKey("3D"), "T3D");
  assert.equal(proposeKey("  "), "TEAM");
});

test("forma della chiave", () => {
  assert.ok(BOARD_KEY_RE.test("LAB"));
  assert.ok(BOARD_KEY_RE.test("HL_2"));
  assert.ok(!BOARD_KEY_RE.test("1X"));
  assert.ok(!BOARD_KEY_RE.test("lab"));
});
