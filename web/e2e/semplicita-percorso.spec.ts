// Ondata «semplicità»: il percorso visibile sulla richiesta e «Cosa faccio adesso», con la spiegazione dei
// termini. Numeri uguali alle schede che li mostrano per esteso, impatto, telefono a 375 px, inglese.
import { test, expect } from "@playwright/test";
import { advance, apiAs, call, expectNoHorizontalScroll, login, newRequest, uniq } from "./fixtures";

const MOBILE = { width: 375, height: 812 };

type Pc = {
  type: string;
  current: string;
  closed: boolean;
  phases: { name: string; status: string; off_path?: boolean }[];
  reviews: { key: string; open: boolean }[];
  impacts: { key: string }[];
  next: { to: string; forward: boolean }[];
  todo: { kind: string; mine: boolean }[];
};

test("richiesta: dati del percorso coerenti con valutazioni, impatti e fasi", async ({ playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const pc = await call<Pc>(req, "GET", "/api/v1/elements/DEM-5/percorso");
  const det = await call<{ element: { state: string }; phases: { name: string }[] }>(req, "GET", "/api/v1/elements/DEM-5");
  const rv = await call<{ items: { key: string; superseded: boolean }[] }>(req, "GET", "/api/v1/elements/DEM-5/reviews");
  expect(pc.type).toBe("business_request");
  expect(pc.current).toBe(det.element.state);
  expect(pc.phases.filter((p) => p.status === "current")).toHaveLength(1);
  expect(pc.phases.map((p) => p.name)).toEqual(det.phases.map((p) => p.name));
  expect(pc.reviews.map((r) => r.key).sort()).toEqual(rv.items.filter((r) => !r.superseded).map((r) => r.key).sort());
  expect(pc.todo.length).toBeGreaterThan(0);
  // Un elemento che non è una richiesta né un impatto: errore di validazione, non un 500.
  const bad = await req.get("/api/v1/elements/ARC-6/percorso");
  expect(bad.status()).toBe(422);
});

test("pannello Percorso: fasi, cosa faccio adesso, chi è coinvolto e cosa succede dopo", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const pc = await call<Pc>(req, "GET", "/api/v1/elements/DEM-5/percorso");
  await login(page, "admin");
  await page.goto("/e/DEM-5?scheda=attivita");
  const panel = page.getByRole("region", { name: "Percorso", exact: true });
  await expect(panel).toBeVisible();
  await expect(panel.getByRole("list", { name: "Fasi del processo" }).locator("li[aria-current=step]")).toHaveCount(1);
  await expect(panel.getByRole("region", { name: "Cosa faccio adesso" })).toBeVisible();
  await expect(panel.getByRole("heading", { name: "Chi è coinvolto" })).toBeVisible();
  await expect(panel.getByRole("heading", { name: "Il lavoro che ne nasce" })).toBeVisible();
  await expect(panel.getByRole("heading", { name: "Cosa succede dopo" })).toBeVisible();
  for (const r of pc.reviews) await expect(panel.getByRole("link", { name: r.key, exact: true })).toBeVisible();
  if (pc.next.some((n) => n.forward)) await expect(panel.getByText(/^Prossima fase: /).first()).toBeVisible();
});

test("spiegazione dei termini: mouse, tastiera e Esc, con aria-describedby", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/e/DEM-5?scheda=attivita");
  const panel = page.getByRole("region", { name: "Percorso", exact: true });
  const term = panel.locator("button[data-term=valutazione]").first();
  await term.hover();
  const tip = page.getByRole("tooltip");
  await expect(tip).toContainText("questionario");
  await expect(term).toHaveAttribute("aria-describedby", (await tip.getAttribute("id"))!);
  await page.mouse.move(0, 0);
  await expect(tip).toHaveCount(0);
  // Da tastiera.
  await term.focus();
  await expect(page.getByRole("tooltip")).toContainText("questionario");
  await page.keyboard.press("Escape");
  await expect(page.getByRole("tooltip")).toHaveCount(0);
  // Al clic resta aperto, e un secondo clic lo chiude.
  await term.click();
  await expect(page.getByRole("tooltip")).toBeVisible();
  await page.mouse.move(0, 0);
  await expect(page.getByRole("tooltip")).toBeVisible();
  await term.click();
  await expect(page.getByRole("tooltip")).toHaveCount(0);
});

