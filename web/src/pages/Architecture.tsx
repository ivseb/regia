import { EntitySelect, Select } from "../select";
import { Fragment, lazy, Suspense, useRef, useState, type FormEvent } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type Asset, type ImportReport } from "../api";
import { PageHeader, useMe } from "../shell";
import { CatalogId } from "../catalogId";
import { CRITICALITY, Criticality, ErrorNote, SectionTitle, Loading } from "../ui";
import { workApi } from "../workApi";
import { proposeKey } from "../boardKey";
import { EaTabLinks } from "./ea/tabs";
import { useTypeNames } from "../typesApi";
import { tr } from "../i18n/core";
import { Trans } from "../i18n/Trans";
import { crumbs } from "../crumbs";

// Catalogo di architettura (piano, passo 2): asset logici con la loro composizione,
// applicazioni con il team owner, team. Le richieste citano gli asset; l'analisi
// tecnica crea gli impatti sulle applicazioni.

type Tab = "asset" | "applicazioni" | "team";

// Metriche di consegna DORA del team (DevSecOps, fase 5a), caricate solo quando si aprono.
const DoraCard = lazy(() => import("./Dora").then((m) => ({ default: m.DoraCard })));

export function Architecture({ tab }: { tab: Tab }) {
  const perms = useMe().data?.active_tenant?.permissions ?? [];
  const canWrite = perms.includes("*") || perms.includes("architecture.write");
  const tabs: { id: Tab; label: string }[] = [
    { id: "asset", label: tr("governance.architecture.asset_logici") },
    { id: "applicazioni", label: tr("governance.compliance.applicazioni") },
    { id: "team", label: tr("governance.requirements.team") },
  ];
  return (
    <>
      <PageHeader path={crumbs("governance", "architecture")} title={tr("governance.architecture.architettura")} />
      <div role="tablist" aria-label={tr("governance.risks.sezione")} className="flex gap-6 overflow-x-auto border-b border-hair px-4 md:px-7">
        {tabs.map((t) => (
          <Link
            key={t.id}
            role="tab"
            aria-selected={tab === t.id}
            to="/architettura"
            search={{ tab: t.id }}
            className={`pb-2.5 no-underline ${tab === t.id ? "border-b-2 border-fg font-semibold text-fg" : "text-muted hover:text-fg"}`}
          >
            {t.label}
          </Link>
        ))}
        {/* Architettura enterprise (piano, passo 20): mappa, capability, dipendenze, razionalizzazione, diagrammi. */}
        <EaTabLinks />
      </div>
      <div className="flex flex-col gap-8 px-4 md:px-7 py-6">
        {tab === "asset" && <Assets canWrite={canWrite} />}
        {tab === "applicazioni" && <Applications canWrite={canWrite} />}
        {tab === "team" && <Teams canWrite={canWrite} />}
      </div>
    </>
  );
}

function Assets({ canWrite }: { canWrite: boolean }) {
  const qc = useQueryClient();
  const assets = useQuery({ queryKey: ["assets"], queryFn: api.assets });
  const [name, setName] = useState("");
  const [kind, setKind] = useState("product");
  const create = useMutation({
    mutationFn: () => api.createAsset({ name, kind }),
    onSuccess: () => {
      setName("");
      qc.invalidateQueries({ queryKey: ["assets"] });
    },
  });
  return (
    <>
      <p className="m-0 max-w-3xl text-muted">{tr("governance.architecture.un_asset_logico_e_cio")}</p>
      {canWrite && (
        <form
          className="flex flex-wrap items-end gap-3"
          onSubmit={(e: FormEvent) => {
            e.preventDefault();
            create.mutate();
          }}
        >
          <label className="flex min-w-0 flex-[1_1_280px] flex-col gap-1">
            <span className="label">{tr("governance.architecture.nuovo_asset_logico")}</span>
            <input className="field" required value={name} onChange={(e) => setName(e.target.value)} placeholder={tr("governance.architecture.es_area_riservata")} />
          </label>
          <label className="flex w-40 flex-col gap-1">
            <span className="label">{tr("governance.compliance.tipo")}</span>
            <Select className="field" value={kind} onChange={(e) => setKind(e.target.value)}>
              {Object.entries(KINDS).map(([k, v]) => (
                <option key={k} value={k}>
                  {v}
                </option>
              ))}
            </Select>
          </label>
          <button className="btn btn-primary" disabled={create.isPending}>{tr("governance.compliance.aggiungi")}</button>
        </form>
      )}
      <ErrorNote error={create.error ?? assets.error} />
      {assets.data?.items.length === 0 && <p className="text-muted">{tr("governance.architecture.nessun_asset_logico_aggiungine_uno")}</p>}
      {assets.data?.items.map((a) => <AssetBlock key={a.id} asset={a} canWrite={canWrite} />)}
    </>
  );
}

