// Blocco «Codice» nella barra laterale della scheda di un elemento: i commit che ne citano la
// chiave, con il rilascio che li contiene e il lead time dal primo commit. Compare solo se ci
// sono commit: un blocco vuoto non dice niente (e senza il modulo DevSecOps non c'è nulla).
import { useQuery } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { commitsApi } from "../commitsApi";
import { ago, date } from "../ui";
import { useT } from "../i18n/react";
import { ModuleChip } from "../moduleIcon";
import { fmtNumber } from "../i18n/format";

export default function CodiceSection({ refKey, open = true }: { refKey: string; open?: boolean }) {
  const t = useT();
  const q = useQuery({ queryKey: ["element-commits", refKey], queryFn: () => commitsApi.of(refKey), staleTime: 60_000, retry: false });
  const data = q.data;
  if (!data || data.commits.length === 0) return null;
  const rel = data.release;
  const days = rel ? fmtNumber(Math.round(rel.lead_days * 10) / 10) : "";
  return (
    <details open={open} className="group flex flex-col" data-testid="sezione-codice">
      <summary className="flex cursor-pointer list-none items-baseline justify-between border-b-2 border-rule pb-1.5 [&::-webkit-details-marker]:hidden">
        <span className="label m-0">
          <span aria-hidden="true" className="mr-1.5 inline-block w-2 text-muted group-open:rotate-90">›</span>
          <ModuleChip module="devsecops" size="sm" className="mr-1.5 align-middle" />
          {t("lavoro.codice.titolo")}
        </span>
        <span className="font-mono text-[11.5px] text-muted tabular">{t("lavoro.codice.commit", { n: data.commits.length })}</span>
      </summary>
      <div className="flex flex-col gap-2 pt-2">
        <p className="m-0 text-[13px] text-muted">
          {rel
            ? rel.lead_days < 1
              ? t("lavoro.codice.rilasciatoOggi", { versione: rel.version, data: date(rel.at) })
              : t("lavoro.codice.rilasciato", { versione: rel.version, data: date(rel.at), giorni: days })
            : t("lavoro.codice.nonRilasciato")}
          {rel?.release_id && (
            <>
              {" "}
              <Link to="/rilasci/$id" params={{ id: rel.release_id }} data-testid="link-rilascio">
                {t("rilasci.apri_rilascio")}
              </Link>
            </>
          )}
        </p>
        <ul className="m-0 flex list-none flex-col p-0">
          {data.commits.map((c) => (
            <li key={c.sha} className="flex flex-col gap-0.5 border-b border-hair py-2">
              <div className="flex flex-wrap items-baseline gap-x-2">
                {c.url ? (
                  <a href={c.url} target="_blank" rel="noopener noreferrer" className="font-mono text-[12px]" aria-label={t("lavoro.codice.apri", { sha: c.sha.slice(0, 7) })}>
                    {c.sha.slice(0, 7)}
                  </a>
                ) : (
                  <span className="font-mono text-[12px]">{c.sha.slice(0, 7)}</span>
                )}
                {c.source === "pull_request" && c.pr_number != null && <span className="font-mono text-[11.5px] text-muted">{t("lavoro.codice.pullRequest", { n: c.pr_number })}</span>}
              </div>
              <span className="min-w-0 break-words text-[13px]">{c.title}</span>
              <span className="text-[12px] text-muted">{[c.author, ago(c.at), c.repository].filter(Boolean).join(" · ")}</span>
            </li>
          ))}
        </ul>
      </div>
    </details>
  );
}
