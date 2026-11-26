// Configurazione → Tipi: nome, plurale, descrizione, icona e colore di ogni tipo di elemento,
// apertura dal portale, nomi in inglese, tipi nuovi da un modello e archiviazione.
// Rinominare cambia solo i testi: chiave, dati, campi e processi restano com'erano.
// Serve tenant.admin.
import { Select } from "../select";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link, useNavigate } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError } from "../api";
import { typesConfigRoute } from "../router";
import { PageHeader } from "../shell";
import { MODULE_LABELS, TYPE_COLORS, TYPE_ICONS, TypeChip, TypeMark, typesApi, type TypeConfig, type TypeUpdate } from "../typesApi";
import { ErrorNote, Loading, SectionTitle, ago } from "../ui";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import { tr } from "../i18n/core";
import { BozzaBar, useBozza } from "../casa/Bozza";
import { diffFields } from "../casa/differenze";

const NEW = "nuovo";

/** Ordine dei gruppi nell'elenco; i moduli non elencati vanno in fondo. */
const MODULE_ORDER = ["strategy", "work", "knowledge", "architecture", "grc", "devsecops", "core"];

const KEY_FROM = (name: string) =>
  name
    .normalize("NFD")
    .replace(/[̀-ͯ]/g, "")
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "")
    .replace(/^(\d)/, "t_$1")
    .slice(0, 63);

export function TypesConfig() {
  const t = useT();
  const { tipo } = typesConfigRoute.useSearch();
  const navigate = useNavigate({ from: typesConfigRoute.fullPath });
  const list = useQuery({ queryKey: ["types-admin"], queryFn: typesApi.list });
  const [archived, setArchived] = useState(false);
  const go = (t?: string) => navigate({ search: { tipo: t } });
  // Sul telefono la scheda sta sotto l'elenco: scegliendo un tipo la si porta in vista.
  const editor = useRef<HTMLElement>(null);
  useEffect(() => {
    if (tipo && window.innerWidth < 768) editor.current?.scrollIntoView({ block: "start" });
  }, [tipo]);

  if (list.error instanceof ApiError && list.error.status === 403) {
    return (
      <>
        <PageHeader path={t("configurazione.path.types")} title={t("configurazione.common.tipiDiElemento")} />
        <p className="px-4 text-muted md:px-7">{t("configurazione.common.serveIlPermessoAmministrare")}</p>
      </>
    );
  }
  const items = list.data?.items ?? [];
  const shown = items.filter((t) => archived || !t.archived || t.key === tipo);
  const modules = [...new Set(shown.map((t) => t.module))].sort((a, b) => rank(a) - rank(b));
  const selected = tipo && tipo !== NEW ? items.find((t) => t.key === tipo) : undefined;

  return (
    <>
      <PageHeader
        path={t("configurazione.path.types")}
        title={t("configurazione.common.tipiDiElemento")}
        actions={
          <button className="btn btn-primary" onClick={() => go(NEW)}>
            {t("configurazione.types.nuovoTipo")}
          </button>
        }
      />
      <div className="flex min-h-0 flex-1 flex-wrap">
        <div className="min-w-0 flex-[999_1_520px] px-4 pb-8 md:px-7">
          <p className="m-0 mb-4 max-w-[640px] text-muted">
            {t("configurazione.types.ogniTipoHaUn")}
          </p>
          <label className="mb-3 flex items-center gap-2">
            <input type="checkbox" className="size-4 accent-[var(--accent)]" checked={archived} onChange={(e) => setArchived(e.target.checked)} />
            <span>{t("configurazione.common.mostraGliArchiviati")}</span>
          </label>
          {list.isPending && <Loading />}
          <ErrorNote error={list.error} />
          {modules.map((m) => (
            <section key={m} aria-label={MODULE_LABELS[m] ?? m} className="mb-5 flex flex-col">
              <SectionTitle>{MODULE_LABELS[m] ?? m}</SectionTitle>
              <ul className="m-0 list-none p-0">
                {shown
                  .filter((t) => t.module === m)
                  .map((t) => (
                    <TypeRow key={t.key} t={t} on={tipo === t.key} onSelect={() => go(t.key)} />
                  ))}
              </ul>
            </section>
          ))}
        </div>
        {tipo && list.data && (
          <aside ref={editor} aria-label={t("configurazione.types.schedaDelTipo")} className="flex min-w-0 flex-[1_1_420px] flex-col gap-5 border-l border-rule px-4 py-5 md:px-6">
            {tipo === NEW ? (
              <NewType items={items} creatable={list.data.creatable_modules} onCreated={(t) => go(t.key)} onClose={() => go()} />
            ) : selected ? (
              <TypeForm key={selected.key} t={selected} onClose={() => go()} />
            ) : (
              <p className="text-muted">{t("configurazione.types.typeNotFound", { name: tipo })}</p>
            )}
          </aside>
        )}
      </div>
    </>
  );
}

