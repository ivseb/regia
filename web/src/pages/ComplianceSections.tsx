// Sezioni della conformità nella pagina di dettaglio (/e/CHIAVE): il controllo (requisiti
// coperti, perimetri, evidenze, efficacia), l'evidenza (validità, firma, validazione),
// l'applicazione (normative a cui è soggetta) e il requisito di progetto (riferimenti
// normativi). Si caricano alla prima apertura della scheda.
import { EntitySelect, Select } from "../select";
import { useState } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ErrorNote, SectionTitle, date, Loading } from "../ui";
import {
  AUTOMATIONS,
  CONTROL_KINDS,
  EFFECTIVENESS,
  EVIDENCE_KINDS,
  FREQUENCIES,
  grcApi,
  type ControlView,
  type EvidenceView,
} from "../grcApi";
import { CoverageBar, CoverageMark, EvidenceLink, EvidenceMark, FrameworkTag, scopeLabel, useGrcPerms } from "./grc-ui";
import { ProposalBox } from "./ControlLiving";
import { AuditBox } from "./AuditPanels";
import { tr } from "../i18n/core";

function Rows({ rows }: { rows: [string, React.ReactNode][] }) {
  return (
    <dl className="m-0 grid grid-cols-[130px_minmax(0,1fr)]">
      {rows.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="border-b border-hair py-1.5 text-muted">{k}</dt>
          <dd className="m-0 border-b border-hair py-1.5">{v}</dd>
        </div>
      ))}
    </dl>
  );
}

function useRefresh(refKey: string) {
  const qc = useQueryClient();
  return () => {
    qc.invalidateQueries({ queryKey: ["grc"] });
    qc.invalidateQueries({ queryKey: ["element", refKey] });
    qc.invalidateQueries({ queryKey: ["activity"] });
  };
}

// ---------------------------------------------------------------------
// Controllo

export function ControlSection({ refKey }: { refKey: string }) {
  const ctl = useQuery({ queryKey: ["grc", "control", refKey], queryFn: () => grcApi.control(refKey) });
  const c = ctl.data;
  if (ctl.error) return <ErrorNote error={ctl.error} />;
  if (!c) return <Loading className="m-0 text-muted" />;
  return (
    <div className="flex flex-col gap-6">
      <ControlFacts c={c} refKey={refKey} />
      <ProposalBox refKey={refKey} />
      <AuditBox refKey={refKey} />
      <MappedRequirements c={c} refKey={refKey} />
      <Needs c={c} />
      <EvidenceList c={c} refKey={refKey} />
    </div>
  );
}

/** Nome leggibile di una sorgente automatica di evidenze; la chiave resta in piccolo accanto, perché è ciò che si digita. */
const COLLECTOR_NAMES: Record<string, string> = { get "devsecops.release"() { return tr("governance.compliancesections.rilasci_e_gate_devsecops"); } };
const collectorName = (k: string) => COLLECTOR_NAMES[k] ?? k;

