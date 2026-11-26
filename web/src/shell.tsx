import { Select } from "./select";
import { lazy, Suspense, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Link, Outlet, useNavigate, useRouterState } from "@tanstack/react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { tempoApi } from "./tempoApi";
import { api, ApiError, collab, type Me } from "./api";
import { dafareApi } from "./dafareApi";
import { applyTheme, getTheme, type Theme } from "./theme";
import { ErrorNote } from "./ui";
import { useLiveUpdates } from "./live";
import { createKinds, SHORTCUT, useCurrentKey, useKeyboardShortcuts, usePaletteShortcut, type CreateKind, type ItemAction, type PaletteStart } from "./shortcuts";
import { FavoriteViews } from "./views/FavoriteViews";
import { buildNav, navItemActive, navPages, sectionKey, type NavItem, type NavSection } from "./nav";
import { Avatar, ModuleChip, uiIconOfPath, uiModuleOfPath } from "./moduleIcon";
import { useTypeNames } from "./typesApi";
import { moduleOfPath, moduleOn, pathAfterSwitch, pathOn, type ModuleKey } from "./modules";
import { useT } from "./i18n/react";
import { Logo, SourceLink } from "./brand";
import { AvvioStrip } from "./pages/AvvioStrip";
import { ProvaBar } from "./pages/ProvaBar";
import { OrganizationSuspended } from "./pages/OrganizationSuspended";
import { tr } from "./i18n/core";
import type { Key } from "./i18n/core";

// Palette dei comandi: si carica alla prima apertura (piano, passo 16).
const CommandPalette = lazy(() => import("./palette").then((m) => ({ default: m.CommandPalette })));
// Pannello «?» e dialogo «+ Crea» (tastiera.tsx): si caricano alla prima apertura.
const KeyboardLayer = lazy(() => import("./tastiera").then((m) => ({ default: m.KeyboardLayer })));
// Accesso (pages/Login.tsx, con il secondo passo dell'MFA): si carica solo senza sessione.
const Login = lazy(() => import("./pages/Login").then((m) => ({ default: m.Login })));

export function useMe() {
  return useQuery({ queryKey: ["me"], queryFn: api.me });
}

/** Radice: senza sessione mostra l'accesso, altrimenti il guscio con la pagina corrente. */
export function Root() {
  const me = useMe();
  // Link monouso (invito, scelta della password): pagine pubbliche, con o senza sessione.
  const path = useRouterState({ select: (s) => s.location.pathname });
  if (path.startsWith("/invito/") || path.startsWith("/password/")) return <Outlet />;
  if (me.isPending) return null;
  if (me.error instanceof ApiError && me.error.status === 401) return <Suspense fallback={null}><Login /></Suspense>;
  if (me.error) return <div className="p-8"><ErrorNote error={me.error} /></div>;
  // Portale del richiedente (piano, passo 14): i membri «requester» non vedono i menu interni.
  if (me.data!.active_tenant?.member_type === "requester") return <PortalShell me={me.data!}><Outlet /></PortalShell>;
  return <Shell me={me.data!}><Outlet /></Shell>;
}

