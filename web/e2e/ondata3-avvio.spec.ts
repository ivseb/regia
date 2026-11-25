// Ondata 3, proposta 7B: primo avvio guidato. Un tenant nuovo (slug «avvio», solo dell'account avvio@)
// mostra stati vuoti con spiegazione e pulsante; «Inizia da qui» calcola i passi dal sistema; il modello
// banca/assicurazione ha l'anteprima prima di applicare e porta i testi normativi «da verificare»;
// un processo di partenza modificato si riporta all'originale con l'anteprima della differenza; telefono.
import { test, expect } from "@playwright/test";
import { apiAs, call, expectNoHorizontalScroll, login } from "./fixtures";

// Tutti i test lavorano sullo stesso tenant, uno dopo l'altro.
test.describe.configure({ mode: "serial" });

const pageHas = (page: import("@playwright/test").Page, text: string | RegExp) => expect(page.getByText(text).first()).toBeVisible();

test("tenant vuoto: stati vuoti con una frase e il pulsante giusto, senza la query in mostra", async ({ page }) => {
  await login(page, "avvio", "avvio");

  await page.goto("/richieste");
  const vuoto = page.getByTestId("lista-vuota");
  await expect(vuoto).toBeVisible();
  await expect(vuoto.getByText("Ancora nessuna richiesta")).toBeVisible();
  await expect(vuoto.getByRole("link", { name: "Crea la prima richiesta" })).toBeVisible();
  await expect(vuoto.getByRole("link", { name: "Oppure parti da un modello" })).toBeVisible();
  // Né la frase della query vuota né la barra della query.
  await expect(page.getByText("corrisponde alla query")).toHaveCount(0);
  await expect(page.getByRole("combobox", { name: /query/i })).toHaveCount(0);

  await page.goto("/rischi?tab=rischi");
  await pageHas(page, "Nessun rischio aperto");
  await expect(page.getByRole("button", { name: "Registra il primo rischio" })).toBeVisible();

  await page.goto("/conformita");
  await pageHas(page, "Nessuna normativa abilitata");
  await expect(page.getByRole("link", { name: "Parti da un modello" })).toBeVisible();

  await page.goto("/documenti");
  await pageHas(page, "Nessuno spazio di documentazione");
  await expect(page.getByRole("button", { name: "Crea il primo spazio" })).toBeVisible();

  // Senza team non c'è nessun board: lo stato vuoto porta ai team.
  await page.goto("/delivery/board");
  await pageHas(page, "Nessuno spazio di delivery");
  await expect(page.getByRole("link", { name: "Vai ai team" })).toBeVisible();
});

test("Inizia da qui: striscia in evidenza e passi calcolati dal sistema, ognuno porta alla pagina giusta", async ({ page }) => {
  await login(page, "avvio", "avvio");
  await page.goto("/richieste");
  const strip = page.getByTestId("avvio-striscia");
  await expect(strip).toContainText(/di 8 passi fatti/);
  await strip.getByRole("link", { name: "Apri" }).click();

  await expect(page).toHaveURL(/\/configurazione\/avvio/);
  await expect(page.getByRole("heading", { name: "Inizia da qui" })).toBeVisible();
  await expect(page.locator("[data-testid^='passo-'][data-done]")).toHaveCount(8);
  // Un tenant appena creato non ha ancora norme, team né prime richieste, e il sistema non chiede di spuntare niente.
  for (const k of ["norme", "team", "richiesta"]) await expect(page.getByTestId(`passo-${k}`)).toHaveAttribute("data-done", "false");
  await expect(page.getByRole("checkbox")).toHaveCount(0);
  // Richiudere non si può finché i passi non sono tutti fatti.
  await expect(page.getByRole("button", { name: "Richiudi «Inizia da qui»" })).toHaveCount(0);

  await page.getByTestId("passo-persone").getByRole("link").click();
  await expect(page).toHaveURL(/\/configurazione\/persone/);
});

test("modello banca/assicurazione: anteprima prima di applicare, testi normativi «da verificare», passi che avanzano", async ({ page }) => {
  await login(page, "avvio", "avvio");
  await page.goto("/configurazione/avvio?tab=modelli");

  // Gli altri modelli ci sono ma sono «in arrivo», senza anteprima.
  const sw = page.getByTestId("modello-software_house");
  await expect(sw).toContainText("In arrivo");
  await expect(sw.getByRole("button")).toHaveCount(0);

  const banca = page.getByTestId("modello-banca_assicurazione");
  await banca.getByRole("button", { name: "Vedi cosa crea" }).click();
  const anteprima = banca.getByTestId("modello-anteprima");
  await expect(anteprima).toBeVisible();
  await expect(anteprima.getByTestId("nota-da-verificare")).toContainText("non fanno fede");
  await expect(anteprima.getByRole("heading", { name: /Cataloghi normativi/ })).toBeVisible();
  const dora = anteprima.getByRole("listitem").filter({ hasText: "DORA" });
  await expect(dora).toContainText("Da verificare");
  await expect(dora).toContainText("Nuovo");
  await expect(anteprima.getByRole("listitem").filter({ hasText: "NIS2" })).toContainText("Da verificare");
  await expect(anteprima.getByRole("listitem").filter({ hasText: "Comitato rischi ICT" })).toContainText("Nuovo");
  await expect(anteprima.getByRole("heading", { name: /Processi/ })).toBeVisible();
  // L'anteprima non ha scritto niente.
  const prima = await call<{ done: number; model: string }>(page.request, "GET", "/api/v1/avvio");
  expect(prima.model).toBe("");
  expect(prima.done).toBeLessThan(8);

  await banca.getByRole("button", { name: "Applica il modello" }).click();
  const fatto = banca.getByTestId("modello-applicato");
  await expect(fatto).toContainText("Fatto");
  await expect(fatto.getByRole("listitem").filter({ hasText: "DORA" })).toContainText("Creato");

  // I passi avanzano da soli: moduli scelti, processo, organi di governo e norme.
  await page.goto("/configurazione/avvio");
  for (const k of ["moduli", "processo", "organi", "norme"]) await expect(page.getByTestId(`passo-${k}`)).toHaveAttribute("data-done", "true");
  await expect(page.getByTestId("passo-richiesta")).toHaveAttribute("data-done", "false");

  // Cosa è applicato: i cataloghi sono «da verificare» finché una persona non li conferma.
  await page.goto("/configurazione/avvio?tab=applicato");
  const cat = page.getByTestId("catalogo-dora");
  await expect(cat).toHaveAttribute("data-unverified", "true");
  await expect(cat.getByText("Da verificare")).toBeVisible();
  await cat.getByRole("button", { name: /Segna come verificato/ }).click();
  await expect(page.getByTestId("catalogo-dora")).toHaveAttribute("data-unverified", "false");
  await expect(page.getByTestId("catalogo-nis2")).toHaveAttribute("data-unverified", "true");
});