function ControlFacts({ c, refKey }: { c: ControlView; refKey: string }) {
  const refresh = useRefresh(refKey);
  const [editing, setEditing] = useState(false);
  const [form, setForm] = useState({ owner_id: c.owner_id, frequency: c.frequency, kind: c.kind, automation: c.automation, collectors: c.collectors.join(", ") });
  const [eff, setEff] = useState("effective");
  const [note, setNote] = useState("");
  const save = useMutation({
    mutationFn: () =>
      grcApi.updateControl(refKey, {
        ...form,
        collectors: form.collectors
          .split(",")
          .map((s) => s.trim())
          .filter(Boolean),
      }),
    onSuccess: () => {
      setEditing(false);
      refresh();
    },
  });
  const assess = useMutation({
    mutationFn: () => grcApi.assess(refKey, eff, note),
    onSuccess: () => {
      setNote("");
      refresh();
    },
  });
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle
        aside={
          c.can.edit && !editing ? (
            <button className="text-[12.5px] text-accent" onClick={() => setEditing(true)}>{tr("governance.compliancesections.modifica")}</button>
          ) : undefined
        }
      >{tr("governance.compliance.controllo")}</SectionTitle>
      {editing ? (
        <form
          className="grid grid-cols-1 gap-2 md:grid-cols-2"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{tr("governance.compliance.responsabile")}</span>
            <EntitySelect kind="member" noneLabel="—" className="field" value={form.owner_id} onChange={(v) => setForm({ ...form, owner_id: v })} />
          </label>
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{tr("governance.compliance.frequenza_di_verifica")}</span>
            <Select className="field" value={form.frequency} onChange={(e) => setForm({ ...form, frequency: e.target.value })}>
              {Object.entries(FREQUENCIES).map(([k, v]) => (
                <option key={k} value={k}>
                  {v}
                </option>
              ))}
            </Select>
          </label>
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{tr("governance.compliance.tipo")}</span>
            <Select className="field" value={form.kind} onChange={(e) => setForm({ ...form, kind: e.target.value })}>
              {Object.entries(CONTROL_KINDS).map(([k, v]) => (
                <option key={k} value={k}>
                  {v}
                </option>
              ))}
            </Select>
          </label>
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{tr("governance.compliancesections.automazione")}</span>
            <Select className="field" value={form.automation} onChange={(e) => setForm({ ...form, automation: e.target.value })}>
              {Object.entries(AUTOMATIONS).map(([k, v]) => (
                <option key={k} value={k}>
                  {v}
                </option>
              ))}
            </Select>
          </label>
          <label className="flex flex-col gap-1 md:col-span-2">
            <span className="label text-muted">{tr("governance.compliancesections.raccolte_automatiche_sorgenti_delle_evidenze")}</span>
            <input className="field font-mono" value={form.collectors} onChange={(e) => setForm({ ...form, collectors: e.target.value })} />
          </label>
          <ErrorNote error={save.error} />
          <div className="flex gap-2 md:col-span-2">
            <button className="btn btn-primary" disabled={save.isPending}>{tr("governance.compliancesections.salva")}</button>
            <button type="button" className="btn" onClick={() => setEditing(false)}>{tr("governance.compliance.annulla")}</button>
          </div>
        </form>
      ) : (
        <Rows
          rows={[
            [tr("governance.compliance.responsabile"), c.owner || <span className="text-danger">{tr("governance.compliance.da_indicare")}</span>],
            [tr("governance.compliance.frequenza"), FREQUENCIES[c.frequency]],
            [tr("governance.compliance.tipo"), `${CONTROL_KINDS[c.kind]}, ${AUTOMATIONS[c.automation]}`],
            [
              tr("governance.compliance.efficacia_2"),
              <span className={c.effectiveness === "ineffective" ? "text-danger" : ""}>
                {EFFECTIVENESS[c.effectiveness]}
                {c.assessed_at && (
                  <span className="text-muted">
                    {" "}
                    · {c.assessed_by}, {date(c.assessed_at)}
                    {c.next_review ? tr("governance.compliancesections.prossima_revisione_entro_il", { date: date(c.next_review) }) : ""}
                  </span>
                )}
                {c.effectiveness_note && <span className="block text-[12.5px] text-muted">{c.effectiveness_note}</span>}
              </span>,
            ],
            [tr("governance.compliancesections.raccolte"), c.collectors.length ? (
              <span>
                {c.collectors.map((k, i) => (
                  <span key={k}>
                    {i > 0 && ", "}
                    {collectorName(k)}
                    {collectorName(k) !== k && <span className="ml-1 font-mono text-[11.5px] text-muted">{k}</span>}
                  </span>
                ))}
              </span>
            ) : (
              "—"
            )],
          ]}
        />
      )}
      {c.can.edit && (
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            assess.mutate();
          }}
        >
          <label className="flex flex-col gap-1">
            <span className="label text-muted">{tr("governance.compliancesections.valuta_l_efficacia")}</span>
            <Select className="field" value={eff} onChange={(e) => setEff(e.target.value)}>
              <option value="effective">{tr("governance.compliancesections.efficace")}</option>
              <option value="partial">{tr("governance.compliancesections.parziale")}</option>
              <option value="ineffective">{tr("governance.compliancesections.inefficace")}</option>
            </Select>
          </label>
          <label className="flex min-w-[220px] flex-1 flex-col gap-1">
            <span className="label text-muted">{tr("governance.compliancesections.nota")}</span>
            <input className="field" value={note} onChange={(e) => setNote(e.target.value)} placeholder={/* i18n-ok: confronto con lo stato */ eff === "effective" ? tr("governance.compliancesections.facoltativa") : tr("governance.compliancesections.perche_non_e_piena")} />
          </label>
          <button className="btn" disabled={assess.isPending}>{tr("governance.compliancesections.registra")}</button>
          <ErrorNote error={assess.error} />
        </form>
      )}
    </section>
  );
}

