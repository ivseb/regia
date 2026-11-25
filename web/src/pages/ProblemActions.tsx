// Dal problema all'azione (replica/usabilita.md, proposta 5): sotto una vulnerabilità, un blocco
// del gate, un requisito o un'aderenza mancante, le azioni dirette (elemento di lavoro nel team
// dell'applicazione, eccezione con il motivo già scritto) e tutto ciò che è stato fatto a partire
// da quel problema, anche da altri moduli. I dati del problema non si riscrivono.
import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { problemApi, type ProblemContext, type ProblemKind, type ProblemLink } from "../crisisApi";
import { vulnApi } from "../vulnApi";
import { EntitySelect } from "../select";
import { ErrorNote } from "../ui";
import { Dialog } from "./work-ui";
import { useT } from "../i18n/react";
import { fmtDate } from "../i18n/format";

type Act = "work_item" | "exception";

const plusDays = (n: number) => {
  const d = new Date();
  d.setDate(d.getDate() + n);
  return d.toISOString().slice(0, 10);
};

export function ProblemActions({ kind, refKey, control, actions = ["work_item"], compact }: {
  kind: ProblemKind;
  refKey: string;
  control?: string;
  actions?: Act[];
  compact?: boolean;
}) {
  const t = useT();
  const q = useQuery({ queryKey: ["problems", kind, refKey, control ?? ""], queryFn: () => problemApi.context(kind, refKey, control) });
  const [dialog, setDialog] = useState<Act | null>(null);
  const x = q.data;
  if (q.error) return <ErrorNote error={q.error} />;
  if (!x) return null;
  return (
    <section className="flex flex-col gap-1.5" aria-label={t("crisi.problem.title")}>
      <span className="label text-muted">{t("crisi.problem.title")}</span>
      {x.links.length > 0 ? (
        <ul className="m-0 flex list-none flex-col gap-1 p-0">
          {x.links.map((l) => (
            <li key={`${l.action}-${l.id}`} className="text-[12.5px]">
              <LinkLine l={l} />
            </li>
          ))}
        </ul>
      ) : (
        <span className="text-[12.5px] text-muted">{t("crisi.problem.none")}</span>
      )}
      {(x.can.work_item || actions.includes("exception")) && (
        <div className="flex flex-wrap gap-2">
          {actions.includes("work_item") && x.can.work_item && (
            <button type="button" className={`btn ${compact ? "py-1 text-[12.5px]" : ""}`} onClick={() => setDialog("work_item")}>
              {t("crisi.problem.createWork")}
            </button>
          )}
          {actions.includes("exception") && x.can.work_item && (
            <button type="button" className={`btn ${compact ? "py-1 text-[12.5px]" : ""}`} onClick={() => setDialog("exception")}>
              {t("crisi.problem.requestException")}
            </button>
          )}
        </div>
      )}
      {dialog === "work_item" && <WorkDialog kind={kind} refKey={refKey} control={control} ctx={x} onClose={() => setDialog(null)} />}
      {dialog === "exception" && <ExceptionDialog refKey={refKey} ctx={x} onClose={() => setDialog(null)} />}
    </section>
  );
}

function LinkLine({ l }: { l: ProblemLink }) {
  const t = useT();
  const label = t(`crisi.problem.action.${l.action}`);
  const status = l.status ? ` · ${statusText(t, l)}` : "";
  const by = l.by ? ` · ${l.by}` : "";
  const when = l.at ? ` · ${fmtDate(l.at)}` : "";
  return (
    <span>
      <span className="font-semibold">{label}</span>{" "}
      {l.element ? (
        <Link to="/e/$key" params={{ key: l.element }}>
          <span className="font-mono">{l.element}</span>
          {l.action === "work_item" ? ` ${l.label}` : ""}
        </Link>
      ) : l.url ? (
        <a href={l.url} target="_blank" rel="noreferrer">{l.label || l.url}</a>
      ) : (
        <span className="font-mono">{l.ref}</span>
      )}
      <span className="text-muted">{status}{by}{when}</span>
    </span>
  );
}

function statusText(t: ReturnType<typeof useT>, l: ProblemLink): string {
  const known = ["pending", "approved", "rejected", "revoked", "expired", "open", "merged", "closed", "draft"];
  return known.includes(l.status) ? t(`crisi.problem.status.${l.status as "pending"}`) : l.status;
}

