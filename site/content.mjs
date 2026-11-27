// Testi del sito in italiano e in inglese. Le funzionalità elencate sono quelle descritte
// nei documenti di docs/ e presenti nel codice: niente promesse che il prodotto non mantiene.
// Nelle stringhe `x` diventa <code>x</code>; il resto è testo semplice (si esegue l'escape).

export const REPO = "https://github.com/ivseb/regia";

export const it = {
  lang: "it",
  name: "Regia",
  htmlLang: "it",
  nav: { features: "Funzionalità", install: "Installazione", docs: "Documentazione", code: "Codice" },
  skip: "Salta al contenuto",
  langLabel: "Lingua",
  menu: "Menu",
  tagline: "Piattaforma enterprise per demand, delivery e conformità",
  description:
    "Regia è una piattaforma open source per demand e portafoglio, SDLC con stage-gate, work management, knowledge, DevSecOps, GRC e architettura enterprise. Un solo binario Go con Postgres incluso.",
  modules: {
    label: "Funzionalità per modulo",
    title: "Nove aree, un solo prodotto",
    lead: "Ogni modulo si può accendere o spegnere per organizzazione.",
    items: [
      {
        name: "Demand e portafoglio",
        sub: "SDLC con stage-gate",
        points: [
          "Portale per le richieste con catalogo, modulo visuale e segnalazione delle richieste simili.",
          "Processi configurabili e versionati, con fasi, approvazioni e regole «Quando… solo se… allora…».",
          "Valutazioni con questionari, scorecard con punteggio 0-100 e graduatoria.",
          "Organi di governo, cabina di portafoglio con scenari, pacchetto della seduta e verbale.",
          "Piano automatico, roadmap, carico dei team, status report e cruscotto.",
        ],
      },
      {
        name: "Progetti",
        sub: "Piano, baseline, fornitori",
        points: [
          "Progetto sopra le epiche, con work package consegnati in agile, a cascata o da un fornitore.",
          "Piano con Gantt, milestone, dipendenze anche tra progetti e baseline con tolleranze.",
          "Change request che nascono da sole quando date, budget o perimetro escono dalla tolleranza.",
          "Fornitori con SAL e deliverable; avviso quando due progetti toccano la stessa applicazione.",
          "Programmi con roadmap, mappa dei semafori del portafoglio, budget e status report.",
        ],
      },
      {
        name: "Work management",
        sub: "Board, sprint, timesheet",
        points: [
          "Board per team (Scrum, Kanban, Supporto) con colonne su più fasi, limiti WIP e corsie.",
          "Epic, story, task e bug, con linguaggio di query, viste salvate e «Da fare».",
          "Timesheet settimanale inviato come richiesta di approvazione, con deleghe e notifiche.",
          "Commenti, allegati, notifiche (anche per email) e aggiornamenti in tempo reale.",
          "Import da Jira, YouTrack e CSV con mappatura, anteprima e annullamento.",
        ],
      },
      {
        name: "Budget gerarchico",
        sub: "Dal plafond al progetto",
        points: [
          "Unità organizzative, plafond, stanziamenti e trasferimenti tra i nodi dell'albero.",
          "Politica di fine periodo e periodi chiusi; importi in centesimi nella valuta del tenant.",
          "Chi vede cosa lo decide il database (RLS): finanza, responsabili d'unità, PM e sponsor.",
          "«Attinge da» sul budget di un progetto e costo delle ore con le tariffe, solo per la finanza.",
        ],
      },
      {
        name: "Knowledge",
        sub: "Documenti controllati",
        points: [
          "Spazi aperti o riservati, pagine con versioni e confronto tra versioni.",
          "Editor a blocchi sopra il Markdown, con ricerca e collegamenti agli oggetti di Regia.",
          "Modelli di partenza: runbook, ADR, policy, verbale e guida.",
          "Documenti controllati con revisione e approvazione, promemoria delle revisioni periodiche.",
        ],
      },
      {
        name: "DevSecOps",
        sub: "GitHub, GitLab, Gitea",
        points: [
          "Connettori per GitHub (anche Enterprise Server), GitLab e Gitea, più un'API di push per qualunque pipeline.",
          "Servizi, ambienti, rilasci e deployment collegati alle applicazioni e alle richieste.",
          "SBOM CycloneDX e SPDX, vulnerabilità deduplicate per servizio e componente.",
          "Gate di rilascio, politiche della pipeline con adozione guidata (una pull request, mai il branch principale).",
          "Le quattro metriche DORA. Regia legge ciò che la pipeline produce: non esegue build né scansioni.",
        ],
      },
      {
        name: "GRC",
        sub: "Rischi, conformità, crisi",
        points: [
          "Registro dei rischi con matrici, trattamento e accettazione; fornitori ICT e incidenti.",
          "Cataloghi normativi caricati come dati (DORA, NIS2, ISO 27001, GDPR, AgID), segnati «da verificare» finché un amministratore non li conferma.",
          "Controlli vivi: efficacia proposta dal sistema, evidenze automatiche, mappatura guidata.",
          "Vista DPO (registro dei trattamenti, DPIA, violazioni con le 72 ore) e spazio dell'auditor con campionamento e richieste di evidenza.",
        ],
      },
      {
        name: "Architettura enterprise",
        sub: "Capacità e applicazioni",
        points: [
          "Capability, applicazioni, tecnologie e dipendenze come grafo navigabile.",
          "Analisi d'impatto in avanti e all'indietro, con esportazione in CSV.",
          "Ciclo di vita e razionalizzazione del parco applicativo; diagrammi C4.",
          "Gli stessi oggetti compaiono nelle richieste, nei rischi, nei controlli e nei rilasci.",
        ],
      },
      {
        name: "Piattaforma",
        sub: "Per tutti i moduli",
        points: [
          "Multi-tenant: più organizzazioni su una sola installazione, isolate dalla RLS di Postgres.",
          "Accesso unico OIDC (PKCE), MFA, token API con permessi, webhook e registro di audit.",
          "Permessi per ruoli e gruppi; primo avvio guidato con modelli per tipo di azienda.",
          "Interfaccia in italiano e inglese, temi chiari e scuri, contrasto elevato e navigazione da tastiera.",
          "AI opzionale: ogni funzione lavora anche senza.",
        ],
      },
    ],
  },
  install: {
    title: "Installazione",
    description: "Come installare Regia con Docker o dal sorgente: porta 8080, volume dati, controllo di salute e primo accesso.",
    label: "Guida rapida",
    lead: "Regia è un solo programma. Si avvia con Docker oppure compilandolo dal sorgente, e porta con sé il proprio Postgres: non serve installare nient'altro.",
    docker: {
      title: "Con Docker",
      intro: "L'immagine contiene il binario e Postgres 18, su Alpine, e gira con un utente non privilegiato. Si costruisce dal repository:",
      build: "Poi si avvia con un volume per i dati:",
      run: "Cosa significano le opzioni:",
      opts: [
        "`-p 8080:8080`: l'interfaccia e l'API ascoltano sulla porta 8080.",
        "`-v regia-data:/data`: il volume con Postgres, le chiavi e gli allegati. Senza volume i dati vanno persi con il contenitore.",
        "`--stop-timeout 60`: all'arresto Regia chiude le richieste (fino a 20 s) e poi ferma Postgres (fino a 20 s); il tempo predefinito di Docker, 10 s, può interromperlo.",
      ],
      health: "Controllo di salute",
      healthText: "Il primo avvio fa `initdb` e applica le migrazioni, quindi può servire un po' di tempo. Quando è pronto risponde:",
      pgvector: "L'immagine include pgvector, per la ricerca semantica. Con `--build-arg PGVECTOR=false` si ottiene una versione senza: lo schema funziona lo stesso e la ricerca semantica resta spenta.",
    },
    binary: {
      title: "Dal sorgente (binario)",
      intro: "Servono Go, Node 24 con pnpm e i binari di Postgres (versione 16 o successiva; il pacchetto Docker usa la 18) sul computer.",
      build: "Si compila prima l'interfaccia e poi il binario:",
      run: "E si avvia:",
      pg: "Postgres incluso",
      pgText:
        "Regia non porta Postgres dentro l'eseguibile: al primo avvio cerca i programmi `initdb` e `postgres` nel `PATH`, oppure nella cartella indicata da `REGIA_PG_BIN`. Con quelli crea il cluster in `<dati>/pg` e lo avvia come processo figlio, solo su un socket unix e senza porte di rete. Al termine lo ferma. Se hai un Postgres già installato altrove, indica la sua cartella dei binari:",
      external: "Con un Postgres esterno si usano invece `DATABASE_URL` e `REGIA_APP_DATABASE_URL`: in quel caso Regia non lo gestisce.",
    },
    first: {
      title: "Primo accesso",
      text: "Si crea la prima organizzazione e il suo amministratore dalla riga di comando. La password non si passa mai come argomento: si imposta con la variabile `REGIA_ADMIN_PASSWORD`, oppure la chiede il comando.",
      docker: "Con Docker:",
      binary: "Dal binario:",
      after: "Poi si entra su http://localhost:8080 con l'email dell'amministratore. Il comando `regia platform-admin grant` assegna il ruolo di superamministratore dell'istanza, che crea le organizzazioni dall'interfaccia.",
    },
    next: {
      title: "E dopo",
      items: [
        ["Esercizio, backup e aggiornamenti", "operativo", "`regia backup`, `regia restore`, `regia doctor`, rotazione delle chiavi, aggiornamento."],
        ["Variabili d'ambiente", "sviluppo/esercizio", "Tutte le variabili `REGIA_*`: indirizzo, cartella dati, SMTP, OIDC, backup."],
        ["Importare da Jira, YouTrack e CSV", "import", "Esportare, mappare, provare e importare."],
        ["Connettori DevSecOps", "devsecops-github", "GitHub, GitLab, Gitea e l'API di push."],
      ],
      docsNote: "La documentazione è in italiano.",
    },
    systemd: {
      title: "In produzione senza Docker",
      text: "Nel repository, in `deploy/systemd/`, ci sono le unit di esempio `regia.service`, `regia-backup.service` e `regia-backup.timer`: un servizio con l'utente `regia`, i dati in `/var/lib/regia`, i binari in `/opt/regia` e un backup notturno in `/var/backups/regia`. La procedura completa è nella guida di esercizio.",
    },
  },
  docsIndex: {
    title: "Documentazione",
    description: "Guide per installare, gestire e collegare Regia, e note tecniche per chi sviluppa.",
    lead: "La documentazione è scritta nel repository, accanto al codice, e questo sito la mostra così com'è.",
    guides: "Guide",
    guidesText: "Per chi installa, gestisce e collega Regia.",
    dev: "Per sviluppatori",
    devText: "Come è fatto il codice, modulo per modulo.",
    onThisPage: "In questa pagina",
    source: "Sorgente su GitHub",
    nav: "Documentazione",
    all: "Tutta la documentazione",
  },
  notFound: { title: "Pagina non trovata", text: "La pagina che cerchi non esiste.", home: "Torna alla home" },
};