function MappedRequirements({ c, refKey }: { c: ControlView; refKey: string }) {
  const refresh = useRefresh(refKey);
  const [editing, setEditing] = useState(false);
  const [text, setText] = useState("");
  const save = useMutation({
    mutationFn: () =>
      grcApi.setControlRequirements(
        refKey,
        text
          .split(/\n/)
          .map((l) => l.trim())
          .filter(Boolean)
          .map((l) => (l.endsWith(" parziale") ? { ref: l.slice(0, -9).trim(), coverage: "partial" } : { ref: l })),
      ),
    onSuccess: () => {
      setEditing(false);
      refresh();
    },
  });
  const current = c.requirements.filter((r) => r.current);
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle
        aside={
          c.can.edit && !editing ? (
            <button
              className="text-[12.5px] text-accent"
              onClick={() => {
                setText(current.map((r) => r.ref + (r.coverage === "partial" ? " parziale" : "")).join("\n"));
                setEditing(true);
              }}
            >{tr("governance.compliancesections.modifica")}</button>
          ) : (
            <span className="font-mono text-[11.5px] text-muted tabular">{current.length}</span>
          )
        }
      >{tr("governance.compliancesections.requisiti_coperti")}</SectionTitle>
      {editing ? (
        <form
          className="flex flex-col gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <textarea className="field min-h-[120px] font-mono" value={text} onChange={(e) => setText(e.target.value)} aria-label={tr("governance.compliancesections.requisiti_uno_per_riga")} />
          <span className="text-[12px] text-muted">{tr("governance.compliancesections.uno_per_riga_come_dora")}</span>
          <ErrorNote error={save.error} />
          <span className="flex gap-2">
            <button className="btn btn-primary" disabled={save.isPending}>{tr("governance.compliancesections.salva")}</button>
            <button type="button" className="btn" onClick={() => setEditing(false)}>{tr("governance.compliance.annulla")}</button>
          </span>
        </form>
      ) : (
        <ul className="m-0 flex list-none flex-col p-0">
          {c.requirements.map((r) => (
            <li key={r.id} className={`flex flex-wrap items-baseline gap-x-2 border-b border-hair py-1.5 ${r.current ? "" : "opacity-60"}`}>
              <FrameworkTag k={r.framework} title={r.framework_name} />
              <Link to="/conformita" search={{ tab: "normative", normativa: r.framework, requisito: r.id }} className="font-mono text-[12px]">
                {r.req_ref}
              </Link>
              <span className="min-w-0 flex-1">{r.title}</span>
              {r.coverage === "partial" && <span className="text-[12px] text-muted">{tr("governance.compliancesections.parziale")}</span>}
              {!r.current && <span className="text-[12px] text-muted">{tr("governance.compliancesections.versione_superata")}</span>}
              {r.current && !r.enabled && <span className="text-[12px] text-muted">{tr("governance.compliancesections.normativa_non_abilitata")}</span>}
            </li>
          ))}
          {c.requirements.length === 0 && <li className="py-1.5 text-muted">{tr("governance.compliancesections.nessun_requisito_collegato")}</li>}
        </ul>
      )}
    </section>
  );
}

