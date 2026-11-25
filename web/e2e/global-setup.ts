// Avvia un server Regia per gli e2e su una cartella dati temporanea:
// seed (scripts/dev-seed.sh), server su e2e.localhost:<E2E_PORT>, dati demo
// (scripts/dev-demo-data.sh). Alla fine ferma il server (e il suo Postgres) e cancella
// la cartella. Con E2E_URL usa un server già pronto e non avvia niente.
import { spawn, execFileSync, type ChildProcess } from "node:child_process";
import { existsSync, mkdtempSync, openSync, rmSync } from "node:fs";
import { createConnection } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const root = resolve(import.meta.dirname, "..", "..");

function portBusy(port: number): Promise<boolean> {
  return new Promise((ok) => {
    const s = createConnection({ host: "127.0.0.1", port }, () => {
      s.destroy();
      ok(true);
    });
    s.on("error", () => ok(false));
  });
}

async function waitHealthy(url: string, server: ChildProcess, log: string) {
  const until = Date.now() + 60_000;
  while (Date.now() < until) {
    if (server.exitCode !== null) throw new Error(`il server Regia è uscito (codice ${server.exitCode}): vedi ${log}`);
    try {
      const r = await fetch(url + "/readyz");
      if (r.ok) return;
    } catch {
      // non ancora in ascolto
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error(`il server Regia non risponde su ${url}: vedi ${log}`);
}

export default async function globalSetup() {
  if (process.env.E2E_URL) return;
  const port = Number(process.env.E2E_PORT || 8140);
  const url = `http://e2e.localhost:${port}`;
  const bin = resolve(root, process.env.E2E_BIN || "bin/regia");
  if (!existsSync(bin)) throw new Error(`manca ${bin}: esegui prima "make web build" dalla radice del repository`);
  if (await portBusy(port)) throw new Error(`la porta ${port} è occupata: ferma l'altro server o usa E2E_PORT (8140-8149)`);

  // Percorso corto: il socket di Postgres (<dati>/run/.s.PGSQL.5432) ha un limite di ~100 caratteri.
  const dir = process.env.E2E_DATA ? resolve(process.env.E2E_DATA) : mkdtempSync(join(tmpdir(), "regia-e2e-"));
  const data = join(dir, "data");
  const log = join(dir, "server.log");
  const env = { ...process.env, DATA: data, M: bin, REGIA_LOG_FORMAT: "text" };
  const t0 = Date.now();
  const seedLog = openSync(join(dir, "seed.log"), "a");
  try {
    execFileSync("bash", [join(root, "scripts/dev-seed.sh")], { cwd: root, env, stdio: ["ignore", seedLog, seedLog] });
  } catch (e) {
    throw new Error(`seed fallito: vedi ${join(dir, "seed.log")} (${(e as Error).message})`);
  }

  const out = openSync(log, "a");
  const server = spawn(bin, ["serve", "--data", data, "--dev", "--http", `127.0.0.1:${port}`], { cwd: root, env, stdio: ["ignore", out, out] });
  const stop = async () => {
    if (server.exitCode === null) {
      server.kill("SIGTERM");
      await new Promise<void>((ok) => {
        const t = setTimeout(() => {
          server.kill("SIGKILL");
          ok();
        }, 15_000);
        server.once("exit", () => {
          clearTimeout(t);
          ok();
        });
      });
    }
    if (!process.env.E2E_KEEP && !process.env.E2E_DATA) rmSync(dir, { recursive: true, force: true });
  };
  try {
    await waitHealthy(url, server, log);
    const demoLog = openSync(join(dir, "demo.log"), "a");
    execFileSync("bash", [join(root, "scripts/dev-demo-data.sh"), url], { cwd: root, env, stdio: ["ignore", demoLog, demoLog] });
  } catch (e) {
    process.env.E2E_KEEP = "1"; // si tengono i log per capire cosa è andato storto
    await stop();
    throw new Error(`avvio del server o dati demo falliti: vedi i log in ${dir} (${(e as Error).message})`);
  }
  console.log(`[e2e] server pronto su ${url} in ${Math.round((Date.now() - t0) / 1000)} s (dati in ${dir})`);
  return stop;
}
