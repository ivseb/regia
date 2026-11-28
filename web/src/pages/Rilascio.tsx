// Pagina di un rilascio (/rilasci/$id): gli elementi che contiene, cioè i commit dello stesso
// servizio fra il rilascio precedente nello stesso ambiente e questo, e le note di rilascio
// generate, raggruppate in novità, correzioni e altro. Le note si copiano o si scaricano in
// Markdown; con l'AI accesa (funzione Bozze) «Riscrivi per il business» propone un testo da
// rileggere, mai salvato.
import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { rilasciApi, type ReleaseDetail, type ReleaseItem } from "../rilasciApi";
import { NOTE_GROUPS, notesFileName, releaseNotesMarkdown } from "../releaseNotes";
import { aiApi } from "../aiApi";
import { PageHeader } from "../shell";
import { ErrorNote, Loading, SectionTitle, date } from "../ui";
import { Markdown } from "../markdown";
import { useT } from "../i18n/react";
import { crumbs } from "../crumbs";

const GROUPS = NOTE_GROUPS;

function download(name: string, text: string) {
  const url = URL.createObjectURL(new Blob([text], { type: "text/markdown;charset=utf-8" }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  document.body.appendChild(a);
  a.click();
  a.remove();
  URL.revokeObjectURL(url);
}

function fileName(d: ReleaseDetail, suffix = ""): string {
  return notesFileName(d.release.service, d.release.version, suffix);
}

function CopyButton({ text, testId }: { text: string; testId?: string }) {
  const t = useT();
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      className="btn"
      data-testid={testId}
      onClick={() => {
        void navigator.clipboard?.writeText(text).then(() => {
          setDone(true);
          window.setTimeout(() => setDone(false), 2000);
        });
      }}
    >
      {done ? t("rilasci.copiato") : t("rilasci.copia")}
    </button>
  );
}

export function ReleasePage({ id }: { id: string }) {
  const t = useT();
  const q = useQuery({ queryKey: ["release", id], queryFn: () => rilasciApi.release(id), retry: false });
  const d = q.data;
  return (
    <>
      <PageHeader path={crumbs("devsecops")} title={d ? `${d.release.service} ${d.release.version}` : t("rilasci.titolo")} />
      <div className="flex max-w-4xl flex-col gap-8 px-4 pb-10 md:px-7">
        {q.isPending && <Loading className="m-0 text-muted" />}
        {q.error != null && <ErrorNote error={q.error} />}
        {d && <ReleaseBody d={d} />}
      </div>
    </>
  );
}

function ReleaseBody({ d }: { d: ReleaseDetail }) {
  const t = useT();
  const r = d.release;
  const w = d.window;
  return (
    <>
      <section className="flex flex-col gap-1.5" aria-label={t("rilasci.dettagli")}>
        <p className="m-0 flex flex-wrap items-baseline gap-x-3 gap-y-1 text-[14px]">
          <span className={r.production ? "font-semibold" : "text-muted"}>{r.environment}</span>
          <span className="tabular">{date(r.at)}</span>
          {r.application && <span className="text-muted">{r.application}</span>}
          {r.approved_by && <span className="text-muted">{t("rilasci.approvato_da", { nome: r.approved_by })}</span>}
          {r.release_url && (
            <a href={r.release_url} target="_blank" rel="noopener noreferrer">
              {t("rilasci.apri_origine")}
            </a>
          )}
        </p>
        {r.change && (
          <p className="m-0 text-[13px]">
            <span className="font-mono">{r.change.number}</span>
            {r.change.request_key && (
              <>
                {" · "}
                <Link to="/e/$key" params={{ key: r.change.request_key }}>
                  {r.change.request_key} {r.change.request_title}
                </Link>
              </>
            )}
            {" · "}
            <Link to="/change/$numero/certificazione" params={{ numero: r.change.number }} data-testid="link-certificazione">
              {t("rilasci.certificazione")}
            </Link>
          </p>
        )}
        <p className="m-0 text-[13px] text-muted" data-testid="finestra-rilascio">
          {w.from
            ? t("rilasci.finestra", { da: date(w.from), precedente: w.previous_version ?? "—", a: date(w.to) })
            : t("rilasci.finestra_prima", { a: date(w.to) })}
        </p>
      </section>
      <Items d={d} />
      <Notes d={d} />
    </>
  );
}

function Items({ d }: { d: ReleaseDetail }) {
  const t = useT();
  return (
    <section className="flex min-w-0 flex-col gap-3" aria-label={t("rilasci.elementi")} data-testid="elementi-rilascio">
      <SectionTitle aside={<span className="font-mono text-[12px] text-muted tabular">{t("rilasci.conteggio", { n: d.items.length, commit: d.commits })}</span>}>
        {t("rilasci.elementi")}
      </SectionTitle>
      {d.items.length === 0 && <p className="m-0 max-w-2xl text-muted">{t("rilasci.nessun_elemento")}</p>}
      {GROUPS.map((g) => {
        const items = d.items.filter((i) => i.group === g);
        if (items.length === 0) return null;
        return (
          <div key={g} className="flex flex-col gap-1">
            <h3 className="label m-0">{t(`rilasci.gruppo.${g}`)}</h3>
            <ul className="m-0 flex list-none flex-col p-0">
              {items.map((i) => <ItemRow key={i.id} i={i} />)}
            </ul>
          </div>
        );
      })}
      {d.hidden > 0 && <p className="m-0 text-[12.5px] text-muted">{t("rilasci.nascosti", { n: d.hidden })}</p>}
    </section>
  );
}

function ItemRow({ i }: { i: ReleaseItem }) {
  const t = useT();
  return (
    <li className="grid grid-cols-[minmax(0,1fr)_auto] items-baseline gap-x-3 gap-y-0.5 border-b border-hair py-2 sm:grid-cols-[90px_minmax(0,1fr)_auto]">
      <Link to="/e/$key" params={{ key: i.key }} className="font-mono text-[12.5px]">
        {i.key}
      </Link>
      <span className="col-span-2 min-w-0 break-words text-[13.5px] sm:col-span-1 sm:row-start-1 sm:col-start-2">
        {i.title}
        {i.continues && <span className="ml-2 text-[12px] text-muted">{t("rilasci.continua")}</span>}
      </span>
      <span className="text-right text-[12px] text-muted sm:row-start-1 sm:col-start-3">
        {i.type_name}
        {i.state ? ` · ${i.state}` : ""}
      </span>
    </li>
  );
}

function Notes({ d }: { d: ReleaseDetail }) {
  const t = useT();
  const md = releaseNotesMarkdown(d, (k, p) => t(k as Parameters<typeof t>[0], p), date);
  const status = useQuery({ queryKey: ["ai-status"], queryFn: aiApi.status, staleTime: 60_000, retry: false });
  const st = status.data;
  const canRewrite = !!st?.enabled && !!st.features?.bozze && st.budget.state !== "blocked" && d.items.length > 0;
  const [hint, setHint] = useState("");
  const rewrite = useMutation({ mutationFn: () => rilasciApi.businessNotes(d.release.id, hint) });
  return (
    <section className="flex min-w-0 flex-col gap-3" aria-label={t("rilasci.note.titolo")} data-testid="note-rilascio">
      <SectionTitle
        aside={
          <span className="flex flex-wrap gap-2">
            <CopyButton text={md} testId="copia-note" />
            <button type="button" className="btn" onClick={() => download(fileName(d), md)}>
              {t("rilasci.scarica")}
            </button>
          </span>
        }
      >
        {t("rilasci.note.titolo")}
      </SectionTitle>
      <pre className="m-0 max-h-[420px] overflow-auto whitespace-pre-wrap break-words border border-hair bg-side p-3 font-mono text-[12.5px]" data-testid="note-markdown">
        {md}
      </pre>
      {canRewrite && (
        <div className="flex flex-col gap-2" data-testid="note-business">
          <div className="flex flex-wrap items-end gap-2">
            <label className="flex min-w-[220px] flex-1 flex-col gap-1 text-[12.5px]">
              <span className="text-muted">{t("rilasci.business.indicazioni")}</span>
              <input value={hint} maxLength={500} onChange={(e) => setHint(e.target.value)} placeholder={t("rilasci.business.esempio")} />
            </label>
            <button type="button" className="btn" disabled={rewrite.isPending} onClick={() => rewrite.mutate()}>
              {rewrite.isPending ? t("rilasci.business.in_corso") : t("rilasci.business.riscrivi")}
            </button>
          </div>
          <ErrorNote error={rewrite.error} />
          {rewrite.data && (
            <div className="flex flex-col gap-2 border border-hair p-3" data-testid="proposta-business">
              <p className="label m-0">{t("rilasci.business.proposta")}</p>
              <div className="text-[13.5px]">
                <Markdown text={rewrite.data.text} />
              </div>
              <div className="flex flex-wrap gap-2">
                <CopyButton text={rewrite.data.text} />
                <button type="button" className="btn" onClick={() => download(fileName(d, "-business"), rewrite.data.text + "\n")}>
                  {t("rilasci.scarica")}
                </button>
                <button type="button" className="btn" onClick={() => rewrite.reset()}>
                  {t("rilasci.business.scarta")}
                </button>
              </div>
            </div>
          )}
        </div>
      )}
    </section>
  );
}
