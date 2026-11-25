// Configurazione → Import (replica/usabilita.md, quasi vittoria «import unificati»): una sola
// pagina per portare dati dentro Regia. Si sceglie che cosa si importa (lavoro da Jira,
// YouTrack e CSV; applicazioni; costi; organizzazione) e ognuno ha il suo modello CSV da
// scaricare. Le procedure sono quelle già esistenti, qui raccolte.
import { useRef, useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, financeApi, type ImportReport } from "../api";
import { PageHeader, useMe } from "../shell";
import { ErrorNote, Loading, SectionTitle } from "../ui";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import { ImportPage } from "./Import";
import { ImportCsv as OrgImport } from "./budget/OrgConfig";
import { ImportTab as CostsImport } from "./Finance";
import { csvModel, downloadCsv, type CsvKind } from "../casa/modelliCsv";

export type ImportWhat = CsvKind;
export const IMPORT_KINDS: ImportWhat[] = ["lavoro", "applicazioni", "costi", "organizzazione"];

const NEED: Record<ImportWhat, string[]> = {
  lavoro: ["tenant.admin"],
  applicazioni: ["architecture.write", "tenant.admin"],
  costi: ["finance.write", "tenant.admin"],
  organizzazione: ["finance.write", "tenant.admin"],
};

export function ImportHub({ cosa, runId }: { cosa?: ImportWhat; runId?: string }) {
  const t = useT();
  const me = useMe();
  const perms = me.data?.active_tenant?.permissions ?? [];
  const can = (k: ImportWhat) => perms.includes("*") || NEED[k].some((p) => perms.includes(p));
  const kinds = IMPORT_KINDS.filter(can);
  // Una esecuzione aperta resta nella procedura del lavoro, senza il selettore.
  if (runId) return <ImportPage runId={runId} />;
  const current: ImportWhat = cosa && kinds.includes(cosa) ? cosa : (kinds[0] ?? "lavoro");
  return (
    <>
      <PageHeader path={t("altro.import.path")} title={t("altro.import.title")} />
      <div className="flex flex-col gap-5 px-4 pb-12 md:px-7">
        <nav aria-label={t("casa.import.what")} className="flex flex-wrap gap-2" data-testid="import-cosa">
          <span className="label self-center">{t("casa.import.what")}</span>
          {kinds.map((k) => (
            <Link
              key={k}
              to="/configurazione/import"
              search={{ cosa: k === "lavoro" ? undefined : k }}
              aria-current={current === k ? "page" : undefined}
              className={`btn no-underline ${current === k ? "border-accent bg-sel font-semibold" : ""}`}
            >
              {t(`casa.import.kind.${k}`)}
            </Link>
          ))}
        </nav>
        <ModelloCsv kind={current} />
      </div>
      {current === "lavoro" && <ImportPage embedded />}
      {current === "applicazioni" && (
        <div className="px-4 pb-12 md:px-7">
          <ApplicationsImport />
        </div>
      )}
      {current === "costi" && (
        <div className="flex flex-col gap-6 px-4 pb-12 md:px-7">
          <CostsPanel />
        </div>
      )}
      {current === "organizzazione" && (
        <div className="px-4 pb-12 md:px-7">
          <OrgImport />
        </div>
      )}
    </>
  );
}

/** Modello CSV da scaricare, con le colonne che l'import riconosce. */
function ModelloCsv({ kind }: { kind: ImportWhat }) {
  const t = useT();
  const m = csvModel(kind);
  return (
    <section className="flex max-w-3xl flex-col gap-2 border border-hair p-3" aria-label={t("casa.import.modelTitle")} data-testid="modello-csv">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <h2 className="m-0 text-[15px] font-bold">{t("casa.import.modelTitle")}</h2>
        <button type="button" className="btn btn-primary ml-auto" onClick={() => downloadCsv(kind)}>
          {t("casa.import.download", { file: m.file })}
        </button>
      </div>
      <p className="m-0 text-[12.5px] text-muted">{t(`casa.import.help.${kind}`)}</p>
      <p className="m-0 font-mono text-[12px] break-words">{m.header.join(";")}</p>
    </section>
  );
}

