// Blocca le dipendenze npm con licenze copyleft o sconosciute (vedi architecture.md §1).
import { execSync } from "node:child_process";

const allowed = /^(MIT|ISC|BSD-2-Clause|BSD-3-Clause|Apache-2\.0|0BSD|BlueOak-1\.0\.0|CC0-1\.0|Unlicense|MPL-2\.0|Python-2\.0|CC-BY-4\.0|OFL-1\.1)$/;
const out = JSON.parse(execSync("pnpm licenses list --json --prod", { encoding: "utf8" }) || "{}");
const bad = [];
for (const [license, pkgs] of Object.entries(out)) {
  const ok = license.split(/\s+(?:OR|AND)\s+|[()]/).filter(Boolean).some((l) => allowed.test(l));
  if (!ok) for (const p of pkgs) bad.push(`${p.name}@${p.versions?.join(",")}: ${license}`);
}
if (bad.length) {
  console.error("Licenze non ammesse:\n" + bad.join("\n"));
  process.exit(1);
}
console.log("Licenze npm (produzione): tutte ammesse.");
