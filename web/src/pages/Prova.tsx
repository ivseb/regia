// Configurazione → Ambiente di prova (replica/usabilita/configurazione.md §5): un tenant di prova
// collegato, con una copia della configurazione, in cui si lavora con le pagine normali; «Promuovi»
// porta nel principale gli oggetti scelti, con «Cosa cambia» per ognuno. Vedi internal/api/prova.go.
import { useState } from "react";
import { useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { api, type Definition } from "../api";
import { PROVA_KINDS, provaApi, type ProvaChange, type ProvaItem, type ProvaKind, type ProvaState } from "../provaApi";
import { diffList, show, type Change } from "../casa/differenze";
import { PageHeader } from "../shell";
import { ErrorNote, Loading, SectionTitle, date } from "../ui";
import { useT } from "../i18n/react";
import type { Key } from "../i18n/core";

/** Cambia organizzazione come fa il menu: si azzerano i dati dell'altra e si ricaricano. */
export function useEnterTenant() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  return useMutation({
    mutationFn: (tenantId: string) => api.switchTenant(tenantId),
    onSuccess: (m) => {
      qc.setQueryData(["me"], m);
      qc.resetQueries({ predicate: (q) => q.queryKey[0] !== "me" });
      navigate({ to: "/configurazione/prova" as never });
    },
  });
}

export function ProvaPage() {
  const t = useT();
  const state = useQuery({ queryKey: ["prova"], queryFn: provaApi.state });
  return (
    <>
      <PageHeader path={t("configurazione.prova.path")} title={t("configurazione.prova.title")} />
      <div className="flex max-w-[860px] flex-col gap-6 px-4 pb-12 md:px-7">
        <p className="m-0 max-w-3xl text-muted">{t("configurazione.prova.intro")}</p>
        {state.isPending && <Loading />}
        {!state.data && <ErrorNote error={state.error} />}
        {state.data?.role === "none" && <Create canManage={state.data.can_manage} />}
        {state.data && state.data.role !== "none" && <Linked s={state.data} />}
        <Scope />
      </div>
    </>
  );
}

function Scope() {
  const t = useT();
  return (
    <section className="flex flex-col gap-2" aria-label={t("configurazione.prova.scope.title")}>
      <SectionTitle>{t("configurazione.prova.scope.title")}</SectionTitle>
      <p className="m-0 text-[13px]"><span className="font-semibold">{t("configurazione.prova.scope.copia")}</span> {t("configurazione.prova.scope.copiaTesto")}</p>
      <p className="m-0 text-[13px]"><span className="font-semibold">{t("configurazione.prova.scope.promuovi")}</span> {t("configurazione.prova.scope.promuoviTesto")}</p>
      <p className="m-0 text-[13px] text-muted"><span className="font-semibold">{t("configurazione.prova.scope.no")}</span> {t("configurazione.prova.scope.noTesto")}</p>
    </section>
  );
}

function Create({ canManage }: { canManage: boolean }) {
  const t = useT();
  const qc = useQueryClient();
  const [sample, setSample] = useState(false);
  const create = useMutation({
    mutationFn: () => provaApi.create(sample),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["prova"] });
      qc.invalidateQueries({ queryKey: ["me"] });
    },
  });
  return (
    <section className="flex flex-col gap-3 border border-rule p-4" aria-label={t("configurazione.prova.none.title")} data-testid="prova-crea">
      <h2 className="m-0 text-[16px] font-bold">{t("configurazione.prova.none.title")}</h2>
      <p className="m-0 text-muted">{t("configurazione.prova.none.text")}</p>
      <label className="flex min-h-11 items-start gap-2.5">
        <input type="checkbox" className="mt-1 size-5" checked={sample} onChange={(e) => setSample(e.target.checked)} />
        <span className="flex flex-col">
          <span>{t("configurazione.prova.none.sample")}</span>
          <span className="text-[12.5px] text-muted">{t("configurazione.prova.none.sampleHelp")}</span>
        </span>
      </label>
      <ErrorNote error={create.error} />
      <div>
        <button className="btn btn-primary" disabled={!canManage || create.isPending} onClick={() => create.mutate()}>
          {create.isPending ? t("configurazione.prova.none.creating") : t("configurazione.prova.none.create")}
        </button>
      </div>
    </section>
  );
}

