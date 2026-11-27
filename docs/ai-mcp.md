# Collegare Claude Desktop, Claude Code o un IDE a Regia (server MCP)

Con il server MCP un assistente esterno (Claude Desktop, Claude Code, VS Code, Cursor…) cerca, legge e propone modifiche su Regia **con i permessi della persona** che si collega. Il modello è quello del client della persona: Regia non chiama nessun fornitore e il budget dell'AI dell'organizzazione non si consuma.

Guida generale dell'AI: [ai.md](ai.md). Dettagli tecnici: [sviluppo/ai.md](sviluppo/ai.md).

> Gli esempi usano `https://regia.example.org` e il segnaposto `<token>`. Non mettere mai un token vero in un file che finisce in un repository: usa una variabile d'ambiente, come negli esempi.

## 1. Chi amministra: accendere il server

1. **Configurazione → Intelligenza artificiale → Server MCP**: spunta **Server MCP acceso** e **Salva le impostazioni**. È spento di default ed è indipendente da «AI accesa»: non serve un fornitore del modello.
2. Sotto l'interruttore c'è l'**indirizzo da dare ai client**, per esempio `https://regia.example.org/api/v1/mcp`.
3. Considera i dati: il testo che gli strumenti leggono arriva al modello del client (per esempio a Claude, tramite Claude Desktop). Regia **non può mascherarlo** come fa con i propri fornitori. Per un'organizzazione regolamentata decidi tu se è accettabile, ed eventualmente chiedi alle persone di usare token di sola lettura.

Per spegnerlo togli la spunta: i client ricevono un errore (403) e il resto di Regia non cambia.

## 2. Ogni persona: creare un token

Nel profilo, sezione **Token API**: un nome, la scadenza, i permessi, poi **Crea token**. Il token:

- vale solo nell'organizzazione in cui lo crei e con i tuoi permessi di adesso (se ti tolgono un permesso, il token lo perde);
- può avere meno permessi di te: con i soli permessi di lettura (per esempio `element.read`) il client vede solo gli strumenti di lettura e non può proporre modifiche;
- scade (90 giorni di default, al massimo un anno) e si revoca dal profilo;
- si vede una volta sola, alla creazione: copialo nel gestore di password o in una variabile d'ambiente.

Il server accetta solo il token, non la sessione del browser.

## 3. Che cosa vede il client

| Strumento | Cosa fa |
|---|---|
| `cerca`, `query`, `leggi` | letture: parole libere, linguaggio di query di Regia, un elemento per chiave |
| `wiki` | lettura della **wiki personale** di chi si collega: elenco delle pagine, una pagina per titolo, ricerca; mai la wiki degli altri |
| `crea`, `aggiorna`, `collega`, `commenta`, `registra_ore` | **non scrivono**: preparano una proposta con un identificativo |
| `proposte` | le proposte ancora da decidere |
| `conferma` | applica una proposta, con i permessi di adesso |
| `rifiuta` | scarta una proposta |

Le risposte citano gli elementi con la chiave e il link (`https://regia.example.org/e/DEM-12`). Il testo degli elementi arriva delimitato come **dato**: il server dice al modello di non seguirlo come istruzione.

**Le modifiche passano sempre dalla persona.** Il client mostra la proposta; solo dopo il tuo sì chiama `conferma`. `conferma` è segnato come strumento che cambia i dati, quindi i client che chiedono il permesso prima di uno strumento (Claude Desktop, Claude Code) lo chiedono anche a te. Le proposte non decise si cancellano dopo i giorni di conservazione dell'organizzazione.

