// Timesheet nel motore delle approvazioni e nelle notifiche: la settimana inviata compare tra
// «Le mie approvazioni» di chi approva e nelle sue notifiche, si decide dalla pagina del
// timesheet, e la persona riceve l'esito. Le pagine si leggono a 375 px senza scorrere di lato.
import { test, expect } from "@playwright/test";
import { apiAs, call, expectNoHorizontalScroll, login, uniq } from "./fixtures";

/** Lunedì di una settimana passata scelta a caso (i test girano in parallelo sullo stesso server). */
function pastWeek() {
  const d = new Date();
  d.setDate(d.getDate() - ((d.getDay() + 6) % 7) - 7 * (30 + Math.floor(Math.random() * 300)));
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

async function prepara(playwright: Parameters<typeof apiAs>[0], baseURL: string) {
  const admin = await apiAs(playwright, baseURL, "admin");
  await call(admin.req, "PUT", "/api/v1/work/time/settings", { require_approval: true, approver_mode: "admin" });
  const team = uniq("Team timesheet");
  await call(admin.req, "POST", "/api/v1/teams", { name: team });
  const spaces = await call(admin.req, "GET", "/api/v1/work/spaces");
  const space = spaces.items.find((s: { team: string }) => s.team === team).key as string;
  const item = await call(admin.req, "POST", `/api/v1/work/spaces/${space}/items`, { type: "story", title: "Lavoro del timesheet" });
  const dev = await apiAs(playwright, baseURL, "dev");
  const week = pastWeek();
  await call(dev.req, "POST", `/api/v1/work/elements/${item.key}/time`, { duration: "2h", date: week });
  await call(dev.req, "POST", "/api/v1/work/timesheet/submit", { week });
  return { admin, dev, week };
}

test("la settimana inviata è tra le approvazioni e le notifiche di chi approva, e si approva dal timesheet", async ({ page, playwright, baseURL }) => {
  const { admin, dev, week } = await prepara(playwright, baseURL!);

  await login(page, "admin");
  // Notifiche: l'invio compare, senza un elemento a cui legarsi.
  await page.goto("/notifiche");
  const avviso = page.getByRole("link").filter({ hasText: /Anna Moretti ha inviato/ }).first();
  await expect(avviso).toBeVisible();
  await expect(avviso).toContainText(/timesheet/i);

  // Le mie approvazioni: la riga porta al timesheet della persona.
  await page.goto("/approvazioni");
  const riga = page.locator(`a[href*="settimana=${week}"]`).filter({ hasText: /Timesheet di Anna Moretti/ });
  await expect(riga).toBeVisible();
  await riga.click();
  await expect(page).toHaveURL(/\/tempo\?/);
  await expect(page.getByRole("button", { name: "Approva" })).toBeVisible();

  // Chi ha inviato aspetta: la settimana è bloccata finché non si decide.
  const prima = await call(dev.req, "GET", `/api/v1/work/timesheet?week=${week}`);
  expect(prima.status).toBe("submitted");

  await page.getByRole("button", { name: "Approva" }).click();
  await expect(page.getByRole("button", { name: "Approva" })).toHaveCount(0);
  const dopo = await call(dev.req, "GET", `/api/v1/work/timesheet?week=${week}`);
  expect(dopo.status).toBe("approved");

  // La richiesta è uscita dall'inbox e la persona ha l'avviso dell'esito.
  const inbox = await call(admin.req, "GET", "/api/v1/approvals/inbox");
  expect(inbox.items.some((i: { timesheet_week: string | null; title: string }) => i.timesheet_week === week && i.title === "Anna Moretti")).toBe(false);
  const avvisi = await call(dev.req, "GET", "/api/v1/notifications");
  expect(avvisi.items.some((n: { kind: string; payload: { event?: string; week?: string } }) => n.kind === "timesheet" && n.payload.event === "approved" && n.payload.week === week)).toBe(true);
  await admin.req.dispose();
  await dev.req.dispose();
});

test("respingere chiede il motivo e la persona lo legge", async ({ playwright, baseURL }) => {
  const { admin, dev, week } = await prepara(playwright, baseURL!);
  const member = dev.me.active_tenant!.member_id;
  const senzaMotivo = await admin.req.fetch("/api/v1/work/timesheet/decide", { method: "POST", data: { member, week, approve: false } });
  expect(senzaMotivo.status()).toBe(422);
  const risposta = await call(admin.req, "POST", "/api/v1/work/timesheet/decide", { member, week, approve: false, comment: "Manca il giovedì" });
  expect(risposta.status).toBe("rejected");
  const avvisi = await call(dev.req, "GET", "/api/v1/notifications");
  expect(avvisi.items.some((n: { kind: string; payload: { event?: string; comment?: string } }) => n.kind === "timesheet" && n.payload.event === "rejected" && n.payload.comment === "Manca il giovedì")).toBe(true);
  // Chi ha inviato non decide la propria settimana, nemmeno dal passo del motore.
  const propria = await dev.req.fetch("/api/v1/work/timesheet/decide", { method: "POST", data: { member, week, approve: true } });
  expect(propria.status()).toBe(422);
  await admin.req.dispose();
  await dev.req.dispose();
});

test("a 375 px approvazioni e notifiche non scorrono in orizzontale", async ({ page, playwright, baseURL }) => {
  const { admin, dev } = await prepara(playwright, baseURL!);
  await admin.req.dispose();
  await dev.req.dispose();
  await page.setViewportSize({ width: 375, height: 812 });
  await login(page, "admin");
  await page.goto("/approvazioni");
  await expect(page.getByRole("link", { name: /Timesheet di Anna Moretti/ }).first()).toBeVisible();
  await expectNoHorizontalScroll(page);
  await page.goto("/notifiche");
  await expect(page.getByRole("link").filter({ hasText: /Anna Moretti ha inviato/ }).first()).toBeVisible();
  await expectNoHorizontalScroll(page);
});
