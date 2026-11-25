// Catalogo dei testi (web/src/i18n): stesse chiavi e segnaposto in italiano e inglese, nessuna
// chiave doppia tra le aree, ogni `code` degli errori del backend ha la sua voce, e la
// funzione t() con interpolazione, plurali e formati. `pnpm test`.
import { test } from "node:test";
import assert from "node:assert/strict";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { errorText, interpolate, lookup, setLang, tr } from "../src/i18n/core.ts";
import { fmtCurrency, fmtDate, fmtDuration, loc } from "../src/i18n/format.ts";
import { browserLang } from "../src/i18n/languages.ts";
import itAll from "../src/i18n/it/index.ts";
import enAll from "../src/i18n/en/index.ts";

const here = dirname(fileURLToPath(import.meta.url));
const i18n = resolve(here, "..", "src", "i18n");
const root = resolve(here, "..", "..");

type Entry = string | Record<string, string>;
const placeholders = (e: Entry): string[] =>
  (typeof e === "string" ? [e] : Object.values(e)).flatMap((s) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1])).sort();
const shape = (e: Entry) => (typeof e === "string" ? "text" : "plural");

/** I file di area di una lingua (ogni .ts di it/ o en/ tranne index e il file generato): [nome, catalogo]. */
const areaNames = (lang: "it" | "en") =>
  readdirSync(join(i18n, lang))
    .filter((f) => f.endsWith(".ts") && f !== "index.ts" && f !== "areas.gen.ts")
    .map((f) => f.slice(0, -3))
    .sort();
async function areas(lang: "it" | "en"): Promise<[string, Record<string, Entry>][]> {
  const names = areaNames(lang);
  assert.ok(names.length >= 4, `nessuna area trovata in ${lang}/`);
  return Promise.all(names.map(async (n) => [n, (await import(`../src/i18n/${lang}/${n}.ts`)).default as Record<string, Entry>] as [string, Record<string, Entry>]));
}

test("ogni file di area in it/ ha il gemello in en/ e viceversa", () => {
  assert.deepEqual(areaNames("en"), areaNames("it"), "le aree di it/ e di en/ non coincidono");
  for (const sub of ["lavoro"]) {
    const list = (l: string) => readdirSync(join(i18n, l, sub)).filter((f) => f.endsWith(".ts") && f !== "areas.gen.ts").sort();
    assert.deepEqual(list("en"), list("it"), `i gruppi di ${sub}/ non coincidono tra it/ e en/`);
  }
});

test("ogni file di area ha solo chiavi col suo prefisso, e nessuna chiave è doppia", async () => {
  const seen = new Map<string, string>();
  for (const [area, cat] of await areas("it")) {
    for (const key of Object.keys(cat)) {
      // Un'area può stare in più file: errors-finance.ts appartiene all'area «errors».
      const prefix = area.split("-")[0];
      assert.ok(key.startsWith(`${prefix}.`), `${area}.ts: la chiave «${key}» deve iniziare con «${prefix}.»`);
      assert.ok(!seen.has(key), `chiave «${key}» in ${area}.ts e in ${seen.get(key)}.ts`);
      seen.set(key, area);
    }
  }
});

test("l'inglese ha le stesse chiavi, gli stessi segnaposto e le stesse forme dell'italiano", async () => {
  const it = new Map(await areas("it"));
  for (const [area, en] of await areas("en")) {
    const src = it.get(area)!;
    assert.deepEqual(Object.keys(en).sort(), Object.keys(src).sort(), `en/${area}.ts: chiavi diverse da it/${area}.ts`);
    for (const key of Object.keys(src)) {
      assert.equal(shape(en[key]), shape(src[key]), `${key}: forma diversa (testo o plurale)`);
      assert.deepEqual(placeholders(en[key]), placeholders(src[key]), `${key}: segnaposto diversi tra italiano e inglese`);
      const parts = typeof en[key] === "string" ? [en[key] as string] : Object.values(en[key]);
      for (const p of parts) assert.ok(p.trim() !== "", `${key}: traduzione vuota`);
    }
  }
});

