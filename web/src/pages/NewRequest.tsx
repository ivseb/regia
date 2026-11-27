import { EntitySelect, Select } from "../select";
import { useEffect, useMemo, useState, type FormEvent, type ReactNode } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type NewRequest as NewRequestBody } from "../api";
import { PageHeader } from "../shell";
import { DRIVERS, ErrorNote, mainPath } from "../ui";
import { fieldsApi } from "../fieldsApi";
import { useTypeNames } from "../typesApi";
import { CustomFieldsForm, defaultsOf, formFacts, payloadOf } from "../fields";
import { useT } from "../i18n/react";
import { aiApi } from "../aiApi";
import { CreateAssistant } from "./CreateAssistant";

// Nomi dei driver nelle condizioni (come demand.driverNames sul server): valori da confrontare, non testi.
// i18n-ok: valori delle condizioni
const DRIVER_FACTS: Record<string, string> = { strategic: "strategico", regulatory: "normativo", operational: "operativo", efficiency: "efficienza", risk: "rischio" };

function Field({ label, hint, children, wide }: { label: string; hint?: string; children: ReactNode; wide?: boolean }) {
  return (
    <label className={`flex flex-col gap-1.5 ${wide ? "md:col-span-2" : ""}`}>
      <span className="label">{label}</span>
      {children}
      {hint && <span className="text-[12px] text-muted">{hint}</span>}
    </label>
  );
}

