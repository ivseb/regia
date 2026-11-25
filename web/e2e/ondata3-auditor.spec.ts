// Ondata 3, C6: spazio dell'auditor. Il revisore ha una home dedicata con l'incarico (perimetro e
// periodo), estrae un campione ripetibile, chiede un'evidenza al responsabile, apre un rilievo che il
// responsabile affronta con un piano e che lui chiude; scrive solo queste cose. Telefono a 375 px.
import { test, expect, type APIRequestContext } from "@playwright/test";
import { apiAs, call, expectNoHorizontalScroll, login, scegli, uniq } from "./fixtures";

type Ctl = { key: string };

/** Controllo con responsabile Anna Moretti e sei evidenze valide (create da lei, validate dalla compliance). */
async function controlWithEvidence(playwright: Parameters<typeof apiAs>[0], baseURL: string, title: string) {
  const co = await apiAs(playwright, baseURL, "conformita");
  const owner = await apiAs(playwright, baseURL, "dev");
  const c = await call<Ctl>(co.req, "POST", "/api/v1/grc/controls", { title, text: "Controllo creato dai test dell'auditor.", owner_id: owner.me.active_tenant!.member_id });
  for (let i = 1; i <= 6; i++) {
    const ev = await call<{ key: string }>(owner.req, "POST", "/api/v1/grc/evidence", {
      controls: [c.key], kind: "link", title: `Verbale ${i} di ${title}`, url: `https://ci.example.test/verbali/${i}`,
    });
    await call(co.req, "POST", `/api/v1/grc/evidence/${ev.key}/validate`, {});
  }
  return { key: c.key, co: co.req, owner: owner.req };
}

async function engagementFor(req: APIRequestContext, key: string, title: string) {
  const day = (m: number) => { const d = new Date(); d.setMonth(d.getMonth() + m); return d.toISOString().slice(0, 10); };
  return call<{ id: string }>(req, "POST", "/api/v1/grc/audit/engagements", { title, period_from: day(-6), period_to: day(1), note: "", controls: [key], apps: [] });
}

test.describe.configure({ mode: "serial" });

test("home dell'auditor: la pagina iniziale del revisore e un nuovo incarico dal modulo", async ({ page }) => {
  await login(page, "revisore");
  await page.goto("/");
  await expect(page).toHaveURL(/\/audit-spazio/);
  await expect(page.getByRole("heading", { name: "Spazio dell'auditor", level: 1 })).toBeVisible();
  await expect(page.getByRole("link", { name: "Spazio dell'auditor" }).first()).toBeVisible();
  const title = uniq("Revisione di prova");
  await page.getByRole("button", { name: "Nuovo incarico" }).first().click();
  const form = page.getByRole("form", { name: "Nuovo incarico di audit" });
  await form.getByLabel("Nome dell'incarico").fill(title);
  await form.getByRole("button", { name: "Apri l'incarico" }).click();
  await expect(page.getByTestId("engagement-title")).toHaveText(title);
  await expect(page.getByTestId("engagement-scope")).toContainText("tutte le norme");
  await expect(page.getByTestId("stat-progress")).toContainText("0 /");
});

