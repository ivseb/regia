// Ondata 3, catalogo del portale: l'amministratore disegna una voce (passi, campo condizionale, instradamento) e
// la pubblica; il richiedente la trova nel catalogo, la compila, e l'elemento nasce del tipo giusto e assegnato
// al referente del team. Richieste simili con «È questa» / «No, è diversa». Telefono a 375 px.
import { test, expect } from "@playwright/test";
import { apiAs, call, expectNoHorizontalScroll, login, memberId, newRequest, scegli, uniq } from "./fixtures";

test("catalogo: voce «Accesso a un sistema» con due passi e campo condizionale, instradata al team", async ({ page, playwright, baseURL }) => {
  const sfx = `${Date.now().toString(36)}${Math.floor(Math.random() * 1e3).toString(36)}`;
  const typeKey = `accesso_${sfx}`;
  const typeName = `Accesso ${sfx}`;
  const sistemaKey = `sistema_${sfx}`;
  const motivoKey = `motivo_${sfx}`;
  const admin = await apiAs(playwright, baseURL!, "admin");
  await call(admin.req, "POST", "/api/v1/types", { key: typeKey, name: typeName, from: "business_request", module: "strategy" });
  await call(admin.req, "POST", "/api/v1/fields", { key: sistemaKey, name: "Sistema richiesto", data_type: "enum", options: [{ name: "ERP" }, { name: "CRM" }] });
  await call(admin.req, "POST", "/api/v1/fields", { key: motivoKey, name: "Motivo della richiesta", data_type: "string" });
  for (const f of [sistemaKey, motivoKey]) await call(admin.req, "PUT", `/api/v1/fields/${f}/types/${typeKey}`, {});
  const lead = await memberId(admin.req, "Marta Ferri");
  const teamName = uniq("Accessi");
  await call(admin.req, "POST", "/api/v1/teams", { name: teamName, lead_id: lead });

  // --- L'amministratore disegna la voce.
  await login(page, "admin");
  await page.goto("/configurazione/portale");
  await expect(page.getByRole("heading", { name: "Catalogo del portale", level: 1 })).toBeVisible();
  const label = uniq("Accesso a un sistema");
  await page.getByRole("textbox", { name: "Nome della voce" }).fill(label);
  await scegli(page.getByRole("combobox", { name: "Che cosa crea" }), typeName);
  await page.getByRole("button", { name: "Aggiungi la voce" }).click();
  await expect(page.getByRole("heading", { name: label, level: 2 })).toBeVisible();
  // Il modulo nasce con tutti i campi del tipo: si tengono due campi, gli altri si tolgono.
  const step1 = page.getByRole("listitem", { name: "Passo 1" });
  for (;;) {
    const names = await step1.getByRole("button", { name: /^Togli / }).evaluateAll((bs) => bs.map((b) => b.getAttribute("aria-label") ?? ""));
    const extra = names.find((n) => !/Togli il passo|Sistema richiesto|Motivo della richiesta/.test(n));
    if (!extra) break;
    await step1.getByRole("button", { name: extra, exact: true }).click();
  }
  await page.getByRole("button", { name: "Aggiungi un passo" }).click();
  await page.getByRole("textbox", { name: "Titolo del passo 1" }).fill("Chi e cosa");
  await page.getByRole("textbox", { name: "Titolo del passo 2" }).fill("Motivazione");
  await page.getByRole("checkbox", { name: "Sistema richiesto: obbligatorio" }).check();
  // Il motivo passa al secondo passo, è obbligatorio e compare solo per l'ERP.
  await page.getByRole("button", { name: "Sposta in basso Motivo della richiesta" }).click();
  await expect(page.getByRole("listitem", { name: "Passo 2" }).getByText("Motivo della richiesta")).toBeVisible();
  await page.getByRole("checkbox", { name: "Motivo della richiesta: obbligatorio" }).check();
  await page.getByRole("button", { name: "Dettagli di Motivo della richiesta" }).click();
  await page.getByRole("button", { name: "Scrivi come testo" }).click();
  await page.getByRole("textbox", { name: "Mostra il campo come testo" }).fill(`${sistemaKey}: ERP`);
  // Instradamento: se ERP, il ruolo di demand manager al referente del team, con priorità ed etichetta.
  await page.getByRole("button", { name: "Aggiungi una regola" }).click();
  const rule = page.getByRole("listitem", { name: "Regola 1" });
  await rule.getByRole("button", { name: "Scrivi come testo" }).click();
  await rule.getByRole("textbox", { name: "Se come testo" }).fill(`${sistemaKey}: ERP`);
  await scegli(rule.getByRole("combobox", { name: "Allora assegna il ruolo" }), /Demand manager/);
  await scegli(rule.getByRole("combobox", { name: "A", exact: true }), teamName);
  await scegli(rule.getByRole("combobox", { name: "Priorità iniziale" }), "Alta");
  await rule.getByRole("textbox", { name: "Etichetta iniziale" }).fill("Accessi");
  // Anteprima dal vivo (a destra): il passo scelto mostra il campo con l'etichetta della voce.
  const preview = page.getByTestId("portal-preview");
  await preview.getByRole("tab", { name: /Chi e cosa/ }).click();
  await expect(preview.getByText("Sistema richiesto")).toBeVisible();
  // Prima della pubblicazione il richiedente non lo vede.
  const rich = await apiAs(playwright, baseURL!, "richiedente");
  const before = await call(rich.req, "GET", "/api/v1/portal");
  expect(before.entries.find((e: { label: string }) => e.label === label)?.steps.length).toBe(1);
  await page.getByRole("button", { name: "Pubblica" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Il modulo pubblicato è aggiornato" })).toBeVisible();

  // --- Il richiedente la trova nel catalogo e la compila.
  await login(page, "richiedente");
  await page.goto("/portale");
  const catalog = page.getByRole("region", { name: "Nuova richiesta" });
  await catalog.getByRole("searchbox", { name: "Cerca nel catalogo" }).fill("accesso a un sistema");
  await expect(catalog.getByRole("heading", { name: label })).toBeVisible();
  await catalog.getByRole("link", { name: `Inizia: ${label}` }).click();
  await expect(page.getByRole("heading", { name: label, level: 1 })).toBeVisible();
  await page.getByRole("textbox", { name: /In una riga, cosa ti serve/ }).fill(uniq("Accesso al gestionale"));
  const next = page.getByRole("button", { name: "Avanti" });
  await next.click();
  await expect(page.getByRole("heading", { name: "Chi e cosa", level: 2 })).toBeVisible();
  await expect(next).toBeDisabled(); // il sistema è obbligatorio
  const sistema = page.getByRole("group", { name: "Sistema richiesto" });
  await sistema.getByRole("button", { name: "CRM" }).click();
  await next.click();
  await expect(page.getByRole("heading", { name: "Motivazione", level: 2 })).toBeVisible();
  await expect(page.getByLabel(/Motivo della richiesta/)).toHaveCount(0); // con il CRM il campo non c'è
  await page.getByRole("button", { name: "Indietro" }).click();
  await sistema.getByRole("button", { name: "ERP" }).click();
  await next.click();
  const motivo = page.getByLabel(/Motivo della richiesta/);
  await expect(motivo).toBeVisible(); // con l'ERP compare
  await expect(next).toBeDisabled(); // ed è obbligatorio
  await motivo.fill("Chiusura contabile di fine anno");
  await next.click();
  await expect(page.getByRole("heading", { name: "Controlla e invia", level: 2 })).toBeVisible();
  await page.getByRole("button", { name: /^Invia/ }).click();
  await expect(page).toHaveURL(/\/portale\/richieste\/[A-Z]+-\d+/);
  const key = /[A-Z]+-\d+$/.exec(new URL(page.url()).pathname)![0];

  // --- L'elemento è del tipo giusto, assegnato al referente del team, con priorità ed etichetta.
  const d = await call(admin.req, "GET", `/api/v1/elements/${key}`);
  expect(d.element.type).toBe(typeKey);
  const dm = (d.stakeholders as { role: string; member_id: string }[]).filter((s) => s.role === "demand_manager");
  expect(dm.map((s) => s.member_id)).toContain(lead);
  const history = JSON.stringify(await call(admin.req, "GET", `/api/v1/elements/${d.element.id}/activity`));
  expect(history).toContain("Priorità alta");
  expect(history).toContain("Accessi");
  await rich.req.dispose();
  await admin.req.dispose();
});

test("portale: richieste simili con «È questa» e «No, è diversa»", async ({ page, playwright, baseURL }) => {
  const admin = await apiAs(playwright, baseURL!, "admin");
  const word = `quarzo${Date.now().toString(36)}`;
  const a = await newRequest(admin.req, uniq(`Portale clienti ${word} per i reclami`));
  const b = await newRequest(admin.req, uniq(`Portale clienti ${word} per le polizze`));
  const who = await apiAs(playwright, baseURL!, "dev");
  const drafts = await call(who.req, "GET", "/api/v1/portal");
  for (const x of drafts.drafts) await call(who.req, "DELETE", `/api/v1/portal/drafts/${x.id}`);
  try {
    await login(page, "dev");
    await page.goto("/portale/nuova");
    await page.getByRole("textbox", { name: /In una riga, cosa ti serve/ }).fill(`Nuovo portale clienti ${word} per i sinistri`);
    const simili = page.getByRole("region", { name: "Richieste simili già aperte" });
    await expect(simili).toBeVisible();
    await expect(simili.getByRole("button", { name: new RegExp(`No, è diversa ${a.key}`) })).toBeVisible();
    await simili.getByRole("button", { name: new RegExp(`No, è diversa ${a.key}`) }).click();
    await expect(simili.getByRole("button", { name: new RegExp(`No, è diversa ${a.key}`) })).toHaveCount(0);
    // «È questa»: la persona si aggiunge alla richiesta già aperta, con un commento, senza duplicarla.
    await simili.getByRole("button", { name: new RegExp(`È questa ${b.key}`) }).click();
    await expect(page).toHaveURL(new RegExp(`/portale/richieste/${b.key}`));
    await expect(page.getByText("Ho la stessa esigenza: mi aggiungo a questa richiesta.")).toBeVisible();
    const mine = await call(who.req, "GET", "/api/v1/portal");
    expect(mine.requests.map((r: { key: string }) => r.key)).toContain(b.key);
  } finally {
    await who.req.dispose();
    await admin.req.dispose();
  }
});

test("catalogo a 375 px: carte, ricerca, editor a schede senza scorrimento orizzontale", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await login(page, "richiedente");
  await page.goto("/portale");
  const catalog = page.getByRole("region", { name: "Nuova richiesta" });
  await expect(catalog.getByRole("searchbox", { name: "Cerca nel catalogo" })).toBeVisible();
  await expectNoHorizontalScroll(page);
  const start = catalog.getByRole("link", { name: /^Inizia/ }).first();
  expect((await start.boundingBox())!.height).toBeGreaterThanOrEqual(43.5);
  await start.click();
  await expect(page.getByRole("heading", { level: 2 }).first()).toBeVisible();
  await expectNoHorizontalScroll(page);

  await login(page, "admin");
  await page.goto("/configurazione/portale");
  await expect(page.getByRole("heading", { name: "Voci del catalogo", level: 3 })).toBeVisible();
  await expectNoHorizontalScroll(page);
  await page.getByRole("button", { name: /^Modifica / }).first().click();
  await expect(page.getByRole("tab", { name: "Anteprima" })).toBeVisible();
  await expectNoHorizontalScroll(page);
  for (const name of ["Pubblica", "Salva bozza"]) expect((await page.getByRole("button", { name }).boundingBox())!.height).toBeGreaterThanOrEqual(43.5);
  await page.getByRole("tab", { name: "Anteprima" }).click();
  await expect(page.getByTestId("portal-preview")).toBeVisible();
  await expectNoHorizontalScroll(page);
});