export function NewRequest() {
  const t = useT();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const tn = useTypeNames();
  const [f, setF] = useState({
    title: "", description: "", sponsor_id: "", business_owner_id: "", requesting_unit_id: "", driver: "",
    is_mandatory: false, regulatory_deadline: "", desired_date: "",
  });
  // Campi personalizzati del tipo (Configurazione → Campi), con predefiniti e visibilità.
  const typeFields = useQuery({ queryKey: ["type-fields", "business_request"], queryFn: () => fieldsApi.typeFields("business_request") });
  const type = useQuery({ queryKey: ["type", "business_request"], queryFn: () => api.type("business_request"), staleTime: 300_000 });
  const fields = useMemo(() => typeFields.data?.items ?? [], [typeFields.data]);
  const [custom, setCustom] = useState<Record<string, unknown>>({});
  // Asset logici proposti dall'assistente alla creazione e accettati: si citano dopo il salvataggio.
  const [links, setLinks] = useState<string[]>([]);
  useEffect(() => setCustom((c) => ({ ...defaultsOf(fields), ...c })), [fields]);
  const initial = type.data ? mainPath(type.data.states)[0]?.name : undefined;
  const facts = formFacts(fields, custom, {
    // i18n-ok: valore di confronto delle condizioni
    tipo: ["business_request", type.data?.name ?? "Richiesta"],
    ...(initial ? { fase: [initial], stato: [initial] } : {}),
    ...(f.driver ? { driver: [f.driver, DRIVER_FACTS[f.driver] ?? f.driver] } : {}),
    obbligatoria: [f.is_mandatory],
    ...(f.desired_date ? { data_desiderata: [f.desired_date] } : {}),
  });
  const set = (k: keyof typeof f) => (e: { target: { value: string } }) => setF({ ...f, [k]: e.target.value });

  const create = useMutation({
    mutationFn: () => {
      const body: NewRequestBody = { title: f.title, description: f.description, is_mandatory: f.is_mandatory };
      if (f.sponsor_id) body.sponsor_id = f.sponsor_id;
      if (f.business_owner_id) body.business_owner_id = f.business_owner_id;
      if (f.requesting_unit_id) body.requesting_unit_id = f.requesting_unit_id;
      if (f.driver) body.driver = f.driver;
      if (f.is_mandatory && f.regulatory_deadline) body.regulatory_deadline = f.regulatory_deadline;
      if (f.desired_date) body.desired_date = f.desired_date;
      const fields = payloadOf(typeFields.data?.items ?? [], custom, facts);
      if (Object.keys(fields).length) body.fields = fields;
      return api.createRequest(body);
    },
    onSuccess: async (el) => {
      // Un collegamento che non riesce non ferma la creazione: la richiesta esiste già.
      if (links.length) await aiApi.cardLinks(el.key, links).catch(() => null);
      qc.invalidateQueries({ queryKey: ["search"] });
      navigate({ to: "/e/$key", params: { key: el.key } });
    },
  });

  function submit(e: FormEvent) {
    e.preventDefault();
    create.mutate();
  }

  return (
    <>
      <PageHeader path={t("lavoro.newrequest.path")} title={tn.create("business_request", t("lavoro.newrequest.typeFallback"))} />
      <form onSubmit={submit} className="grid max-w-4xl grid-cols-1 gap-x-8 gap-y-5 px-4 md:px-7 pb-10 md:grid-cols-2">
        <Field label={t("lavoro.newrequest.title")} wide>
          <input className="field text-[16px] font-semibold" required maxLength={500} value={f.title} onChange={set("title")} placeholder={t("lavoro.newrequest.titlePh")} />
        </Field>
        <Field label={t("lavoro.newrequest.need")} hint={t("lavoro.newrequest.needHint")} wide>
          <textarea className="field min-h-32" value={f.description} onChange={set("description")} />
        </Field>
        <CreateAssistant
          type="business_request"
          title={f.title}
          description={f.description}
          links={links}
          onLinks={setLinks}
          onAccept={(p) => {
            if (p.key === "driver") setF((x) => ({ ...x, driver: String(p.value) }));
            else setCustom((c) => ({ ...c, [p.key]: p.value }));
          }}
        />
        <Field label={t("lavoro.newrequest.sponsor")} hint={t("lavoro.newrequest.sponsorHint")}>
          <EntitySelect kind="member" className="field" noneLabel={t("lavoro.newrequest.tbd")} value={f.sponsor_id} onChange={(v) => setF((x) => ({ ...x, sponsor_id: v }))} />
        </Field>
        <Field label={t("lavoro.newrequest.owner")}>
          <EntitySelect kind="member" className="field" noneLabel={t("lavoro.newrequest.tbd")} value={f.business_owner_id} onChange={(v) => setF((x) => ({ ...x, business_owner_id: v }))} />
        </Field>
        <Field label={t("lavoro.newrequest.unit")}>
          <EntitySelect kind="unit" className="field" noneLabel={t("lavoro.newrequest.noUnit")} value={f.requesting_unit_id} onChange={(v) => setF((x) => ({ ...x, requesting_unit_id: v }))} />
        </Field>
        <Field label={t("lavoro.newrequest.driver")}>
          <Select className="field" value={f.driver} onChange={set("driver")}>
            <option value="">{t("lavoro.newrequest.tbd")}</option>
            {Object.entries(DRIVERS).map(([k, v]) => <option key={k} value={k}>{v}</option>)}
          </Select>
        </Field>
        <Field label={t("lavoro.newrequest.desired")}>
          <input className="field" type="date" value={f.desired_date} onChange={set("desired_date")} />
        </Field>
        <div className="flex flex-col gap-3 border-t border-hair pt-4 md:col-span-2">
          <label className="flex items-center gap-2">
            <input type="checkbox" className="size-4 accent-[var(--accent)]" checked={f.is_mandatory} onChange={(e) => setF({ ...f, is_mandatory: e.target.checked })} />
            <span className="font-semibold">{t("lavoro.newrequest.mandatory")}</span>
            <span className="text-muted">{t("lavoro.newrequest.mandatoryHint")}</span>
          </label>
          {f.is_mandatory && (
            <Field label={t("lavoro.newrequest.deadline")}>
              <input className="field max-w-xs" type="date" required value={f.regulatory_deadline} onChange={set("regulatory_deadline")} />
            </Field>
          )}
        </div>
        <CustomFieldsForm fields={fields} values={custom} onChange={(k, v) => setCustom((c) => ({ ...c, [k]: v }))} facts={facts} />
        <div className="flex flex-col gap-3 md:col-span-2">
          <ErrorNote error={create.error} />
          <div className="flex gap-2">
            <button className="btn btn-primary" disabled={create.isPending}>{create.isPending ? t("lavoro.newrequest.saving") : t("lavoro.newrequest.create")}</button>
            <button type="button" className="btn" onClick={() => navigate({ to: "/richieste" })}>{t("lavoro.newrequest.cancel")}</button>
          </div>
          <p className="m-0 text-[12px] text-muted">{t("lavoro.newrequest.note")}</p>
        </div>
      </form>
    </>
  );
}
