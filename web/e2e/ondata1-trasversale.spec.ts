// Ondata 1, pacchetto trasversale: moduli accendibili per tenant, menu del revisore, ⌘K con il
// tipo accanto alla chiave, pagina 404, titoli e bersagli sul telefono.
import { test, expect } from "@playwright/test";
import { apiAs, call, expectNoHorizontalScroll, login, newRequest, uniq } from "./fixtures";

// Il test dei moduli spegne e riaccende un modulo nel tenant «altro» (solo suo): in serie.
test.describe.configure({ mode: "serial" });

test("un modulo spento sparisce da menu, ⌘K e pagine, e le sue API rispondono 404", async ({ page }) => {
  await login(page, "admin", "altro");
  const modules = (await call(page.request, "GET", "/api/v1/tenant/modules")).items;
  expect(modules.map((m: { key: string }) => m.key)).toEqual(["demand", "projects", "work", "knowledge", "devsecops", "grc", "architecture", "continuity"]);
  try {
    await page.goto("/configurazione/moduli");
    await expect(page.getByRole("heading", { name: "Moduli", level: 1 })).toBeVisible();
    const nav = page.getByRole("navigation", { name: "Navigazione principale" });
    await expect(nav.getByRole("link", { name: "Documenti", exact: true })).toBeVisible();

    // Si spegne Knowledge dalla pagina, con la spiegazione di cosa cambia.
    await page.getByRole("checkbox", { name: "Knowledge" }).uncheck();
    await expect(page.getByText("Si spengono: Knowledge.")).toBeVisible();
    await page.getByRole("button", { name: "Salva le modifiche" }).click();
    await expect(page.getByRole("button", { name: "Salva le modifiche" })).toBeDisabled();

    // Menu: sparisce la voce dei documenti; il resto resta.
    await expect(nav.getByRole("link", { name: "Documenti", exact: true })).toHaveCount(0);
    await expect(nav.getByRole("link", { name: "Architettura", exact: true })).toBeVisible();
    // Indice degli elenchi: sparisce l'elenco dei documenti, gli altri restano.
    await nav.getByRole("link", { name: "Elenchi", exact: true }).click();
    await expect(page.getByRole("heading", { name: "Elenchi", level: 1 })).toBeVisible();
    await expect(page.locator('a[href="/elenco/rischi"]').first()).toBeVisible();
    await expect(page.locator('a[href="/elenco/documenti"]')).toHaveCount(0);

    // ⌘K: la pagina non si trova più, le altre sì.
    await page.keyboard.press("ControlOrMeta+k");
    const dialog = page.getByRole("dialog", { name: "Palette dei comandi" });
    const input = dialog.getByRole("combobox");
    await input.fill("documenti");
    await expect(dialog.getByRole("option", { name: /Documenti/ })).toHaveCount(0);
    await input.fill("architettura");
    await expect(dialog.getByRole("option", { name: /Architettura/ }).first()).toBeVisible();
    await page.keyboard.press("Escape");

    // Pagina: l'indirizzo diretto dice che il modulo è spento (e come riaccenderlo).
    await page.goto("/documenti");
    await expect(page.getByRole("heading", { name: "Modulo spento", level: 1 })).toBeVisible();
    await expect(page.getByRole("link", { name: "Vai ai moduli" })).toBeVisible();
    await expect(page).toHaveTitle("Modulo spento · Regia");

    // API: 404 coerente con il codice del modulo; i dati non si toccano.
    const r = await page.request.get("/api/v1/kb/spaces");
    expect(r.status()).toBe(404);
    expect((await r.json()).code).toBe("module.disabled");
  } finally {
    await call(page.request, "PUT", "/api/v1/tenant/modules", { modules: { knowledge: true } });
  }
  // Riacceso, tutto torna com'era.
  expect((await page.request.get("/api/v1/kb/spaces")).status()).toBe(200);
  await page.goto("/documenti");
  await expect(page.getByRole("navigation", { name: "Navigazione principale" }).getByRole("link", { name: "Documenti", exact: true })).toBeVisible();
});

test("il revisore ha il menu ridotto ai moduli di controllo", async ({ page }) => {
  await login(page, "revisore");
  const me = await call(page.request, "GET", "/api/v1/me");
  expect(me.active_tenant.menu_profile).toBe("auditor");
  await page.goto("/conformita");
  const nav = page.getByRole("navigation", { name: "Navigazione principale" });
  for (const name of ["Rischi e conformità", "DevSecOps", "Architettura", "Documenti", "Registro di audit"]) {
    await expect(nav.getByRole("link", { name, exact: true }), name).toBeVisible();
  }
  // Conformità e Rischi sono schede della stessa voce.
  const grc = page.getByRole("navigation", { name: "Rischi e conformità" });
  for (const name of ["Conformità", "Rischi e incidenti"]) {
    await expect(grc.getByRole("link", { name, exact: true }), name).toBeVisible();
  }
  for (const name of ["Richieste", "Cruscotto", "Graduatoria", "Le mie approvazioni", "Board dei team", "Timesheet", "Nuova richiesta"]) {
    await expect(nav.getByRole("link", { name, exact: true }), name).toHaveCount(0);
  }
  // Nessun gruppo di azioni per creare in ⌘K.
  await page.keyboard.press("ControlOrMeta+k");
  const dialog = page.getByRole("dialog", { name: "Palette dei comandi" });
  await dialog.getByRole("combobox").fill("nuova richiesta");
  await expect(dialog.getByRole("option", { name: /Nuova richiesta/ })).toHaveCount(0);
});

