// Commit → elemento: un Gitea finto (server Node nel test) con un commit che cita la chiave di un
// incidente; il connettore lo legge e la scheda dell'elemento mostra il blocco «Codice».
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

test("la scheda mostra i commit che citano la chiave", async ({ page }) => {
  const rnd = Math.random().toString(36).replace(/[^a-z]/g, "").slice(0, 5).padEnd(5, "x");
  const code = `CM${rnd}`.toUpperCase();
  const repo = code.toLowerCase();
  const owner = `acme${rnd}`;
  await login(page, "admin");
  const app = await call(page.request, "POST", "/api/v1/architecture/applications", { name: code, code });
  const inc = await call(page.request, "POST", "/api/v1/incidents", {
    title: uniq("Totale errato"), detected_at: new Date(Date.now() - 36 * 3600e3).toISOString(), impacted: [app.key],
  });
  const key: string = inc.key;
  const server = await fakeGitea(owner, repo, `${key} corregge il totale dell'ordine\n\ncorpo`, new Date(Date.now() - 6 * 3600e3));
  try {
    const base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`;
    const g = "/api/v1/devsecops/gitea";
    const it = await call(page.request, "POST", `${g}/integrations`, {
      name: uniq("Gitea commit"), base_url: base, owners: [owner], token: TOKEN, webhook_secret: "segreto-del-webhook-e2e-01", private_network: true,
    });
    await call(page.request, "POST", `${g}/integrations/${it.id}/discover`);
    const repos = await call<{ items: { id: string; full_name: string; status: string }[] }>(page.request, "GET", `${g}/integrations/${it.id}/repos`);
    expect(repos.items.map((r) => r.status)).toEqual(["suggested"]);
    await call(page.request, "POST", `${g}/integrations/${it.id}/repos/accept`, { ids: [repos.items[0].id] });
    await call(page.request, "POST", `${g}/integrations/${it.id}/sync`);
    await expect
      .poll(async () => {
        const runs = await call<{ items: { status: string; stats: { commits?: number } }[] }>(page.request, "GET", `${g}/integrations/${it.id}/runs`);
        return runs.items[0] ? `${runs.items[0].status}:${runs.items[0].stats.commits ?? 0}` : "nessuna";
      }, { timeout: 30_000 })
      .toBe("ok:1");

    // L'endpoint e il blocco «Codice» nella barra laterale della scheda.
    const c = await call<{ commits: { title: string; author: string }[] }>(page.request, "GET", `/api/v1/elements/${key}/commits`);
    expect(c.commits).toHaveLength(1);
    await page.goto(`/e/${key}`);
    const blocco = page.getByTestId("sezione-codice");
    await expect(blocco).toBeVisible();
    await expect(blocco.locator("summary")).toContainText("Codice");
    await expect(blocco.getByText(`${key} corregge il totale dell'ordine`)).toBeVisible();
    await expect(blocco.getByText("Ana Rossi", { exact: false })).toBeVisible();
    await expect(blocco.getByRole("link", { name: "Apri il commit abc1234" })).toBeVisible();
    await expect(blocco.getByText("Non ancora rilasciato in produzione.")).toBeVisible();

    // Un elemento senza commit non mostra il blocco.
    const altro = await call(page.request, "POST", "/api/v1/incidents", {
      title: uniq("Senza codice"), detected_at: new Date(Date.now() - 36 * 3600e3).toISOString(), impacted: [app.key],
    });
    await page.goto(`/e/${altro.key}`);
    await expect(page.getByRole("heading", { level: 1 }).first()).toBeVisible();
    await expect(page.getByTestId("sezione-codice")).toHaveCount(0);
  } finally {
    server.close();
  }
});
