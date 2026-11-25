// Nomi per lingua di fasi e opzioni dei campi (Configurazione → Campi e editor dei processi). Il nome
// resta canonico: condizioni, processi e codice lo citano; la traduzione è solo l'etichetta mostrata.
// Vedi docs/sviluppo/etichette-lingua.md.
import type { Source } from "../types";

export default {
  "configurazione.etichette.titolo": "Traduzioni",
  "configurazione.etichette.riassunto": "{n} su {total} in inglese",
  "configurazione.etichette.opzioniGuida": "Il nome in inglese di ogni valore, mostrato a chi usa l'interfaccia in inglese. Lasciato vuoto segue il nome italiano, che resta quello usato da condizioni e processi.",
  "configurazione.etichette.faseGuida": "Il nome in inglese della fase, mostrato a chi usa l'interfaccia in inglese. Lasciato vuoto segue il nome italiano, che resta quello usato da passaggi, condizioni e codice.",
  "configurazione.etichette.opzioneInglese": "Nome in inglese di «{name}»",
  "configurazione.etichette.faseInglese": "Nome in inglese",
  "configurazione.etichette.nomeItaliano": "Nome",
  "configurazione.etichette.inglese": "Inglese",
} satisfies Source;