test("⌘K mostra il tipo accanto alla chiave e raggruppa i risultati per tipo", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "richiedente");
  const word = `granito${Date.now().toString(36)}`;
  const { key } = await newRequest(req, uniq(`Rilievo ${word} sul catalogo`));
  await req.dispose();
  await login(page, "dm");
  await page.goto("/richieste");
  await expect(page.getByRole("heading", { name: "Richieste", level: 1 })).toBeVisible();
  await page.keyboard.press("ControlOrMeta+k");
  const dialog = page.getByRole("dialog", { name: "Palette dei comandi" });
  await dialog.getByRole("combobox").fill(key);
  const option = dialog.getByRole("option", { name: new RegExp(`Richiesta · ${key}`) });
  await expect(option).toBeVisible();
  // Il gruppo porta il nome del tipo al plurale.
  await expect(dialog.getByRole("group", { name: "Richieste" })).toBeVisible();
  await expect(dialog.getByRole("group", { name: "Richieste" }).getByRole("option", { name: new RegExp(key) })).toBeVisible();

  // Un impatto del demo: l'applicazione è il titolo, la richiesta il contesto (non due volte lo stesso testo).
  const found = await call<{ items: { key: string; title: string }[] }>(page.request, "GET", "/api/v1/elements?q=" + encodeURIComponent("tipo: impact") + "&limit=50");
  const sample = found.items.find((i) => i.title.includes(" · "));
  expect(sample, "un impatto del demo con titolo «applicazione · richiesta»").toBeTruthy();
  await dialog.getByRole("combobox").fill(sample!.key);
  const imp = dialog.getByRole("option", { name: new RegExp(`Impatto · ${sample!.key}`) });
  await expect(imp).toBeVisible();
  await expect(dialog.getByRole("group", { name: "Impatti" })).toBeVisible();
  const text = (await imp.innerText()).replace(/\s+/g, " ");
  const [app, request] = sample!.title.split(" · ", 2);
  expect(text).toContain(app);
  expect(text).toContain(`richiesta: ${request.slice(0, 20)}`);
  expect(text.split(app).length - 1, `«${app}» ripetuto in «${text}»`).toBe(1);
});

test("404: pagina con stile, titolo e link per ripartire", async ({ page }) => {
  await login(page, "dm");
  await page.goto("/architettura/applicazioni-che-non-esiste");
  await expect(page.getByRole("heading", { name: "Pagina non trovata", level: 1 })).toBeVisible();
  await expect(page).toHaveTitle("Pagina non trovata · Regia");
  await expect(page.getByText("/architettura/applicazioni-che-non-esiste")).toBeVisible();
  await page.getByRole("link", { name: "Vai all'inizio" }).click();
  await expect(page).not.toHaveURL(/non-esiste/);
});

test("menu e titoli: «Governance» e «Architettura» uguali nel menu e nelle pagine", async ({ page }) => {
  await login(page, "dm");
  await page.goto("/architettura/mappa");
  const nav = page.getByRole("navigation", { name: "Navigazione principale" });
  // «Governance» è una scheda del «Portafoglio» (menu snello), con lo stesso nome nella scheda e nella pagina.
  await expect(nav.getByRole("link", { name: "Portafoglio", exact: true })).toBeVisible();
  await expect(nav.getByText("Governo", { exact: true })).toHaveCount(0);
  await expect(nav.getByRole("link", { name: "Mappa dell'architettura" })).toHaveCount(0);
  await expect(nav.getByRole("link", { name: "Architettura", exact: true })).toBeVisible();
  // Anche dentro una scheda della mappa la voce resta quella attiva.
  await expect(nav.getByRole("link", { name: "Architettura", exact: true })).toHaveAttribute("aria-current", "page");
  await expect(page.getByRole("heading", { name: "Architettura", level: 1 })).toBeVisible();
  await page.goto("/governance");
  await expect(page.getByRole("navigation", { name: "Portafoglio" }).getByRole("link", { name: "Governance", exact: true })).toHaveAttribute("aria-current", "page");
  await expect(nav.getByRole("link", { name: "Portafoglio", exact: true })).toHaveAttribute("aria-current", "page");
  await expect(page).toHaveTitle("Governance · Regia");
  await page.goto("/richieste/nuova");
  await expect(page.getByRole("heading", { name: "Nuova richiesta", level: 1 })).toBeVisible();
});

test.describe("telefono (375 px)", () => {
  test.use({ viewport: { width: 375, height: 812 }, isMobile: true, hasTouch: true });

  test("bersagli da 44 px, salto al contenuto e titolo di pagina", async ({ page }) => {
    await login(page, "dm");
    await page.goto("/richieste");
    await expect(page.getByRole("heading", { name: "Richieste", level: 1 })).toBeVisible();
    await expect(page).toHaveTitle("Richieste · Regia");
    await expectNoHorizontalScroll(page);
    await expect(page.getByRole("link", { name: "Salta al contenuto" })).toHaveCount(1);

    // Pulsanti della barra in alto e voci del menu: almeno 44 px di altezza.
    const header = page.locator("header").first();
    for (const name of ["Cerca", "Menu"]) {
      const box = await header.getByRole("button", { name, exact: true }).boundingBox();
      expect(box!.height, `altezza di «${name}»`).toBeGreaterThanOrEqual(43.5);
    }
    await header.getByRole("button", { name: "Menu" }).click();
    const link = page.getByRole("navigation", { name: "Navigazione principale" }).getByRole("link", { name: "Cruscotto", exact: true });
    await expect(link).toBeVisible();
    expect((await link.boundingBox())!.height).toBeGreaterThanOrEqual(43.5);
  });
});
