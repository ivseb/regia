// Registro dei trattamenti (art. 30 GDPR, da verificare) e DPIA (art. 35, da verificare): l'elenco, la
// scheda di un trattamento e la DPIA. Le modifiche passano da finestre che sul telefono sono fogli dal basso.
import { useMemo, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { privacyApi, type Dpia, type Treatment, type TreatmentInput } from "../privacyApi";
import { ApiError } from "../api";
import { useT } from "../i18n/react";
import { EntitySelect, OptionSelect } from "../select";
import { ErrorNote, Loading, date } from "../ui";
import { Dialog } from "./work-ui";
import { Check, DataBadges, DpiaBadge, Empty, IssueBadge, TField, splitList } from "./privacy-ui";

/** Il trattamento vuoto (con le applicazioni di partenza, se si arriva da un'applicazione senza trattamento). */
export function emptyTreatment(apps: string[] = []): TreatmentInput {
  return {
    name: "", purpose: "", legal_basis: "", legal_basis_note: "", data_subjects: [], data_categories: [], special_categories: false, large_scale: false,
    profiling: false, high_risk: false, recipients: "", transfers_extra_eu: false, transfer_details: "", retention: "", owner_id: "", status: "active", applications: apps,
  };
}

const inputOf = (x: Treatment): TreatmentInput => ({
  name: x.name, purpose: x.purpose, legal_basis: x.legal_basis, legal_basis_note: x.legal_basis_note, data_subjects: x.data_subjects, data_categories: x.data_categories,
  special_categories: x.special_categories, large_scale: x.large_scale, profiling: x.profiling, high_risk: x.high_risk, recipients: x.recipients,
  transfers_extra_eu: x.transfers_extra_eu, transfer_details: x.transfer_details, retention: x.retention, owner_id: x.owner_id ?? "", status: x.status,
  applications: x.applications.map((a) => a.id),
});

export function useTreatments(archived = false) {
  return useQuery({ queryKey: ["privacy", "treatments", archived], queryFn: () => privacyApi.treatments(archived) });
}

function refresh(qc: ReturnType<typeof useQueryClient>) {
  void qc.invalidateQueries({ queryKey: ["privacy"] });
}

// ---------------------------------------------------------------------
// Elenco dei trattamenti

export function TreatmentsTab({ canWrite, onEdit, onNew, onDpia }: { canWrite: boolean; onEdit: (t: Treatment) => void; onNew: () => void; onDpia: (t: Treatment) => void }) {
  const t = useT();
  const [archived, setArchived] = useState(false);
  const q = useTreatments(archived);
  const bases = useMemo(() => Object.fromEntries((q.data?.legal_bases ?? []).map((b) => [b.key, b.name])), [q.data]);
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center gap-2">
        {canWrite && (
          <button type="button" className="btn btn-primary min-h-[44px] justify-center" onClick={onNew}>
            {t("governance.privacy.treatment.new")}
          </button>
        )}
        <a className="btn min-h-[44px] justify-center no-underline" href={privacyApi.csvUrl} download>
          {t("governance.privacy.treatment.csv")}
        </a>
        <Check checked={archived} onChange={setArchived}>
          {t("governance.privacy.treatment.showArchived")}
        </Check>
      </div>
      <p className="m-0 text-[12.5px] text-muted">{t("governance.privacy.treatment.help")}</p>
      <ErrorNote error={q.error} />
      {!q.data && !q.error && <Loading rows={4} />}
      {q.data && q.data.items.length === 0 && <Empty>{t("governance.privacy.treatment.empty")}</Empty>}
      {q.data && q.data.items.length > 0 && (
        <ul className="m-0 flex list-none flex-col p-0" aria-label={t("governance.privacy.tab.trattamenti")}>
          {q.data.items.map((x) => (
            <li key={x.id} className="flex min-w-0 flex-col gap-1.5 border-b border-hair py-3">
              <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                <button type="button" className="min-h-[44px] min-w-0 max-w-full break-words border-0 bg-transparent p-0 text-left text-[15px] font-semibold underline md:min-h-[24px]" onClick={() => onEdit(x)}>
                  {x.name}
                </button>
                {x.status === "archived" && <span className="label border border-rule px-1.5 py-0.5 text-[10.5px] text-muted">{t("governance.privacy.treatment.archived")}</span>}
                {x.issues.map((i) => (
                  <IssueBadge key={i} issue={i} />
                ))}
              </div>
              <p className="m-0 text-[13px] text-muted">
                {x.legal_basis ? bases[x.legal_basis] : t("governance.privacy.treatment.noBasis")}
                {x.owner ? ` · ${x.owner}` : ""}
                {x.transfers_extra_eu ? ` · ${t("governance.privacy.treatment.extraEu")}` : ""}
              </p>
              {x.applications.length > 0 && (
                <p className="m-0 flex flex-wrap items-center gap-x-2 gap-y-1 text-[13px]">
                  {x.applications.map((a) => (
                    <span key={a.id} className="inline-flex items-center gap-1.5">
                      {a.name} <DataBadges app={a} />
                    </span>
                  ))}
                </p>
              )}
              <div className="flex flex-wrap items-center gap-2">
                {x.dpia ? <DpiaBadge status={x.dpia.status} /> : x.dpia_required ? <span className="text-[12.5px] text-flag">{t("governance.privacy.dpia.required")}</span> : null}
                {(x.dpia || x.dpia_required) && (
                  <button type="button" className="btn btn-quiet min-h-[44px] justify-center md:min-h-[28px]" onClick={() => onDpia(x)}>
                    {x.dpia ? t("governance.privacy.dpia.open") : canWrite ? t("governance.privacy.dpia.start") : t("governance.privacy.dpia.open")}
                  </button>
                )}
              </div>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------
// Scheda di un trattamento

export function TreatmentDialog({ treatment, initial, canWrite, onClose, onDpia }: { treatment: Treatment | null; initial?: TreatmentInput; canWrite: boolean; onClose: () => void; onDpia: (t: Treatment) => void }) {
  const t = useT();
  const qc = useQueryClient();
  const bases = useTreatments().data?.legal_bases ?? [];
  const [f, setF] = useState<TreatmentInput>(treatment ? inputOf(treatment) : (initial ?? emptyTreatment()));
  const [subjects, setSubjects] = useState(f.data_subjects.join(", "));
  const [cats, setCats] = useState(f.data_categories.join(", "));
  const [confirmDelete, setConfirmDelete] = useState(false);
  function set<K extends keyof TreatmentInput>(k: K, v: TreatmentInput[K]) {
    setF((p) => ({ ...p, [k]: v }));
  }
  const hints = useMemo(() => Object.fromEntries((treatment?.applications ?? []).map((a) => [a.id, a.name])), [treatment]);
  const save = useMutation({
    mutationFn: () => {
      const body = { ...f, data_subjects: splitList(subjects), data_categories: splitList(cats) };
      return treatment ? privacyApi.updateTreatment(treatment.id, body) : privacyApi.createTreatment(body);
    },
    onSuccess: () => {
      refresh(qc);
      onClose();
    },
  });
  const del = useMutation({
    mutationFn: () => privacyApi.deleteTreatment(treatment!.id),
    onSuccess: () => {
      refresh(qc);
      onClose();
    },
  });
  const dpiaNeeded = f.special_categories || f.large_scale || f.profiling || f.high_risk;
  return (
    <Dialog title={treatment ? treatment.name : t("governance.privacy.treatment.new")} onClose={onClose} wide>
      <form
        className="flex flex-col gap-3"
        onSubmit={(e) => {
          e.preventDefault();
          if (canWrite) save.mutate();
        }}
      >
        <fieldset disabled={!canWrite} className="m-0 flex min-w-0 flex-col gap-3 border-0 p-0">
          <TField label={t("governance.privacy.f.name")}>
            <input className="field" value={f.name} maxLength={120} required onChange={(e) => set("name", e.target.value)} />
          </TField>
          <TField label={t("governance.privacy.f.purpose")}>
            <textarea className="field" rows={3} value={f.purpose} onChange={(e) => set("purpose", e.target.value)} />
          </TField>
          <TField label={t("governance.privacy.f.basis")} hint={t("governance.privacy.f.basisHint")}>
            <OptionSelect
              value={f.legal_basis}
              onChange={(v) => set("legal_basis", v)}
              options={bases.map((b) => ({ value: b.key, label: b.name }))}
              noneLabel={t("governance.privacy.f.basisNone")}
              aria-label={t("governance.privacy.f.basis")}
            />
          </TField>
          <TField label={t("governance.privacy.f.basisNote")}>
            <input className="field" value={f.legal_basis_note} onChange={(e) => set("legal_basis_note", e.target.value)} />
          </TField>
          <TField label={t("governance.privacy.f.subjects")} hint={t("governance.privacy.f.listHint")}>
            <input className="field" value={subjects} onChange={(e) => setSubjects(e.target.value)} />
          </TField>
          <TField label={t("governance.privacy.f.categories")} hint={t("governance.privacy.f.listHint")}>
            <input className="field" value={cats} onChange={(e) => setCats(e.target.value)} />
          </TField>
          <div className="flex flex-col" role="group" aria-label={t("governance.privacy.f.dpiaCriteria")}>
            <span className="label text-muted">{t("governance.privacy.f.dpiaCriteria")}</span>
            <Check checked={f.special_categories} onChange={(v) => set("special_categories", v)}>{t("governance.privacy.f.special")}</Check>
            <Check checked={f.large_scale} onChange={(v) => set("large_scale", v)}>{t("governance.privacy.f.largeScale")}</Check>
            <Check checked={f.profiling} onChange={(v) => set("profiling", v)}>{t("governance.privacy.f.profiling")}</Check>
            <Check checked={f.high_risk} onChange={(v) => set("high_risk", v)}>{t("governance.privacy.f.highRisk")}</Check>
            {dpiaNeeded && <span className="text-[12.5px] text-flag">{t("governance.privacy.f.dpiaNeeded")}</span>}
          </div>
          <TField label={t("governance.privacy.f.recipients")}>
            <textarea className="field" rows={2} value={f.recipients} onChange={(e) => set("recipients", e.target.value)} />
          </TField>
          <Check checked={f.transfers_extra_eu} onChange={(v) => set("transfers_extra_eu", v)}>{t("governance.privacy.f.extraEu")}</Check>
          {f.transfers_extra_eu && (
            <TField label={t("governance.privacy.f.extraEuDetails")}>
              <input className="field" value={f.transfer_details} onChange={(e) => set("transfer_details", e.target.value)} />
            </TField>
          )}
          <TField label={t("governance.privacy.f.retention")}>
            <input className="field" value={f.retention} onChange={(e) => set("retention", e.target.value)} />
          </TField>
          <TField label={t("governance.privacy.f.owner")}>
            <EntitySelect kind="member" value={f.owner_id} onChange={(v) => set("owner_id", v)} noneLabel={t("governance.privacy.f.ownerNone")} aria-label={t("governance.privacy.f.owner")} />
          </TField>
          <TField label={t("governance.privacy.f.apps")} hint={t("governance.privacy.f.appsHint")}>
            <EntitySelect kind="application" multiple value={f.applications} onChange={(v) => set("applications", v)} hints={hints} aria-label={t("governance.privacy.f.apps")} />
          </TField>
          {treatment && (
            <Check checked={f.status === "archived"} onChange={(v) => set("status", v ? "archived" : "active")}>
              {t("governance.privacy.f.archive")}
            </Check>
          )}
        </fieldset>
        <p className="m-0 text-[12px] text-muted">{t("governance.privacy.treatment.legalNote")}</p>
        <ErrorNote error={save.error ?? del.error} />
        <div className="flex flex-wrap items-center gap-2">
          {canWrite && (
            <button type="submit" className="btn btn-primary min-h-[44px] justify-center" disabled={save.isPending}>
              {t("governance.privacy.save")}
            </button>
          )}
          {treatment && (dpiaNeeded || treatment.dpia) && (
            <button type="button" className="btn min-h-[44px] justify-center" onClick={() => onDpia(treatment)}>
              {t("governance.privacy.dpia.open")}
            </button>
          )}
          {canWrite && treatment && !confirmDelete && (
            <button type="button" className="btn btn-quiet ml-auto min-h-[44px] justify-center" onClick={() => setConfirmDelete(true)}>
              {t("governance.privacy.delete")}
            </button>
          )}
          {canWrite && treatment && confirmDelete && (
            <button type="button" className="btn btn-danger ml-auto min-h-[44px] justify-center" disabled={del.isPending} onClick={() => del.mutate()}>
              {t("governance.privacy.deleteConfirm")}
            </button>
          )}
        </div>
      </form>
    </Dialog>
  );
}

// ---------------------------------------------------------------------
// DPIA

export function DpiaTab({ canWrite, onDpia }: { canWrite: boolean; onDpia: (t: Treatment) => void }) {
  const t = useT();
  const q = useTreatments();
  const rows = useMemo(() => {
    const rank = (x: Treatment) => (x.dpia_required && !x.dpia ? 0 : x.dpia?.status === "in_review" ? 1 : x.dpia?.status === "draft" ? 2 : 3);
    return (q.data?.items ?? []).filter((x) => x.dpia || x.dpia_required).sort((a, b) => rank(a) - rank(b) || a.name.localeCompare(b.name));
  }, [q.data]);
  return (
    <div className="flex flex-col gap-4">
      <p className="m-0 text-[12.5px] text-muted">{t("governance.privacy.dpia.help")}</p>
      <ErrorNote error={q.error} />
      {!q.data && !q.error && <Loading rows={3} />}
      {q.data && rows.length === 0 && <Empty>{t("governance.privacy.dpia.empty")}</Empty>}
      <ul className="m-0 flex list-none flex-col p-0" aria-label={t("governance.privacy.tab.dpia")}>
        {rows.map((x) => (
          <li key={x.id} className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-hair py-3">
            <span className="min-w-0 flex-[1_1_14rem] break-words text-[15px] font-semibold">{x.name}</span>
            {x.dpia ? <DpiaBadge status={x.dpia.status} /> : <span className="text-[12.5px] text-flag">{t("governance.privacy.dpia.missing")}</span>}
            <button type="button" className="btn min-h-[44px] justify-center md:min-h-[28px]" onClick={() => onDpia(x)}>
              {x.dpia ? t("governance.privacy.dpia.open") : canWrite ? t("governance.privacy.dpia.start") : t("governance.privacy.dpia.open")}
            </button>
          </li>
        ))}
      </ul>
    </div>
  );
}

type DpiaForm = { description: string; necessity: string; risk_assessment: string; dpo_opinion: string; review_id: string; risk_ids: string[]; control_ids: string[] };
const formOf = (d: Dpia | null): DpiaForm => ({
  description: d?.description ?? "", necessity: d?.necessity ?? "", risk_assessment: d?.risk_assessment ?? "", dpo_opinion: d?.dpo_opinion ?? "",
  review_id: d?.review_id ?? "", risk_ids: d?.risks.map((r) => r.id) ?? [], control_ids: d?.controls.map((c) => c.id) ?? [],
});

export function DpiaDialog({ treatment, canWrite, onClose }: { treatment: Treatment; canWrite: boolean; onClose: () => void }) {
  const q = useQuery({
    queryKey: ["privacy", "dpia", treatment.id],
    queryFn: async () => {
      try {
        return await privacyApi.dpia(treatment.id);
      } catch (e) {
        if (e instanceof ApiError && e.status === 404) return null;
        throw e;
      }
    },
  });
  const t = useT();
  return (
    <Dialog title={t("governance.privacy.dpia.title", { name: treatment.name })} onClose={onClose} wide>
      <ErrorNote error={q.error} />
      {q.data === undefined && !q.error && <Loading rows={3} />}
      {q.data !== undefined && <DpiaEditor key={q.data?.updated_at ?? "new"} treatment={treatment} dpia={q.data} canWrite={canWrite} onClose={onClose} />}
    </Dialog>
  );
}

function DpiaEditor({ treatment, dpia, canWrite, onClose }: { treatment: Treatment; dpia: Dpia | null; canWrite: boolean; onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [f, setF] = useState<DpiaForm>(formOf(dpia));
  function set<K extends keyof DpiaForm>(k: K, v: DpiaForm[K]) {
    setF((p) => ({ ...p, [k]: v }));
  }
  const status = dpia?.status ?? "draft";
  const locked = status === "approved" || !canWrite;
  const hints = useMemo(() => Object.fromEntries([...(dpia?.risks ?? []), ...(dpia?.controls ?? [])].map((x) => [x.id, x.title])), [dpia]);
  const onDone = (d: Dpia) => {
    refresh(qc);
    qc.setQueryData(["privacy", "dpia", treatment.id], d);
  };
  const save = useMutation({ mutationFn: () => privacyApi.saveDpia(treatment.id, f), onSuccess: onDone });
  const move = useMutation({
    mutationFn: async (to: "draft" | "in_review" | "approved") => {
      if (!locked) await privacyApi.saveDpia(treatment.id, f);
      return privacyApi.dpiaTo(treatment.id, to);
    },
    onSuccess: onDone,
  });
  const reviews = dpia?.reviews ?? [];
  const effLabel = (v: string) =>
    v === "effective" ? t("governance.privacy.dpia.eff.effective") : v === "partial" ? t("governance.privacy.dpia.eff.partial") : v === "ineffective" ? t("governance.privacy.dpia.eff.ineffective") : t("governance.privacy.dpia.eff.not_assessed");
  return (
    <form
      className="flex flex-col gap-3"
      onSubmit={(e) => {
        e.preventDefault();
        if (!locked) save.mutate();
      }}
    >
      <p className="m-0 flex flex-wrap items-center gap-2 text-[12.5px] text-muted">
        <DpiaBadge status={status} />
        {dpia?.approved_at && dpia.approved_by && <span>{t("governance.privacy.dpia.approvedBy", { name: dpia.approved_by, date: date(dpia.approved_at) })}</span>}
        <span>{t("governance.privacy.dpia.legalNote")}</span>
      </p>
      <fieldset disabled={locked} className="m-0 flex min-w-0 flex-col gap-3 border-0 p-0">
        <TField label={t("governance.privacy.dpia.f.description")}>
          <textarea className="field" rows={3} value={f.description} onChange={(e) => set("description", e.target.value)} />
        </TField>
        <TField label={t("governance.privacy.dpia.f.necessity")}>
          <textarea className="field" rows={3} value={f.necessity} onChange={(e) => set("necessity", e.target.value)} />
        </TField>
        <TField label={t("governance.privacy.dpia.f.riskAssessment")}>
          <textarea className="field" rows={3} value={f.risk_assessment} onChange={(e) => set("risk_assessment", e.target.value)} />
        </TField>
        <TField label={t("governance.privacy.dpia.f.risks")} hint={t("governance.privacy.dpia.f.risksHint")}>
          <EntitySelect kind="element" types={["risk"]} multiple value={f.risk_ids} onChange={(v) => set("risk_ids", v)} hints={hints} placeholder={t("governance.privacy.dpia.f.risksPh")} aria-label={t("governance.privacy.dpia.f.risks")} />
        </TField>
        <TField label={t("governance.privacy.dpia.f.controls")} hint={t("governance.privacy.dpia.f.controlsHint")}>
          <EntitySelect kind="element" types={["control"]} multiple value={f.control_ids} onChange={(v) => set("control_ids", v)} hints={hints} placeholder={t("governance.privacy.dpia.f.controlsPh")} aria-label={t("governance.privacy.dpia.f.controls")} />
        </TField>
        {reviews.length > 0 || f.review_id ? (
          <TField label={t("governance.privacy.dpia.f.review")} hint={t("governance.privacy.dpia.f.reviewHint")}>
            <OptionSelect
              value={f.review_id}
              onChange={(v) => set("review_id", v)}
              options={[...reviews, ...(dpia?.review && !reviews.some((r) => r.id === dpia.review!.id) ? [dpia.review] : [])].map((r) => ({
                value: r.id,
                label: `${r.key} · ${r.request}`,
                detail: r.outcome ?? r.stage,
              }))}
              noneLabel={t("governance.privacy.dpia.f.reviewNone")}
              aria-label={t("governance.privacy.dpia.f.review")}
            />
          </TField>
        ) : (
          <p className="m-0 text-[12.5px] text-muted">{t("governance.privacy.dpia.f.reviewNoneAvailable")}</p>
        )}
        <TField label={t("governance.privacy.dpia.f.opinion")}>
          <textarea className="field" rows={3} value={f.dpo_opinion} onChange={(e) => set("dpo_opinion", e.target.value)} />
        </TField>
      </fieldset>
      {dpia && dpia.risks.length + dpia.controls.length > 0 && (
        <ul className="m-0 flex list-none flex-col gap-1 p-0 text-[13px]" aria-label={t("governance.privacy.dpia.linked")}>
          {dpia.risks.map((r) => (
            <li key={r.id}>
              <span className="font-mono text-[12px] text-muted">{r.key}</span> {r.title}
            </li>
          ))}
          {dpia.controls.map((c) => (
            <li key={c.id}>
              <span className="font-mono text-[12px] text-muted">{c.key}</span> {c.title}
              {c.detail && <span className="text-muted"> · {effLabel(c.detail)}</span>}
            </li>
          ))}
        </ul>
      )}
      <ErrorNote error={save.error ?? move.error} />
      {canWrite && (
        <div className="flex flex-wrap items-center gap-2">
          {status !== "approved" && (
            <button type="submit" className="btn btn-primary min-h-[44px] justify-center" disabled={save.isPending || move.isPending}>
              {t("governance.privacy.save")}
            </button>
          )}
          {status === "draft" && dpia && (
            <button type="button" className="btn min-h-[44px] justify-center" disabled={move.isPending} onClick={() => move.mutate("in_review")}>
              {t("governance.privacy.dpia.toReview")}
            </button>
          )}
          {status === "in_review" && (
            <>
              <button type="button" className="btn min-h-[44px] justify-center" disabled={move.isPending} onClick={() => move.mutate("approved")}>
                {t("governance.privacy.dpia.approve")}
              </button>
              <button type="button" className="btn btn-quiet min-h-[44px] justify-center" disabled={move.isPending} onClick={() => move.mutate("draft")}>
                {t("governance.privacy.dpia.toDraft")}
              </button>
            </>
          )}
          {status === "approved" && (
            <button type="button" className="btn min-h-[44px] justify-center" disabled={move.isPending} onClick={() => move.mutate("in_review")}>
              {t("governance.privacy.dpia.reopen")}
            </button>
          )}
          <button type="button" className="btn btn-quiet ml-auto min-h-[44px] justify-center" onClick={onClose}>
            {t("governance.privacy.close")}
          </button>
        </div>
      )}
    </form>
  );
}
