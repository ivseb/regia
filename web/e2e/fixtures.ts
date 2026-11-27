// Aiuti comuni agli e2e: password di sviluppo (letta da scripts/dev-seed.sh, mai ripetuta
// qui), accesso via API per i test che non provano il login, chiamate API per preparare
// i dati di ogni test, controllo dello scorrimento orizzontale.
import { expect, type APIRequestContext, type Locator, type Page } from "@playwright/test";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const seed = readFileSync(resolve(import.meta.dirname, "..", "..", "scripts", "dev-seed.sh"), "utf8");
export const PASSWORD = process.env.REGIA_DEV_PASSWORD || /REGIA_DEV_PASSWORD:-'([^']+)'/.exec(seed)![1];

export type Me = {
  account_id: string;
  tenants: { tenant_id: string; slug: string; name: string }[];
  active_tenant: { tenant_id: string; member_id: string } | null;
};

/** Accede come <who>@demo.test (via API, cookie nel contesto della pagina) nel tenant indicato. */
export async function login(page: Page, who: string, tenant = "demo"): Promise<Me> {
  const r = await page.request.post("/api/v1/auth/login", { data: { email: `${who}@demo.test`, password: PASSWORD } });
  expect(r.ok(), `accesso di ${who}: ${r.status()}`).toBeTruthy();
  let me = (await r.json()) as Me;
  const t = me.tenants.find((x) => x.slug === tenant);
  if (t && me.active_tenant?.tenant_id !== t.tenant_id) {
    const s = await page.request.post("/api/v1/me/tenant", { data: { tenant_id: t.tenant_id } });
    expect(s.ok()).toBeTruthy();
    me = (await s.json()) as Me;
  }
  return me;
}

/** Chiamata API con verifica dell'esito; restituisce il JSON (o null). */
export async function call<T = any>(req: APIRequestContext, method: string, path: string, data?: unknown): Promise<T> {
  const r = await req.fetch(path, { method, data: data === undefined ? undefined : data });
  const body = await r.text();
  expect(r.ok(), `${method} ${path}: ${r.status()} ${body.slice(0, 300)}`).toBeTruthy();
  return (body ? JSON.parse(body) : null) as T;
}

/**
 * Crea un'organizzazione (superamministratore). Due creazioni nello stesso istante possono
 * scontrarsi sulla chiave del registro di audit (at, seq) e dare 500: si riprova una volta.
 */
export async function createTenant(req: APIRequestContext, name: string, slug: string): Promise<void> {
  for (let i = 0; ; i++) {
    const r = await req.post("/api/v1/platform/tenants", { data: { name, slug } });
    if (r.ok()) return;
    if (r.status() < 500 || i >= 2) {
      expect(r.ok(), `POST /api/v1/platform/tenants: ${r.status()} ${(await r.text()).slice(0, 300)}`).toBeTruthy();
      return;
    }
    await new Promise((ok) => setTimeout(ok, 200 + Math.random() * 300));
  }
}

/** Contesto API separato, autenticato come <who> nel tenant demo (per preparare i dati). */
export async function apiAs(playwright: { request: { newContext: (o: object) => Promise<APIRequestContext> } }, baseURL: string, who: string, tenant = "demo") {
  const req = await playwright.request.newContext({ baseURL });
  const r = await req.post("/api/v1/auth/login", { data: { email: `${who}@demo.test`, password: PASSWORD } });
  expect(r.ok(), `accesso API di ${who}`).toBeTruthy();
  const me = (await r.json()) as Me;
  const t = me.tenants.find((x) => x.slug === tenant)!;
  const s = await req.post("/api/v1/me/tenant", { data: { tenant_id: t.tenant_id } });
  expect(s.ok()).toBeTruthy();
  return { req, me: (await s.json()) as Me };
}

/** Suffisso unico per i nomi creati dai test (i test girano in parallelo sullo stesso server). */
export function uniq(prefix: string) {
  return `${prefix} ${Date.now().toString(36)}${Math.floor(Math.random() * 1e4).toString(36)}`;
}

/** Id del membro del tenant con questo nome visualizzato. */
export async function memberId(req: APIRequestContext, name: string): Promise<string> {
  const { items } = await call<{ items: { id: string; display_name: string }[] }>(req, "GET", "/api/v1/members");
  const m = items.find((x) => x.display_name === name);
  expect(m, `membro ${name}`).toBeTruthy();
  return m!.id;
}

/** Nuova richiesta (come chi possiede req), inviata salvo send: false. */
export async function newRequest(req: APIRequestContext, title: string, opts: { send?: boolean; fields?: Record<string, unknown>; extra?: Record<string, unknown> } = {}) {
  const sponsor = await memberId(req, "Marta Ferri");
  const c = await call<{ id: string }>(req, "POST", "/api/v1/demand/requests", {
    title, description: "Richiesta creata dai test end-to-end.", sponsor_id: sponsor, fields: { urgenza: "Media", ...opts.fields }, ...opts.extra,
  });
  if (opts.send !== false) await call(req, "POST", `/api/v1/elements/${c.id}/transitions`, { to: "Inviata" });
  const d = await call<{ element: { key: string } }>(req, "GET", `/api/v1/elements/${c.id}`);
  return { id: c.id, key: d.element.key };
}

/** Passaggi di fase in sequenza (via API). */
export async function advance(req: APIRequestContext, id: string, ...states: string[]) {
  for (const to of states) await call(req, "POST", `/api/v1/elements/${id}/transitions`, { to, comment: "Passaggio dei test end-to-end." });
}

/** Nessuno scorrimento orizzontale della pagina. */
export async function expectNoHorizontalScroll(page: Page) {
  const { sw, cw } = await page.evaluate(() => ({ sw: document.documentElement.scrollWidth, cw: document.documentElement.clientWidth }));
  expect(sw, `larghezza del contenuto ${sw}px su ${cw}px`).toBeLessThanOrEqual(cw);
}

/**
 * Sceglie una voce dal selettore comune (web/src/select.tsx) al posto di `selectOption`.
 * `voce`: testo esatto (o espressione) della voce, `{ value }` per il valore, `{ index }` per la posizione.
 * `cerca`: testo da scrivere nel campo di ricerca prima di scegliere (liste lunghe e liste del server).
 */
export async function scegli(trigger: Locator, voce: string | RegExp | { value: string } | { index: number }, opts: { cerca?: string } = {}) {
  const page = trigger.page();
  await trigger.click();
  if (opts.cerca !== undefined) await page.getByRole("combobox", { name: /^Cerca/ }).fill(opts.cerca);
  const options = page.getByRole("option");
  let o: Locator;
  if (typeof voce === "string" || voce instanceof RegExp) o = options.filter({ hasText: voce });
  else if ("value" in voce) o = page.locator(`[role=option][data-value="${voce.value}"]`);
  else o = options.nth(voce.index);
  // Una voce con testo uguale a un'altra più lunga: preferisce quella esatta.
  if (typeof voce === "string") {
    const exact = page.getByRole("option", { name: voce, exact: true });
    if ((await exact.count()) > 0) o = exact;
  }
  await o.first().click();
  await expect(page.getByRole("listbox")).toHaveCount(0);
}

/** Valore scelto di un selettore comune (al posto di `toHaveValue`/`inputValue`). */
export async function valoreScelto(trigger: Locator): Promise<string> {
  return (await trigger.getAttribute("data-value")) ?? "";
}
