// Certificazione del rilascio: /change/$numero/certificazione (router.tsx, certificationRoute).
// Un documento di una pagina A4 per numero di change: versioni rilasciate, elementi, test,
// Sonar, CVE al rilascio, decisioni del gate e approvazioni. Si legge dal vivo; «Genera»
// congela un'istantanea numerata con impronta SHA-256 (?versione=N la mostra).
// Stampa: il documento sta anche in #print-root (print.css nasconde il resto della pagina),
// così vale sia il pulsante sia ⌘P; in stampa niente pulsanti né selettore, due colonne compatte.
import { useEffect, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import "../print.css";
import { ApiError } from "../api";
import { rilasciApi, type CertSection, type Certification, type ServiceNowChange, CERT_SECTIONS } from "../rilasciApi";
import type { CheckResult } from "../devsecopsApi";
import { PageHeader } from "../shell";
import { ErrorNote, Loading, SectionTitle } from "../ui";
import { Select } from "../select";
import { crumbs } from "../crumbs";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import type { Key } from "../i18n/core";
import { fmtDate, fmtDateTime, fmtNumber } from "../i18n/format";
import { groupItems, runNumbers, shortHash, testTotals, visibleSections } from "../certificazione";

type Props = { number: string; version?: number; onVersion: (v?: number) => void };

export function CertificationPage({ number, version, onVersion }: Props) {
  const t = useT();
  const qc = useQueryClient();
  const cert = useQuery({
    queryKey: ["certification", number, version ?? 0],
    queryFn: () => rilasciApi.certification(number, version),
    retry: (n, e) => !(e instanceof ApiError && e.status === 404) && n < 2,
  });
  const generate = useMutation({
    mutationFn: () => rilasciApi.generateCertification(number),
    onSuccess: (res) => {
      if (res.version) qc.setQueryData(["certification", number, res.version.version], res);
      qc.invalidateQueries({ queryKey: ["certification", number] });
      onVersion(res.version?.version);
    },
  });
  const c = cert.data;
  const path = `${crumbs("devsecops")} / ${t("rilasci.certificazione.crumb")}`;
  const title = t("rilasci.certificazione.titolo", { number: c?.change.number ?? number });
  const notFound = cert.error instanceof ApiError && cert.error.status === 404;
  return (
    <>
      <PageHeader path={path} title={title} />
      <div className="flex max-w-5xl min-w-0 flex-col gap-6 px-4 pb-10 md:px-7">
        {notFound && version ? (
          <div className="flex flex-col gap-2">
            <p className="m-0">{t("rilasci.certificazione.versioneNonTrovata", { n: version, number })}</p>
            <button className="btn self-start" onClick={() => onVersion(undefined)}>{t("rilasci.certificazione.tornaDalVivo")}</button>
          </div>
        ) : notFound ? (
          <div className="flex flex-col gap-1">
            <h2 className="m-0 text-[18px] font-bold">{t("rilasci.certificazione.nonTrovato")}</h2>
            <p className="m-0 text-muted">{t("rilasci.certificazione.nonTrovatoTesto", { number })}</p>
          </div>
        ) : (
          <ErrorNote error={cert.error} />
        )}
        {!c && !cert.error && <Loading rows={6} />}
        {c && (
          <>
            <Meta c={c} />
            <Actions c={c} number={number} onVersion={onVersion} generating={generate.isPending} onGenerate={() => generate.mutate()} />
            <ErrorNote error={generate.error} />
            {!c.evidence_mapped && c.can_generate && (
              <p className="m-0 max-w-3xl border-l-2 border-hair pl-3 text-[12px] text-muted">
                <Trans
                  k="rilasci.certificazione.evidenzaNonMappata"
                  slots={{ gate: <Link to="/configurazione/gate">{t("rilasci.certificazione.evidenzaNonMappataLink")}</Link> }}
                />
              </p>
            )}
            <CertBody c={c} />
            <PrintCopy c={c} title={title} />
          </>
        )}
      </div>
    </>
  );
}

// ---------------------------------------------------------------------
// Intestazione: richiesta, stato, impronta, evidenza

function statusText(t: ReturnType<typeof useT>, c: Certification): string {
  const v = c.version;
  if (!v) return t("rilasci.certificazione.statoVivo", { when: fmtDateTime(c.at) });
  return v.by
    ? t("rilasci.certificazione.statoVersione", { n: v.version, when: fmtDateTime(v.at), by: v.by })
    : t("rilasci.certificazione.statoVersioneAnonima", { n: v.version, when: fmtDateTime(v.at) });
}

function Meta({ c, print = false }: { c: Certification; print?: boolean }) {
  const t = useT();
  const req = c.change.request;
  const v = c.version;
  return (
    <div className="flex min-w-0 flex-col gap-1">
      <p className="m-0 min-w-0 break-words">
        {req ? (
          <>
            <Link to="/e/$key" params={{ key: req.key }} className="font-mono">{t("rilasci.certificazione.richiesta", { key: req.key })}</Link> {req.title}
          </>
        ) : (
          <span className="text-muted">{t("rilasci.certificazione.senzaRichiesta")}</span>
        )}
      </p>
      <p className={`m-0 min-w-0 break-words ${print ? "" : "text-[13px]"} text-muted`}>
        {statusText(t, c)}
        {v && (
          <>
            {" "}
            <span className="font-mono text-fg" title={v.sha256}>{t("rilasci.certificazione.impronta", { hash: shortHash(v.sha256) })}</span>
          </>
        )}
        {v?.evidence_key && (
          <>
            {" · "}
            <Link to="/e/$key" params={{ key: v.evidence_key }} className="font-mono">{t("rilasci.certificazione.evidenza", { key: v.evidence_key })}</Link>
          </>
        )}
      </p>
    </div>
  );
}

function Actions({ c, number, onVersion, generating, onGenerate }: {
  c: Certification;
  number: string;
  onVersion: (v?: number) => void;
  generating: boolean;
  onGenerate: () => void;
}) {
  const t = useT();
  const [copied, setCopied] = useState(false);
  const empty = !c.version && c.releases.length === 0;
  const copy = () => {
    void navigator.clipboard?.writeText(window.location.origin + c.path).then(() => {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2500);
    });
  };
  return (
    <div className="flex flex-wrap items-end gap-2">
      {c.can_generate && (
        <button className="btn btn-primary" disabled={generating || empty} title={empty ? t("rilasci.certificazione.senzaRilasci") : undefined} onClick={onGenerate}>
          {generating ? t("rilasci.certificazione.generazione") : t("rilasci.certificazione.genera")}
        </button>
      )}
      <button className="btn" onClick={() => window.print()}>{t("rilasci.certificazione.stampa")}</button>
      <button className="btn" onClick={copy}>{t("rilasci.certificazione.copiaLink")}</button>
      {copied && <span role="status" className="self-center text-[12.5px] text-ok">{t("rilasci.certificazione.linkCopiato")}</span>}
      {c.generated.length > 0 && (
        <label className="ml-auto flex min-w-0 flex-col gap-1">
          <span className="label">{t("rilasci.certificazione.versioni")}</span>
          <Select
            key={number}
            className="field py-1"
            value={c.version ? String(c.version.version) : ""}
            onChange={(e) => onVersion(e.target.value ? Number(e.target.value) : undefined)}
          >
            <option value="">{t("rilasci.certificazione.dalVivo")}</option>
            {c.generated.map((g) => (
              <option key={g.version} value={String(g.version)}>{t("rilasci.certificazione.versioneVoce", { n: g.version, when: fmtDate(g.at) })}</option>
            ))}
          </Select>
        </label>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------
// Corpo: le sezioni nell'ordine delle impostazioni

const SECTION_TITLE: Record<CertSection, Key> = {
  change: "rilasci.certificazione.sezione.change",
  releases: "rilasci.certificazione.sezione.releases",
  items: "rilasci.certificazione.sezione.items",
  tests: "rilasci.certificazione.sezione.tests",
  sonar: "rilasci.certificazione.sezione.sonar",
  security: "rilasci.certificazione.sezione.security",
  gate: "rilasci.certificazione.sezione.gate",
};

function CertBody({ c, print = false }: { c: Certification; print?: boolean }) {
  const t = useT();
  const sections = visibleSections(c.sections, CERT_SECTIONS);
  return (
    <div className={print ? "columns-2 gap-x-[7mm]" : "flex min-w-0 flex-col gap-7"}>
      {sections.map((s) => (
        <section key={s} aria-label={t(SECTION_TITLE[s])} className={`flex min-w-0 break-inside-avoid flex-col ${print ? "mb-[4mm] gap-[1.5mm]" : "gap-2"}`}>
          <SectionTitle>{t(SECTION_TITLE[s])}</SectionTitle>
          <Section s={s} c={c} />
        </section>
      ))}
    </div>
  );
}

function Section({ s, c }: { s: CertSection; c: Certification }) {
  switch (s) {
    case "change":
      return <ChangeSection c={c} />;
    case "releases":
      return <ReleasesSection c={c} />;
    case "items":
      return <ItemsSection c={c} />;
    case "tests":
      return <TestsSection c={c} />;
    case "sonar":
      return <SonarSection c={c} />;
    case "security":
      return <SecuritySection c={c} />;
    case "gate":
      return <GateSection c={c} />;
  }
}

const rowCls = "grid min-w-0 grid-cols-[minmax(0,1fr)_auto] gap-x-3 border-b border-hair py-1";
const listCls = "m-0 flex list-none flex-col p-0";
const Empty = ({ children }: { children: ReactNode }) => <p className="m-0 text-muted">{children}</p>;

function Row({ k, children }: { k: Key; children: ReactNode }) {
  const t = useT();
  return (
    <div className="grid min-w-0 grid-cols-[minmax(0,9rem)_minmax(0,1fr)] gap-x-3 border-b border-hair py-1">
      <dt className="text-muted">{t(k)}</dt>
      <dd className="m-0 min-w-0 break-words">{children}</dd>
    </div>
  );
}

const SN_APPROVAL: Record<string, { k: Key; tone: string }> = {
  "not requested": { k: "rilasci.certificazione.sn.approvazione.notRequested", tone: "text-muted" },
  requested: { k: "rilasci.certificazione.sn.approvazione.requested", tone: "text-flag" },
  approved: { k: "rilasci.certificazione.sn.approvazione.approved", tone: "text-ok" },
  rejected: { k: "rilasci.certificazione.sn.approvazione.rejected", tone: "text-danger" },
};

/** Finestra «dal … al …» (o solo un estremo); nulla se mancano entrambi. */
function windowText(t: ReturnType<typeof useT>, from?: string | null, to?: string | null): string | null {
  if (from && to) return t("rilasci.certificazione.sn.finestra", { from: fmtDateTime(from), to: fmtDateTime(to) });
  if (from) return t("rilasci.certificazione.sn.finestraDal", { from: fmtDateTime(from) });
  if (to) return t("rilasci.certificazione.sn.finestraAl", { to: fmtDateTime(to) });
  return null;
}

function ServiceNowRows({ sn }: { sn: ServiceNowChange }) {
  const t = useT();
  const approval = sn.approval ? SN_APPROVAL[sn.approval.toLowerCase()] : undefined;
  const planned = windowText(t, sn.planned_start, sn.planned_end);
  const actual = windowText(t, sn.actual_start, sn.actual_end);
  return (
    <>
      {sn.state && <Row k="rilasci.certificazione.sn.stato"><span className="font-mono">{sn.state}</span></Row>}
      {sn.approval && (
        <Row k="rilasci.certificazione.sn.approvazione">
          <span className={`font-semibold ${approval?.tone ?? ""}`}>{approval ? t(approval.k) : sn.approval}</span>
        </Row>
      )}
      {sn.type && <Row k="rilasci.certificazione.sn.tipo">{sn.type}</Row>}
      {planned && <Row k="rilasci.certificazione.sn.pianificata"><span className="tabular">{planned}</span></Row>}
      {actual && <Row k="rilasci.certificazione.sn.effettiva"><span className="tabular">{actual}</span></Row>}
      {sn.url && (
        <Row k="rilasci.certificazione.sn.record">
          <a href={sn.url} target="_blank" rel="noreferrer">{t("rilasci.certificazione.sn.apri")}</a>
        </Row>
      )}
    </>
  );
}

function ChangeSection({ c }: { c: Certification }) {
  const t = useT();
  const ch = c.change;
  return (
    <dl className="m-0">
      <Row k="rilasci.certificazione.change.numero"><span className="font-mono">{ch.number}</span></Row>
      {ch.aliases.length > 0 && <Row k="rilasci.certificazione.change.alias"><span className="font-mono">{ch.aliases.join(", ")}</span></Row>}
      <Row k="rilasci.certificazione.change.titolo">{ch.title || <span className="text-muted">{t("rilasci.certificazione.change.nessunTitolo")}</span>}</Row>
      <Row k="rilasci.certificazione.change.richiesta">
        {ch.request ? (
          <>
            <Link to="/e/$key" params={{ key: ch.request.key }} className="font-mono">{ch.request.key}</Link> {ch.request.title}
            {ch.request.state && <span className="text-muted"> · {ch.request.state}</span>}
          </>
        ) : (
          <span className="text-muted">—</span>
        )}
      </Row>
      {ch.servicenow && <ServiceNowRows sn={ch.servicenow} />}
    </dl>
  );
}

function ReleasesSection({ c }: { c: Certification }) {
  const t = useT();
  if (c.releases.length === 0) return <Empty>{t("rilasci.certificazione.rilasci.nessuno")}</Empty>;
  return (
    <table className="w-full table-fixed border-collapse">
      <thead>
        <tr className="label text-left">
          <th scope="col" className="w-[34%] border-b border-rule py-1 pr-2">{t("rilasci.certificazione.rilasci.servizio")}</th>
          <th scope="col" className="w-[24%] border-b border-rule py-1 pr-2">{t("rilasci.certificazione.rilasci.versione")}</th>
          <th scope="col" className="w-[20%] border-b border-rule py-1 pr-2">{t("rilasci.certificazione.rilasci.ambiente")}</th>
          <th scope="col" className="border-b border-rule py-1">{t("rilasci.certificazione.rilasci.data")}</th>
        </tr>
      </thead>
      <tbody>
        {c.releases.map((r) => (
          <tr key={r.id} className="border-b border-hair align-top">
            <td className="py-1 pr-2 break-words">{r.service}</td>
            <td className="py-1 pr-2 break-words">
              <Link to="/rilasci/$id" params={{ id: r.id }} className="font-mono tabular" title={t("rilasci.certificazione.rilasci.apri")}>{r.version}</Link>
            </td>
            <td className={`py-1 pr-2 break-words ${r.production ? "font-semibold" : "text-muted"}`}>{r.environment}</td>
            <td className="py-1 tabular">{fmtDate(r.at)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

const GROUP_TITLE: Record<string, Key> = { new: "rilasci.certificazione.elementi.new", fix: "rilasci.certificazione.elementi.fix", other: "rilasci.certificazione.elementi.other" };

function ItemsSection({ c }: { c: Certification }) {
  const t = useT();
  const groups = groupItems(c.items);
  return (
    <div className="flex min-w-0 flex-col gap-2">
      {groups.length === 0 && <Empty>{t("rilasci.certificazione.elementi.nessuno")}</Empty>}
      {groups.map((g) => (
        <div key={g.group} className="flex min-w-0 break-inside-avoid flex-col">
          <h4 className="m-0 text-[0.9em] font-semibold">
            {t(GROUP_TITLE[g.group])} <span className="font-mono font-normal text-muted tabular">{g.items.length}</span>
          </h4>
          <ul className={listCls}>
            {g.items.map((i) => (
              <li key={i.id || i.key} className="grid min-w-0 grid-cols-[auto_minmax(0,1fr)] gap-x-2 border-b border-hair py-0.5">
                <Link to="/e/$key" params={{ key: i.key }} className="font-mono tabular">{i.key}</Link>
                <span className="min-w-0 break-words">
                  {i.title} <span className="text-[0.9em] text-muted">· {i.type_name || i.type} · {i.state}</span>
                </span>
              </li>
            ))}
          </ul>
        </div>
      ))}
      {c.hidden > 0 && <p className="m-0 text-[0.9em] text-muted">{t("rilasci.certificazione.elementi.nascosti", { count: c.hidden })}</p>}
    </div>
  );
}

const OUTCOME: Record<string, Key> = {
  passed: "rilasci.certificazione.esito.passed",
  failed: "rilasci.certificazione.esito.failed",
  skipped: "rilasci.certificazione.esito.skipped",
  not_run: "rilasci.certificazione.esito.not_run",
};
const ORIGIN: Record<string, Key> = {
  pipeline: "rilasci.certificazione.test.origine.pipeline",
  alm: "rilasci.certificazione.test.origine.alm",
  native: "rilasci.certificazione.test.origine.native",
};
const OUTCOME_TONE: Record<string, string> = { passed: "text-ok", failed: "text-danger", skipped: "text-flag", not_run: "text-muted" };

function Outcome({ o }: { o: string }) {
  const t = useT();
  return <span className={`font-semibold ${OUTCOME_TONE[o] ?? ""}`}>{OUTCOME[o] ? t(OUTCOME[o]) : o}</span>;
}

/** Riga di un controllo (test o Sonar): nome, versione, esito, sintesi, collegamento. */
function CheckRow({ name, service, version, outcome, summary, url, extra }: {
  name: string;
  service: string;
  version: string;
  outcome: CheckResult["outcome"] | string;
  summary: string;
  url: string;
  extra?: ReactNode;
}) {
  const t = useT();
  return (
    <li className={rowCls}>
      <span className="min-w-0 break-words">
        <span className="font-mono">{name}</span> <span className="text-muted">· {service} <span className="font-mono">{version}</span></span>
        {extra}
        {summary && <span className="block text-[0.9em] text-muted">{summary}</span>}
      </span>
      <span className="text-right">
        <Outcome o={outcome} />
        {url && (
          <a href={url} target="_blank" rel="noreferrer" className="block text-[0.9em] print:hidden">{t("rilasci.certificazione.dettaglio")}</a>
        )}
      </span>
    </li>
  );
}

function TestsSection({ c }: { c: Certification }) {
  const t = useT();
  const tot = testTotals(c.test_totals, (n) => fmtNumber(n));
  if (c.tests.length === 0) return <Empty>{t("rilasci.certificazione.test.vuoto")}</Empty>;
  return (
    <div className="flex min-w-0 flex-col gap-1">
      {tot && <p className="m-0 font-semibold tabular">{t("rilasci.certificazione.test.totali", tot)}</p>}
      <ul className={listCls}>
        {c.tests.map((r, i) => {
          const nums = runNumbers(r);
          return (
            <CheckRow key={i} name={r.name} service={r.service} version={r.version} outcome={r.outcome} summary={r.summary} url={r.url}
              extra={<span className="text-muted"> · {ORIGIN[r.origin] ? t(ORIGIN[r.origin]) : r.origin}{nums && <span className="font-mono tabular"> · {nums}</span>}</span>} />
          );
        })}
      </ul>
    </div>
  );
}

function SonarSection({ c }: { c: Certification }) {
  const t = useT();
  if (c.sonar.length === 0) return <Empty>{t("rilasci.certificazione.sonar.vuoto")}</Empty>;
  return (
    <ul className={listCls}>
      {c.sonar.map((s, i) => <CheckRow key={i} {...s} />)}
    </ul>
  );
}

function SecuritySection({ c }: { c: Certification }) {
  const t = useT();
  if (c.security.length === 0) return <Empty>{t("rilasci.certificazione.sicurezza.vuoto")}</Empty>;
  return (
    <ul className={listCls}>
      {c.security.map((s, i) => (
        <li key={i} className="flex min-w-0 flex-col border-b border-hair py-1">
          <span className="min-w-0 break-words">
            {s.service} <span className="font-mono">{s.version}</span> <span className="text-muted">· {s.environment}</span>
          </span>
          {s.cves ? (
            <span className={`font-mono text-[0.9em] tabular ${s.cves.critical > 0 ? "text-danger" : s.cves.high > 0 ? "text-flag" : ""}`}>
              {t("rilasci.certificazione.cve", { c: s.cves.critical, h: s.cves.high, m: s.cves.medium, l: s.cves.low })}
            </span>
          ) : (
            <span className="text-[0.9em] text-muted">{t("rilasci.certificazione.cve.nonDichiarate")}</span>
          )}
        </li>
      ))}
    </ul>
  );
}

const GATE: Record<string, { k: Key; tone: string }> = {
  pass: { k: "rilasci.certificazione.gate.pass", tone: "text-ok" },
  warn: { k: "rilasci.certificazione.gate.warn", tone: "text-flag" },
  fail: { k: "rilasci.certificazione.gate.fail", tone: "text-danger" },
};

function GateSection({ c }: { c: Certification }) {
  const t = useT();
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <h4 className="m-0 text-[0.9em] font-semibold">{t("rilasci.certificazione.gate.decisioni")}</h4>
      {c.gate.length === 0 ? (
        <Empty>{t("rilasci.certificazione.gate.vuoto")}</Empty>
      ) : (
        <ul className={listCls}>
          {c.gate.map((g, i) => (
            <li key={i} className={rowCls}>
              <span className="min-w-0 break-words">
                {g.service || "—"} <span className="font-mono">{g.version}</span> <span className="text-muted">· {g.environment} · {fmtDate(g.at)}</span>
              </span>
              <span className="text-right">
                <span className={`font-semibold ${GATE[g.outcome]?.tone ?? ""}`}>{GATE[g.outcome] ? t(GATE[g.outcome].k) : g.outcome}</span>
                {g.overridden && <span className="block text-[0.9em] text-flag">{t("rilasci.certificazione.gate.override")}</span>}
              </span>
            </li>
          ))}
        </ul>
      )}
      <h4 className="m-0 text-[0.9em] font-semibold">{t("rilasci.certificazione.approvazioni")}</h4>
      {c.approvals.length === 0 ? (
        <Empty>{t("rilasci.certificazione.approvazioni.vuoto")}</Empty>
      ) : (
        <ul className={listCls}>
          {c.approvals.map((a, i) => {
            const parts: string[] = [];
            if (a.requested_by) parts.push(t("rilasci.certificazione.approvazioni.richiesta", { name: a.requested_by }));
            if (a.approved_by)
              parts.push(
                a.approved_at
                  ? t("rilasci.certificazione.approvazioni.approvataIl", { name: a.approved_by, when: fmtDate(a.approved_at) })
                  : t("rilasci.certificazione.approvazioni.approvata", { name: a.approved_by }),
              );
            return (
              <li key={i} className="flex min-w-0 flex-col border-b border-hair py-1">
                <span className="min-w-0 break-words">
                  {a.service} <span className="font-mono">{a.version}</span> <span className="text-muted">· {a.environment}</span>
                </span>
                <span className={`text-[0.9em] ${a.approved_by ? "" : "text-flag"}`}>{parts.length ? parts.join(", ") : t("rilasci.certificazione.approvazioni.nessuna")}</span>
              </li>
            );
          })}
        </ul>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------
// Copia per la stampa

/** Il documento in #print-root, fuori dall'app: sempre nel tema chiaro, caratteri compatti, una pagina. */
function PrintCopy({ c, title }: { c: Certification; title: string }) {
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
    return () => root.remove();
  }, [root]);
  const stato = c.version ? t("rilasci.certificazione.piedeVersione", { n: c.version.version, hash: c.version.sha256 }) : t("rilasci.certificazione.piedeVivo");
  return createPortal(
    <article className="flex flex-col gap-[3mm] text-[8.5pt] leading-snug">
      <header className="flex flex-col gap-[1mm] border-b-2 border-rule pb-[2mm]">
        <h1 className="m-0 text-[16pt] leading-tight font-bold tracking-[-0.02em]">{title}</h1>
        <Meta c={c} print />
      </header>
      <CertBody c={c} print />
      <footer className="border-t border-hair pt-[1mm] font-mono text-[7pt] break-all text-muted">
        {t("rilasci.certificazione.piede", { number: c.change.number, stato, when: fmtDateTime(new Date()) })} · {window.location.origin + c.path}
        {c.version ? `?versione=${c.version.version}` : ""}
      </footer>
    </article>,
    root,
  );
}
