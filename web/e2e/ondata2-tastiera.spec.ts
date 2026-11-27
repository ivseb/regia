// Ondata 2, pacchetto «tastiera» (replica/usabilita.md, proposta 10): scorciatoie con il pannello «?»,
// «+ Crea» globale, ⌘K che trova persone, unità, pagine della configurazione ed elementi con azioni
// dirette; filtro per tipo e colonne della lista richieste. Telefono a 375 px e una vista in inglese.
import { test, expect, type Page } from "@playwright/test";
import { apiAs, call, expectNoHorizontalScroll, login, newRequest, scegli, uniq } from "./fixtures";

const palette = (page: Page) => page.getByRole("dialog", { name: "Palette dei comandi" });

test("il pannello «?» elenca le scorciatoie e «g» + lettera porta alla pagina", async ({ page }) => {
  await login(page, "dm");
  await page.goto("/richieste");
  await expect(page.getByRole("heading", { name: "Richieste", level: 1 })).toBeVisible();
  await page.waitForLoadState("networkidle"); // le scorciatoie si registrano a pagina pronta

  await page.keyboard.press("?");
  const help = page.getByRole("dialog", { name: "Scorciatoie da tastiera" });
  await expect(help).toBeVisible();
  for (const gruppo of ["Generali", "Nelle liste", "Sull'elemento aperto o evidenziato", "Vai a"]) {
    await expect(help.getByRole("region", { name: gruppo })).toBeVisible();
  }
  await expect(help.getByText("Assegna a me")).toBeVisible();
  await expect(help.getByText("Cambia fase")).toBeVisible();
  await expect(help.getByText("Commenta")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(help).toHaveCount(0);

  // «Vai a»: due tasti.
  await page.keyboard.press("g");
  await page.keyboard.press("c");
  await expect(page).toHaveURL(/\/cruscotto/);
  await page.keyboard.press("g");
  await page.keyboard.press("b");
  await expect(page).toHaveURL(/\/delivery\/board/);

  // Mentre si scrive in un campo le lettere non sono scorciatoie.
  await page.goto("/richieste");
  await page.getByRole("button", { name: /Cerca o vai a/ }).click();
  await palette(page).getByRole("combobox").fill("gc?");
  await expect(page).toHaveURL(/\/richieste/);
  await expect(palette(page)).toBeVisible();
});

test("j e k scorrono le righe della lista e Invio apre l'elemento", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "richiedente");
  const { key } = await newRequest(req, uniq("Voce della lista da tastiera"));
  await req.dispose();
  await login(page, "dm");
  await page.goto(`/richieste?q=${encodeURIComponent(`chiave: ${key}`)}`);
  await expect(page.getByRole("row").filter({ hasText: key })).toBeVisible();
  await page.waitForLoadState("networkidle");
  await page.keyboard.press("j");
  await expect(page.locator("[data-riga-attiva]")).toHaveCount(1);
  await expect(page.locator("[data-riga-attiva]")).toContainText(key);
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(new RegExp(`/e/${key}$`));
});

test("«+ Crea» crea una richiesta e una story con un dialogo breve", async ({ page }) => {
  await login(page, "dm");
  await page.goto("/richieste");
  await expect(page.getByRole("heading", { name: "Richieste", level: 1 })).toBeVisible();

  // Dal pulsante nella barra laterale: una richiesta.
  const titolo = uniq("Richiesta da Crea");
  await page.getByRole("button", { name: /^\+ Crea/ }).first().click();
  const crea = page.getByRole("dialog", { name: "Crea" });
  await expect(crea).toBeVisible();
  await crea.getByLabel("Titolo").fill(titolo);
  await crea.getByLabel(/Descrizione/).fill("Creata con il dialogo breve.");
  await crea.getByRole("button", { name: "Crea", exact: true }).click();
  await expect(page).toHaveURL(/\/e\/DEM-\d+$/);
  await expect(page.getByRole("heading", { name: titolo, level: 1 })).toBeVisible();

  // Dalla scorciatoia «c»: una story in uno spazio di lavoro.
  await page.waitForLoadState("networkidle");
  await page.keyboard.press("c");
  await expect(crea).toBeVisible();
  await scegli(crea.getByRole("combobox", { name: "Che cosa" }), "Story");
  const storia = uniq("Story da Crea");
  await crea.getByLabel("Titolo").fill(storia);
  await expect(crea.getByRole("combobox", { name: "Spazio di lavoro" })).toBeVisible();
  await crea.getByRole("button", { name: "Crea", exact: true }).click();
  await expect(page).toHaveURL(/\/e\/[A-Z]+-\d+$/);
  await expect(page.getByRole("heading", { name: storia, level: 1 })).toBeVisible();
});

