<p><img src="docs/logo.svg" alt="" width="56" height="56"></p>

# Regia

Piattaforma enterprise per governare il lavoro di un'azienda, dalla richiesta alla produzione: demand e portafoglio, SDLC con stage-gate, work management, Knowledge, DevSecOps, conformità (DORA, NIS2, ISO 27001, GDPR, AgID) e architettura enterprise.

**Un solo binario Go, che avvia e gestisce da sé il suo Postgres** (nell'immagine Docker c'è già Postgres 18). Multi-tenant con Row Level Security, interfaccia in italiano e inglese, AI facoltativa.

- **Sito e documentazione:** <https://ivseb.github.io/regia/>
- **Licenza:** [AGPL-3.0-only](LICENSE), © 2026 Ivan Sebben. Dettagli e componenti di terze parti: [docs/licenza.md](docs/licenza.md) e [NOTICE](NOTICE).

## Moduli

- **Demand e portafoglio:** richieste di business con processi versionati e stage-gate, valutazioni con questionari, approvazioni, graduatoria, cabina di portafoglio, budget gerarchico.
- **Work e delivery:** spazi di team, backlog, board, sprint, epic collegati alle richieste, timesheet con approvazioni.
- **Knowledge:** spazi di documentazione, pagine a blocchi, commenti in linea, conferme di lettura.
- **DevSecOps:** connettori GitHub, GitLab e Gitea, API per le pipeline, SBOM, vulnerabilità governate, politiche della pipeline, gate di rilascio.
- **Conformità e rischi:** cataloghi normativi, controlli, rischi ICT, fornitori, registro delle informazioni, incidenti, vista DPO.
- **Architettura enterprise:** capacità, applicazioni, dipendenze e diagrammi.

## Provarlo

Con Docker:

```bash
docker build -f deploy/Dockerfile -t regia .
docker run -d --name regia -p 8080:8080 -v regia-data:/data --stop-timeout 60 regia
```

Dal sorgente (servono Go, Node 24 con pnpm e i binari di Postgres ≥ 16, oppure `REGIA_PG_BIN`):

```bash
make web build   # frontend e binario in bin/regia
make seed        # organizzazioni di prova "demo" e "altro"
make run         # http://127.0.0.1:8080
```

Esercizio, backup, aggiornamenti e Docker: [docs/operativo.md](docs/operativo.md). Sviluppo e test: [docs/sviluppo.md](docs/sviluppo.md).

## Contributi

Per ora il repository **non accetta contributi di codice esterni**: tutto il codice resta di un solo titolare (motivo in [docs/licenza.md](docs/licenza.md)). Segnalazioni e idee sono benvenute nelle issue.

---

**English.** Regia is an enterprise platform for demand and portfolio management, stage-gate SDLC, work management, knowledge, DevSecOps, compliance (DORA, NIS2, ISO 27001, GDPR) and enterprise architecture. It ships as a single Go binary that runs and manages its own Postgres (bundled in the Docker image), is multi-tenant with row-level security, and has an Italian and English UI. Website: <https://ivseb.github.io/regia/>. Licensed under AGPL-3.0-only. The documentation is currently in Italian.