function Shell({ me, children }: { me: Me; children: ReactNode }) {
  const t = useT();
  const qc = useQueryClient();
  // «Da fare» (ondata 2): un solo numero per ciò che chiede un'azione, per il menu e per il campanello.
  const todoCount = useQuery({ queryKey: ["todo", "count"], queryFn: dafareApi.count, enabled: !!me.active_tenant, refetchInterval: 120_000 });
  // Avvisi non letti: il numero accanto ad «Attività».
  const unread = useQuery({ queryKey: ["notifications", "count"], queryFn: collab.unread, enabled: !!me.active_tenant, refetchInterval: 120_000 });
  // Timesheet da approvare (piano, passo 18): il numero accanto alla voce Timesheet.
  const timePending = useQuery({ queryKey: ["tempo", "pending"], queryFn: tempoApi.pending, enabled: !!me.active_tenant && moduleOn(me, "work"), staleTime: 60_000 });
  useLiveUpdates(me.active_tenant?.tenant_id);
  // Sotto i 768 px il menu è a scomparsa: si apre dalla barra in alto e si chiude cambiando pagina.
  const [menuOpen, setMenuOpen] = useState(false);
  const path = useRouterState({ select: (s) => s.location.pathname });
  useEffect(() => setMenuOpen(false), [path]);
  // Palette dei comandi ⌘K / Ctrl+K (piano, passo 16), disponibile in tutta l'app.
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [paletteStart, setPaletteStart] = useState<PaletteStart | undefined>();
  const openPalette = useCallback((start?: PaletteStart) => {
    setPaletteStart(start);
    setPaletteOpen(true);
  }, []);
  const togglePalette = useCallback(() => {
    setPaletteStart(undefined);
    setPaletteOpen((o) => !o);
  }, []);
  usePaletteShortcut(togglePalette);
  // Tastiera e «+ Crea» (replica/usabilita.md, proposta 10): pannello «?», dialogo di creazione e avvisi brevi.
  const navigate = useNavigate();
  const [helpOpen, setHelpOpen] = useState(false);
  const [creating, setCreating] = useState<{ kind?: CreateKind } | null>(null);
  const [notice, setNotice] = useState("");
  const noticeTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const notify = useCallback((text: string) => {
    setNotice(text);
    clearTimeout(noticeTimer.current);
    noticeTimer.current = setTimeout(() => setNotice(""), 4000);
  }, []);
  const canCreate = createKinds(me).length > 0;
  const openCreate = useCallback((kind?: CreateKind) => setCreating({ kind }), []);
  const tenantName = me.tenants.find((t) => t.tenant_id === me.active_tenant?.tenant_id)?.name ?? me.tenants[0]?.name ?? "Regia";
  // Organizzazioni sospese: se l'account non ne ha di attive, la pagina lo dice invece di un errore generico.
  const suspendedOrgs = me.tenants.filter((o) => o.status && o.status !== "active");
  const switchTenant = useMutation({
    mutationFn: api.switchTenant,
    onSuccess: (m) => {
      // Si azzerano i dati dell'organizzazione precedente; i componenti restano collegati e ricaricano.
      qc.setQueryData(["me"], m);
      qc.resetQueries({ predicate: (q) => q.queryKey[0] !== "me" });
      // Pilota «Home lab», difetto 8: se nella nuova organizzazione la pagina non vale, si va all'inizio.
      const next = pathAfterSwitch(m, path);
      if (next !== path) navigate({ to: next as never });
    },
  });
  const logout = useMutation({
    mutationFn: api.logout,
    onSuccess: () => {
      qc.clear();
      window.location.assign("/");
    },
  });

  const tn = useTypeNames();
  const counts = {
    todo: todoCount.data?.total,
    todoMore: todoCount.data?.more,
    activity: unread.data?.unread,
    timePending: timePending.data?.items.length,
  };
  const sections = buildNav(me, counts, tn);
  // Tutte le pagine del menu, anche quelle riunite in una voce o in un indice (Configurazione, Elenchi).
  const pages = navPages(me, counts, tn);

  // Pagine raggiungibili con «g» + lettera: le pagine del menu (con i permessi e i moduli accesi) e le due fisse.
  const allowedKey = pages.map((i) => i.to ?? "").join("|");
  const allowed = useMemo(() => new Set<string>(["/notifiche", "/profilo", ...allowedKey.split("|")]), [allowedKey]);
  // Sezioni chiuse dall'utente, ricordate nel browser; quella della pagina attiva resta aperta.
  const [closed, setClosed] = useState<ReadonlySet<string>>(readClosed);
  const toggleSection = useCallback((key: string) => {
    setClosed((prev) => {
      const next = new Set(prev);
      if (!next.delete(key)) next.add(key);
      writeClosed(next);
      return next;
    });
  }, []);
  const currentKey = useCurrentKey();
  useKeyboardShortcuts(
    {
      allowed,
      help: () => setHelpOpen(true),
      create: () => canCreate && openCreate(),
      search: openPalette,
      go: (to) => navigate({ to: to as never }),
      open: (key) => navigate({ to: "/e/$key", params: { key } }),
      item: (action: ItemAction, key) => {
        if (action === "phase") return openPalette({ only: "phase", key });
        void import("./tastiera").then((m) =>
          m.runItemAction(action, key, { me, qc, notify, navigate: (to, hash) => navigate({ to: to as never, hash }) }),
        );
      },
    },
    currentKey,
  );

  // Titolo della scheda del browser: la voce di menu della pagina corrente (le pagine con un
  // titolo proprio, come elementi e documenti, lo impostano dopo aver caricato i dati).
  // Il layout effect gira prima degli effetti dei figli: le pagine con un titolo proprio (e la pagina
  // «non trovato», anche sotto una voce di menu come /architettura/xyz) lo impostano dopo di lui.
  const navTitle = pageTitleFor(path, pages);
  useLayoutEffect(() => {
    if (navTitle) document.title = `${navTitle} · Regia`;
  }, [navTitle, path]);

  return (
    <div className="flex min-h-screen flex-col md:flex-row">
      <a href="#contenuto" className="skip-link">{t("shell.skip")}</a>
      <header className="sticky top-0 z-40 flex items-center gap-2.5 border-b border-rule bg-bg px-4 py-2.5 md:hidden">
        <Logo />
        <span className="min-w-0 flex-1 truncate text-[15px] font-bold" title={tenantName}>{tenantName}</span>
        <button className="btn py-1" aria-haspopup="dialog" onClick={() => openPalette()}>
          {t("shell.search")}
        </button>
        <Bell enabled={!!me.active_tenant} todo />
        <button className="btn py-1" aria-expanded={menuOpen} aria-controls="menu-principale" onClick={() => setMenuOpen((o) => !o)}>
          {menuOpen ? t("shell.menu.close") : t("shell.menu.open")}
        </button>
      </header>
      <nav
        id="menu-principale"
        aria-label={t("shell.mainNav")}
        className={`${menuOpen ? "fixed inset-x-0 top-[49px] bottom-0 z-50 flex overflow-y-auto bg-side" : "hidden"} flex-col gap-5 border-r border-hair bg-side px-3 py-4 md:sticky md:top-0 md:flex md:h-screen md:w-[248px] md:shrink-0 md:overflow-y-auto`}
      >
        <div className="flex items-center gap-2.5">
          <Logo />
          {me.tenants.length > 1 ? (
            <Select
              aria-label={t("shell.organization")}
              className="min-w-0 flex-1 truncate border-0 bg-transparent text-[15px] font-bold text-fg"
              value={me.active_tenant?.tenant_id ?? ""}
              onChange={(e) => switchTenant.mutate(e.target.value)}
            >
              {me.tenants.map((t) => (
                <option key={t.tenant_id} value={t.tenant_id} disabled={!!t.status && t.status !== "active"}>
                  {t.status && t.status !== "active" ? tr("organizzazioni.sospesaTra", { name: t.name }) : t.name}
                </option>
              ))}
            </Select>
          ) : (
            <span className="text-[15px] font-bold">{me.tenants[0]?.name ?? t("shell.noOrganization")}</span>
          )}
          <span className="ml-auto hidden shrink-0 md:inline-flex">
            <Bell enabled={!!me.active_tenant} todo />
          </span>
        </div>
        {me.is_platform_admin && (
          <Link to={"/piattaforma/organizzazioni" as never} className="-mt-3 flex min-h-11 shrink-0 items-center text-[12.5px] text-muted underline hover:text-fg md:min-h-0">
            {t("organizzazioni.gestisci")}
          </Link>
        )}
        <button
          className="hidden items-center gap-2 border border-hair px-2.5 py-1.5 text-left text-muted hover:border-rule hover:text-fg md:flex"
          aria-haspopup="dialog"
          aria-keyshortcuts="Meta+K Control+K"
          onClick={() => openPalette()}
        >
          <span className="flex-1">{t("shell.searchOrGo")}</span>
          <kbd className="font-mono text-[11px]">{SHORTCUT}</kbd>
        </button>
        <div className="hidden gap-1.5 md:flex">
          {canCreate && (
            <button className="btn btn-primary flex-1 justify-center" aria-haspopup="dialog" aria-keyshortcuts="c" onClick={() => openCreate()}>
              {t("tastiera.create.button")}
              <kbd className="font-mono text-[11px] font-normal opacity-80">c</kbd>
            </button>
          )}
          <button className="btn flex-1 justify-center md:flex-none" aria-haspopup="dialog" aria-keyshortcuts="?" aria-label={t("tastiera.help.open")} title={t("tastiera.help.open")} onClick={() => setHelpOpen(true)}>
            ?
          </button>
        </div>
        <FavoriteViews enabled={!!me.active_tenant} />
        {sections.map((sec) => {
          const key = sectionKey(sec);
          return <NavGroup key={key} id={key} sec={sec} path={path} closed={closed.has(key)} onToggle={toggleSection} />;
        })}
        <div className="mt-auto flex flex-col gap-3 border-t border-hair pt-4">
          <ThemeSwitch />
          <div className="flex items-center gap-2.5">
            <Link to="/profilo" title={t("shell.profileLink")} className="flex min-w-0 flex-1 items-center gap-2.5 text-fg no-underline hover:text-accent">
            <Avatar name={me.display_name} className="size-7 text-[11px]" />
            <span className="flex min-w-0 flex-1 flex-col">
              <span className="font-semibold [overflow-wrap:anywhere]">{me.display_name}</span>
              <span className="text-[12px] [overflow-wrap:anywhere] text-muted">{me.email}</span>
            </span>
            </Link>
            <button className="text-[12px] text-muted underline hover:text-fg" onClick={() => logout.mutate()}>
              {t("shell.logout")}
            </button>
          </div>
          <SourceLink className="text-muted" />
        </div>
      </nav>
      <main id="contenuto" tabIndex={-1} data-modulo={uiModuleOfPath(path)} className={`flex min-w-0 flex-1 flex-col outline-none ${canCreate ? "max-md:pb-20" : ""}`}>
        {!me.active_tenant ? (
          // Le pagine di piattaforma restano raggiungibili al superamministratore anche senza organizzazione attiva.
          me.is_platform_admin && path.startsWith("/piattaforma/") ? (
            <PageEnter path={path}>{children}</PageEnter>
          ) : suspendedOrgs.length > 0 ? (
            <OrganizationSuspended names={suspendedOrgs.map((o) => o.name)} />
          ) : (
            <p className="p-8 text-muted">{t("shell.noActiveOrg")}</p>
          )
        ) : pathOn(me, path) ? (
          <>
            <ProvaBar />
            <AvvioStrip me={me} />
            <PageEnter path={path}>{children}</PageEnter>
          </>
        ) : (
          <ModuleOff module={moduleOfPath(path)!} />
        )}
      </main>
      {/* «+ Crea» sul telefono: in basso a destra, dove arriva il pollice. Non sulle pagine che hanno
          già una barra d'azioni fissa in basso (vista di crisi), dove la coprirebbe. */}
      {canCreate && !paletteOpen && !creating && !path.startsWith("/crisi/") && (
        <button
          className="btn btn-primary fixed right-4 bottom-[max(1rem,env(safe-area-inset-bottom))] z-40 md:hidden"
          aria-haspopup="dialog"
          onClick={() => openCreate()}
        >
          {t("tastiera.create.button")}
        </button>
      )}
      {me.active_tenant && paletteOpen && (
        <Suspense fallback={null}>
          <CommandPalette
            open={paletteOpen}
            start={paletteStart}
            onClose={() => setPaletteOpen(false)}
            notify={notify}
            onCreate={canCreate ? openCreate : undefined}
            onHelp={() => setHelpOpen(true)}
          />
        </Suspense>
      )}
      {me.active_tenant && (helpOpen || creating) && (
        <Suspense fallback={null}>
          <KeyboardLayer
            help={helpOpen}
            create={creating}
            allowed={allowed}
            onClose={() => {
              setHelpOpen(false);
              setCreating(null);
            }}
            notify={notify}
          />
        </Suspense>
      )}
      <div role="status" aria-live="polite" className="pointer-events-none fixed inset-x-4 bottom-20 z-[80] flex justify-center md:bottom-6">
        {notice && (
          <span key={notice} className="avviso-entra pointer-events-auto flex items-center gap-2.5 bg-fg px-3.5 py-2.5 text-[13px] font-semibold text-bg shadow-[5px_5px_0_var(--m-devsecops)]">
            <span className="size-2.5 shrink-0 bg-m-devsecops" aria-hidden="true" />
            {notice}
          </span>
        )}
      </div>
    </div>
  );
}

