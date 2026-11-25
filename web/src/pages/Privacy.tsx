// Vista DPO (ondata 3, replica/usabilita/governo.md, C8): home con ciò che chiede attenzione, registro dei
// trattamenti, DPIA, violazioni dei dati personali con le 72 ore per la notifica e flussi di dati dalle
// applicazioni. I riferimenti al GDPR sono da verificare. Telefono di primo livello: una colonna sola,
// bersagli da 44 px, nessuno scorrimento orizzontale.
import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { privacyApi, type AppRef, type Breach, type Home, type Treatment, type TreatmentInput } from "../privacyApi";
import { useT } from "../i18n/react";
import { OptionSelect } from "../select";
import { PageHeader, useMe } from "../shell";
import { ErrorNote, Loading } from "../ui";
import { crumbs } from "../crumbs";
import { Dialog } from "./work-ui";
import { Countdown, dateTime, fromLocalInput, toLocalInput } from "./riskUi";
import { DpiaDialog, DpiaTab, TreatmentDialog, TreatmentsTab, emptyTreatment, useTreatments } from "./PrivacyRegister";
import { Block, Check, DataBadges, DpiaBadge, Empty, IssueBadge, TField } from "./privacy-ui";

export type PrivacyTab = "panoramica" | "trattamenti" | "dpia" | "violazioni" | "flussi";
const TABS: PrivacyTab[] = ["panoramica", "trattamenti", "dpia", "violazioni", "flussi"];

/** Finestra aperta: la scheda di un trattamento (nuovo se treatment è null), oppure la sua DPIA. */
type Open = { kind: "treatment"; treatment: Treatment | null; initial?: TreatmentInput } | { kind: "dpia"; treatment: Treatment } | null;

export function PrivacyPage({ tab }: { tab: PrivacyTab }) {
  const t = useT();
  const perms = useMe().data?.active_tenant?.permissions ?? [];
  const canWrite = perms.includes("*") || perms.includes("privacy.write");
  const [open, setOpen] = useState<Open>(null);
  const edit = (x: Treatment) => setOpen({ kind: "treatment", treatment: x });
  const dpia = (x: Treatment) => setOpen({ kind: "dpia", treatment: x });
  const fresh = (apps: string[] = []) => setOpen({ kind: "treatment", treatment: null, initial: emptyTreatment(apps) });
  return (
    <>
      <PageHeader path={`${crumbs("governance")} / ${t("governance.privacy.crumb")}`} title={t("governance.privacy.title")} />
      <div role="tablist" aria-label={t("governance.privacy.tabs")} className="flex flex-wrap gap-x-5 gap-y-0 border-b border-hair px-4 pt-1 md:px-7">
        {TABS.map((k) => (
          <Link
            key={k}
            to="/privacy"
            search={{ scheda: k === "panoramica" ? undefined : k }}
            role="tab"
            aria-selected={tab === k}
            className={`touch-44 inline-flex items-center pb-2.5 no-underline ${tab === k ? "border-b-2 border-fg font-semibold text-fg" : "text-muted hover:text-fg"}`}
          >
            {t(`governance.privacy.tab.${k}`)}
          </Link>
        ))}
      </div>
      <div className="flex min-w-0 flex-col gap-6 px-4 py-5 md:px-7">
        {tab === "panoramica" && <HomeTab canWrite={canWrite} onEdit={edit} onDpia={dpia} onNew={fresh} />}
        {tab === "trattamenti" && <TreatmentsTab canWrite={canWrite} onEdit={edit} onNew={() => fresh()} onDpia={dpia} />}
        {tab === "dpia" && <DpiaTab canWrite={canWrite} onDpia={dpia} />}
        {tab === "violazioni" && <BreachesTab canWrite={canWrite} />}
        {tab === "flussi" && <FlowsTab canWrite={canWrite} onNew={fresh} />}
      </div>
      {open?.kind === "treatment" && (
        <TreatmentDialog key={open.treatment?.id ?? "new"} treatment={open.treatment} initial={open.initial} canWrite={canWrite} onClose={() => setOpen(null)} onDpia={dpia} />
      )}
      {open?.kind === "dpia" && <DpiaDialog key={open.treatment.id} treatment={open.treatment} canWrite={canWrite} onClose={() => setOpen(null)} />}
    </>
  );
}

