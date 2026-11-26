// Organizzazioni (tenant) dall'interfaccia, per il superamministratore (area «organizzazioni»).
// Le chiavi iniziano con «organizzazioni.»: vedi docs/sviluppo/testi-e-lingue.md.
import type { Source } from "../types";

export default {
  "organizzazioni.path": "piattaforma / organizzazioni",
  "organizzazioni.title": "Organizzazioni",
  "organizzazioni.gestisci": "Gestisci organizzazioni",
  "organizzazioni.riservato": "Questa pagina è riservata al superamministratore dell'istanza.",
  "organizzazioni.intro": "Un'organizzazione è uno spazio separato, con le sue persone, i suoi dati e le sue impostazioni: niente di quello che c'è in una si vede nelle altre. Creandola ne diventi amministratore con il tuo account.",
  "organizzazioni.entra": "Entra",

  "organizzazioni.nuova.title": "Nuova organizzazione",
  "organizzazioni.nuova.nome": "Nome",
  "organizzazioni.nuova.slug": "Identificativo",
  "organizzazioni.nuova.slugHelp": "Minuscole, cifre e trattini (da 2 a 63 caratteri). Non si cambia più.",
  "organizzazioni.nuova.lingua": "Lingua",
  "organizzazioni.nuova.linguaHelp": "È la lingua dei nomi di tipi, fasi, ruoli e processi di partenza.",
  "organizzazioni.nuova.valuta": "Valuta base",
  "organizzazioni.nuova.altroAdmin": "Un altro amministratore (facoltativo)",
  "organizzazioni.nuova.altroAdminHelp": "Se la scrivi, la persona riceve un invito con il ruolo di amministratore.",
  "organizzazioni.nuova.crea": "Crea organizzazione",
  "organizzazioni.nuova.creando": "Creazione in corso…",

  "organizzazioni.fatto.title": "«{name}» è pronta",
  "organizzazioni.fatto.testo": "Sei già amministratore: entra per iniziare. Ti portiamo su «Inizia da qui», dove si sceglie anche un modello di partenza per il tipo di azienda.",
  "organizzazioni.fatto.altra": "Crea un'altra organizzazione",

  "organizzazioni.persone.title": "Come metterci dentro le persone",
  "organizzazioni.persone.passo1": "Entra nell'organizzazione con «Entra».",
  "organizzazioni.persone.passo2": "Apri Configurazione → Persone e accessi.",
  "organizzazioni.persone.passo3": "Premi «Aggiungi persona», scegli «Invita», scrivi l'email e il ruolo: la persona riceve un link per accedere.",
  "organizzazioni.persone.nota": "Chi ha già un account su questa istanza lo riusa: con la stessa email entra anche nella nuova organizzazione e le cambia dal menu in alto.",
  "organizzazioni.persone.vai": "Entra e apri Persone e accessi",

  "organizzazioni.invito.inviato": "Invito inviato per email a {email}.",
  "organizzazioni.invito.copia": "Invito pronto per {email}: l'istanza non invia email, copia il link e consegnalo tu.",
  "organizzazioni.invito.link": "Link di invito",
  "organizzazioni.invito.errore": "L'organizzazione è stata creata, ma l'invito all'altro amministratore non è riuscito: puoi rifarlo da Persone e accessi.",

  "organizzazioni.elenco.title": "Organizzazioni dell'istanza",
  "organizzazioni.elenco.persone": {"one": "{count} persona", "other": "{count} persone"},
  "organizzazioni.elenco.attiva": "Sei qui",
  "organizzazioni.elenco.nonMembro": "Non ne fai parte",

  "organizzazioni.stato.attiva": "Attiva",
  "organizzazioni.stato.sospesa": "Sospesa",
  "organizzazioni.stato.etichetta": "Stato",
  "organizzazioni.rinomina": "Rinomina",
  "organizzazioni.rinomina.nome": "Nome dell'organizzazione {name}",
  "organizzazioni.rinomina.salva": "Salva",
  "organizzazioni.rinomina.annulla": "Annulla",
  "organizzazioni.sospendi": "Sospendi",
  "organizzazioni.sospendi.sicuro": "Sì, sospendi",
  "organizzazioni.sospendi.effetto": "I membri non potranno più entrare finché non la riattivi. I dati restano: non viene cancellato niente.",
  "organizzazioni.sospendi.stessa": "Non puoi sospendere l'organizzazione in cui stai lavorando: entra in un'altra.",
  "organizzazioni.riattiva": "Riattiva",
  "organizzazioni.sospesaTra": "{name} (sospesa)",

  "organizzazioni.sospesa.title": "Organizzazione sospesa",
  "organizzazioni.sospesa.testo": {
    "one": "L'organizzazione «{name}» è sospesa. Rivolgiti all'amministratore dell'istanza.",
    "other": "Le organizzazioni {name} sono sospese. Rivolgiti all'amministratore dell'istanza.",
  },
} satisfies Source;
