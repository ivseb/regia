// Blocco «Continuità» nella sezione Architettura dell'applicazione e nella pagina della capability:
// dichiarato contro richiesto (con la BIA da cui viene), dipendenze più lente, piani, ultimo test
// e incoerenze dell'oggetto. Con il modulo spento (o non preparato) non compare.
import { useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useMe } from "../../shell";
import { moduleOn } from "../../modules";
import { EntitySelect } from "../../select";
import { ErrorNote, SectionTitle, date } from "../../ui";
import { useT } from "../../i18n/react";
import { useStateName } from "../../stateLabels";
import {
  continuityApi,
  withinLimit,
  type ApplicationContinuity,
  type Can,
  type CapabilityContinuity,
  type Issue,
  type PlanRef,
  type TestBrief,
} from "../../continuityApi";
import { IssueRow, Minutes, OkMark } from "./ui";

export function ContinuityBlock({ kind, refKey }: { kind: "application" | "capability"; refKey: string }) {
  const me = useMe().data;
  const on = moduleOn(me, "continuity");
  const app = useQuery({ queryKey: ["continuity", "application", refKey], queryFn: () => continuityApi.application(refKey), enabled: on && kind === "application", retry: false });
  const cap = useQuery({ queryKey: ["continuity", "capability", refKey], queryFn: () => continuityApi.capability(refKey), enabled: on && kind === "capability", retry: false });
  if (!on) return null;
  // Errori (modulo non preparato, oggetto fuori dal grafo): il blocco semplicemente non c'è.
  if (kind === "application") return app.data ? <ApplicationBlock d={app.data} /> : null;
  return cap.data ? <CapabilityBlock d={cap.data} /> : null;
}

function Frame({ children }: { children: ReactNode }) {
  const t = useT();
  return (
    <section className="flex min-w-0 flex-col gap-3" aria-label={t("continuita.block.title")}>
      <SectionTitle
        aside={
          <Link to="/architettura/continuita" className="text-[12px] text-accent underline">
            {t("continuita.block.all")}
          </Link>
        }
      >
        {t("continuita.block.title")}
      </SectionTitle>
      {children}
    </section>
  );
}