function Needs({ c }: { c: ControlView }) {
  if (!c.operational) return <p className="m-0 text-[13px] text-muted">{tr("governance.compliancesections.il_controllo_copre_i_requisiti", { phase: c.phase })}</p>;
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle>{tr("governance.compliancesections.dove_serve")}</SectionTitle>
      {c.needs.length === 0 && <p className="m-0 text-muted">{tr("governance.compliancesections.i_requisiti_collegati_non_si")}</p>}
      <ul className="m-0 flex list-none flex-col p-0">
        {c.needs.map((n) => (
          <li key={`${n.scope.kind}:${n.scope.id}`} className="flex flex-col gap-0.5 border-b border-hair py-1.5">
            <span className="flex flex-wrap items-baseline gap-x-2">
              <span className="font-medium">{scopeLabel(n.scope)}</span>
              <span className={`text-[12.5px] ${n.status === "ok" ? "text-ok" : n.status === "expiring" ? "text-flag" : "text-danger"}`}>
                {n.status === "ok" ? tr("governance.compliancesections.evidenza_valida") : n.status === "expiring" ? tr("governance.compliancesections.evidenza_in_scadenza") : tr("governance.compliancesections.manca_un_evidenza_valida")}
              </span>
            </span>
            <span className="font-mono text-[11.5px] text-muted">{n.requirements.join(", ")}</span>
          </li>
        ))}
      </ul>
    </section>
  );
}

function EvidenceList({ c, refKey }: { c: ControlView; refKey: string }) {
  const [adding, setAdding] = useState(false);
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle
        aside={
          c.can.add_evidence && !adding ? (
            <button className="text-[12.5px] text-accent" onClick={() => setAdding(true)}>{tr("governance.compliancesections.aggiungi_evidenza")}</button>
          ) : undefined
        }
      >{tr("governance.compliance.evidenze")}</SectionTitle>
      {adding && <NewEvidence controlKey={refKey} onDone={() => setAdding(false)} />}
      <ul className="m-0 flex list-none flex-col p-0">
        {c.evidence.map((e) => (
          <li key={e.id} className="flex flex-col border-b border-hair py-1.5">
            <EvidenceLink e={e} />
            <span className="text-[12px] text-muted">
              {EVIDENCE_KINDS[e.kind] ?? e.kind} · {scopeLabel(e.scope)}
              {e.validated_by ? tr("governance.compliancesections.validata_da", { validated_by: e.validated_by }) : ""}
              {e.derived ? tr("governance.compliancesections.dalla_verifica_di_un_requisito") : ""}
            </span>
          </li>
        ))}
        {c.evidence.length === 0 && <li className="py-1.5 text-muted">{tr("governance.compliancesections.nessuna_evidenza")}</li>}
      </ul>
    </section>
  );
}

