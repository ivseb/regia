// Requisiti (piano, passi 5 e 6): scheda «Requisiti» della richiesta e dell'impatto con la
// matrice di tracciabilità, dettaglio del requisito (celle, verifica con evidenze e firma,
// deroghe), dettaglio della deroga, inbox «Da verificare». Il modulo si carica alla prima
// visita: il bundle iniziale non cresce.
import { Select } from "../select";
import { useMemo, useState, type FormEvent, type ReactNode } from "react";
import { Link } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { collab } from "../api";
import {
  ORIGIN_LABELS,
  STATUS_LABELS,
  requirementsApi,
  type CellStatus,
  type CreateRequirementInput,
  type ExceptionView,
  type Matrix,
  type MatrixCell,
  type MatrixFilter,
  type RequirementCell,
  type RequirementView,
} from "../requirementsApi";
import { PageHeader } from "../shell";
import { ErrorNote, SectionTitle, ago, date, Loading } from "../ui";
import { useNarrow } from "./timeline";
import { ProblemActions } from "./ProblemActions";
import { tr } from "../i18n/core";
import { loc } from "../i18n/format";
import { crumbs } from "../crumbs";

// ---------------------------------------------------------------------
// Stato di una cella: quadratino e parola, mai solo il colore.
// ---------------------------------------------------------------------

const STATUS_STYLE: Record<CellStatus, { dot: string; text: string }> = {
  proposed: { dot: "border border-rule bg-transparent", text: "text-muted" },
  open: { dot: "bg-fg", text: "text-fg" },
  to_verify: { dot: "bg-accent", text: "text-accent" },
  verified: { dot: "bg-ok", text: "text-ok" },
  waived: { dot: "bg-flag", text: "text-flag" },
  not_applicable: { dot: "bg-hair", text: "text-muted line-through" },
};

export function StatusMark({ status, short, title }: { status: CellStatus; short?: boolean; title?: string }) {
  const s = STATUS_STYLE[status];
  return (
    <span className={`inline-flex items-center gap-1.5 whitespace-nowrap text-[12.5px] ${s.text}`} title={title ?? STATUS_LABELS[status]}>
      <span className={`size-2 shrink-0 ${s.dot}`} aria-hidden="true" />
      {short ? <span className="sr-only">{STATUS_LABELS[status]}</span> : STATUS_LABELS[status]}
    </span>
  );
}

function cellTitle(c: MatrixCell): string {
  const parts = [STATUS_LABELS[c.status]];
  if (c.status === "waived" && c.exception_key) parts.push(tr("governance.requirements.deroga_fino_al", { exception_key: c.exception_key, date: date(c.expires_on) }));
  if (c.exception_expired) parts.push(tr("governance.requirements.deroga_scaduta", { p: c.exception_key ?? "" }));
  if (c.verified_by) parts.push(tr("governance.requirements.verificato_da", { verified_by: c.verified_by }));
  return parts.join(" · ");
}

function Count({ n, label, tone }: { n: number; label: string; tone?: string }) {
  return (
    <span className={tone ?? ""}>
      <span className="font-semibold tabular">{n}</span> {label}
    </span>
  );
}

// ---------------------------------------------------------------------
// Scheda «Requisiti»: matrice requisiti × impatti
// ---------------------------------------------------------------------

/** Righe della matrice caricate per volta: con 300 requisiti × 30 impatti la matrice intera pesa 1,3 MB. */
const MATRIX_PAGE = 50;

export default function RequirementsSection({ refKey, isImpact }: { refKey: string; isImpact: boolean }) {
  const [flt, setFltState] = useState<MatrixFilter>({});
  const [limit, setLimit] = useState(MATRIX_PAGE);
  const setFlt = (f: MatrixFilter) => {
    setFltState(f);
    setLimit(MATRIX_PAGE);
  };
  const [creating, setCreating] = useState(false);
  // La matrice di un impatto ha una sola colonna e pochi requisiti: intera. Quella di una richiesta a blocchi.
  const matrix = useQuery({
    queryKey: ["requirements", "matrix", refKey, flt, isImpact ? 0 : limit],
    queryFn: () => requirementsApi.matrix(refKey, flt, isImpact ? undefined : limit),
    placeholderData: (p) => p,
  });
  const narrow = useNarrow();
  const m = matrix.data;
  const filtered = !!(flt.team || flt.category || flt.status);
  return (
    <section className="flex flex-col gap-3" aria-label={tr("governance.requirements.requisiti")}>
      <SectionTitle
        aside={
          m?.can.create && !creating ? (
            <button className="text-[12px] text-accent underline" onClick={() => setCreating(true)}>{tr("governance.requirements.nuovo_requisito")}</button>
          ) : undefined
        }
      >{tr("governance.requirements.requisiti")}{" "}{isImpact ? tr("governance.requirements.dell_applicazione") : tr("governance.requirements.e_matrice_di_tracciabilita")}
      </SectionTitle>
      <ErrorNote error={matrix.error} />
      {m && <SummaryLine m={m} isImpact={isImpact} />}
      {creating && m && <CreateForm refKey={refKey} matrix={m} isImpact={isImpact} onDone={() => setCreating(false)} />}
      {m && (m.rows.length > 0 || filtered) && (
        <div className="flex flex-wrap items-end gap-2">
          {!isImpact && m.teams.length > 1 && (
            <FilterSelect label={tr("governance.requirements.team")} value={flt.team} options={m.teams.map((t) => [t, t])} onChange={(team) => setFlt({ ...flt, team })} />
          )}
          <FilterSelect label={tr("governance.requirements.categoria")} value={flt.category} options={m.categories.map((c) => [c, c])} onChange={(category) => setFlt({ ...flt, category })} />
          <FilterSelect
            label={tr("governance.compliance.stato")}
            value={flt.status}
            options={(Object.keys(STATUS_LABELS) as CellStatus[]).map((s) => [s, STATUS_LABELS[s]])}
            onChange={(status) => setFlt({ ...flt, status })}
          />
          {filtered && (
            <button className="btn py-1 text-[12.5px]" onClick={() => setFlt({})}>{tr("governance.requirements.togli_i_filtri")}</button>
          )}
          <a className="btn ml-auto py-1 text-[12.5px] no-underline" href={requirementsApi.csvUrl(refKey, flt)} download>{tr("governance.requirements.esporta_csv")}</a>
        </div>
      )}
      {m && m.rows.length === 0 && (
        <p className="m-0 text-[12.5px] text-muted">
          {filtered
            ? tr("governance.requirements.nessun_requisito_con_questi_filtri")
            : tr("governance.requirements.nessun_requisito_nascono_dalle_baseline")}
        </p>
      )}
      {m && m.rows.length > 0 && (narrow || m.columns.length <= 1 ? <MatrixList m={m} /> : <MatrixTable m={m} />)}
      {m && m.more && (
        <div className="flex flex-wrap items-center gap-3 text-[12.5px]">
          <span className="text-muted tabular">{tr("governance.requirements.di_requisiti", { length: m.rows.length, total_rows: m.total_rows })}</span>
          <button className="btn py-1 text-[12.5px]" disabled={matrix.isFetching} onClick={() => setLimit(limit + MATRIX_PAGE)}>{tr("governance.requirements.mostra_altri", { p: Math.min(MATRIX_PAGE, m.total_rows - m.rows.length) })}</button>
          <button className="text-accent underline" disabled={matrix.isFetching} onClick={() => setLimit(m.total_rows)}>{tr("governance.requirements.mostra_tutti")}</button>
        </div>
      )}
    </section>
  );
}