test("prima richiesta: il passo si completa e lo stato vuoto sparisce", async ({ page, playwright, baseURL }) => {
  const { req, me } = await apiAs(playwright, baseURL!, "avvio", "avvio");
  await call(req, "POST", "/api/v1/demand/requests", {
    title: "Prima richiesta del primo avvio", description: "Creata dal test end-to-end.", sponsor_id: me.active_tenant!.member_id, fields: { urgenza: "Media" },
  });
  await login(page, "avvio", "avvio");
  await page.goto("/configurazione/avvio");
  await expect(page.getByTestId("passo-richiesta")).toHaveAttribute("data-done", "true");
  await page.goto("/richieste");
  await expect(page.getByTestId("lista-vuota")).toHaveCount(0);
  await expect(page.getByText("Prima richiesta del primo avvio").first()).toBeVisible();
});

test("ripristina il pacchetto di partenza: un processo modificato torna all'originale, con anteprima e conferma", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "avvio", "avvio");
  const key = "demand_standard";
  await login(page, "avvio", "avvio");
  await page.goto("/configurazione/avvio?tab=applicato");
  await expect(page.getByTestId(`processo-${key}`)).toHaveAttribute("data-state", "original");
  await expect(page.getByTestId(`processo-${key}`).getByRole("button", { name: /Ripristina/ })).toHaveCount(0);

  // Qualcuno modifica la prima fase e pubblica.
  const proc = await call<{ current: { phases: { description?: string }[] } }>(req, "GET", `/api/v1/processes/${key}`);
  proc.current.phases[0].description = "Fase cambiata dal test";
  await call(req, "PUT", `/api/v1/processes/${key}/draft`, { definition: proc.current, notes: "Modifica del test" });
  await call(req, "POST", `/api/v1/processes/${key}/publish`, { notes: "Modifica del test" });

  await page.reload();
  const riga = page.getByTestId(`processo-${key}`);
  await expect(riga).toHaveAttribute("data-state", "modified");
  await riga.getByRole("button", { name: /Ripristina il pacchetto di partenza/ }).click();
  const pannello = riga.getByTestId("ripristino-pannello");
  await expect(pannello).toBeVisible();
  await expect(pannello.getByTestId("ripristino-differenza").first()).toContainText("Fase cambiata dal test");
  // Non ha ancora fatto niente.
  const ancora = await call<{ processes: { key: string; state: string }[] }>(req, "GET", "/api/v1/avvio/applicato");
  expect(ancora.processes.find((p) => p.key === key)!.state).toBe("modified");

  await pannello.getByRole("button", { name: "Annulla" }).click();
  await expect(riga.getByTestId("ripristino-pannello")).toHaveCount(0);
  await riga.getByRole("button", { name: /Ripristina il pacchetto di partenza/ }).click();
  await riga.getByRole("button", { name: "Conferma il ripristino" }).click();
  await expect(riga).toHaveAttribute("data-state", "original");
  await expect(riga).toContainText("ripristinato il");
});

test("telefono a 375 px: niente scorrimento orizzontale e bersagli da 44 px", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await login(page, "avvio", "avvio");
  for (const tab of ["passi", "modelli", "applicato"]) {
    await page.goto(`/configurazione/avvio?tab=${tab}`);
    await expect(page.getByRole("heading", { name: "Inizia da qui" })).toBeVisible();
    await expect(page.getByRole("tab", { selected: true })).toBeVisible();
    await expectNoHorizontalScroll(page);
  }
  await page.goto("/configurazione/avvio?tab=passi");
  for (const l of await page.getByRole("tab").all()) expect((await l.boundingBox())!.height).toBeGreaterThanOrEqual(44);
  for (const l of await page.getByTestId("passo-moduli").getByRole("link").all()) expect((await l.boundingBox())!.height).toBeGreaterThanOrEqual(44);
  // Anteprima del modello e ripristino sul telefono.
  await page.goto("/configurazione/avvio?tab=modelli");
  await page.getByTestId("modello-banca_assicurazione").getByRole("button", { name: /Vedi cosa crea|Chiudi l'anteprima/ }).click();
  await expect(page.getByTestId("modello-anteprima")).toBeVisible();
  await expectNoHorizontalScroll(page);
  for (const b of await page.getByTestId("modello-banca_assicurazione").getByRole("button").all()) expect((await b.boundingBox())!.height).toBeGreaterThanOrEqual(44);
});