function NewEvidence({ controlKey, onDone }: { controlKey: string; onDone: () => void }) {
  const refresh = useRefresh(controlKey);
  const [f, setF] = useState({ kind: "attachment", title: "", note: "", url: "", page: "", scope: "tenant", target: "", valid_until: "" });
  const create = useMutation({
    mutationFn: () =>
      grcApi.createEvidence({
        controls: [controlKey],
        ...f,
        target: f.scope === "tenant" ? undefined : f.target,
      }),
    onSuccess: () => {
      refresh();
      onDone();
    },
  });
  return (
    <form
      className="grid grid-cols-1 gap-2 border-y border-hair py-3 md:grid-cols-2"
      onSubmit={(e) => {
        e.preventDefault();
        create.mutate();
      }}
    >
      <label className="flex flex-col gap-1">
        <span className="label text-muted">{tr("governance.compliance.tipo")}</span>
        <Select className="field" value={f.kind} onChange={(e) => setF({ ...f, kind: e.target.value })}>
          <option value="attachment">{tr("governance.compliancesections.allegato_si_carica_dopo_nella")}</option>
          <option value="link">{tr("governance.compliancesections.link")}</option>
          <option value="knowledge_page">{tr("governance.compliancesections.pagina_knowledge")}</option>
          <option value="note">{tr("governance.compliancesections.nota_2")}</option>
        </Select>
      </label>
      <label className="flex flex-col gap-1">
        <span className="label text-muted">{tr("governance.compliance.titolo")}</span>
        <input className="field" required value={f.title} onChange={(e) => setF({ ...f, title: e.target.value })} />
      </label>
      {f.kind === "link" && (
        <label className="flex flex-col gap-1 md:col-span-2">
          <span className="label text-muted">{tr("governance.compliancesections.indirizzo")}</span>
          <input className="field" type="url" required value={f.url} onChange={(e) => setF({ ...f, url: e.target.value })} placeholder="https://" />
        </label>
      )}
      {f.kind === "knowledge_page" && (
        <label className="flex flex-col gap-1 md:col-span-2">
          <span className="label text-muted">{tr("governance.compliancesections.pagina_chiave")}</span>
          <input className="field font-mono" required value={f.page} onChange={(e) => setF({ ...f, page: e.target.value })} placeholder="KB-12" />
        </label>
      )}
      <label className="flex flex-col gap-1 md:col-span-2">
        <span className="label text-muted">{tr("governance.compliancesections.nota")}</span>
        <textarea className="field min-h-[56px]" required={f.kind === "note"} value={f.note} onChange={(e) => setF({ ...f, note: e.target.value })} />
      </label>
      <label className="flex flex-col gap-1">
        <span className="label text-muted">{tr("governance.compliancesections.vale_per")}</span>
        <Select className="field" value={f.scope} onChange={(e) => setF({ ...f, scope: e.target.value })}>
          <option value="tenant">{tr("governance.compliancesections.tutta_l_organizzazione")}</option>
          <option value="application">{tr("governance.compliancesections.un_applicazione")}</option>
          <option value="process">{tr("governance.compliancesections.un_processo")}</option>
          <option value="legal_entity">{tr("governance.compliancesections.un_entita_legale")}</option>
        </Select>
      </label>
      {f.scope !== "tenant" ? (
        <label className="flex flex-col gap-1">
          <span className="label text-muted">{f.scope === "legal_entity" ? tr("governance.compliancesections.codice_dell_entita") : tr("governance.compliancesections.chiave_o_codice")}</span>
          <input className="field font-mono" required value={f.target} onChange={(e) => setF({ ...f, target: e.target.value })} placeholder="API-CLI" />
        </label>
      ) : (
        <span />
      )}
      <label className="flex flex-col gap-1">
        <span className="label text-muted">{tr("governance.compliancesections.valida_fino_al_vuoto_la")}</span>
        <input className="field" type="date" value={f.valid_until} onChange={(e) => setF({ ...f, valid_until: e.target.value })} />
      </label>
      <ErrorNote error={create.error} />
      <div className="flex gap-2 md:col-span-2">
        <button className="btn btn-primary" disabled={create.isPending}>{tr("governance.compliancesections.salva_in_bozza")}</button>
        <button type="button" className="btn" onClick={onDone}>{tr("governance.compliance.annulla")}</button>
      </div>
      <p className="m-0 text-[12px] text-muted md:col-span-2">{tr("governance.compliancesections.la_valida_un_altra_persona")}</p>
    </form>
  );
}

// ---------------------------------------------------------------------
// Evidenza

