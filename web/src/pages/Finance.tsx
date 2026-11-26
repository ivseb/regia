import { EntitySelect, Select } from "../select";
import { useEffect, useMemo, useState, type FormEvent, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  ApiError,
  financeApi,
  type Budget,
  type BudgetKind,
  type CostFilter,
  type FinOverview,
  type ImportMapping,
  type ImportRun,
  type LineInput,
  type Rate,
} from "../api";
import { fmtDate, fmtNumber } from "../i18n/format";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import { PageHeader } from "../shell";
import { ErrorNote, SectionTitle, date } from "../ui";
import { BudgetSettingsForm } from "./budget/BudgetSettingsForm";
import { BudgetSource, type SourceLine } from "./budget/BudgetSource";
import { METHOD_KEYS, SOURCE_KEYS, SCurve, centsToInput, kindLabel, methodLabel, money, parseMoney, pct, ratio, sourceLabel, statusLabel } from "./finance-ui";
import { crumbs } from "../crumbs";
import { FinanceTabs } from "./portafoglio-ui";

// Pagina Finanza (demand.md §9-10, schermate D08 e D09): esercizi, budget e trasferimenti;
// voci di costo con filtri; import CSV in due tempi; tariffe e ore; earned value; struttura.
// Solo per chi ha finance.read; le modifiche chiedono finance.write (il server verifica).

export type FinTab = "budget" | "costi" | "import" | "tariffe" | "ev" | "struttura";
const TABS: FinTab[] = ["budget", "costi", "import", "tariffe", "ev", "struttura"];

export function Finance({ tab, richiesta }: { tab: FinTab; richiesta?: string }) {
  const t = useT();
  const ov = useQuery({ queryKey: ["finance", "overview"], queryFn: financeApi.overview, retry: false });
  const forbidden = ov.error instanceof ApiError && ov.error.status === 403;
  return (
    <>
      <PageHeader path={crumbs("demand", "finance")} title={t("budget.fin.budgetECosti")} />
      <FinanceTabs />
      <div role="tablist" aria-label={t("budget.fin.sezione")} className="flex gap-5 overflow-x-auto border-b border-hair px-4 pt-1 md:px-7">
        {TABS.map((k) => (
          <Link
            key={k}
            to="/finanza"
            search={{ tab: k === "budget" ? undefined : k, richiesta }}
            role="tab"
            aria-selected={tab === k}
            className={`shrink-0 pb-2.5 no-underline ${tab === k ? "border-b-2 border-fg font-semibold text-fg" : "text-muted hover:text-fg"}`}
          >
            {t(`budget.fin.tab.${k}`)}
          </Link>
        ))}
      </div>
      <div className="flex flex-col gap-6 px-4 py-5 md:px-7">
        {forbidden ? (
          <p className="text-muted">{t("budget.fin.forbidden")}</p>
        ) : (
          <ErrorNote error={ov.error} />
        )}
        {ov.data && tab === "budget" && <BudgetTab ov={ov.data} richiesta={richiesta} />}
        {ov.data && tab === "costi" && <CostsTab ov={ov.data} richiesta={richiesta} />}
        {ov.data && tab === "import" && <ImportTab ov={ov.data} />}
        {ov.data && tab === "tariffe" && <RatesTab ov={ov.data} richiesta={richiesta} />}
        {ov.data && tab === "ev" && <EVTab ov={ov.data} richiesta={richiesta} />}
        {ov.data && tab === "struttura" && <StructureTab ov={ov.data} />}
      </div>
    </>
  );
}

// ---------------------------------------------------------------------
// Pezzi comuni
// ---------------------------------------------------------------------

function Field({ label, children, wide }: { label: string; children: ReactNode; wide?: boolean }) {
  return (
    <label className={`flex min-w-0 flex-col gap-1 ${wide ? "sm:col-span-2" : ""}`}>
      <span className="label text-muted">{label}</span>
      {children}
    </label>
  );
}

function Th({ children, right, hide }: { children?: ReactNode; right?: boolean; hide?: string }) {
  return (
    <th scope="col" className={`border-b-2 border-rule px-2 py-1.5 first:pl-0 ${right ? "text-right" : "text-left"} ${hide ?? ""}`}>
      {children}
    </th>
  );
}

function Td({ children, right, mono, hide, className }: { children?: ReactNode; right?: boolean; mono?: boolean; hide?: string; className?: string }) {
  return (
    <td className={`px-2 py-1.5 align-top first:pl-0 ${right ? "text-right" : ""} ${mono ? "font-mono" : ""} ${hide ?? ""} ${className ?? ""}`}>{children}</td>
  );
}

function Table({ head, children, empty }: { head: ReactNode; children: ReactNode; empty?: string }) {
  return (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse text-[13px] tabular">
        <thead>
          <tr className="label">{head}</tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
      {empty && <p className="m-0 py-2 text-muted">{empty}</p>}
    </div>
  );
}

function Status({ s }: { s: string }) {
  const strong = s === "approved" || s === "done";
  const weak = s === "superseded" || s === "rolled_back" || s === "rejected";
  return <span className={`whitespace-nowrap ${strong ? "font-semibold" : weak ? "text-muted" : s === "submitted" || s === "pending" || s === "preview" ? "text-accent" : ""}`}>{statusLabel(s)}</span>;
}

function useInvalidate() {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: ["finance"] });
}

const today = () => new Date().toISOString().slice(0, 10);

// i18n-ok: chiave di ruolo di esempio, non un testo dell'interfaccia
const ROLE_EXAMPLE = "backend";

// ---------------------------------------------------------------------
// Budget: esercizi, versioni, trasferimenti
// ---------------------------------------------------------------------

function BudgetTab({ ov, richiesta }: { ov: FinOverview; richiesta?: string }) {
  const t = useT();
  const cur = ov.settings.base_currency;
  const current = ov.fiscal_years.find((f) => f.start_date <= today() && f.end_date >= today()) ?? ov.fiscal_years[0];
  const [fyID, setFY] = useState(current?.id ?? "");
  useEffect(() => {
    if (!fyID && current) setFY(current.id);
  }, [current, fyID]);
  const fy = ov.fiscal_years.find((f) => f.id === fyID);
  const [selected, setSelected] = useState<string | null>(null);
  const budgets = useQuery({
    queryKey: ["finance", "budgets", fyID, richiesta ?? ""],
    queryFn: () => financeApi.budgets({ fiscal_year: fyID, element: richiesta }),
    enabled: !!fyID,
  });
  const invalidate = useInvalidate();
  const feeds = ov.settings.portfolio_fiscal_year_id === fyID;
  const link = useMutation({
    mutationFn: () => financeApi.saveSettings(feeds ? { clear_portfolio: true } : { portfolio_fiscal_year_id: fyID }),
    onSuccess: invalidate,
  });

  return (
    <>
      <section className="flex flex-col gap-3">
        <SectionTitle aside={<NewFiscalYear ov={ov} onDone={(id) => setFY(id)} />}>{t("budget.fin.esercizio")}</SectionTitle>
        {ov.fiscal_years.length === 0 ? (
          <p className="m-0 text-muted">{t("budget.fin.nessunEsercizioCreaneUno")}</p>
        ) : (
          <div className="flex flex-wrap items-end gap-x-8 gap-y-3">
            <Field label={t("budget.fin.esercizio")}>
              <Select className="field w-auto" value={fyID} onChange={(e) => { setFY(e.target.value); setSelected(null); }}>
                {ov.fiscal_years.map((f) => (
                  <option key={f.id} value={f.id}>
                    {f.name} ({date(f.start_date)} – {date(f.end_date)})
                  </option>
                ))}
              </Select>
            </Field>
            {fy && (
              <dl className="m-0 flex flex-wrap gap-x-8 gap-y-2">
                {(
                  [
                    ["envelope", t("budget.fin.fy.envelope"), money(fy.envelope_cents, cur)],
                    ["allocated", t("budget.fin.fy.allocated"), money(fy.allocated_cents, cur)],
                    ["requested", t("budget.fin.fy.requested"), money(fy.requested_cents, cur)],
                    ["residual", t("budget.fin.fy.residual"), money(fy.residual_cents, cur)],
                  ] as const
                ).map(([id, label, v]) => (
                  <div key={id} className="flex flex-col">
                    <dt className="label text-muted">{label}</dt>
                    <dd className={`m-0 font-mono text-[17px] font-semibold tabular ${id === "residual" && (fy.residual_cents ?? 0) < 0 ? "text-danger" : ""}`}>{v}</dd>
                  </div>
                ))}
              </dl>
            )}
            {fy && ov.can_write && (
              <div className="flex flex-col gap-1">
                <EnvelopeEditor fyID={fy.id} value={fy.envelope_cents} currency={cur} />
              </div>
            )}
          </div>
        )}
        {fy && (
          <p className="m-0 flex flex-wrap items-center gap-2 text-[12.5px] text-muted">
            {feeds
              ? t("budget.fin.feeds", { basis: ov.settings.portfolio_basis === "residual" ? t("budget.fin.basis.residual") : t("budget.fin.basis.envelope") })
              : t("budget.fin.manualBudget")}
            {ov.can_write && (
              <button className="text-accent underline" onClick={() => link.mutate()} disabled={link.isPending}>
                {feeds ? t("budget.fin.unlink") : t("budget.fin.useForRanking")}
              </button>
            )}
            <Link to="/graduatoria" className="text-accent underline">{t("budget.fin.graduatoria")}</Link>
          </p>
        )}
        <ErrorNote error={link.error} />
      </section>

      {fy && (
        <section className="flex flex-col gap-3">
          <SectionTitle aside={richiesta ? <Link to="/finanza" search={{}} className="text-[12px] text-accent underline">{t("budget.fin.tutteLeRichieste")}</Link> : undefined}>
            {t("budget.fin.budgetOf", { name: fy.name })}
            {richiesta ? ` · ${richiesta}` : ""}
          </SectionTitle>
          <ErrorNote error={budgets.error} />
          <Table
            head={
              <>
                <Th>{t("budget.fin.elemento")}</Th>
                <Th>{t("budget.fin.versione")}</Th>
                <Th>{t("budget.fin.stato")}</Th>
                <Th right>{t("budget.fin.totale")}</Th>
                <Th right hide="hidden md:table-cell">{t("budget.fin.conTrasferimenti")}</Th>
                <Th hide="hidden xl:table-cell">{t("budget.fin.approvatoDa")}</Th>
              </>
            }
            empty={budgets.data && budgets.data.items.length === 0 ? t("budget.fin.noBudgets") : undefined}
          >
            {budgets.data?.items.map((b) => (
              <tr
                key={b.id}
                className={`cursor-pointer border-b border-hair hover:bg-sel ${selected === b.id ? "bg-sel" : ""}`}
                onClick={() => setSelected(selected === b.id ? null : b.id)}
              >
                <Td>
                  <span className="flex min-w-0 flex-col">
                    <button className="self-start text-left font-mono text-[12px] text-accent" aria-expanded={selected === b.id}>{b.element_key}</button>
                    <span className="min-w-0 break-words">{b.element_title}</span>
                  </span>
                </Td>
                <Td mono>v{b.version} <span className="font-sans text-muted">{kindLabel(b.kind)}</span></Td>
                <Td><Status s={b.status} /></Td>
                <Td right mono>{money(b.total_cents, cur)}</Td>
                <Td right mono hide="hidden md:table-cell">{b.effective_cents !== b.total_cents ? money(b.effective_cents, cur) : "—"}</Td>
                <Td hide="hidden xl:table-cell">{b.approved_by ? `${b.approved_by} · ${date(b.approved_at)}` : "—"}</Td>
              </tr>
            ))}
          </Table>
          {selected && <BudgetDetail id={selected} ov={ov} onClose={() => setSelected(null)} onSelect={setSelected} />}
          {ov.can_write && <NewBudget ov={ov} fyID={fy.id} richiesta={richiesta} onCreated={setSelected} />}
        </section>
      )}
      {fy && <Transfers ov={ov} fyID={fy.id} budgets={budgets.data?.items ?? []} />}
    </>
  );
}

