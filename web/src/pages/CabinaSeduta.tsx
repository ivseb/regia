import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { portfolioApi, type SedutaItem, type SedutaPackage } from "../api";
import { fmtDate, fmtDateTime, fmtNumber } from "../i18n/format";
import type { Key } from "../i18n/core";
import { useT } from "../i18n/react";
import { OptionSelect } from "../select";
import { ErrorNote, euro } from "../ui";
import { Field } from "./budget/budget-ui";
import "../cabina-print.css";

// Pacchetto della seduta: l'ordine del giorno con importo, punteggio, posizione in graduatoria, esito delle
// valutazioni e requisiti. «Genera verbale» crea la pagina in Knowledge (collegata alle richieste decise);
// «Stampa» manda al PDF del browser solo il pacchetto (@media print, vedi cabina-print.css).

const score = (n: number | null) => (n == null ? "—" : fmtNumber(n, { maximumFractionDigits: 1 }));

export function CabinaSeduta() {
  const t = useT();
  const qc = useQueryClient();
  const meetings = useQuery({ queryKey: ["cabina", "sedute"], queryFn: () => portfolioApi.meetings() });
  const list = (meetings.data?.items ?? []).filter((m) => m.status !== "cancelled");
  const [picked, setPicked] = useState("");
  // Senza scelta: la prima seduta non ancora svolta, altrimenti l'ultima.
  const now = Date.now();
  const sorted = list.slice().sort((a, b) => a.scheduled_at.localeCompare(b.scheduled_at));
  const fallback = (sorted.find((m) => Date.parse(m.scheduled_at) >= now && m.status !== "minuted") ?? sorted[sorted.length - 1])?.id ?? "";
  const id = picked || fallback;
  const pk = useQuery({ queryKey: ["cabina", "seduta", id], queryFn: () => portfolioApi.sedutaPackage(id), enabled: !!id });
  const verbale = useMutation({
    mutationFn: () => portfolioApi.generateVerbale(id),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["cabina", "seduta", id] }),
  });
  const [printing, setPrinting] = useState(false);
  if (meetings.data && list.length === 0) {
    return (
      <section aria-label={t("lavoro.cabina.pack.title")} className="flex flex-col gap-2">
        <h2 className="label m-0 border-b-2 border-rule pb-1.5">{t("lavoro.cabina.pack.title")}</h2>
        <p className="m-0 text-muted">{t("lavoro.cabina.pack.none")}</p>
      </section>
    );
  }
  const d = pk.data;
  return (
    <section aria-label={t("lavoro.cabina.pack.title")} className="flex flex-col gap-3" data-cabina-pack>
      <h2 className="label m-0 border-b-2 border-rule pb-1.5">{t("lavoro.cabina.pack.title")}</h2>
      <ErrorNote error={meetings.error ?? pk.error} />
      <div className="flex flex-wrap items-end gap-3">
        <Field label={t("lavoro.cabina.pack.meeting")} className="w-80 max-w-full">
          <OptionSelect
            value={id}
            onChange={setPicked}
            options={sorted.map((m) => ({ value: m.id, label: `${m.title} · ${fmtDate(m.scheduled_at)}`, detail: m.body }))}
          />
        </Field>
        {d && (
          <>
            <button type="button" className="btn touch-44" onClick={() => setPrinting(true)}>
              {t("lavoro.cabina.pack.print")}
            </button>
            {d.can_manage && (
              <button type="button" className="btn btn-primary touch-44" disabled={verbale.isPending} onClick={() => verbale.mutate()}>
                {t("lavoro.cabina.pack.minutes")}
              </button>
            )}
            <Link to="/governance/sedute/$id" params={{ id: d.id }} className="touch-44 flex items-center text-accent underline">
              {t("lavoro.cabina.pack.open")}
            </Link>
          </>
        )}
      </div>
      <ErrorNote error={verbale.error} />
      {verbale.data && (
        <p className="m-0 border-l-2 border-accent pl-3" role="status" data-verbale>
          {t(verbale.data.created ? "lavoro.cabina.pack.minutesCreated" : "lavoro.cabina.pack.minutesExists", { linked: verbale.data.linked })}{" "}
          <Link to="/documenti/$key" params={{ key: verbale.data.key }} className="font-mono text-accent underline">
            {verbale.data.key}
          </Link>
        </p>
      )}
      {d && <Agenda d={d} />}
      {printing && <PrintPackage d={d!} onDone={() => setPrinting(false)} />}
    </section>
  );
}

function Reviews({ it }: { it: SedutaItem }) {
  const t = useT();
  if (it.reviews.length === 0) return <span className="text-muted">{t("lavoro.cabina.pack.noReviews")}</span>;
  return (
    <ul className="m-0 flex list-none flex-col gap-0.5 p-0">
      {it.reviews.map((r, i) => (
        <li key={i} className={r.polarity === "negative" ? "text-danger" : ""}>
          {r.type}: <span className="font-semibold">{r.outcome ?? t("lavoro.cabina.pack.reviewOpen")}</span>
        </li>
      ))}
    </ul>
  );
}