// ---------------------------------------------------------------------
// Home

function Tile({ n, label, to, hot }: { n: number; label: string; to: PrivacyTab; hot?: boolean }) {
  return (
    <Link
      to="/privacy"
      search={{ scheda: to === "panoramica" ? undefined : to }}
      className={`flex min-h-[44px] min-w-0 flex-col gap-0.5 border px-3 py-2 no-underline ${hot && n > 0 ? "border-flag" : "border-hair"}`}
    >
      <span className={`text-[26px] font-bold leading-none tabular ${hot && n > 0 ? "text-flag" : ""}`}>{n}</span>
      <span className="break-words text-[12.5px] text-muted">{label}</span>
    </Link>
  );
}

function HomeTab({ canWrite, onEdit, onDpia, onNew }: { canWrite: boolean; onEdit: (x: Treatment) => void; onDpia: (x: Treatment) => void; onNew: (apps?: string[]) => void }) {
  const t = useT();
  const q = useQuery({ queryKey: ["privacy", "home"], queryFn: privacyApi.home });
  const treatments = useTreatments().data?.items ?? [];
  const byId = (id: string) => treatments.find((x) => x.id === id);
  if (q.error) return <ErrorNote error={q.error} />;
  const h: Home | undefined = q.data;
  if (!h) return <Loading rows={4} />;
  const c = h.counts;
  const nothing = c.no_basis + c.dpia_missing + c.dpia_in_review + c.open_breaches + c.unlinked_apps === 0;
  return (
    <>
      <div className="grid grid-cols-2 gap-2 md:grid-cols-3 lg:grid-cols-6">
        <Tile n={c.treatments} label={t("governance.privacy.home.treatments")} to="trattamenti" />
        <Tile n={c.open_breaches} label={t("governance.privacy.home.breaches")} to="violazioni" hot />
        <Tile n={c.no_basis} label={t("governance.privacy.home.noBasis")} to="trattamenti" hot />
        <Tile n={c.dpia_missing} label={t("governance.privacy.home.dpiaMissing")} to="dpia" hot />
        <Tile n={c.dpia_in_review} label={t("governance.privacy.home.dpiaReview")} to="dpia" hot />
        <Tile n={c.unlinked_apps} label={t("governance.privacy.home.unlinked")} to="flussi" hot />
      </div>
      {c.treatments === 0 && (
        <Empty>
          {t("governance.privacy.home.firstRun")}{" "}
          {canWrite && (
            <button type="button" className="btn btn-primary mt-2 min-h-[44px] justify-center" onClick={() => onNew()}>
              {t("governance.privacy.treatment.new")}
            </button>
          )}
        </Empty>
      )}
      {c.treatments > 0 && nothing && <Empty>{t("governance.privacy.home.allClear")}</Empty>}
      {h.breaches.length > 0 && (
        <Block title={t("governance.privacy.home.breachesTitle")} count={c.open_breaches}>
          <ul className="m-0 flex list-none flex-col gap-2 p-0">
            {h.breaches.map((b) => (
              <li key={b.id}>
                <BreachClock b={b} compact />
              </li>
            ))}
          </ul>
          <Link to="/privacy" search={{ scheda: "violazioni" }} className="inline-flex min-h-[44px] items-center">
            {t("governance.privacy.home.allBreaches")}
          </Link>
        </Block>
      )}
      {h.no_basis.length > 0 && (
        <Block title={t("governance.privacy.home.noBasisTitle")} count={c.no_basis}>
          <TreatmentRows items={h.no_basis} action={canWrite ? t("governance.privacy.home.setBasis") : t("governance.privacy.home.view")} onAction={(x) => onEdit(byId(x.id) ?? x)} />
        </Block>
      )}
      {h.dpia_missing.length > 0 && (
        <Block title={t("governance.privacy.home.dpiaMissingTitle")} count={c.dpia_missing}>
          <TreatmentRows items={h.dpia_missing} action={canWrite ? t("governance.privacy.dpia.start") : t("governance.privacy.home.view")} onAction={(x) => onDpia(byId(x.id) ?? x)} />
        </Block>
      )}
      {h.in_review.length > 0 && (
        <Block title={t("governance.privacy.home.dpiaReviewTitle")} count={c.dpia_in_review}>
          <ul className="m-0 flex list-none flex-col p-0">
            {h.in_review.map((d) => (
              <li key={d.id} className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-hair py-2">
                <span className="min-w-0 flex-[1_1_14rem] break-words font-semibold">{d.treatment}</span>
                <DpiaBadge status="in_review" />
                <button type="button" className="btn min-h-[44px] justify-center md:min-h-[28px]" onClick={() => { const x = byId(d.treatment_id); if (x) onDpia(x); }}>
                  {t("governance.privacy.dpia.open")}
                </button>
              </li>
            ))}
          </ul>
        </Block>
      )}
      {h.unlinked.length > 0 && (
        <Block title={t("governance.privacy.home.unlinkedTitle")} count={c.unlinked_apps}>
          <UnlinkedRows apps={h.unlinked} canWrite={canWrite} onNew={onNew} />
          {c.unlinked_apps > h.unlinked.length && (
            <Link to="/privacy" search={{ scheda: "flussi" }} className="inline-flex min-h-[44px] items-center">
              {t("governance.privacy.home.allUnlinked", { count: c.unlinked_apps })}
            </Link>
          )}
        </Block>
      )}
      <p className="m-0 text-[12px] text-muted">{t("governance.privacy.legalNote")}</p>
    </>
  );
}

