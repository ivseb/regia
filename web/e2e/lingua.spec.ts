// Lingua dell'interfaccia (replica/lingua.md, passo 1): Accept-Language alla prima visita,
// accesso e Profilo in inglese, errori tradotti dal `code`, date nel formato della lingua,
// cambio dal Profilo senza ricaricare e persistenza della scelta.
import { test, expect, type Page, type Playwright } from "@playwright/test";
import { PASSWORD, apiAs, call, scegli, uniq } from "./fixtures";

/** Persona nuova, senza ruoli e senza scelta di lingua: la scelta non tocca gli altri test. */
async function freshMember(playwright: Playwright, baseURL: string) {
  const { req } = await apiAs(playwright, baseURL, "admin");
  const email = `${uniq("lingua").replace(" ", "-")}@demo.test`;
  const added = await call(req, "POST", "/api/v1/admin/members", { email, display_name: "Prova lingua", member_type: "standard", mode: "invite", role_ids: [], group_ids: [] });
  const token = added.link.path.split("/").pop();
  await call(req, "POST", `/api/v1/auth/invitations/${token}/accept`, { display_name: "Prova lingua", password: PASSWORD });
  await req.dispose();
  return email;
}

async function signIn(page: Page, email: string, password: string, button: string) {
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: button, exact: true }).click();
}

test.describe("prima visita con il browser in inglese", () => {
  test.use({ locale: "en-US" });

  test("l'accesso è in inglese, l'errore è tradotto dal code e vale Accept-Language", async ({ page, playwright, baseURL }) => {
    const email = await freshMember(playwright, baseURL!);
    await page.goto("/profilo");
    await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
    await expect(page.locator("html")).toHaveAttribute("lang", "en");
    await expect(page.getByText("From request to release, with the rules in plain sight.")).toBeVisible();

    // Password sbagliata: il detail del server è italiano, il client mostra la traduzione del code.
    await signIn(page, email, "sbagliata-123456", "Sign in");
    await expect(page.getByRole("alert")).toContainText("Incorrect email or password.");
    await expect(page.getByRole("alert")).not.toContainText("credenziali");

    // Accesso: la persona non ha scelto, vale Accept-Language («browser»).
    await signIn(page, email, PASSWORD, "Sign in");
    await expect(page.getByRole("heading", { name: "Prova lingua", level: 1 })).toBeVisible();
    // Con fetch dal browser (page.request non manda Accept-Language): la lingua viene dall'intestazione.
    const me = await page.evaluate(() => fetch("/api/v1/me").then((r) => r.json()));
    expect(me.language).toMatchObject({ effective: "en", source: "browser", member: null, tenant: null });

    // Profilo in inglese; le date seguono la lingua (mese per primo).
    const tok = await call(page.request, "POST", "/api/v1/me/tokens", { name: "Weekly report", expires_in_days: 90, scopes: [] });
    await page.reload();
    await expect(page.getByRole("heading", { name: "Language" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "API tokens" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Appearance", exact: true })).toBeVisible();
    const expires = new Date(tok.item.expires_at).toLocaleDateString("en-US", { timeZone: "Europe/Rome" });
    await expect(page.getByText(`Expires on ${expires}`)).toBeVisible();
  });
});

test("il cambio dal Profilo è immediato, senza ricaricare, e resta", async ({ page, playwright, baseURL }) => {
  const email = await freshMember(playwright, baseURL!);
  await page.goto("/profilo");
  await signIn(page, email, PASSWORD, "Accedi");
  await expect(page.getByRole("heading", { name: "Lingua", exact: true })).toBeVisible();
  await expect(page.locator("html")).toHaveAttribute("lang", "it");
  const tok = await call(page.request, "POST", "/api/v1/me/tokens", { name: "Report", expires_in_days: 30, scopes: [] });
  await page.reload();
  const expiresIt = new Date(tok.item.expires_at).toLocaleDateString("it-IT", { timeZone: "Europe/Rome" });
  await expect(page.getByText(`Scade il ${expiresIt}`)).toBeVisible();

  // Segno nella pagina: se la pagina si ricarica, sparisce.
  await page.evaluate(() => ((window as unknown as { __segno: number }).__segno = 1));
  const names = page.waitForRequest((r) => /\/api\/v1\/type-names\?lang=en/.test(r.url()));
  await scegli(page.getByLabel("Lingua", { exact: true }), { value: "en" });

  await expect(page.getByRole("heading", { name: "API tokens" })).toBeVisible();
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await expect(page.getByText(`Expires on ${new Date(tok.item.expires_at).toLocaleDateString("en-US", { timeZone: "Europe/Rome" })}`)).toBeVisible();
  expect(await page.evaluate(() => (window as unknown as { __segno?: number }).__segno)).toBe(1);
  await names; // i nomi dei tipi si chiedono nella lingua della persona
  const me = await call(page.request, "GET", "/api/v1/me");
  expect(me.language).toMatchObject({ effective: "en", source: "member", member: "en" });

  // La scelta resta dopo il ricaricamento.
  await page.reload();
  await expect(page.getByRole("heading", { name: "API tokens" })).toBeVisible();
  await expect(page.locator("html")).toHaveAttribute("lang", "en");

  // «Come il browser»: si torna a seguire il predefinito (qui il browser è italiano).
  await scegli(page.getByLabel("Language", { exact: true }), { value: "" });
  await expect(page.getByRole("heading", { name: "Token API" })).toBeVisible();
  await expect(page.locator("html")).toHaveAttribute("lang", "it");
});

test("pagina non trovata e stato dell'accesso: in inglese con la lingua scelta", async ({ page, playwright, baseURL }) => {
  const email = await freshMember(playwright, baseURL!);
  await page.goto("/profilo");
  await signIn(page, email, PASSWORD, "Accedi");
  await expect(page.getByRole("heading", { name: "Prova lingua", level: 1 })).toBeVisible();
  await call(page.request, "PUT", "/api/v1/me/language", { locale: "en" });
  await page.goto("/non-esiste-davvero");
  await expect(page.getByRole("heading", { name: "Page not found" })).toBeVisible();
  await expect(page.getByText("doesn't exist, or it has moved")).toBeVisible();
  await expect(page).toHaveTitle("Page not found · Regia");
});

test("l'amministratore vede anche la lingua dell'organizzazione", async ({ page }) => {
  await page.request.post("/api/v1/auth/login", { data: { email: "admin@demo.test", password: PASSWORD } });
  await page.goto("/profilo");
  await expect(page.getByRole("heading", { name: "Lingua dell'organizzazione" })).toBeVisible();
  await expect(page.getByLabel("Lingua predefinita")).toBeVisible();
});

test("login in inglese anche scegliendo la lingua prima dell'accesso", async ({ page }) => {
  await page.goto("/");
  await scegli(page.getByLabel("Lingua", { exact: true }), { value: "en" });
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  await expect(page.locator("html")).toHaveAttribute("lang", "en");
  await page.reload();
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
});
