// Tipi del lavoro dai dati: un tipo creato dall'editor (modulo work) è proposto da «Nuovo elemento»
// della board e la card compare; il campo Punti compare solo se il tipo lo ha tra i campi associati.
import { test, expect } from "@playwright/test";
import { apiAs, call, login, uniq } from "./fixtures";

test("tipi del lavoro: un tipo nuovo si crea dalla board, senza punti se il tipo non li ha", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "admin");
  const team = uniq("Team tipi");
  await call(req, "POST", "/api/v1/teams", { name: team });
  const spaces = await call(req, "GET", "/api/v1/work/spaces");
  const space = spaces.items.find((s: { team: string }) => s.team === team).key as string;
  const suffix = Date.now().toString(36);
  const nome = `Idea ${suffix}`;
  // Da «task»: il tipo non ha il campo Punti, che invece ha la story.
  await call(req, "POST", "/api/v1/types", { key: `idea_${suffix}`, name: nome, from: "task", module: "work" });
  await req.dispose();

  await login(page, "admin");
  await page.goto(`/delivery/board?spazio=${space}&sprint=tutti`);
  await expect(page.getByRole("heading", { name: new RegExp(`Board · ${team}`) })).toBeVisible();
  await page.getByRole("button", { name: "Nuovo elemento" }).click();
  const dialog = page.getByRole("dialog", { name: "Nuovo elemento" });
  const tipi = dialog.getByRole("radiogroup", { name: "Tipo" });
  // Epic non è sulla board di default; il tipo nuovo sì.
  await expect(tipi.getByRole("radio", { name: "Epic" })).toHaveCount(0);
  await expect(tipi.getByRole("radio", { name: "Story" })).toBeVisible();
  // Story ha i Punti; il tipo nuovo no.
  await tipi.getByRole("radio", { name: "Story" }).click();
  await expect(dialog.getByLabel("Punti")).toBeVisible();
  await tipi.getByRole("radio", { name: nome }).click();
  await expect(dialog.getByLabel("Punti")).toHaveCount(0);
  const titolo = uniq("Spunto");
  await dialog.getByLabel("Titolo").fill(titolo);
  await dialog.getByRole("button", { name: "Crea", exact: true }).click();
  await expect(page.getByRole("article", { name: new RegExp(titolo) })).toBeVisible();
});