function TreatmentRows({ items, action, onAction }: { items: Treatment[]; action: string; onAction: (x: Treatment) => void }) {
  return (
    <ul className="m-0 flex list-none flex-col p-0">
      {items.map((x) => (
        <li key={x.id} className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-hair py-2">
          <span className="min-w-0 flex-[1_1_14rem] break-words font-semibold">{x.name}</span>
          {x.issues.map((i) => (
            <IssueBadge key={i} issue={i} />
          ))}
          <button type="button" className="btn min-h-[44px] justify-center md:min-h-[28px]" onClick={() => onAction(x)}>
            {action}
          </button>
        </li>
      ))}
    </ul>
  );
}

function UnlinkedRows({ apps, canWrite, onNew }: { apps: AppRef[]; canWrite: boolean; onNew: (apps?: string[]) => void }) {
  const t = useT();
  return (
    <ul className="m-0 flex list-none flex-col p-0">
      {apps.map((a) => (
        <li key={a.id} className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-hair py-2">
          <Link to="/e/$key" params={{ key: a.key }} className="min-w-0 flex-[1_1_14rem] break-words font-semibold">
            {a.name}
          </Link>
          <DataBadges app={a} />
          {canWrite && (
            <button type="button" className="btn min-h-[44px] justify-center md:min-h-[28px]" onClick={() => onNew([a.id])}>
              {t("governance.privacy.flows.createTreatment")}
            </button>
          )}
        </li>
      ))}
    </ul>
  );
}

// ---------------------------------------------------------------------
// Violazioni dei dati

/** L'orologio delle 72 ore di una violazione: quanto manca alla notifica all'autorità, o come è andata. */
function BreachClock({ b, compact }: { b: Breach; compact?: boolean }) {
  const t = useT();
  const open = b.status === "open" || b.status === "overdue";
  const tone = b.status === "overdue" ? "border-danger" : open && b.hours_left < 24 ? "border-flag" : "border-rule";
  return (
    <div className={`flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 border-l-4 bg-sel px-3 py-2 ${tone}`}>
      <div className="flex min-w-0 flex-[1_1_14rem] flex-col gap-0.5">
        <Link to="/e/$key" params={{ key: b.key }} className="break-words font-semibold">
          <span className="font-mono text-[12px] font-normal text-muted">{b.key}</span> {b.title}
        </Link>
        {!compact && <span className="text-[12.5px] text-muted">{t("governance.privacy.breach.detected", { when: dateTime(b.detected_at) })}</span>}
      </div>
      <div className="flex min-w-0 flex-col gap-0.5" role="group" aria-label={t("governance.privacy.breach.clock")}>
        {open ? (
          <>
            <span className="label text-flag">{t("governance.privacy.breach.deadline", { hours: 72 })}</span>
            <span className="text-[16px]">
              <Countdown due={b.deadline_at} />
            </span>
            <span className="text-[12px] text-muted tabular">{t("governance.privacy.breach.by", { when: dateTime(b.deadline_at) })}</span>
          </>
        ) : (
          <span className="text-[13px] font-semibold">
            {b.status === "not_required"
              ? t("governance.privacy.breach.status.not_required")
              : t(`governance.privacy.breach.status.${b.status}`, { when: dateTime(b.notified_at) })}
          </span>
        )}
      </div>
    </div>
  );
}