function Linked({ s }: { s: ProvaState }) {
  const t = useT();
  const qc = useQueryClient();
  const enter = useEnterTenant();
  const [confirm, setConfirm] = useState(false);
  const inProva = s.role === "prova";
  const recreate = useMutation({
    mutationFn: provaApi.recreate,
    onSuccess: () => {
      setConfirm(false);
      qc.invalidateQueries({ queryKey: ["prova"] });
    },
  });
  return (
    <>
      <section className="flex flex-col gap-3 border border-rule p-4" aria-label={t("configurazione.prova.linked.title")} data-testid="prova-collegata">
        <h2 className="m-0 text-[16px] font-bold">{inProva ? t("configurazione.prova.linked.inProva") : t("configurazione.prova.linked.title")}</h2>
        <p className="m-0">
          {inProva ? t("configurazione.prova.linked.principale", { name: s.linked?.name ?? "" }) : t("configurazione.prova.linked.prova", { name: s.linked?.name ?? "" })}
        </p>
        <p className="m-0 text-[12.5px] text-muted">
          {t("configurazione.prova.linked.date", { created: date(s.created_at), refreshed: date(s.refreshed_at) })}
          {s.with_sample ? ` · ${t("configurazione.prova.linked.campione")}` : ""}
        </p>
        <div className="flex flex-wrap gap-2">
          {s.linked && (
            <button className="btn btn-primary" disabled={enter.isPending} onClick={() => enter.mutate(s.linked!.tenant_id)}>
              {inProva ? t("configurazione.prova.linked.tornaPrincipale") : t("configurazione.prova.linked.entra")}
            </button>
          )}
          {s.can_manage && !confirm && (
            <button className="btn" onClick={() => setConfirm(true)}>{t("configurazione.prova.linked.ricrea")}</button>
          )}
        </div>
        {confirm && (
          <div role="alertdialog" aria-label={t("configurazione.prova.linked.ricrea")} className="flex flex-col gap-2 border border-flag p-3" data-testid="prova-ricrea">
            <p className="m-0 text-[13px]">{t("configurazione.prova.linked.ricreaSpiega")}</p>
            <div className="flex flex-wrap gap-2">
              <button className="btn btn-primary" disabled={recreate.isPending} onClick={() => recreate.mutate()}>
                {recreate.isPending ? t("configurazione.prova.linked.ricreo") : t("configurazione.prova.linked.ricreaConferma")}
              </button>
              <button className="btn" onClick={() => setConfirm(false)}>{t("configurazione.common.annulla")}</button>
            </div>
          </div>
        )}
        <ErrorNote error={enter.error ?? recreate.error} />
      </section>
      {s.can_manage && <Promote inProva={inProva} />}
      {s.can_manage && <History />}
    </>
  );
}

// ---------------------------------------------------------------------
// Promuovi

