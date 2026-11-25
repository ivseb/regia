// Organizzazioni (tenant) dall'interfaccia: solo il superamministratore le crea, ci entra e vede come
// metterci dentro le persone. L'account admin@demo.test lo è per il seed (scripts/dev-seed.sh).
import { test, expect } from "@playwright/test";
import { call, expectNoHorizontalScroll, login, scegli, uniq } from "./fixtures";

test("chi non è superamministratore non vede il link e riceve un rifiuto", async ({ page }) => {
  await login(page, "dm");
  const r = await page.request.get("/api/v1/platform/tenants");
  expect(r.status()).toBe(403);
  await page.goto("/piattaforma/organizzazioni");
  await expect(page.getByRole("alert")).toContainText("riservata al superamministratore");
  await expect(page.getByRole("link", { name: "Gestisci organizzazioni" })).toHaveCount(0);
});

test("il superamministratore crea un'organizzazione, ci entra e vede come invitare persone", async ({ page }) => {
  await login(page, "admin");
  await page.goto("/richieste");
  await page.getByRole("link", { name: "Gestisci organizzazioni" }).click();
  await expect(page.getByRole("heading", { name: "Organizzazioni", level: 1 })).toBeVisible();

  const nome = uniq("Org e2e");
  await page.getByLabel("Nome", { exact: true }).fill(nome);
  // L'identificativo si propone dal nome.
  await expect(page.getByLabel("Identificativo")).toHaveValue(/^org-e2e-[a-z0-9]+$/);
  const slug = await page.getByLabel("Identificativo").inputValue();
  await scegli(page.getByRole("combobox", { name: "Lingua" }), "English");
  await page.getByRole("button", { name: "Crea organizzazione" }).click();

  const fatto = page.getByTestId("organizzazione-creata");
  await expect(fatto).toBeVisible();
  await expect(fatto.getByRole("heading", { name: `«${nome}» è pronta` })).toBeVisible();
  await expect(page.getByTestId("organizzazione-persone")).toContainText("Aggiungi persona");
  await expect(page.getByTestId(`org-${slug}`)).toBeVisible();

  // Un identificativo già usato è rifiutato con il messaggio specifico.
  const dup = await page.request.post("/api/v1/platform/tenants", { data: { name: "Doppia", slug } });
  expect(dup.status()).toBe(409);
  expect((await dup.json()).code).toBe("validation.organizzazioni.slug_taken");

  // «Entra» cambia organizzazione e porta a «Inizia da qui» (nella lingua scelta).
  await fatto.getByRole("button", { name: "Entra", exact: true }).click();
  await expect(page).toHaveURL(/\/configurazione\/avvio/);
  await expect(page.getByRole("heading", { name: "Start here", level: 1 })).toBeVisible();
  const me = await call(page.request, "GET", "/api/v1/me");
  expect(me.tenants.find((t: { tenant_id: string }) => t.tenant_id === me.active_tenant.tenant_id).slug).toBe(slug);

  // Dentro c'è solo chi l'ha creata: le persone del demo non si vedono.
  const { items } = await call<{ items: { display_name: string }[] }>(page.request, "GET", "/api/v1/members");
  expect(items.map((m) => m.display_name)).not.toContain("Luca Neri");

  // Il percorso per le persone: Configurazione → Persone, con «Aggiungi persona» e «Invita».
  await page.goto("/configurazione/persone");
  await expect(page.getByRole("button", { name: "Add person" })).toBeVisible();
});

test("a 375 px la pagina Organizzazioni non scorre in orizzontale", async ({ page }) => {
  await page.setViewportSize({ width: 375, height: 800 });
  await login(page, "admin");
  await page.goto("/piattaforma/organizzazioni");
  await expect(page.getByRole("heading", { name: "Organizzazioni", level: 1 })).toBeVisible();
  await expect(page.getByTestId("organizzazioni-elenco")).toBeVisible();
  await expectNoHorizontalScroll(page);
});
