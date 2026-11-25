import { Select } from "../select";
import { useEffect, useState, type ReactNode } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { State } from "../api";
import {
  FONTS,
  THEMES,
  appearanceApi,
  applyAppearance,
  fontName,
  loadFont,
  themeName,
  type AppearanceChoice,
  type FontKey,
  type ThemeKey,
} from "../appearance";
import { ErrorNote, SectionTitle, StageGate } from "../ui";
import { useT } from "../i18n/react";
import { Trans } from "../i18n/Trans";
import { fmtDate } from "../i18n/format";
import type { Key } from "../i18n/core";

// Profilo, sezione "Aspetto": tema e font personali con anteprima dal vivo, e default
// dell'organizzazione per chi ha tenant.admin (replica/design.md, "Temi e font").

const SAMPLE_STATE_KEYS: Key[] = [
  "profile.sample.state.draft",
  "profile.sample.state.triage",
  "profile.sample.state.evaluation",
  "profile.sample.state.approval",
  "profile.sample.state.planned",
  "profile.sample.state.inProgress",
  "profile.sample.state.release",
  "profile.sample.state.closed",
];

// phase: indice in SAMPLE_STATE_KEYS.
const SAMPLE_ROWS: { id: string; title: Key; phase: number; amount: string; dora: boolean }[] = [
  { id: "DEM-142", title: "profile.sample.row1", phase: 3, amount: "180 k€", dora: false },
  { id: "DEM-139", title: "profile.sample.row2", phase: 2, amount: "64 k€", dora: true },
  { id: "DEM-131", title: "profile.sample.row3", phase: 5, amount: "1.240 k€", dora: false },
];

/** Campione di pagina con il tema e il font indicati, indipendente dal resto dell'interfaccia. */
function Sample({ theme, font, mode }: { theme: ThemeKey; font: FontKey; mode: "light" | "dark" }) {
  const t = useT();
  const states: State[] = SAMPLE_STATE_KEYS.map((k, i) => ({ id: String(i), name: t(k), category: i === 0 ? "todo" : i === 7 ? "done" : "in_progress" }));
  return (
    <div
      data-skin={theme}
      data-font={font}
      data-theme={mode}
      role="img"
      aria-label={t("profile.look.previewLabel", { theme: themeName(theme), mode: mode === "dark" ? t("profile.look.dark") : t("profile.look.light"), font: fontName(font) })}
      className="min-w-0 border border-hair bg-bg font-sans text-fg"
    >
      <div inert className="flex flex-col gap-3 p-4">
        <div className="flex items-center justify-between gap-2">
          <span className="font-mono text-[11px] text-muted">{t("profile.sample.section")}</span>
          <span className="label text-muted">{mode === "dark" ? t("profile.look.dark") : t("profile.look.light")}</span>
        </div>
        <h2 className="m-0 text-[24px] leading-[1.05] font-bold tracking-[-0.02em]">{t("profile.sample.heading")}</h2>
        <input className="field font-mono text-[12px]" readOnly value="fase:Valutazione,Approvazione costo>50k" tabIndex={-1} />
        <div className="flex flex-col">
          <div className="label grid grid-cols-[64px_minmax(0,1fr)_auto] gap-3 border-b-2 border-rule pb-1.5">
            <span>{t("profile.sample.id")}</span>
            <span>{t("profile.sample.titleAndPhase")}</span>
            <span className="text-right">{t("profile.sample.cost")}</span>
          </div>
          {SAMPLE_ROWS.map((r, i) => (
            <div key={r.id} className={`grid grid-cols-[64px_minmax(0,1fr)_auto] items-start gap-3 border-b border-hair py-2 ${i === 0 ? "bg-sel" : ""}`}>
              <span className="font-mono text-[12px] text-accent">{r.id}</span>
              <span className="flex min-w-0 flex-col gap-1">
                <span className="flex min-w-0 items-center gap-1.5">
                  <span className="truncate font-medium">{t(r.title)}</span>
                  {r.dora && <span className="shrink-0 border border-flag px-1 text-[10.5px] font-semibold text-flag">DORA</span>}
                </span>
                <StageGate states={states} current={states[r.phase].name} />
              </span>
              <span className="font-mono text-[12px] tabular">{r.amount}</span>
            </div>
          ))}
        </div>
        <p className="m-0 flex flex-wrap gap-x-3 text-[12px]">
          <span className="text-ok">● {t("profile.sample.approved")}</span>
          <span className="text-flag">● {t("profile.sample.due")}</span>
          <span className="text-danger">● {t("profile.sample.rejected")}</span>
          <span className="text-muted">{t("profile.sample.updated")}</span>
        </p>
        <div className="flex flex-wrap gap-2">
          <button type="button" tabIndex={-1} className="btn btn-primary py-1.5">{t("profile.sample.approve")}</button>
          <button type="button" tabIndex={-1} className="btn py-1.5">{t("profile.sample.defer")}</button>
          <button type="button" tabIndex={-1} className="btn btn-danger py-1.5">{t("profile.sample.reject")}</button>
        </div>
      </div>
    </div>
  );
}