function CostsPanel() {
  const t = useT();
  const ov = useQuery({ queryKey: ["finance", "overview"], queryFn: financeApi.overview, retry: false });
  if (ov.isPending) return <Loading className="m-0 text-muted" />;
  if (!ov.data) return <ErrorNote error={ov.error} />;
  return (
    <>
      <p className="m-0 max-w-3xl text-[12.5px] text-muted">
        <Trans k="casa.import.costsNote" slots={{ link: <Link to="/finanza" search={{ tab: "costi" }}>{t("casa.import.costsLink")}</Link> }} />
      </p>
      <CostsImport ov={ov.data} />
    </>
  );
}

/** Applicazioni da CSV: prova (senza scrivere), poi conferma. */
function ApplicationsImport() {
  const t = useT();
  const qc = useQueryClient();
  const file = useRef<HTMLInputElement>(null);
  const [csv, setCsv] = useState<string | null>(null);
  const [report, setReport] = useState<ImportReport | null>(null);
  const run = useMutation({
    mutationFn: ({ text, dry }: { text: string; dry: boolean }) => api.importApplications(text, dry),
    onSuccess: (r) => {
      setReport(r);
      if (!r.dry_run) {
        setCsv(null);
        qc.invalidateQueries({ queryKey: ["applications"] });
        qc.invalidateQueries({ queryKey: ["assets"] });
        qc.invalidateQueries({ queryKey: ["teams"] });
      }
    },
  });
  return (
    <section className="flex flex-col gap-3" aria-label={t("casa.import.kind.applicazioni")}>
      <SectionTitle>{t("casa.import.apps.title")}</SectionTitle>
      <p className="m-0 max-w-3xl text-[12.5px] text-muted">{t("casa.import.apps.help")}</p>
      <div className="flex flex-wrap items-center gap-2">
        <input
          ref={file}
          type="file"
          accept=".csv,text/csv"
          className="sr-only"
          aria-label={t("casa.import.apps.file")}
          onChange={async (e) => {
            const f = e.target.files?.[0];
            e.target.value = "";
            if (!f) return;
            const text = await f.text();
            setCsv(text);
            setReport(null);
            run.mutate({ text, dry: true });
          }}
        />
        <button type="button" className="btn" onClick={() => file.current?.click()} disabled={run.isPending}>
          {t("casa.import.apps.pick")}
        </button>
      </div>
      <ErrorNote error={run.error} />
      {report && (
        <div className="flex flex-col gap-2 border-l-2 border-accent pl-4" aria-live="polite">
          <p className="m-0">
            {report.dry_run ? t("casa.import.apps.dryResult", { created: report.created, updated: report.updated }) : t("casa.import.apps.doneResult", { created: report.created, updated: report.updated })}
            {report.errors > 0 && <span className="font-semibold text-danger"> {t("casa.import.apps.errors", { count: report.errors })}</span>}
          </p>
          {report.rows
            .filter((r) => r.outcome === "error")
            .map((r) => (
              <p key={r.line} className="m-0 text-[12.5px] text-danger">
                {t("casa.import.apps.line", { line: r.line })} {r.name && `(${r.name})`}: {r.message}
              </p>
            ))}
          {report.dry_run && csv && (
            <div className="flex gap-2">
              <button type="button" className="btn btn-primary" disabled={run.isPending || report.created + report.updated === 0} onClick={() => run.mutate({ text: csv, dry: false })}>
                {t("casa.import.apps.confirm")}
              </button>
              <button
                type="button"
                className="btn"
                onClick={() => {
                  setCsv(null);
                  setReport(null);
                }}
              >
                {t("configurazione.common.annulla")}
              </button>
            </div>
          )}
        </div>
      )}
    </section>
  );
}
