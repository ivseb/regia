// Nomi per lingua di opzioni dei campi e fasi dei processi: si traducono in Configurazione, il nome
// italiano resta quello canonico, e chi usa l'interfaccia in inglese vede il nome inglese.
import { test, expect } from "@playwright/test";
import { call, login, uniq, scegli } from "./fixtures";

test("etichette: opzione di un campo e fase di un processo tradotte in inglese", async ({ page }) => {
  await login(page, "admin");

  // Opzione di un campo a scelta: «Telefono» → «Phone».
  const name = uniq("Canale di contatto");
  const fkey = name.toLowerCase().replace(/[^a-z0-9]+/g, "_");
  const opt = uniq("Telefono");
  await call(page.request, "POST", "/api/v1/fields", { key: fkey, name, data_type: "enum", options: [{ name: opt }, { name: uniq("Posta") }] });
  await page.goto("/configurazione/campi");
  await page.getByRole("button", { name, exact: true }).click();
  const editor = page.getByRole("complementary", { name: "Modifica del campo" });
  await editor.getByText("Traduzioni", { exact: true }).click();
  await editor.getByLabel(`Nome in inglese di «${opt}»`).fill("Phone");
  await editor.getByRole("button", { name: "Salva", exact: true }).click();
  await expect(async () => {
    const f = await call(page.request, "GET", `/api/v1/fields/${fkey}`);
    expect(f.options[0].name).toBe(opt);
    expect(f.options[0].labels).toEqual({ en: "Phone" });
  }).toPass();

  // Fase di un processo: nell'editor, riquadro «Traduzioni» della fase.
  await page.goto("/processi");
  await page.getByRole("button", { name: "Nuovo processo" }).click();
  const form = page.getByRole("region", { name: "Nuovo processo" });
  await form.getByLabel("Copiando un processo esistente").check();
  await scegli(form.getByLabel("Processo da copiare"), "Ciclo di vita del bug · Bug");
  const pname = uniq("Bug tradotto");
  await form.getByLabel("Nome", { exact: true }).fill(pname);
  await form.getByLabel("Priorità").fill("901");
  await form.getByRole("button", { name: "Crea e apri l'editor" }).click();
  await expect(page.getByRole("heading", { name: pname, level: 1 })).toBeVisible();

  await page.getByRole("button", { name: "+ Fase" }).click();
  const props = page.getByRole("complementary", { name: "Proprietà dell'elemento selezionato" });
  const phaseName = props.getByLabel(/^Nome/).first();
  const phase = uniq("Collaudo");
  await phaseName.fill(phase);
  await phaseName.press("Tab");
  await props.getByText("Traduzioni", { exact: true }).click();
  await props.getByLabel("Nome in inglese", { exact: true }).fill("Acceptance test");
  await page.getByLabel("Note della versione").fill("Fase con il nome inglese.");
  const publish = page.getByRole("button", { name: /^Pubblica v\d+$/ });
  await expect(publish).toBeEnabled();
  const version = Number((await publish.innerText()).replace(/\D/g, ""));
  await publish.click();
  await expect(page.getByRole("status").filter({ hasText: `Pubblicata la v${version}` })).toBeVisible();

  // Il nome resta quello italiano, l'etichetta inglese è disponibile per le liste.
  await expect(async () => {
    const r = await call(page.request, "GET", "/api/v1/state-labels");
    expect(r.items[phase]).toEqual({ en: "Acceptance test" });
  }).toPass();
});

test.describe("in inglese", () => {
  test.use({ locale: "en-US" });

  test("etichette: il nome inglese dell'opzione si vede nella configurazione dei campi", async ({ page }) => {
    await login(page, "admin");
    const name = uniq("Contact channel");
    const fkey = name.toLowerCase().replace(/[^a-z0-9]+/g, "_");
    const opt = uniq("Telefono");
    await call(page.request, "POST", "/api/v1/fields", { key: fkey, name, data_type: "enum", options: [{ name: opt, labels: { en: "Phone line" } }, { name: uniq("Posta") }] });
    await page.goto("/configurazione/campi");
    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    const row = page.getByRole("row").filter({ has: page.getByRole("button", { name, exact: true }) });
    await expect(row.getByText("Phone line")).toBeVisible();
    await expect(row.getByText(opt)).toHaveCount(0);
  });
});