function BreachesTab({ canWrite }: { canWrite: boolean }) {
  const t = useT();
  const [all, setAll] = useState(false);
  const q = useQuery({ queryKey: ["privacy", "breaches", all], queryFn: () => privacyApi.breaches(all) });
  const [edit, setEdit] = useState<Breach | null>(null);
  const [notify, setNotify] = useState<Breach | null>(null);
  const items = q.data?.items ?? [];
  return (
    <div className="flex flex-col gap-4">
      <p className="m-0 text-[13px]">{t("governance.privacy.breach.help", { hours: q.data?.hours ?? 72 })}</p>
      <p className="m-0 text-[12px] text-muted">{t("governance.privacy.breach.legalNote")}</p>
      <Check checked={all} onChange={setAll}>
        {t("governance.privacy.breach.showAll")}
      </Check>
      <ErrorNote error={q.error} />
      {!q.data && !q.error && <Loading rows={3} />}
      {q.data && items.length === 0 && <Empty>{t("governance.privacy.breach.empty")}</Empty>}
      <ul className="m-0 flex list-none flex-col gap-4 p-0" aria-label={t("governance.privacy.tab.violazioni")}>
        {items.map((b) => (
          <li key={b.id} className="flex min-w-0 flex-col gap-2">
            <BreachClock b={b} />
            <p className="m-0 flex flex-wrap items-center gap-x-2 gap-y-1 text-[13px]">
              <span className="text-muted">{t(`governance.privacy.breach.source.${b.source}`)}</span>
              {b.applications.map((a) => (
                <span key={a.id} className="inline-flex items-center gap-1.5">
                  {a.name} <DataBadges app={a} />
                </span>
              ))}
            </p>
            {b.treatments.length > 0 && (
              <p className="m-0 text-[13px]">
                <span className="text-muted">{t("governance.privacy.breach.treatments")}: </span>
                {b.treatments.map((x) => x.name).join(", ")}
              </p>
            )}
            {b.note && <p className="m-0 text-[13px] text-muted">{b.note}</p>}
            {b.not_required && b.not_required_reason && <p className="m-0 text-[13px] text-muted">{b.not_required_reason}</p>}
            <div className="flex flex-wrap items-center gap-2">
              {canWrite && b.personal && (b.status === "open" || b.status === "overdue") && (
                <button type="button" className="btn btn-primary min-h-[44px] justify-center" onClick={() => setNotify(b)}>
                  {t("governance.privacy.breach.registerNotice")}
                </button>
              )}
              {canWrite && b.personal && !(b.status === "open" || b.status === "overdue") && (
                <button type="button" className="btn min-h-[44px] justify-center" onClick={() => setNotify(b)}>
                  {t("governance.privacy.breach.editNotice")}
                </button>
              )}
              {canWrite && (
                <button type="button" className="btn btn-quiet min-h-[44px] justify-center" onClick={() => setEdit(b)}>
                  {b.personal ? t("governance.privacy.breach.link") : t("governance.privacy.breach.mark")}
                </button>
              )}
              <Link to="/crisi/$key" params={{ key: b.key }} className="inline-flex min-h-[44px] items-center">
                {t("governance.privacy.breach.crisis")}
              </Link>
            </div>
          </li>
        ))}
      </ul>
      {edit && <BreachDialog breach={edit} onClose={() => setEdit(null)} />}
      {notify && <NotifyDialog breach={notify} onClose={() => setNotify(null)} />}
    </div>
  );
}