function EnvelopeEditor({ fyID, value, currency }: { fyID: string; value: number | null; currency: string }) {
  const t = useT();
  const [text, setText] = useState(centsToInput(value));
  useEffect(() => setText(centsToInput(value)), [value]);
  const invalidate = useInvalidate();
  const save = useMutation({
    mutationFn: () => {
      const c = parseMoney(text);
      return financeApi.updateFiscalYear(fyID, c == null ? { clear_envelope: true } : { envelope_cents: c });
    },
    onSuccess: invalidate,
  });
  return (
    <form className="flex items-end gap-2" onSubmit={(e) => { e.preventDefault(); save.mutate(); }}>
      <Field label={t("budget.fin.envelopeLabel", { currency })}>
        <input className="field w-32 text-right tabular" inputMode="decimal" value={text} onChange={(e) => setText(e.target.value)} />
      </Field>
      <button className="btn" disabled={save.isPending || text === centsToInput(value)}>{t("budget.fin.salva")}</button>
      <ErrorNote error={save.error} />
    </form>
  );
}

function NewFiscalYear({ ov, onDone }: { ov: FinOverview; onDone: (id: string) => void }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const y = new Date().getFullYear() + 1;
  const [name, setName] = useState(String(y));
  const [start, setStart] = useState(`${y}-01-01`);
  const [end, setEnd] = useState(`${y}-12-31`);
  const [env, setEnv] = useState("");
  const invalidate = useInvalidate();
  const save = useMutation({
    mutationFn: () => financeApi.createFiscalYear({ name, start_date: start, end_date: end, envelope_cents: parseMoney(env) }),
    onSuccess: (f) => {
      invalidate();
      setOpen(false);
      onDone(f.id);
    },
  });
  if (!ov.can_write) return null;
  if (!open) return <button className="text-[12px] text-accent underline" onClick={() => setOpen(true)}>{t("budget.fin.nuovoEsercizio")}</button>;
  return (
    <form
      className="fixed inset-x-4 top-24 z-50 grid gap-3 border border-rule bg-bg p-4 sm:static sm:inset-auto sm:z-auto sm:grid-cols-5 sm:items-end sm:border-0 sm:p-0"
      onSubmit={(e) => { e.preventDefault(); save.mutate(); }}
    >
      <Field label={t("budget.fin.nome")}><input className="field" required value={name} onChange={(e) => setName(e.target.value)} /></Field>
      <Field label={t("budget.fin.inizio")}><input className="field" type="date" required value={start} onChange={(e) => setStart(e.target.value)} /></Field>
      <Field label={t("budget.fin.fine")}><input className="field" type="date" required value={end} onChange={(e) => setEnd(e.target.value)} /></Field>
      <Field label={t("budget.fin.envelopeLabel", { currency: ov.settings.base_currency })}><input className="field text-right tabular" inputMode="decimal" value={env} onChange={(e) => setEnv(e.target.value)} /></Field>
      <div className="flex gap-2">
        <button className="btn btn-primary" disabled={save.isPending}>{t("budget.fin.crea")}</button>
        <button type="button" className="btn" onClick={() => setOpen(false)}>{t("budget.fin.annulla")}</button>
      </div>
      <div className="sm:col-span-5"><ErrorNote error={save.error} /></div>
    </form>
  );
}

type EditLine = { cost_category_id: string; funding_source_id: string; period: string; amount: string; note: string };
const toEdit = (l: { cost_category_id: string; funding_source_id: string | null; period: string | null; amount_cents: number; note: string | null }): EditLine => ({
  cost_category_id: l.cost_category_id,
  funding_source_id: l.funding_source_id ?? "",
  period: l.period ? l.period.slice(0, 7) : "",
  amount: centsToInput(l.amount_cents),
  note: l.note ?? "",
});
const fromEdit = (l: EditLine): LineInput => ({
  cost_category_id: l.cost_category_id,
  funding_source_id: l.funding_source_id || null,
  period: l.period || null,
  amount_cents: parseMoney(l.amount) ?? 0,
  note: l.note || null,
});

const sourceLines = (ls: EditLine[]): SourceLine[] => ls.map((l) => ({ period: l.period, cents: parseMoney(l.amount) ?? 0 }));

/** Righe del budget: categoria, mese (vuoto = intero esercizio), fonte, importo. */
function LinesEditor({ ov, lines, setLines }: { ov: FinOverview; lines: EditLine[]; setLines: (l: EditLine[]) => void }) {
  const t = useT();
  const cats = ov.categories.filter((c) => c.active);
  const set = (i: number, patch: Partial<EditLine>) => setLines(lines.map((l, j) => (j === i ? { ...l, ...patch } : l)));
  const total = lines.reduce((s, l) => s + (parseMoney(l.amount) ?? 0), 0);
  return (
    <div className="flex flex-col gap-2">
      {lines.map((l, i) => (
        <div key={i} className="grid grid-cols-2 gap-2 border-b border-hair pb-2 sm:grid-cols-[minmax(0,2fr)_9rem_minmax(0,1.3fr)_8rem_auto] sm:items-end">
          <Field label={t("budget.fin.categoria")}>
            <Select className="field" value={l.cost_category_id} onChange={(e) => set(i, { cost_category_id: e.target.value })}>
              {cats.map((c) => (
                <option key={c.id} value={c.id}>{c.name} · {c.nature.toUpperCase()}</option>
              ))}
            </Select>
          </Field>
          <Field label={t("budget.fin.mese")}>
            <input className="field" type="month" value={l.period} onChange={(e) => set(i, { period: e.target.value })} />
          </Field>
          <Field label={t("budget.fin.fonte")}>
            <Select className="field" value={l.funding_source_id} onChange={(e) => set(i, { funding_source_id: e.target.value })}>
              <option value="">—</option>
              {ov.funding_sources.map((f) => (
                <option key={f.id} value={f.id}>{f.name}</option>
              ))}
            </Select>
          </Field>
          <Field label={t("budget.fin.amountLabel", { currency: ov.settings.base_currency })}>
            <input className="field text-right tabular" inputMode="decimal" required value={l.amount} onChange={(e) => set(i, { amount: e.target.value })} />
          </Field>
          <button type="button" className="btn btn-danger justify-center" aria-label={t("budget.fin.removeLineN", { n: i + 1 })} onClick={() => setLines(lines.filter((_, j) => j !== i))}>
            {t("budget.fin.remove")}
          </button>
        </div>
      ))}
      <div className="flex items-center justify-between gap-3">
        <button
          type="button"
          className="text-[12.5px] text-accent underline"
          onClick={() => setLines([...lines, { cost_category_id: cats[0]?.id ?? "", funding_source_id: "", period: "", amount: "", note: "" }])}
        >
          {t("budget.fin.addLine")}
        </button>
        <span className="text-[12.5px]">{t("budget.fin.totale")} <span className="font-mono font-semibold tabular">{money(total, ov.settings.base_currency)}</span></span>
      </div>
    </div>
  );
}