const rank = (m: string) => {
  const i = MODULE_ORDER.indexOf(m);
  return i < 0 ? 99 : i;
};

function TypeRow({ t, on, onSelect }: { t: TypeConfig; on: boolean; onSelect: () => void }) {
  return (
    <li className={`border-b border-hair ${on ? "bg-sel" : "hover:bg-sel/50"}`}>
      <button type="button" onClick={onSelect} aria-current={on ? "true" : undefined} className="flex w-full min-w-0 items-center gap-3 border-0 bg-transparent px-1 py-2.5 text-left text-fg">
        <TypeMark icon={t.icon} color={t.color} name={t.name} size={26} />
        <span className="flex min-w-0 flex-1 flex-col">
          <span className={`truncate font-semibold ${t.archived ? "text-muted" : ""}`}>{t.name}</span>
          <span className="truncate font-mono text-[11.5px] text-muted">{t.key}</span>
        </span>
        <span className="flex shrink-0 flex-wrap justify-end gap-x-3 gap-y-0.5 text-[12px] text-muted tabular">
          <span>{tr("configurazione.types.elementCount", { count: t.elements })}</span>
          <span className="hidden sm:inline">{tr("configurazione.types.fieldCount", { count: t.fields })}</span>
        </span>
        {t.is_system && <span className="label shrink-0 text-muted">{tr("configurazione.common.diSistema")}</span>}
        {t.archived && <span className="label shrink-0 text-flag">{tr("configurazione.common.archiviato")}</span>}
      </button>
    </li>
  );
}

function Labeled({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <label className="flex min-w-0 flex-col gap-1.5">
      <span className="label">{label}</span>
      {children}
      {hint && <span className="text-[12px] text-muted">{hint}</span>}
    </label>
  );
}

/** Scelta dell'icona e del colore, con la palette del tema. */
function Look({ icon, color, name, onIcon, onColor }: { icon: string; color: string; name: string; onIcon: (i: string) => void; onColor: (c: string) => void }) {
  const t = useT();
  return (
    <>
      <fieldset className="m-0 flex min-w-0 flex-col gap-1.5 border-0 p-0">
        <legend className="label mb-1.5 p-0">{t("configurazione.types.icona")}</legend>
        <div className="grid grid-cols-4 gap-1.5 sm:grid-cols-8">
          <button type="button" aria-pressed={icon === ""} aria-label={t("configurazione.types.inizialeDelNome")} title={t("configurazione.types.inizialeDelNome")} onClick={() => onIcon("")}
            className={`btn flex items-center justify-center p-1 ${icon === "" ? "border-accent bg-sel" : ""}`}>
            <TypeMark color={color} name={name} size={24} />
          </button>
          {Object.entries(TYPE_ICONS).map(([k, g]) => (
            <button key={k} type="button" aria-pressed={icon === k} aria-label={g.label} title={g.label} onClick={() => onIcon(k)}
              className={`btn flex items-center justify-center p-1 ${icon === k ? "border-accent bg-sel" : ""}`}>
              <TypeMark icon={k} color={color} name={name} size={24} />
            </button>
          ))}
        </div>
      </fieldset>
      <fieldset className="m-0 flex min-w-0 flex-col gap-1.5 border-0 p-0">
        <legend className="label mb-1.5 p-0">{t("configurazione.common.colore")}</legend>
        <div className="flex flex-wrap gap-1.5">
          {TYPE_COLORS.map((c) => (
            <button key={c.key} type="button" aria-pressed={color === c.key} onClick={() => onColor(c.key)}
              className={`btn flex items-center gap-1.5 py-1 ${color === c.key ? "border-accent bg-sel" : ""}`}>
              <span className={`size-3 ${c.bg}`} aria-hidden="true" />
              {c.label}
            </button>
          ))}
        </div>
      </fieldset>
    </>
  );
}

