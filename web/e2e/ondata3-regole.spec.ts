// Ondata 3, proposta 6 (seconda metà): le regole dei processi si leggono e si compilano come frasi
// «Quando… solo se… allora…», e il tipo di valutazione mostra «Quando si apre» con «Aggiungi una regola».
import { test, expect, type Page } from "@playwright/test";
import { apiAs, call, expectNoHorizontalScroll, login, newRequest, scegli, uniq } from "./fixtures";

/** Un processo nuovo copiato dallo standard delle richieste. */
async function nuovoProcesso(page: Page, priority: number) {
  const name = uniq("Processo regole");
  const key = name.toLowerCase().replaceAll(/[^a-z0-9]+/g, "_");
  await call(page.request, "POST", "/api/v1/processes", { key, name, type: "business_request", priority, copy_from: "demand_standard" });
  return { key, name };
}

async function bozzaOCorrente(page: Page, key: string) {
  const p = await call(page.request, "GET", `/api/v1/processes/${key}`);
  return p.draft ?? p.current;
}

test("regola a frase: Quando entra in Valutazione, solo se criticità alta, allora apri la valutazione Security review", async ({ page }) => {
  await login(page, "admin");
  const { key, name } = await nuovoProcesso(page, 905);
  await page.goto(`/processi/${key}`);
  await page.getByRole("button", { name: "+ Nuova regola" }).click();
  const frase = page.getByTestId("regola-frase");
  await expect(frase).toBeVisible();

  await scegli(frase.getByLabel("Evento della regola"), "entra nella fase «Valutazione»");
  await frase.getByRole("button", { name: "Scrivi come testo" }).first().click();
  await frase.getByRole("textbox", { name: /come testo/ }).first().fill("criticita_max: alta");
  await scegli(frase.getByLabel("Aggiungi un'azione…"), "Apri una valutazione");
  await scegli(frase.getByLabel("Tipo di valutazione"), "Security review");

  // La frase intera compare nell'elenco delle regole.
  const elenco = page.getByTestId("regola-frase-elenco").last();
  await expect(elenco).toContainText(/^Quando entra nella fase «Valutazione», solo se .*alta.*, allora apri la valutazione «Security review»/);
  // Niente chiavi tecniche in vista nella frase.
  await expect(elenco).not.toContainText("open_review");

  // Formato salvato compatibile con le regole esistenti.
  await page.getByRole("button", { name: "Salva bozza" }).click();
  // Si aspetta la bozza con la regola completa: sotto carico il salvataggio arriva dopo il clic.
  await expect.poll(async () => ((await bozzaOCorrente(page, key)).rules as { when?: string }[]).at(-1)?.when, { timeout: 15000 }).toBe("criticita_max: alta");
  const regole = (await bozzaOCorrente(page, key)).rules as { on: string; when?: string; actions: { kind: string; ref?: string }[] }[];
  const mia = regole.at(-1)!;
  expect(mia.on).toBe("enter:Valutazione");
  expect(mia.when).toBe("criticita_max: alta");
  expect(mia.actions).toEqual([{ kind: "open_review", ref: "security_review" }]);

  // Pubblicata, compare in «Quando si apre» del tipo, con il link alla regola.
  await call(page.request, "POST", `/api/v1/processes/${key}/publish`, { notes: "regola a frase" });
  await page.goto("/configurazione/valutazioni?tab=tipi");
  const quando = page.getByTestId("quando-si-apre-regola").filter({ hasText: /solo se .*alta.*apri la valutazione «Security review»/ }).filter({ hasText: name }).filter({ hasNotText: /solo per gli impatti/ });
  await expect(quando).toHaveCount(1);
  await quando.getByRole("link", { name: /Apri la regola/ }).click();
  await expect(page).toHaveURL(new RegExp(`/processi/${key}`));
  await expect(page.getByTestId("regola-frase")).toBeVisible();
});