function BudgetDetail({ id, ov, onClose, onSelect }: { id: string; ov: FinOverview; onClose: () => void; onSelect: (id: string) => void }) {
  const t = useT();
  const cur = ov.settings.base_currency;
  const b = useQuery({ queryKey: ["finance", "budget", id], queryFn: () => financeApi.budget(id) });
  const [lines, setLines] = useState<EditLine[] | null>(null);
  const [note, setNote] = useState("");
  useEffect(() => {
    if (b.data) {
      setLines(b.data.lines?.map(toEdit) ?? []);
      setNote(b.data.note);
    }
  }, [b.data]);
  const invalidate = useInvalidate();
  const save = useMutation({ mutationFn: () => financeApi.saveLines(id, (lines ?? []).map(fromEdit), note), onSuccess: invalidate });
  const submit = useMutation({
    mutationFn: async () => {
      await financeApi.saveLines(id, (lines ?? []).map(fromEdit), note);
      return financeApi.submitBudget(id);
    },
    onSuccess: invalidate,
  });
  const del = useMutation({ mutationFn: () => financeApi.deleteBudget(id), onSuccess: () => { invalidate(); onClose(); } });
  const derive = useMutation({
    mutationFn: (kind: BudgetKind) => financeApi.createBudget({ element: b.data!.element_id, fiscal_year_id: b.data!.fiscal_year_id, kind }),
    onSuccess: (nb) => { invalidate(); onSelect(nb.id); },
  });
  if (b.error) return <ErrorNote error={b.error} />;
  if (!b.data || !lines) return null;
  const d: Budget = b.data;
  return (
    <section aria-label={t("budget.fin.budgetDetail", { key: d.element_key, version: d.version })} className="flex flex-col gap-3 border-l-2 border-accent pl-4">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <h3 className="m-0 text-[17px] font-bold">
          <Link to="/e/$key" params={{ key: d.element_key }} className="font-mono text-accent no-underline hover:underline">{d.element_key}</Link>{" "}
          v{d.version} · {kindLabel(d.kind)}
        </h3>
        <Status s={d.status} />
        <button className="ml-auto text-[12px] text-muted underline hover:text-fg" onClick={onClose}>{t("budget.fin.chiudi")}</button>
      </div>
      {d.decision_note && d.status === "draft" && (
        <p className="m-0 border-l-2 border-danger pl-3 text-[13px]"><span className="font-semibold">{t("budget.fin.respinto")} </span>{d.decision_note}</p>
      )}
      {d.status === "submitted" && (
        <p className="m-0 text-[13px] text-muted">
          <Trans
            k="budget.fin.inApproval"
            slots={{ link: <Link to="/approvazioni" className="text-accent underline">{t("budget.fin.leMieApprovazioni")}</Link> }}
          />
        </p>
      )}
      <BudgetSource cur={cur} canWrite={ov.can_write} kind={d.kind} budget={{ id: d.id, status: d.status }} lines={sourceLines(lines)} />
      {d.can_edit ? (
        <form className="flex flex-col gap-3" onSubmit={(e) => { e.preventDefault(); save.mutate(); }}>
          <LinesEditor ov={ov} lines={lines} setLines={setLines} />
          <Field label={t("budget.fin.nota")}>
            <input className="field" value={note} onChange={(e) => setNote(e.target.value)} />
          </Field>
          <div className="flex flex-wrap gap-2">
            <button className="btn" disabled={save.isPending}>{t("budget.fin.salvaLaBozza")}</button>
            <button type="button" className="btn btn-primary" disabled={submit.isPending || lines.length === 0} onClick={() => submit.mutate()}>
              {d.kind === "forecast" ? t("budget.fin.publishForecast") : t("budget.fin.sendApproval")}
            </button>
            <button type="button" className="btn btn-danger" disabled={del.isPending} onClick={() => del.mutate()}>{t("budget.fin.eliminaLaBozza")}</button>
          </div>
        </form>
      ) : (
        <Table
          head={
            <>
              <Th>{t("budget.fin.categoria")}</Th>
              <Th hide="hidden sm:table-cell">{t("budget.fin.mese")}</Th>
              <Th hide="hidden md:table-cell">{t("budget.fin.fonte")}</Th>
              <Th right>{t("budget.fin.importo")}</Th>
              <Th right>{t("budget.fin.effettivo")}</Th>
            </>
          }
        >
          {d.lines?.map((l) => (
            <tr key={l.id} className="border-b border-hair">
              <Td>{l.category} <span className="font-mono text-[10.5px] text-muted uppercase">{l.nature}</span></Td>
              <Td hide="hidden sm:table-cell">{l.period ? l.period.slice(0, 7) : t("budget.fin.wholeYear")}</Td>
              <Td hide="hidden md:table-cell">{l.funding_source ?? "—"}</Td>
              <Td right mono>{money(l.amount_cents, cur, true)}</Td>
              <Td right mono className={l.effective_cents !== l.amount_cents ? "font-semibold" : "text-muted"}>
                {money(l.effective_cents, cur, true)}
                {l.pending_out_cents > 0 && <span className="block text-[11px] font-normal text-accent">−{money(l.pending_out_cents, cur)} {t("budget.fin.pendingLabel")}</span>}
              </Td>
            </tr>
          ))}
        </Table>
      )}
      {ov.can_write && d.status === "approved" && (
        <div className="flex flex-wrap gap-2">
          {d.kind !== "forecast" && <button className="btn" disabled={derive.isPending} onClick={() => derive.mutate("revised")}>{t("budget.fin.nuovaRevisione")}</button>}
          <button className="btn" disabled={derive.isPending} onClick={() => derive.mutate("forecast")}>{t("budget.fin.nuovaPrevisioneAFinire")}</button>
        </div>
      )}
      <ErrorNote error={save.error ?? submit.error ?? del.error ?? derive.error} />
    </section>
  );
}

function NewBudget({ ov, fyID, richiesta, onCreated }: { ov: FinOverview; fyID: string; richiesta?: string; onCreated: (id: string) => void }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [element, setElement] = useState(richiesta ?? "");
  const [lines, setLines] = useState<EditLine[]>([]);
  const [allocId, setAllocId] = useState("");
  const invalidate = useInvalidate();
  const pers = ov.categories.find((c) => c.code === "PERS")?.id ?? ov.categories[0]?.id ?? "";
  const create = useMutation({
    mutationFn: () => financeApi.createBudget({ element: element.trim(), fiscal_year_id: fyID, kind: "requested", lines: lines.map(fromEdit), allocation_id: allocId || null }),
    onSuccess: (b) => {
      invalidate();
      setOpen(false);
      setLines([]);
      setAllocId("");
      onCreated(b.id);
    },
  });
  if (!open)
    return (
      <button
        className="btn self-start"
        onClick={() => {
          setOpen(true);
          setLines([{ cost_category_id: pers, funding_source_id: "", period: "", amount: "", note: "" }]);
        }}
      >
        {t("budget.fin.newBudget")}
      </button>
    );
  return (
    <form className="flex flex-col gap-3 border-t-2 border-rule pt-3" onSubmit={(e) => { e.preventDefault(); create.mutate(); }}>
      <h3 className="label m-0">{t("budget.fin.nuovoBudgetProposto")}</h3>
      <Field label={t("budget.fin.richiestaOProgettoChiave")}>
        <input className="field max-w-60 font-mono" required value={element} onChange={(e) => { setElement(e.target.value.toUpperCase()); setAllocId(""); }} />
      </Field>
      <BudgetSource
        cur={ov.settings.base_currency}
        canWrite
        kind="requested"
        lines={sourceLines(lines)}
        element={/^[A-Z][A-Z0-9_]*-\d+$/.test(element.trim()) ? element.trim() : ""}
        fiscalYear={fyID}
        value={allocId}
        onChange={setAllocId}
      />
      <LinesEditor ov={ov} lines={lines} setLines={setLines} />
      <div className="flex gap-2">
        <button className="btn btn-primary" disabled={create.isPending}>{t("budget.fin.creaLaBozza")}</button>
        <button type="button" className="btn" onClick={() => setOpen(false)}>{t("budget.fin.annulla")}</button>
      </div>
      <ErrorNote error={create.error} />
    </form>
  );
}

function Transfers({ ov, fyID, budgets }: { ov: FinOverview; fyID: string; budgets: Budget[] }) {
  const t = useT();
  const cur = ov.settings.base_currency;
  const list = useQuery({ queryKey: ["finance", "transfers", fyID], queryFn: () => financeApi.transfers(fyID) });
  const approved = budgets.filter((b) => b.status === "approved" && b.kind !== "forecast");
  const [fromB, setFromB] = useState("");
  const [toB, setToB] = useState("");
  const [fromL, setFromL] = useState("");
  const [toL, setToL] = useState("");
  const [amount, setAmount] = useState("");
  const [reason, setReason] = useState("");
  const fromD = useQuery({ queryKey: ["finance", "budget", fromB], queryFn: () => financeApi.budget(fromB), enabled: !!fromB });
  const toD = useQuery({ queryKey: ["finance", "budget", toB], queryFn: () => financeApi.budget(toB), enabled: !!toB });
  const invalidate = useInvalidate();
  const create = useMutation({
    mutationFn: () => financeApi.createTransfer({ from_line_id: fromL, to_line_id: toL, amount_cents: parseMoney(amount) ?? 0, reason }),
    onSuccess: () => {
      invalidate();
      setAmount("");
      setReason("");
    },
  });
  const lineOpt = (b?: Budget) =>
    b?.lines?.map((l) => (
      <option key={l.id} value={l.id}>
        {l.category}{l.period ? ` · ${l.period.slice(0, 7)}` : ""} · {t("budget.fin.available", { amount: money(l.effective_cents - l.pending_out_cents, cur) })}
      </option>
    ));
  return (
    <section className="flex flex-col gap-3">
      <SectionTitle>{t("budget.fin.trasferimenti")}</SectionTitle>
      <ErrorNote error={list.error} />
      <Table
        head={
          <>
            <Th>{t("budget.fin.data")}</Th>
            <Th>Da</Th>
            <Th>A</Th>
            <Th right>{t("budget.fin.importo")}</Th>
            <Th>{t("budget.fin.stato")}</Th>
            <Th hide="hidden md:table-cell">{t("budget.fin.motivazione")}</Th>
          </>
        }
        empty={list.data && list.data.items.length === 0 ? t("budget.fin.noTransfers") : undefined}
      >
        {list.data?.items.map((t) => (
          <tr key={t.id} className="border-b border-hair">
            <Td mono>{date(t.created_at)}</Td>
            <Td><span className="font-mono text-accent">{t.from_element}</span> <span className="text-muted">{t.from_category}</span></Td>
            <Td><span className="font-mono text-accent">{t.to_element}</span> <span className="text-muted">{t.to_category}</span></Td>
            <Td right mono>{money(t.amount_cents, cur, true)}</Td>
            <Td>
              <Status s={t.status} />
              {t.decided_by && <span className="block text-[11.5px] text-muted">{t.decided_by}</span>}
            </Td>
            <Td hide="hidden md:table-cell"><span className="block max-w-80 break-words">{t.reason}</span></Td>
          </tr>
        ))}
      </Table>
      {ov.can_write && approved.length > 0 && (
        <form className="grid gap-3 border-t border-hair pt-3 sm:grid-cols-2 lg:grid-cols-4 lg:items-end" onSubmit={(e: FormEvent) => { e.preventDefault(); create.mutate(); }}>
          <Field label={t("budget.fin.daBudget")}>
            <Select className="field" required value={fromB} onChange={(e) => { setFromB(e.target.value); setFromL(""); }}>
              <option value="">—</option>
              {approved.map((b) => <option key={b.id} value={b.id}>{b.element_key} v{b.version}</option>)}
            </Select>
          </Field>
          <Field label={t("budget.fin.rigaDiPartenza")}>
            <Select className="field" required value={fromL} onChange={(e) => setFromL(e.target.value)}>
              <option value="">—</option>
              {lineOpt(fromD.data)}
            </Select>
          </Field>
          <Field label={t("budget.fin.aBudget")}>
            <Select className="field" required value={toB} onChange={(e) => { setToB(e.target.value); setToL(""); }}>
              <option value="">—</option>
              {approved.map((b) => <option key={b.id} value={b.id}>{b.element_key} v{b.version}</option>)}
            </Select>
          </Field>
          <Field label={t("budget.fin.rigaDiArrivo")}>
            <Select className="field" required value={toL} onChange={(e) => setToL(e.target.value)}>
              <option value="">—</option>
              {lineOpt(toD.data)?.filter((o) => o.key !== fromL)}
            </Select>
          </Field>
          <Field label={t("budget.fin.amountLabel", { currency: cur })}>
            <input className="field text-right tabular" inputMode="decimal" required value={amount} onChange={(e) => setAmount(e.target.value)} />
          </Field>
          <Field label={t("budget.fin.motivazioneObbligatoriaNellAudit")} wide>
            <input className="field" required value={reason} onChange={(e) => setReason(e.target.value)} />
          </Field>
          <button className="btn btn-primary justify-center" disabled={create.isPending || !reason.trim()}>{t("budget.fin.trasferisci")}</button>
          <p className="m-0 text-[12px] text-muted sm:col-span-2 lg:col-span-4">{t("budget.fin.transferHelp")}</p>
          <div className="sm:col-span-2 lg:col-span-4"><ErrorNote error={create.error} /></div>
        </form>
      )}
    </section>
  );
}

// ---------------------------------------------------------------------
// Voci di costo
// ---------------------------------------------------------------------