/** Percorsi le cui voci restano attive anche nelle sottopagine. */
const NAV_PREFIX = new Set(["/processi", "/configurazione/valutazioni", "/configurazione/campi", "/configurazione/tipi", "/configurazione/requisiti", "/documenti", "/architettura"]);

const CLOSED_KEY = "regia.menu.chiuse";

function readClosed(): ReadonlySet<string> {
  try {
    const v = JSON.parse(localStorage.getItem(CLOSED_KEY) ?? "[]");
    return new Set(Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : []);
  } catch {
    return new Set();
  }
}

function writeClosed(keys: ReadonlySet<string>) {
  try {
    localStorage.setItem(CLOSED_KEY, JSON.stringify([...keys]));
  } catch {
    // Archivio del browser non disponibile: la scelta vale fino al prossimo caricamento.
  }
}

/** Freccia della sezione richiudibile: giù se aperta, a destra se chiusa. */
function Chevron({ open }: { open: boolean }) {
  return (
    <svg viewBox="0 0 12 12" width="12" height="12" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="square" aria-hidden="true" className={`shrink-0 text-muted transition-transform ${open ? "rotate-90" : ""}`}>
      <path d="M4.5 2.5 8 6l-3.5 3.5" />
    </svg>
  );
}

/**
 * Sezione della barra laterale: quadratino del modulo con l'icona, voci con la barretta del colore del
 * modulo quando sono attive, contatori a pillola (pieni se chiedono un'azione). Una sezione con una sola
 * voce dallo stesso nome (Documenti, Architettura, Elenchi, Configurazione) è un collegamento unico.
 * Le sezioni con più voci si chiudono dalla testata; quella della pagina attiva resta sempre aperta.
 */
