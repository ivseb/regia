// Giorni lavorativi lato client: stesse regole di internal/platform/calendar (weekend e
// festività nazionali italiane, lunedì dell'Angelo compreso). Date come "AAAA-MM-GG", in UTC.

import { loc } from "./i18n/format";

const DAY = 86_400_000;

export function parse(s: string): number {
  return Date.UTC(+s.slice(0, 4), +s.slice(5, 7) - 1, +s.slice(8, 10));
}

export function fmt(t: number): string {
  return new Date(t).toISOString().slice(0, 10);
}

export function addDays(t: number, n: number): number {
  return t + n * DAY;
}

export function diffDays(a: number, b: number): number {
  return Math.round((b - a) / DAY);
}

export function today(): number {
  const d = new Date();
  return Date.UTC(d.getFullYear(), d.getMonth(), d.getDate());
}

const easterCache = new Map<number, number>();
function easter(y: number): number {
  const c = easterCache.get(y);
  if (c !== undefined) return c;
  const a = y % 19,
    b = Math.floor(y / 100),
    cc = y % 100,
    d = Math.floor(b / 4),
    e = b % 4,
    f = Math.floor((b + 8) / 25),
    g = Math.floor((b - f + 1) / 3),
    h = (19 * a + b - d - g + 15) % 30,
    i = Math.floor(cc / 4),
    k = cc % 4,
    l = (32 + 2 * e + 2 * i - h - k) % 7,
    m = Math.floor((a + 11 * h + 22 * l) / 451),
    month = Math.floor((h + l - 7 * m + 114) / 31),
    day = ((h + l - 7 * m + 114) % 31) + 1;
  const t = Date.UTC(y, month - 1, day);
  easterCache.set(y, t);
  return t;
}

const FIXED = ["01-01", "01-06", "04-25", "05-01", "06-02", "08-15", "11-01", "12-08", "12-25", "12-26"];

export function isHoliday(t: number): boolean {
  const s = fmt(t);
  return FIXED.includes(s.slice(5)) || t === addDays(easter(+s.slice(0, 4)), 1);
}

export function isWorkday(t: number): boolean {
  const wd = new Date(t).getUTCDay();
  return wd !== 0 && wd !== 6 && !isHoliday(t);
}

export function nextWorkday(t: number): number {
  while (!isWorkday(t)) t = addDays(t, 1);
  return t;
}

export function endAfter(start: number, n: number): number {
  let t = nextWorkday(start);
  while (n > 1) {
    t = addDays(t, 1);
    if (isWorkday(t)) n--;
  }
  return t;
}

export function workdays(a: number, b: number): number {
  let n = 0;
  for (let t = a; t <= b; t = addDays(t, 1)) if (isWorkday(t)) n++;
  return n;
}

export function afterEnd(end: number): number {
  return nextWorkday(addDays(end, 1));
}

export function monday(t: number): number {
  const wd = (new Date(t).getUTCDay() + 6) % 7;
  return addDays(t, -wd);
}

// Nomi dei mesi con Intl, nella lingua corrente (le date sono in UTC).
const md = (t: number, o: Intl.DateTimeFormatOptions) => new Date(t).toLocaleDateString(loc(), { timeZone: "UTC", ...o });

export function short(t: number): string {
  return md(t, { day: "numeric", month: "short" }).replace(/\.$/, "");
}

export function shortYear(t: number): string {
  return md(t, { day: "numeric", month: "short", year: "numeric" });
}

export function monthName(t: number, long = false): string {
  return md(t, { month: long ? "long" : "short" }).replace(/\.$/, "");
}