export function EvidenceSection({ refKey }: { refKey: string }) {
  const refresh = useRefresh(refKey);
  const ev = useQuery({ queryKey: ["grc", "evidence", "detail", refKey], queryFn: () => grcApi.evidence(refKey) });
  const [until, setUntil] = useState("");
  const [note, setNote] = useState("");
  const act = useMutation({
    mutationFn: (a: "validate" | "reject" | "revoke") =>
      a === "validate" ? grcApi.validate(refKey, until, note) : a === "reject" ? grcApi.reject(refKey, note) : grcApi.revoke(refKey, note),
    onSuccess: () => {
      setNote("");
      refresh();
    },
  });
  const e: EvidenceView | undefined = ev.data;
  if (ev.error) return <ErrorNote error={ev.error} />;
  if (!e) return <Loading className="m-0 text-muted" />;
  return (
    <section className="flex flex-col gap-3">
      <SectionTitle>{tr("governance.compliance.evidenza")}</SectionTitle>
      <Rows
        rows={[
          [tr("governance.compliance.stato"), <EvidenceMark status={e.status} />],
          [tr("governance.compliance.tipo"), `${EVIDENCE_KINDS[e.kind] ?? e.kind}${e.source === "automatic" ? tr("governance.compliancesections.automatica", { collector: collectorName(e.collector) }) : ""}`],
          [tr("governance.compliancesections.vale_per"), scopeLabel(e.scope)],
          [tr("governance.compliancesections.validita"), tr("governance.compliancesections.dal", { from: date(e.valid_from), to: e.valid_until ? tr("governance.compliancesections.al", { to: date(e.valid_until) }) : "" })],
          [
            tr("governance.compliance.controlli"),
            <span className="flex flex-col">
              {e.linked_controls.map((c) => (
                <span key={c.id}>
                  <Link to="/e/$key" params={{ key: c.key }} search={{ scheda: "dettagli" }} className="font-mono text-[12px]">
                    {c.key}
                  </Link>{" "}
                  {c.title}
                  {c.coverage === "partial" && <span className="text-[12px] text-muted"> · {tr("governance.compliancesections.copertura_parziale")}</span>}
                  {c.via === "ai" && <span className="text-[12px] text-muted"> · {tr("governance.compliancesections.collegata_con_assistente")}</span>}
                </span>
              ))}
            </span>,
          ],
          ...(e.url ? ([[tr("governance.compliancesections.link_2"), <a href={e.url} target="_blank" rel="noreferrer noopener">{e.url}</a>]] as [string, React.ReactNode][]) : []),
          ...(e.page
            ? ([
                [
                  tr("governance.compliancesections.pagina"),
                  <Link to="/e/$key" params={{ key: e.page.key }}>
                    {e.page.key} {e.page.title}
                  </Link>,
                ],
              ] as [string, React.ReactNode][])
            : []),
          [tr("governance.compliancesections.caricata_da"), e.created_by || "—"],
          ...(e.kind === "attachment" ? ([[tr("governance.compliancesections.allegati"), tr("governance.compliancesections.scheda_allegati", { attachments: e.attachments })]] as [string, React.ReactNode][]) : []),
          [
            tr("governance.compliancesections.firma"),
            e.signature ? (
              <span className="flex flex-col">
                <span>
                  {e.validated_by || e.collector}, {e.validated_at ? date(e.validated_at) : ""}{" "}
                  {e.signature_ok ? <span className="text-ok">{tr("governance.compliancesections.integra")}</span> : <span className="font-semibold text-danger">{tr("governance.compliancesections.il_contenuto_e_cambiato_dopo")}</span>}
                </span>
                <span className="font-mono text-[11px] break-all text-muted">{e.signature}</span>
              </span>
            ) : (
              "—"
            ),
          ],
          ...(e.status_note ? ([[tr("governance.compliancesections.nota"), e.status_note]] as [string, React.ReactNode][]) : []),
        ]}
      />
      {(e.can.validate || e.can.reject || e.can.revoke) && (
        <form className="flex flex-wrap items-end gap-2" onSubmit={(ev) => ev.preventDefault()}>
          {e.can.validate && (
            <label className="flex flex-col gap-1">
              <span className="label text-muted">{tr("governance.compliance.valida_fino_al")}</span>
              <input className="field" type="date" value={until} onChange={(ev) => setUntil(ev.target.value)} />
            </label>
          )}
          <label className="flex min-w-[220px] flex-1 flex-col gap-1">
            <span className="label text-muted">{tr("governance.compliancesections.nota")}</span>
            <input className="field" value={note} onChange={(ev) => setNote(ev.target.value)} />
          </label>
          {e.can.validate && (
            <button className="btn btn-primary" onClick={() => act.mutate("validate")} disabled={act.isPending}>{tr("governance.compliancesections.valida_e_firma")}</button>
          )}
          {e.can.reject && (
            <button className="btn" onClick={() => act.mutate("reject")} disabled={act.isPending || !note.trim()}>{tr("governance.compliancesections.respingi")}</button>
          )}
          {e.can.revoke && (
            <button className="btn" onClick={() => act.mutate("revoke")} disabled={act.isPending || !note.trim()}>{tr("governance.compliancesections.revoca")}</button>
          )}
        </form>
      )}
      {e.status === "draft" && !e.can.validate && <p className="m-0 text-[12.5px] text-muted">{tr("governance.compliancesections.in_attesa_della_validazione_da")}</p>}
      <ErrorNote error={act.error} />
    </section>
  );
}

// ---------------------------------------------------------------------
// Applicazione: normative a cui è soggetta

