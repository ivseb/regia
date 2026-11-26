// Modello di partenza «Persona sola / home lab»: da «Inizia da qui» a una board kanban pronta, in un passo.
// Non si applica al tenant demo condiviso: il test crea un'organizzazione nuova e ci passa.
import { test, expect } from "@playwright/test";
import { call, login } from "./fixtures";

test("persona sola: un passo da «Inizia da qui» alla board con una card Attività", async ({ page }) => {
  await login(page, "admin");
  const id = Date.now().toString(36) + Math.floor(Math.random() * 1e4).toString(36);
  const slug = `persona-${id}`;
  const created = await page.request.post("/api/v1/platform/tenants", { data: { name: `Home lab ${id}`, slug } });
  expect(created.ok(), `creazione organizzazione: ${created.status()}`).toBeTruthy();
  const me = await call(page.request, "GET", "/api/v1/me");
  const t = me.tenants.find((x: { slug: string }) => x.slug === slug);
  expect(t, "organizzazione creata").toBeTruthy();
  const sw = await page.request.post("/api/v1/me/tenant", { data: { tenant_id: t.tenant_id } });
  expect(sw.ok()).toBeTruthy();

  // «Inizia da qui» → Modelli: la scheda del modello e la sua anteprima.
  await page.goto("/configurazione/avvio?tab=modelli");
  const scheda = page.getByTestId("modello-persona_sola");
  await expect(scheda).toContainText("Persona sola / home lab");
  await scheda.getByRole("button", { name: "Vedi cosa crea" }).click();
  const campi = page.getByTestId("persona-campi");
  await expect(campi).toBeVisible();
  const anteprima = page.getByTestId("modello-anteprima");
  await expect(anteprima).toContainText("Attività");
  await expect(anteprima).toContainText("Idea");
  await expect(anteprima).toContainText("Home lab");

  // La chiave della board la sceglie chi applica (unica nell'organizzazione).
  const chiave = `E${id.replace(/[^a-z0-9]/g, "").toUpperCase().slice(0, 8)}`;
  await campi.getByRole("textbox", { name: "Chiave della board" }).fill(chiave);
  await campi.getByRole("textbox", { name: "Nome del team" }).click(); // esce dal campo: l'anteprima si rilegge
  await expect(anteprima).toContainText(`${chiave} · Kanban`);
  await page.getByTestId("modello-applica").click();
  await expect(page.getByTestId("modello-applicato")).toBeVisible();

  // La board dello spazio: kanban senza sprint, tre colonne.
  await page.getByTestId("modello-apri-board").click();
  await expect(page).toHaveURL(new RegExp(`spazio=${chiave}`));
  await expect(page.getByRole("heading", { name: "Board", level: 1 })).toBeVisible();
  for (const colonna of ["Da fare", "In corso", "Fatto"]) {
    await expect(page.getByRole("group", { name: new RegExp(`^${colonna}: \\d+ element[io]$`) })).toBeVisible();
  }
  await expect(page.getByRole("group", { name: /^In revisione/ })).toHaveCount(0);

  // Una card di tipo Attività creata dalla board.
  await page.getByRole("button", { name: "Nuovo elemento" }).click();
  const dialog = page.getByRole("dialog");
  // Il tipo si sceglie dalle schede in cima al dialogo.
  await dialog.getByRole("radio", { name: "Attività" }).click();
  await dialog.getByRole("textbox", { name: /Titolo/ }).fill("Prima attività del laboratorio");
  await dialog.getByRole("button", { name: "Crea" }).click();
  const card = page.getByRole("article", { name: /Prima attività del laboratorio$/ });
  await expect(card).toBeVisible();
  await expect(page.getByRole("group", { name: "Da fare: 1 elemento" })).toBeVisible();

  // Riapplicarlo non duplica niente (via API: anteprima senza oggetti nuovi).
  const dopo = await call(page.request, "GET", `/api/v1/avvio/modelli/persona_sola?board_key=${chiave}&team_name=${encodeURIComponent("Home lab")}`);
  expect(dopo.new).toBe(0);
});
