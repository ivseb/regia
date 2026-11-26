// Archiviazione dei tipi di sistema (epic, sprint, ...): si archiviano quando non hanno elementi.
import type { Source } from "../types";

export default {
  "configurazione.tipiLavoro.sistema": "Tipo di sistema: senza elementi si archivia come gli altri, e si può sempre riattivare.",
  "configurazione.tipiLavoro.sistemaConElementi": "Tipo di sistema con {count} elementi: si archivia solo quando non ne ha più (spostali o eliminali).",
} satisfies Source;
