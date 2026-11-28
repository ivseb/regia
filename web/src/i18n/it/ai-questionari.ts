// Questionari precompilati dall'AI (funzione «questionari»).
// Le chiavi iniziano con «ai.»: vedi docs/sviluppo/testi-e-lingue.md.
import type { Source } from "../types";

export default {
  "ai.feature.questionari": "Questionari precompilati",
  "ai.feature.questionari.d": "Risposte proposte per le domande di una sezione, ognuna con la sua fonte: valutazioni concluse, fatti del grafo, pagine Knowledge, allegati.",
  "ai.questionari.propose": "Proponi le risposte",
  "ai.questionari.proposeHelp": "L'AI legge le valutazioni concluse della stessa applicazione (e di quelle simili), i fatti del grafo, le pagine collegate e gli allegati, e propone una risposta per le domande ancora vuote. Niente si salva finché non lo accetti.",
  "ai.questionari.working": "Sto leggendo le fonti e preparando le proposte…",
  "ai.questionari.result": "{proposed} proposte con fonte, {none} da compilare a mano.",
  "ai.questionari.remaining": "Restano {n} domande: chiedi di nuovo per proporle.",
  "ai.questionari.acceptSection": "Accetta le proposte della sezione ({n})",
  "ai.questionari.confirmTitle": "Queste domande cambiano il punteggio o generano prescrizioni e requisiti. Rileggile prima di accettarle tutte:",
  "ai.questionari.confirmAll": "Accetta tutte",
  "ai.questionari.cancel": "Annulla",
  "ai.questionari.boxLabel": "Proposta dell'AI",
  "ai.questionari.accept": "Accetta",
  "ai.questionari.edit": "Modifica",
  "ai.questionari.acceptEdited": "Accetta con la modifica",
  "ai.questionari.discard": "Scarta",
  "ai.questionari.none": "Nessuna fonte: da compilare a mano.",
  "ai.questionari.confidence.alta": "confidenza alta",
  "ai.questionari.confidence.media": "confidenza media",
  "ai.questionari.confidence.bassa": "confidenza bassa",
  "ai.questionari.sources": "Fonti:",
  "ai.questionari.source.answer": "risposta confermata in {key}",
  "ai.questionari.source.similar": "risposta di {app} (applicazione simile) in {key}",
  "ai.questionari.source.page": "pagina {key} «{title}»",
  "ai.questionari.source.fact": "{key}: {detail}",
  "ai.questionari.source.attachment": "allegato «{name}» di {key}",
  "ai.questionari.careful": "Guardala con attenzione prima di accettarla.",
} satisfies Source;
