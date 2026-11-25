import type { Cabina, RankItem } from "../api";

// Calcoli della cabina di portafoglio, tutti nel browser su un solo carico di dati: spostando la
// linea di taglio (o il budget) residuo, team in sovraccarico e date si aggiornano senza chiamate.

/** Quante richieste, nell'ordine dato, stanno nel budget (come il server). */
export function autoCut(items: RankItem[], budget: number | null): number {
  if (budget == null) return items.length;
  let c = 0;
  let n = 0;
  for (const it of items) {
    c += it.estimate_cents ?? 0;
    if (c > budget) break;
    n++;
  }
  return n;
}

export type TeamStat = {
  id: string;
  name: string;
  /** Picco del carico sulla capacità (1 = 100%). */
  peak: number;
  /** Indici delle settimane oltre la capacità. */
  over: number[];
  overloaded: boolean;
};

const EPS = 0.05;

/** Carico (giorni) del team per settimana con le richieste sopra la linea; `skip` ne esclude una. */
function demand(cab: Cabina, items: RankItem[], cut: number, teamIdx: number, skip?: string): number[] {
  const team = cab.teams[teamIdx];
  const d = team.base.slice();
  for (let i = 0; i < Math.min(cut, items.length); i++) {
    if (items[i].id === skip) continue;
    const e = cab.rows[items[i].id]?.efforts.find((x) => x.team_id === team.id);
    if (e) e.weeks.forEach((v, w) => (d[w] += v));
  }
  return d;
}

function statOf(cab: Cabina, teamIdx: number, d: number[]): TeamStat {
  const team = cab.teams[teamIdx];
  let peak = 0;
  const over: number[] = [];
  d.forEach((v, w) => {
    const cap = team.capacity[w] ?? 0;
    if (v > cap + EPS) over.push(w);
    // Settimane senza capacità (festività): il rapporto si legge su un giorno, non all'infinito.
    peak = Math.max(peak, v / Math.max(cap, 1));
  });
  return { id: team.id, name: team.name, peak, over, overloaded: over.length > 0 };
}

export function teamStats(cab: Cabina, items: RankItem[], cut: number): TeamStat[] {
  return cab.teams.map((_, i) => statOf(cab, i, demand(cab, items, cut, i)));
}

export type CutEval = {
  cut: number;
  inside: RankItem[];
  outside: RankItem[];
  committed: number;
  residual: number | null;
  teams: TeamStat[];
  overloaded: TeamStat[];
  /** Ultima data di fine tra le richieste dentro (AAAA-MM-GG). */
  end: string | null;
};

export function evalCut(cab: Cabina, items: RankItem[], cut: number, budget: number | null): CutEval {
  const c = Math.max(0, Math.min(cut, items.length));
  const inside = items.slice(0, c);
  const committed = inside.reduce((a, it) => a + (it.estimate_cents ?? 0), 0);
  const teams = teamStats(cab, items, c);
  let end: string | null = null;
  for (const it of inside) {
    const e = cab.windows[it.id]?.end;
    if (e && (!end || e > end)) end = e;
  }
  return {
    cut: c,
    inside,
    outside: items.slice(c),
    committed,
    residual: budget == null ? null : budget - committed,
    teams,
    overloaded: teams.filter((t) => t.overloaded),
    end,
  };
}

export type Suggestion = {
  item: RankItem;
  /** La richiesta dopo cui si propone di metterla (la prima fuori dalla linea). */
  after: RankItem | null;
  /** Posizione finale proposta (1-based). */
  to: number;
  /** Togliendola il team rientra nella capacità. */
  frees: boolean;
  days: number;
};

/**
 * Chi occupa il team sopra la linea e che cosa spostare: la richiesta a punteggio più basso (l'ultima
 * in graduatoria) che, tolta, riporta il team nella capacità. Gli obblighi normativi in testa non si propongono.
 */
export function occupants(cab: Cabina, items: RankItem[], cut: number, teamId: string): { item: RankItem; days: number; overDays: number }[] {
  const ti = cab.teams.findIndex((t) => t.id === teamId);
  if (ti < 0) return [];
  const st = teamStats(cab, items, cut)[ti];
  const out: { item: RankItem; days: number; overDays: number }[] = [];
  for (let i = 0; i < Math.min(cut, items.length); i++) {
    const e = cab.rows[items[i].id]?.efforts.find((x) => x.team_id === teamId);
    if (!e || e.days <= 0) continue;
    out.push({ item: items[i], days: e.days, overDays: st.over.reduce((a, w) => a + (e.weeks[w] ?? 0), 0) });
  }
  return out;
}

export function suggestMove(cab: Cabina, items: RankItem[], cut: number, teamId: string, mandatoryFirst: boolean): Suggestion | null {
  const ti = cab.teams.findIndex((t) => t.id === teamId);
  if (ti < 0) return null;
  const occ = occupants(cab, items, cut, teamId).filter((o) => o.overDays > 0 && !(mandatoryFirst && o.item.is_mandatory));
  if (occ.length === 0) return null;
  // Dall'ultimo in graduatoria: il punteggio più basso.
  const lowFirst = occ.slice().sort((a, b) => b.item.position - a.item.position);
  const after = items[cut] ?? null;
  const to = after ? Math.min(cut + 1, items.length) : items.length;
  for (const o of lowFirst) {
    if (!statOf(cab, ti, demand(cab, items, cut, ti, o.item.id)).overloaded) return { item: o.item, after, to, frees: true, days: o.days };
  }
  const o = lowFirst[0];
  return { item: o.item, after, to, frees: false, days: o.days };
}

export type Why = { k: string; params: Record<string, string | number> };

/** Perché la richiesta sta in questa posizione (a partire dalla seconda guardando quella sopra). */
export function whyHere(items: RankItem[], i: number, mandatoryFirst: boolean, fmt: { score: (n: number) => string; date: (iso: string) => string }): Why {
  const it = items[i];
  const prev = i > 0 ? items[i - 1] : null;
  if (it.pinned) return { k: "pinned", params: { reason: it.pin_reason ?? "" } };
  if (it.is_mandatory && mandatoryFirst) {
    return it.regulatory_deadline
      ? { k: "mandatoryDeadline", params: { date: fmt.date(it.regulatory_deadline) } }
      : { k: "mandatory", params: {} };
  }
  if (it.score == null) return { k: "noScore", params: {} };
  if (!prev) return { k: "first", params: { score: fmt.score(it.score) } };
  if (mandatoryFirst && prev.is_mandatory) return { k: "afterMandatory", params: { prev: prev.key } };
  if (prev.score == null || prev.pinned) return { k: "belowPinned", params: { prev: prev.key } };
  if (prev.score > it.score) return { k: "lowerScore", params: { score: fmt.score(it.score), prevScore: fmt.score(prev.score), prev: prev.key } };
  return { k: "tie", params: { prev: prev.key } };
}
