// Rilascio → elementi: un Gitea finto con un commit che cita un incidente, poi un rilascio in
// produzione dalla pipeline. Il blocco «Codice» porta alla pagina del rilascio, che elenca
// l'elemento fra le correzioni con le note in Markdown e il link alla certificazione del change.
import { test, expect } from "@playwright/test";
import { createServer, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import { call, login, uniq } from "./fixtures";

const TOKEN = "gitea-token-e2e-0123456789";

function fakeGitea(owner: string, repo: string, message: string, when: Date): Promise<Server> {
  const json = (res: import("node:http").ServerResponse, body: unknown) => {
    res.setHeader("Content-Type", "application/json");
    res.end(JSON.stringify(body));
  };
  const server = createServer((req, res) => {
    if (req.headers.authorization !== `token ${TOKEN}`) {
      res.statusCode = 401;
      res.end("{}");
      return;
    }
    const url = new URL(req.url ?? "/", "http://x");
    const p = url.pathname.replace(/^\/api\/v1/, "");
    const page = url.searchParams.get("page") ?? "1";
    const d = when.toISOString();
    if (p === "/version") return json(res, { version: "1.22.3" });
    if (p === "/user") return json(res, { id: 1, login: "regia" });
    if (p === `/orgs/${owner}`) return json(res, { id: 1, username: owner });
    if (p === `/orgs/${owner}/repos`)
      return json(res, page === "1" ? [{ id: 4242, name: repo, full_name: `${owner}/${repo}`, default_branch: "main", html_url: `http://gitea/${owner}/${repo}` }] : []);
    if (p === `/repos/${owner}/${repo}/commits`)
      return json(res, page === "1" ? [{ sha: "abc1234".padEnd(40, "0"), html_url: `http://gitea/${owner}/${repo}/commit/abc1234`,
        commit: { message, author: { name: "Ana Rossi", date: d }, committer: { date: d } } }] : []);
    if (p.startsWith(`/repos/${owner}/${repo}/`) && (p.endsWith("/releases") || p.endsWith("/pulls"))) return json(res, []);
    if (p.endsWith("/status")) return json(res, { state: "pending", sha: "e".repeat(40), statuses: [] });
    res.statusCode = 404;
    res.end("{}");
  });
  return new Promise((ok) => server.listen(0, "127.0.0.1", () => ok(server)));
}

test("dal blocco Codice alla pagina del rilascio, con le note e la certificazione", async ({ page }) => {
  const rnd = Math.random().toString(36).replace(/[^a-z]/g, "").slice(0, 5).padEnd(5, "x");
  const code = `RL${rnd}`.toUpperCase();
  const repo = code.toLowerCase();
  const owner = `acme${rnd}`;
  const change = `CHG${Date.now().toString().slice(-7)}`;
  await login(page, "admin");
  const app = await call(page.request, "POST", "/api/v1/architecture/applications", { name: code, code });
  const inc = await call(page.request, "POST", "/api/v1/incidents", {
    title: uniq("Totale errato"), detected_at: new Date(Date.now() - 36 * 3600e3).toISOString(), impacted: [app.key],
  });
  const key: string = inc.key;
  const server = await fakeGitea(owner, repo, `${key} corregge il totale dell'ordine`, new Date(Date.now() - 6 * 3600e3));
  try {
    const base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
    const g = "/api/v1/devsecops/gitea";
    const it = await call(page.request, "POST", `${g}/integrations`, {
      name: uniq("Gitea rilascio"), base_url: base, owners: [owner], token: TOKEN, webhook_secret: "segreto-del-webhook-e2e-02", private_network: true,
    });
    await call(page.request, "POST", `${g}/integrations/${it.id}/discover`);
    const repos = await call<{ items: { id: string }[] }>(page.request, "GET", `${g}/integrations/${it.id}/repos`);
    await call(page.request, "POST", `${g}/integrations/${it.id}/repos/accept`, { ids: [repos.items[0].id] });
    await call(page.request, "POST", `${g}/integrations/${it.id}/sync`);
    await expect
      .poll(async () => (await call<{ commits: unknown[] }>(page.request, "GET", `/api/v1/elements/${key}/commits`)).commits.length, { timeout: 30_000 })
      .toBe(1);
    const c = await call<{ commits: { service: string }[] }>(page.request, "GET", `/api/v1/elements/${key}/commits`);

    // Il rilascio in produzione, dopo il commit, dalla pipeline (token con devsecops.write).
    const tok = await call(page.request, "POST", "/api/v1/me/tokens", { name: uniq("CI"), expires_in_days: 1, scopes: ["devsecops.write"] });
    const r = await page.request.fetch("/api/v1/devsecops/releases", {
      method: "POST", headers: { Authorization: `Bearer ${tok.token}` },
      data: { format: "regia.devsecops/v1", service: { name: c.commits[0].service }, environment: "prod", version: "2.4.0", change_number: change,
        at: new Date(Date.now() - 3600e3).toISOString() },
    });
    expect(r.ok(), await r.text()).toBeTruthy();

    // Dalla scheda alla pagina del rilascio.
    await page.goto(`/e/${key}`);
    const blocco = page.getByTestId("sezione-codice");
    await expect(blocco.getByText(/Rilasciato in 2\.4\.0/)).toBeVisible();
    await blocco.getByTestId("link-rilascio").click();
    await expect(page).toHaveURL(/\/rilasci\//);
    await expect(page.getByRole("heading", { level: 1 })).toHaveText(`${c.commits[0].service} 2.4.0`);
    const elementi = page.getByTestId("elementi-rilascio");
    await expect(elementi.getByRole("heading", { name: "Correzioni" })).toBeVisible();
    await expect(elementi.getByRole("link", { name: key })).toBeVisible();
    await expect(page.getByTestId("finestra-rilascio")).toContainText("Primo rilascio del servizio");
    const md = page.getByTestId("note-markdown");
    await expect(md).toContainText("## Correzioni");
    await expect(md).toContainText(`(${key})`);
    await expect(md).toContainText(`Change ${change}.`);

    // La certificazione del change, dal link della pagina.
    await page.getByTestId("link-certificazione").click();
    await expect(page).toHaveURL(new RegExp(`/change/${change}/certificazione`));
    await expect(page.getByRole("heading", { level: 1 })).toContainText(change);
    await expect(page.getByRole("link", { name: key }).first()).toBeVisible();
  } finally {
    server.close();
  }
});