function ApplicationBlock({ d }: { d: ApplicationContinuity }) {
  const t = useT();
  const deps = [...d.dependencies].sort((a, b) => Number(b.slower) - Number(a.slower));
  return (
    <Frame>
      <table className="w-auto border-collapse text-[12.5px]">
        <thead>
          <tr className="text-left text-[11.5px] text-muted">
            <th scope="col" className="pr-4 font-normal">
              <span className="sr-only">{t("continuita.block.value")}</span>
            </th>
            <th scope="col" className="pr-4 font-normal">{t("continuita.block.declared")}</th>
            <th scope="col" className="pr-4 font-normal">{t("continuita.block.required")}</th>
            <th scope="col" className="font-normal">
              <span className="sr-only">{t("continuita.test.result")}</span>
            </th>
          </tr>
        </thead>
        <tbody>
          {([
            [t("continuita.field.rto"), d.declared.rto, d.required.rto],
            [t("continuita.field.rpo"), d.declared.rpo, d.required.rpo],
          ] as const).map(([label, declared, required]) => (
            <tr key={label}>
              <th scope="row" className="pr-4 text-left font-normal text-muted">{label}</th>
              <td className="pr-4"><Minutes m={declared} /></td>
              <td className="pr-4"><Minutes m={required} /></td>
              <td><OkMark ok={withinLimit(declared, required)} /></td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="m-0 text-[12.5px]">
        {d.required.source ? (
          <>
            <span className="text-muted">{t("continuita.block.fromBia")} </span>
            <Link to="/e/$key" params={{ key: d.required.source.key }}>
              {d.required.source.title}
            </Link>
          </>
        ) : (
          <span className="text-muted">{t("continuita.block.noRequired")}</span>
        )}
      </p>
      <p className="m-0 text-[12.5px]">
        <span className="text-muted">{t("continuita.block.criticalFunction")} </span>
        {t("continuita.block.criticalLine", {
          declared: d.critical_declared ? t("continuita.common.yes") : t("continuita.common.no"),
          required: d.critical_required ? t("continuita.common.yes") : t("continuita.common.no"),
        })}
        {d.critical_required && !d.critical_declared && <span className="ml-2 font-semibold text-danger">{t("continuita.ok.no")}</span>}
      </p>
      {deps.length > 0 && (
        <div className="flex flex-col gap-1">
          <span className="label">{t("continuita.block.dependencies")}</span>
          <ul className="m-0 flex list-none flex-col p-0">
            {deps.map((x) => (
              <li key={x.id} className="flex flex-wrap items-baseline gap-x-3 border-b border-hair py-1 text-[12.5px]">
                <Link to="/e/$key" params={{ key: x.key }} className="min-w-0 flex-[1_1_180px] text-fg no-underline hover:underline">
                  {x.title}
                </Link>
                <span className="text-[11.5px] text-muted">{x.kind === "provider" ? t("continuita.kind.provider") : t("continuita.kind.application")}</span>
                <span>
                  <span className="text-muted">{t("continuita.field.rto")} </span>
                  <Minutes m={x.rto_minutes} />
                </span>
                {x.slower && <span className="font-semibold text-danger">{t("continuita.block.slower")}</span>}
                {x.rto_minutes == null && <span className="text-flag">{t("continuita.block.noRto")}</span>}
              </li>
            ))}
          </ul>
        </div>
      )}
      <Plans plans={d.plans} can={d.can} target={d.application.id} />
      <LastTest test={d.last_test} />
      <Issues items={d.issues} />
    </Frame>
  );
}

function CapabilityBlock({ d }: { d: CapabilityContinuity }) {
  const t = useT();
  const stateName = useStateName();
  const b = d.bia;
  return (
    <Frame>
      {b ? (
        <div className="flex flex-col gap-1 text-[12.5px]">
          <p className="m-0 flex flex-wrap items-baseline gap-x-3">
            <Link to="/e/$key" params={{ key: b.key }} className="font-mono">
              {b.key}
            </Link>
            <span>{stateName(b.state)}</span>
            {b.critical && <span className="border border-flag px-1 text-[10.5px] font-semibold text-flag">{t("continuita.bia.critical")}</span>}
            {b.approved_version != null && <span className="text-muted">{t("continuita.block.approvedOn", { v: b.approved_version, date: date(b.approved_at) })}</span>}
          </p>
          <p className="m-0 flex flex-wrap gap-x-4 tabular">
            <span>
              <span className="text-muted">{t("continuita.field.mtpd")} </span>
              <Minutes m={b.mtpd_minutes} />
            </span>
            <span>
              <span className="text-muted">{t("continuita.field.rto")} </span>
              <Minutes m={b.rto_minutes} />
            </span>
            <span>
              <span className="text-muted">{t("continuita.field.rpo")} </span>
              <Minutes m={b.rpo_minutes} />
            </span>
          </p>
          {b.review_due && <span className="text-muted">{t("continuita.bia.reviewDue", { date: date(b.review_due) })}</span>}
        </div>
      ) : (
        <p className="m-0 text-[12.5px]">
          <span className="text-muted">{t("continuita.block.noBia")} </span>
          <Link to="/architettura/continuita/bia" search={{ capability: d.capability.key }}>
            {t("continuita.block.goBia")}
          </Link>
        </p>
      )}
      {d.applications.length > 0 && (
        <div className="flex flex-col gap-1">
          <span className="label">{t("continuita.block.applications")}</span>
          <ul className="m-0 flex list-none flex-col p-0">
            {d.applications.map((a) => (
              <li key={a.id} className="flex flex-wrap items-baseline gap-x-3 border-b border-hair py-1 text-[12.5px]">
                <Link to="/e/$key" params={{ key: a.key }} className="min-w-0 flex-[1_1_180px] text-fg no-underline hover:underline">
                  {a.title}
                </Link>
                <span>
                  <span className="text-muted">{t("continuita.field.rto")} </span>
                  <Minutes m={a.rto_minutes} />
                  {b && <span className="ml-1.5"><OkMark ok={withinLimit(a.rto_minutes, b.rto_minutes)} /></span>}
                </span>
                <span>
                  <span className="text-muted">{t("continuita.field.rpo")} </span>
                  <Minutes m={a.rpo_minutes} />
                  {b && <span className="ml-1.5"><OkMark ok={withinLimit(a.rpo_minutes, b.rpo_minutes)} /></span>}
                </span>
              </li>
            ))}
          </ul>
        </div>
      )}
      <Plans plans={d.plans} can={d.can} target={d.capability.id} />
      <LastTest test={d.last_test} />
      <Issues items={d.issues} />
    </Frame>
  );
}

function Plans({ plans, can, target }: { plans: PlanRef[]; can: Can; target: string }) {
  const t = useT();
  const qc = useQueryClient();
  const [page, setPage] = useState("");
  const done = () => {
    setPage("");
    void qc.invalidateQueries({ queryKey: ["continuity"] });
  };
  const add = useMutation({ mutationFn: () => continuityApi.addPlan(page, target), onSuccess: done });
  const remove = useMutation({ mutationFn: (p: PlanRef) => continuityApi.removePlan(p.page.id, p.target.id), onSuccess: done });
  return (
    <div className="flex flex-col gap-1">
      <span className="label">{t("continuita.block.plans")}</span>
      {plans.length === 0 && <span className="text-[12.5px] text-muted">{t("continuita.block.noPlans")}</span>}
      <ul className="m-0 flex list-none flex-col p-0">
        {plans.map((p) => (
          <li key={p.page.id} className="flex flex-wrap items-baseline gap-x-3 border-b border-hair py-1 text-[12.5px]">
            <Link to="/documenti/$key" params={{ key: p.page.key }} className="min-w-0 flex-[1_1_180px]">
              {p.page.title}
            </Link>
            {p.approved ? (
              <span className="text-ok">{t("continuita.block.planApproved", { v: p.approved_version ?? "—" })}</span>
            ) : (
              <span className="font-semibold text-flag">{t("continuita.block.planNotApproved")}</span>
            )}
            {can.write && (
              <button type="button" className="bg-transparent p-0 text-[12px] text-muted underline hover:text-danger" disabled={remove.isPending} onClick={() => remove.mutate(p)}>
                {t("continuita.block.unlinkPlan")}
              </button>
            )}
          </li>
        ))}
      </ul>
      {can.write && (
        <form
          className="flex flex-wrap gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            if (page) add.mutate();
          }}
        >
          <EntitySelect
            kind="element"
            types={["page"]}
            className="field min-w-0 flex-[1_1_200px]"
            noneLabel={t("continuita.block.pickPage")}
            value={page}
            onChange={(v: string) => setPage(v)}
            aria-label={t("continuita.block.pickPage")}
          />
          <button className="btn" disabled={!page || add.isPending}>
            {t("continuita.block.linkPlan")}
          </button>
        </form>
      )}
      <ErrorNote error={add.error ?? remove.error} />
    </div>
  );
}

function LastTest({ test }: { test: TestBrief | null }) {
  const t = useT();
  return (
    <p className="m-0 text-[12.5px]">
      <span className="text-muted">{t("continuita.block.lastTest")} </span>
      {test ? (
        <>
          <Link to="/e/$key" params={{ key: test.key }} className="font-mono">
            {test.key}
          </Link>{" "}
          <span>{date(test.executed_on ?? test.planned_on)}</span>
          {test.outcome && <span className={`ml-2 ${test.outcome === "failed" ? "font-semibold text-danger" : ""}`}>{t(`continuita.outcome.${test.outcome}`)}</span>}
        </>
      ) : (
        <span className="text-muted">{t("continuita.block.noTest")}</span>
      )}
    </p>
  );
}

function Issues({ items }: { items: Issue[] }) {
  const t = useT();
  if (items.length === 0) return <p className="m-0 text-[12.5px] text-ok">{t("continuita.block.noIssues")}</p>;
  return (
    <div className="flex flex-col gap-1">
      <span className="label">{t("continuita.block.issues", { count: items.length })}</span>
      <ul className="m-0 flex list-none flex-col p-0">
        {items.map((i) => (
          <IssueRow key={i.ref} issue={i} />
        ))}
      </ul>
    </div>
  );
}
