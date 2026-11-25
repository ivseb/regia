import { tr } from "./i18n/core";

/** Segmenti del percorso mostrato sopra il titolo di una pagina («governance / conformità»), nella lingua corrente. */
export type Crumb =
  | "demand" | "delivery" | "governance" | "architecture" | "devsecops" | "configuration" | "time"
  | "requests" | "roadmap" | "ranking" | "dashboard" | "compliance" | "metrics" | "adherence" | "backlog"
  | "requirements" | "policy" | "vulnerabilities" | "risks" | "risksIncidents" | "board" | "finance"
  | "reviews" | "report" | "impacts" | "applications" | "assets" | "incidents" | "documents";

export function crumbs(...parts: Crumb[]): string {
  return parts.map((p) => tr(`common.crumb.${p}`)).join(" / ");
}