/** Come si vedrà il tipo: riga di una lista e chip. */
function Preview({ icon, color, name, plural, keyText }: { icon: string; color: string; name: string; plural: string; keyText: string }) {
  const t = useT();
  const n = name.trim() || t("configurazione.types.typeNameFallback");
  return (
    <section aria-label={t("configurazione.common.anteprima")} className="flex flex-col gap-2">
      <SectionTitle>{t("configurazione.common.anteprima")}</SectionTitle>
      <div className="flex flex-col gap-3 border border-hair p-3">
        <div className="flex min-w-0 items-center gap-3 border-b border-hair pb-2">
          <TypeMark icon={icon} color={color} name={n} size={22} />
          <span className="font-mono text-[12px] text-accent">{keyText}-12</span>
          <span className="min-w-0 flex-1 truncate">{t("configurazione.types.titoloDiUnElemento")}</span>
          <span className="text-[12px] text-muted">{n}</span>
        </div>
        <div className="flex flex-wrap items-center gap-3 text-[12.5px] text-muted">
          <span>{t("configurazione.types.chip")}</span>
          <TypeChip icon={icon} color={color} name={n} />
          <span>{t("configurazione.types.elenco")}</span>
          <span className="font-semibold text-fg">{plural.trim() || n}</span>
        </div>
      </div>
    </section>
  );
}

