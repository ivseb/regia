// Requisiti veri dai questionari: una regola «requirement» propone un requisito durante la
// compilazione e, alla chiusura della valutazione, lo fa nascere nel modulo Requisiti con
// origine «Questionario».
import { test, expect } from "@playwright/test";
import { apiAs, call, expectNoHorizontalScroll, login, newRequest, uniq } from "./fixtures";

test("regola «requisito»: proposto nella valutazione, vero alla chiusura", async ({ page, playwright, baseURL }) => {
  // Un modello e un tipo di valutazione propri (non toccano quelli di partenza).
  const { req: admin } = await apiAs(playwright, baseURL!, "admin");
  const sfx = Date.now().toString(36);
  const tplKey = `qreq_${sfx}`;
  const typeKey = `rreq_${sfx}`;
  const typeName = uniq("Verifica requisiti");
  const reqTitle = uniq("Autenticazione a più fattori");
  const definition = {
    name: `Modello ${sfx}`,
    sections: [{ key: "generale", title: "Generale", questions: [{ key: "mfa", label: "MFA attiva per gli accessi?", type: "yes_no", required: true }] }],
    derivations: [{ key: "r_mfa", when: "mfa: no", kind: "requirement", title: reqTitle, text: "Attivare la MFA per tutti gli accessi amministrativi.", category: "Identità" }],
  };
  await call(admin, "PUT", `/api/v1/review-config/templates/${tplKey}/draft`, { definition, notes: "e2e" });
  await call(admin, "POST", `/api/v1/review-config/templates/${tplKey}/publish`, { notes: "v1" });
  const type = await call(admin, "POST", "/api/v1/review-config/types", { key: typeKey, name: typeName, team: "compliance", scope: "request", template_key: tplKey, mandatory: false });
  await admin.dispose();

  const { req } = await apiAs(playwright, baseURL!, "richiedente");
  const { id } = await newRequest(req, uniq("Accesso amministrativo ai sistemi"));
  await req.dispose();
  const { req: dm } = await apiAs(playwright, baseURL!, "dm");
  const opened = await call(dm, "POST", `/api/v1/elements/${id}/reviews`, { type: typeKey });
  const rk = opened.review.key as string;
  await dm.dispose();

  // Compilazione: la risposta fa comparire il requisito proposto, separato dalle prescrizioni.
  await login(page, "dm");
  await page.setViewportSize({ width: 375, height: 800 });
  await page.goto(`/valutazioni/${rk}`);
  await page.getByRole("radiogroup", { name: /MFA attiva/ }).getByRole("radio", { name: "No" }).click();
  const proposals = page.getByTestId("requirement-proposals");
  await expect(proposals).toBeVisible();
  await expect(proposals.getByText("Requisiti proposti dalle risposte")).toBeVisible();
  await expect(proposals.getByText(reqTitle)).toBeVisible();
  await expect(proposals.getByText("Attivare la MFA per tutti gli accessi amministrativi.")).toBeVisible();
  await expect(proposals.getByText("Identità")).toBeVisible();
  await expect(page.getByText("Prescrizioni proposte dalle risposte")).toBeVisible();
  await expectNoHorizontalScroll(page);
  const send = page.getByRole("button", { name: "Invia al team" });
  await expect(send).toBeEnabled();
  await send.click();
  await expect(page.getByText(/Fase: Inviata al team/).first()).toBeVisible();

  // Il team prende in carico e chiude con esito favorevole.
  await page.context().clearCookies();
  await login(page, "compliance");
  await page.goto(`/valutazioni/${rk}`);
  await page.getByRole("button", { name: "Prendi in carico" }).click();
  await expect(page.getByText(/Fase: In esame/).first()).toBeVisible();
  const outcomes = page.getByRole("radiogroup", { name: "Esito" });
  await outcomes.getByRole("radio", { name: "Favorevole", exact: true }).click();
  await page.getByLabel("Motivazione").fill("Va bene con il requisito di MFA.");
  await page.getByRole("button", { name: "Registra l'esito" }).click();
  await expect(page.getByRole("button", { name: "Registra l'esito" })).toHaveCount(0);
  // Dopo la chiusura la valutazione cita il requisito creato.
  const created = page.getByTestId("requirement-proposals").getByRole("link", { name: /^[A-Z]+-\d+$/ });
  await expect(created).toBeVisible();
  const reqKey = (await created.textContent())!.trim();
  await expectNoHorizontalScroll(page);

  // Il requisito è vero: di progetto, con origine «Questionario», verificato dal team.
  const { req: reader } = await apiAs(playwright, baseURL!, "compliance");
  const info = (await call(reader, "GET", `/api/v1/requirements/${reqKey}`)).requirement;
  await reader.dispose();
  expect(info.origin).toBe("questionnaire");
  expect(info.scope).toBe("project");
  expect(info.review_key).toBe(rk);
  expect(info.title).toBe(reqTitle);
  await page.goto(`/e/${reqKey}`);
  await page.getByRole("tab", { name: "Verifica" }).click();
  await expect(page.getByText("Regola del questionario della valutazione")).toBeVisible();
  await expect(page.getByRole("link", { name: rk }).first()).toBeVisible();
  await expectNoHorizontalScroll(page);

  // Il tipo di prova esce dall'elenco.
  const { req: admin2 } = await apiAs(playwright, baseURL!, "admin");
  await call(admin2, "PATCH", `/api/v1/review-config/types/${type.id}`, { archived: true });
  await admin2.dispose();
});