function Swatch({ colors }: { colors: string[] }) {
  return (
    <span className="inline-flex border border-hair" aria-hidden="true">
      {colors.map((c, i) => (
        <span key={i} className="size-3.5" style={{ background: c }} />
      ))}
    </span>
  );
}

function Option({ name, checked, onChange, children }: { name: string; checked: boolean; onChange: () => void; children: ReactNode }) {
  return (
    <label className={`flex cursor-pointer items-start gap-2.5 border-b border-hair px-2 py-2.5 ${checked ? "bg-sel" : "hover:bg-sel/50"}`}>
      <input type="radio" name={name} className="mt-1 accent-[var(--accent)]" checked={checked} onChange={onChange} />
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">{children}</span>
    </label>
  );
}

const sameChoice = (a: AppearanceChoice, b: AppearanceChoice) => a.theme === b.theme && a.font === b.font;

/** Sezione "Aspetto" del profilo: scelta personale, valida su tutti i dispositivi. */
export function AppearanceSection() {
  const t = useT();
  const qc = useQueryClient();
  const prefs = useQuery({ queryKey: ["preferences"], queryFn: appearanceApi.preferences });
  const saved = prefs.data?.appearance ?? { theme: null, font: null };
  const [draft, setDraft] = useState<AppearanceChoice | null>(null);
  const choice = draft ?? saved;
  const org = prefs.data?.tenant_default;
  const theme: ThemeKey = choice.theme ?? (org?.theme as ThemeKey | null) ?? "atlante";
  const font: FontKey = choice.font ?? (org?.font as FontKey | null) ?? "atkinson";
  const dirty = draft !== null && !sameChoice(draft, saved);
  const [done, setDone] = useState(false);

  // Il font dell'anteprima si carica solo quando lo si sceglie.
  useEffect(() => {
    void loadFont(font);
  }, [font]);

  const save = useMutation({
    mutationFn: () => appearanceApi.savePreferences(choice),
    onSuccess: (p) => {
      qc.setQueryData(["preferences"], p);
      setDraft(null);
      setDone(true);
      void applyAppearance(p.effective);
    },
  });
  const set = (patch: Partial<AppearanceChoice>) => {
    setDone(false);
    setDraft({ ...choice, ...patch });
  };
  const orgLabel = (n: string) => (org ? t("profile.look.followOrg", { name: n }) : t("profile.look.followDefault", { name: n }));

  return (
    <section className="flex flex-col gap-4">
      <SectionTitle>{t("profile.look.title")}</SectionTitle>
      <p className="m-0 max-w-[640px] text-muted">{t("profile.look.text")}</p>
      <ErrorNote error={prefs.error} />
      {prefs.data && (
        <>
          <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
            <fieldset className="m-0 flex min-w-0 flex-col border-0 p-0">
              <legend className="label mb-1.5 p-0">{t("profile.look.theme")}</legend>
              <Option name="aspetto-tema" checked={choice.theme === null} onChange={() => set({ theme: null })}>
                <span className="font-semibold">{orgLabel(themeName(org?.theme ?? "atlante"))}</span>
                <span className="text-[12px] text-muted">{t("profile.look.followHint")}</span>
              </Option>
              {THEMES.map((th) => (
                <Option key={th.key} name="aspetto-tema" checked={choice.theme === th.key} onChange={() => set({ theme: th.key })}>
                  <span className="flex flex-wrap items-center gap-x-2.5 gap-y-1">
                    <span className="font-semibold">{th.name}</span>
                    <Swatch colors={th.swatch.light} />
                    <Swatch colors={th.swatch.dark} />
                  </span>
                  <span className="text-[12px] text-muted">{t(`profile.theme.${th.key}`)}</span>
                </Option>
              ))}
            </fieldset>
            <fieldset className="m-0 flex min-w-0 flex-col border-0 p-0">
              <legend className="label mb-1.5 p-0">{t("profile.look.font")}</legend>
              <Option name="aspetto-font" checked={choice.font === null} onChange={() => set({ font: null })}>
                <span className="font-semibold">{orgLabel(fontName(org?.font ?? "atkinson"))}</span>
                <span className="text-[12px] text-muted">{t("profile.look.followHint")}</span>
              </Option>
              {FONTS.map((f) => (
                <Option key={f.key} name="aspetto-font" checked={choice.font === f.key} onChange={() => set({ font: f.key })}>
                  <span className="font-semibold">
                    {f.name} <span className="font-normal text-muted">{t("profile.look.fontWith", { mono: f.mono })}</span>
                  </span>
                  <span className="text-[12px] text-muted">{t(`profile.font.${f.key}`)}</span>
                </Option>
              ))}
              <p className="m-0 mt-2 text-[12px] text-muted">{t("profile.look.fontNote")}</p>
            </fieldset>
          </div>

          <div className="flex flex-col gap-2">
            <span className="label">{t("profile.look.preview")}</span>
            <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
              <Sample theme={theme} font={font} mode="light" />
              <Sample theme={theme} font={font} mode="dark" />
            </div>
          </div>

          <ErrorNote error={save.error} />
          <div className="flex flex-wrap items-center gap-2">
            <button type="button" className="btn btn-primary" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>
              {save.isPending ? t("profile.look.saving") : t("profile.look.save")}
            </button>
            <button type="button" className="btn" disabled={!dirty || save.isPending} onClick={() => setDraft(null)}>
              {t("common.cancel")}
            </button>
            {done && !dirty && <span role="status" className="text-[12px] text-ok">{t("profile.look.saved")}</span>}
          </div>
        </>
      )}
    </section>
  );
}

