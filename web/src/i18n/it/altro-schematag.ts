// Schema dei tag dei rilasci alla scoperta dei repository GitHub e Gitea (pilota home lab, difetto 5).
// Le chiavi iniziano con «altro.»: vedi docs/sviluppo/testi-e-lingue.md.
import type { Source } from "../types";

export default {
  "altro.schemaTag.title": "Quale tag è il rilascio?",
  "altro.schemaTag.intro": "Questi repository pubblicano più tag per ogni versione, per esempio quello del chart e quello dell'applicazione. Se valgono tutti come rilascio, le metriche DORA li contano due volte.",
  "altro.schemaTag.question": "{repo}: quale schema di tag è il rilascio vero?",
  "altro.schemaTag.scheme": { one: "{count} tag recente: {examples}", other: "{count} tag recenti, per esempio {examples}" },
  "altro.schemaTag.all": "Tutti: ogni tag è un rilascio",
  "altro.schemaTag.save": "Salva",
  "altro.schemaTag.note": "La scelta va nel profilo della pipeline dell'integrazione (tag_filter del repository) e si cambia da lì.",
  "altro.schemaTag.saved": "Salvato nel profilo «{profile}», versione {version}.",
  "altro.schemaTag.savedNew": "Creato il profilo «{profile}» e collegato all'integrazione.",
  "altro.schemaTag.pruned": { one: "Tolto {count} rilascio già letto dell'altro schema.", other: "Tolti {count} rilasci già letti degli altri schemi." },
} satisfies Source;
