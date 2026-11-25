// Connettore Gitea: la pagina si apre, la configurazione si salva (senza rete esterna: la
// creazione non contatta Gitea) e si legge a 375 px senza scorrimento orizzontale.
import { test, expect } from "@playwright/test";
import { login } from "./fixtures";

test.describe("Connettore Gitea", () => {
  test("si apre e salva una configurazione", async ({ page }) => {
    await login(page, "admin");
    await page.goto("/configurazione/integrazioni/gitea");
    await expect(page.getByRole("heading", { name: "Gitea" })).toBeVisible();
    await page.getByRole("button", { name: "Nuova integrazione" }).click();
    const nome = `Gitea locale ${Date.now()}`;
    await page.getByLabel("Nome", { exact: true }).fill(nome);
    await page.getByLabel("Indirizzo di Gitea").fill("https://gitea.example.org");
    await page.getByPlaceholder("es. acme, mario").fill("acme, mario");
    await page.getByLabel("Token di accesso").fill("token-di-prova-0123456789");
    await page.getByRole("button", { name: "Crea l'integrazione" }).click();
    await expect(page.getByText(nome).first()).toBeVisible();
    await expect(page.getByText("0 associati su 0 repository").first()).toBeVisible();
    // Il token non torna mai.
    await expect(page.getByText("token-di-prova-0123456789")).toHaveCount(0);
  });

  test("a 375 px non scorre in orizzontale", async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 812 });
    await login(page, "admin");
    await page.goto("/configurazione/integrazioni/gitea");
    await expect(page.getByRole("heading", { name: "Gitea" })).toBeVisible();
    const over = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(over).toBeLessThanOrEqual(1);
  });
});
