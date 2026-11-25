// Valutazione con questionario: il demand manager compila e invia, il team Compliance la
// trova nell'inbox, la prende in esame e registra l'esito.
import { test, expect } from "@playwright/test";
import { apiAs, call, login, newRequest, uniq } from "./fixtures";

test("valutazione: compila il questionario, poi il team decide", async ({ page, playwright, baseURL }) => {
  const { req } = await apiAs(playwright, baseURL!, "richiedente");
  const title = uniq("Esternalizzazione del servizio paghe");
  const { id } = await newRequest(req, title);
  await req.dispose();
  const { req: dm } = await apiAs(playwright, baseURL!, "dm");
  const opened = await call(dm, "POST", `/api/v1/elements/${id}/reviews`, { type: "parere_compliance" });
  const rk = opened.review.key as string;
  await dm.dispose();

  // Compilazione.
  await login(page, "dm");
  await page.goto(`/valutazioni/${rk}`);
  await expect(page.getByRole("heading", { name: "Parere compliance e DORA", level: 1 })).toBeVisible();
  const send = page.getByRole("button", { name: "Invia al team" });
  await expect(send).toBeDisabled();
  await expect(page.getByText("mancano risposte obbligatorie nel questionario")).toBeVisible();
  await page.getByRole("radiogroup", { name: /Coinvolge un fornitore ICT nuovo/ }).getByRole("radio", { name: "Sì" }).click();
  const supplier = page.getByRole("textbox", { name: /fornitore/i });
  if (await supplier.count()) await supplier.first().fill("Centro elaborazione paghe esterno");
  await expect(send).toBeEnabled();
  await send.click();
  await expect(page.getByText(/Fase: Inviata al team/).first()).toBeVisible();

  // Il team Compliance la trova tra le sue e decide.
  await page.context().clearCookies();
  await login(page, "compliance");
  await page.goto("/valutazioni");
  const link = page.getByRole("link", { name: new RegExp(rk) }).first();
  await expect(link).toBeVisible();
  await link.click();
  await expect(page).toHaveURL(new RegExp(`/valutazioni/${rk}`));
  await page.getByRole("button", { name: "Prendi in carico" }).click();
  await expect(page.getByText(/Fase: In esame/).first()).toBeVisible();

  // Le prescrizioni proposte dalle risposte vanno accettate o scartate prima dell'esito.
  // Il nuovo fornitore ICT propone l'iscrizione nel registro DORA.
  const record = page.getByRole("button", { name: "Registra l'esito" });
  await expect(page.getByText("Prima accetta o scarta le 1 prescrizioni proposte.")).toBeVisible();
  const proposal = page.getByRole("listitem").filter({ hasText: "registro delle informazioni (art. 28 DORA)" });
  await proposal.getByRole("button", { name: "Accetta" }).click();
  await expect(proposal.getByText(/accettata da Andrea Fontana/)).toBeVisible();
  // Con una prescrizione accettata l'esito "Favorevole" semplice non è ammesso.
  const outcomes = page.getByRole("radiogroup", { name: "Esito" });
  await outcomes.getByRole("radio", { name: "Favorevole", exact: true }).click();
  await expect(page.getByText("Ci sono prescrizioni accettate: scegli un esito con prescrizioni.")).toBeVisible();
  await expect(record).toBeDisabled();
  await outcomes.getByRole("radio", { name: "Favorevole con prescrizioni" }).click();
  await page.getByLabel("Motivazione").fill("Fornitore conforme: contratto con le clausole DORA.");
  await record.click();
  await expect(record).toHaveCount(0);
  await expect(page.getByText(/Andrea Fontana ·/)).toBeVisible();
  await expect(page.getByText("Favorevole con prescrizioni").first()).toBeVisible();
});
