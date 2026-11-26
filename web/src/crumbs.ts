import { tr } from "./i18n/core";

/** Segmenti del percorso mostrato sopra il titolo di una pagina («governance / conformità»), nella lingua corrente. */
export type Crumb =
  | "demand" | "delivery" | "governance" | "grc" | "architecture" | "devsecops" | "configuration" | "time"
  | "requests" | "roadmap" | "ranking" | "dashboard" | "compliance" | "metrics" | "adherence" | "backlog"
  | "requirements" | "policy" | "vulnerabilities" | "risks" | "risksIncidents" | "board" | "finance"
  | "reviews" | "report" | "impacts" | "applications" | "assets" | "incidents" | "documents";

export function crumbs(...parts: Crumb[]): string {
  // «Governance» non è più una sezione del menu (nav.ts): DevSecOps, architettura e documenti
  // hanno la loro, il resto sta in «rischi e conformità».
  if (parts[0] === "governance") parts = ["devsecops", "architecture", "documents"].includes(parts[1]) ? parts.slice(1) : ["grc", ...parts.slice(1)];
  return parts.map((p) => tr(`common.crumb.${p}`)).join(" / ");
}
