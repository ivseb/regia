// Stati vuoti di un'organizzazione appena nata: una frase che dice a cosa serve la pagina e l'azione
// giusta, senza la query in mostra (area «lavoro»).
import type { Source } from "../types";

export default {
  "lavoro.vuoti.business_request.titolo": "Ancora nessuna richiesta",
  "lavoro.vuoti.business_request.testo": "Una richiesta è la proposta di un lavoro o di un investimento: da qui passa alla valutazione e alla decisione.",
  "lavoro.vuoti.business_request.azione": "Crea la prima richiesta",
  "lavoro.vuoti.risk.titolo": "Nessun rischio registrato",
  "lavoro.vuoti.risk.testo": "Il registro tiene i rischi con probabilità, impatto e trattamento. Registra il primo, anche solo da rivedere.",
  "lavoro.vuoti.risk.azione": "Registra il primo rischio",
  "lavoro.vuoti.incident.titolo": "Nessun incidente registrato",
  "lavoro.vuoti.incident.testo": "Qui si apre un incidente quando qualcosa si rompe: scadenze e notifiche alle autorità partono da lì.",
  "lavoro.vuoti.incident.azione": "Vai agli incidenti",
  "lavoro.vuoti.control.titolo": "Nessun controllo",
  "lavoro.vuoti.control.testo": "Un controllo è ciò che fate per rispettare un requisito normativo, con le sue evidenze. Si crea dalla conformità.",
  "lavoro.vuoti.control.azione": "Vai ai controlli",
  "lavoro.vuoti.page.titolo": "Nessun documento",
  "lavoro.vuoti.page.testo": "I documenti stanno negli spazi di documentazione: runbook, decisioni di architettura, policy.",
  "lavoro.vuoti.page.azione": "Vai ai documenti",
  "lavoro.vuoti.application.titolo": "Nessuna applicazione",
  "lavoro.vuoti.application.testo": "Il catalogo delle applicazioni serve a collegare richieste, rischi, controlli e rilasci a ciò che usate davvero.",
  "lavoro.vuoti.application.azione": "Vai al catalogo",
  "lavoro.vuoti.logical_asset.titolo": "Nessun asset",
  "lavoro.vuoti.logical_asset.testo": "Gli asset sono i sistemi, i dati e i servizi su cui poggiano le applicazioni.",
  "lavoro.vuoti.logical_asset.azione": "Vai al catalogo",
  "lavoro.vuoti.impact.titolo": "Nessun impatto",
  "lavoro.vuoti.impact.testo": "Un impatto nasce quando una richiesta tocca un'applicazione o un team: compare qui da solo.",
  "lavoro.vuoti.modello": "Oppure parti da un modello",

  "lavoro.vuoti.board.titolo": "Il board è vuoto",
  "lavoro.vuoti.board.testo": "Il lavoro compare qui quando un team lo prende in carico. Crea la prima storia dal backlog; se hai scritto un filtro, toglilo.",
  "lavoro.vuoti.board.azione": "Vai al backlog",

  "lavoro.vuoti.rischi.titolo": "Nessun rischio aperto",
  "lavoro.vuoti.rischi.testo": "Il registro tiene i rischi con probabilità, impatto e trattamento. Registra il primo, anche solo da rivedere.",
  "lavoro.vuoti.rischi.azione": "Registra il primo rischio",

  "lavoro.vuoti.conformita.modello": "Parti da un modello",

  "lavoro.vuoti.documenti.titolo": "Nessuno spazio di documentazione",
  "lavoro.vuoti.documenti.testo": "Uno spazio raccoglie le pagine di un gruppo di lavoro. Runbook, decisioni di architettura e policy hanno già i loro template.",
  "lavoro.vuoti.documenti.azione": "Crea il primo spazio",
} satisfies Source;
