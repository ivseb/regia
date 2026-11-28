// Record letti da ServiceNow (connettore in sola lettura): le righe di incidenti e change,
// la sezione «ServiceNow» della scheda dell'applicazione e le proposte di incidente di Regia
// in Rischi e incidenti. Regia non scrive mai su ServiceNow: ogni numero porta al record.
import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { servicenowApi, type SNChange, type SNIncident, type SNProposal, type SNRef } from "../servicenowApi";
import { SectionTitle } from "../ui";
import { MutError, Tag, dateTime } from "./adminUi";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";

/** Collegamento al record in ServiceNow (si apre a parte). */
export function SNLink({ href, children }: { href: string; children: string }) {
  const t = useT();
  if (!href) return <span className="font-mono text-[13px] font-medium">{children}</span>;
  return (
    <a className="font-mono text-[13px] font-medium" href={href} target="_blank" rel="noreferrer" title={t("altro.servicenow.rec.openInSN")}>
      {children}
    </a>
  );
}

function RefLink({ r }: { r: SNRef }) {
  return <Link to="/e/$key" params={{ key: r.key || r.id }}>{r.title || r.key}</Link>;
}

/** Lo stato dell'incidente per le metriche DORA. */
export function DoraTag({ x }: { x: SNIncident }) {
  const t = useT();
  if (x.regia_incident) return <Tag tone="ok">{t("altro.servicenow.dora.viaRegia")}</Tag>;
  if (x.dora_status === "excluded") return <Tag>{t("altro.servicenow.dora.excluded")}</Tag>;
  if (x.counts) return <Tag tone="ok">{t("altro.servicenow.dora.counted")}</Tag>;
  return <Tag tone="flag">{t("altro.servicenow.dora.pending")}</Tag>;
}

export function SNIncidentRow({ x, showApp = true, bare = false }: { x: SNIncident; showApp?: boolean; bare?: boolean }) {
  const t = useT();
  return (
    <li className={`flex min-w-0 flex-col gap-1 ${bare ? "" : "border-b border-hair py-2.5"}`}>
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
        <SNLink href={x.url}>{x.number}</SNLink>
        <Tag tone={x.priority <= 1 ? "danger" : "flag"}>{t("altro.servicenow.rec.priority", { n: x.priority })}</Tag>
        {x.state_label && <Tag tone="fg">{x.state_label}</Tag>}
        <DoraTag x={x} />
        <span className="ml-auto text-[12px] text-muted tabular">{dateTime(x.opened_at)}</span>
      </div>
      {x.short_description && <span className="break-words">{x.short_description}</span>}
      <span className="flex flex-wrap gap-x-2 text-[12px] text-muted">
        {x.service && <span className="break-words">{t("altro.servicenow.rec.service", { name: x.service })}</span>}
        {showApp && x.application && (
          <span>
            <Trans k="altro.servicenow.rec.application" slots={{ app: <RefLink r={x.application} /> }} />
          </span>
        )}
        {x.resolved_at ? <span>{t("altro.servicenow.rec.resolved", { date: dateTime(x.resolved_at) })}</span> : <span className="text-danger">{t("altro.servicenow.rec.open")}</span>}
        {x.regia_incident && (
          <span>
            <Trans k="altro.servicenow.rec.regiaIncident" slots={{ link: <Link to="/e/$key" params={{ key: x.regia_incident.key }} className="font-mono">{x.regia_incident.key}</Link> }} />
          </span>
        )}
      </span>
    </li>
  );
}

function approvalTone(a: string): "ok" | "danger" | "muted" {
  if (a === "approved") return "ok";
  if (a === "rejected") return "danger";
  return "muted";
}

export function SNChangeRow({ x, showApp = true }: { x: SNChange; showApp?: boolean }) {
  const t = useT();
  const start = x.actual_start ?? x.planned_start;
  const end = x.actual_end ?? x.planned_end;
  return (
    <li className="flex min-w-0 flex-col gap-1 border-b border-hair py-2.5">
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
        <SNLink href={x.url}>{x.number}</SNLink>
        <Tag tone={approvalTone(x.approval)}>{x.approval_label || x.approval || t("altro.servicenow.rec.noApproval")}</Tag>
        {x.state_label && <Tag tone="fg">{x.state_label}</Tag>}
        {x.type && <span className="text-[12px] text-muted">{x.type}</span>}
        {start && (
          <span className="ml-auto text-[12px] text-muted tabular">
            {end ? t("altro.servicenow.rec.window", { from: dateTime(start), to: dateTime(end) }) : dateTime(start)}
          </span>
        )}
      </div>
      {x.short_description && <span className="break-words">{x.short_description}</span>}
      <span className="flex flex-wrap gap-x-2 text-[12px] text-muted">
        {x.service && <span className="break-words">{t("altro.servicenow.rec.service", { name: x.service })}</span>}
        {showApp && x.application && (
          <span>
            <Trans k="altro.servicenow.rec.application" slots={{ app: <RefLink r={x.application} /> }} />
          </span>
        )}
        {x.regia_change && (
          <span>
            <Trans k="altro.servicenow.rec.regiaChange" slots={{ number: <span className="font-mono">{x.regia_change}</span> }} />
          </span>
        )}
        {x.request && (
          <span>
            <Trans k="altro.servicenow.rec.request" slots={{ link: <Link to="/e/$key" params={{ key: x.request.key || x.request.id }}>{x.request.key}</Link> }} />
          </span>
        )}
      </span>
    </li>
  );
}