test("ogni code degli errori scritti nel backend ha la sua voce in errors", () => {
  const codes = new Set<string>();
  const walk = (dir: string) => {
    for (const f of readdirSync(dir)) {
      const p = join(dir, f);
      if (statSync(p).isDirectory()) walk(p);
      else if (f.endsWith(".go") && !f.endsWith("_test.go")) {
        const src = readFileSync(p, "utf8");
        // writeProblem(w, r, status, "code", …), problemField(…, "code", …), "code": "x", Code: "x"
        // e InvalidCode("validation.x", …) / InvalidParams("validation.x", …) del motore
        const re = /(?:InvalidCode|InvalidParams)\(\s*"([a-z_0-9.]+)"|(?:writeProblem(?:Params)?|problemField)\([^,]+,[^,]+,[^,]+,\s*"([a-z_0-9.]+)"|"code":\s*"([a-z_0-9]+\.[a-z_0-9.]+|validation)"|\bCode:\s*"([a-z_0-9]+\.[a-z_0-9.]+|validation)"/g;
        for (const m of src.matchAll(re)) codes.add(m[1] ?? m[2] ?? m[3] ?? m[4]);
      }
    }
  };
  walk(join(root, "internal"));
  assert.ok(codes.size > 50, `trovati solo ${codes.size} code: l'espressione non funziona più?`);
  const missing = [...codes].filter((c) => !(`errors.${c}` in itAll)).sort();
  assert.deepEqual(missing, [], `code senza voce in it/errors.ts e en/errors.ts: ${missing.join(", ")}`);
});

test("interpolazione e plurali", async () => {
  assert.equal(interpolate("Ciao {nome}, {n} nuovi", { nome: "Ada", n: 3 }), "Ciao Ada, 3 nuovi");
  assert.equal(interpolate("Manca {x}", {}), "Manca {x}");
  await setLang("it", false);
  assert.equal(tr("profile.tokens.days", { count: 1 }), "1 giorno");
  assert.equal(tr("profile.tokens.days", { count: 30 }), "30 giorni");
  await setLang("en", false);
  assert.equal(tr("profile.tokens.days", { count: 1 }), "1 day");
  assert.equal(tr("profile.tokens.days", { count: 0 }), "0 days");
  assert.equal(tr("auth.login.title"), "Sign in");
  assert.equal(lookup("errors.nonexistent"), undefined);
});

test("errori: in italiano il detail del server, in inglese la traduzione del code, con riserva", async () => {
  await setLang("it", false);
  assert.equal(errorText("auth.forbidden", "permesso mancante: x", { perm: "x" }), "permesso mancante: x");
  await setLang("en", false);
  assert.equal(errorText("auth.forbidden", "permesso mancante: tenant.admin", { perm: "tenant.admin" }), "Missing permission: tenant.admin.");
  // Senza i parametri il segnaposto resterebbe vuoto: si usa il detail.
  assert.equal(errorText("auth.forbidden", "permesso mancante: tenant.admin"), "permesso mancante: tenant.admin");
  // Un code senza traduzione: il detail italiano.
  assert.equal(errorText("codice.sconosciuto", "dettaglio"), "dettaglio");
  assert.equal(errorText("auth.invalid_credentials", "credenziali non valide"), "Incorrect email or password.");
});

test("formati nella lingua della persona", async () => {
  const d = "2026-11-25T12:00:00Z";
  await setLang("it", false);
  assert.equal(loc(), "it-IT");
  assert.equal(fmtDate(d, { timeZone: "UTC" }), "25/11/2026");
  assert.equal(fmtDate(null), "—");
  assert.match(fmtCurrency(12345.5, "EUR"), /12\.345,50/);
  assert.equal(fmtDuration(450), "7h 30m");
  assert.equal(fmtDuration(0), "0m");
  assert.equal(fmtDuration(600, { days: true }), "1g 2h");
  await setLang("en", false);
  assert.equal(loc(), "en-US");
  assert.equal(fmtDate(d, { timeZone: "UTC" }), "11/25/2026");
  assert.match(fmtCurrency(12345.5, "EUR"), /€12,345\.50/);
  assert.equal(fmtDuration(600, { days: true }), "1d 2h");
});

test("lingua del browser: la prima supportata, altrimenti l'italiano", () => {
  assert.equal(browserLang(["en-GB", "it"]), "en");
  assert.equal(browserLang(["de-DE", "it-IT"]), "it");
  assert.equal(browserLang(["fr", "de"]), "it");
  assert.equal(browserLang([]), "it");
  assert.equal(enAll["auth.login.title"], "Sign in");
});
