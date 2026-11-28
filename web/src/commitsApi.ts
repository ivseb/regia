// Commit legati a un elemento (DevSecOps): la chiave nel messaggio di un commit o nel titolo di
// una pull request unita, letti dai connettori GitHub e Gitea.
import { call } from "./api";

export type ElementCommit = {
  sha: string;
  title: string;
  author: string;
  at: string;
  repository: string;
  url: string;
  source: "commit" | "pull_request";
  pr_number: number | null;
  service: string;
};

export type ElementCommits = {
  commits: ElementCommit[];
  /** Il rilascio in produzione che completa l'elemento (per ogni servizio, il primo dopo l'ultimo commit). */
  release: { service: string; version: string; at: string; lead_days: number; release_id: string | null } | null;
  first_at: string | null;
};

export const commitsApi = {
  of: (ref: string) => call<ElementCommits>("GET", `/elements/${encodeURIComponent(ref)}/commits`),
};