function WorkDialog({ kind, refKey, control, ctx, onClose }: { kind: ProblemKind; refKey: string; control?: string; ctx: ProblemContext; onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [title, setTitle] = useState(ctx.title);
  const [body, setBody] = useState(ctx.body);
  const [team, setTeam] = useState(ctx.team?.id ?? "");
  const [due, setDue] = useState("");
  const m = useMutation({
    mutationFn: () => problemApi.createWork({ kind, ref: refKey, control, title, body_text: body, team_id: team, due: due || undefined }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["problems"] });
      void qc.invalidateQueries({ queryKey: ["elements"] });
      onClose();
    },
  });
  return (
    <Dialog title={t("crisi.problem.createWork")} onClose={onClose} wide>
      <form className="flex flex-col gap-3" onSubmit={(e) => { e.preventDefault(); m.mutate(); }}>
        {ctx.application && (
          <p className="m-0 text-[12.5px] text-muted">{t("crisi.problem.workHint", { app: `${ctx.application.key} ${ctx.application.title}` })}</p>
        )}
        <label className="flex flex-col gap-1">
          <span className="label">{t("crisi.problem.fieldTitle")}</span>
          <input className="field" required value={title} onChange={(e) => setTitle(e.target.value)} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("crisi.problem.fieldBody")}</span>
          <textarea className="field min-h-28" value={body} onChange={(e) => setBody(e.target.value)} />
        </label>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <label className="flex flex-col gap-1">
            <span className="label">{t("crisi.problem.fieldTeam")}</span>
            <EntitySelect kind="team" className="field" value={team} onChange={(v) => setTeam(v)} aria-label={t("crisi.problem.fieldTeam")} />
            {!ctx.team && <span className="text-[12px] text-flag">{t("crisi.problem.noTeam")}</span>}
          </label>
          <label className="flex flex-col gap-1">
            <span className="label">{t("crisi.problem.fieldDue")}</span>
            <input className="field" type="date" value={due} onChange={(e) => setDue(e.target.value)} />
          </label>
        </div>
        <ErrorNote error={m.error} />
        <div className="flex gap-2">
          <button className="btn btn-primary min-h-[44px]" disabled={m.isPending || !title.trim() || !team}>{t("crisi.problem.create")}</button>
          <button type="button" className="btn min-h-[44px]" onClick={onClose}>{t("crisi.problem.cancel")}</button>
        </div>
      </form>
    </Dialog>
  );
}

function ExceptionDialog({ refKey, ctx, onClose }: { refKey: string; ctx: ProblemContext; onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [reason, setReason] = useState(ctx.reason);
  const [until, setUntil] = useState(plusDays(30));
  const m = useMutation({
    mutationFn: () => vulnApi.requestException({ ids: [refKey], reason, until }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["problems"] });
      void qc.invalidateQueries({ queryKey: ["vuln"] });
      void qc.invalidateQueries({ queryKey: ["vuln-item"] });
      onClose();
    },
  });
  return (
    <Dialog title={t("crisi.problem.requestException")} onClose={onClose} wide>
      <form className="flex flex-col gap-3" onSubmit={(e) => { e.preventDefault(); m.mutate(); }}>
        <p className="m-0 text-[12.5px] text-muted">{t("crisi.problem.exceptionHint")}</p>
        <label className="flex flex-col gap-1">
          <span className="label">{t("crisi.problem.reason")}</span>
          <textarea className="field min-h-24" required value={reason} onChange={(e) => setReason(e.target.value)} />
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{t("crisi.problem.until")}</span>
          <input className="field" type="date" required min={plusDays(1)} value={until} onChange={(e) => setUntil(e.target.value)} />
        </label>
        <ErrorNote error={m.error} />
        <div className="flex gap-2">
          <button className="btn btn-primary min-h-[44px]" disabled={m.isPending || reason.trim() === ctx.reason.trim() || !reason.trim()}>{t("crisi.problem.send")}</button>
          <button type="button" className="btn min-h-[44px]" onClick={onClose}>{t("crisi.problem.cancel")}</button>
        </div>
      </form>
    </Dialog>
  );
}