function BreachDialog({ breach, onClose }: { breach: Breach; onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const treatments = useTreatments().data?.items ?? [];
  const [personal, setPersonal] = useState(true);
  const [note, setNote] = useState(breach.note);
  const [ids, setIds] = useState(breach.treatments.map((x) => x.id));
  const save = useMutation({
    mutationFn: () => privacyApi.setBreach(breach.id, { personal, note, treatment_ids: personal ? ids : [] }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["privacy"] });
      onClose();
    },
  });
  return (
    <Dialog title={`${breach.key} · ${breach.title}`} onClose={onClose}>
      <form
        className="flex flex-col gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <div role="radiogroup" aria-label={t("governance.privacy.breach.markTitle")} className="flex flex-col">
          <label className="flex min-h-[44px] items-center gap-3">
            <input type="radio" name="personal" checked={personal} onChange={() => setPersonal(true)} />
            <span>{t("governance.privacy.breach.isPersonal")}</span>
          </label>
          <label className="flex min-h-[44px] items-center gap-3">
            <input type="radio" name="personal" checked={!personal} onChange={() => setPersonal(false)} />
            <span>{t("governance.privacy.breach.notPersonal")}</span>
          </label>
        </div>
        {personal && (
          <TField label={t("governance.privacy.breach.treatments")}>
            <OptionSelect
              multiple
              value={ids}
              onChange={setIds}
              options={treatments.map((x) => ({ value: x.id, label: x.name }))}
              placeholder={t("governance.privacy.breach.treatmentsPh")}
              aria-label={t("governance.privacy.breach.treatments")}
            />
          </TField>
        )}
        <TField label={t("governance.privacy.breach.note")}>
          <textarea className="field" rows={3} value={note} onChange={(e) => setNote(e.target.value)} />
        </TField>
        <ErrorNote error={save.error} />
        <div className="flex gap-2">
          <button type="submit" className="btn btn-primary min-h-[44px] justify-center" disabled={save.isPending}>
            {t("governance.privacy.save")}
          </button>
          <button type="button" className="btn btn-quiet min-h-[44px] justify-center" onClick={onClose}>
            {t("governance.privacy.close")}
          </button>
        </div>
      </form>
    </Dialog>
  );
}

function NotifyDialog({ breach, onClose }: { breach: Breach; onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [mode, setMode] = useState<"sent" | "none">(breach.not_required ? "none" : "sent");
  const [at, setAt] = useState(toLocalInput(breach.notified_at ?? new Date().toISOString()));
  const [reason, setReason] = useState(breach.not_required_reason);
  const done = () => {
    void qc.invalidateQueries({ queryKey: ["privacy"] });
    onClose();
  };
  const save = useMutation({
    mutationFn: () => (mode === "sent" ? privacyApi.notify(breach.id, { notified_at: fromLocalInput(at) }) : privacyApi.notify(breach.id, { not_required: true, reason })),
    onSuccess: done,
  });
  const clear = useMutation({ mutationFn: () => privacyApi.notify(breach.id, { clear: true }), onSuccess: done });
  const registered = breach.notified_at !== null || breach.not_required;
  return (
    <Dialog title={t("governance.privacy.breach.noticeTitle", { key: breach.key })} onClose={onClose}>
      <form
        className="flex flex-col gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          save.mutate();
        }}
      >
        <p className="m-0 text-[13px]">
          {t("governance.privacy.breach.deadline", { hours: 72 })}: <span className="font-semibold tabular">{dateTime(breach.deadline_at)}</span>
        </p>
        <div role="radiogroup" aria-label={t("governance.privacy.breach.noticeTitle", { key: breach.key })} className="flex flex-col">
          <label className="flex min-h-[44px] items-center gap-3">
            <input type="radio" name="mode" checked={mode === "sent"} onChange={() => setMode("sent")} />
            <span>{t("governance.privacy.breach.sent")}</span>
          </label>
          <label className="flex min-h-[44px] items-center gap-3">
            <input type="radio" name="mode" checked={mode === "none"} onChange={() => setMode("none")} />
            <span>{t("governance.privacy.breach.notRequired")}</span>
          </label>
        </div>
        {mode === "sent" ? (
          <TField label={t("governance.privacy.breach.sentAt")}>
            <input type="datetime-local" className="field" value={at} onChange={(e) => setAt(e.target.value)} />
          </TField>
        ) : (
          <TField label={t("governance.privacy.breach.reason")} hint={t("governance.privacy.breach.reasonHint")}>
            <textarea className="field" rows={3} value={reason} onChange={(e) => setReason(e.target.value)} />
          </TField>
        )}
        <ErrorNote error={save.error ?? clear.error} />
        <div className="flex flex-wrap gap-2">
          <button type="submit" className="btn btn-primary min-h-[44px] justify-center" disabled={save.isPending}>
            {t("governance.privacy.save")}
          </button>
          {registered && (
            <button type="button" className="btn min-h-[44px] justify-center" disabled={clear.isPending} onClick={() => clear.mutate()}>
              {t("governance.privacy.breach.clear")}
            </button>
          )}
          <button type="button" className="btn btn-quiet min-h-[44px] justify-center" onClick={onClose}>
            {t("governance.privacy.close")}
          </button>
        </div>
      </form>
    </Dialog>
  );
}