/**
 * Sezione «ServiceNow» della scheda dell'applicazione: gli ultimi incidenti e le change dei
 * servizi CMDB associati. Compare solo se ci sono dati (e se chi guarda può leggerli).
 */
export function ServiceNowAppSection({ applicationId }: { applicationId: string }) {
  const t = useT();
  const inc = useQuery({ queryKey: ["sn-incidents", "app", applicationId], queryFn: () => servicenowApi.incidents({ application: applicationId, limit: 5 }), retry: false });
  const chg = useQuery({ queryKey: ["sn-changes", "app", applicationId], queryFn: () => servicenowApi.changes({ application: applicationId, limit: 5 }), retry: false });
  const incidents = inc.data?.items ?? [];
  const changes = chg.data?.items ?? [];
  if (incidents.length === 0 && changes.length === 0) return null;
  return (
    <section className="flex min-w-0 flex-col gap-3" aria-label={t("altro.servicenow.app.title")}>
      <SectionTitle>{t("altro.servicenow.app.title")}</SectionTitle>
      <p className="m-0 max-w-[720px] text-[12.5px] text-muted">{t("altro.servicenow.app.note")}</p>
      {incidents.length > 0 && (
        <div className="flex min-w-0 flex-col gap-1">
          <h4 className="label m-0">{t("altro.servicenow.app.incidents")}</h4>
          <ul className="m-0 flex list-none flex-col p-0">
            {incidents.map((x) => (
              <SNIncidentRow key={x.id} x={x} showApp={false} />
            ))}
          </ul>
        </div>
      )}
      {changes.length > 0 && (
        <div className="flex min-w-0 flex-col gap-1">
          <h4 className="label m-0">{t("altro.servicenow.app.changes")}</h4>
          <ul className="m-0 flex list-none flex-col p-0">
            {changes.map((x) => (
              <SNChangeRow key={x.id} x={x} showApp={false} />
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}

/**
 * Proposte da ServiceNow in Rischi e incidenti (solo per chi può segnalare): ogni incidente
 * P1-P2 letto diventa un incidente di Regia, conta soltanto nelle DORA o non è rilevante.
 * La scelta e la classificazione DORA/NIS2 le fa la persona.
 */
export function ServiceNowProposals({ onCreate }: { onCreate: (p: SNProposal) => void }) {
  const t = useT();
  const qc = useQueryClient();
  const list = useQuery({ queryKey: ["sn-incidents", "proposals"], queryFn: () => servicenowApi.incidents({ proposals: true, limit: 20 }), retry: false });
  const [busy, setBusy] = useState<string | null>(null);
  const refresh = () => {
    qc.invalidateQueries({ queryKey: ["sn-incidents"] });
    qc.invalidateQueries({ queryKey: ["dora"] });
  };
  const open = useMutation({
    mutationFn: (id: string) => servicenowApi.proposal(id),
    onMutate: (id) => setBusy(id),
    onSettled: () => setBusy(null),
    onSuccess: (p) => onCreate(p),
  });
  const decide = useMutation({
    mutationFn: (v: { id: string; status: "counted" | "excluded" | "pending" }) => servicenowApi.decide(v.id, v.status),
    onMutate: (v) => setBusy(v.id),
    onSettled: () => setBusy(null),
    onSuccess: refresh,
  });
  const items = list.data?.items ?? [];
  if (items.length === 0) return null;
  return (
    <section className="flex min-w-0 flex-col gap-2" aria-label={t("altro.servicenow.prop.title")}>
      <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{items.length}</span>}>{t("altro.servicenow.prop.title")}</SectionTitle>
      <p className="m-0 max-w-3xl text-[13px] text-muted">{t("altro.servicenow.prop.intro")}</p>
      <p className="m-0 max-w-3xl border-l-2 border-flag pl-3 text-[13px]">{t("altro.servicenow.prop.classify")}</p>
      <MutError errors={[open.error, decide.error]} />
      <ul className="m-0 flex list-none flex-col p-0">
        {items.map((x) => (
          <li key={x.id} className="flex min-w-0 flex-col gap-2 border-b border-hair py-2.5">
            <ul className="m-0 list-none p-0">
              <SNIncidentRow x={x} bare />
            </ul>
            <div className="flex flex-wrap gap-2">
              <button type="button" className="btn btn-primary py-1.5" disabled={busy === x.id} onClick={() => open.mutate(x.id)}>
                {t("altro.servicenow.prop.create")}
              </button>
              {x.dora_status !== "counted" ? (
                <button type="button" className="btn py-1.5" disabled={busy === x.id} onClick={() => decide.mutate({ id: x.id, status: "counted" })}>
                  {t("altro.servicenow.prop.count")}
                </button>
              ) : (
                <button type="button" className="btn py-1.5" disabled={busy === x.id} onClick={() => decide.mutate({ id: x.id, status: "pending" })}>
                  {t("altro.servicenow.prop.undo")}
                </button>
              )}
              <button type="button" className="btn py-1.5" disabled={busy === x.id} onClick={() => decide.mutate({ id: x.id, status: "excluded" })}>
                {t("altro.servicenow.prop.exclude")}
              </button>
            </div>
          </li>
        ))}
      </ul>
    </section>
  );
}
