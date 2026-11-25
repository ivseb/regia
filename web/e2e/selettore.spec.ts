// Selettore comune (web/src/select.tsx): ricerca sul server tra molte persone, tastiera, scelta
// multipla a chip, voce «io» e recenti, liste statiche con e senza ricerca, pannello a tutto schermo a 375 px.
import { test, expect, type Page } from "@playwright/test";
import { apiAs, call, expectNoHorizontalScroll, login, newRequest, scegli, uniq } from "./fixtures";

// Nome fisso: le persone si creano una volta sola anche se più processi eseguono il file (409 = già create).
const TAG = "Selettore-prova";
const N = 110;
const nome = (i: number) => `Zeta ${TAG} ${String(i).padStart(3, "0")}`;

test.beforeAll(async ({ playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const gia = await call<{ items: unknown[] }>(req, "GET", `/api/v1/members?q=${encodeURIComponent(TAG)}&limit=200`);
  // 110 persone, dieci richieste per volta.
  for (let a = 1; a <= N && gia.items.length < N; a += 10) {
    await Promise.all(
      Array.from({ length: 10 }, (_, k) => a + k)
        .filter((i) => i <= N)
        .map(async (i) => {
          const r = await req.post("/api/v1/admin/members", {
            data: { email: `${nome(i).toLowerCase().replace(/\s+/g, ".")}@selettore.test`, display_name: nome(i), member_type: "standard", mode: "create", role_ids: [], group_ids: [] },
          });
          expect([201, 409], `creazione di ${nome(i)}: ${r.status()}`).toContain(r.status());
        }),
    );
  }
  await req.dispose();
});

/** Richieste al server per l'elenco delle persone, da quando si chiama. */
function spiaMembri(page: Page) {
  const urls: string[] = [];
  page.on("request", (r) => {
    const u = new URL(r.url());
    if (u.pathname === "/api/v1/members") urls.push(u.search);
  });
  return urls;
}

test("persona tra molte: niente richieste finché non si apre, ricerca sul server con limite, tastiera e recenti", async ({ page }) => {
  await login(page, "dm");
  const urls = spiaMembri(page);
  await page.goto("/richieste/nuova");
  const sponsor = page.getByLabel("Sponsor");
  await expect(sponsor).toBeVisible();
  await page.waitForLoadState("networkidle");
  expect(urls.filter((u) => u.includes("limit=20"))).toEqual([]); // nessun elenco prima dell'apertura

  const risposta = page.waitForResponse((r) => r.url().includes("/api/v1/members?q=&limit=20"));
  await sponsor.click();
  const lista = await (await risposta).json();
  expect(lista.items.length).toBeLessThanOrEqual(20);
  const box = page.getByRole("listbox");
  await expect(box).toBeVisible();
  await expect(page.getByRole("option", { name: /\(io\)/ })).toBeVisible(); // «io» in cima
  const cerca = page.getByRole("combobox", { name: /^Cerca/ });
  await expect(cerca).toBeFocused();
  expect(await page.getByRole("option").count()).toBeLessThanOrEqual(23);

  // Una persona qualsiasi tra le 110 si trova scrivendo il nome.
  await cerca.fill(nome(87));
  await expect(page.getByRole("option")).toHaveCount(1);
  await expect(page.getByRole("option").first()).toContainText(nome(87));
  await page.keyboard.press("Enter");
  await expect(box).toHaveCount(0);
  await expect(sponsor).toBeFocused();
  await expect(sponsor).toContainText(nome(87));

  // Riaperto: tra i recenti c'è la persona appena scelta; frecce, Fine e Inizio muovono la voce attiva; Esc chiude e rida il focus.
  await sponsor.press("ArrowDown");
  await expect(page.getByText("Recenti", { exact: true })).toBeVisible();
  await expect(page.getByRole("option", { name: nome(87) }).first()).toBeVisible();
  const attivo = () => cerca.getAttribute("aria-activedescendant");
  expect(await attivo()).toMatch(/-o\d+$/); // parte dalla voce attuale
  await page.keyboard.press("Home");
  const primo = await attivo();
  expect(primo).toMatch(/-o0$/);
  await page.keyboard.press("ArrowDown");
  const secondo = await attivo();
  expect(secondo).not.toBe(primo);
  await page.keyboard.press("End");
  const ultimo = await attivo();
  expect(ultimo).not.toBe(secondo);
  await page.keyboard.press("Home");
  expect(await attivo()).toBe(primo);
  await page.keyboard.press("Escape");
  await expect(box).toHaveCount(0);
  await expect(sponsor).toBeFocused();

  // Scrivere sul pulsante apre la ricerca già avviata; Tab chiude senza cambiare la scelta.
  await sponsor.press("z");
  await expect(cerca).toHaveValue("z");
  await page.keyboard.press("Tab");
  await expect(box).toHaveCount(0);
  await expect(sponsor).toContainText(nome(87));

  // «Nessuno» azzera la scelta.
  await scegli(sponsor, "— da definire —");
  await expect(sponsor).toContainText("— da definire —");

  // Nessun elenco completo: tutte le richieste di persone avevano un limite.
  expect(urls.length).toBeGreaterThan(0);
  expect(urls.every((u) => u.includes("limit=") || u.includes("ids="))).toBeTruthy();
});

test("persona tra molte: nessun risultato e ricerca per parte del nome", async ({ page }) => {
  await login(page, "dm");
  await page.goto("/richieste/nuova");
  await page.getByLabel("Sponsor").click();
  const cerca = page.getByRole("combobox", { name: /^Cerca/ });
  await cerca.fill("qqqqqq-inesistente");
  await expect(page.getByText("Nessun risultato").first()).toBeVisible();
  await cerca.fill(`${TAG} 05`);
  await expect(page.getByRole("option")).toHaveCount(10); // 050..059
});

test("scelta multipla a chip: aggiunge dalla ricerca, toglie con la ×, salva", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const fname = uniq("Revisori");
  const fkey = fname.toLowerCase().replace(/[^a-z0-9]+/g, "_");
  await call(req, "POST", "/api/v1/fields", { key: fkey, name: fname, data_type: "member", multi: true });
  await call(req, "PUT", `/api/v1/fields/${fkey}/types/business_request`, {});
  const { req: rich } = await apiAs(playwright, baseURL!, "richiedente");
  const { key, id } = await newRequest(rich, uniq("Richiesta con revisori"));
  await rich.dispose();

  await login(page, "admin");
  await page.goto(`/e/${key}`);
  await page.getByRole("button", { name: `Modifica ${fname}` }).click();
  const form = page.locator("form").filter({ has: page.getByRole("button", { name: "Salva", exact: true }) }).first();
  const scelta = form.getByRole("combobox");
  for (const i of [12, 34]) {
    await scelta.click();
    await page.getByRole("combobox", { name: /^Cerca/ }).fill(nome(i));
    await page.getByRole("option", { name: nome(i) }).click();
    await expect(page.getByRole("listbox")).toBeVisible(); // la scelta multipla resta aperta
    await page.keyboard.press("Escape");
    await expect(form.getByText(nome(i))).toBeVisible();
  }
  await form.getByRole("button", { name: `Togli ${nome(12)}` }).click();
  await expect(form.getByText(nome(12))).toHaveCount(0);
  await scelta.click();
  await page.getByRole("combobox", { name: /^Cerca/ }).fill(nome(99));
  await page.getByRole("option", { name: nome(99) }).click();
  await page.keyboard.press("Escape");
  await form.getByRole("button", { name: "Salva", exact: true }).click();
  await expect(page.getByRole("button", { name: `Modifica ${fname}` })).toContainText(nome(34));
  await expect(page.getByRole("button", { name: `Modifica ${fname}` })).toContainText(nome(99));

  const det = await call<{ element: { fields: Record<string, string[]> } }>(req, "GET", `/api/v1/elements/${id}`);
  expect(det.element.fields[fkey]).toHaveLength(2);
  await req.dispose();
});

