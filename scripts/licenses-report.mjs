// Elenco delle dipendenze distribuite con Regia e delle loro licenze, in Markdown.
// Usa gli stessi strumenti del controllo licenze (go-licenses e pnpm licenses).
// Uso: make licenses-report  (scrive docs/licenze-dipendenze.md)
import { execSync } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const goLicenses = process.env.GO_LICENSES ?? "github.com/google/go-licenses/v2@v2.0.1";

const goCSV = execSync(`go run ${goLicenses} report ./... --ignore regia`, {
  cwd: root, encoding: "utf8", stdio: ["ignore", "pipe", "ignore"],
});
// Una riga per modulo: go-licenses elenca i pacchetti, qui bastano i moduli.
const goMods = new Map();
for (const line of goCSV.trim().split("\n")) {
  const [pkg, url, license] = line.split(",");
  const m = /github\.com\/[^/]+\/[^/]+\/blob\/([^/]+)\//.exec(url) ?? /\/\+\/([^:]+):/.exec(url);
  const mod = pkg.replace(/\/(json|semaphore|unix|oidc)$/, "");
  const key = `${mod} ${license}`;
  if (!goMods.has(key)) goMods.set(key, { mod, version: m?.[1] ?? "", license, url });
}

const npm = JSON.parse(execSync("pnpm licenses list --json --prod", { cwd: resolve(root, "web"), encoding: "utf8" }) || "{}");
const npmRows = [];
for (const [license, pkgs] of Object.entries(npm)) {
  for (const p of pkgs) npmRows.push({ name: p.name, version: (p.versions ?? []).join(", "), license, homepage: p.homepage ?? "" });
}
npmRows.sort((a, b) => a.name.localeCompare(b.name));

const count = (rows, key) => {
  const c = {};
  for (const r of rows) c[r[key]] = (c[r[key]] ?? 0) + 1;
  return Object.entries(c).sort((a, b) => b[1] - a[1]).map(([l, n]) => `${l} (${n})`).join(", ");
};

const out = [];
out.push("# Dipendenze e licenze");
out.push("");
out.push("Generato da `make licenses-report` (go-licenses e `pnpm licenses list --prod`): non modificare a mano.");
out.push("Il controllo `make licenses` blocca le licenze non permissive; vedi `docs/licenza.md`.");
out.push("");
out.push(`## Go (incluse nel binario): ${goMods.size} moduli`);
out.push("");
out.push(`Licenze: ${count([...goMods.values()], "license")}.`);
out.push("");
out.push("| modulo | versione | licenza |");
out.push("| --- | --- | --- |");
for (const r of [...goMods.values()].sort((a, b) => a.mod.localeCompare(b.mod))) {
  out.push(`| [${r.mod}](${r.url}) | ${r.version} | ${r.license} |`);
}
out.push("");
out.push(`## Frontend (incluso nel binario): ${npmRows.length} pacchetti npm di produzione`);
out.push("");
out.push(`Licenze: ${count(npmRows, "license")}.`);
out.push("");
out.push("| pacchetto | versione | licenza |");
out.push("| --- | --- | --- |");
for (const r of npmRows) out.push(`| ${r.name} | ${r.version} | ${r.license} |`);
out.push("");
process.stdout.write(out.join("\n"));
