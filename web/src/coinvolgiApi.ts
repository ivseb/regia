// Procedura guidata «Coinvolgi un team»: anteprima e conferma (internal/api/coinvolgi.go).
import { call } from "./api";

export type CoinvolgiInput = {
  team_id?: string;
  new_team?: string;
  process: string;
  /** Fase d'ingresso; vuoto = quando la richiesta si crea. */
  phase: string;
  when: string;
  review: boolean;
  work: boolean;
  mandatory: boolean;
};

export type CoinvolgiResult = {
  team: string;
  team_new: boolean;
  review: {
    type_key: string;
    type_name: string;
    template_key: string;
    mandatory: boolean;
    review_team_new: boolean;
    group_new: boolean;
  } | null;
  work: boolean;
  rule: { key: string; name: string; process: string; process_name: string; on: string; phase: string; when: string };
  version: number;
  warnings: string[];
  applied: boolean;
};

export const coinvolgiApi = {
  preview: (i: CoinvolgiInput) => call<CoinvolgiResult>("POST", "/coinvolgi/anteprima", i),
  apply: (i: CoinvolgiInput) => call<CoinvolgiResult>("POST", "/coinvolgi", i),
};
