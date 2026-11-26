import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Link } from "@tanstack/react-router";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { kb, type ImportReport, type PageDetail, type RefInfo } from "../knowledgeApi";
import { PageMarkdown } from "../kbMarkdown";
import { ErrorNote } from "../ui";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import { fmtDate } from "../i18n/format";
import { Dialog } from "./work-ui";
import "../print.css";

// Export e import di Markdown (replica/knowledge.md): lo spazio o una pagina con le sue
// sottopagine in uno zip con gli allegati e i collegamenti relativi; l'import legge lo stesso
// formato (e cartelle di Markdown scritte altrove) e mostra prima l'anteprima.
// La stampa in PDF usa il browser: il documento finisce in #print-root con il suo CSS.

export function ExportDialog({ space, page, onClose }: { space: string; page?: { key: string; title: string }; onClose: () => void }) {
  const t = useT();
  const [scope, setScope] = useState<"page" | "space">(page ? "page" : "space");
  const [approved, setApproved] = useState(false);
  const [attachments, setAttachments] = useState(true);
  const href = kb.exportUrl(space, { page: scope === "page" ? page?.key : undefined, approved, attachments });
  return (
    <Dialog title={t("knowledge.transfer.exportTitle")} onClose={onClose} wide>
      <div className="flex flex-col gap-3">
        <p className="m-0 text-[12.5px] text-muted">
          {/* i18n-ok: nome fisso della cartella nello zip */}
          <Trans k="knowledge.transfer.exportIntro" slots={{ dir: <code className="font-mono">_allegati/</code> }} />
        </p>
        {page && (
          <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
            <legend className="label mb-1">{t("knowledge.transfer.scope")}</legend>
            <label className="flex items-center gap-2">
              <input type="radio" name="scope" checked={scope === "page"} onChange={() => setScope("page")} />
              <span>{t("knowledge.transfer.scopePage", { title: page.title })}</span>
            </label>
            <label className="flex items-center gap-2">
              <input type="radio" name="scope" checked={scope === "space"} onChange={() => setScope("space")} />
              {t("knowledge.transfer.scopeSpace", { space })}
            </label>
          </fieldset>
        )}
        <label className="flex items-start gap-2">
          <input type="checkbox" className="mt-1" checked={approved} onChange={(e) => setApproved(e.target.checked)} />
          <span>
            {t("knowledge.transfer.approvedOnly")}
            <span className="block text-[12.5px] text-muted">{t("knowledge.transfer.approvedOnlyHint")}</span>
          </span>
        </label>
        <label className="flex items-center gap-2">
          <input type="checkbox" checked={attachments} onChange={(e) => setAttachments(e.target.checked)} />
          {t("knowledge.transfer.includeAttachments")}
        </label>
        <div className="flex justify-end gap-2">
          <button type="button" className="btn" onClick={onClose}>{t("knowledge.transfer.close")}</button>
          <a className="btn btn-primary no-underline" href={href} download onClick={() => window.setTimeout(onClose, 400)}>
            {t("knowledge.transfer.download")}
          </a>
        </div>
      </div>
    </Dialog>
  );
}