function NavGroup({ id, sec, path, closed, onToggle }: { id: string; sec: NavSection; path: string; closed: boolean; onToggle: (key: string) => void }) {
  const t = useT();
  // La prima voce con il nome della sezione (DevSecOps, Documenti, Architettura) diventa la testata.
  const head = sec.items[0]?.to && sec.items[0].label === sec.label ? sec.items[0] : null;
  const rest = head ? sec.items.slice(1) : sec.items;
  const isActive = (it: NavItem) => navItemActive(it, path, NAV_PREFIX);
  const current = sec.items.some(isActive);
  const canClose = rest.length > 0 && !current;
  const open = !canClose || !closed;
  const listId = `menu-sezione-${id}`;
  const voce = (it: NavItem) => (isActive(it) ? { "aria-current": "page" as const } : {});
  return (
    <div className={`m-${sec.module} flex flex-col gap-0.5`}>
      {head ? (
        <div className="flex items-center">
          <Link
            to={head.to!}
            {...voce(head)}
            activeOptions={{ exact: true, includeSearch: false }}
            className={`nav-voce nav-testa -mx-1 flex min-h-11 flex-1 items-center gap-2 px-1 py-1 text-[12.5px] font-bold text-fg no-underline md:min-h-0 ${isActive(head) ? "nav-attiva" : ""}`}
          >
            <ModuleChip module={sec.module} icon={sec.icon} />
            <span className="flex-1">{sec.label}</span>
          </Link>
          {canClose && (
            <button
              type="button"
              className="flex size-11 shrink-0 items-center justify-center text-muted hover:text-fg md:size-7"
              aria-expanded={open}
              aria-controls={listId}
              aria-label={t("shell.menu.section", { name: sec.label })}
              onClick={() => onToggle(id)}
            >
              <Chevron open={open} />
            </button>
          )}
        </div>
      ) : canClose ? (
        <button
          type="button"
          className="flex min-h-11 w-full items-center gap-2 pb-1 text-left text-fg md:min-h-0"
          aria-expanded={open}
          aria-controls={listId}
          onClick={() => onToggle(id)}
        >
          <ModuleChip module={sec.module} icon={sec.icon} />
          <span className="flex-1 text-[12.5px] font-bold">{sec.label}</span>
          <Chevron open={open} />
        </button>
      ) : (
        <div className="flex items-center gap-2 pb-1">
          <ModuleChip module={sec.module} icon={sec.icon} />
          <span className="text-[12.5px] font-bold">{sec.label}</span>
        </div>
      )}
      {rest.length > 0 && (
        <div id={listId} hidden={!open} className="flex flex-col gap-0.5">
          {rest.map((it) =>
            it.to ? (
              <Link
                key={it.label}
                to={it.to}
                {...voce(it)}
                activeOptions={{ exact: true, includeSearch: false }}
                className={`nav-voce flex min-h-11 items-center gap-2 py-1 pr-2 pl-[30px] text-fg no-underline md:min-h-0 ${isActive(it) ? "nav-attiva" : ""}`}
              >
                <span className="flex-1">{it.label}</span>
                {!!it.count && (
                  <span className={`nav-conta tabular ${it.hot ? "urgente" : ""}`}>
                    {it.count}
                    {it.more ? "+" : ""}
                    {it.countLabel ? ` ${it.countLabel}` : ""}
                  </span>
                )}
              </Link>
            ) : (
              <span key={it.label} className="flex items-center gap-2 py-1 pl-[30px] text-muted" aria-disabled="true">
                <span className="flex-1">{it.label}</span>
                <span className="font-mono text-[10.5px]">{t("shell.soon")}</span>
              </span>
            ),
          )}
        </div>
      )}
    </div>
  );
}

