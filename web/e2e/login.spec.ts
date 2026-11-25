// Accesso con password, cambio di organizzazione e secondo fattore TOTP.
import { test, expect } from "@playwright/test";
import { createHmac } from "node:crypto";
import { PASSWORD, apiAs, call, uniq, scegli } from "./fixtures";

/** Codice TOTP (RFC 6238: SHA-1, 6 cifre, 30 s) per la chiave base32 mostrata dal profilo. */
function totp(secretB32: string, step = Math.floor(Date.now() / 1000 / 30)) {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567";
  let bits = "";
  for (const c of secretB32.replace(/[\s=]/g, "").toUpperCase()) bits += alphabet.indexOf(c).toString(2).padStart(5, "0");
  const key = Buffer.from(bits.match(/.{8}/g)!.map((b) => parseInt(b, 2)));
  const msg = Buffer.alloc(8);
  msg.writeBigUInt64BE(BigInt(step));
  const h = createHmac("sha1", key).update(msg).digest();
  const off = h[h.length - 1] & 0x0f;
  return String((h.readUInt32BE(off) & 0x7fffffff) % 1_000_000).padStart(6, "0");
}

async function fillLogin(page: import("@playwright/test").Page, email: string, password: string) {
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: "Accedi", exact: true }).click();
}

test("accesso con password e cambio di organizzazione", async ({ page }) => {
  await page.goto("/richieste");
  await expect(page.getByRole("heading", { name: "Accedi" })).toBeVisible();

  // Password sbagliata: messaggio visibile, si resta sulla pagina di accesso.
  await fillLogin(page, "admin@demo.test", "sbagliata-123456");
  await expect(page.getByRole("alert").or(page.getByText(/credenziali|non valid/i)).first()).toBeVisible();

  await fillLogin(page, "admin@demo.test", PASSWORD);
  const org = page.getByRole("combobox", { name: "Organizzazione" });
  await expect(org).toBeVisible();
  await scegli(org, "Demo S.p.A.");
  await expect(page.getByRole("heading", { name: "Richieste", level: 1 })).toBeVisible();
  await expect(page.getByRole("link", { name: "DEM-1", exact: true })).toBeVisible();

  // Nell'altra organizzazione non ci sono le richieste del demo.
  await scegli(org, "Altro Cliente");
  await expect(org).toContainText("Altro Cliente");
  await expect(page.getByRole("link", { name: "DEM-1", exact: true })).toHaveCount(0);
  const me = await call(page.request, "GET", "/api/v1/me");
  expect(me.tenants.find((t: { tenant_id: string }) => t.tenant_id === me.active_tenant.tenant_id).slug).toBe("altro");

  // Si torna al demo e i dati ricompaiono.
  await scegli(org, "Demo S.p.A.");
  await expect(page.getByRole("link", { name: "DEM-1", exact: true })).toBeVisible();

  // Uscita: si torna alla pagina di accesso.
  await page.getByRole("button", { name: "Esci" }).click();
  await expect(page.getByRole("heading", { name: "Accedi" })).toBeVisible();
});

test("secondo fattore: attivazione dal profilo e accesso con il codice", async ({ page, playwright, baseURL }) => {
  // Persona nuova (invito accettato via API): l'MFA attivata non tocca gli altri test.
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const email = `${uniq("mfa").replace(" ", "-")}@demo.test`;
  const added = await call(req, "POST", "/api/v1/admin/members", { email, display_name: "Prova MFA", member_type: "standard", mode: "invite", role_ids: [], group_ids: [] });
  const token = added.link.path.split("/").pop();
  await call(req, "POST", `/api/v1/auth/invitations/${token}/accept`, { display_name: "Prova MFA", password: PASSWORD });
  await req.dispose();

  await page.goto("/profilo");
  await fillLogin(page, email, PASSWORD);
  const mfa = page.getByRole("heading", { name: "Autenticazione a due fattori" });
  await expect(mfa).toBeVisible();
  await page.getByRole("button", { name: "Attiva", exact: true }).click();
  await expect(page.getByRole("img", { name: /Codice QR/ })).toBeVisible();
  const secret = (await page.locator("code.select-all").innerText()).replace(/\s/g, "");
  expect(secret).toMatch(/^[A-Z2-7]{16,}$/);

  // Codice sbagliato: errore e nessuna attivazione.
  await page.getByLabel("Codice a 6 cifre").fill("000000" === totp(secret) ? "111111" : "000000");
  await page.getByRole("button", { name: "Conferma e attiva" }).click();
  await expect(page.getByText(/codice/i).filter({ hasNot: page.getByRole("textbox") }).last()).toBeVisible();

  const step = Math.floor(Date.now() / 1000 / 30);
  await page.getByLabel("Codice a 6 cifre").fill(totp(secret, step));
  await page.getByRole("button", { name: "Conferma e attiva" }).click();
  await expect(page.getByText("Codici di recupero: conservali in un posto sicuro.")).toBeVisible();
  await expect(page.getByText("attiva", { exact: true })).toBeVisible();

  // Nuovo accesso: dopo la password serve il codice (quello dell'intervallo successivo,
  // perché un codice già usato non vale di nuovo).
  await page.context().clearCookies();
  await page.goto("/richieste");
  await fillLogin(page, email, PASSWORD);
  await expect(page.getByRole("heading", { name: "Verifica in due passaggi" })).toBeVisible();
  await page.getByLabel("Codice a 6 cifre").fill("123456" === totp(secret, step + 1) ? "654321" : "123456");
  await page.getByRole("button", { name: "Accedi", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Verifica in due passaggi" })).toBeVisible();
  await page.getByLabel("Codice a 6 cifre").fill(totp(secret, step + 1));
  await page.getByRole("button", { name: "Accedi", exact: true }).click();
  await expect(page.getByRole("heading", { name: "Richieste", level: 1 })).toBeVisible();
  const me = await call(page.request, "GET", "/api/v1/me/account");
  expect(JSON.stringify(me)).toContain(email);
});