function SummaryLine({ m, isImpact }: { m: Matrix; isImpact: boolean }) {
  const s = m.summary;
  if (s.requisiti === 0) return null;
  return (
    <div className="flex flex-col gap-1.5">
      <p className="m-0 flex flex-wrap gap-x-4 gap-y-1 text-[12.5px] text-muted">
        <Count n={s.requisiti} label={isImpact ? tr("governance.requirements.applicabili") : tr("governance.requirements.requisiti_2")} />
        <Count n={s.requisiti_aperti} label={tr("governance.requirements.da_accettare_o_realizzare")} />
        <Count n={s.requisiti_da_verificare} label={tr("governance.requirements.da_verificare")} tone={s.requisiti_da_verificare ? "text-accent" : ""} />
        <Count n={s.requisiti_verificati} label={tr("governance.requirements.verificati")} />
        {s.requisiti_in_deroga > 0 && <Count n={s.requisiti_in_deroga} label={tr("governance.requirements.in_deroga")} tone="text-flag" />}
        {s.deroghe_scadute > 0 && <Count n={s.deroghe_scadute} label={tr("governance.requirements.deroghe_scadute")} tone="font-semibold text-danger" />}
      </p>
      {s.pronto_al_rilascio ? (
        <p className="m-0 self-start border-l-2 border-ok pl-2 text-[12.5px] font-semibold text-ok">{tr("governance.requirements.pronto_al_rilascio_tutti_verificati")}</p>
      ) : (
        <p className="m-0 self-start border-l-2 border-flag pl-2 text-[12.5px] text-fg">
          <span className="font-semibold">{tr("governance.requirements.non_pronto_al_rilascio")}</span>{" "}{tr("governance.requirements.mancano_requisiti", { count: s.mancanti_totale ?? s.mancanti.length })}
        </p>
      )}
    </div>
  );
}

function FilterSelect({ label, value, options, onChange }: { label: string; value?: string; options: [string, string][]; onChange: (v?: string) => void }) {
  return (
    <label className="flex flex-col gap-0.5">
      <span className="label text-muted">{label}</span>
      <Select className="field py-1 text-[12.5px]" value={value ?? ""} onChange={(e) => onChange(e.target.value || undefined)}>
        <option value="">{tr("governance.requirements.tutti")}</option>
        {options.map(([v, l]) => (
          <option key={v} value={v}>
            {l}
          </option>
        ))}
      </Select>
    </label>
  );
}

function RowHead({ r }: { r: Matrix["rows"][number] }) {
  return (
    <span className="flex min-w-0 flex-col gap-0.5">
      <span className="min-w-0">
        <Link to="/e/$key" params={{ key: r.key }} className="font-mono text-[12px] font-medium text-accent no-underline hover:underline">
          {r.key}
        </Link>{" "}
        <span className={r.status === "not_applicable" ? "text-muted line-through" : ""}>{r.title}</span>
      </span>
      <span className="text-[11.5px] text-muted">
        {r.scope === "project" ? tr("governance.requirements.progetto") : tr("governance.requirements.applicazione")}
        {r.category ? ` · ${r.category}` : ""} · {ORIGIN_LABELS[r.origin]}
        {r.method === "evidence" ? tr("governance.requirements.da_evidenza") : ""}
      </span>
    </span>
  );
}