function TypeForm({ t, onClose }: { t: TypeConfig; onClose: () => void }) {
  const qc = useQueryClient();
  const base = {
    name: t.name,
    plural: t.plural_name,
    description: t.description,
    icon: t.icon,
    color: t.color,
    portal: t.portal_enabled,
    enName: t.labels.en?.name ?? "",
    enPlural: t.labels.en?.plural ?? "",
  };
  // Bozza del tipo: si vede cosa cambia e si pubblica; si ricorda finché non si pubblica o si scarta.
  const bozza = useBozza(base, `tipo.${t.key}`);
  const d = bozza.draft;
  function set<K extends keyof typeof base>(k: K, v: (typeof base)[K]) {
    bozza.patch({ [k]: v } as Partial<typeof base>);
  }
  const [saved, setSaved] = useState(false);
  const done = () => {
    qc.invalidateQueries({ queryKey: ["types-admin"] });
    qc.invalidateQueries({ queryKey: ["type-names"] });
    qc.invalidateQueries({ queryKey: ["type"] });
    qc.invalidateQueries({ queryKey: ["fields-catalog"] });
  };
  const save = useMutation({
    mutationFn: () => {
      const body: TypeUpdate = {
        name: d.name,
        plural_name: d.plural,
        description: d.description,
        icon: d.icon,
        color: d.color,
        labels: d.enName.trim() || d.enPlural.trim() ? { ...t.labels, en: { name: d.enName, plural: d.enPlural } } : omitEn(t.labels),
      };
      if (d.portal !== t.portal_enabled) body.portal_enabled = d.portal;
      return typesApi.update(t.key, body);
    },
    onSuccess: () => {
      setSaved(true);
      done();
    },
  });
  const archive = useMutation({ mutationFn: (v: boolean) => typesApi.update(t.key, { archived: v }), onSuccess: done });
  const dirty = bozza.dirty;
  const changes = diffFields([
    { label: tr("configurazione.common.nome"), before: base.name, after: d.name },
    { label: tr("configurazione.types.nomeAlPlurale"), before: base.plural, after: d.plural },
    { label: tr("configurazione.common.descrizione"), before: base.description, after: d.description },
    { label: tr("configurazione.types.icona"), before: base.icon, after: d.icon, format: (v) => (v ? (TYPE_ICONS[String(v)]?.label ?? String(v)) : tr("configurazione.types.inizialeDelNome")) },
    { label: tr("configurazione.common.colore"), before: base.color, after: d.color, format: (v) => TYPE_COLORS.find((c) => c.key === v)?.label ?? String(v || "—") },
    { label: tr("configurazione.types.apribileDalPortale"), before: base.portal, after: d.portal },
    { label: tr("configurazione.types.nameEnglish"), before: base.enName, after: d.enName },
    { label: tr("configurazione.types.pluralEnglish"), before: base.enPlural, after: d.enPlural },
  ]);

  return (
    <>
      <div className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 flex-col gap-1">
          <span className="font-mono text-[12px] text-accent">{t.key}</span>
          <h2 className="m-0 text-[20px] leading-[1.15] font-bold">{t.name}</h2>
          <span className="text-[12.5px] text-muted">
            {MODULE_LABELS[t.module] ?? t.module} · {tr("configurazione.types.elementCount", { count: t.elements })} · {tr("configurazione.types.updatedAgo", { when: ago(t.updated_at) })}
            {t.is_system ? tr("configurazione.types.diSistema") : ""}
            {t.archived ? tr("configurazione.types.archiviato") : ""}
          </span>
        </div>
        <button className="btn py-1" onClick={onClose}>{tr("configurazione.common.chiudi")}</button>
      </div>
      <form
        className="flex flex-col gap-4"
        onSubmit={(e) => {
          e.preventDefault();
        }}
      >
        <SectionTitle>{tr("configurazione.common.nome")}</SectionTitle>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Labeled label={tr("configurazione.common.nome")}>
            <input className="field" required maxLength={80} value={d.name} onChange={(e) => set("name", e.target.value)} />
          </Labeled>
          <Labeled label={tr("configurazione.types.nomeAlPlurale")} hint={tr("configurazione.types.ilNomeAlSingolare")}>
            <input className="field" maxLength={80} value={d.plural} onChange={(e) => set("plural", e.target.value)} />
          </Labeled>
        </div>
        <Labeled label={tr("configurazione.common.descrizione")} hint={tr("configurazione.types.aiutaChiSceglieIl")}>
          <textarea className="field min-h-16" maxLength={500} value={d.description} onChange={(e) => set("description", e.target.value)} />
        </Labeled>
        <SectionTitle>{tr("configurazione.common.aspetto")}</SectionTitle>
        <Look icon={d.icon} color={d.color} name={d.name} onIcon={(v) => set("icon", v)} onColor={(v) => set("color", v)} />
        <Preview icon={d.icon} color={d.color} name={d.name} plural={d.plural} keyText={t.key.slice(0, 3).toUpperCase()} />
        <SectionTitle>{tr("configurazione.types.portale")}</SectionTitle>
        <label className="flex items-start gap-2">
          <input type="checkbox" className="mt-0.5 size-4 accent-[var(--accent)]" checked={d.portal} onChange={(e) => set("portal", e.target.checked)} />
          <span className="flex flex-col">
            <span>{tr("configurazione.types.apribileDalPortale")}</span>
            <span className="text-[12px] text-muted">{tr("configurazione.types.iRichiedentiSenzaAccesso")}</span>
          </span>
        </label>
        <SectionTitle>{tr("configurazione.types.traduzioni")}</SectionTitle>
        <p className="m-0 text-[12.5px] text-muted">{tr("configurazione.types.nomiInInglesePer")}</p>
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <Labeled label={tr("configurazione.types.nameEnglish")}>
            <input className="field" maxLength={80} value={d.enName} onChange={(e) => set("enName", e.target.value)} />
          </Labeled>
          <Labeled label={tr("configurazione.types.pluralEnglish")}>
            <input className="field" maxLength={80} value={d.enPlural} onChange={(e) => set("enPlural", e.target.value)} />
          </Labeled>
        </div>
        <BozzaBar
          changes={changes}
          pending={save.isPending}
          error={save.error}
          published={saved && !dirty}
          blocked={!d.name.trim()}
          onPublish={() => {
            setSaved(false);
            save.mutate();
          }}
          onDiscard={() => {
            bozza.discard();
            save.reset();
          }}
          impact={tr("casa.types.impact", { count: t.elements })}
        />
      </form>
      <section className="flex flex-col gap-2">
        <SectionTitle>{tr("configurazione.types.collegamenti")}</SectionTitle>
        <div className="flex flex-col gap-1.5">
          <Link to="/configurazione/campi" search={{ tipo: t.key }} className="w-fit">
            {tr("configurazione.types.fieldsOfType", { count: t.fields })}
          </Link>
          {t.process_key ? (
            <span>
              <Trans k="configurazione.types.appliedProcess" slots={{ link: <Link to="/processi/$key" params={{ key: t.process_key }}>{t.process_name}</Link> }} />
            </span>
          ) : (
            <span className="text-muted">
              <Trans k="configurazione.types.noAppliedProcess" slots={{ link: <Link to="/processi">{tr("configurazione.types.vaiAiProcessi")}</Link> }} />
            </span>
          )}
        </div>
      </section>
      {(() => {
        // Un tipo di sistema si archivia solo se non ha elementi (epic e sprint, per esempio, dove non servono).
        const blocked = t.is_system && !t.archived && t.elements > 0;
        return (
          <section className="flex flex-col gap-2">
            <SectionTitle>{t.archived ? tr("configurazione.types.tipoArchiviato") : tr("configurazione.common.archivia")}</SectionTitle>
            <p className="m-0 text-[12.5px] text-muted">
              {t.archived
                ? tr("configurazione.types.ilTipoNonCompare")
                : tr("configurazione.types.ilTipoSparisceDai")}
            </p>
            {t.is_system && <p className="m-0 text-[12px] text-muted">{blocked ? tr("configurazione.tipiLavoro.sistemaConElementi", { count: t.elements }) : tr("configurazione.tipiLavoro.sistema")}</p>}
            <ErrorNote error={archive.error} />
            <div>
              <button className={`btn ${t.archived ? "" : "btn-danger"}`} disabled={archive.isPending || blocked} onClick={() => archive.mutate(!t.archived)}>
                {t.archived ? tr("configurazione.types.riattivaIlTipo") : tr("configurazione.types.archiviaIlTipo")}
              </button>
            </div>
          </section>
        );
      })()}
    </>
  );
}