function Agenda({ d }: { d: SedutaPackage }) {
  const t = useT();
  return (
    <div className="overflow-x-auto">
      <table className="w-full border-collapse tabular" data-cabina-agenda>
        <caption className="sr-only">{t("lavoro.cabina.pack.caption", { title: d.title })}</caption>
        <thead>
          <tr className="label h-9 text-left">
            <th scope="col" className="w-8 pr-2">#</th>
            <th scope="col" className="px-2">{t("budget.rank.col.request")}</th>
            <th scope="col" className="px-2 text-right">{t("lavoro.cabina.pack.amount")}</th>
            <th scope="col" className="px-2 text-right">{t("budget.rank.col.score")}</th>
            <th scope="col" className="px-2 text-right">{t("lavoro.cabina.pack.position")}</th>
            <th scope="col" className="px-2">{t("lavoro.cabina.pack.reviews")}</th>
            <th scope="col" className="px-2">{t("lavoro.cabina.pack.requirements")}</th>
            <th scope="col" className="pl-2">{t("lavoro.cabina.pack.outcome")}</th>
          </tr>
        </thead>
        <tbody>
          {d.items.map((it, i) => (
            <tr key={it.element_id} className="border-b border-hair align-top text-[13px]">
              <td className="py-2 pr-2 font-mono">{i + 1}</td>
              <td className="px-2 py-2">
                <Link to="/e/$key" params={{ key: it.key }} className="font-medium text-fg no-underline hover:underline">
                  {it.title}
                </Link>
                <span className="block font-mono text-[11.5px] text-accent">
                  {it.key}
                  {it.is_mandatory ? ` · ${t("budget.rank.row.mandatory")}` : ""}
                </span>
              </td>
              <td className="px-2 py-2 text-right font-mono whitespace-nowrap">{euro(it.amount_cents)}</td>
              <td className="px-2 py-2 text-right font-mono">{score(it.score)}</td>
              <td className="px-2 py-2 text-right font-mono">{it.position ?? "—"}</td>
              <td className="px-2 py-2"><Reviews it={it} /></td>
              <td className="px-2 py-2 font-mono whitespace-nowrap">
                {it.requirements.total > 0 ? t("lavoro.cabina.pack.reqOf", { done: it.requirements.verified, total: it.requirements.total }) : "—"}
              </td>
              <td className="py-2 pl-2">
                {it.outcome ? <span className="font-semibold">{t(`lavoro.governance.outcome.${it.outcome}` as Key)}</span> : <span className="text-muted">{t("lavoro.cabina.pack.pending")}</span>}
              </td>
            </tr>
          ))}
          {d.items.length === 0 && (
            <tr>
              <td colSpan={8} className="py-3 text-muted">{t("lavoro.cabina.pack.emptyAgenda")}</td>
            </tr>
          )}
        </tbody>
        {d.items.length > 0 && (
          <tfoot>
            <tr className="text-[13px] font-semibold">
              <td colSpan={2} className="py-2 pr-2">{t("lavoro.cabina.pack.total")}</td>
              <td className="px-2 py-2 text-right font-mono">{euro(d.total_cents)}</td>
              <td colSpan={5} />
            </tr>
          </tfoot>
        )}
      </table>
    </div>
  );
}

/** Il pacchetto da stampare sta in #print-root, fuori dall'app (print.css nasconde il resto). */
function PrintPackage({ d, onDone }: { d: SedutaPackage; onDone: () => void }) {
  const t = useT();
  const [root] = useState(() => {
    const el = document.createElement("div");
    el.id = "print-root";
    el.dataset.skin = document.documentElement.dataset.skin ?? "atlante";
    el.dataset.font = document.documentElement.dataset.font ?? "atkinson";
    el.dataset.theme = "light";
    return el;
  });
  useEffect(() => {
    document.body.appendChild(root);
    const prev = document.title;
    document.title = d.title;
    const done = () => onDone();
    window.addEventListener("afterprint", done);
    const timer = window.setTimeout(() => window.print(), 150);
    return () => {
      window.clearTimeout(timer);
      window.removeEventListener("afterprint", done);
      document.title = prev;
      root.remove();
    };
  }, [root, onDone, d.title]);
  return createPortal(
    <div className="cabina-print">
      <header>
        <h1>{d.title}</h1>
        <p>
          {d.body} · {fmtDateTime(d.scheduled_at)}
          {d.location ? ` · ${d.location}` : ""}
        </p>
        <p>{t("lavoro.cabina.pack.printHead", { count: d.items.length, total: euro(d.total_cents) })}</p>
      </header>
      <table>
        <thead>
          <tr>
            <th>#</th>
            <th>{t("budget.rank.col.request")}</th>
            <th>{t("lavoro.cabina.pack.amount")}</th>
            <th>{t("budget.rank.col.score")}</th>
            <th>{t("lavoro.cabina.pack.position")}</th>
            <th>{t("lavoro.cabina.pack.reviews")}</th>
            <th>{t("lavoro.cabina.pack.requirements")}</th>
            <th>{t("lavoro.cabina.pack.outcome")}</th>
          </tr>
        </thead>
        <tbody>
          {d.items.map((it, i) => (
            <tr key={it.element_id}>
              <td>{i + 1}</td>
              <td>
                <strong>{it.key}</strong> {it.title}
              </td>
              <td>{euro(it.amount_cents)}</td>
              <td>{score(it.score)}</td>
              <td>{it.position ?? "—"}</td>
              <td>{it.reviews.length ? it.reviews.map((r) => `${r.type}: ${r.outcome ?? t("lavoro.cabina.pack.reviewOpen")}`).join("; ") : "—"}</td>
              <td>{it.requirements.total > 0 ? t("lavoro.cabina.pack.reqOf", { done: it.requirements.verified, total: it.requirements.total }) : "—"}</td>
              <td>{it.outcome ? t(`lavoro.governance.outcome.${it.outcome}` as Key) : t("lavoro.cabina.pack.pending")}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>,
    root,
  );
}
