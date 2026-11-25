// Genera web/src/i18n/{it,en}/areas.gen.ts e .../lavoro/areas.gen.ts: l'elenco dei file di area
// di ogni cartella, uniti in un solo oggetto. TypeScript non sa elencare i file di una cartella,
// quindi le chiavi tipizzate (t("chiave")) dipendono da questi file generati (in .gitignore).
// Si lancia da solo prima di typecheck, build, dev e test (web/package.json).
// Una nuova area: it/<area>.ts e en/<area>.ts, nient'altro.
import { readdirSync, readFileSync, writeFileSync, existsSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const i18n = resolve(dirname(fileURLToPath(import.meta.url)), "..", "web", "src", "i18n");
const GEN = "areas.gen.ts";

for (const dir of ["it", "en", "it/lavoro", "en/lavoro"]) {
  const abs = join(i18n, dir);
  if (!existsSync(abs)) continue;
  const names = readdirSync(abs)
    .filter((f) => f.endsWith(".ts") && f !== "index.ts" && f !== GEN)
    .map((f) => f.slice(0, -3))
    .sort();
  const body = [
    "// GENERATO da scripts/gen-i18n.mjs: non modificare, non è nel repository.",
    ...names.map((n, i) => `import a${i} from "./${n}.ts";`),
    "",
    `export default { ${names.map((_, i) => `...a${i}`).join(", ")} };`,
    "",
  ].join("\n");
  const file = join(abs, GEN);
  if (!existsSync(file) || readFileSync(file, "utf8") !== body) writeFileSync(file, body);
}
