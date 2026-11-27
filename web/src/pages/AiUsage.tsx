// Configurazione → Consumo dell'AI (replica/ai.md §1 e §4): il registro delle chiamate del mese,
// con il budget, i totali per funzione, persona e modello e le ultime chiamate (hash del prompt,
// mai il contenuto). Solo per chi amministra l'organizzazione.
import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useQuery } from "@tanstack/react-query";
import { ApiError } from "../api";
import { aiApi, usd, type UsageRow } from "../aiApi";
import { PageHeader } from "../shell";
import { ErrorNote, Loading, SectionTitle } from "../ui";
import { useT } from "../i18n/react";
import { lookup } from "../i18n/core";
import { fmtDateTime, fmtNumber } from "../i18n/format";

const featureName = (k: string) => lookup(`ai.feature.${k}`) ?? k;

function Table({ title, rows, label }: { title: string; rows: UsageRow[]; label: (r: UsageRow) => string }) {
  const t = useT();
  return (
    <section className="flex flex-col gap-2" aria-label={title}>
      <SectionTitle>{title}</SectionTitle>
      {rows.length === 0 ? (
        <p className="m-0 text-muted">{t("ai.usage.none")}</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full border-collapse text-[13px]">
            <thead>
              <tr className="border-b border-rule text-left">
                <th className="py-1.5 pr-3 font-semibold">{t("ai.usage.col.name")}</th>
                <th className="py-1.5 pr-3 text-right font-semibold">{t("ai.usage.col.calls")}</th>
                <th className="py-1.5 pr-3 text-right font-semibold">{t("ai.usage.col.tokens")}</th>
                <th className="py-1.5 pr-3 text-right font-semibold">{t("ai.usage.col.cost")}</th>
                <th className="py-1.5 text-right font-semibold">{t("ai.usage.col.errors")}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.key} className="border-b border-hair">
                  <td className="py-1.5 pr-3 [overflow-wrap:anywhere]">{label(r)}</td>
                  <td className="py-1.5 pr-3 text-right tabular">{fmtNumber(r.calls)}</td>
                  <td className="py-1.5 pr-3 text-right tabular">{fmtNumber(r.input_tokens + r.output_tokens)}</td>
                  <td className="py-1.5 pr-3 text-right tabular">{usd(r.cost_usd)}</td>
                  <td className="py-1.5 text-right tabular">{fmtNumber(r.errors)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}

export function AiUsage() {
  const t = useT();
  const [month, setMonth] = useState("");
  const usage = useQuery({ queryKey: ["ai-usage", month], queryFn: () => aiApi.usage(month || undefined) });
  if (usage.error instanceof ApiError && usage.error.status === 403) {
    return (
      <>
        <PageHeader path={t("ai.usage.path")} title={t("ai.usage.title")} />
        <p className="px-4 text-muted md:px-7">{t("ai.cfg.adminOnly")}</p>
      </>
    );
  }
  const u = usage.data;
  const b = u?.budget;
  return (
    <>
      <PageHeader path={t("ai.usage.path")} title={t("ai.usage.title")} actions={<Link to="/configurazione/ai" className="btn">{t("ai.usage.settingsLink")}</Link>} />
      <div className="flex max-w-[960px] flex-col gap-6 px-4 pb-10 md:px-7">
        <p className="m-0 text-muted">{t("ai.usage.intro")}</p>
        <label className="flex max-w-xs flex-col gap-1">
          <span className="label">{t("ai.usage.month")}</span>
          <input className="field" type="month" value={month || u?.month || ""} onChange={(e) => setMonth(e.target.value)} />
        </label>
        {usage.isPending && <Loading />}
        <ErrorNote error={usage.error} />
        {u && b && (
          <>
            <div className="grid grid-cols-2 gap-3 md:grid-cols-4" data-testid="consumo-totali">
              {[
                [t("ai.usage.calls"), fmtNumber(u.total.calls)],
                [t("ai.usage.tokens"), fmtNumber(u.total.input_tokens + u.total.output_tokens)],
                [t("ai.usage.cost"), usd(u.total.cost_usd)],
                [t("ai.usage.errors"), fmtNumber(u.total.errors)],
              ].map(([k, val]) => (
                <div key={k} className="flex flex-col gap-1 border border-hair p-3">
                  <span className="label">{k}</span>
                  <span className="text-[22px] font-bold tabular">{val}</span>
                </div>
              ))}
            </div>
            {b.month === u.month && (
              <p className={`m-0 ${b.state === "blocked" ? "font-semibold text-danger" : ""}`} role="status">
                {b.state === "none"
                  ? t("ai.usage.noBudget")
                  : t(`ai.budget.${b.state}`, { percent: fmtNumber(Math.floor(b.percent)) })}
              </p>
            )}
            <Table title={t("ai.usage.byFeature")} rows={u.by_feature} label={(r) => featureName(r.label)} />
            <Table title={t("ai.usage.byMember")} rows={u.by_member} label={(r) => r.label || t("ai.usage.nobody")} />
            <Table title={t("ai.usage.byModel")} rows={u.by_model} label={(r) => r.key} />
            <section className="flex flex-col gap-2" aria-label={t("ai.usage.recent")}>
              <SectionTitle>{t("ai.usage.recent")}</SectionTitle>
              {u.recent.length === 0 ? (
                <p className="m-0 text-muted">{t("ai.usage.none")}</p>
              ) : (
                <div className="overflow-x-auto">
                  <table className="w-full border-collapse text-[12.5px]" data-testid="consumo-chiamate">
                    <thead>
                      <tr className="border-b border-rule text-left">
                        <th className="py-1.5 pr-3 font-semibold">{t("ai.usage.col.when")}</th>
                        <th className="py-1.5 pr-3 font-semibold">{t("ai.usage.col.who")}</th>
                        <th className="py-1.5 pr-3 font-semibold">{t("ai.usage.col.feature")}</th>
                        <th className="py-1.5 pr-3 font-semibold">{t("ai.usage.col.model")}</th>
                        <th className="py-1.5 pr-3 text-right font-semibold">{t("ai.usage.col.tokens")}</th>
                        <th className="py-1.5 pr-3 text-right font-semibold">{t("ai.usage.col.cost")}</th>
                        <th className="py-1.5 pr-3 font-semibold">{t("ai.usage.col.outcome")}</th>
                        <th className="py-1.5 font-semibold">{t("ai.usage.col.hash")}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {u.recent.map((c) => (
                        <tr key={c.id} className="border-b border-hair align-top">
                          <td className="py-1.5 pr-3 whitespace-nowrap">{fmtDateTime(c.at)}</td>
                          <td className="py-1.5 pr-3">{c.member || t("ai.usage.nobody")}</td>
                          <td className="py-1.5 pr-3">{featureName(c.feature)}</td>
                          <td className="py-1.5 pr-3 font-mono [overflow-wrap:anywhere]">{c.model}</td>
                          <td className="py-1.5 pr-3 text-right tabular">{fmtNumber(c.input_tokens + c.output_tokens)}</td>
                          <td className="py-1.5 pr-3 text-right tabular">{usd(c.cost_usd)}</td>
                          <td className="py-1.5 pr-3">
                            {lookup(`ai.outcome.${c.outcome}`) ?? c.outcome}
                            {c.error_code ? ` (${c.error_code})` : ""}
                          </td>
                          <td className="py-1.5 font-mono text-muted" title={c.prompt_sha256}>{c.prompt_sha256.slice(0, 10)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </section>
          </>
        )}
      </div>
    </>
  );
}