function omitEn(l: TypeConfig["labels"]): TypeConfig["labels"] {
  const { en: _en, ...rest } = l;
  void _en;
  return rest;
}

/** Procedura in due passi: da quale tipo partire, poi nome e chiave. */
function NewType({ items, creatable, onCreated, onClose }: { items: TypeConfig[]; creatable: string[]; onCreated: (t: TypeConfig) => void; onClose: () => void }) {
  const t = useT();
  const qc = useQueryClient();
  const [step, setStep] = useState<1 | 2>(1);
  const [from, setFrom] = useState<string>("");
  const [d, setD] = useState({ name: "", plural: "", key: "", icon: "", color: "accent", module: "work" });
  const [keyTouched, setKeyTouched] = useState(false);
  const model = items.find((t) => t.key === from);
  const models = useMemo(() => items.filter((t) => !t.archived), [items]);
  function set<K extends keyof typeof d>(k: K, v: (typeof d)[K]) {
    setD((x) => ({ ...x, [k]: v }));
  }
  const choose = (key: string) => {
    setFrom(key);
    const m = items.find((t) => t.key === key);
    setD((x) => ({ ...x, icon: m?.icon ?? "", color: m?.color || "accent", module: m && creatable.includes(m.module) ? m.module : x.module }));
  };
  const create = useMutation({
    mutationFn: () => typesApi.create({ key: d.key, name: d.name, plural_name: d.plural, module: d.module, icon: d.icon, color: d.color, from: from || undefined }),
    onSuccess: (t) => {
      qc.invalidateQueries({ queryKey: ["types-admin"] });
      qc.invalidateQueries({ queryKey: ["type-names"] });
      qc.invalidateQueries({ queryKey: ["fields-catalog"] });
      onCreated(t);
    },
  });
  return (
    <>
      <div className="flex items-start justify-between gap-3">
        <div className="flex flex-col gap-1">
          <span className="font-mono text-[12px] text-accent">NUOVO TIPO · PASSO {step} DI 2</span>
          <h2 className="m-0 text-[20px] leading-[1.15] font-bold">{step === 1 ? t("configurazione.types.daDoveParti") : t("configurazione.types.comeSiChiama")}</h2>
        </div>
        <button className="btn py-1" onClick={onClose}>{t("configurazione.common.chiudi")}</button>
      </div>
      {step === 1 ? (
        <div className="flex flex-col gap-4">
          <p className="m-0 text-[12.5px] text-muted">
            {t("configurazione.types.ilTipoNuovoEredita")}
          </p>
          <fieldset className="m-0 flex flex-col border-0 p-0">
            <legend className="sr-only">{t("configurazione.types.tipoDiPartenza")}</legend>
            <label className="flex items-start gap-3 border-b border-hair py-2.5">
              <input type="radio" name="modello" className="mt-1" checked={from === ""} onChange={() => choose("")} />
              <span className="flex flex-col">
                <span className="font-semibold">{t("configurazione.common.daZero")}</span>
                <span className="text-[12px] text-muted">{t("configurazione.types.nessunaFaseNeCampo")}</span>
              </span>
            </label>
            {models.map((t) => (
              <label key={t.key} className="flex items-start gap-3 border-b border-hair py-2.5">
                <input type="radio" name="modello" className="mt-1" checked={from === t.key} onChange={() => choose(t.key)} />
                <TypeMark icon={t.icon} color={t.color} name={t.name} size={22} />
                <span className="flex min-w-0 flex-col">
                  <span className="font-semibold">{t.name}</span>
                  <span className="text-[12px] text-muted">{MODULE_LABELS[t.module] ?? t.module} · {tr("configurazione.types.fieldCount", { count: t.fields })}</span>
                </span>
              </label>
            ))}
          </fieldset>
          <div className="flex gap-2">
            <button className="btn btn-primary" onClick={() => setStep(2)}>{t("configurazione.types.continua")}</button>
            <button className="btn" onClick={onClose}>{t("configurazione.common.annulla")}</button>
          </div>
        </div>
      ) : (
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            create.mutate();
          }}
        >
          <p className="m-0 text-[12.5px] text-muted">{model ? t("configurazione.types.startFrom", { name: model.name }) : t("configurazione.types.partiDaZero")}</p>
          <Labeled label={t("configurazione.common.nome")}>
            <input
              className="field"
              required
              maxLength={80}
              autoFocus
              value={d.name}
              onChange={(e) => {
                set("name", e.target.value);
                if (!keyTouched) set("key", KEY_FROM(e.target.value));
              }}
            />
          </Labeled>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <Labeled label={t("configurazione.types.nomeAlPlurale")}>
              <input className="field" maxLength={80} value={d.plural} onChange={(e) => set("plural", e.target.value)} />
            </Labeled>
            <Labeled label={t("configurazione.common.chiave")} hint={t("configurazione.types.perQueryEApi")}>
              <input
                className="field font-mono"
                required
                pattern="[a-z][a-z0-9_]{0,62}"
                value={d.key}
                onChange={(e) => {
                  setKeyTouched(true);
                  set("key", e.target.value);
                }}
              />
            </Labeled>
          </div>
          {!model || !creatable.includes(model.module) ? (
            <Labeled label={t("configurazione.types.modulo")} hint={t("configurazione.types.doveCompareIlTipo")}>
              <Select className="field" value={d.module} onChange={(e) => set("module", e.target.value)}>
                {creatable.map((m) => (
                  <option key={m} value={m}>{MODULE_LABELS[m] ?? m}</option>
                ))}
              </Select>
            </Labeled>
          ) : (
            <p className="m-0 text-[12.5px] text-muted">{t("configurazione.types.moduleSame", { module: MODULE_LABELS[model.module] ?? model.module })}</p>
          )}
          <Look icon={d.icon} color={d.color} name={d.name} onIcon={(v) => set("icon", v)} onColor={(v) => set("color", v)} />
          <Preview icon={d.icon} color={d.color} name={d.name} plural={d.plural} keyText={(d.key || "tip").slice(0, 3).toUpperCase()} />
          <ErrorNote error={create.error} />
          <div className="flex gap-2">
            <button className="btn btn-primary" disabled={create.isPending}>{create.isPending ? t("configurazione.types.creazione") : t("configurazione.types.creaIlTipo")}</button>
            <button type="button" className="btn" onClick={() => setStep(1)}>{t("configurazione.types.indietro")}</button>
          </div>
        </form>
      )}
    </>
  );
}