test("⌘K: persone, unità, pagine della configurazione ed elementi con «Assegna a me» e «Copia link»", async ({ page, playwright, baseURL }) => {
  const dev = await apiAs(playwright, baseURL!, "dev");
  const spaces = await call<{ items: { key: string }[] }>(dev.req, "GET", "/api/v1/work/spaces");
  expect(spaces.items.length, "uno spazio di lavoro nel demo").toBeGreaterThan(0);
  const parola = `tastiera${Date.now().toString(36)}`;
  const item = await call<{ key: string; assignee_id: string | null }>(dev.req, "POST", `/api/v1/work/spaces/${spaces.items[0].key}/items`, { type: "story", title: `Story ${parola} da assegnare` });
  expect(item.assignee_id).toBeNull();
  await dev.req.dispose();

  await login(page, "admin");
  await page.goto("/richieste");
  await expect(page.getByRole("heading", { name: "Richieste", level: 1 })).toBeVisible();
  await page.waitForLoadState("networkidle");
  const input = palette(page).getByRole("combobox", { name: "Cerca elementi, pagine e azioni" });

  // Una persona.
  await page.keyboard.press("ControlOrMeta+k");
  await input.fill("Marta");
  const persona = palette(page).getByRole("group", { name: "Persone" }).getByRole("option", { name: /Marta Ferri/ });
  await expect(persona).toBeVisible();
  await persona.click();
  await expect(page).toHaveURL(/\/richieste\?q=assegnatario/);

  // Una pagina della configurazione.
  await page.keyboard.press("ControlOrMeta+k");
  await input.fill("persone e accessi");
  const cfg = palette(page).getByRole("group", { name: "Configurazione" }).getByRole("option", { name: /Persone e accessi/ });
  await expect(cfg).toBeVisible();
  await cfg.click();
  await expect(page).toHaveURL(/\/configurazione\/persone/);

  // Un elemento di un altro modulo (lavoro): il tipo accanto alla chiave e «Assegna a me» con Tab + Invio.
  await page.keyboard.press("ControlOrMeta+k");
  await input.fill(item.key);
  const riga = palette(page).getByRole("option", { name: new RegExp(`${item.key}.*${parola}`) });
  await expect(riga).toBeVisible();
  await expect(riga.getByRole("button", { name: "Apri" })).toBeVisible();
  await expect(riga.getByRole("button", { name: "Copia link" })).toBeVisible();
  await input.press("Tab");
  await input.press("Enter");
  await expect(page.getByRole("status")).toContainText(`${item.key} è assegnato a te.`);
  await expect(palette(page)).toHaveCount(0);
  const dopo = await call<{ element: { assignee_id: string | null } }>(page.request, "GET", `/api/v1/elements/${item.key}`);
  const me = await call<{ active_tenant: { member_id: string } }>(page.request, "GET", "/api/v1/me");
  expect(dopo.element.assignee_id).toBe(me.active_tenant.member_id);

  // Comandi: «Crea…» apre il dialogo.
  await page.keyboard.press("ControlOrMeta+k");
  await input.fill("crea: rischio");
  await palette(page).getByRole("option", { name: /Crea: Rischio/ }).click();
  await expect(page.getByRole("dialog", { name: "Crea" }).getByRole("combobox", { name: "Che cosa" })).toContainText("Rischio");
});

test("con un modulo spento ⌘K e «+ Crea» non lo propongono", async ({ page }) => {
  // Si usa l'altra organizzazione: spegnere un modulo non tocca i test che girano sul demo.
  await login(page, "admin", "altro");
  const prima = await call<{ items: { key: string; enabled: boolean }[] }>(page.request, "GET", "/api/v1/tenant/modules");
  const acceso = prima.items.find((m) => m.key === "grc")!.enabled;
  try {
    await call(page.request, "PUT", "/api/v1/tenant/modules", { modules: { grc: true } });
    await page.goto("/richieste");
    await page.waitForLoadState("networkidle");
    await page.keyboard.press("ControlOrMeta+k");
    await palette(page).getByRole("combobox").fill("crea: rischio");
    await expect(palette(page).getByRole("option", { name: /Crea: Rischio/ })).toBeVisible();
    await page.keyboard.press("Escape");

    await call(page.request, "PUT", "/api/v1/tenant/modules", { modules: { grc: false } });
    await page.reload();
    await page.waitForLoadState("networkidle");
    await page.keyboard.press("ControlOrMeta+k");
    await palette(page).getByRole("combobox").fill("rischio");
    await expect(palette(page).getByRole("option", { name: /Rischio/ })).toHaveCount(0);
    await page.keyboard.press("Escape");
    await page.getByRole("button", { name: /^\+ Crea/ }).first().click();
    await page.getByRole("dialog", { name: "Crea" }).getByRole("combobox", { name: "Che cosa" }).click();
    await expect(page.getByRole("option", { name: "Rischio" })).toHaveCount(0);
    await expect(page.getByRole("option", { name: "Richiesta" })).toBeVisible();
  } finally {
    await call(page.request, "PUT", "/api/v1/tenant/modules", { modules: { grc: acceso } });
  }
});