// ---------------------------------------------------------------------
// Flussi di dati

function FlowsTab({ canWrite, onNew }: { canWrite: boolean; onNew: (apps?: string[]) => void }) {
  const t = useT();
  const q = useQuery({ queryKey: ["privacy", "flows"], queryFn: privacyApi.flows });
  if (q.error) return <ErrorNote error={q.error} />;
  const f = q.data;
  if (!f) return <Loading rows={4} />;
  return (
    <div className="flex flex-col gap-6">
      <p className="m-0 text-[13px] text-muted">{t("governance.privacy.flows.help")}</p>
      <div className="grid grid-cols-3 gap-2">
        {[
          ["personal", f.personal],
          ["credentials", f.credentials],
          ["financial", f.financial],
        ].map(([k, n]) => (
          <div key={k as string} className="flex min-w-0 flex-col gap-0.5 border border-hair px-3 py-2">
            <span className="text-[22px] font-bold leading-none tabular">{n as number}</span>
            <span className="break-words text-[12.5px] text-muted">{t(`governance.privacy.data.${k as "personal"}`)}</span>
          </div>
        ))}
      </div>
      <Block title={t("governance.privacy.flows.unlinkedTitle")} count={f.unlinked.length}>
        {f.unlinked.length === 0 ? <Empty>{t("governance.privacy.flows.unlinkedNone")}</Empty> : <UnlinkedRows apps={f.unlinked} canWrite={canWrite} onNew={onNew} />}
      </Block>
      {f.treatments.length === 0 && <Empty>{t("governance.privacy.flows.noTreatments")}</Empty>}
      {f.treatments.map((tf) => {
        const name = new Map(tf.nodes.map((n) => [n.id, n.name]));
        return (
          <Block key={tf.treatment.id} title={tf.treatment.name} count={tf.nodes.length}>
            {tf.exposed > 0 && <p className="m-0 border-l-4 border-flag px-3 py-1 text-[13px]">{t("governance.privacy.flows.exposed", { count: tf.exposed })}</p>}
            <ul className="m-0 flex list-none flex-col p-0" aria-label={t("governance.privacy.flows.apps")}>
              {tf.nodes.map((n) => (
                <li key={n.id} className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1 border-b border-hair py-2">
                  <Link to="/e/$key" params={{ key: n.key }} className="min-w-0 break-words font-semibold">
                    {n.name}
                  </Link>
                  <DataBadges app={n} />
                  <span className="text-[12px] text-muted">{n.core ? t("governance.privacy.flows.inTreatment") : t("governance.privacy.flows.neighbour")}</span>
                  {!n.core && n.personal && !n.linked && <span className="label border border-flag px-1.5 py-0.5 text-[10.5px] text-flag">{t("governance.privacy.flows.noTreatment")}</span>}
                </li>
              ))}
            </ul>
            {tf.edges.length > 0 && (
              <ul className="m-0 flex list-none flex-col gap-1 p-0 text-[13px]" aria-label={t("governance.privacy.flows.deps")}>
                {tf.edges.map((e) => (
                  <li key={e.id} className="break-words">
                    {name.get(e.source)} <span className="text-muted">{e.name}</span> {name.get(e.target)}
                    {e.data && <span className="text-muted"> · {t("governance.privacy.flows.data", { data: e.data })}</span>}
                  </li>
                ))}
              </ul>
            )}
          </Block>
        );
      })}
    </div>
  );
}