test("lista statica: sotto le 8 voci niente ricerca, da 8 in su sì", async ({ page }) => {
  await login(page, "dm");
  await page.goto("/richieste/nuova");
  const driver = page.getByLabel("Driver");
  await driver.click();
  await expect(page.getByRole("listbox")).toBeVisible();
  await expect(page.getByRole("combobox", { name: /^Cerca/ })).toHaveCount(0);
  const n = await page.getByRole("option").count();
  expect(n).toBeLessThan(8);
  // Senza campo di ricerca si naviga con le frecce e con l'iniziale; Invio sceglie.
  await page.keyboard.press("ArrowDown");
  await page.keyboard.press("Enter");
  await expect(page.getByRole("listbox")).toHaveCount(0);
  await expect(driver).toBeFocused();

  await login(page, "admin");
  await page.goto("/configurazione/campi");
  await page.getByRole("button", { name: "Nuovo campo" }).click();
  const editor = page.getByRole("complementary", { name: "Modifica del campo" });
  const tipo = editor.getByLabel(/^Tipo di dato/);
  await tipo.click();
  const cerca = page.getByRole("combobox", { name: /^Cerca/ });
  await expect(cerca).toBeVisible();
  await cerca.fill("intero");
  await expect(page.getByRole("option")).toHaveCount(1);
  await page.keyboard.press("Enter");
  await expect(tipo).toContainText("Numero intero");
});

test.describe("telefono", () => {
  test.use({ viewport: { width: 375, height: 812 }, isMobile: true, hasTouch: true });

  test("375 px: pannello a tutto schermo con la ricerca in alto e bersagli da 44 px", async ({ page }) => {
    await login(page, "dm");
    await page.goto("/richieste/nuova");
    const sponsor = page.getByLabel("Sponsor");
    await sponsor.scrollIntoViewIfNeeded();
    await sponsor.tap();
    const pannello = page.getByRole("dialog");
    await expect(pannello).toBeVisible();
    const b = (await pannello.boundingBox())!;
    expect(b.x).toBe(0);
    expect(b.y).toBe(0);
    expect(b.width).toBe(375);
    expect(b.height).toBe(812);
    const cerca = page.getByRole("combobox", { name: /^Cerca/ });
    const c = (await cerca.boundingBox())!;
    expect(c.y).toBeLessThan(70);
    expect(c.height).toBeGreaterThanOrEqual(44);
    await expect(cerca).toBeFocused();
    const o = (await page.getByRole("option").first().boundingBox())!;
    expect(o.height).toBeGreaterThanOrEqual(44);
    await expectNoHorizontalScroll(page);
    await cerca.fill(nome(61));
    await page.getByRole("option", { name: nome(61) }).tap();
    await expect(pannello).toHaveCount(0);
    await expect(sponsor).toContainText(nome(61));
    await expectNoHorizontalScroll(page);
    // «Chiudi» esce senza scegliere.
    await sponsor.tap();
    await page.getByRole("button", { name: "Chiudi" }).tap();
    await expect(pannello).toHaveCount(0);
    await expect(sponsor).toContainText(nome(61));
  });
});