function Promote({ inProva }: { inProva: boolean }) {
  const t = useT();
  const qc = useQueryClient();
  const diff = useQuery({ queryKey: ["prova", "diff"], queryFn: provaApi.diff });
  const [off, setOff] = useState<Set<string>>(new Set());
  const [touched, setTouched] = useState(false);
  const [done, setDone] = useState<number | null>(null);
  const items = diff.data?.items ?? [];
  const id = (i: ProvaItem) => `${i.kind}/${i.key}`;
  // Preselezionati gli oggetti senza conflitto; chi ha toccato le caselle decide da sé.
  const checked = (i: ProvaItem) => (touched ? !off.has(id(i)) : !i.conflict);
  const picked = items.filter(checked);
  const promote = useMutation({
    mutationFn: () => provaApi.promote(picked.map((i) => ({ kind: i.kind, key: i.key }))),
    onSuccess: (r) => {
      setDone(r.promoted.length);
      setTouched(false);
      setOff(new Set());
      qc.invalidateQueries({ queryKey: ["prova"] });
      // Le pagine di configurazione del principale mostrano subito il nuovo.
      qc.invalidateQueries({ predicate: (q) => q.queryKey[0] !== "me" && q.queryKey[0] !== "prova" });
    },
  });
  const toggle = (i: ProvaItem) => {
    const next = new Set(touched ? off : items.filter((x) => !checked(x)).map(id));
    if (next.has(id(i))) next.delete(id(i));
    else next.add(id(i));
    setTouched(true);
    setOff(next);
  };
  return (
    <section className="flex flex-col gap-3" aria-label={t("configurazione.prova.promuovi.title")} data-testid="prova-promuovi">
      <SectionTitle>{t("configurazione.prova.promuovi.title")}</SectionTitle>
      <p className="m-0 text-[13px] text-muted">{inProva ? t("configurazione.prova.promuovi.dallaProva") : t("configurazione.prova.promuovi.dalPrincipale")}</p>
      {diff.isPending && <Loading />}
      <ErrorNote error={diff.error} />
      {diff.data && items.length === 0 && <p className="m-0 text-muted" role="status">{t("configurazione.prova.promuovi.nessuna")}</p>}
      {done !== null && (
        <p role="status" className="m-0 font-semibold text-ok" data-testid="prova-promosso">{t("configurazione.prova.promuovi.fatto", { count: done })}</p>
      )}
      {PROVA_KINDS.map((kind) => {
        const rows = items.filter((i) => i.kind === kind);
        if (rows.length === 0) return null;
        return (
          <div key={kind} className="flex flex-col">
            <span className="label">{t(`configurazione.prova.kind.${kind}` as Key)}</span>
            {rows.map((i) => (
              <ItemRow key={id(i)} item={i} on={checked(i)} onToggle={() => toggle(i)} />
            ))}
          </div>
        );
      })}
      {items.length > 0 && (
        <div className="flex flex-wrap items-center gap-3">
          <button className="btn btn-primary" disabled={picked.length === 0 || promote.isPending} onClick={() => promote.mutate()}>
            {promote.isPending ? t("configurazione.prova.promuovi.promuovo") : t("configurazione.prova.promuovi.bottone", { count: picked.length })}
          </button>
          <span className="text-[12.5px] text-muted">{t("configurazione.prova.promuovi.nota")}</span>
        </div>
      )}
      <ErrorNote error={promote.error} />
    </section>
  );
}

const SHORT = 160;
const short = (v: unknown) => {
  const s = show(v);
  return s.length > SHORT ? `${s.slice(0, SHORT)}…` : s;
};

