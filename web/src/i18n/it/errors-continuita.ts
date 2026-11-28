// Errori della continuità operativa (docs/sviluppo/continuita.md).
import type { Source } from "../types";

export default {
  "errors.validation.continuity.not_ready": "La continuità non è ancora preparata in questa organizzazione.",
  "errors.validation.continuity.bia_exists": "Questa capability ha già la BIA {key}.",
  "errors.validation.continuity.bia_locked": "La BIA si modifica solo in Bozza: usa «Aggiorna» per riportarla in Bozza.",
  "errors.validation.continuity.bia_incomplete": "Per proseguire servono la griglia degli impatti piena, l'RTO e l'RPO.",
  "errors.validation.continuity.not_capability": "La BIA si fa su una capability.",
  "errors.validation.continuity.subject_invalid": "Il test riguarda un'applicazione o una capability.",
  "errors.validation.continuity.test_locked": "Il test è chiuso o annullato: non si modifica più.",
  "errors.validation.continuity.test_type": "Questo tipo di test non è nella configurazione.",
  "errors.validation.continuity.outcome": "Esito o rilievi non validi: servono l'esito, la data di esecuzione e, se il test ha rilievi o non è superato, almeno un rilievo.",
  "errors.validation.continuity.config": "La configurazione non è valida: controlla nomi, chiavi, livelli e orizzonti.",
  "errors.validation.continuity.plan_page": "Il piano deve essere una pagina dei documenti.",
  "errors.validation.continuity.plan_target": "Il piano si collega a una capability o a un'applicazione.",
  "errors.validation.continuity.version_conflict": "Qualcuno ha salvato la configurazione nel frattempo: ricarica e riprova.",
} satisfies Source;