export const en = {
  lang: "en",
  name: "Regia",
  htmlLang: "en",
  nav: { features: "Features", install: "Installation", docs: "Documentation", code: "Code" },
  skip: "Skip to content",
  langLabel: "Language",
  menu: "Menu",
  tagline: "Enterprise platform for demand, delivery and compliance",
  description:
    "Regia is an open source platform for demand and portfolio management, stage-gate SDLC, work management, knowledge, DevSecOps, GRC and enterprise architecture. One Go binary with Postgres included.",
  modules: {
    label: "Features by module",
    title: "Nine areas, one product",
    lead: "Each module can be switched on or off per organisation.",
    items: [
      {
        name: "Demand and portfolio",
        sub: "Stage-gate SDLC",
        points: [
          "Request portal with catalogue, visual form and detection of similar requests.",
          "Configurable, versioned processes with phases, approvals and «When… only if… then…» rules.",
          "Assessments with questionnaires, scorecards scoring 0-100 and a ranking.",
          "Governance bodies, a portfolio cockpit with scenarios, meeting pack and minutes.",
          "Automatic plan, roadmap, team load, status reports and dashboard.",
        ],
      },
      {
        name: "Projects",
        sub: "Plan, baseline, suppliers",
        points: [
          "Projects above epics, with work packages delivered agile, waterfall or by a supplier.",
          "Plan with Gantt, milestones, dependencies across projects and a baseline with tolerances.",
          "Change requests that open by themselves when dates, budget or scope leave the tolerance.",
          "Suppliers with progress claims and deliverables; a warning when two projects touch the same application.",
          "Programmes with a roadmap, portfolio traffic-light map, budget and status reports.",
        ],
      },
      {
        name: "Work management",
        sub: "Boards, sprints, timesheets",
        points: [
          "Team boards (Scrum, Kanban, Support) with multi-phase columns, WIP limits and swimlanes.",
          "Epics, stories, tasks and bugs, with a query language, saved views and «To do».",
          "Weekly timesheet submitted as an approval request, with delegation and notifications.",
          "Comments, attachments, notifications (also by email) and real-time updates.",
          "Import from Jira, YouTrack and CSV with mapping, preview and undo.",
        ],
      },
      {
        name: "Hierarchical budget",
        sub: "From the ceiling to the project",
        points: [
          "Organisational units, ceiling, allocations and transfers between the nodes of the tree.",
          "End-of-period policy and closed periods; amounts in cents in the tenant's currency.",
          "Who sees what is decided by the database (RLS): finance, unit managers, PMs and sponsors.",
          "«Draws from» on a project budget and cost of hours with rates, for finance only.",
        ],
      },
      {
        name: "Knowledge",
        sub: "Controlled documents",
        points: [
          "Open or restricted spaces, pages with versions and a diff between versions.",
          "Block editor on top of Markdown, with search and links to Regia objects.",
          "Starter templates: runbook, ADR, policy, minutes and guide.",
          "Controlled documents with review and approval, reminders for periodic reviews.",
        ],
      },
      {
        name: "DevSecOps",
        sub: "GitHub, GitLab, Gitea",
        points: [
          "Connectors for GitHub (also Enterprise Server), GitLab and Gitea, plus a push API for any pipeline.",
          "Services, environments, releases and deployments linked to applications and requests.",
          "CycloneDX and SPDX SBOMs, vulnerabilities deduplicated by service and component.",
          "Release gate, pipeline policies with guided adoption (a pull request, never the main branch).",
          "The four DORA metrics. Regia reads what the pipeline already produces: it does not run builds or scans.",
        ],
      },
      {
        name: "GRC",
        sub: "Risk, compliance, crisis",
        points: [
          "Risk register with matrices, treatment and acceptance; ICT suppliers and incidents.",
          "Regulatory catalogues loaded as data (DORA, NIS2, ISO 27001, GDPR, AgID), marked «to be verified» until an administrator confirms them.",
          "Live controls: effectiveness proposed by the system, automatic evidence, guided mapping.",
          "DPO view (record of processing, DPIA, breaches with the 72 hours) and an auditor space with sampling and evidence requests.",
        ],
      },
      {
        name: "Enterprise architecture",
        sub: "Capabilities and applications",
        points: [
          "Capabilities, applications, technologies and dependencies as a navigable graph.",
          "Impact analysis in both directions, with CSV export.",
          "Lifecycle and rationalisation of the application landscape; C4 diagrams.",
          "The same objects appear in requests, risks, controls and releases.",
        ],
      },
      {
        name: "Platform",
        sub: "Across all modules",
        points: [
          "Multi-tenant: several organisations on one installation, isolated by Postgres RLS.",
          "Single sign-on with OIDC (PKCE), MFA, scoped API tokens, webhooks and an audit log.",
          "Permissions by roles and groups; guided first run with templates per type of company.",
          "Interface in Italian and English, light and dark themes, high contrast and keyboard navigation.",
          "Optional AI: every feature works without it.",
        ],
      },
    ],
  },
  install: {
    title: "Installation",
    description: "How to install Regia with Docker or from source: port 8080, data volume, health check and first login.",
    label: "Quick guide",
    lead: "Regia is a single program. Start it with Docker or build it from source; it brings its own Postgres, so there is nothing else to install.",
    docker: {
      title: "With Docker",
      intro: "The image contains the binary and Postgres 18, on Alpine, and runs as an unprivileged user. Build it from the repository:",
      build: "Then start it with a volume for the data:",
      run: "What the options mean:",
      opts: [
        "`-p 8080:8080`: the interface and the API listen on port 8080.",
        "`-v regia-data:/data`: the volume holding Postgres, keys and attachments. Without a volume the data is lost with the container.",
        "`--stop-timeout 60`: on shutdown Regia closes requests (up to 20 s) and then stops Postgres (up to 20 s); Docker's default of 10 s can interrupt it.",
      ],
      health: "Health check",
      healthText: "The first start runs `initdb` and the migrations, so it can take a little while. When it is ready it answers:",
      pgvector: "The image includes pgvector, for semantic search. With `--build-arg PGVECTOR=false` you get a version without it: the schema works anyway and semantic search stays off.",
    },
    binary: {
      title: "From source (binary)",
      intro: "You need Go, Node 24 with pnpm, and the Postgres binaries (version 16 or later; the Docker image uses 18) on the machine.",
      build: "Build the interface first, then the binary:",
      run: "And start it:",
      pg: "Included Postgres",
      pgText:
        "Regia does not embed Postgres in the executable: at first start it looks for the `initdb` and `postgres` programs in the `PATH`, or in the folder named by `REGIA_PG_BIN`. With those it creates the cluster in `<data>/pg` and starts it as a child process, on a unix socket only and with no network port. It stops it on exit. If Postgres is installed somewhere else, point to its binaries folder:",
      external: "With an external Postgres, use `DATABASE_URL` and `REGIA_APP_DATABASE_URL` instead: in that case Regia does not manage it.",
    },
    first: {
      title: "First login",
      text: "Create the first organisation and its administrator from the command line. The password is never an argument: set it with the `REGIA_ADMIN_PASSWORD` variable, or the command prompts for it.",
      docker: "With Docker:",
      binary: "From the binary:",
      after: "Then sign in at http://localhost:8080 with the administrator's email. The `regia platform-admin grant` command gives the instance super-administrator role, which creates organisations from the interface.",
    },
    next: {
      title: "What next",
      items: [
        ["Operations, backup and upgrades", "operativo", "`regia backup`, `regia restore`, `regia doctor`, key rotation, upgrading."],
        ["Environment variables", "sviluppo/esercizio", "All the `REGIA_*` variables: address, data folder, SMTP, OIDC, backup."],
        ["Importing from Jira, YouTrack and CSV", "import", "Export, map, preview and import."],
        ["DevSecOps connectors", "devsecops-github", "GitHub, GitLab, Gitea and the push API."],
      ],
      docsNote: "Documentation is currently in Italian.",
    },
    systemd: {
      title: "In production without Docker",
      text: "The repository has example units in `deploy/systemd/`: `regia.service`, `regia-backup.service` and `regia-backup.timer`: a service running as the `regia` user, data in `/var/lib/regia`, binaries in `/opt/regia` and a nightly backup in `/var/backups/regia`. The full procedure is in the operations guide.",
    },
  },
  docsIndex: {
    title: "Documentation",
    description: "Documentation is currently in Italian: guides to install, run and connect Regia, and technical notes for developers.",
    lead: "Documentation is currently in Italian.",
    leadMore: "It is written in the repository, next to the code, and this site shows it as it is. The pages below open the Italian version.",
    guides: "Guides",
    guidesText: "For those who install, run and connect Regia.",
    dev: "For developers",
    devText: "How the code is built, module by module.",
    onThisPage: "On this page",
    source: "Source on GitHub",
    nav: "Documentation",
    all: "All the documentation",
  },
  notFound: { title: "Page not found", text: "The page you are looking for does not exist.", home: "Back to home" },
};