test("campione ripetibile, richiesta di evidenza, rilievo: l'auditor chiede, il responsabile risponde, l'auditor chiude", async ({ page, playwright, baseURL }) => {
  const title = uniq("Prove di ripristino");
  const { key, owner } = await controlWithEvidence(playwright, baseURL!, title);
  const aud = await apiAs(playwright, baseURL!, "revisore");
  const eng = await engagementFor(aud.req, key, uniq("Incarico"));

  await login(page, "revisore");
  await page.goto(`/audit-spazio?incarico=${eng.id}`);
  await expect(page.getByTestId("stat-progress")).toContainText("0 / 1");
  const card = page.getByTestId("audit-control").filter({ hasText: key });
  await expect(card).toContainText("da esaminare");
  await card.getByRole("button", { name: "Lavora su questo controllo" }).click();
  const work = card.getByTestId("control-work");

  // Campione di 3 su 6 con il seme 7: ripetuto con lo stesso seme dà le stesse evidenze.
  await work.getByLabel("Quante evidenze").fill("3");
  await work.getByLabel("Seme (facoltativo)").fill("7");
  await work.getByRole("button", { name: "Estrai il campione" }).click();
  const sample = work.getByTestId("audit-sample").first();
  await expect(sample).toContainText("Campione di 3 su 6");
  await expect(sample).toContainText("Seme 7");

  // Esito del primo elemento: non conforme, con la nota.
  await sample.locator("summary").click();
  const first = sample.getByTestId("sample-item").first();
  await scegli(first.getByRole("combobox", { name: "Esito" }), "non conforme");
  await first.getByPlaceholder(/^Nota sull'esito/).fill("Verbale senza firma");
  await first.getByRole("button", { name: "Salva l'esito" }).click();
  await expect(first).toContainText("non conforme");

  // Richiesta di evidenza al responsabile, con scadenza.
  const reqTitle = uniq("Verbale firmato");
  await work.getByLabel("Titolo della richiesta").fill(reqTitle);
  await work.getByLabel("Cosa serve").fill("Serve la versione firmata del verbale.");
  await work.getByLabel("Scadenza").fill(new Date(Date.now() + 7 * 864e5).toISOString().slice(0, 10));
  await work.getByRole("button", { name: "Invia la richiesta" }).click();
  await expect(work.getByTestId("audit-request").filter({ hasText: reqTitle })).toBeVisible();

  // Rilievo.
  const findTitle = uniq("Verbali non firmati");
  await work.getByLabel("Titolo del rilievo").fill(findTitle);
  await work.getByLabel("Descrizione").fill("Un verbale su tre è senza firma.");
  await work.getByRole("button", { name: "Apri il rilievo" }).click();
  await expect(work.getByTestId("audit-finding").filter({ hasText: findTitle })).toBeVisible();

  // Esame del controllo: l'avanzamento sale.
  await work.getByRole("button", { name: "Segna esaminato" }).click();
  await expect(page.getByTestId("stat-progress")).toContainText("1 / 1");
  await expect(page.getByTestId("stat-requests")).toContainText("1");
  await expect(page.getByTestId("stat-findings")).toContainText("1");

  // Il responsabile ha la notifica e risponde dalla scheda del controllo.
  const notes = await call<{ items: unknown[] }>(owner, "GET", "/api/v1/notifications");
  expect(JSON.stringify(notes.items)).toContain(reqTitle);
  await login(page, "dev");
  await page.goto(`/e/${key}`);
  const box = page.getByRole("region", { name: "Audit su questo controllo" });
  await expect(box).toBeVisible();
  const request = box.getByTestId("audit-request").filter({ hasText: reqTitle });
  await request.getByRole("button", { name: "Rispondi" }).click();
  await request.getByLabel("Risposta", { exact: true }).fill("Allego il verbale firmato.");
  await request.getByLabel("Link alla documentazione (facoltativo)").fill("https://wiki.example.test/verbale-firmato");
  await request.getByRole("button", { name: "Invia la risposta" }).click();
  await expect(request.getByTestId("audit-response")).toContainText("Allego il verbale firmato.");
  // Commento in linea e piano d'azione sul rilievo.
  await box.getByLabel("Commento", { exact: true }).fill("Il modello ora prevede la firma.");
  await box.getByRole("button", { name: "Aggiungi il commento" }).click();
  await expect(box.getByTestId("audit-comment")).toContainText("Il modello ora prevede la firma.");
  const finding = box.getByTestId("audit-finding").filter({ hasText: findTitle });
  await finding.getByRole("button", { name: "Proponi il piano d'azione" }).click();
  await finding.getByLabel("Piano d'azione", { exact: true }).fill("Firma obbligatoria nel modello di verbale.");
  await finding.getByRole("button", { name: "Salva il piano" }).click();
  await expect(finding.getByTestId("finding-plan")).toContainText("Firma obbligatoria");
  await expect(finding).toContainText("piano proposto");
  // Il responsabile non chiude il rilievo e non vede i comandi dell'auditor.
  await expect(finding.getByRole("button", { name: "Chiudi il rilievo" })).toHaveCount(0);

  // L'auditor chiude il rilievo e la richiesta; lo stato si vede nella scheda del controllo.
  await login(page, "revisore");
  await page.goto(`/audit-spazio?incarico=${eng.id}`);
  const f2 = page.getByRole("region", { name: "Rilievi" }).getByTestId("audit-finding").filter({ hasText: findTitle });
  await expect(f2).toContainText("Firma obbligatoria");
  await f2.getByRole("button", { name: "Chiudi il rilievo" }).click();
  await f2.getByLabel("Perché chiudi il rilievo").fill("Piano attuato e verificato.");
  await f2.getByRole("button", { name: "Chiudi il rilievo" }).last().click();
  await expect(f2).toContainText("chiuso");
  const r2 = page.getByRole("region", { name: "Richieste di evidenza" }).getByTestId("audit-request").filter({ hasText: reqTitle });
  await expect(r2).toContainText("risposta ricevuta");
  await r2.getByRole("button", { name: "Chiudi la richiesta" }).click();
  await expect(r2).toContainText("chiusa");
  await page.goto(`/e/${key}`);
  await expect(page.getByRole("region", { name: "Audit su questo controllo" }).getByTestId("audit-finding").filter({ hasText: findTitle })).toContainText("chiuso");

  // L'auditor non modifica i controlli.
  const r = await aud.req.fetch(`/api/v1/grc/controls/${key}`, { method: "PATCH", data: { frequency: "monthly" } });
  expect(r.status()).toBe(403);

  // Ripetibilità: un secondo campione con lo stesso seme dà le stesse evidenze (alla fine, perché
  // il campione nuovo diventerebbe il primo dell'elenco e cambierebbe la riga su cui lavora il test).
  const ctl = await call<{ samples: { seed: number; items: { evidence: { key: string } }[] }[] }>(aud.req, "GET", `/api/v1/grc/controls/${key}/audit`);
  const again = await call<{ items: { evidence: { key: string } }[] }>(aud.req, "POST", `/api/v1/grc/audit/engagements/${eng.id}/samples`, { control: key, size: 3, seed: 7 });
  expect(again.items.map((i) => i.evidence.key)).toEqual(ctl.samples.find((s) => s.seed === 7)!.items.map((i) => i.evidence.key));
});

test("telefono a 375 px: home e lavoro sul controllo senza scorrimento orizzontale", async ({ page, playwright, baseURL }) => {
  const { key } = await controlWithEvidence(playwright, baseURL!, uniq("Controllo da telefono"));
  const aud = await apiAs(playwright, baseURL!, "revisore");
  const eng = await engagementFor(aud.req, key, uniq("Incarico telefono"));
  await page.setViewportSize({ width: 375, height: 812 });
  await login(page, "revisore");
  await page.goto(`/audit-spazio?incarico=${eng.id}`);
  await expect(page.getByTestId("stat-progress")).toBeVisible();
  await expectNoHorizontalScroll(page);
  const card = page.getByTestId("audit-control").filter({ hasText: key });
  const open = card.getByRole("button", { name: "Lavora su questo controllo" });
  expect((await open.boundingBox())!.height).toBeGreaterThanOrEqual(43.5);
  await open.click();
  const work = card.getByTestId("control-work");
  await work.getByRole("button", { name: "Estrai il campione" }).click();
  await expect(work.getByTestId("audit-sample").first()).toBeVisible();
  await work.getByTestId("audit-sample").first().locator("summary").click();
  await expectNoHorizontalScroll(page);
  await page.goto(`/e/${key}`);
  await expect(page.getByRole("region", { name: "Audit su questo controllo" })).toBeVisible();
  await expectNoHorizontalScroll(page);
});