Ogni chiamata finisce nel registro (**Configurazione → Consumo dell'AI**) con la funzione «Server MCP» e il fornitore «client», senza token né costo.

## 4. Claude Code

Con il comando, per te sola (`--scope user`) o per il progetto (`--scope project`, scrive `.mcp.json`):

```bash
claude mcp add --transport http regia https://regia.example.org/api/v1/mcp --scope user --header "Authorization: Bearer ${REGIA_TOKEN}"
```

Le virgolette doppie fanno espandere `REGIA_TOKEN` alla shell: imposta prima la variabile (`export REGIA_TOKEN=<token>`).

Oppure nel file `.mcp.json` alla radice del progetto, che si può condividere perché il token resta nell'ambiente:

```json
{
  "mcpServers": {
    "regia": {
      "type": "http",
      "url": "https://regia.example.org/api/v1/mcp",
      "headers": {
        "Authorization": "Bearer ${REGIA_TOKEN}"
      }
    }
  }
}
```

`"type": "http"` serve. Claude Code espande `${REGIA_TOKEN}` dall'ambiente; se la variabile manca, `claude mcp list` e `/mcp` lo segnalano. Verifica con `/mcp` dentro Claude Code.

## 5. Claude Desktop

Claude Desktop collega i server remoti in due modi.

**Connettore personalizzato** (Impostazioni → Connettori → Aggiungi connettore personalizzato), con l'indirizzo del server. L'intestazione con il token si può inserire solo se nel dialogo compare la sezione **Request headers**, che oggi è in beta e disponibile solo per alcune organizzazioni: in quel caso scrivi `Authorization` come nome e `Bearer <token>` come valore. Il connettore passa dal cloud di Anthropic, quindi Regia deve essere raggiungibile da Internet (e dagli indirizzi `160.79.104.0/21`, se c'è un filtro in ingresso). Il server MCP di Regia non offre ancora l'accesso con OAuth.

**Con `mcp-remote`**, un programma di terze parti (non di Anthropic né di Regia) che Claude Desktop avvia in locale e che inoltra al server remoto con l'intestazione. Serve Node.js. Nel file `claude_desktop_config.json`:

- macOS: `~/Library/Application Support/Claude/claude_desktop_config.json`
- Windows: `%APPDATA%\Claude\claude_desktop_config.json`

```json
{
  "mcpServers": {
    "regia": {
      "command": "npx",
      "args": [
        "mcp-remote",
        "https://regia.example.org/api/v1/mcp",
        "--header",
        "Authorization:${AUTH_HEADER}"
      ],
      "env": {
        "AUTH_HEADER": "Bearer <token>"
      }
    }
  }
}
```

La forma `Authorization:${AUTH_HEADER}`, senza spazio, evita un problema noto di Claude Desktop su Windows con gli spazi negli argomenti. Il token sta nel file in chiaro: tieni il file solo sul tuo computer. Dopo ogni modifica chiudi e riapri Claude Desktop del tutto.

## 6. VS Code

Nel file `.vscode/mcp.json` del progetto; VS Code chiede il token al primo avvio e lo conserva in modo sicuro:

```json
{
  "inputs": [
    {
      "type": "promptString",
      "id": "regia-token",
      "description": "Token API di Regia",
      "password": true
    }
  ],
  "servers": {
    "regia": {
      "type": "http",
      "url": "https://regia.example.org/api/v1/mcp",
      "headers": {
        "Authorization": "Bearer ${input:regia-token}"
      }
    }
  }
}
```

Le versioni recenti di VS Code introducono anche il formato `mcpServers` in `.mcp.json` alla radice del progetto: controlla la documentazione di VS Code per la tua versione.

## 7. Cursor

In `~/.cursor/mcp.json` (per tutti i progetti) o `.cursor/mcp.json` (per uno):

```json
{
  "mcpServers": {
    "regia": {
      "url": "https://regia.example.org/api/v1/mcp",
      "headers": {
        "Authorization": "Bearer ${env:REGIA_TOKEN}"
      }
    }
  }
}
```

Imposta `REGIA_TOKEN` nel profilo della shell o nell'ambiente del sistema.

## 8. Se non funziona

| Risposta | Causa |
|---|---|
| 401 `auth.token_required` | manca l'intestazione `Authorization: Bearer …` (o il client manda la sessione del browser) |
| 401 `auth.invalid_token` | token sbagliato, scaduto o revocato |
| 403 `ai.mcp_disabled` | chi amministra non ha acceso il server MCP |
| mancano gli strumenti che scrivono | il token è di sola lettura |
| «elemento inesistente o non visibile» | con i tuoi permessi non lo vedi, come nell'interfaccia |

Il server usa il trasporto HTTP «streamable» dell'MCP, senza sessioni. Un proxy inverso davanti a Regia deve lasciar passare l'intestazione `Authorization` e le richieste POST su `/api/v1/mcp`.
