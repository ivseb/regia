// AI, ondata 2 (replica/ai.md): server MCP, domande sul grafo, streaming, avviso del budget.
// Le chiavi iniziano con «ai.»: vedi docs/sviluppo/testi-e-lingue.md.
import type { Source } from "../types";

export default {
  "ai.feature.mcp": "Server MCP",
  "ai.tool.conferma": "conferma",
  "ai.tool.rifiuta": "rifiuta",
  "ai.tool.proposte": "proposte",

  "ai.notif.label": "AI",
  "ai.notif.hint": "Budget mensile dell'AI oltre l'80% (a chi amministra l'organizzazione)",
  "ai.notif.budget_warning": "Il budget dell'AI di {month} è <f>al {percent}%</f>: al 100% le funzioni dell'AI si fermano fino al mese dopo",
  "ai.notif.plain": "Avviso dell'AI",

  "ai.mcp.title": "Server MCP",
  "ai.mcp.intro": "Con il server MCP Claude Desktop, Claude Code o un IDE lavorano su Regia con i permessi della persona che si collega. Il modello è quello della persona: Regia non chiama nessun fornitore, non conta token e non può mascherare i dati personali, perché li legge il client. Le modifiche restano proposte, che la persona conferma.",
  "ai.mcp.enable": "Server MCP acceso",
  "ai.mcp.enableHelp": "Indipendente dall'AI di Regia: non serve un fornitore. Ogni persona si collega con un suo token API, che crea nel profilo; con un token di sola lettura vede solo le letture.",
  "ai.mcp.url": "Indirizzo da dare al client",
  "ai.mcp.urlHelp": "Con l'intestazione Authorization: Bearer seguita dal token. La guida per chi amministra ha gli esempi per Claude Desktop, Claude Code e gli IDE.",

  "ai.panel.usingTools": "Sto usando: {tools}…",

  "ai.graph.path": "domande sul grafo",
  "ai.graph.listTitle": "Domande sul grafo",
  "ai.graph.listEmpty": "Nessun elemento con questa query.",
  "ai.graph.mode": "Che cosa chiedi",
  "ai.graph.modeChat": "Conversazione",
  "ai.graph.modeGraph": "Domanda sul grafo",
  "ai.graph.empty": "Chiedi quali elementi vuoi vedere, per esempio «quali richieste aperte sono urgenti?». L'assistente scrive la query di Regia: la vedi, la puoi cambiare e salvare come vista.",
  "ai.graph.thinking": "Scrivo la query…",
  "ai.graph.question": "La tua domanda sul grafo",
  "ai.graph.placeholder": "Per esempio: quali applicazioni critiche hanno vulnerabilità scadute?",
  "ai.graph.query": "Query",
  "ai.graph.run": "Esegui",
  "ai.graph.openList": "Apri nell'elenco",
  "ai.graph.none": "Nessun elemento trovato.",
  "ai.graph.count": { one: "{count}{more} elemento", other: "{count}{more} elementi" },
  "ai.graph.results": "Elementi trovati",
  "ai.graph.viewName": "Nome della vista",
  "ai.graph.save": "Salva come vista",
  "ai.graph.saved": "Vista salvata.",
  "ai.graph.openView": "Aprila",
  "ai.graph.corrected": "La prima query («{query}») non era valida: {error}. L'assistente l'ha corretta una volta.",
  "ai.graph.invalid": "L'assistente non è riuscito a scrivere una query valida nemmeno correggendola: {error}. Correggila qui sotto a mano.",
} satisfies Source;