function CostsTab({ ov, richiesta }: { ov: FinOverview; richiesta?: string }) {
  const t = useT();
  const cur = ov.settings.base_currency;
  const [draft, setDraft] = useState<CostFilter>({ element: richiesta });
  const [filter, setFilter] = useState<CostFilter>({ element: richiesta });
  useEffect(() => {
    setDraft((d) => ({ ...d, element: richiesta }));
    setFilter((f) => ({ ...f, element: richiesta }));
  }, [richiesta]);
  const list = useInfiniteQuery({
    queryKey: ["finance", "costs", filter],
    queryFn: ({ pageParam }) => financeApi.costs(filter, pageParam),
    initialPageParam: undefined as string | undefined,
    getNextPageParam: (p) => p.next,
  });
  const items = list.data?.pages.flatMap((p) => p.items) ?? [];
  const head = list.data?.pages[0];
  const invalidate = useInvalidate();
  const del = useMutation({ mutationFn: financeApi.deleteCost, onSuccess: invalidate });
  const set = (k: keyof CostFilter, v: string) => setDraft({ ...draft, [k]: v || undefined });
  return (
    <>
      <form
        className="grid grid-cols-2 gap-2 sm:grid-cols-4 lg:grid-cols-[8rem_minmax(0,1fr)_8rem_8rem_9rem_9rem_minmax(0,1fr)_auto] lg:items-end"
        onSubmit={(e) => { e.preventDefault(); setFilter(draft); }}
        aria-label={t("budget.fin.filtri")}
      >
        <Field label={t("budget.fin.richiesta")}><input className="field font-mono" placeholder="DEM-12" value={draft.element ?? ""} onChange={(e) => set("element", e.target.value.toUpperCase())} /></Field>
        <Field label={t("budget.fin.categoria")}>
          <Select className="field" value={draft.category ?? ""} onChange={(e) => set("category", e.target.value)}>
            <option value="">{t("budget.fin.tutte")}</option>
            {ov.categories.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
          </Select>
        </Field>
        <Field label={t("budget.fin.tipo")}>
          <Select className="field" value={draft.kind ?? ""} onChange={(e) => set("kind", e.target.value)}>
            <option value="">{t("budget.fin.tutti")}</option>
            <option value="actual">{t("budget.fin.consuntivo")}</option>
            <option value="committed">{t("budget.fin.impegnato")}</option>
          </Select>
        </Field>
        <Field label={t("budget.fin.origine")}>
          <Select className="field" value={draft.source ?? ""} onChange={(e) => set("source", e.target.value)}>
            <option value="">{t("budget.fin.tutte")}</option>
            {SOURCE_KEYS.map((k) => <option key={k} value={k}>{sourceLabel(k)}</option>)}
          </Select>
        </Field>
        <Field label={t("budget.fin.dal")}><input className="field" type="date" value={draft.from ?? ""} onChange={(e) => set("from", e.target.value)} /></Field>
        <Field label={t("budget.fin.to")}><input className="field" type="date" value={draft.to ?? ""} onChange={(e) => set("to", e.target.value)} /></Field>
        <Field label={t("budget.fin.testo")}><input className="field" placeholder={t("budget.fin.descrizioneFornitoreNumero")} value={draft.q ?? ""} onChange={(e) => set("q", e.target.value)} /></Field>
        <button className="btn btn-primary justify-center">{t("budget.fin.filtra")}</button>
      </form>
      {head && (
        <dl className="m-0 flex flex-wrap gap-x-8 gap-y-2 border-b-2 border-rule pb-3">
          {[
            [t("budget.fin.entries"), fmtNumber(head.count)],
            [t("budget.fin.actualCap"), money(head.actual_base_cents, cur)],
            [t("budget.fin.committedCap"), money(head.committed_base_cents, cur)],
            [t("budget.fin.totalCap"), money(head.total_base_cents, cur)],
          ].map(([k, v]) => (
            <div key={k} className="flex flex-col">
              <dt className="label text-muted">{k}</dt>
              <dd className="m-0 font-mono text-[17px] font-semibold tabular">{v}</dd>
            </div>
          ))}
        </dl>
      )}
      <ErrorNote error={list.error ?? del.error} />
      <Table
        head={
          <>
            <Th>{t("budget.fin.data")}</Th>
            <Th hide="hidden sm:table-cell">{t("budget.fin.richiesta")}</Th>
            <Th hide="hidden md:table-cell">{t("budget.fin.categoria")}</Th>
            <Th>{t("budget.fin.descrizione")}</Th>
            <Th hide="hidden lg:table-cell">{t("budget.fin.origine")}</Th>
            <Th right hide="hidden sm:table-cell">{t("budget.fin.importo")}</Th>
            <Th right>{t("budget.fin.inCurrency", { currency: cur })}</Th>
            <Th />
          </>
        }
        empty={list.data && items.length === 0 ? t("budget.fin.noEntries") : undefined}
      >
        {items.map((c) => (
          <tr key={c.id} className="border-b border-hair">
            <Td mono className="whitespace-nowrap text-[12px] sm:text-[13px]">{fmtDate(c.entry_date, { day: "2-digit", month: "2-digit", year: "2-digit" })}</Td>
            <Td mono hide="hidden sm:table-cell"><Link to="/e/$key" params={{ key: c.element_key }} className="text-accent no-underline hover:underline">{c.element_key}</Link></Td>
            <Td hide="hidden md:table-cell">{c.category}</Td>
            <Td>
              <span className="font-mono text-[11.5px] text-accent sm:hidden">{c.element_key} </span>
              <span className="block max-w-[46vw] break-words sm:max-w-80">{c.member ? `${c.member} · ${fmtNumber(c.hours)} ${t("budget.fin.hoursUnit")}` : c.description || "—"}</span>
              <span className="block text-[11.5px] text-muted">
                {c.kind === "committed" ? t("budget.fin.impegnato") : t("budget.fin.consuntivo")}
                {c.supplier ? ` · ${c.supplier}` : ""}
                {c.source_ref && c.source !== "work_log" ? ` · ${c.source_ref.replace(/^ref:/, "").replace(/^sha:.*/, t("budget.fin.noNumber"))}` : ""}
                {c.hourly_cents != null ? ` · ${money(c.hourly_cents, c.currency, true)}/${t("budget.fin.hoursUnit")}` : ""}
                {c.capitalizable ? ` · ${t("budget.fin.capitalizableLower")}` : ""}
              </span>
            </Td>
            <Td hide="hidden lg:table-cell">{sourceLabel(c.source)}</Td>
            <Td right mono hide="hidden sm:table-cell">{c.currency !== cur ? money(c.amount_cents, c.currency, true) : ""}</Td>
            <Td right mono>{money(c.amount_base_cents, cur, true)}</Td>
            <Td right>
              {ov.can_write && c.source !== "work_log" && (
                <button className="text-[12px] text-muted underline hover:text-danger" aria-label={t("budget.fin.deleteEntryOf", { date: date(c.entry_date) })} onClick={() => del.mutate(c.id)}>
                  {t("budget.fin.elimina")}
                </button>
              )}
            </Td>
          </tr>
        ))}
      </Table>
      {list.hasNextPage && (
        <button className="btn self-start" disabled={list.isFetchingNextPage} onClick={() => list.fetchNextPage()}>{t("budget.fin.altreVoci")}</button>
      )}
      {ov.can_write && <NewCost ov={ov} richiesta={filter.element} />}
    </>
  );
}

function NewCost({ ov, richiesta }: { ov: FinOverview; richiesta?: string }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const cons = ov.categories.find((c) => c.code === "CONS")?.id ?? ov.categories[0]?.id ?? "";
  const blank = () => ({ element: richiesta ?? "", cost_category_id: cons, source: "invoice", kind: "actual", entry_date: today(), amount: "", currency: ov.settings.base_currency, description: "", supplier: "", source_ref: "", capitalizable: false });
  const [f, setF] = useState(blank);
  const invalidate = useInvalidate();
  const create = useMutation({
    mutationFn: () =>
      financeApi.createCost({
        element: f.element.trim(),
        cost_category_id: f.cost_category_id,
        source: f.source,
        kind: f.kind,
        entry_date: f.entry_date,
        amount_cents: parseMoney(f.amount) ?? 0,
        currency: f.currency,
        description: f.description,
        supplier: f.supplier || null,
        source_ref: f.source_ref || null,
        capitalizable: f.capitalizable,
      }),
    onSuccess: () => {
      invalidate();
      setF(blank());
    },
  });
  if (!open) return <button className="btn self-start" onClick={() => setOpen(true)}>{t("budget.fin.nuovaVoce")}</button>;
  return (
    <form className="grid gap-3 border-t-2 border-rule pt-3 sm:grid-cols-2 lg:grid-cols-4" onSubmit={(e) => { e.preventDefault(); create.mutate(); }}>
      <h3 className="label m-0 sm:col-span-2 lg:col-span-4">{t("budget.fin.nuovaVoceDiCosto")}</h3>
      <Field label={t("budget.fin.richiesta")}><input className="field font-mono" required value={f.element} onChange={(e) => setF({ ...f, element: e.target.value.toUpperCase() })} /></Field>
      <Field label={t("budget.fin.categoria")}>
        <Select className="field" value={f.cost_category_id} onChange={(e) => setF({ ...f, cost_category_id: e.target.value })}>
          {ov.categories.filter((c) => c.active).map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
        </Select>
      </Field>
      <Field label={t("budget.fin.origine")}>
        <Select className="field" value={f.source} onChange={(e) => setF({ ...f, source: e.target.value })}>
          {["invoice", "purchase_order", "recurring", "manual"].map((k) => <option key={k} value={k}>{sourceLabel(k)}</option>)}
        </Select>
      </Field>
      <Field label={t("budget.fin.tipo")}>
        <Select className="field" value={f.kind} onChange={(e) => setF({ ...f, kind: e.target.value })}>
          <option value="actual">{t("budget.fin.consuntivo")}</option>
          <option value="committed">{t("budget.fin.impegnato")}</option>
        </Select>
      </Field>
      <Field label={t("budget.fin.data")}><input className="field" type="date" required value={f.entry_date} onChange={(e) => setF({ ...f, entry_date: e.target.value })} /></Field>
      <Field label={t("budget.fin.importo")}><input className="field text-right tabular" inputMode="decimal" required value={f.amount} onChange={(e) => setF({ ...f, amount: e.target.value })} /></Field>
      <Field label={t("budget.fin.valuta")}><input className="field font-mono uppercase" maxLength={3} value={f.currency} onChange={(e) => setF({ ...f, currency: e.target.value.toUpperCase() })} /></Field>
      <Field label={t("budget.fin.numeroDelDocumento")}><input className="field" value={f.source_ref} onChange={(e) => setF({ ...f, source_ref: e.target.value })} /></Field>
      <Field label={t("budget.fin.descrizione")} wide><input className="field" value={f.description} onChange={(e) => setF({ ...f, description: e.target.value })} /></Field>
      <Field label={t("budget.fin.fornitore")}><input className="field" value={f.supplier} onChange={(e) => setF({ ...f, supplier: e.target.value })} /></Field>
      <label className="flex items-center gap-2 self-end pb-2">
        <input type="checkbox" checked={f.capitalizable} onChange={(e) => setF({ ...f, capitalizable: e.target.checked })} />
        {t("budget.fin.capitalizzabile")}
      </label>
      <div className="flex gap-2 sm:col-span-2 lg:col-span-4">
        <button className="btn btn-primary" disabled={create.isPending}>{t("budget.fin.registra")}</button>
        <button type="button" className="btn" onClick={() => setOpen(false)}>{t("budget.fin.chiudi")}</button>
      </div>
      <div className="sm:col-span-2 lg:col-span-4"><ErrorNote error={create.error} /></div>
    </form>
  );
}

// ---------------------------------------------------------------------
// Import CSV in due tempi
// ---------------------------------------------------------------------

const DATE_FORMATS = ["AAAA-MM-GG", "GG/MM/AAAA", "GG.MM.AAAA", "GG-MM-AAAA", "MM/GG/AAAA"];

export function ImportTab({ ov }: { ov: FinOverview }) {
  const t = useT();
  const cur = ov.settings.base_currency;
  const profiles = useQuery({ queryKey: ["finance", "profiles"], queryFn: financeApi.profiles });
  const runs = useQuery({ queryKey: ["finance", "imports"], queryFn: financeApi.imports });
  const [profileID, setProfileID] = useState("");
  const [file, setFile] = useState<{ name: string; text: string } | null>(null);
  const [run, setRun] = useState<ImportRun | null>(null);
  const [skip, setSkip] = useState(false);
  const [editing, setEditing] = useState(false);
  const invalidate = useInvalidate();
  useEffect(() => {
    if (!profileID && profiles.data?.items[0]) setProfileID(profiles.data.items[0].id);
  }, [profiles.data, profileID]);
  const header = useMemo(() => {
    if (!file) return [] as string[];
    const first = file.text.replace(/^﻿/, "").split(/\r?\n/)[0] ?? "";
    const delim = (first.match(/;/g)?.length ?? 0) > (first.match(/,/g)?.length ?? 0) ? ";" : first.includes("\t") ? "\t" : ",";
    return first.split(delim).map((h) => h.trim().replace(/^"|"$/g, "")).filter(Boolean);
  }, [file]);
  const preview = useMutation({
    mutationFn: () => financeApi.previewImport({ profile_id: profileID, file_name: file!.name, csv: file!.text }),
    onSuccess: (r) => { setRun(r); setSkip(false); invalidate(); },
  });
  const confirm = useMutation({
    mutationFn: () => financeApi.confirmImport(run!.id, file!.text, skip),
    onSuccess: (r) => { setRun(r); invalidate(); },
  });
  const rollback = useMutation({ mutationFn: financeApi.rollbackImport, onSuccess: (r) => { if (run?.id === r.id) setRun(r); invalidate(); } });
  const profile = profiles.data?.items.find((p) => p.id === profileID);

  return (
    <>
      <section className="flex flex-col gap-3">
        <SectionTitle>{t("budget.fin.1Prova")}</SectionTitle>
        <div className="flex flex-wrap items-end gap-3">
          <Field label={t("budget.fin.profiloDiMappatura")}>
            <Select className="field w-auto min-w-52" value={profileID} onChange={(e) => setProfileID(e.target.value)}>
              {profiles.data?.items.length === 0 && <option value="">{t("budget.fin.nessunProfilo")}</option>}
              {profiles.data?.items.map((p) => <option key={p.id} value={p.id}>{p.name}</option>)}
            </Select>
          </Field>
          {ov.can_write && (
            <button className="text-[12.5px] text-accent underline" onClick={() => setEditing((v) => !v)}>
              {editing ? t("budget.fin.closeProfile") : profile ? t("budget.fin.editOrNewProfile") : t("budget.fin.newProfile")}
            </button>
          )}
          <Field label={t("budget.fin.fileCsv")}>
            <input
              className="field w-auto"
              type="file"
              accept=".csv,text/csv,text/plain"
              onChange={async (e) => {
                const f = e.target.files?.[0];
                setRun(null);
                setFile(f ? { name: f.name, text: await f.text() } : null);
              }}
            />
          </Field>
          <button className="btn btn-primary" disabled={!file || !profileID || preview.isPending || !ov.can_write} onClick={() => preview.mutate()}>
            {t("budget.fin.tryNoSave")}
          </button>
        </div>
        {editing && <ProfileEditor fields={profiles.data?.fields ?? ov.import_fields} header={header} profile={profile} onSaved={(id) => { setProfileID(id); setEditing(false); }} />}
        <ErrorNote error={preview.error} />
      </section>

      {run && (
        <section className="flex flex-col gap-3" aria-live="polite">
          <SectionTitle aside={<Status s={run.status} />}>{t("budget.fin.outcomeOf", { name: run.file_name })}</SectionTitle>
          <dl className="m-0 flex flex-wrap gap-x-8 gap-y-2">
            {(
              [
                ["rows", t("budget.fin.rowsCap"), run.rows_total],
                ["ok", run.status === "preview" ? t("budget.fin.toImport") : t("budget.fin.importate"), run.rows_ok],
                ["dup", t("budget.fin.giaPresenti"), run.rows_duplicate],
                ["err", t("budget.fin.withErrors"), run.rows_error],
              ] as const
            ).map(([id, label, v]) => (
              <div key={id} className="flex flex-col">
                <dt className="label text-muted">{label}</dt>
                <dd className={`m-0 font-mono text-[17px] font-semibold tabular ${id === "err" && Number(v) > 0 ? "text-danger" : ""}`}>{v}</dd>
              </div>
            ))}
          </dl>
          {run.errors.length > 0 && (
            <Table head={<><Th>{t("budget.fin.riga")}</Th><Th>{t("budget.fin.campo")}</Th><Th>{t("budget.fin.errore")}</Th></>}>
              {run.errors.slice(0, 50).map((e, i) => (
                <tr key={i} className="border-b border-hair">
                  <Td mono>{e.row}</Td>
                  <Td>{e.field ? ov.import_fields[e.field] ?? e.field : "—"}</Td>
                  <Td className="text-danger">{e.message}</Td>
                </tr>
              ))}
            </Table>
          )}
          {run.preview.length > 0 && (
            <>
              <h3 className="label m-0 text-muted">{t("budget.fin.previewFirst", { count: run.preview.length })}</h3>
              <Table
                head={
                  <>
                    <Th>{t("budget.fin.riga")}</Th>
                    <Th>{t("budget.fin.data")}</Th>
                    <Th>{t("budget.fin.richiesta")}</Th>
                    <Th hide="hidden md:table-cell">{t("budget.fin.categoria")}</Th>
                    <Th hide="hidden lg:table-cell">{t("budget.fin.descrizione")}</Th>
                    <Th right>{t("budget.fin.importo")}</Th>
                    <Th right hide="hidden sm:table-cell">In {cur}</Th>
                  </>
                }
              >
                {run.preview.map((p) => (
                  <tr key={p.row} className={`border-b border-hair ${p.duplicate ? "text-muted" : ""}`}>
                    <Td mono>{p.row}</Td>
                    <Td mono>{date(p.entry_date)}</Td>
                    <Td mono>{p.element_key}{p.duplicate && <span className="ml-1.5 font-sans text-[11px]">{t("budget.fin.giaImportata")}</span>}</Td>
                    <Td hide="hidden md:table-cell">{p.category}</Td>
                    <Td hide="hidden lg:table-cell"><span className="block max-w-72 break-words">{p.description || "—"}</span></Td>
                    <Td right mono>{money(p.amount_cents, p.currency, true)}</Td>
                    <Td right mono hide="hidden sm:table-cell">{money(p.amount_base_cents, cur, true)}</Td>
                  </tr>
                ))}
              </Table>
            </>
          )}
          {run.status === "preview" && ov.can_write && (
            <div className="flex flex-wrap items-center gap-3">
              {run.rows_error > 0 && (
                <label className="flex items-center gap-2">
                  <input type="checkbox" checked={skip} onChange={(e) => setSkip(e.target.checked)} />
                  {t("budget.fin.skipErrors", { count: run.rows_error })}
                </label>
              )}
              <button className="btn btn-primary" disabled={confirm.isPending || !file || (run.rows_error > 0 && !skip) || run.rows_ok === 0} onClick={() => confirm.mutate()}>
                {t("budget.fin.confirmImport", { count: run.rows_ok })}
              </button>
            </div>
          )}
          <ErrorNote error={confirm.error} />
        </section>
      )}

      <section className="flex flex-col gap-3">
        <SectionTitle>{t("budget.fin.importRecenti")}</SectionTitle>
        <ErrorNote error={runs.error ?? rollback.error} />
        <Table
          head={<><Th>{t("budget.fin.data")}</Th><Th>{t("budget.fin.file")}</Th><Th hide="hidden md:table-cell">{t("budget.fin.profilo")}</Th><Th>{t("budget.fin.stato")}</Th><Th right>{t("budget.fin.importate")}</Th><Th right hide="hidden sm:table-cell">{t("budget.fin.giaPresenti")}</Th><Th /></>}
          empty={runs.data && runs.data.items.length === 0 ? t("budget.fin.noImports") : undefined}
        >
          {runs.data?.items.map((r) => (
            <tr key={r.id} className="border-b border-hair">
              <Td mono className="whitespace-nowrap">{date(r.created_at)}</Td>
              <Td><span className="block max-w-56 [overflow-wrap:anywhere]">{r.file_name}</span></Td>
              <Td hide="hidden md:table-cell">{r.profile ?? "—"}</Td>
              <Td><Status s={r.status} /></Td>
              <Td right mono>{r.status === "preview" ? "—" : r.rows_ok}</Td>
              <Td right mono hide="hidden sm:table-cell">{r.rows_duplicate}</Td>
              <Td right>
                {r.status === "done" && ov.can_write && (
                  <button className="text-[12px] text-muted underline hover:text-danger" onClick={() => rollback.mutate(r.id)}>{t("budget.fin.annulla")}</button>
                )}
              </Td>
            </tr>
          ))}
        </Table>
      </section>
    </>
  );
}

function ProfileEditor({
  fields,
  header,
  profile,
  onSaved,
}: {
  fields: Record<string, string>;
  header: string[];
  profile?: { id: string; name: string; mapping: ImportMapping };
  onSaved: (id: string) => void;
}) {
  const t = useT();
  const [asNew, setAsNew] = useState(!profile);
  const [name, setName] = useState(profile?.name ?? "");
  const [m, setM] = useState<ImportMapping>(profile?.mapping ?? { columns: {}, date_format: "GG/MM/AAAA", decimal: ",", delimiter: ";", defaults: {} });
  // Colonne: quelle del profilo più quelle lette dal file scelto.
  const cols = Array.from(new Set([...Object.keys(m.columns), ...header]));
  const invalidate = useInvalidate();
  const save = useMutation({
    mutationFn: () => financeApi.saveProfile(asNew ? null : profile!.id, name, m),
    onSuccess: (p) => { invalidate(); onSaved(p.id); },
  });
  const setCol = (c: string, f: string) => {
    const next = { ...m.columns };
    if (f) next[c] = f;
    else delete next[c];
    setM({ ...m, columns: next });
  };
  const setDef = (f: string, v: string) => setM({ ...m, defaults: { ...(m.defaults ?? {}), [f]: v } });
  return (
    <form className="flex flex-col gap-3 border-l-2 border-accent pl-4" onSubmit={(e) => { e.preventDefault(); save.mutate(); }}>
      <div className="grid gap-3 sm:grid-cols-4">
        <Field label={t("budget.fin.nomeDelProfilo")}><input className="field" required value={name} onChange={(e) => setName(e.target.value)} /></Field>
        <Field label={t("budget.fin.separatore")}>
          <Select className="field" value={m.delimiter ?? ""} onChange={(e) => setM({ ...m, delimiter: e.target.value })}>
            <option value="">{t("budget.fin.automatico")}</option>
            <option value=";">{t("budget.fin.puntoEVirgola")}</option>
            <option value=",">{t("budget.fin.virgola")}</option>
            <option value={"\t"}>{t("budget.fin.tabulazione")}</option>
          </Select>
        </Field>
        <Field label={t("budget.fin.formatoDelleDate")}>
          <Select className="field" value={m.date_format ?? ""} onChange={(e) => setM({ ...m, date_format: e.target.value })}>
            {DATE_FORMATS.map((d) => <option key={d} value={d}>{d}</option>)}
          </Select>
        </Field>
        <Field label={t("budget.fin.separatoreDecimale")}>
          <Select className="field" value={m.decimal ?? ","} onChange={(e) => setM({ ...m, decimal: e.target.value })}>
            <option value=",">{t("budget.fin.commaDecimal")}</option>
            <option value=".">{t("budget.fin.dotDecimal")}</option>
          </Select>
        </Field>
      </div>
      <h4 className="label m-0 text-muted">{t("budget.fin.colonneDelFileCampi")}</h4>
      {cols.length === 0 && <p className="m-0 text-[12.5px] text-muted">{t("budget.fin.scegliUnFileLe")}</p>}
      <div className="grid gap-2 sm:grid-cols-2 lg:grid-cols-3">
        {cols.map((c) => (
          <label key={c} className="grid grid-cols-[minmax(0,1fr)_minmax(0,1.2fr)] items-center gap-2">
            <span className="min-w-0 font-mono text-[12.5px] [overflow-wrap:anywhere]" title={c}>{c}</span>
            <Select className="field" value={m.columns[c] ?? ""} onChange={(e) => setCol(c, e.target.value)}>
              <option value="">{t("budget.fin.nonImportare")}</option>
              {Object.entries(fields).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
            </Select>
          </label>
        ))}
      </div>
      <div className="grid gap-3 sm:grid-cols-3">
        <Field label={t("budget.fin.elementoSeLaColonna")}><input className="field font-mono" placeholder="DEM-12" value={m.defaults?.element ?? ""} onChange={(e) => setDef("element", e.target.value.toUpperCase())} /></Field>
        <Field label={t("budget.fin.categoriaSeMancaCodice")}><input className="field font-mono" placeholder="CONS" value={m.defaults?.category ?? ""} onChange={(e) => setDef("category", e.target.value)} /></Field>
        <Field label={t("budget.fin.tipoSeManca")}>
          <Select className="field" value={m.defaults?.kind ?? ""} onChange={(e) => setDef("kind", e.target.value)}>
            <option value="">{t("budget.fin.consuntivo")}</option>
            <option value="committed">{t("budget.fin.impegnato")}</option>
          </Select>
        </Field>
      </div>
      <div className="flex flex-wrap items-center gap-3">
        {profile && (
          <label className="flex items-center gap-2 text-[12.5px]">
            <input type="checkbox" checked={asNew} onChange={(e) => setAsNew(e.target.checked)} /> {t("budget.fin.salvaComeNuovoProfilo")}
          </label>
        )}
        <button className="btn btn-primary" disabled={save.isPending}>{t("budget.fin.salvaIlProfilo")}</button>
      </div>
      <ErrorNote error={save.error} />
    </form>
  );
}

// ---------------------------------------------------------------------
// Tariffe e ore
// ---------------------------------------------------------------------

function RatesTab({ ov, richiesta }: { ov: FinOverview; richiesta?: string }) {
  const t = useT();
  const cur = ov.settings.base_currency;
  const rates = useQuery({ queryKey: ["finance", "rates"], queryFn: financeApi.rates });
  const logs = useQuery({ queryKey: ["finance", "worklogs", richiesta ?? ""], queryFn: () => financeApi.workLogs(richiesta) });
  const [edit, setEdit] = useState<Rate | null>(null);
  const invalidate = useInvalidate();
  const del = useMutation({ mutationFn: financeApi.deleteRate, onSuccess: invalidate });
  const delLog = useMutation({ mutationFn: financeApi.deleteWorkLog, onSuccess: invalidate });
  const recompute = useMutation({ mutationFn: financeApi.recompute, onSuccess: invalidate });
  return (
    <>
      <section className="flex flex-col gap-3">
        <SectionTitle>{t("budget.fin.tariffeOrarie")}</SectionTitle>
        <p className="m-0 text-[12.5px] text-muted">{t("budget.fin.ratesHelp")}</p>
        <ErrorNote error={rates.error ?? del.error} />
        <Table
          head={<><Th>{t("budget.fin.personaORuolo")}</Th><Th right>{t("budget.fin.tariffa")}</Th><Th>{t("budget.fin.validita")}</Th><Th right hide="hidden sm:table-cell">{t("budget.fin.voci")}</Th><Th /></>}
          empty={rates.data && rates.data.items.length === 0 ? t("budget.fin.noRates") : undefined}
        >
          {rates.data?.items.map((r) => (
            <tr key={r.id} className={`border-b border-hair ${r.valid_to && r.valid_to < today() ? "text-muted" : ""}`}>
              <Td>{r.member ? r.member : <span>{t("budget.fin.ruolo2")} <span className="font-mono">{r.role_key}</span></span>}{r.note && <span className="block text-[11.5px] text-muted">{r.note}</span>}</Td>
              <Td right mono>{money(r.hourly_cents, r.currency, true)}/{t("budget.fin.hoursUnit")}</Td>
              <Td mono className="whitespace-nowrap">{date(r.valid_from)} → {r.valid_to ? date(r.valid_to) : t("budget.fin.ongoing")}</Td>
              <Td right mono hide="hidden sm:table-cell">{r.uses}</Td>
              <Td right>
                {ov.can_write && (
                  <span className="flex justify-end gap-3 text-[12px]">
                    <button className="text-accent underline" onClick={() => setEdit(r)}>{t("budget.fin.modifica")}</button>
                    <button className="text-muted underline hover:text-danger" onClick={() => del.mutate(r.id)}>{t("budget.fin.elimina")}</button>
                  </span>
                )}
              </Td>
            </tr>
          ))}
        </Table>
        {ov.can_write && <RateForm key={edit?.id ?? "nuova"} rate={edit} currency={cur} onDone={() => setEdit(null)} />}
      </section>
      <section className="flex flex-col gap-3">
        <SectionTitle
          aside={
            ov.can_write ? (
              <button className="text-[12px] text-accent underline" disabled={recompute.isPending} onClick={() => recompute.mutate()}>
                {t("budget.fin.recompute")}
              </button>
            ) : undefined
          }
        >
          {t("budget.fin.hoursLogged")}{richiesta ? ` · ${richiesta}` : ""}
        </SectionTitle>
        {recompute.data && (
          <p className="m-0 text-[12.5px]">
            {t("budget.fin.recomputed", { costed: recompute.data.costed, missing: recompute.data.without_rate })}
          </p>
        )}
        <ErrorNote error={logs.error ?? delLog.error ?? recompute.error} />
        <Table
          head={<><Th>{t("budget.fin.data")}</Th><Th>{t("budget.fin.richiesta")}</Th><Th>{t("budget.fin.persona")}</Th><Th right>{t("budget.fin.ore")}</Th><Th hide="hidden md:table-cell">{t("budget.fin.ruolo")}</Th><Th right>{t("budget.fin.costo")}</Th><Th /></>}
          empty={logs.data && logs.data.items.length === 0 ? t("budget.fin.noHours") : undefined}
        >
          {logs.data?.items.map((w) => (
            <tr key={w.id} className="border-b border-hair">
              <Td mono className="whitespace-nowrap">{date(w.work_date)}</Td>
              <Td mono><span className="text-accent">{w.element_key}</span></Td>
              <Td>{w.member}</Td>
              <Td right mono>{fmtNumber(w.hours)}</Td>
              <Td hide="hidden md:table-cell">{w.role_key ?? "—"}</Td>
              <Td right mono>
                {w.cost_base_cents != null ? money(w.cost_base_cents, cur, true) : <span className="font-sans text-danger">{t("budget.fin.senzaTariffa")}</span>}
                {w.hourly_cents != null && (
                  <span className="block font-sans text-[11px] text-muted">
                    {money(w.hourly_cents, w.rate_currency ?? cur, true)}/{t("budget.fin.hoursUnit")} · {w.rate_kind === "member" ? t("budget.fin.persona2") : t("budget.fin.ruolo2")}
                  </span>
                )}
              </Td>
              <Td right>
                {ov.can_write && <button className="text-[12px] text-muted underline hover:text-danger" onClick={() => delLog.mutate(w.id)}>{t("budget.fin.elimina")}</button>}
              </Td>
            </tr>
          ))}
        </Table>
        {ov.can_write && <WorkLogForm richiesta={richiesta} />}
      </section>
    </>
  );
}

function RateForm({ rate, currency, onDone }: { rate: Rate | null; currency: string; onDone: () => void }) {
  const t = useT();
  const [subject, setSubject] = useState<"role" | "member">(rate?.member_id ? "member" : "role");
  const [role, setRole] = useState(rate?.role_key ?? "");
  const [member, setMember] = useState(rate?.member_id ?? "");
  const [hourly, setHourly] = useState(rate ? centsToInput(rate.hourly_cents) : "");
  const [cur, setCur] = useState(rate?.currency ?? currency);
  const [from, setFrom] = useState(rate?.valid_from ?? today());
  const [to, setTo] = useState(rate?.valid_to ?? "");
  const [note, setNote] = useState(rate?.note ?? "");
  const invalidate = useInvalidate();
  const save = useMutation({
    mutationFn: () => {
      const r = {
        member_id: subject === "member" ? member : null,
        role_key: subject === "role" ? role : null,
        hourly_cents: parseMoney(hourly) ?? 0,
        currency: cur,
        valid_from: from,
        valid_to: to || null,
        note,
      };
      return rate ? financeApi.updateRate(rate.id, r) : financeApi.createRate(r);
    },
    onSuccess: () => { invalidate(); onDone(); },
  });
  return (
    <form className="grid gap-3 border-t border-hair pt-3 sm:grid-cols-3 lg:grid-cols-[9rem_minmax(0,1fr)_8rem_5rem_9rem_9rem_auto] lg:items-end" onSubmit={(e) => { e.preventDefault(); save.mutate(); }}>
      <Field label={t("budget.fin.per")}>
        <Select className="field" value={subject} onChange={(e) => setSubject(e.target.value as "role" | "member")}>
          <option value="role">{t("budget.fin.ruolo2")}</option>
          <option value="member">{t("budget.fin.persona2")}</option>
        </Select>
      </Field>
      {subject === "role" ? (
        <Field label={t("budget.fin.ruolo")}><input className="field font-mono" required placeholder={ROLE_EXAMPLE} value={role} onChange={(e) => setRole(e.target.value)} /></Field>
      ) : (
        <Field label={t("budget.fin.persona")}>
          <EntitySelect kind="member" noneLabel="—" className="field" required value={member} onChange={(v) => setMember(v)} />
        </Field>
      )}
      <Field label={t("budget.fin.tariffaOraria")}><input className="field text-right tabular" inputMode="decimal" required value={hourly} onChange={(e) => setHourly(e.target.value)} /></Field>
      <Field label={t("budget.fin.valuta")}><input className="field font-mono uppercase" maxLength={3} value={cur} onChange={(e) => setCur(e.target.value.toUpperCase())} /></Field>
      <Field label={t("budget.fin.dal")}><input className="field" type="date" required value={from} onChange={(e) => setFrom(e.target.value)} /></Field>
      <Field label={t("budget.fin.alVuotoInCorso")}><input className="field" type="date" value={to} onChange={(e) => setTo(e.target.value)} /></Field>
      <div className="flex gap-2">
        <button className="btn btn-primary" disabled={save.isPending}>{rate ? t("budget.fin.salva") : t("budget.fin.aggiungi")}</button>
        {rate && <button type="button" className="btn" onClick={onDone}>{t("budget.fin.annulla")}</button>}
      </div>
      <Field label={t("budget.fin.nota")} wide><input className="field" value={note} onChange={(e) => setNote(e.target.value)} /></Field>
      <div className="sm:col-span-3 lg:col-span-7"><ErrorNote error={save.error} /></div>
    </form>
  );
}

function WorkLogForm({ richiesta }: { richiesta?: string }) {
  const t = useT();
  const [f, setF] = useState({ element: richiesta ?? "", member_id: "", work_date: today(), hours: "8", role_key: "", capitalizable: false });
  const invalidate = useInvalidate();
  const save = useMutation({
    mutationFn: () =>
      financeApi.createWorkLog({ element: f.element.trim(), member_id: f.member_id || undefined, work_date: f.work_date, hours: Number(f.hours.replace(",", ".")), role_key: f.role_key || null, capitalizable: f.capitalizable }),
    onSuccess: invalidate,
  });
  return (
    <form className="grid gap-3 border-t border-hair pt-3 sm:grid-cols-3 lg:grid-cols-[8rem_minmax(0,1fr)_9rem_5rem_8rem_auto_auto] lg:items-end" onSubmit={(e) => { e.preventDefault(); save.mutate(); }}>
      <Field label={t("budget.fin.richiesta")}><input className="field font-mono" required value={f.element} onChange={(e) => setF({ ...f, element: e.target.value.toUpperCase() })} /></Field>
      <Field label={t("budget.fin.persona")}>
        <EntitySelect kind="member" noneLabel={t("budget.fin.me")} className="field" value={f.member_id} onChange={(v) => setF({ ...f, member_id: v })} />
      </Field>
      <Field label={t("budget.fin.data")}><input className="field" type="date" required value={f.work_date} onChange={(e) => setF({ ...f, work_date: e.target.value })} /></Field>
      <Field label={t("budget.fin.ore")}><input className="field text-right tabular" inputMode="decimal" required value={f.hours} onChange={(e) => setF({ ...f, hours: e.target.value })} /></Field>
      <Field label={t("budget.fin.ruolo")}><input className="field font-mono" placeholder={ROLE_EXAMPLE} value={f.role_key} onChange={(e) => setF({ ...f, role_key: e.target.value })} /></Field>
      <label className="flex items-center gap-2 pb-2"><input type="checkbox" checked={f.capitalizable} onChange={(e) => setF({ ...f, capitalizable: e.target.checked })} />{t("budget.fin.capitalizzabile")}</label>
      <button className="btn btn-primary" disabled={save.isPending}>{t("budget.fin.registraLeOre")}</button>
      <div className="sm:col-span-3 lg:col-span-7"><ErrorNote error={save.error} /></div>
    </form>
  );
}

// ---------------------------------------------------------------------
// Earned value
// ---------------------------------------------------------------------

function EVTab({ ov, richiesta }: { ov: FinOverview; richiesta?: string }) {
  const t = useT();
  const cur = ov.settings.base_currency;
  const list = useQuery({ queryKey: ["finance", "ev"], queryFn: financeApi.projects });
  const [sel, setSel] = useState(richiesta ?? "");
  useEffect(() => {
    if (!sel && list.data?.items[0]) setSel(list.data.items[0].key);
  }, [list.data, sel]);
  const [activate, setActivate] = useState("");
  const invalidate = useInvalidate();
  const on = useMutation({
    mutationFn: () => financeApi.saveProject(activate.trim(), { ev_enabled: true }),
    onSuccess: (p) => { invalidate(); setSel(p.key); setActivate(""); },
  });
  return (
    <>
      <section className="flex flex-col gap-3">
        <SectionTitle>{t("budget.fin.progettiConEarnedValue")}</SectionTitle>
        <ErrorNote error={list.error} />
        <Table
          head={<><Th>{t("budget.fin.progetto")}</Th><Th hide="hidden md:table-cell">{t("budget.fin.metodo")}</Th><Th right>{t("budget.fin.completamento2")}</Th><Th right>CPI</Th><Th right>SPI</Th><Th right hide="hidden sm:table-cell">EAC</Th></>}
          empty={list.data && list.data.items.length === 0 ? t("budget.fin.evOptional") : undefined}
        >
          {list.data?.items.map((p) => {
            const l = p.latest;
            return (
              <tr key={p.key} className={`cursor-pointer border-b border-hair hover:bg-sel ${sel === p.key ? "bg-sel" : ""}`} onClick={() => setSel(p.key)}>
                <Td><span className="font-mono text-[12px] text-accent">{p.key}</span> <span className="block max-w-72 break-words">{p.title}</span></Td>
                <Td hide="hidden md:table-cell">{p.ev_method ? methodLabel(p.ev_method) : "—"}</Td>
                <Td right mono>{pct(l?.percent_complete, 1)}</Td>
                <Td right mono className={l?.cpi != null && l.cpi < 1 ? "text-danger" : ""}>{ratio(l?.cpi)}</Td>
                <Td right mono className={l?.spi != null && l.spi < 1 ? "text-danger" : ""}>{ratio(l?.spi)}</Td>
                <Td right mono hide="hidden sm:table-cell">{money(l?.eac_cents, cur)}</Td>
              </tr>
            );
          })}
        </Table>
        {ov.can_write && (
          <form className="flex flex-wrap items-end gap-2" onSubmit={(e) => { e.preventDefault(); on.mutate(); }}>
            <Field label={t("budget.fin.attivaSuUnaRichiesta")}><input className="field w-40 font-mono" placeholder="DEM-12" required value={activate} onChange={(e) => setActivate(e.target.value.toUpperCase())} /></Field>
            <button className="btn" disabled={on.isPending}>{t("budget.fin.attiva")}</button>
            <ErrorNote error={on.error} />
          </form>
        )}
      </section>
      {sel && <EVDetail key={sel} refKey={sel} currency={cur} />}
    </>
  );
}

function EVDetail({ refKey, currency }: { refKey: string; currency: string }) {
  const t = useT();
  const p = useQuery({ queryKey: ["finance", "ev", refKey], queryFn: () => financeApi.project(refKey) });
  const [method, setMethod] = useState("");
  const [pc, setPc] = useState("");
  useEffect(() => {
    if (p.data) {
      setMethod(p.data.ev_method ?? "percent_complete");
      setPc(String(p.data.percent_complete));
    }
  }, [p.data]);
  const invalidate = useInvalidate();
  const save = useMutation({
    mutationFn: () => financeApi.saveProject(refKey, { ev_enabled: true, ev_method: method, percent_complete: Number(pc.replace(",", ".")) }),
    onSuccess: invalidate,
  });
  const off = useMutation({ mutationFn: () => financeApi.saveProject(refKey, { ev_enabled: false }), onSuccess: invalidate });
  const snap = useMutation({ mutationFn: () => financeApi.snapshot(refKey), onSuccess: invalidate });
  if (p.error) return <ErrorNote error={p.error} />;
  if (!p.data) return null;
  const d = p.data;
  const l = d.latest;
  const kpis: [string, string, boolean?][] = [
    ["BAC", money(d.bac_cents, currency)],
    ["PV", money(l?.pv_cents, currency)],
    ["EV", money(l?.ev_cents, currency)],
    ["AC", money(l?.ac_cents, currency)],
    ["CPI", ratio(l?.cpi), l?.cpi != null && l.cpi < 1],
    ["SPI", ratio(l?.spi), l?.spi != null && l.spi < 1],
    ["EAC", money(l?.eac_cents, currency), l?.eac_cents != null && l.eac_cents > d.bac_cents],
  ];
  return (
    <section className="flex flex-col gap-4" aria-label={t("budget.fin.evOf", { key: d.key })}>
      <SectionTitle aside={<Link to="/e/$key" params={{ key: d.key }} className="text-[12px] text-accent underline">{t("budget.fin.apriLaRichiesta")}</Link>}>
        {d.key} · {d.title}
      </SectionTitle>
      {d.message && <p className="m-0 text-[12.5px] text-muted">{d.message}</p>}
      <dl className="m-0 grid grid-cols-4 gap-x-6 gap-y-2 sm:grid-cols-7">
        {kpis.map(([k, v, bad]) => (
          <div key={k} className="flex flex-col">
            <dt className="label text-muted">{k}</dt>
            <dd className={`m-0 font-mono text-[15px] font-semibold tabular ${bad ? "text-danger" : ""}`}>{v}</dd>
          </div>
        ))}
      </dl>
      {d.curve && d.curve.length > 0 ? <SCurve project={d} currency={currency} /> : <p className="m-0 text-muted">{t("budget.fin.senzaDatePianificateNon")}</p>}
      {d.snapshots && d.snapshots.length > 0 && (
        <details>
          <summary className="cursor-pointer text-[12.5px] text-muted hover:text-fg">{t("budget.fin.snapshotsTable", { count: d.snapshots.length })}</summary>
          <Table head={<><Th>{t("budget.fin.mese")}</Th><Th right>PV</Th><Th right>EV</Th><Th right>AC</Th><Th right>%</Th><Th right>CPI</Th><Th right>SPI</Th></>}>
            {d.snapshots.map((s) => (
              <tr key={s.period_end} className="border-b border-hair">
                <Td mono>{s.period_end.slice(0, 7)}{s.manual ? <span className="ml-1 font-sans text-[11px] text-muted">{t("budget.fin.manuale")}</span> : null}</Td>
                <Td right mono>{money(s.pv_cents, currency)}</Td>
                <Td right mono>{money(s.ev_cents, currency)}</Td>
                <Td right mono>{money(s.ac_cents, currency)}</Td>
                <Td right mono>{pct(s.percent_complete, 1)}</Td>
                <Td right mono>{ratio(s.cpi)}</Td>
                <Td right mono>{ratio(s.spi)}</Td>
              </tr>
            ))}
          </Table>
        </details>
      )}
      {d.can_edit && (
        <form className="flex flex-wrap items-end gap-3 border-t border-hair pt-3" onSubmit={(e) => { e.preventDefault(); save.mutate(); }}>
          <Field label={t("budget.fin.metodo")}>
            <Select className="field w-auto" value={method} onChange={(e) => setMethod(e.target.value)}>
              {METHOD_KEYS.map((k) => <option key={k} value={k}>{methodLabel(k)}</option>)}
            </Select>
          </Field>
          {method === "percent_complete" && (
            <Field label={t("budget.fin.completamento")}><input className="field w-24 text-right tabular" inputMode="decimal" value={pc} onChange={(e) => setPc(e.target.value)} /></Field>
          )}
          <button className="btn btn-primary" disabled={save.isPending}>{t("budget.fin.salvaEAggiorna")}</button>
          <button type="button" className="btn" disabled={snap.isPending} onClick={() => snap.mutate()}>{t("budget.fin.fotografiaDiOggi")}</button>
          <button type="button" className="btn btn-danger" disabled={off.isPending} onClick={() => off.mutate()}>{t("budget.fin.disattiva")}</button>
          <ErrorNote error={save.error ?? snap.error ?? off.error} />
        </form>
      )}
      <p className="m-0 text-[12px] text-muted">{t("budget.fin.evHelp")}</p>
    </section>
  );
}

// ---------------------------------------------------------------------
// Struttura: categorie, fonti, cambi, impostazioni
// ---------------------------------------------------------------------

function StructureTab({ ov }: { ov: FinOverview }) {
  const t = useT();
  const invalidate = useInvalidate();
  const rates = useQuery({ queryKey: ["finance", "fx"], queryFn: financeApi.exchangeRates });
  const [cat, setCat] = useState({ name: "", code: "", nature: "opex", sourcing: "external" });
  const addCat = useMutation({ mutationFn: () => financeApi.createCategory(cat), onSuccess: () => { invalidate(); setCat({ ...cat, name: "", code: "" }); } });
  const toggle = useMutation({ mutationFn: (c: { id: string; active: boolean }) => financeApi.updateCategory(c.id, { active: c.active }), onSuccess: invalidate });
  const [fs, setFs] = useState("");
  const addFs = useMutation({ mutationFn: () => financeApi.createFundingSource(fs), onSuccess: () => { invalidate(); setFs(""); } });
  const [fx, setFx] = useState({ currency: "USD", valid_on: today(), rate: "" });
  const addFx = useMutation({ mutationFn: () => financeApi.setExchangeRate({ currency: fx.currency, valid_on: fx.valid_on, rate_to_base: Number(fx.rate.replace(",", ".")) }), onSuccess: invalidate });
  const [labor, setLabor] = useState(ov.settings.labor_category_id ?? "");
  const [basis, setBasis] = useState(ov.settings.portfolio_basis);
  const [th, setTh] = useState(ov.settings.alert_thresholds.join(", "));
  const saveSettings = useMutation({
    mutationFn: () =>
      financeApi.saveSettings({
        labor_category_id: labor || undefined,
        portfolio_basis: basis,
        alert_thresholds: th.split(/[,\s]+/).filter(Boolean).map(Number),
      }),
    onSuccess: invalidate,
  });
  const w = ov.can_write;
  return (
    <>
      <BudgetSettingsForm canWrite={w} currency={ov.settings.base_currency} />
      <section className="flex flex-col gap-3">
        <SectionTitle>{t("budget.fin.impostazioni")}</SectionTitle>
        <form className="grid gap-3 sm:grid-cols-4 sm:items-end" onSubmit={(e) => { e.preventDefault(); saveSettings.mutate(); }}>
          <Field label={t("budget.fin.valutaBase")}>
            <input className="field font-mono" disabled value={ov.settings.base_currency} title={t("budget.fin.siSceglieAllaCreazione")} />
          </Field>
          <Field label={t("budget.fin.categoriaDeiCostiDelle")}>
            <Select className="field" disabled={!w} value={labor} onChange={(e) => setLabor(e.target.value)}>
              {ov.categories.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
            </Select>
          </Field>
          <Field label={t("budget.fin.budgetDellaGraduatoria")}>
            <Select className="field" disabled={!w} value={basis} onChange={(e) => setBasis(e.target.value as "envelope" | "residual")}>
              <option value="residual">{t("budget.fin.residualBasis")}</option>
              <option value="envelope">{t("budget.fin.plafondDellEsercizio")}</option>
            </Select>
          </Field>
          <Field label={t("budget.fin.soglieDiAllerta")}><input className="field font-mono" disabled={!w} value={th} onChange={(e) => setTh(e.target.value)} /></Field>
          {w && <button className="btn btn-primary justify-self-start" disabled={saveSettings.isPending}>{t("budget.fin.salva")}</button>}
          <div className="sm:col-span-4"><ErrorNote error={saveSettings.error} /></div>
        </form>
      </section>
      <section className="flex flex-col gap-3">
        <SectionTitle>{t("budget.fin.categorieDiCosto")}</SectionTitle>
        <Table head={<><Th>{t("budget.fin.nome")}</Th><Th>{t("budget.fin.codice")}</Th><Th>{t("budget.fin.natura")}</Th><Th hide="hidden sm:table-cell">{t("budget.fin.provenienza")}</Th><Th /></>}>
          {ov.categories.map((c) => (
            <tr key={c.id} className={`border-b border-hair ${c.active ? "" : "text-muted"}`}>
              <Td>{c.name}</Td>
              <Td mono>{c.code ?? "—"}</Td>
              <Td mono>{c.nature.toUpperCase()}</Td>
              <Td hide="hidden sm:table-cell">{c.sourcing === "internal" ? t("budget.fin.interna") : t("budget.fin.esterna")}</Td>
              <Td right>{w && <button className="text-[12px] text-muted underline hover:text-fg" onClick={() => toggle.mutate({ id: c.id, active: !c.active })}>{c.active ? t("budget.fin.disattiva") : t("budget.fin.reactivate")}</button>}</Td>
            </tr>
          ))}
        </Table>
        {w && (
          <form className="grid gap-2 sm:grid-cols-5 sm:items-end" onSubmit={(e) => { e.preventDefault(); addCat.mutate(); }}>
            <Field label={t("budget.fin.nome")}><input className="field" required value={cat.name} onChange={(e) => setCat({ ...cat, name: e.target.value })} /></Field>
            <Field label={t("budget.fin.codice")}><input className="field font-mono" value={cat.code} onChange={(e) => setCat({ ...cat, code: e.target.value.toUpperCase() })} /></Field>
            <Field label={t("budget.fin.natura")}>
              <Select className="field" value={cat.nature} onChange={(e) => setCat({ ...cat, nature: e.target.value })}><option value="opex">OPEX</option><option value="capex">CAPEX</option></Select>
            </Field>
            <Field label={t("budget.fin.provenienza")}>
              <Select className="field" value={cat.sourcing} onChange={(e) => setCat({ ...cat, sourcing: e.target.value })}><option value="internal">{t("budget.fin.interna")}</option><option value="external">{t("budget.fin.esterna")}</option></Select>
            </Field>
            <button className="btn" disabled={addCat.isPending}>{t("budget.fin.aggiungi")}</button>
            <div className="sm:col-span-5"><ErrorNote error={addCat.error ?? toggle.error} /></div>
          </form>
        )}
      </section>
      <section className="flex flex-col gap-3">
        <SectionTitle>{t("budget.fin.fontiDiFinanziamento")}</SectionTitle>
        <p className="m-0">{ov.funding_sources.map((f) => f.name).join(" · ") || "—"}</p>
        {w && (
          <form className="flex flex-wrap items-end gap-2" onSubmit={(e) => { e.preventDefault(); addFs.mutate(); }}>
            <Field label={t("budget.fin.nuovaFonte")}><input className="field w-64" required value={fs} onChange={(e) => setFs(e.target.value)} /></Field>
            <button className="btn" disabled={addFs.isPending}>{t("budget.fin.aggiungi")}</button>
            <ErrorNote error={addFs.error} />
          </form>
        )}
      </section>
      <section className="flex flex-col gap-3">
        <SectionTitle>{t("budget.fin.fxTitle", { currency: ov.settings.base_currency })}</SectionTitle>
        <p className="m-0 text-[12.5px] text-muted">{t("budget.fin.ogniVoceUsaL")}</p>
        <ErrorNote error={rates.error} />
        <Table head={<><Th>{t("budget.fin.valuta")}</Th><Th>{t("budget.fin.dal")}</Th><Th right>{t("budget.fin.oneUnit")}</Th></>} empty={rates.data && rates.data.items.length === 0 ? t("budget.fin.noFx") : undefined}>
          {rates.data?.items.map((x) => (
            <tr key={x.currency + x.valid_on} className="border-b border-hair">
              <Td mono>{x.currency}</Td>
              <Td mono>{date(x.valid_on)}</Td>
              <Td right mono>{fmtNumber(x.rate_to_base, { maximumFractionDigits: 6 })} {ov.settings.base_currency}</Td>
            </tr>
          ))}
        </Table>
        {w && (
          <form className="flex flex-wrap items-end gap-2" onSubmit={(e) => { e.preventDefault(); addFx.mutate(); }}>
            <Field label={t("budget.fin.valuta")}><input className="field w-20 font-mono uppercase" maxLength={3} required value={fx.currency} onChange={(e) => setFx({ ...fx, currency: e.target.value.toUpperCase() })} /></Field>
            <Field label={t("budget.fin.dal")}><input className="field" type="date" required value={fx.valid_on} onChange={(e) => setFx({ ...fx, valid_on: e.target.value })} /></Field>
            <Field label={t("budget.fin.fxIn", { currency: ov.settings.base_currency })}><input className="field w-28 text-right tabular" inputMode="decimal" required value={fx.rate} onChange={(e) => setFx({ ...fx, rate: e.target.value })} /></Field>
            <button className="btn" disabled={addFx.isPending}>{t("budget.fin.salvaIlCambio")}</button>
            <ErrorNote error={addFx.error} />
          </form>
        )}
      </section>
    </>
  );
}