/** Ingresso della pagina (6 px, 0,25 s) a ogni cambio di percorso, senza rimontare la pagina. */
function PageEnter({ path, children }: { path: string; children: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el?.animate || matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    el.animate([{ opacity: 0, transform: "translateY(6px)" }, { opacity: 1, transform: "none" }], { duration: 250, easing: "cubic-bezier(.2,.7,.2,1)" });
  }, [path]);
  return (
    <div ref={ref} className="flex min-w-0 flex-1 flex-col">
      {children}
    </div>
  );
}

const MODULE_NAMES: Record<ModuleKey, Key> = {
  demand: "shell.module.demand",
  projects: "shell.module.projects",
  work: "shell.module.work",
  knowledge: "shell.module.knowledge",
  devsecops: "shell.module.devsecops",
  grc: "shell.module.grc",
  architecture: "shell.module.architecture",
};

/** Pagina di un modulo spento per l'organizzazione: dice perché e dove si riaccende. */
function ModuleOff({ module }: { module: ModuleKey }) {
  const t = useT();
  useEffect(() => {
    document.title = `${t("shell.moduleOff.title")} · Regia`;
  }, [t]);
  const perms = useMe().data?.active_tenant?.permissions ?? [];
  const canSwitch = perms.includes("*") || perms.includes("tenant.admin");
  return (
    <div>
      <PageHeader path="moduli" title={t("shell.moduleOff.title")} />
      <div className="flex max-w-[60ch] flex-col gap-3 px-4 pb-8 md:px-7">
        <p className="m-0">
          {t("shell.moduleOff.text", { name: t(MODULE_NAMES[module]) })}
        </p>
        {canSwitch ? (
          <div className="flex gap-2">
            <Link to="/configurazione/moduli" className="btn btn-primary no-underline">{t("shell.moduleOff.goTo")}</Link>
          </div>
        ) : (
          <p className="m-0 text-muted">{t("shell.moduleOff.askAdmin")}</p>
        )}
      </div>
    </div>
  );
}