export function ApplicationCompliance({ appKey }: { appKey: string }) {
  const { read } = useGrcPerms();
  const sc = useQuery({ queryKey: ["grc", "scope", "application", appKey], queryFn: () => grcApi.scope("application", appKey), enabled: read });
  if (!read) return null;
  const s = sc.data;
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle
        aside={
          <Link to="/conformita" search={{ tab: "applicazioni", perimetro: `application:${appKey}` }} className="text-[12.5px]">{tr("governance.compliancesections.dettaglio")}</Link>
        }
      >{tr("governance.compliance.conformita")}</SectionTitle>
      <ErrorNote error={sc.error} />
      {s && s.frameworks.length === 0 && <p className="m-0 text-muted">{tr("governance.compliancesections.nessun_requisito_normativo_proprio_dell")}</p>}
      {s?.frameworks.map((f) => (
        <div key={f.key} className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b border-hair py-1.5">
          <FrameworkTag k={f.key} />
          <span className="min-w-0 flex-1">{f.name}</span>
          <CoverageMark status={f.worst} />
          <CoverageBar c={f} />
        </div>
      ))}
    </section>
  );
}

// ---------------------------------------------------------------------
// Requisito di progetto: riferimenti normativi

export function RequirementLinks({ refKey }: { refKey: string }) {
  const qc = useQueryClient();
  const links = useQuery({ queryKey: ["grc", "links", refKey], queryFn: () => grcApi.links(refKey) });
  const [editing, setEditing] = useState(false);
  const [text, setText] = useState("");
  const save = useMutation({
    mutationFn: () =>
      grcApi.setLinks(
        refKey,
        text
          .split(/\n/)
          .map((l) => l.trim())
          .filter(Boolean),
      ),
    onSuccess: () => {
      setEditing(false);
      qc.invalidateQueries({ queryKey: ["grc"] });
    },
  });
  const l = links.data;
  if (!l) return <ErrorNote error={links.error} />;
  const all = [...l.catalog.map((x) => ({ ...x, from: "catalogo" })), ...l.manual.map((x) => ({ ...x, from: "" }))];
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle
        aside={
          l.can_edit && !editing ? (
            <button
              className="text-[12.5px] text-accent"
              onClick={() => {
                setText(l.manual.map((x) => x.ref).join("\n"));
                setEditing(true);
              }}
            >{tr("governance.compliancesections.modifica")}</button>
          ) : undefined
        }
      >{tr("governance.compliancesections.riferimenti_normativi")}</SectionTitle>
      <p className="m-0 text-[12.5px] text-muted">{tr("governance.compliancesections.la_verifica_di_questo_requisito")}</p>
      {editing ? (
        <form
          className="flex flex-col gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            save.mutate();
          }}
        >
          <textarea className="field min-h-[80px] font-mono" value={text} onChange={(e) => setText(e.target.value)} /* i18n-ok: esempio di riferimento */ placeholder="dora:Art. 9" aria-label={tr("governance.compliancesections.riferimenti_uno_per_riga")} />
          <ErrorNote error={save.error} />
          <span className="flex gap-2">
            <button className="btn btn-primary" disabled={save.isPending}>{tr("governance.compliancesections.salva")}</button>
            <button type="button" className="btn" onClick={() => setEditing(false)}>{tr("governance.compliance.annulla")}</button>
          </span>
        </form>
      ) : (
        <ul className="m-0 flex list-none flex-col p-0">
          {all.map((x) => (
            <li key={x.from + x.ref} className="flex flex-wrap items-baseline gap-x-2 border-b border-hair py-1.5">
              <FrameworkTag k={x.framework} />
              <span className="font-mono text-[12px]">{x.req_ref}</span>
              <span className="min-w-0 flex-1">{x.found ? x.title : <span className="text-muted">{tr("governance.compliancesections.non_presente_nelle_normative_importate")}</span>}</span>
              {x.from && <span className="text-[12px] text-muted">{tr("governance.compliancesections.dalla_voce_del_catalogo")}</span>}
            </li>
          ))}
          {all.length === 0 && <li className="py-1.5 text-muted">{tr("governance.compliancesections.nessuno")}</li>}
        </ul>
      )}
    </section>
  );
}
