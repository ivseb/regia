// Note di rilascio generate (pagina /rilasci/$id): `pnpm test` (node:test).
import { test } from "node:test";
import assert from "node:assert/strict";
import { setLang, tr } from "../src/i18n/core.ts";

await setLang("it", false);
import { notesFileName, releaseNotesMarkdown } from "../src/releaseNotes.ts";
import type { ReleaseDetail, ReleaseItem } from "../src/rilasciApi.ts";

const item = (key: string, title: string, group: ReleaseItem["group"]): ReleaseItem => ({
  id: key, key, title, type: "story", type_name: "Story", state: "Fatto", state_category: "done", group, commits: 1, continues: false,
});

const detail = (items: ReleaseItem[], change = true): ReleaseDetail =>
  ({
    release: { id: "r1", service: "portale", version: "1.1.0", environment: "prod", at: "2026-11-24T10:00:00Z", change: change ? { number: "CHG0002" } : null },
    window: { from: null, to: "2026-11-24T10:00:00Z", previous_version: null },
    items, hidden: 0, commits: items.length,
  }) as unknown as ReleaseDetail;

const t = (k: string, p?: Record<string, string | number>) => tr(k as Parameters<typeof tr>[0], p);
const day = () => "24/11/2026";

test("le note sono raggruppate in novità, correzioni e altro, nell'ordine", () => {
  const md = releaseNotesMarkdown(detail([item("WEB-3", "Errore al logout", "fix"), item("WEB-1", "Login  con\nSPID", "new"), item("WEB-9", "Pulizia", "other")]), t, day);
  assert.equal(md, [
    "# portale 1.1.0", "", "Rilasciato in prod il 24/11/2026.", "", "Change CHG0002.",
    "", "## Novità", "", "- Login con SPID (WEB-1)",
    "", "## Correzioni", "", "- Errore al logout (WEB-3)",
    "", "## Altro", "", "- Pulizia (WEB-9)", "",
  ].join("\n"));
});

test("un gruppo vuoto non compare; senza elementi lo dice", () => {
  const md = releaseNotesMarkdown(detail([item("WEB-3", "Errore", "fix")], false), t, day);
  assert.ok(!md.includes("## Novità") && md.includes("## Correzioni") && !md.includes("Change"));
  assert.ok(releaseNotesMarkdown(detail([]), t, day).includes("Nessun elemento collegato."));
});

test("il nome del file è pulito", () => {
  assert.equal(notesFileName("acme/portale", "1.1.0+build 7"), "note-acme-portale-1.1.0-build-7.md");
});