/**
 * Pagina del menu che corrisponde meglio al percorso (la più specifica), per il titolo della scheda.
 * Usa tutte le pagine (navPages), anche quelle che nel menu stanno dentro un indice o una scheda.
 */
function pageTitleFor(path: string, pages: NavItem[]): string | null {
  let best: NavItem | null = null;
  for (const it of pages) {
    if (!it.to) continue;
    if (path === it.to || path.startsWith(it.to + "/")) {
      if (!best || it.to.length > best.to!.length) best = it;
    }
  }
  if (best) return best.label;
  if (path.startsWith("/notifiche")) return tr("dafare.nav.activity");
  // Le vecchie code, ora dentro «Da fare», conservano il loro titolo.
  if (path === "/approvazioni") return tr("shell.nav.approvals");
  if (path === "/valutazioni") return tr("shell.nav.reviews");
  if (path === "/requisiti") return tr("shell.nav.verify");
  if (path.startsWith("/profilo")) return tr("shell.portal.profile");
  return null;
}

function ThemeSwitch() {
  const t = useT();
  const [theme, setTheme] = useState<Theme>(getTheme());
  const opts: { v: Theme; label: string }[] = [
    { v: "light", label: t("shell.theme.light") },
    { v: "dark", label: t("shell.theme.dark") },
    { v: "system", label: t("shell.theme.system") },
  ];
  return (
    <div role="radiogroup" aria-label={t("shell.theme.label")} className="flex border border-hair text-[12px]">
      {opts.map((o) => (
        <button
          key={o.v}
          role="radio"
          aria-checked={theme === o.v}
          className={`flex-1 py-1 ${theme === o.v ? "bg-fg text-bg" : "text-muted hover:text-fg"}`}
          onClick={() => {
            applyTheme(o.v);
            setTheme(o.v);
          }}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

/** Intestazione di pagina: percorso in mono, titolo grande, azioni a destra. */
export function PageHeader({ path, title, actions }: { path: string; title: string; actions?: ReactNode }) {
  // Il modulo viene dal percorso della pagina: icona nel percorso e striscia sotto il titolo nel suo colore.
  const pathname = useRouterState({ select: (s) => s.location.pathname });
  const module = uiModuleOfPath(pathname);
  return (
    <header className={`m-${module} flex flex-wrap items-end gap-4 px-4 md:px-7 pt-6 pb-4`}>
      <div className="flex flex-col gap-1">
        <span className="flex items-center gap-2 font-mono text-[11.5px] text-muted">
          <ModuleChip module={module} icon={uiIconOfPath(pathname)} size="sm" />
          {path}
        </span>
        <h1 className="m-0 text-[34px] leading-[1.05] font-bold tracking-[-0.025em]">{title}</h1>
        <span className="mt-2 block h-1 w-14 bg-mc" aria-hidden="true" />
      </div>
      {actions && <div className="ml-auto flex gap-2">{actions}</div>}
    </header>
  );
}

/**
 * Campanello. Nel guscio interno (`todo`) conta solo ciò che chiede un'azione e porta a «Da fare»; gli avvisi
 * informativi stanno in «Attività». Nel portale dei richiedenti, che non ha «Da fare», conta le notifiche non
 * lette e porta all'elenco.
 */
function Bell({ enabled, todo = false }: { enabled: boolean; todo?: boolean }) {
  const t = useT();
  // Si aggiorna con il flusso SSE (live.ts invalida tutte le query); l'intervallo è una riserva.
  const count = useQuery({ queryKey: ["notifications", "count"], queryFn: collab.unread, enabled: enabled && !todo, refetchInterval: 120_000 });
  const todoCount = useQuery({ queryKey: ["todo", "count"], queryFn: dafareApi.count, enabled: enabled && todo, refetchInterval: 120_000 });
  const n = todo ? (todoCount.data?.total ?? 0) : (count.data?.unread ?? 0);
  const big = todo ? (todoCount.data?.more ?? false) || n >= 100 : n >= 100;
  const label = todo
    ? n
      ? t("dafare.bellTodo", { n: big ? t("shell.moreThan99") : n })
      : t("dafare.bellNone")
    : n
      ? t("shell.notificationsUnread", { n: big ? t("shell.moreThan99") : n })
      : t("shell.notifications");
  return (
    <Link
      to={todo ? "/da-fare" : "/notifiche"}
      aria-label={label}
      title={label}
      className="relative flex size-11 items-center justify-center text-fg no-underline hover:text-accent md:size-8"
      activeProps={{ className: "!text-accent" }}
    >
      <svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="square" aria-hidden="true">
        <path d="M6 9a6 6 0 0 1 12 0c0 5 2 6.5 2 6.5H4S6 14 6 9Z" />
        <path d="M10 19a2 2 0 0 0 4 0" />
      </svg>
      {n > 0 && (
        <span className="absolute -top-0.5 -right-1 min-w-[18px] bg-accent px-1 text-center font-mono text-[10.5px] leading-[16px] text-accent-fg tabular">
          {n >= 99 && big ? "99+" : n}
        </span>
      )}
    </Link>
  );
}

// ---------------------------------------------------------------------
// Guscio del portale (piano, passo 14): per i membri «requester» niente menu interni,
// solo il portale, le notifiche e il profilo. I link agli elementi (dalle notifiche)
// portano alla richiesta nel portale.

const PORTAL_PATHS = ["/portale", "/notifiche", "/profilo"];

function PortalShell({ me, children }: { me: Me; children: ReactNode }) {
  const t = useT();
  const tn = useTypeNames();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const path = useRouterState({ select: (s) => s.location.pathname });
  useLiveUpdates(me.active_tenant?.tenant_id);
  const allowed = PORTAL_PATHS.some((p) => path === p || path.startsWith(p + "/"));
  useEffect(() => {
    if (allowed) return;
    const m = /^\/(?:e|richieste)\/([A-Za-z][A-Za-z0-9_]*-\d+)/.exec(path);
    if (m) navigate({ to: "/portale/richieste/$key", params: { key: m[1].toUpperCase() }, replace: true });
    else navigate({ to: "/portale", replace: true });
  }, [allowed, path, navigate]);
  const tenantName = me.tenants.find((t) => t.tenant_id === me.active_tenant?.tenant_id)?.name ?? "Regia";
  const logout = useMutation({
    mutationFn: api.logout,
    onSuccess: () => {
      qc.clear();
      window.location.assign("/");
    },
  });
  const initials = me.display_name.split(/\s+/).slice(0, 2).map((p) => p[0]).join("").toUpperCase();
  return (
    <div className="flex min-h-screen flex-col">
      <header className="sticky top-0 z-40 flex items-center gap-3 border-b border-rule bg-bg px-4 py-2.5 md:px-7">
        <Link to="/portale" className="flex min-w-0 items-center gap-2.5 text-fg no-underline">
          <Logo />
          <span className="truncate text-[15px] font-bold" title={tenantName}>{tenantName}</span>
          <span className="hidden font-mono text-[11.5px] text-muted sm:inline">{t("shell.portal.name")}</span>
        </Link>
        <span className="ml-auto flex items-center gap-1.5">
          <Link to="/portale/nuova" className="btn btn-primary hidden py-1 no-underline sm:inline-flex">{tn.create("business_request", t("shell.portal.requestFallback"))}</Link>
          <Bell enabled={!!me.active_tenant} />
          <Link to="/profilo" title={t("shell.portal.profile")} className="flex size-8 items-center justify-center no-underline">
            <span className="sr-only">{t("shell.portal.profileOf")}</span>
            <span className="flex size-7 items-center justify-center rounded-full bg-accent text-[11px] font-semibold text-accent-fg">{initials}</span>
          </Link>
          <button className="text-[12px] text-muted underline hover:text-fg" onClick={() => logout.mutate()}>
            {t("shell.logout")}
          </button>
        </span>
      </header>
      <main className="flex min-w-0 flex-1 flex-col">{allowed && me.active_tenant ? children : null}</main>
      <footer className="flex flex-wrap items-center gap-3 border-t border-hair px-4 py-3 md:px-7">
        <span className="w-48">
          <ThemeSwitch />
        </span>
        <span className="text-[12px] text-muted">{me.email}</span>
        <SourceLink className="ml-auto text-muted" />
      </footer>
    </div>
  );
}