test("impatto: il percorso funziona anche sull'impatto e la richiesta elenca l'impatto per team", async ({ page, playwright, baseURL }) => {
  const { req: rq } = await apiAs(playwright, baseURL!, "richiedente");
  const r = await newRequest(rq, uniq("Percorso impatto"));
  const { req: dm } = await apiAs(playwright, baseURL!, "dm");
  const apps = await call(dm, "GET", "/api/v1/architecture/applications?q=");
  const app = apps.items.find((a: { team: string | null }) => a.team === "Web");
  expect(app, "applicazione del team Web").toBeTruthy();
  await advance(dm, r.id, "Triage");
  const impact = await call<{ id: string; key: string }>(dm, "POST", `/api/v1/demand/requests/${r.key}/impacts`, { application_id: app.id, change_kind: "change" });
  const onReq = await call<Pc>(dm, "GET", `/api/v1/elements/${r.key}/percorso`);
  expect(onReq.impacts).toHaveLength(1);
  const onImp = await call<Pc & { request_key: string }>(dm, "GET", `/api/v1/elements/${impact.id}/percorso`);
  expect(onImp.type).toBe("impact");
  expect(onImp.request_key).toBe(r.key);
  await login(page, "dm");
  await page.goto(`/e/${onReq.impacts[0].key}?scheda=attivita`);
  const panel = page.getByRole("region", { name: "Percorso dell'impatto" });
  await expect(panel).toBeVisible();
  await expect(panel.getByRole("link", { name: `Fa parte della richiesta ${r.key}` })).toBeVisible();
  await expect(panel.getByRole("region", { name: "Cosa faccio adesso" })).toBeVisible();
  // Sulla richiesta l'impatto compare con il suo team.
  await page.goto(`/e/${r.key}?scheda=attivita`);
  await expect(page.getByRole("region", { name: "Percorso", exact: true }).getByRole("link", { name: onReq.impacts[0].key, exact: true })).toBeVisible();
  await rq.dispose();
  await dm.dispose();
});

test("telefono a 375 px: il percorso si legge senza scorrere di lato", async ({ page }) => {
  await page.setViewportSize(MOBILE);
  await login(page, "admin");
  await page.goto("/e/DEM-5?scheda=attivita");
  const panel = page.getByRole("region", { name: "Percorso", exact: true });
  await expect(panel).toBeVisible();
  // Chiuso: una riga con la fase e cosa tocca a te; la scheda sotto resta in vista.
  await expect(panel.locator("summary")).toContainText(/Percorso/);
  await expectNoHorizontalScroll(page);
  await panel.locator("summary").click();
  await expect(panel.getByRole("region", { name: "Cosa faccio adesso" })).toBeVisible();
  await expectNoHorizontalScroll(page);
  await panel.locator("button[data-term=fase]").first().click();
  await expect(page.getByRole("tooltip")).toBeVisible();
  const box = (await page.getByRole("tooltip").boundingBox())!;
  expect(box.x).toBeGreaterThanOrEqual(0);
  expect(box.x + box.width).toBeLessThanOrEqual(MOBILE.width);
  await expectNoHorizontalScroll(page);
});

test.describe("in inglese", () => {
  test.use({ locale: "en-US" });
  test("pannello e glossario", async ({ page }) => {
    await login(page, "admin");
    await page.goto("/e/DEM-5?scheda=attivita");
    const panel = page.getByRole("region", { name: "Path", exact: true });
    await expect(panel).toBeVisible();
    await expect(panel.getByRole("region", { name: "What do I do now" })).toBeVisible();
    await expect(panel.getByRole("heading", { name: "What happens next" })).toBeVisible();
    await panel.locator("button[data-term=fase]").first().hover();
    await expect(page.getByRole("tooltip")).toContainText("A stage is one stretch of the process");
    const text = await panel.innerText();
    expect(text).not.toMatch(/\b(Cosa|Chi è|Fasi|Tocca|Nessun[aoi]?)\b/);
  });
});