test("il revisore non ha «+ Crea» né comandi di creazione", async ({ page }) => {
  await login(page, "revisore");
  await page.goto("/conformita");
  await page.waitForLoadState("networkidle");
  await expect(page.getByRole("button", { name: /^\+ Crea/ })).toHaveCount(0);
  await page.keyboard.press("c");
  await expect(page.getByRole("dialog", { name: "Crea" })).toHaveCount(0);
  await page.keyboard.press("ControlOrMeta+k");
  await palette(page).getByRole("combobox").fill("crea");
  await expect(palette(page).getByRole("option", { name: /Crea/ })).toHaveCount(0);
});

test("filtro: i campi del tipo della lista in cima; la lista richieste ha le colonne utili", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "richiedente");
  const { key } = await newRequest(req, uniq("Richiesta con colonne"));
  await req.dispose();
  await login(page, "dm");
  await page.goto(`/richieste?q=${encodeURIComponent(`chiave: ${key}`)}`);
  const tabella = page.getByRole("table");
  for (const colonna of ["Unità", "Sponsor", "Stima", "Scadenza", "Punteggio", "Fase", "Assegnatario"]) {
    await expect(tabella.getByRole("columnheader", { name: colonna })).toBeVisible();
  }
  const riga = tabella.getByRole("row").filter({ hasText: key });
  await expect(riga).toContainText("Marta Ferri"); // sponsor
  await expect(riga).toContainText("giorni"); // giorni nella fase

  await page.getByRole("button", { name: "+ Filtro" }).click();
  await expect(page.getByText("Campi di «Richiesta»")).toBeVisible();
  const proposti = page.getByRole("list").filter({ hasText: "Scadenza normativa" });
  await expect(proposti.first()).toBeVisible();
  // I campi dei lavori (Sprint, Punti) non sono tra quelli proposti per le richieste.
  await expect(page.getByRole("button", { name: /^Punti/ })).toHaveCount(0);
  await page.getByPlaceholder("Cerca un campo…").fill("punti");
  await expect(page.getByText("Di altri tipi")).toBeVisible();
});

test("telefono a 375 px: «+ Crea» raggiungibile e ⌘K a tutto schermo", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await login(page, "dm");
  await page.goto("/richieste");
  await expect(page.getByRole("heading", { name: "Richieste", level: 1 })).toBeVisible();
  const crea = page.getByRole("button", { name: "+ Crea" });
  await expect(crea).toBeVisible();
  const box = (await crea.boundingBox())!;
  expect(box.y).toBeGreaterThan(812 / 2); // nella metà bassa, dove arriva il pollice
  expect(box.height).toBeGreaterThanOrEqual(43.5);
  await expectNoHorizontalScroll(page);
  await crea.click();
  await expect(page.getByRole("dialog", { name: "Crea" })).toBeVisible();
  await page.keyboard.press("Escape");

  await page.getByRole("button", { name: "Cerca", exact: true }).click();
  const p = palette(page);
  await expect(p).toBeVisible();
  const b = (await p.boundingBox())!;
  expect(b.width).toBeGreaterThanOrEqual(374);
  expect(b.height).toBeGreaterThanOrEqual(800);
  await p.getByRole("combobox").fill("Marta");
  const persona = p.getByRole("option", { name: /Marta Ferri/ });
  await expect(persona).toBeVisible();
  // Le azioni dirette sono sempre a vista, con un bersaglio da 44 px.
  const azione = p.getByRole("option", { name: /Marta Ferri/ }).getByRole("button", { name: "Aperti da" });
  await expect(azione).toBeVisible();
  expect((await azione.boundingBox())!.height).toBeGreaterThanOrEqual(43.5);
  await expectNoHorizontalScroll(page);
});

test.describe("in inglese", () => {
  test.use({ locale: "en-US" });

  test("pannello «?», «+ Create» e ⌘K senza parole italiane", async ({ page }) => {
    await login(page, "admin");
    await page.goto("/richieste");
    await page.waitForLoadState("networkidle");
    await page.keyboard.press("?");
    const help = page.getByRole("dialog", { name: "Keyboard shortcuts" });
    await expect(help).toBeVisible();
    await expect(help.getByRole("region", { name: "Go to" })).toBeVisible();
    const testo = await help.innerText();
    expect(/\b(Vai|Crea|Cerca|Apri|Chiudi|Commenta|Assegna|Segui)\b/.exec(testo)).toBeNull();
    await page.keyboard.press("Escape");

    await page.getByRole("button", { name: /^\+ Create/ }).first().click();
    const crea = page.getByRole("dialog", { name: "Create" });
    await expect(crea.getByLabel("Title")).toBeVisible();
    await page.keyboard.press("Escape");

    await page.keyboard.press("ControlOrMeta+k");
    const p = page.getByRole("dialog", { name: "Command palette" });
    await p.getByRole("combobox").fill("people and access");
    await expect(p.getByRole("group", { name: "Configuration" })).toBeVisible();
    const t = await p.innerText();
    expect(/\b(Crea|Cerca|Apri|Chiudi|Persone|Configurazione|Comandi)\b/.exec(t)).toBeNull();
  });
});
