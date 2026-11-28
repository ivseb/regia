// AI: ragionamento configurabile per livello e per funzione (internal/ai/model.go, effortFor).
// Le chiavi iniziano con «ai.»: vedi docs/sviluppo/testi-e-lingue.md.
import type { Source } from "../types";

export default {
  "ai.cfg.reasoning": "Ragionamento",
  "ai.cfg.reasoningHelp": "Quanto il modello ragiona prima di rispondere: di più vuol dire risposte più accurate, ma più lente e più costose. In automatico il livello rapido ragiona poco e l'approfondito molto (l'assistente medio, perché fa più giri con gli strumenti); le richieste brevi (domande sul grafo, riassunti, classificazione) ragionano sempre poco, salvo un valore scelto per la funzione. Ollama e i servizi compatibili decidono da sé.",
  "ai.cfg.reasoning.auto": "Automatico",
  "ai.cfg.reasoning.low": "Basso",
  "ai.cfg.reasoning.medium": "Medio",
  "ai.cfg.reasoning.high": "Alto",
  "ai.cfg.reasoningTier": "Come il livello",
  "ai.cfg.reasoningFor": "Ragionamento per {name}",
  "ai.reason.max_tokens": "Il modello ha esaurito i token prima di rispondere: riprova o semplifica la domanda.",
  "ai.reason.empty": "Il modello ha risposto senza testo.",
} satisfies Source;
