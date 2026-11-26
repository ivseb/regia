import type { Translation } from "../types";
import type it from "../it/configurazione-tipi-lavoro";

export default {
  "configurazione.tipiLavoro.sistema": "System type: with no items it can be archived like the others, and reactivated at any time.",
  "configurazione.tipiLavoro.sistemaConElementi": "System type with {count} items: it can only be archived once it has none (move or delete them).",
} satisfies Translation<typeof it>;
