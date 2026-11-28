// Identità aziendale: mappatura di un gruppo dell'IdP, token SCIM mostrato una volta,
// utente creato da SCIM senza ruoli, gruppo governato dall'IdP, revoca del token.
import { expect, test } from "@playwright/test";
import { call, login, scegli } from "./fixtures";

test("identità aziendale: mappatura, SCIM e gruppo governato", async ({ page }) => {
  await login(page, "admin");
  const tag = Date.now().toString(36);
  const groupName = `Editori IdP ${tag}`;
  const group = await call<{ id: string }>(page.request, "POST", "/api/v1/admin/groups", { name: groupName, parent_id: null });

  await page.goto("/configurazione/persone?tab=identita");
  await expect(page.getByRole("tab", { name: "Identità aziendale" })).toHaveAttribute("aria-selected", "true");

  // Gruppo dell'IdP scritto a mano e mappato su quello di Regia.
  await page.getByLabel("Identificativo nell'IdP (nome o object ID)").fill(`grp-${tag}`);
  await page.getByLabel("Nome da mostrare").fill(`Editori ${tag}`);
  await page.getByRole("button", { name: "Aggiungi gruppo" }).click();
  const row = page.getByRole("listitem").filter({ hasText: `Editori ${tag}` }).first();
  await expect(row.getByText("Non mappato: non dà nulla.")).toBeVisible();
  await scegli(row.getByLabel(`Mappa Editori ${tag} su un gruppo di Regia`), groupName);
  await expect(row.getByText(groupName)).toBeVisible();

  // Token SCIM: si vede una volta sola.
  await page.getByLabel("Nome del token (per esempio Entra ID)").fill(`e2e ${tag}`);
  await page.getByRole("button", { name: "Crea token" }).click();
  const box = page.getByRole("status").filter({ hasText: "Token SCIM: copialo ora" });
  const token = (await box.locator("code").innerText()).trim();
  expect(token).toMatch(/^scim_/);
  await expect(page.getByText("Fonte dei gruppi: SCIM")).toBeVisible();

  // L'IdP crea una persona e la mette nel gruppo: entra senza ruoli diretti.
  const auth = { Authorization: `Bearer ${token}`, "Content-Type": "application/scim+json" };
  const email = `scim-${tag}@demo.test`;
  const u = await page.request.post("/scim/v2/Users", { headers: auth, data: { userName: email, displayName: `Persona SCIM ${tag}`, active: true } });
  expect(u.status()).toBe(201);
  const user = await u.json();
  const g = await page.request.post("/scim/v2/Groups", { headers: auth, data: { displayName: `Editori ${tag}`, externalId: `grp-${tag}`, members: [{ value: user.id }] } });
  expect(g.status()).toBe(201);

  await page.goto(`/configurazione/persone?tab=membri&sel=${user.id}`);
  const member = page.getByRole("row").filter({ hasText: `Persona SCIM ${tag}` });
  await expect(member.getByText("da SCIM")).toBeVisible();

  // Il gruppo di Regia è governato dall'IdP: niente aggiunte a mano.
  await page.goto(`/configurazione/persone?tab=gruppi&sel=${group.id}`);
  await expect(page.getByText("Gruppo governato dall'IdP: l'appartenenza si cambia nell'IdP, non qui.")).toBeVisible();
  await expect(page.getByText(`Persona SCIM ${tag}`)).toBeVisible();

  // Revoca dall'interfaccia: il token non vale più.
  await page.goto("/configurazione/persone?tab=identita");
  const tok = page.getByRole("listitem").filter({ hasText: `e2e ${tag}` });
  await tok.getByRole("button", { name: "Revoca" }).click();
  await tok.getByRole("button", { name: "Conferma la revoca" }).click();
  await expect(tok).toHaveCount(0);
  const after = await page.request.get("/scim/v2/Users", { headers: auth });
  expect(after.status()).toBe(401);
});