test("«Aggiungi una regola» dal tipo di valutazione, e la regola scatta su un elemento", async ({ page, playwright, baseURL }) => {
  await login(page, "admin");
  const { key, name } = await nuovoProcesso(page, 1);
  await page.goto("/configurazione/valutazioni?tab=tipi");
  const tipo = page.getByRole("listitem").filter({ hasText: "Parere compliance e DORA" }).filter({ has: page.getByTestId("quando-si-apre") }).last();
  await tipo.getByRole("button", { name: "Aggiungi una regola" }).click();
  await scegli(tipo.getByLabel("In quale processo?"), name);
  await tipo.getByRole("button", { name: "Imposta la regola" }).click();

  // L'editor si apre con la regola già impostata su «apri la valutazione di questo tipo».
  await expect(page).toHaveURL(new RegExp(`/processi/${key}`));
  const frase = page.getByTestId("regola-frase");
  await expect(frase).toBeVisible();
  await expect(frase.getByLabel("Tipo di valutazione")).toContainText("Parere compliance e DORA");
  await expect(page.getByTestId("regola-frase-elenco").last()).toContainText("allora apri la valutazione «Parere compliance e DORA»");
  await page.getByRole("button", { name: "Salva bozza" }).click();
  await expect.poll(async () => JSON.stringify((await bozzaOCorrente(page, key)).rules)).toContain("parere_compliance");
  await call(page.request, "POST", `/api/v1/processes/${key}/publish`, { notes: "aggiungi una regola" });

  // La regola scatta: la richiesta entra in Valutazione e il parere (che lo standard apre solo per le
  // funzioni critiche) si apre comunque.
  const { req } = await apiAs(playwright, baseURL!, "richiedente");
  const { id } = await newRequest(req, uniq("Richiesta con regola a frase"));
  await req.dispose();
  const { req: dm } = await apiAs(playwright, baseURL!, "dm");
  for (const to of ["Triage", "Valutazione"]) await call(dm, "POST", `/api/v1/elements/${id}/transitions`, { to, comment: "Passaggio dei test end-to-end." });
  await expect
    .poll(async () => JSON.stringify((await call(dm, "GET", `/api/v1/elements/${id}/reviews`)).items))
    .toContain("parere_compliance");
  await dm.dispose();
  // Il processo di prova vale per le richieste nuove (priorità 1: la più alta): lo si archivia subito.
  await call(page.request, "PATCH", `/api/v1/processes/${key}`, { archived: true });
});

test("375 px: la frase va a capo per parti, bersagli da 44 px, niente scorrimento orizzontale", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 812 });
  await login(page, "admin");
  const { key } = await nuovoProcesso(page, 907);
  await page.goto(`/processi/${key}`);
  await page.getByRole("button", { name: "+ Nuova regola" }).click();
  const frase = page.getByTestId("regola-frase");
  await scegli(frase.getByLabel("Aggiungi un'azione…"), "Assegna un ruolo");
  await expect(frase.getByRole("combobox", { name: "Ruolo" })).toBeVisible();
  await expectNoHorizontalScroll(page);
  for (const c of [frase.getByLabel("Evento della regola"), frase.getByLabel("Aggiungi un'azione…"), frase.getByRole("combobox", { name: "Azione" }), frase.getByRole("combobox", { name: "Ruolo" }), frase.getByRole("button", { name: "Togli l'azione" })]) {
    const box = await c.first().boundingBox();
    expect(box && box.height, "bersaglio da almeno 44 px").toBeGreaterThanOrEqual(43.5);
    expect(box!.x + box!.width).toBeLessThanOrEqual(375);
  }
  await page.goto("/configurazione/valutazioni?tab=tipi");
  await expect(page.getByTestId("quando-si-apre").first()).toBeVisible();
  await expectNoHorizontalScroll(page);
  const bottone = page.getByRole("button", { name: "Aggiungi una regola" }).first();
  expect((await bottone.boundingBox())!.height).toBeGreaterThanOrEqual(43.5);
});