const KINDS: Record<string, string> = { get product() { return tr("governance.architecture.prodotto"); }, get service() { return tr("governance.architecture.servizio"); }, get channel() { return tr("governance.architecture.canale"); }, get capability() { return tr("governance.architecture.capacita"); }, get other() { return tr("governance.architecture.altro"); } };

function AssetBlock({ asset, canWrite }: { asset: Asset; canWrite: boolean }) {
  const qc = useQueryClient();
  const [adding, setAdding] = useState(false);
  const [appId, setAppId] = useState("");
  const [rel, setRel] = useState("composed_of");
  const done = () => {
    qc.invalidateQueries({ queryKey: ["assets"] });
    qc.invalidateQueries({ queryKey: ["applications"] });
  };
  const add = useMutation({
    mutationFn: () => api.addComponent(asset.id, appId, rel),
    onSuccess: () => {
      setAppId("");
      done();
    },
  });
  const remove = useMutation({ mutationFn: (app: string) => api.removeComponent(asset.id, app), onSuccess: done });
  const linked = new Set(asset.components.map((c) => c.application_id));
  return (
    <section className="flex flex-col gap-2">
      <SectionTitle
        aside={
          <span className="flex items-center gap-3 text-[12px] text-muted">
            <span className="font-mono">{asset.key}</span>
            <span>{KINDS[asset.kind] ?? asset.kind}</span>
            {asset.state !== "Attivo" && <span className="text-flag">{asset.state}</span>}
            {canWrite && !adding && (
              <button className="text-accent underline" onClick={() => setAdding(true)}>{tr("governance.architecture.collega_applicazione")}</button>
            )}
          </span>
        }
      >
        {asset.name}
      </SectionTitle>
      {asset.components.length === 0 && <p className="m-0 text-[12.5px] text-muted">{tr("governance.riskdetail.nessuna_applicazione_collegata")}</p>}
      {asset.components.length > 0 && (
        <div className="overflow-x-auto">
        <table className="w-full border-collapse">
          <tbody>
            {asset.components.map((c) => (
              <tr key={c.application_id} className="border-b border-hair">
                <td className="w-32 py-1.5 pr-3 font-mono text-[12px] text-accent">
                  <Link to="/e/$key" params={{ key: c.code ?? c.key }} className="no-underline">
                    <CatalogId code={c.code} itemKey={c.key} />
                  </Link>
                </td>
                <td className="px-3 py-1.5 font-medium">{c.name}</td>
                <td className="px-3 py-1.5 text-[12.5px] text-muted">{c.relation === "uses" ? tr("governance.architecture.usa") : tr("governance.architecture.composto_da")}</td>
                <td className="px-3 py-1.5">{c.team ?? <span className="text-flag">{tr("governance.vulnerabilities.senza_team")}</span>}</td>
                <td className="px-3 py-1.5">
                  <Criticality value={c.criticality} />
                </td>
                <td className="py-1.5 pl-3 text-right">
                  {canWrite && (
                    <button className="text-[12px] text-muted underline hover:text-danger" disabled={remove.isPending} onClick={() => remove.mutate(c.application_id)}>{tr("governance.riskui.scollega")}</button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        </div>
      )}
      {adding && (
        <form
          className="flex flex-wrap items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            add.mutate();
          }}
        >
          <EntitySelect kind="application" aria-label={tr("governance.requirements.applicazione")} className="field max-w-sm" required noneLabel={tr("governance.riskdetail.scegli_un_applicazione")} filter={(o) => !linked.has(o.value)} value={appId} onChange={setAppId} />
          <Select aria-label={tr("governance.architecture.relazione")} className="field w-40" value={rel} onChange={(e) => setRel(e.target.value)}>
            <option value="composed_of">{tr("governance.architecture.composto_da")}</option>
            <option value="uses">{tr("governance.architecture.usa")}</option>
          </Select>
          <button className="btn btn-primary" disabled={add.isPending}>{tr("governance.riskdetail.collega")}</button>
          <button type="button" className="btn" onClick={() => setAdding(false)}>{tr("governance.compliance.chiudi")}</button>
        </form>
      )}
      <ErrorNote error={add.error ?? remove.error} />
    </section>
  );
}

function Applications({ canWrite }: { canWrite: boolean }) {
  const [search, setSearch] = useState("");
  const apps = useQuery({ queryKey: ["applications", search], queryFn: () => api.applications(search) });
  return (
    <>
      <div className="flex flex-wrap items-end gap-3">
        <label className="flex min-w-0 flex-[1_1_280px] flex-col gap-1">
          <span className="label">{tr("governance.risks.cerca")}</span>
          <input className="field" value={search} onChange={(e) => setSearch(e.target.value)} placeholder={tr("governance.architecture.nome_codice_o_chiave")} />
        </label>
      </div>
      {canWrite && <ImportCSV />}
      {canWrite && <NewApplication />}
      <ErrorNote error={apps.error} />
      <div className="overflow-x-auto">
        <table className="w-full min-w-[760px] border-collapse">
          <thead>
            <tr className="label h-9 text-left">
              <th scope="col" className="border-b-2 border-rule pr-3">{tr("governance.architecture.codice")}</th>
              <th scope="col" className="border-b-2 border-rule px-3">{tr("governance.requirements.applicazione")}</th>
              <th scope="col" className="border-b-2 border-rule px-3">{tr("governance.architecture.team_owner")}</th>
              <th scope="col" className="border-b-2 border-rule px-3">{tr("governance.riskdetail.criticita")}</th>
              <th scope="col" className="border-b-2 border-rule px-3">{tr("governance.architecture.dati")}</th>
              <th scope="col" className="border-b-2 border-rule px-3">{tr("governance.devsecops.asset")}</th>
              <th scope="col" className="border-b-2 border-rule pl-3">{tr("governance.risks.fase")}</th>
            </tr>
          </thead>
          <tbody>
            {apps.data?.items.map((a) => (
              <tr key={a.id} className="border-b border-hair hover:bg-sel/50">
                <td className="py-2 pr-3 text-[12px] whitespace-nowrap">
                  <CatalogId code={a.code} itemKey={a.key} />
                </td>
                <td className="px-3 py-2">
                  <Link to="/e/$key" params={{ key: a.key }} className="font-medium text-fg no-underline hover:underline">
                    {a.name}
                  </Link>
                </td>
                <td className="px-3 py-2">{a.team ?? <span className="text-flag">{tr("governance.vulnerabilities.senza_team")}</span>}</td>
                <td className="px-3 py-2">
                  <Criticality value={a.criticality} critical={a.supports_critical_function} />
                </td>
                <td className="px-3 py-2">{a.data_classification ?? "—"}</td>
                <td className="px-3 py-2 text-[12.5px]">{a.assets.join(", ") || "—"}</td>
                <td className="py-2 pl-3 text-[12.5px] whitespace-nowrap">{a.state}</td>
              </tr>
            ))}
            {apps.data?.items.length === 0 && (
              <tr>
                <td colSpan={7} className="py-8 text-muted">{tr("governance.architecture.nessuna_applicazione")}{search ? tr("governance.architecture.corrisponde_alla_ricerca") : tr("governance.architecture.importa_il_catalogo_da_csv")}.</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </>
  );
}

function NewApplication() {
  const tn = useTypeNames();
  const qc = useQueryClient();
  const [open, setOpen] = useState(false);
  const [f, setF] = useState({ name: "", code: "", criticality: "", team_id: "", critical: false, data: "" });
  const create = useMutation({
    mutationFn: () =>
      api.createApplication({
        name: f.name,
        code: f.code || undefined,
        criticality: f.criticality || undefined,
        team_id: f.team_id || undefined,
        supports_critical_function: f.critical,
        data_classification: f.data || undefined,
      } as never),
    onSuccess: () => {
      setF({ name: "", code: "", criticality: "", team_id: "", critical: false, data: "" });
      setOpen(false);
      qc.invalidateQueries({ queryKey: ["applications"] });
    },
  });
  if (!open)
    return (
      <button className="btn self-start" onClick={() => setOpen(true)}>
        {tn.create("application", tr("governance.requirements.applicazione"))}
      </button>
    );
  return (
    <form
      className="flex flex-wrap items-end gap-3 border-l-2 border-accent pl-4"
      onSubmit={(e) => {
        e.preventDefault();
        create.mutate();
      }}
    >
      <label className="flex min-w-0 flex-[1_1_240px] flex-col gap-1">
        <span className="label">{tr("governance.compliance.nome")}</span>
        <input className="field" required value={f.name} onChange={(e) => setF({ ...f, name: e.target.value })} />
      </label>
      <label className="flex w-32 flex-col gap-1">
        <span className="label">{tr("governance.architecture.codice")}</span>
        <input className="field font-mono" value={f.code} onChange={(e) => setF({ ...f, code: e.target.value })} />
      </label>
      <label className="flex w-40 flex-col gap-1">
        <span className="label">{tr("governance.architecture.team_owner")}</span>
        <EntitySelect kind="team" noneLabel="—" className="field" value={f.team_id} onChange={(v) => setF({ ...f, team_id: v })} />
      </label>
      <label className="flex w-32 flex-col gap-1">
        <span className="label">{tr("governance.riskdetail.criticita")}</span>
        <Select className="field" value={f.criticality} onChange={(e) => setF({ ...f, criticality: e.target.value })}>
          <option value="">—</option>
          {Object.entries(CRITICALITY).map(([k, v]) => (
            <option key={k} value={k}>
              {v}
            </option>
          ))}
        </Select>
      </label>
      <label className="flex w-36 flex-col gap-1">
        <span className="label">{tr("governance.architecture.dati")}</span>
        <input className="field" value={f.data} onChange={(e) => setF({ ...f, data: e.target.value })} placeholder={tr("governance.architecture.es_personali")} />
      </label>
      <label className="flex items-center gap-2 pb-2">
        <input type="checkbox" checked={f.critical} onChange={(e) => setF({ ...f, critical: e.target.checked })} />
        <span>{tr("governance.architecture.funzione_critica_dora")}</span>
      </label>
      <button className="btn btn-primary" disabled={create.isPending}>{tr("governance.architecture.crea")}</button>
      <button type="button" className="btn" onClick={() => setOpen(false)}>{tr("governance.compliance.annulla")}</button>
      <div className="basis-full">
        <ErrorNote error={create.error} />
      </div>
    </form>
  );
}

function ImportCSV() {
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
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-3">
        <input
          ref={file}
          type="file"
          accept=".csv,text/csv"
          className="hidden"
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
        <button className="btn" onClick={() => file.current?.click()} disabled={run.isPending}>{tr("governance.architecture.importa_da_csv")}</button>
        <span className="text-[12px] text-muted">
          <Trans k="governance.architecture.colonne_asset_prova" slots={{ cols: /* i18n-ok: nomi delle colonne del file */ <code className="font-mono">codice; nome; criticita; funzione_critica; classificazione_dati; team; asset</code> }} />
        </span>
      </div>
      {report && (
        <div className="flex flex-col gap-2 border-l-2 border-accent pl-4">
          <p className="m-0">
            {report.dry_run ? tr("governance.architecture.prova") : tr("governance.architecture.importate")}
            <Trans k="governance.architecture.nuove_aggiornate" slots={{ created: <span className="font-semibold">{report.created}</span>, updated: <span className="font-semibold">{report.updated}</span> }} />
            {report.errors > 0 && (
              <>
                , <span className="font-semibold text-danger">{tr("governance.architecture.righe_con_errori", { count: report.errors })}</span>
              </>
            )}
            {report.teams_created.length > 0 && tr("governance.architecture.team_nuovi", { p: report.teams_created.join(", ") })}
            {report.assets_created.length > 0 && tr("governance.architecture.asset_nuovi", { p: report.assets_created.join(", ") })}.
          </p>
          {report.rows
            .filter((r) => r.outcome === "error")
            .map((r) => (
              <p key={r.line} className="m-0 text-[12.5px] text-danger">{tr("governance.architecture.riga", { line: r.line })}{" "}{r.name && `(${r.name})`}: {r.message}
              </p>
            ))}
          {report.dry_run && csv && (
            <div className="flex gap-2">
              <button className="btn btn-primary" disabled={run.isPending || report.created + report.updated === 0} onClick={() => run.mutate({ text: csv, dry: false })}>{tr("governance.architecture.conferma_l_import")}</button>
              <button
                className="btn"
                onClick={() => {
                  setCsv(null);
                  setReport(null);
                }}
              >{tr("governance.compliance.annulla")}</button>
            </div>
          )}
        </div>
      )}
      <ErrorNote error={run.error} />
    </div>
  );
}

/** Spazio di delivery del team (work management): la board, oppure il pulsante per crearlo. */
function TeamSpaceCell({ teamId, canWrite }: { teamId: string; canWrite: boolean }) {
  const qc = useQueryClient();
  const spaces = useQuery({ queryKey: ["work", "spaces"], queryFn: workApi.spaces, staleTime: 30_000 });
  const create = useMutation({
    mutationFn: (key: string) => workApi.createSpace(teamId, key),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["work", "spaces"] }),
  });
  const sp = spaces.data?.items.find((s) => s.team_id === teamId);
  if (sp)
    return (
      <Link to="/delivery/board" search={{ spazio: sp.key }} className="font-mono text-[12px] text-accent no-underline hover:underline">{tr("governance.architecture.board", { key: sp.key })}</Link>
    );
  const proposed = spaces.data?.teams_without_space.find((t) => t.id === teamId)?.proposed_key;
  if (!proposed) return null;
  return canWrite ? (
    <span className="flex flex-col gap-0.5">
      <button className="btn self-start py-0.5 text-[12.5px]" disabled={create.isPending} onClick={() => create.mutate(proposed)}>{tr("governance.architecture.crea_lo_spazio", { proposed })}</button>
      {create.error && <span className="text-[12px] text-danger">{create.error.message}</span>}
    </span>
  ) : (
    <span className="text-[12px] text-muted">{tr("governance.devsecops.nessuno")}</span>
  );
}

function Teams({ canWrite }: { canWrite: boolean }) {
  const qc = useQueryClient();
  const teams = useQuery({ queryKey: ["teams"], queryFn: api.teams });
  const [name, setName] = useState("");
  const [lead, setLead] = useState("");
  // Board del team (pilota home lab, difetto 7): chiave vuota = quella proposta dal nome.
  const [boardKey, setBoardKey] = useState("");
  const [boardModel, setBoardModel] = useState("");
  const [delivery, setDelivery] = useState<string | null>(null); // team con il riquadro delle metriche aperto
  const create = useMutation({
    mutationFn: () => api.createTeam(name, lead || undefined, { board_key: boardKey.trim().toUpperCase(), board_model: boardModel }),
    onSuccess: () => {
      setName("");
      setLead("");
      setBoardKey("");
      setBoardModel("");
      qc.invalidateQueries({ queryKey: ["teams"] });
      qc.invalidateQueries({ queryKey: ["work", "spaces"] }); // il team nuovo ha già il suo spazio
    },
  });
  const setLeadOf = useMutation({
    mutationFn: ({ id, name, lead, capacity }: { id: string; name: string; lead: string; capacity?: number }) =>
      api.updateTeam(id, name, lead || undefined, capacity),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["teams"] }),
  });
  return (
    <>
      <p className="m-0 max-w-3xl text-muted">{tr("governance.architecture.il_referente_del_team_diventa")}</p>
      {canWrite && (
        <form
          className="flex flex-wrap items-end gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            create.mutate();
          }}
        >
          <label className="flex min-w-0 flex-[1_1_240px] flex-col gap-1">
            <span className="label">{tr("governance.architecture.nuovo_team")}</span>
            <input className="field" required value={name} onChange={(e) => setName(e.target.value)} />
          </label>
          <label className="flex w-56 flex-col gap-1">
            <span className="label">{tr("governance.riskdetail.referente")}</span>
            <EntitySelect kind="member" noneLabel="—" className="field" value={lead} onChange={(v) => setLead(v)} />
          </label>
          <label className="flex w-40 flex-col gap-1">
            <span className="label">{tr("governance.team.chiave")}</span>
            <input className="field font-mono uppercase" maxLength={20} value={boardKey} placeholder={name.trim() ? proposeKey(name) : ""}
              pattern="[A-Za-z][A-Za-z0-9_]{0,19}" onChange={(e) => setBoardKey(e.target.value)} aria-describedby="team-chiave-aiuto" />
          </label>
          <label className="flex w-56 flex-col gap-1">
            <span className="label">{tr("governance.team.modello")}</span>
            <Select className="field" value={boardModel} onChange={(e) => setBoardModel(e.target.value)}>
              <option value="">{tr("governance.team.modello.nessuno")}</option>
              <option value="scrum">{tr("governance.team.modello.scrum")}</option>
              <option value="kanban">{tr("governance.team.modello.kanban")}</option>
              <option value="supporto">{tr("governance.team.modello.supporto")}</option>
            </Select>
          </label>
          <button className="btn btn-primary" disabled={create.isPending}>{tr("governance.compliance.aggiungi")}</button>
          <p id="team-chiave-aiuto" className="m-0 basis-full text-[12.5px] text-muted">{tr("governance.team.chiaveAiuto")}</p>
        </form>
      )}
      <ErrorNote error={create.error ?? setLeadOf.error ?? teams.error} />
      <div className="overflow-x-auto">
      <table className="w-full max-w-3xl border-collapse">
        <thead>
          <tr className="label h-9 text-left">
            <th scope="col" className="border-b-2 border-rule pr-3">{tr("governance.requirements.team")}</th>
            <th scope="col" className="border-b-2 border-rule px-3">{tr("governance.riskdetail.referente")}</th>
            <th scope="col" className="border-b-2 border-rule px-3 text-right">{tr("governance.architecture.capacita_gg_sett")}</th>
            <th scope="col" className="border-b-2 border-rule pl-3 text-right">{tr("governance.compliance.applicazioni")}</th>
            <th scope="col" className="border-b-2 border-rule pl-3">{tr("governance.architecture.spazio_di_delivery")}</th>
          </tr>
        </thead>
        <tbody>
          {teams.data?.items.map((t) => (
            <Fragment key={t.id}>
            <tr className="border-b border-hair">
              <td className="py-2 pr-3 font-medium">
                {t.name}
                <button
                  type="button"
                  aria-expanded={delivery === t.id}
                  className="ml-3 bg-transparent p-0 text-[12px] font-normal text-accent underline"
                  onClick={() => setDelivery(delivery === t.id ? null : t.id)}
                >{tr("governance.architecture.consegna")}</button>
              </td>
              <td className="px-3 py-1.5">
                {canWrite ? (
                  <EntitySelect kind="member" noneLabel={tr("governance.architecture.nessuno")} aria-label={tr("governance.architecture.referente_del_team", { name: t.name })} className="field py-1" value={t.lead_id ?? ""} onChange={(v) => setLeadOf.mutate({ id: t.id, name: t.name, lead: v })} />
                ) : (
                  (t.lead ?? <span className="text-flag">{tr("governance.devsecops.nessuno")}</span>)
                )}
              </td>
              <td className="px-3 py-1.5 text-right">
                {canWrite ? (
                  <input
                    aria-label={tr("governance.architecture.capacita_del_team", { name: t.name })}
                    className="field w-20 py-1 text-right font-mono"
                    inputMode="decimal"
                    defaultValue={t.capacity_days_week ?? 5}
                    onBlur={(e) => {
                      const v = Number(e.target.value.replace(",", "."));
                      if (!Number.isNaN(v) && v !== (t.capacity_days_week ?? 5)) setLeadOf.mutate({ id: t.id, name: t.name, lead: t.lead_id ?? "", capacity: v });
                    }}
                  />
                ) : (
                  <span className="font-mono text-[12px]">{t.capacity_days_week ?? 5}</span>
                )}
              </td>
              <td className="py-2 pl-3 text-right font-mono text-[12px]">{t.applications}</td>
              <td className="py-1.5 pl-3">
                <TeamSpaceCell teamId={t.id} canWrite={canWrite} />
              </td>
            </tr>
            {delivery === t.id && (
              <tr className="border-b border-hair">
                <td colSpan={5} className="py-3">
                  <Suspense fallback={<Loading className="m-0 text-muted" />}>
                    <DoraCard scope="team" subject={t.id} />
                  </Suspense>
                </td>
              </tr>
            )}
            </Fragment>
          ))}
        </tbody>
      </table>
      </div>
    </>
  );
}