function ItemRow({ item, on, onToggle }: { item: ProvaItem; on: boolean; onToggle: () => void }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const defChanges = item.before_def || item.after_def ? diffDefinitions(item.before_def, item.after_def, t) : [];
  return (
    <div className="flex flex-col border-b border-hair py-1" data-testid={`prova-voce-${item.kind}-${item.key}`} data-change={item.change}>
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <label className="flex min-h-11 min-w-0 flex-1 basis-56 items-center gap-2.5">
          <input type="checkbox" className="size-5 shrink-0" checked={on} onChange={onToggle} aria-label={t("configurazione.prova.promuovi.scegli", { name: item.name })} />
          <span className="min-w-0 break-words font-semibold">{item.name}</span>
        </label>
        <span className="font-mono text-[11.5px] text-muted">{t(`configurazione.prova.change.${item.change}` as Key)}</span>
        <button className="btn btn-quiet touch-44 text-[13px]" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
          {t("configurazione.prova.cosaCambia")}
        </button>
      </div>
      {item.conflict && <p role="note" className="m-0 pb-1 pl-8 text-[12.5px] text-danger">{t("configurazione.prova.conflitto")}</p>}
      {open && (
        <ul className="m-0 flex list-none flex-col gap-2 p-0 pb-2 pl-0 md:pl-8" data-testid="prova-cosa-cambia">
          {item.changes.length === 0 && defChanges.length === 0 && <li className="text-[13px] text-muted">{t("configurazione.prova.nessunDettaglio")}</li>}
          {item.changes.map((c, i) => (
            <ChangeLine key={i} c={c} />
          ))}
          {defChanges.map((c, i) => (
            <li key={`d${i}`} className="flex flex-col gap-0.5 border-b border-hair pb-2 text-[13px]">
              <span className="font-semibold">{c.label}</span>
              <span className="break-words text-muted">{t("configurazione.prova.nelPrincipale")}: {c.before}</span>
              <span className="break-words">{t("configurazione.prova.nellaProva")}: {c.after}</span>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

const ATTRS = new Set([
  "name", "description", "data_type", "multi", "settings", "aliases", "help", "default", "archived", "options", "plural_name", "icon", "color", "portal",
  "labels", "fields", "type", "priority", "applies_when", "mode", "rules", "override_enabled", "override_hours", "kind", "approval_limit_cents", "quorum",
  "inline_decision", "definition", "required", "ordinal", "private", "show_in_list", "condition", "section", "visible_when", "required_from", "category",
]);

function ChangeLine({ c }: { c: ProvaChange }) {
  const t = useT();
  const lab = (a: string) => (ATTRS.has(a) ? t(`configurazione.prova.attr.${a}` as Key) : a);
  let label = lab(c.attr);
  if (c.sub) {
    const sub = c.attr === "options" ? t("configurazione.prova.sub.option", { name: c.sub }) : t("configurazione.prova.sub.field", { name: c.sub });
    label = c.detail && c.detail !== "+" && c.detail !== "-" ? `${sub} · ${lab(c.detail)}` : sub;
  }
  if (c.detail === "+" || c.detail === "-") {
    return (
      <li className="border-b border-hair pb-2 text-[13px]">
        <span className="font-semibold">{label}</span> — {c.detail === "+" ? t("configurazione.prova.sub.added") : t("configurazione.prova.sub.removed")}
      </li>
    );
  }
  return (
    <li className="flex flex-col gap-0.5 border-b border-hair pb-2 text-[13px]">
      <span className="font-semibold">{label}</span>
      <span className="break-words text-muted">{t("configurazione.prova.nelPrincipale")}: {short(c.before)}</span>
      <span className="break-words">{t("configurazione.prova.nellaProva")}: {short(c.after)}</span>
    </li>
  );
}

/** Differenze di un processo, in parole: fasi, passaggi, gate e regole (come «Cosa è applicato»). */
function diffDefinitions(before: Definition | undefined, after: Definition | undefined, t: ReturnType<typeof useT>): Change[] {
  const b = before ?? ({ phases: [], transitions: [] } as unknown as Definition);
  const a = after ?? ({ phases: [], transitions: [] } as unknown as Definition);
  const phase = (x: Definition["phases"][number]) => `${x.category}${x.description ? ` · ${x.description}` : ""}${x.off_path ? ` · ${t("configurazione.avvio.fuoriPercorso")}` : ""}`;
  const step = (x: Definition["transitions"][number]) =>
    [x.label, (x.roles ?? []).join(", "), (x.gates ?? []).length ? `${t("configurazione.avvio.gate")}: ${(x.gates ?? []).join(", ")}` : "", x.when ?? ""].filter(Boolean).join(" · ") || "—";
  return [
    ...diffList(t("configurazione.avvio.fase"), b.phases ?? [], a.phases ?? [], (x) => x.name, (x) => x.name, phase),
    ...diffList(t("configurazione.avvio.passaggio"), b.transitions ?? [], a.transitions ?? [], (x) => `${(x.from ?? []).join("+")}>${x.to}`, (x) => `${(x.from ?? []).join(", ")} → ${x.to}`, step),
    ...diffList(t("configurazione.avvio.gate"), b.gates ?? [], a.gates ?? [], (x) => x.key, (x) => x.name || x.key, (x) => x.condition || x.check || x.message || "—"),
    ...diffList(t("configurazione.avvio.regola"), b.rules ?? [], a.rules ?? [], (x) => x.key, (x) => x.name || x.key, (x) => `${x.on}${x.when ? ` · ${x.when}` : ""} · ${(x.actions ?? []).map((k) => k.kind).join(", ")}`),
  ];
}

// ---------------------------------------------------------------------
// Storico

function History() {
  const t = useT();
  const h = useQuery({ queryKey: ["prova", "storico"], queryFn: provaApi.history });
  const rows = h.data?.items ?? [];
  return (
    <section className="flex flex-col" aria-label={t("configurazione.prova.storico.title")}>
      <SectionTitle>{t("configurazione.prova.storico.title")}</SectionTitle>
      {h.isPending && <Loading />}
      {h.data && rows.length === 0 && <p className="m-0 text-muted">{t("configurazione.prova.storico.vuoto")}</p>}
      {rows.map((r) => (
        <div key={r.id} className="flex flex-col gap-0.5 border-b border-hair py-2" data-testid="prova-storico-riga">
          <span className="text-[13px] font-semibold">{t("configurazione.prova.storico.riga", { date: date(r.promoted_at), who: r.promoted_by || "—", count: r.items.length })}</span>
          <span className="break-words text-[12.5px] text-muted">{r.items.map((i) => `${t(`configurazione.prova.kind.${i.kind}` as Key)}: ${i.name}`).join(" · ")}</span>
        </div>
      ))}
    </section>
  );
}

export type { ProvaKind };