/** Matrice per desktop: righe i requisiti, colonne gli impatti, prima colonna fissa. */
function MatrixTable({ m }: { m: Matrix }) {
  return (
    <div className="overflow-x-auto border-t border-hair">
      <table className="w-full border-collapse text-[13px]">
        <thead>
          <tr className="text-left">
            <th scope="col" className="label sticky left-0 z-10 min-w-[260px] border-b-2 border-rule bg-bg py-2 pr-3">{tr("governance.requirements.requisito")}</th>
            {m.columns.map((c) => (
              <th key={c.impact_id} scope="col" className="min-w-[132px] border-b-2 border-rule px-2 py-2 align-bottom font-normal">
                <Link to="/e/$key" params={{ key: c.impact_key }} className="block font-semibold text-fg no-underline hover:text-accent">
                  {c.application}
                </Link>
                <span className="block text-[11.5px] text-muted">
                  <span className="font-mono">{c.impact_key}</span>
                  {c.team ? ` · ${c.team}` : ""}
                </span>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {m.rows.map((r) => (
            <tr key={r.id} className="border-b border-hair align-top hover:bg-sel/40">
              <th scope="row" className="sticky left-0 z-10 bg-bg py-2 pr-3 text-left font-normal">
                <RowHead r={r} />
              </th>
              {r.cells.map((c, i) => (
                <td key={m.columns[i]?.impact_id ?? i} className="px-2 py-2">
                  {c ? (
                    <span className="flex flex-col gap-0.5">
                      <StatusMark status={c.status} title={cellTitle(c)} />
                      {c.status === "waived" && c.expires_on && <span className="text-[11.5px] text-muted tabular">{tr("governance.requirements.fino_al", { date: date(c.expires_on) })}</span>}
                      {c.exception_expired && <span className="text-[11.5px] font-semibold text-danger">{tr("governance.requirements.deroga_scaduta_2")}</span>}
                    </span>
                  ) : (
                    <span className="text-muted" aria-label={tr("governance.requirements.non_si_applica")}>·</span>
                  )}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/** Matrice per telefono (e per un solo impatto): elenco dei requisiti con lo stato per applicazione. */
function MatrixList({ m }: { m: Matrix }) {
  return (
    <ul className="m-0 flex list-none flex-col border-t border-hair p-0">
      {m.rows.map((r) => (
        <li key={r.id} className="flex flex-col gap-1.5 border-b border-hair py-2.5">
          <RowHead r={r} />
          <span className="flex flex-col gap-0.5">
            {r.cells.map((c, i) =>
              c ? (
                <span key={m.columns[i].impact_id} className="flex items-baseline justify-between gap-3 text-[12.5px]">
                  <span className="min-w-0 truncate text-muted">{m.columns.length > 1 || !m.impact ? m.columns[i].application : tr("governance.compliance.stato")}</span>
                  <span className="flex shrink-0 flex-col items-end">
                    <StatusMark status={c.status} title={cellTitle(c)} />
                    {c.status === "waived" && c.expires_on && <span className="text-[11.5px] text-muted tabular">{tr("governance.requirements.fino_al", { date: date(c.expires_on) })}</span>}
                    {c.exception_expired && <span className="text-[11.5px] font-semibold text-danger">{tr("governance.requirements.deroga_scaduta_2")}</span>}
                  </span>
                </span>
              ) : null,
            )}
            {r.cells.every((c) => !c) && (
              <span className="flex justify-between text-[12.5px]">
                <span className="text-muted">{tr("governance.requirements.nessun_impatto")}</span>
                <StatusMark status={r.status} />
              </span>
            )}
          </span>
        </li>
      ))}
    </ul>
  );
}

type Verifier = { team: string; role: string };

function VerifierFields({ v, onChange, teams, roles }: { v: Verifier; onChange: (v: Verifier) => void; teams: { key: string; name: string }[]; roles: { key: string; name: string }[] }) {
  const byRole = v.role !== "" || (v.team === "" && teams.length === 0);
  return (
    <fieldset className="m-0 flex flex-col gap-1.5 border-0 p-0">
      <legend className="label mb-1">{tr("governance.requirements.chi_verifica")}</legend>
      <div className="flex gap-4 text-[13px]">
        <label className="flex items-center gap-1.5">
          <input type="radio" checked={!byRole} onChange={() => onChange({ team: teams[0]?.key ?? "", role: "" })} />{" "}{tr("governance.requirements.un_team_valutatore")}</label>
        <label className="flex items-center gap-1.5">
          <input type="radio" checked={byRole} onChange={() => onChange({ team: "", role: roles[0]?.key ?? "" })} />{" "}{tr("governance.requirements.un_ruolo")}</label>
      </div>
      {byRole ? (
        <Select className="field" aria-label={tr("governance.requirements.ruolo_che_verifica")} value={v.role} onChange={(e) => onChange({ team: "", role: e.target.value })}>
          {roles.map((r) => (
            <option key={r.key} value={r.key}>
              {r.name}
            </option>
          ))}
        </Select>
      ) : (
        <Select className="field" aria-label={tr("governance.requirements.team_che_verifica")} value={v.team} onChange={(e) => onChange({ team: e.target.value, role: "" })}>
          {teams.map((t) => (
            <option key={t.key} value={t.key}>
              {t.name}
            </option>
          ))}
        </Select>
      )}
    </fieldset>
  );
}

function CreateForm({ refKey, matrix, isImpact, onDone }: { refKey: string; matrix: Matrix; isImpact: boolean; onDone: () => void }) {
  const qc = useQueryClient();
  const cfg = useQuery({ queryKey: ["requirement-config"], queryFn: requirementsApi.config, staleTime: 60_000 });
  const [f, setF] = useState<CreateRequirementInput>({ title: "", text: "", category: "", scope: isImpact ? "application" : "project", verification_method: "manual" });
  const [ver, setVer] = useState<Verifier>({ team: "", role: "" });
  const teams = (cfg.data?.teams ?? []).filter((t) => !t.archived);
  const roles = cfg.data?.roles ?? [];
  const v = ver.team === "" && ver.role === "" && teams[0] ? { team: teams[0].key, role: "" } : ver;
  const create = useMutation({
    mutationFn: () => requirementsApi.create(refKey, { ...f, verifier_team: v.team || undefined, verifier_role: v.role || undefined }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["requirements"] });
      qc.invalidateQueries({ queryKey: ["element", refKey] });
      onDone();
    },
  });
  const baseline = useMutation({
    mutationFn: (catalog: string) => requirementsApi.applyBaseline(refKey, catalog),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["requirements"] }),
  });
  const set = (p: Partial<CreateRequirementInput>) => setF({ ...f, ...p });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    create.mutate();
  };
  return (
    <form className="flex flex-col gap-3 border-l-2 border-accent pl-3" onSubmit={submit}>
      <label className="flex flex-col gap-1">
        <span className="label">{tr("governance.compliance.titolo")}</span>
        <input className="field" required maxLength={500} value={f.title} onChange={(e) => set({ title: e.target.value })} />
      </label>
      <label className="flex flex-col gap-1">
        <span className="label">{tr("governance.requirements.testo")}</span>
        <textarea className="field min-h-20" value={f.text} onChange={(e) => set({ text: e.target.value })} />
      </label>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <label className="flex flex-col gap-1">
          <span className="label">{tr("governance.requirements.categoria")}</span>
          <input className="field" list="categorie-requisiti" maxLength={120} value={f.category} onChange={(e) => set({ category: e.target.value })} />
          <datalist id="categorie-requisiti">
            {matrix.categories.map((c) => (
              <option key={c} value={c} />
            ))}
          </datalist>
        </label>
        {!isImpact && (
          <label className="flex flex-col gap-1">
            <span className="label">{tr("governance.requirements.ambito")}</span>
            <Select className="field" value={f.scope === "project" ? "" : f.impact ?? ""} onChange={(e) => set(e.target.value ? { scope: "application", impact: e.target.value } : { scope: "project", impact: undefined })}>
              <option value="">{tr("governance.requirements.progetto_tutte_le_applicazioni")}</option>
              {matrix.columns.map((c) => (
                <option key={c.impact_id} value={c.impact_key}>{tr("governance.requirements.solo", { application: c.application, impact_key: c.impact_key })}</option>
              ))}
            </Select>
          </label>
        )}
      </div>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <label className="flex flex-col gap-1">
          <span className="label">{tr("governance.requirements.criterio_di_verifica")}</span>
          <Select className="field" value={f.verification_method} onChange={(e) => set({ verification_method: e.target.value as "manual" | "evidence" })}>
            <option value="manual">{tr("governance.requirements.manuale_con_la_firma_di")}</option>
            <option value="evidence">{tr("governance.requirements.automatico_da_evidenza_devsecops")}</option>
          </Select>
        </label>
        {f.verification_method === "evidence" && (
          <label className="flex flex-col gap-1">
            <span className="label">{tr("governance.compliance.controllo")}</span>
            <input className="field font-mono" placeholder="sca_block" value={f.verification_check ?? ""} onChange={(e) => set({ verification_check: e.target.value })} />
          </label>
        )}
      </div>
      <label className="flex flex-col gap-1">
        <span className="label">{tr("governance.requirements.come_si_verifica")}</span>
        <input className="field" value={f.verification_criterion ?? ""} onChange={(e) => set({ verification_criterion: e.target.value })} />
      </label>
      {cfg.data && <VerifierFields v={v} onChange={setVer} teams={teams} roles={roles} />}
      <div className="flex flex-wrap items-center gap-2">
        <button className="btn btn-primary" disabled={create.isPending || !f.title.trim()}>{tr("governance.requirements.crea_il_requisito")}</button>
        <button type="button" className="btn" onClick={onDone}>{tr("governance.compliance.annulla")}</button>
        {cfg.data && cfg.data.catalogs.filter((c) => c.version).length > 0 && (
          <span className="ml-auto flex items-center gap-2 text-[12.5px] text-muted">{tr("governance.requirements.oppure_applica_le_baseline_di")}{cfg.data.catalogs
              .filter((c) => c.version)
              .map((c) => (
                <button key={c.key} type="button" className="btn py-1 text-[12.5px]" disabled={baseline.isPending} onClick={() => baseline.mutate(c.key)}>
                  {c.name}
                </button>
              ))}
          </span>
        )}
      </div>
      {baseline.data && <p className="m-0 text-[12.5px] text-muted">{baseline.data.created.length ? tr("governance.requirements.creati_requisiti", { count: baseline.data.created.length }) : tr("governance.requirements.nessun_requisito_nuovo_le_baseline")}</p>}
      <ErrorNote error={create.error ?? baseline.error} />
    </form>
  );
}

// ---------------------------------------------------------------------
// Dettaglio del requisito (scheda «Dettagli» della pagina dell'elemento)
// ---------------------------------------------------------------------

function Fact({ k, children }: { k: string; children: ReactNode }) {
  return (
    <div className="contents">
      <dt className="border-b border-hair py-1.5 text-muted">{k}</dt>
      <dd className="m-0 min-w-0 border-b border-hair py-1.5">{children}</dd>
    </div>
  );
}

export function RequirementDetail({ refKey }: { refKey: string }) {
  const view = useQuery({ queryKey: ["requirements", "view", refKey], queryFn: () => requirementsApi.get(refKey) });
  const v = view.data;
  if (view.error) return <ErrorNote error={view.error} />;
  if (!v) return <Loading className="m-0 text-muted" />;
  const r = v.requirement;
  return (
    <>
      <section className="flex flex-col gap-2" aria-label={tr("governance.requirements.il_requisito")}>
        <SectionTitle>{tr("governance.requirements.il_requisito")}</SectionTitle>
        <dl className="m-0 grid grid-cols-[minmax(96px,32%)_minmax(0,1fr)] text-[13px]">
          <Fact k={tr("governance.requirements.ambito")}>
            {r.scope === "project" ? (
              <>{tr("governance.requirements.progetto_tutte_le_applicazioni_di")}{" "}<Link to="/e/$key" params={{ key: r.request_key }} className="font-mono text-accent no-underline hover:underline">
                  {r.request_key}
                </Link>
              </>
            ) : (
              <>{tr("governance.requirements.applicazione_2", { application: r.application ?? "" })}{" "}<Link to="/e/$key" params={{ key: r.impact_key ?? "" }} className="font-mono text-accent no-underline hover:underline">
                  {r.impact_key}
                </Link>
              </>
            )}
          </Fact>
          <Fact k={tr("governance.requirements.origine")}>
            {r.origin === "prescription" && r.review_key ? (
              <span className="flex flex-col gap-0.5">
                <span>{tr("governance.requirements.prescrizione_della_valutazione")}{" "}<Link to="/valutazioni/$key" params={{ key: r.review_key }} className="font-mono text-accent no-underline hover:underline">
                    {r.review_key}
                  </Link>
                </span>
                {r.prescription && <span className="text-[12.5px] text-muted">«{r.prescription}»</span>}
              </span>
            ) : r.origin === "questionnaire" && r.review_key ? (
              <span>
                {tr("governance.requisitiQ.origine.regola")}{" "}
                <Link to="/valutazioni/$key" params={{ key: r.review_key }} className="font-mono text-accent no-underline hover:underline">
                  {r.review_key}
                </Link>
              </span>
            ) : r.origin === "catalog" ? (
              <span>
                {r.catalog_name ?? r.catalog_key} <span className="font-mono tabular">v{r.catalog_version}</span>{" "}{tr("governance.requirements.voce")}{" "}<span className="font-mono">{r.catalog_item}</span>
                {r.baseline_key && <span className="text-muted">{tr("governance.requirements.baseline", { baseline_key: r.baseline_key })}</span>}
              </span>
            ) : (
              tr("governance.requirements.inserito_a_mano")
            )}
          </Fact>
          <Fact k={tr("governance.requirements.categoria")}>{r.category || "—"}</Fact>
          <Fact k={tr("governance.requirements.verifica")}>
            {r.verification_method === "evidence" ? (
              <span className="flex flex-col gap-0.5">
                <span>{tr("governance.requirements.automatica_da_evidenza_controllo")}{" "}<span className="font-mono">{r.verification_check}</span>
                </span>
                <span className="text-[12.5px] text-muted">{tr("governance.requirements.si_verifica_da_solo_quando")}</span>
              </span>
            ) : (
              tr("governance.requirements.manuale_con_la_firma_di")
            )}
            {r.verification_criterion && <span className="block text-[12.5px] text-muted">{r.verification_criterion}</span>}
          </Fact>
          <Fact k={tr("governance.requirements.chi_verifica")}>{r.verifier_team ? tr("governance.requirements.team_2", { verifier_team: r.verifier_team }) : r.verifier_role_name ? tr("governance.requirements.ruolo", { verifier_role_name: r.verifier_role_name }) : "—"}</Fact>
        </dl>
        <p className="m-0 text-[12.5px] text-muted">{tr("governance.requirements.chi_realizza_il_requisito_o")}</p>
      </section>
      <Cells v={v} refKey={refKey} />
      <Verifications v={v} />
      <Exceptions v={v} />
    </>
  );
}

function useRefreshRequirement(refKey: string) {
  const qc = useQueryClient();
  return () => {
    qc.invalidateQueries({ queryKey: ["requirements"] });
    qc.invalidateQueries({ queryKey: ["element", refKey] });
    qc.invalidateQueries({ queryKey: ["activity"] });
    qc.invalidateQueries({ queryKey: ["inbox"] });
  };
}

type Action = { kind: "ready" | "verify"; cell: RequirementCell } | { kind: "exception" } | null;

function Cells({ v, refKey }: { v: RequirementView; refKey: string }) {
  const [action, setAction] = useState<Action>(null);
  return (
    <section className="flex flex-col gap-2" aria-label={tr("governance.compliance.applicazioni")}>
      <SectionTitle
        aside={
          v.can.exception && action?.kind !== "exception" ? (
            <button className="text-[12px] text-accent underline" onClick={() => setAction({ kind: "exception" })}>{tr("governance.requirements.chiedi_una_deroga")}</button>
          ) : undefined
        }
      >{tr("governance.compliance.applicazioni")}</SectionTitle>
      {v.cells.length === 0 && <p className="m-0 text-[12.5px] text-muted">{tr("governance.requirements.nessun_impatto_il_requisito_di")}</p>}
      <ul className="m-0 list-none p-0">
        {v.cells.map((c) => (
          <li key={c.impact_id} className="flex flex-col gap-1.5 border-b border-hair py-2.5">
            <span className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
              <span className="min-w-0">
                <span className="font-semibold">{c.application}</span>{" "}
                <Link to="/e/$key" params={{ key: c.impact_key }} className="font-mono text-[12px] text-accent no-underline hover:underline">
                  {c.impact_key}
                </Link>
                {c.team && <span className="text-[12.5px] text-muted"> · {c.team}</span>}
              </span>
              <StatusMark status={c.status} />
            </span>
            <span className="flex flex-wrap gap-x-3 text-[12px] text-muted">
              {c.implemented_by && <span>{tr("governance.requirements.realizzato_da", { implemented_by: c.implemented_by })}{" "}{c.implemented_at ? ago(c.implemented_at) : ""}</span>}
              {c.verified_by && <span>{tr("governance.requirements.verificato_da_2", { verified_by: c.verified_by })}{" "}{c.verified_at ? ago(c.verified_at) : ""}</span>}
              {c.status === "waived" && c.exception_key && (
                <span className="text-flag">{tr("governance.requirements.in_deroga_fino_al", { exception_key: c.exception_key, date: date(c.expires_on) })}</span>
              )}
              {c.exception_expired && <span className="font-semibold text-danger">{tr("governance.requirements.la_deroga_e_scaduta", { exception_key: c.exception_key ?? "" })}</span>}
            </span>
            {(c.can.ready || c.can.verify || c.can.why_not) && (
              <span className="flex flex-wrap items-center gap-2">
                {c.can.ready && (
                  <button className="btn py-1 text-[12.5px]" onClick={() => setAction({ kind: "ready", cell: c })}>{tr("governance.requirements.pronto_per_la_verifica")}</button>
                )}
                {c.can.verify && (
                  <button className="btn btn-primary py-1 text-[12.5px]" onClick={() => setAction({ kind: "verify", cell: c })}>{tr("governance.requirements.verifica")}</button>
                )}
                {c.can.why_not && <span className="text-[12px] text-muted">{c.can.why_not}</span>}
              </span>
            )}
            {action && action.kind !== "exception" && action.cell.impact_id === c.impact_id && (
              action.kind === "ready" ? (
                <ReadyForm refKey={refKey} cell={c} onDone={() => setAction(null)} />
              ) : (
                <VerifyForm refKey={refKey} cell={c} onDone={() => setAction(null)} />
              )
            )}
          </li>
        ))}
      </ul>
      {action?.kind === "exception" && <ExceptionForm v={v} refKey={refKey} onDone={() => setAction(null)} />}
      {v.cells.some((c) => !["verified", "waived", "not_applicable"].includes(c.status)) && <ProblemActions kind="requirement" refKey={refKey} />}
    </section>
  );
}

function ReadyForm({ refKey, cell, onDone }: { refKey: string; cell: RequirementCell; onDone: () => void }) {
  const refresh = useRefreshRequirement(refKey);
  const [note, setNote] = useState("");
  const run = useMutation({
    mutationFn: () => requirementsApi.ready(refKey, cell.impact_key, note),
    onSuccess: () => {
      refresh();
      onDone();
    },
  });
  return (
    <form
      className="flex flex-col gap-2 border-l-2 border-accent pl-3"
      onSubmit={(e) => {
        e.preventDefault();
        run.mutate();
      }}
    >
      <label className="flex flex-col gap-1">
        <span className="label">{tr("governance.requirements.cosa_e_stato_fatto_facoltativo")}</span>
        <textarea className="field min-h-16" value={note} onChange={(e) => setNote(e.target.value)} />
      </label>
      <span className="flex gap-2">
        <button className="btn btn-primary" disabled={run.isPending}>{tr("governance.requirements.passa_alla_verifica")}</button>
        <button type="button" className="btn" onClick={onDone}>{tr("governance.compliance.annulla")}</button>
      </span>
      <ErrorNote error={run.error} />
    </form>
  );
}

function VerifyForm({ refKey, cell, onDone }: { refKey: string; cell: RequirementCell; onDone: () => void }) {
  const refresh = useRefreshRequirement(refKey);
  const atts = useQuery({ queryKey: ["attachments", refKey], queryFn: () => collab.attachments(refKey) });
  const [outcome, setOutcome] = useState<"verified" | "rejected">("verified");
  const [note, setNote] = useState("");
  const [links, setLinks] = useState("");
  const [chosen, setChosen] = useState<string[]>([]);
  const [evidence, setEvidence] = useState("");
  const run = useMutation({
    mutationFn: () =>
      requirementsApi.verify(refKey, cell.impact_key, {
        outcome,
        note,
        links: links.split(/\s+/).filter(Boolean),
        attachments: chosen,
        evidence_element: evidence.trim() || undefined,
      }),
    onSuccess: () => {
      refresh();
      onDone();
    },
  });
  const files = (atts.data?.items ?? []).filter((a) => !a.comment_id);
  return (
    <form
      className="flex flex-col gap-3 border-l-2 border-accent pl-3"
      onSubmit={(e) => {
        e.preventDefault();
        run.mutate();
      }}
    >
      <fieldset className="m-0 flex flex-wrap gap-4 border-0 p-0 text-[13px]">
        <legend className="label mb-1">{tr("governance.requirements.esito_per", { application: cell.application })}</legend>
        <label className="flex items-center gap-1.5">
          <input type="radio" name="esito" checked={outcome === "verified"} onChange={() => setOutcome("verified")} />{" "}{tr("governance.requirements.verificato")}</label>
        <label className="flex items-center gap-1.5">
          <input type="radio" name="esito" checked={outcome === "rejected"} onChange={() => setOutcome("rejected")} />{" "}{tr("governance.requirements.non_superato_torna_da_realizzare")}</label>
      </fieldset>
      <label className="flex flex-col gap-1">
        <span className="label">{outcome === "rejected" ? tr("governance.requirements.motivazione") : tr("governance.requirements.nota_dell_evidenza")}</span>
        <textarea className="field min-h-16" required={outcome === "rejected"} value={note} onChange={(e) => setNote(e.target.value)} />
      </label>
      <label className="flex flex-col gap-1">
        <span className="label">{tr("governance.requirements.link_alle_evidenze_uno_per")}</span>
        <textarea className="field min-h-12 font-mono text-[12.5px]" placeholder="https://…" value={links} onChange={(e) => setLinks(e.target.value)} />
      </label>
      {files.length > 0 && (
        <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
          <legend className="label mb-1">{tr("governance.requirements.allegati_del_requisito")}</legend>
          {files.map((a) => (
            <label key={a.id} className="flex items-center gap-2 text-[13px]">
              <input
                type="checkbox"
                checked={chosen.includes(a.id)}
                onChange={(e) => setChosen(e.target.checked ? [...chosen, a.id] : chosen.filter((x) => x !== a.id))}
              />
              <span className="min-w-0 truncate">{a.name}</span>
              <span className="font-mono text-[11px] text-muted">{a.sha256.slice(0, 12)}</span>
            </label>
          ))}
        </fieldset>
      )}
      {files.length === 0 && <p className="m-0 text-[12px] text-muted">{tr("governance.requirements.per_allegare_un_file_all")}</p>}
      <label className="flex flex-col gap-1">
        <span className="label">{tr("governance.requirements.elemento_di_evidenza_facoltativo")}</span>
        <input className="field font-mono" placeholder="DEM-42" value={evidence} onChange={(e) => setEvidence(e.target.value)} />
      </label>
      <p className="m-0 text-[12px] text-muted">{tr("governance.requirements.la_verifica_si_firma_con")}</p>
      <span className="flex gap-2">
        <button className="btn btn-primary" disabled={run.isPending}>{tr("governance.requirements.firma_e_registra")}</button>
        <button type="button" className="btn" onClick={onDone}>{tr("governance.compliance.annulla")}</button>
      </span>
      <ErrorNote error={run.error} />
    </form>
  );
}

function plusDays(days: number): string {
  const d = new Date();
  d.setDate(d.getDate() + days);
  return d.toISOString().slice(0, 10);
}

function ExceptionForm({ v, refKey, onDone }: { v: RequirementView; refKey: string; onDone: () => void }) {
  const refresh = useRefreshRequirement(refKey);
  const eligible = v.cells.filter((c) => c.can.exception);
  const [impacts, setImpacts] = useState<string[]>(eligible.map((c) => c.impact_key));
  const [reason, setReason] = useState("");
  const [risk, setRisk] = useState(v.settings.risk_levels[0]?.key ?? "");
  const [expires, setExpires] = useState(plusDays(90));
  const [comp, setComp] = useState("");
  const run = useMutation({
    mutationFn: () => requirementsApi.requestException(refKey, { impacts, reason, risk_level: risk, expires_on: expires, compensating: comp }),
    onSuccess: () => {
      refresh();
      onDone();
    },
  });
  return (
    <form
      className="flex flex-col gap-3 border-l-2 border-flag pl-3"
      onSubmit={(e) => {
        e.preventDefault();
        run.mutate();
      }}
    >
      <p className="m-0 text-[12.5px] text-muted">{tr("governance.requirements.la_deroga_copre_le_applicazioni")}</p>
      {eligible.length > 1 && (
        <fieldset className="m-0 flex flex-col gap-1 border-0 p-0">
          <legend className="label mb-1">{tr("governance.compliance.applicazioni")}</legend>
          {eligible.map((c) => (
            <label key={c.impact_id} className="flex items-center gap-2 text-[13px]">
              <input
                type="checkbox"
                checked={impacts.includes(c.impact_key)}
                onChange={(e) => setImpacts(e.target.checked ? [...impacts, c.impact_key] : impacts.filter((x) => x !== c.impact_key))}
              />
              {c.application} <span className="font-mono text-[12px] text-muted">{c.impact_key}</span>
            </label>
          ))}
        </fieldset>
      )}
      <label className="flex flex-col gap-1">
        <span className="label">{tr("governance.requirements.motivazione")}</span>
        <textarea className="field min-h-20" required value={reason} onChange={(e) => setReason(e.target.value)} />
      </label>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <label className="flex flex-col gap-1">
          <span className="label">{tr("governance.requirements.rischio_residuo")}</span>
          <Select className="field" value={risk} onChange={(e) => setRisk(e.target.value)}>
            {v.settings.risk_levels.map((l) => (
              <option key={l.key} value={l.key}>
                {l.name}
              </option>
            ))}
          </Select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{tr("governance.requirements.scadenza")}</span>
          <input className="field" type="date" required min={plusDays(1)} max={plusDays(v.settings.max_exception_days)} value={expires} onChange={(e) => setExpires(e.target.value)} />
        </label>
      </div>
      <label className="flex flex-col gap-1">
        <span className="label">{tr("governance.requirements.misure_compensative")}</span>
        <textarea className="field min-h-16" value={comp} onChange={(e) => setComp(e.target.value)} />
      </label>
      <span className="flex gap-2">
        <button className="btn btn-primary" disabled={run.isPending || impacts.length === 0}>{tr("governance.requirements.chiedi_l_approvazione")}</button>
        <button type="button" className="btn" onClick={onDone}>{tr("governance.compliance.annulla")}</button>
      </span>
      <ErrorNote error={run.error} />
    </form>
  );
}

function Verifications({ v }: { v: RequirementView }) {
  if (v.verifications.length === 0) return null;
  return (
    <section className="flex flex-col gap-2" aria-label={tr("governance.requirements.verifiche")}>
      <SectionTitle aside={<span className="font-mono text-[11.5px] text-muted tabular">{v.verifications.length}</span>}>{tr("governance.requirements.verifiche_firmate")}</SectionTitle>
      <ol className="m-0 flex list-none flex-col p-0">
        {v.verifications.map((x) => (
          <li key={x.id} className="flex flex-col gap-1 border-b border-hair py-2.5 text-[13px]">
            <span className="flex flex-wrap items-baseline justify-between gap-x-3">
              <span>
                <StatusMark short status={x.outcome === "verified" ? "verified" : "open"} /* i18n-ok: stato, non un testo */ title={x.outcome === "verified" ? tr("governance.requirements.verificato") : tr("governance.requirements.non_superato")} />{" "}
                <span className="font-semibold">{x.outcome === "verified" ? tr("governance.requirements.verificato") : tr("governance.requirements.non_superato")}</span>
                <span className="text-muted"> · {x.impact_key} · {x.by ?? (x.method === "automatic" ? tr("governance.requirements.automatica", { p: x.collector ?? "pipeline" }) : "—")}</span>
              </span>
              <time dateTime={x.at} className="text-[12px] text-muted tabular" title={new Date(x.at).toLocaleString(loc())}>
                {new Date(x.at).toLocaleString(loc(), { dateStyle: "short", timeStyle: "short" })}
              </time>
            </span>
            {x.note && <span className="whitespace-pre-wrap">{x.note}</span>}
            {(x.links.length > 0 || x.attachments.length > 0 || x.evidence_key) && (
              <span className="flex flex-col gap-0.5 text-[12.5px]">
                {x.links.map((l) => (
                  <a key={l} href={l} target="_blank" rel="noreferrer noopener" className="truncate text-accent">
                    {l}
                  </a>
                ))}
                {x.attachments.map((a) => (
                  <a key={a.id} href={collab.contentUrl(a.id, true)} className="truncate text-accent" title={`SHA-256 ${a.sha256}`}>
                    {a.name}
                  </a>
                ))}
                {x.evidence_key && (
                  <Link to="/e/$key" params={{ key: x.evidence_key }} className="text-accent">
                    {x.evidence_key} {x.evidence_title}
                  </Link>
                )}
              </span>
            )}
            <span className="font-mono text-[11px] text-muted" title={x.signature}>{tr("governance.requirements.firma", { p: x.signature.slice(0, 16) })}</span>
          </li>
        ))}
      </ol>
    </section>
  );
}

const EXCEPTION_STAGE: Record<string, string> = {
  get draft() { return tr("governance.requirements.bozza"); },
  get pending() { return tr("governance.requirements.in_approvazione"); },
  get approved() { return tr("governance.requirements.in_vigore"); },
  get rejected() { return tr("governance.requirements.respinta"); },
  get expired() { return tr("governance.requirements.scaduta"); },
  get revoked() { return tr("governance.requirements.revocata"); },
};

function Exceptions({ v }: { v: RequirementView }) {
  if (v.exceptions.length === 0) return null;
  return (
    <section className="flex flex-col gap-2" aria-label={tr("governance.requirements.deroghe")}>
      <SectionTitle>{tr("governance.requirements.deroghe")}</SectionTitle>
      <ul className="m-0 list-none p-0">
        {v.exceptions.map((e) => (
          <li key={e.id} className="border-b border-hair">
            <Link to="/e/$key" params={{ key: e.key }} className="flex flex-wrap items-baseline justify-between gap-x-3 py-2 text-fg no-underline hover:bg-sel/50">
              <span>
                <span className="font-mono text-[12px] text-accent">{e.key}</span> <span className="font-semibold">{e.phase || EXCEPTION_STAGE[e.stage]}</span>
                <span className="text-[12.5px] text-muted">{" "}{tr("governance.requirements.rischio", { p: e.risk_name.toLowerCase() })}{" "}{e.impacts.join(", ")}</span>
              </span>
              <span className={`text-[12.5px] tabular ${e.stage === "expired" ? "font-semibold text-danger" : "text-muted"}`}>{tr("governance.requirements.scade_il", { date: date(e.expires_on) })}</span>
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}

// ---------------------------------------------------------------------
// Dettaglio della deroga
// ---------------------------------------------------------------------

export function ExceptionDetail({ refKey }: { refKey: string }) {
  const view = useQuery({ queryKey: ["requirements", "exception", refKey], queryFn: () => requirementsApi.exception(refKey) });
  const [editing, setEditing] = useState(false);
  const e = view.data;
  if (view.error) return <ErrorNote error={view.error} />;
  if (!e) return <Loading className="m-0 text-muted" />;
  const soon = e.stage === "approved" && e.days_left <= e.settings.expiry_notice_days;
  return (
    <section className="flex flex-col gap-2" aria-label={tr("governance.requirements.la_deroga")}>
      <SectionTitle
        aside={
          e.can.edit && !editing ? (
            <button className="text-[12px] text-accent underline" onClick={() => setEditing(true)}>{tr("governance.compliancesections.modifica")}</button>
          ) : undefined
        }
      >{tr("governance.requirements.la_deroga")}</SectionTitle>
      {editing ? (
        <ExceptionEdit e={e} refKey={refKey} onDone={() => setEditing(false)} />
      ) : (
        <dl className="m-0 grid grid-cols-[minmax(96px,32%)_minmax(0,1fr)] text-[13px]">
          <Fact k={tr("governance.requirements.requisito")}>
            <Link to="/e/$key" params={{ key: e.requirement_key }} className="font-mono text-accent no-underline hover:underline">
              {e.requirement_key}
            </Link>{" "}
            {e.requirement_title}
          </Fact>
          <Fact k={tr("governance.compliance.applicazioni")}>
            {e.impacts.map((k, i) => (
              <span key={k}>
                {i > 0 && ", "}
                <Link to="/e/$key" params={{ key: k }} className="font-mono text-accent no-underline hover:underline">
                  {k}
                </Link>
              </span>
            ))}
          </Fact>
          <Fact k={tr("governance.compliance.stato")}>{EXCEPTION_STAGE[e.stage]}</Fact>
          <Fact k={tr("governance.requirements.rischio_residuo")}>
            <span className={e.risk_score >= 3 ? "font-semibold" : ""}>{e.risk_name}</span> <span className="font-mono text-[12px] text-muted">({e.risk_score})</span>
          </Fact>
          <Fact k={tr("governance.requirements.scadenza")}>
            <span className={e.stage === "expired" ? "font-semibold text-danger" : soon ? "font-semibold text-flag" : ""}>{date(e.expires_on)}</span>
            {e.stage === "approved" && <span className="text-[12.5px] text-muted tabular">{tr("governance.requirements.tra_giorni", { count: e.days_left })}</span>}
          </Fact>
          <Fact k={tr("governance.requirements.motivazione")}>
            <span className="whitespace-pre-wrap">{e.reason}</span>
          </Fact>
          <Fact k={tr("governance.requirements.misure_compensative")}>
            <span className="whitespace-pre-wrap">{e.compensating || "—"}</span>
          </Fact>
          <Fact k={tr("governance.requirements.chiesta_da")}>{e.requested_by ?? "—"}</Fact>
        </dl>
      )}
      {e.stage === "pending" && <p className="m-0 text-[12.5px] text-muted">{tr("governance.requirements.le_decisioni_stanno_nella_scheda")}</p>}
      {(e.stage === "expired" || e.stage === "rejected") && (
        <p className="m-0 text-[12.5px] text-muted">{tr("governance.requirements.per_rinnovarla_riaprila_in_bozza")}</p>
      )}
    </section>
  );
}

function ExceptionEdit({ e, refKey, onDone }: { e: ExceptionView; refKey: string; onDone: () => void }) {
  const qc = useQueryClient();
  const [reason, setReason] = useState(e.reason);
  const [risk, setRisk] = useState(e.risk_level);
  const [expires, setExpires] = useState(e.expires_on);
  const [comp, setComp] = useState(e.compensating);
  const run = useMutation({
    mutationFn: () => requirementsApi.updateException(refKey, { reason, risk_level: risk, expires_on: expires, compensating: comp }),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["requirements"] });
      qc.invalidateQueries({ queryKey: ["element", refKey] });
      onDone();
    },
  });
  return (
    <form
      className="flex flex-col gap-3"
      onSubmit={(ev) => {
        ev.preventDefault();
        run.mutate();
      }}
    >
      <label className="flex flex-col gap-1">
        <span className="label">{tr("governance.requirements.motivazione")}</span>
        <textarea className="field min-h-20" required value={reason} onChange={(ev) => setReason(ev.target.value)} />
      </label>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        <label className="flex flex-col gap-1">
          <span className="label">{tr("governance.requirements.rischio_residuo")}</span>
          <Select className="field" value={risk} onChange={(ev) => setRisk(ev.target.value)}>
            {e.settings.risk_levels.map((l) => (
              <option key={l.key} value={l.key}>
                {l.name}
              </option>
            ))}
          </Select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="label">{tr("governance.requirements.scadenza")}</span>
          <input className="field" type="date" required min={plusDays(1)} max={plusDays(e.settings.max_exception_days)} value={expires} onChange={(ev) => setExpires(ev.target.value)} />
        </label>
      </div>
      <label className="flex flex-col gap-1">
        <span className="label">{tr("governance.requirements.misure_compensative")}</span>
        <textarea className="field min-h-16" value={comp} onChange={(ev) => setComp(ev.target.value)} />
      </label>
      <span className="flex gap-2">
        <button className="btn btn-primary" disabled={run.isPending}>{tr("governance.compliancesections.salva")}</button>
        <button type="button" className="btn" onClick={onDone}>{tr("governance.compliance.annulla")}</button>
      </span>
      <ErrorNote error={run.error} />
    </form>
  );
}

// ---------------------------------------------------------------------
// Inbox «Da verificare»
// ---------------------------------------------------------------------

const INBOX_MAX = 5000;

export function VerifyInbox() {
  // Le prime 500 celle (le più vecchie); con more si chiede il resto, a blocchi, fino a 5.000.
  const [limit, setLimit] = useState<number | undefined>(undefined);
  const inbox = useQuery({
    queryKey: limit ? ["requirements", "inbox", limit] : ["requirements", "inbox"],
    queryFn: () => requirementsApi.inbox(limit),
    placeholderData: (p) => p,
  });
  const items = useMemo(() => inbox.data?.items ?? [], [inbox.data]);
  return (
    <>
      <PageHeader path={crumbs("demand", "requirements")} title={tr("governance.requirements.da_verificare_2")} />
      <div className="flex flex-col gap-3 px-4 pb-10 md:px-7">
        <p className="m-0 max-w-3xl text-[13px] text-muted">{tr("governance.requirements.requisiti_pronti_per_la_verifica")}</p>
        <ErrorNote error={inbox.error} />
        {inbox.isPending && <Loading className="text-muted" />}
        {inbox.data && items.length === 0 && <p className="m-0 py-2 text-muted">{tr("governance.requirements.niente_da_verificare")}</p>}
        {items.length > 0 && (
          <ul className="m-0 list-none border-t-2 border-rule p-0">
            {items.map((it) => (
              <li key={it.requirement_id + it.impact_key} className="border-b border-hair">
                <Link
                  to="/e/$key"
                  params={{ key: it.requirement_key }}
                  search={{ scheda: "dettagli" }}
                  className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-4 gap-y-1 py-2.5 text-fg no-underline hover:bg-sel/50 md:grid-cols-[96px_minmax(0,1.4fr)_minmax(0,1fr)_130px] md:items-center"
                >
                  <span className="font-mono text-[12px] font-medium text-accent">{it.requirement_key}</span>
                  <span className="text-right text-[12.5px] text-muted tabular md:order-last">{ago(it.since)}</span>
                  <span className="col-span-2 flex min-w-0 flex-col md:col-span-1">
                    <span className="font-semibold">{it.title}</span>
                    <span className="text-[12.5px] text-muted">
                      {it.category || "—"} · {it.scope === "project" ? tr("governance.requirements.progetto_2") : tr("governance.requirements.applicazione_3")}
                    </span>
                  </span>
                  <span className="col-span-2 flex min-w-0 flex-col md:col-span-1">
                    <span className="truncate">
                      {it.application} <span className="font-mono text-[12px] text-muted">{it.impact_key}</span>
                    </span>
                    <span className="truncate text-[12.5px] text-muted">
                      {it.request_key} {it.request_title}
                      {it.implemented_by ? tr("governance.requirements.realizzato_da_2", { implemented_by: it.implemented_by }) : ""}
                    </span>
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        )}
        {inbox.data?.more && (
          <span className="flex flex-wrap items-center gap-3 text-[12.5px] text-muted">
            <span className="tabular">{tr("governance.requirements.le_prime_celle_dalla_piu", { length: items.length })}</span>
            {items.length < INBOX_MAX && (
              <button className="btn py-1 text-[12.5px]" disabled={inbox.isFetching} onClick={() => setLimit(Math.min(items.length + 500, INBOX_MAX))}>{tr("governance.requirements.mostra_altre")}</button>
            )}
          </span>
        )}
      </div>
    </>
  );
}