/** Default dell'organizzazione, per chi ha tenant.admin: vale per chi non ha scelto. */
export function TenantAppearanceSection({ tenantName }: { tenantName: string }) {
  const t = useT();
  const qc = useQueryClient();
  const current = useQuery({ queryKey: ["tenant-appearance"], queryFn: appearanceApi.tenant });
  const saved: AppearanceChoice = { theme: current.data?.theme ?? null, font: current.data?.font ?? null };
  const [draft, setDraft] = useState<AppearanceChoice | null>(null);
  const choice = draft ?? saved;
  const dirty = draft !== null && !sameChoice(draft, saved);
  const [done, setDone] = useState(false);
  const theme = choice.theme ?? "atlante";
  const font = choice.font ?? "atkinson";
  const mode = document.documentElement.dataset.theme === "dark" ? "dark" : "light";

  useEffect(() => {
    void loadFont(font);
  }, [font]);

  const save = useMutation({
    mutationFn: () => appearanceApi.saveTenant(choice),
    onSuccess: (r) => {
      qc.setQueryData(["tenant-appearance"], r);
      setDraft(null);
      setDone(true);
      // Cambia l'aspetto di chi segue il default, compreso chi sta salvando.
      void qc.invalidateQueries({ queryKey: ["preferences"] });
      void qc.invalidateQueries({ queryKey: ["me"] });
    },
  });
  const set = (patch: Partial<AppearanceChoice>) => {
    setDone(false);
    setDraft({ ...choice, ...patch });
  };

  return (
    <section className="flex flex-col gap-4">
      <SectionTitle>{t("profile.look.tenantTitle")}</SectionTitle>
      <p className="m-0 max-w-[640px] text-muted">
        <Trans k="profile.look.tenantText" slots={{ tenant: <span className="text-fg">{tenantName}</span> }} />
      </p>
      <ErrorNote error={current.error} />
      {current.data && (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-[minmax(0,1fr)_minmax(0,1.3fr)]">
          <div className="flex flex-col gap-3">
            <label className="flex flex-col gap-1.5">
              <span className="label">{t("profile.look.tenantTheme")}</span>
              <Select className="field" value={choice.theme ?? ""} onChange={(e) => set({ theme: (e.target.value || null) as ThemeKey | null })}>
                <option value="">{t("profile.look.product", { name: themeName("atlante") })}</option>
                {THEMES.map((th) => (
                  <option key={th.key} value={th.key}>{th.name}</option>
                ))}
              </Select>
            </label>
            <label className="flex flex-col gap-1.5">
              <span className="label">{t("profile.look.tenantFont")}</span>
              <Select className="field" value={choice.font ?? ""} onChange={(e) => set({ font: (e.target.value || null) as FontKey | null })}>
                <option value="">{t("profile.look.product", { name: fontName("atkinson") })}</option>
                {FONTS.map((f) => (
                  <option key={f.key} value={f.key}>{f.name}</option>
                ))}
              </Select>
            </label>
            <ErrorNote error={save.error} />
            <div className="flex flex-wrap items-center gap-2">
              <button type="button" className="btn btn-primary" disabled={!dirty || save.isPending} onClick={() => save.mutate()}>
                {save.isPending ? t("profile.look.saving") : t("profile.look.saveDefault")}
              </button>
              {dirty && <button type="button" className="btn" onClick={() => setDraft(null)}>{t("common.cancel")}</button>}
            </div>
            {done && !dirty && <span role="status" className="text-[12px] text-ok">{t("profile.look.tenantSaved")}</span>}
            {current.data.updated_at && !dirty && (
              <span className="text-[12px] text-muted">{t("profile.look.lastChange", { date: fmtDate(current.data.updated_at) })}</span>
            )}
          </div>
          <Sample theme={theme} font={font} mode={mode} />
        </div>
      )}
    </section>
  );
}
