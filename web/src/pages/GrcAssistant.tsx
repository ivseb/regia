// GRC assistito (replica/ai.md §2 punto 5, ondata 5): collegamento evidenza → controllo nei due
// versi, bozze di policy e runbook, bozza della notifica DORA. Compare solo con l'AI accesa e la
// funzione «grc» pronta: senza AI le schede e le notifiche restano identiche. Ogni proposta si
// accetta una per una; le bozze si rileggono e si salvano solo con il clic della persona.
import { useState, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { aiApi } from "../aiApi";
import { grcAiApi, type DocKind, type DraftVersion, type FieldStatus, type GrcMapResult, type GrcProposal, type NoticeField } from "../grcAiApi";
import { kb } from "../knowledgeApi";
import { Select } from "../select";
import { ErrorNote } from "../ui";
import { useT } from "../i18n/react";
import { getLang, type Key } from "../i18n/core";
import { fmtDateTime } from "../i18n/format";
import { LinkedText } from "./Assistente";

/** Lo stato dell'AI per il GRC: pronto solo con l'AI accesa e la funzione «grc». */
export function useGrcAI() {
  const status = useQuery({ queryKey: ["ai-status"], queryFn: aiApi.status, staleTime: 60_000, retry: false });
  const st = status.data;
  const ready = !!st?.enabled && !!st.features?.grc;
  return { ready, masked: !!st?.masked, blocked: st?.budget.state === "blocked" };
}

function Frame({ id, title, children }: { id: string; title: string; children: ReactNode }) {
  const t = useT();
  const { masked } = useGrcAI();
  return (
    <section aria-labelledby={id} className="flex min-w-0 max-w-3xl flex-col gap-3 border border-hair bg-side p-3" data-testid="assistente-grc">
      <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
        <h3 id={id} className="label m-0">
          {title}
        </h3>
        <span className="text-[12px] text-muted">{masked ? t("ai.card.noteMasked") : t("ai.card.note")}</span>
      </div>
      {children}
    </section>
  );
}

function Busy({ on }: { on: boolean }) {
  const t = useT();
  return on ? <p className="m-0 text-muted">{t("ai.card.working")}</p> : null;
}

// ---------------------------------------------------------------------
// Collegamento evidenza → controllo

function ProposalItem({ p, mode }: { p: GrcProposal; mode: "evidenza" | "controllo" }) {
  const t = useT();
  const qc = useQueryClient();
  const other = mode === "evidenza" ? p.control : p.evidence;
  const decide = useMutation({
    mutationFn: (accept: boolean) => grcAiApi.decide({ evidence: p.evidence.key, control: p.control.key, coverage: p.coverage, reason: p.reason, accept }),
    onSuccess: (_, accept) => {
      if (!accept) return;
      qc.invalidateQueries({ queryKey: ["grc"] });
      qc.invalidateQueries({ queryKey: ["activity"] });
    },
  });
  const done = decide.isSuccess ? (decide.variables ? "accepted" : "rejected") : "";
  return (
    <li className="flex min-w-0 flex-col gap-1.5 border-b border-hair py-2.5" data-testid="proposta-grc">
      <p className="m-0 flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5">
        <Link to="/e/$key" params={{ key: other.key }} search={{ scheda: "dettagli" }} className="touch-44 inline-flex items-center font-mono text-[12.5px]">
          {other.key}
        </Link>
        <span className="min-w-0 [overflow-wrap:anywhere]">{other.title}</span>
        <span className="border border-hair px-1.5 text-[12px]">{t(p.coverage === "full" ? "ai.grc.coverage.full" : "ai.grc.coverage.partial")}</span>
      </p>
      {p.reason && <p className="m-0 text-[13px] [overflow-wrap:anywhere]">{p.reason}</p>}
      {p.requirements.length > 0 && (
        <p className="m-0 text-[12.5px] text-muted [overflow-wrap:anywhere]">{t("ai.grc.requirements", { list: p.requirements.join(", ") })}</p>
      )}
      <ErrorNote error={decide.error} />
      {done ? (
        <p className="m-0 text-[13px] text-ok" role="status">
          {t(done === "accepted" ? "ai.grc.linked" : "ai.grc.discarded")}
        </p>
      ) : p.can_accept ? (
        <div className="flex flex-wrap gap-2">
          <button type="button" className="btn btn-primary touch-44" disabled={decide.isPending} onClick={() => decide.mutate(true)}>
            {t("ai.grc.accept", { key: other.key })}
          </button>
          <button type="button" className="btn touch-44" disabled={decide.isPending} onClick={() => decide.mutate(false)}>
            {t("ai.grc.discard")}
          </button>
        </div>
      ) : (
        <p className="m-0 text-[12.5px] text-muted">{t("ai.grc.cannotLink")}</p>
      )}
    </li>
  );
}

function MapResult({ r }: { r: GrcMapResult }) {
  const t = useT();
  return (
    <div className="flex min-w-0 flex-col gap-2 border-t border-hair pt-3" data-testid="risposta-grc">
      {r.text && <p className="m-0 [overflow-wrap:anywhere]">{r.text}</p>}
      <span className="label">{t(r.mode === "evidenza" ? "ai.grc.map.controlsTitle" : "ai.grc.map.evidenceTitle")}</span>
      {r.proposals.length === 0 ? (
        <p className="m-0 text-[13px]" role="status">
          {t("ai.grc.map.none", { count: r.candidates })}
        </p>
      ) : (
        <ul className="m-0 flex list-none flex-col p-0" aria-label={t("ai.grc.map.list")}>
          {r.proposals.map((p) => (
            <ProposalItem key={p.evidence.id + p.control.id} p={p} mode={r.mode} />
          ))}
        </ul>
      )}
      {r.mode === "controllo" && (
        <>
          <span className="label mt-1">{t("ai.grc.missing.title")}</span>
          {r.missing.length === 0 ? (
            <p className="m-0 text-[13px]">{t("ai.grc.missing.none")}</p>
          ) : (
            <ul className="m-0 flex list-none flex-col gap-1 p-0" data-testid="mancanze-grc">
              {r.missing.map((m, i) => (
                <li key={i} className="flex min-w-0 flex-wrap items-baseline gap-x-2 text-[13px]">
                  <span className="min-w-0 flex-1 basis-60 [overflow-wrap:anywhere]">{m.text}</span>
                  {m.requirement && <span className="font-mono text-[12px]">{m.requirement}</span>}
                  <span className="text-[12px] text-muted">{t(m.source === "regia" ? "ai.card.gap.byRegia" : "ai.grc.missing.byModel")}</span>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
      {(r.unverified ?? 0) > 0 && <p className="m-0 text-[12.5px] text-muted">{t("ai.grc.unverified", { count: r.unverified ?? 0 })}</p>}
    </div>
  );
}

function MapAssist({ subject, mode }: { subject: string; mode: "evidenza" | "controllo" }) {
  const t = useT();
  const qc = useQueryClient();
  const { blocked } = useGrcAI();
  const run = useMutation({
    mutationFn: () => grcAiApi.map(mode === "evidenza" ? { evidence: subject } : { control: subject }),
    onSuccess: (r) => qc.setQueryData(["ai-status"], r.status),
  });
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <p className="m-0 text-[13px] text-muted">{t(mode === "evidenza" ? "ai.grc.map.evidenceHelp" : "ai.grc.map.controlHelp")}</p>
      {blocked ? (
        <p className="m-0 text-[12.5px] text-flag">{t("ai.panel.budgetBlocked")}</p>
      ) : (
        <div>
          <button type="button" className="btn touch-44 whitespace-normal" disabled={run.isPending} onClick={() => run.mutate()}>
            {t(mode === "evidenza" ? "ai.grc.map.evidenceGo" : "ai.grc.map.controlGo")}
          </button>
        </div>
      )}
      <div aria-live="polite" className="flex min-w-0 flex-col gap-2">
        <Busy on={run.isPending} />
        <ErrorNote error={run.error} />
        {run.data && <MapResult key={run.submittedAt} r={run.data} />}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------
// Bozze di policy e runbook

function SaveDraft({ source, kind, title, text }: { source: string; kind: DocKind; title: string; text: string }) {
  const t = useT();
  const spaces = useQuery({ queryKey: ["kb-spaces"], queryFn: kb.spaces });
  const writable = (spaces.data?.items ?? []).filter((s) => s.rights.write);
  const [space, setSpace] = useState("");
  const save = useMutation({ mutationFn: () => grcAiApi.saveDraft({ source, kind, title, text, space: space || writable[0]?.key || "" }) });
  if (save.data)
    return (
      <p className="m-0 text-[13px] text-ok" role="status">
        {t("ai.grc.draft.saved", { state: save.data.state })}{" "}
        <Link to="/documenti/$key" params={{ key: save.data.key }} className="font-mono">
          {save.data.key}
        </Link>
      </p>
    );
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <ErrorNote error={spaces.error ?? save.error} />
      {spaces.isSuccess && writable.length === 0 ? (
        <p className="m-0 text-[13px] text-muted">{t("ai.wiki.promoteNoSpace")}</p>
      ) : (
        <div className="flex min-w-0 flex-wrap items-end gap-2">
          <label className="flex min-w-0 flex-col gap-1.5">
            <span className="label">{t("ai.wiki.promoteSpace")}</span>
            <Select className="field" value={space || writable[0]?.key || ""} onChange={(e) => setSpace(e.target.value)}>
              {writable.map((s) => (
                <option key={s.key} value={s.key}>
                  {s.name}
                </option>
              ))}
            </Select>
          </label>
          <button type="button" className="btn btn-primary touch-44 whitespace-normal" disabled={save.isPending || !text.trim() || writable.length === 0} onClick={() => save.mutate()}>
            {t("ai.grc.draft.save")}
          </button>
        </div>
      )}
      <p className="m-0 text-[12px] text-muted">{t("ai.grc.draft.saveHelp")}</p>
    </div>
  );
}

export function DraftAssist({ source }: { source: string }) {
  const t = useT();
  const qc = useQueryClient();
  const { blocked } = useGrcAI();
  const [open, setOpen] = useState(false);
  const [kind, setKind] = useState<DocKind>("policy");
  const [hint, setHint] = useState("");
  const [title, setTitle] = useState("");
  const [text, setText] = useState("");
  const run = useMutation({
    mutationFn: () => grcAiApi.draft(source, kind, hint.trim() || undefined),
    onSuccess: (r) => {
      qc.setQueryData(["ai-status"], r.status);
      setTitle(r.title);
      setText(r.text);
    },
  });
  const r = run.data;
  const id = `bozza-grc-${source}`;
  return (
    <div className="flex min-w-0 flex-col gap-2">
      <div>
        <button type="button" className="btn touch-44 whitespace-normal" aria-expanded={open} disabled={blocked} onClick={() => setOpen((o) => !o)}>
          {t("ai.grc.draft.open")}
        </button>
      </div>
      {open && !blocked && (
        <form
          className="flex min-w-0 flex-col gap-2 border-t border-hair pt-3"
          onSubmit={(e) => {
            e.preventDefault();
            run.mutate();
          }}
        >
          <fieldset className="m-0 flex flex-wrap gap-x-4 gap-y-1 border-0 p-0">
            <legend className="label mb-1 p-0">{t("ai.grc.draft.kind")}</legend>
            {(["policy", "runbook"] as DocKind[]).map((k) => (
              <label key={k} className="touch-44 inline-flex items-center gap-2">
                <input type="radio" name={`tipo-doc-${source}`} className="size-4 accent-[var(--accent)]" checked={kind === k} onChange={() => setKind(k)} />
                {t(k === "policy" ? "ai.grc.draft.policy" : "ai.grc.draft.runbook")}
              </label>
            ))}
          </fieldset>
          <label className="flex flex-col gap-1">
            <span className="label">{t("ai.card.hint")}</span>
            <input className="field" maxLength={500} value={hint} onChange={(e) => setHint(e.target.value)} placeholder={t("ai.grc.draft.hintPh")} />
          </label>
          <div>
            <button className="btn btn-primary touch-44" disabled={run.isPending}>
              {t("ai.card.write")}
            </button>
          </div>
        </form>
      )}
      <div aria-live="polite" className="flex min-w-0 flex-col gap-2">
        <Busy on={run.isPending} />
        <ErrorNote error={run.error} />
        {r && (
          <div className="flex min-w-0 flex-col gap-2 border-t border-hair pt-3" data-testid="bozza-grc">
            <label className="flex flex-col gap-1">
              <span className="label">{t("ai.grc.draft.title")}</span>
              <input className="field" maxLength={200} value={title} onChange={(e) => setTitle(e.target.value)} />
            </label>
            <label htmlFor={id} className="label">
              {t("ai.grc.draft.text")}
            </label>
            <textarea id={id} className="field min-h-64 font-mono text-[12.5px] leading-relaxed" value={text} onChange={(e) => setText(e.target.value)} />
            {r.refs.length > 0 && (
              <p className="m-0 text-[12.5px] [overflow-wrap:anywhere]">
                {t("ai.grc.draft.cited")} <LinkedText text={r.refs.map((x) => x.key).join(", ")} refs={r.refs} />
              </p>
            )}
            {(r.unverified ?? 0) > 0 && <p className="m-0 text-[12.5px] text-muted">{t("ai.grc.draft.unverified", { count: r.unverified ?? 0 })}</p>}
            <p className="m-0 text-[12px] text-muted">{t("ai.card.draftReview")}</p>
            <SaveDraft source={source} kind={r.kind} title={title} text={text} />
          </div>
        )}
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------
// Scheda dell'elemento: controllo, evidenza, rischio

/** Il riquadro del GRC assistito nella scheda; senza AI non compare nulla. */
export default function GrcElementAssist({ type, refKey }: { type: string; refKey: string }) {
  const t = useT();
  const { ready } = useGrcAI();
  if (!ready) return null;
  const id = `assistente-grc-${refKey}`;
  if (type === "evidence")
    return (
      <Frame id={id} title={t("ai.grc.title")}>
        <MapAssist subject={refKey} mode="evidenza" />
      </Frame>
    );
  if (type === "control")
    return (
      <Frame id={id} title={t("ai.grc.title")}>
        <MapAssist subject={refKey} mode="controllo" />
        <DraftAssist source={refKey} />
      </Frame>
    );
  if (type === "risk")
    return (
      <Frame id={id} title={t("ai.grc.title")}>
        <DraftAssist source={refKey} />
      </Frame>
    );
  return null;
}

/** Bozza di policy o runbook da un requisito normativo (pannello del requisito). */
export function RequirementAssist({ id }: { id: string }) {
  const t = useT();
  const { ready } = useGrcAI();
  if (!ready) return null;
  return (
    <Frame id={`assistente-grc-req-${id}`} title={t("ai.grc.title")}>
      <DraftAssist source={`req:${id}`} />
    </Frame>
  );
}

// ---------------------------------------------------------------------
// Bozza della notifica DORA

const SECTIONS: Record<number, string> = { 1: "ai.grc.notice.s1", 2: "ai.grc.notice.s2", 3: "ai.grc.notice.s3", 4: "ai.grc.notice.s4" };

function StatusChip({ s }: { s: FieldStatus }) {
  const t = useT();
  const tone = s === "compilato" ? "text-ok" : s === "mancante" ? "text-flag" : "text-muted";
  return <span className={`border border-hair px-1.5 text-[12px] ${tone}`}>{t(`ai.grc.status.${s}` as Key)}</span>;
}

function FieldRow({ f, value, onChange }: { f: NoticeField; value: string; onChange: (v: string) => void }) {
  const t = useT();
  const en = getLang() === "en";
  const id = `campo-dora-${f.num.replace(".", "-")}`;
  const empty = !value.trim();
  const status: FieldStatus = empty ? "mancante" : f.status === "mancante" ? "da_verificare" : f.status;
  return (
    <div className="flex min-w-0 flex-col gap-1 border-b border-hair py-2" data-testid={`campo-${f.num}`}>
      <div className="flex min-w-0 flex-wrap items-baseline gap-x-2 gap-y-0.5">
        <label htmlFor={id} className="min-w-0 flex-1 basis-56 text-[13px] font-semibold [overflow-wrap:anywhere]">
          <span className="font-mono">{f.num}</span> {en ? f.name_en : f.name}
        </label>
        <StatusChip s={status} />
      </div>
      {f.when && <p className="m-0 text-[12px] text-muted">{t("ai.grc.notice.when", { when: f.when })}</p>}
      <textarea id={id} className="field min-h-11 text-[13px]" rows={f.narrative ? 3 : 1} value={value} onChange={(e) => onChange(e.target.value)} />
      {!empty && f.source && <p className="m-0 text-[12px] text-muted [overflow-wrap:anywhere]">{t("ai.grc.notice.source", { source: f.source })}</p>}
      {empty && f.hint && <p className="m-0 text-[12px] text-muted [overflow-wrap:anywhere]">{t("ai.grc.notice.hint", { hint: f.hint })}</p>}
    </div>
  );
}

/** Le versioni salvate della bozza, con l'esportazione. Senza versioni non mostra nulla. */
export function NoticeVersions({ notification, items }: { notification: string; items?: DraftVersion[] }) {
  const t = useT();
  const q = useQuery({ queryKey: ["risk", "notice-versions", notification], queryFn: () => grcAiApi.versions(notification), enabled: !items });
  const list = items ?? q.data?.items ?? [];
  if (list.length === 0) return null;
  return (
    <details className="text-[13px]" data-testid="versioni-notifica">
      <summary className="touch-44 inline-flex cursor-pointer items-center text-muted">{t("ai.grc.versions.title", { count: list.length })}</summary>
      <ul className="m-0 mt-1 flex list-none flex-col gap-1 p-0">
        {list.map((v) => (
          <li key={v.version} className="flex min-w-0 flex-wrap items-baseline gap-x-3 gap-y-0.5">
            <span className="tabular">
              {t("ai.grc.versions.item", { version: v.version, at: fmtDateTime(v.created_at), who: v.created_by || "—" })}
            </span>
            <span className="text-[12px] text-muted">{t(v.source === "assistente" ? "ai.grc.versions.byAssistant" : "ai.grc.versions.byPerson")}</span>
            <a className="touch-44 inline-flex items-center" href={grcAiApi.exportUrl(notification, v.version, "md")} download>
              {t("ai.grc.versions.md")}
            </a>
            <a className="touch-44 inline-flex items-center" href={grcAiApi.exportUrl(notification, v.version, "json")} download>
              {t("ai.grc.versions.json")}
            </a>
          </li>
        ))}
      </ul>
    </details>
  );
}

/** Bozza della notifica DORA con l'assistente, dentro la notifica aperta. */
export function DoraNoticeAssist({ notification, onSaved }: { notification: string; onSaved: (content: string) => void }) {
  const t = useT();
  const qc = useQueryClient();
  const { ready, blocked } = useGrcAI();
  const [values, setValues] = useState<Record<string, string>>({});
  const run = useMutation({
    mutationFn: () => grcAiApi.notice(notification),
    onSuccess: (r) => {
      qc.setQueryData(["ai-status"], r.status);
      setValues(Object.fromEntries(r.fields.map((f) => [f.num, f.value])));
    },
  });
  const save = useMutation({
    mutationFn: () =>
      grcAiApi.saveNotice(
        notification,
        (run.data?.fields ?? []).map((f) => {
          const v = values[f.num] ?? "";
          return { num: f.num, value: v, status: !v.trim() ? "mancante" : v === f.value ? f.status : "da_verificare", source: v === f.value ? f.source : undefined };
        }),
      ),
    onSuccess: (r) => {
      onSaved(r.notification.content);
      qc.invalidateQueries({ queryKey: ["risk", "notice-versions", notification] });
      qc.invalidateQueries({ queryKey: ["activity"] });
    },
  });
  if (!ready) return <NoticeVersions notification={notification} />;
  const r = run.data;
  const missing = r ? r.fields.filter((f) => !(values[f.num] ?? "").trim()).length : 0;
  return (
    <Frame id={`assistente-dora-${notification}`} title={t("ai.grc.notice.title")}>
      <p className="m-0 text-[13px] text-muted">{t("ai.grc.notice.help")}</p>
      {blocked ? (
        <p className="m-0 text-[12.5px] text-flag">{t("ai.panel.budgetBlocked")}</p>
      ) : (
        <div>
          <button type="button" className="btn touch-44 whitespace-normal" disabled={run.isPending} onClick={() => run.mutate()}>
            {t(r ? "ai.grc.notice.again" : "ai.grc.notice.go")}
          </button>
        </div>
      )}
      <div aria-live="polite" className="flex min-w-0 flex-col gap-2">
        <Busy on={run.isPending} />
        <ErrorNote error={run.error} />
        {r && (
          <div className="flex min-w-0 flex-col gap-1" data-testid="bozza-dora">
            <p className="m-0 text-[13px]" role="status">
              {t("ai.grc.notice.summary", { fields: r.fields.length, missing, written: r.written })}
            </p>
            <p className="m-0 text-[12px] text-muted [overflow-wrap:anywhere]">{t("ai.grc.notice.sources", { list: r.sources.join("; ") })}</p>
            {[1, 2, 3, 4]
              .filter((s) => r.fields.some((f) => f.section === s))
              .map((s) => (
                <fieldset key={s} className="m-0 mt-2 flex min-w-0 flex-col border-0 p-0">
                  <legend className="label mb-1 p-0">{t(SECTIONS[s] as Key)}</legend>
                  {r.fields
                    .filter((f) => f.section === s)
                    .map((f) => (
                      <FieldRow key={f.num} f={f} value={values[f.num] ?? ""} onChange={(v) => setValues((x) => ({ ...x, [f.num]: v }))} />
                    ))}
                </fieldset>
              ))}
            {(r.unverified ?? 0) > 0 && <p className="m-0 text-[12.5px] text-muted">{t("ai.grc.notice.unverified", { count: r.unverified ?? 0 })}</p>}
            <ErrorNote error={save.error} />
            {save.data ? (
              <p className="m-0 text-[13px] text-ok" role="status">
                {t("ai.grc.notice.saved", { version: save.data.version.version })}
              </p>
            ) : (
              <div className="flex flex-wrap gap-2 pt-1">
                <button type="button" className="btn btn-primary touch-44 whitespace-normal" disabled={save.isPending} onClick={() => save.mutate()}>
                  {t("ai.grc.notice.save")}
                </button>
              </div>
            )}
            <p className="m-0 text-[12px] text-muted">{t("ai.grc.notice.notSent")}</p>
          </div>
        )}
      </div>
      <NoticeVersions notification={notification} items={save.data ? undefined : r?.versions.length ? r.versions : undefined} />
    </Frame>
  );
}
