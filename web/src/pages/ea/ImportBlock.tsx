// Import ed export del catalogo esteso (replica/architettura-ea.md §7): CSV di capability o
// dipendenze, catalogo completo in JSON. Prima una prova senza salvare, poi la conferma.
import { useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { ErrorNote } from "../../ui";
import { eaApi, type EAImportReport } from "../../eaApi";
import { useT } from "../../i18n/react";
import { Trans } from "../../i18n/Trans";

const COLUMNS: Record<"capabilities" | "dependencies", string> = {
  // i18n-ok: intestazioni del file CSV di scambio, uguali in ogni lingua
  capabilities: "codice; nome; padre; criticita; descrizione; applicazioni; asset",
  // i18n-ok: intestazioni del file CSV di scambio, uguali in ogni lingua
  dependencies: "origine; destinazione; tipo; protocollo; criticita; dati; classificazione",
};

export function ImportBlock({ kind, canWrite }: { kind: "capabilities" | "dependencies"; canWrite: boolean }) {
  const t = useT();
  const qc = useQueryClient();
  const file = useRef<HTMLInputElement>(null);
  const [pending, setPending] = useState<{ text: string; json: boolean } | null>(null);
  const [report, setReport] = useState<EAImportReport | null>(null);
  const run = useMutation({
    mutationFn: ({ text, json, dry }: { text: string; json: boolean; dry: boolean }) =>
      json ? eaApi.importJSON(JSON.parse(text), dry) : eaApi.importCSV(kind, text, dry),
    onSuccess: (r) => {
      setReport(r);
      if (!r.dry_run) {
        setPending(null);
        qc.invalidateQueries({ queryKey: ["ea"] });
      }
    },
  });
  return (
    <section className="flex flex-col gap-2" aria-label={t("altro.ea.import.label")}>
      <div className="flex flex-wrap items-center gap-2">
        <a className="btn no-underline" href={eaApi.exportUrl("csv", kind)} download>
          {t("altro.ea.import.exportCsv")}
        </a>
        <a className="btn no-underline" href={eaApi.exportUrl("json")} download>
          {t("altro.ea.import.catalogJson")}
        </a>
        {canWrite && (
          <>
            <input
              ref={file}
              type="file"
              accept=".csv,.json,text/csv,application/json"
              className="hidden"
              onChange={async (e) => {
                const f = e.target.files?.[0];
                e.target.value = "";
                if (!f) return;
                const text = await f.text();
                const json = f.name.toLowerCase().endsWith(".json") || text.trimStart().startsWith("{");
                setPending({ text, json });
                setReport(null);
                run.mutate({ text, json, dry: true });
              }}
            />
            <button className="btn" disabled={run.isPending} onClick={() => file.current?.click()}>
              {t("altro.ea.import.importFile")}
            </button>
          </>
        )}
      </div>
      <p className="m-0 text-[12px] text-muted">
        <Trans k="altro.ea.import.columns" slots={{ columns: <code className="font-mono">{COLUMNS[kind]}</code> }} />
      </p>
      {report && (
        <div className="flex flex-col gap-1.5 border-l-2 border-accent pl-4">
          <p className="m-0">
            {report.dry_run ? t("altro.ea.import.dryRun") : t("altro.ea.import.imported")}
            <Trans
              k="altro.ea.import.counts"
              slots={{
                created: <span className="font-semibold">{report.created}</span>,
                updated: <span className="font-semibold">{report.updated}</span>,
              }}
            />
            {report.errors > 0 && (
              <>
                , <span className="font-semibold text-danger">{t("altro.ea.import.errorRows", { count: report.errors })}</span>
              </>
            )}
            .
          </p>
          {report.rows
            .filter((r) => r.outcome === "error")
            .slice(0, 50)
            .map((r) => (
              <p key={`${r.kind}-${r.line}`} className="m-0 text-[12.5px] text-danger">
                {t("altro.ea.import.row", { line: r.line, name: r.name ?? "", message: r.message ?? "" })}
              </p>
            ))}
          {report.dry_run && pending && (
            <div className="flex gap-2">
              <button className="btn btn-primary" disabled={run.isPending || report.created + report.updated === 0} onClick={() => run.mutate({ ...pending, dry: false })}>
                {t("altro.ea.import.confirm")}
              </button>
              <button
                className="btn"
                onClick={() => {
                  setPending(null);
                  setReport(null);
                }}
              >
                {t("altro.ea.import.cancel")}
              </button>
            </div>
          )}
        </div>
      )}
      <ErrorNote error={run.error} />
    </section>
  );
}