export function ImportDialog({ space, parent, onClose }: { space: string; parent?: { key: string; title: string }; onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const input = useRef<HTMLInputElement>(null);
  const [file, setFile] = useState<File | null>(null);
  const [under, setUnder] = useState(!!parent);
  const [report, setReport] = useState<ImportReport | null>(null);
  const [done, setDone] = useState(false);
  const run = useMutation({
    mutationFn: (x: { f: File; dry: boolean }) => kb.importZip(space, x.f, { dry: x.dry, parent: under ? parent?.key : undefined }),
    onSuccess: (r) => {
      setReport(r);
      if (!r.dry) {
        setDone(true);
        qc.invalidateQueries({ queryKey: ["kb"] });
      }
    },
  });
  function pick(f: File | null) {
    setFile(f);
    setReport(null);
    setDone(false);
    if (f) run.mutate({ f, dry: true });
  }
  return (
    <Dialog title={t("knowledge.transfer.importTitle")} onClose={onClose} wide>
      <div className="flex flex-col gap-3">
        <p className="m-0 text-[12.5px] text-muted">
          {t("knowledge.transfer.importIntro")}
        </p>
        <div className="flex flex-wrap items-center gap-3">
          <input ref={input} type="file" accept=".zip,application/zip" className="sr-only" aria-label={t("knowledge.transfer.zipLabel")} onChange={(e) => pick(e.target.files?.[0] ?? null)} />
          <button type="button" className="btn" disabled={run.isPending || done} onClick={() => input.current?.click()}>
            {file ? t("knowledge.transfer.pickOther") : t("knowledge.transfer.pickZip")}
          </button>
          {file && <span className="min-w-0 break-words text-[12.5px] text-muted">{file.name} · {(file.size / 1024).toFixed(file.size > 1048576 ? 0 : 1)} kB</span>}
        </div>
        {parent && (
          <label className="flex items-center gap-2">
            <input type="checkbox" checked={under} disabled={done} onChange={(e) => { setUnder(e.target.checked); if (file) setReport(null); }} />
            <span>{t("knowledge.transfer.putUnder", { title: parent.title })}</span>
          </label>
        )}
        {run.isPending && <p className="m-0 text-muted" role="status">{done ? t("knowledge.transfer.importing") : t("knowledge.transfer.reading")}</p>}
        <ErrorNote error={run.error} />
        {report && (
          <div className="flex flex-col gap-2" aria-live="polite">
            <p className="m-0 font-semibold">
              {report.dry ? t("knowledge.transfer.preview") : done ? t("knowledge.transfer.imported") : ""}
              {t("knowledge.transfer.pages", { count: report.pages })}
              {report.attachments > 0 && `, ${t("knowledge.transfer.attachments", { count: report.attachments })}`}
              {report.ignored > 0 && <span className="font-normal text-muted"> · {t("knowledge.transfer.ignored", { count: report.ignored })}</span>}
            </p>
            <ul className="m-0 flex max-h-[220px] list-none flex-col overflow-y-auto border border-hair p-0 text-[13px]">
              {report.items.map((it) => (
                <li key={it.path} className="flex items-baseline gap-2 border-b border-hair px-2 py-1 last:border-b-0" style={{ paddingLeft: 8 + it.level * 16 }}>
                  {it.key ? (
                    <Link to="/documenti/$key" params={{ key: it.key }} className="min-w-0 break-words text-accent no-underline hover:underline" onClick={onClose}>{it.title}</Link>
                  ) : (
                    <span className="min-w-0 break-words">{it.title}</span>
                  )}
                  {it.synthetic && <span className="shrink-0 text-[11.5px] text-muted">{t("knowledge.transfer.folder")}</span>}
                  {it.attachments > 0 && <span className="shrink-0 text-[11.5px] text-muted">{t("knowledge.transfer.attShort", { count: it.attachments })}</span>}
                  {it.key && <span className="ml-auto shrink-0 font-mono text-[11.5px] text-muted">{it.key}</span>}
                </li>
              ))}
            </ul>
            {report.warnings.length > 0 && (
              <ul className="m-0 pl-5 text-[12.5px] text-flag">
                {report.warnings.slice(0, 8).map((w) => <li key={w}>{w}</li>)}
                {report.warnings.length > 8 && <li>{t("knowledge.transfer.more", { count: report.warnings.length - 8 })}</li>}
              </ul>
            )}
          </div>
        )}
        <div className="flex justify-end gap-2">
          <button type="button" className="btn" onClick={onClose}>{done ? t("knowledge.transfer.close") : t("knowledge.transfer.cancel")}</button>
          {file && report?.dry && !done && (
            <button className="btn btn-primary" disabled={run.isPending || report.pages === 0} onClick={() => run.mutate({ f: file, dry: false })}>
              {t("knowledge.transfer.importN", { count: report.pages })}
            </button>
          )}
        </div>
      </div>
    </Dialog>
  );
}

/**
 * Stampa o PDF: il documento va in #print-root, fuori dall'app; print.css nasconde il resto,
 * usa sempre il tema chiaro e mette in ogni pagina l'intestazione del documento e il piè con
 * i numeri di pagina (dove il browser supporta i riquadri @page).
 */
export function PagePrint({
  d,
  title,
  body,
  showApproved,
  refs,
  onDone,
}: {
  d: PageDetail;
  title: string;
  body: string;
  showApproved: boolean;
  refs?: RefInfo[];
  onDone: () => void;
}) {
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
    const prevTitle = document.title;
    document.title = `${d.page.key} ${title}`; // il nome del PDF salvato
    const done = () => onDone();
    window.addEventListener("afterprint", done);
    const t = window.setTimeout(() => window.print(), 150);
    return () => {
      window.clearTimeout(t);
      window.removeEventListener("afterprint", done);
      document.title = prevTitle;
      root.remove();
    };
  }, [root, onDone, d.page.key, title]);
  const p = d.page;
  const approved = d.approved;
  const today = fmtDate(new Date());
  const fmt = (iso: string | null | undefined) => (iso ? fmtDate(iso) : "");
  const version = showApproved && approved ? approved.version : p.version;
  const state = p.controlled
    ? showApproved && approved
      ? `${t("knowledge.transfer.approvedState")}${approved.approved_at ? ` ${t("knowledge.transfer.onDate", { date: fmt(approved.approved_at) })}` : ""}${approved.approved_by ? ` ${t("knowledge.transfer.byWho", { name: approved.approved_by })}` : ""}`
      : t("knowledge.transfer.workingVersion", { state: p.state || t("knowledge.transfer.draft") })
    : t("knowledge.transfer.uncontrolled");
  return createPortal(
    <div className="kb-print">
      <header className="kb-print-head">
        <span className="font-mono text-[10px]">{p.space_name} · {p.key}</span>
        <h1>{title}</h1>
        <dl>
          <div><dt>{t("knowledge.transfer.version")}</dt><dd>v{version}</dd></div>
          <div><dt>{t("knowledge.transfer.state")}</dt><dd>{state}</dd></div>
          {p.controlled && d.approvers.length > 0 && <div><dt>{t("knowledge.transfer.approvers")}</dt><dd>{d.approvers.map((a) => a.name).join(", ")}</dd></div>}
          {p.controlled && p.next_review && <div><dt>{t("knowledge.transfer.nextReview")}</dt><dd>{fmtDate(new Date(p.next_review + "T00:00:00"))}</dd></div>}
          <div><dt>{t("knowledge.transfer.updated")}</dt><dd>{fmt(p.updated_at)}{p.updated_by ? ` · ${p.updated_by}` : ""}</dd></div>
        </dl>
      </header>
      <PageMarkdown text={body} mentions={p.mentions} refs={refs} />
      <footer className="kb-print-foot">
        {/* i18n-ok: nome del prodotto */}
        <span>Regia · {p.key} · v{version}{p.controlled ? (showApproved && approved ? ` · ${t("knowledge.transfer.footApproved")}` : ` · ${t("knowledge.transfer.footNotApproved")}`) : ""}</span>
        <span>{t("knowledge.transfer.printed", { date: today })}</span>
      </footer>
    </div>,
    root,
  );
}
