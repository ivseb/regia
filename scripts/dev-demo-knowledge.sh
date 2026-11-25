#!/usr/bin/env bash
# Documenti di esempio nel tenant "demo" (piano, passo 19; solo sviluppo e prova): spazio
# «Documentazione IT» con il runbook del Portale B2B, un ADR collegato alla richiesta B2B, una
# policy di sicurezza controllata e approvata (con la versione successiva in lavorazione), un
# verbale con una menzione, qualche versione; spazio riservato «Sicurezza operativa».
# Rifiniture: commenti in linea con risposte, un thread risolto e uno orfano, una campagna di
# lettura sulla policy con una conferma, e l'import dell'export dello spazio in uno spazio nuovo.
# Uso: scripts/dev-demo-knowledge.sh [url]  — dopo dev-demo-data.sh (richieste e applicazioni).
set -euo pipefail
B=${1:-http://127.0.0.1:8080}; T=$(mktemp -d)
PW=${REGIA_DEV_PASSWORD:-}   # sull'istanza di prova la password è quella dell'installazione
[ -n "$PW" ] || eval "$(grep -m1 '^PW=' "$(dirname "$0")/dev-seed.sh")"   # altrimenti quella di sviluppo del seed
login() { curl -s -c $T/$1 -H 'Content-Type: application/json' -d "{\"email\":\"$1@demo.test\",\"password\":\"$PW\"}" $B/api/v1/auth/login > $T/$1.me; }
call() { local who=$1 m=$2 p=$3 body=${4:-} o=$T/.risposta c; if [ -n "$body" ]; then c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m -H 'Content-Type: application/json' -d "$body" "$B$p"); else c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m "$B$p"); fi; [ "$c" -lt 400 ] 2>/dev/null || echo "ATTENZIONE: $m $p ha risposto HTTP $c: $(head -c 300 $o)" >&2; cat $o; }
py() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
for u in admin arch dm sicurezza compliance dev; do login $u; done
DEMO=$(py 'next(t["tenant_id"] for t in d["tenants"] if t["slug"]=="demo")' < $T/admin.me)
call admin POST /api/v1/me/tenant "{\"tenant_id\":\"$DEMO\"}" > /dev/null
me() { py 'd["active_tenant"]["member_id"]' < $T/$1.me; }
SEC=$(me sicurezza); COMP=$(me compliance)
q() { python3 -c 'import urllib.parse,sys; print(urllib.parse.quote(sys.argv[1]))' "$1"; }

# Export dello spazio e import in uno spazio nuovo (pagine, sottopagine e collegamenti).
plus_import() {
  if ! call admin GET /api/v1/kb/spaces/IMP | grep -q '"space"'; then
    call admin POST /api/v1/kb/spaces '{"key":"IMP","name":"Archivio importato","description":"Pagine importate da uno zip di Markdown"}' > /dev/null
    curl -s -b $T/admin "$B/api/v1/kb/spaces/DOC/export" -o $T/doc.zip
    curl -s -b $T/admin -F file=@$T/doc.zip "$B/api/v1/kb/spaces/IMP/import" | py '"Import in IMP: " + str(d["pages"]) + " pagine, " + str(d["attachments"]) + " allegati"'
  fi
}

# Rifiniture: commenti in linea, conferma di lettura, export e import (idempotente).
plus() {
  keyof() { call arch GET /api/v1/kb/spaces/DOC | py "next(p['key'] for p in d['pages'] if p['title']=='$1')"; }
  local RB POL VERB
  RB=$(keyof "Runbook del Portale B2B"); POL=$(keyof "Policy di sicurezza delle applicazioni web"); VERB=$(keyof "Verbale di avvio del Portale B2B")
  if [ "$(call arch GET /api/v1/kb/pages/$RB/threads | py 'len(d["items"])')" != "0" ]; then echo "Commenti e letture di esempio già presenti."; plus_import; return; fi
  # Commenti in linea sul runbook: una discussione con risposte e una menzione, una risolta.
  local T1 T2
  T1=$(call dev POST /api/v1/kb/pages/$RB/threads '{"quote":"svuotare la cache delle sessioni","prefix":"rilascio: ","suffix":", poi riavviare un nodo","body":"Su quale nodo? Serve farlo anche sui nodi in manutenzione?"}' | py 'd["items"][0]["id"]')
  call arch POST /api/v1/kb/threads/$T1/comments '{"body":"Solo sui nodi attivi. @Anna Moretti aggiungo una riga al runbook."}' > /dev/null
  T2=$(call dm POST /api/v1/kb/pages/$RB/threads '{"quote":"coda delle notifiche sotto le 500 voci","body":"La soglia di 500 è ancora giusta dopo ultimo rilascio?"}' | py 'd["items"][0]["id"]')
  call arch POST /api/v1/kb/threads/$T2/comments '{"body":"Sì, confermata dal team Integrazioni."}' > /dev/null
  call dm POST /api/v1/kb/threads/$T2/resolve > /dev/null
  # Thread orfano: la frase commentata viene poi riscritta nel verbale.
  call dev POST /api/v1/kb/pages/$VERB/threads '{"quote":"Si procede con l'\''accesso SPID e CIE","body":"Va citato anche il piano B se SPID non risponde."}' > /dev/null
  call arch PUT /api/v1/kb/pages/$VERB "$(call arch GET /api/v1/kb/pages/$VERB | python3 -c 'import json,sys; d=json.load(sys.stdin)["page"]; print(json.dumps({"body": d["body"].replace("Si procede con l\x27accesso SPID e CIE", "L\x27accesso passa da SPID e CIE, con ADR dedicato"), "note": "Frase riformulata", "base_version": d["version"]}))')" > /dev/null
  # Commento sulla policy approvata.
  call dev POST /api/v1/kb/pages/$POL/threads '{"quote":"vulnerabilità critiche corrette entro 7 giorni","body":"Vale anche per le dipendenze di terze parti o solo per il codice nostro?"}' > /dev/null
  # Campagna di lettura sulla policy: persone e gruppo Sicurezza, scadenza tra due settimane.
  local DEV DM GRP CAMP DUE
  DEV=$(me dev); DM=$(me dm)
  GRP=$(call admin GET /api/v1/groups | py 'next(g["id"] for g in d["items"] if g["name"]=="Sicurezza")')
  DUE=$(date -v+14d +%F 2>/dev/null || date -d '+14 days' +%F)
  CAMP=$(call arch POST /api/v1/kb/pages/$POL/readings "{\"name\":\"Presa visione della policy web 2026\",\"due_date\":\"$DUE\",\"audience\":[{\"kind\":\"member\",\"id\":\"$DEV\"},{\"kind\":\"member\",\"id\":\"$DM\"},{\"kind\":\"group\",\"id\":\"$GRP\"}]}" | py 'd["items"][0]["id"]')
  call sicurezza POST /api/v1/kb/readings/$CAMP/confirm "{\"version\":$(call arch GET /api/v1/kb/pages/$POL | py 'd["page"]["approved_version"]')}" > /dev/null
  echo "Commenti in linea su $RB, $VERB e $POL; campagna di lettura su $POL (una conferma)."
  plus_import
}

if call arch GET /api/v1/kb/spaces/DOC | grep -q '"space"'; then echo "Documenti di esempio già presenti."; plus; rm -rf $T; exit 0; fi

# Chiavi degli elementi da citare: la richiesta B2B e le applicazioni dell'area riservata.
B2B=$(call dm GET "/api/v1/elements?q=$(q 'tipo: business_request portale clienti B2B')" | py 'd["items"][0]["key"]')
call arch GET /api/v1/architecture/applications | py '"\n".join(a["code"]+"|"+a["key"] for a in d["items"])' > $T/apps
app() { grep "^$1|" $T/apps | cut -d'|' -f2; }
WEB=$(app WEB-AR); API=$(app API-CLI); IAM=$(app IAM)

# json TITOLO [CAMPO=VALORE…] < testo: corpo JSON con il testo da stdin in "body".
json() { python3 -c '
import json,sys
d={"title":sys.argv[1]} if sys.argv[1] else {}
for kv in sys.argv[2:]:
    k,v=kv.split("=",1)
    d[k]=int(v) if v.isdigit() else v
b=sys.stdin.read()
if b.strip(): d["body"]=b.rstrip()
print(json.dumps(d))' "$@"; }
page() { local who=$1 title=$2; shift 2; call $who POST /api/v1/kb/spaces/${SPACE}/pages "$(json "$title" "$@")" | py 'd["page"]["key"]'; }
version() { call $1 GET /api/v1/kb/pages/$2 | py 'd["page"]["version"]'; }
save() { local who=$1 key=$2 note=$3; call $who PUT /api/v1/kb/pages/$key "$(json "" "note=$note" "base_version=$(version $who $key)")" | py '"  " + d["page"]["key"] + " v" + str(d["page"]["version"])'; }

# --- Spazio «Documentazione IT» ------------------------------------------------------------
call arch POST /api/v1/kb/spaces '{"key":"DOC","name":"Documentazione IT","description":"Runbook, decisioni di architettura, policy e verbali dei sistemi IT"}' | py '"Spazio " + d["key"]'
SPACE=DOC
B2BP=$(page arch "Portale clienti B2B" </dev/null)
save arch $B2BP "Panoramica" <<EOF
Area riservata per i clienti business: contratti, fatture, referenti. La richiesta è $B2B.

| componente | applicazione | team |
|---|---|---|
| Frontend | $WEB | Web |
| API clienti | $API | Integrazioni |
| Identità | $IAM | Identity |

> [!NOTA] Le pagine figlie raccolgono runbook, decisioni e guide del portale.
EOF

RB=$(page arch "Runbook del Portale B2B" parent=$B2BP template=runbook </dev/null)
save arch $RB "Prima stesura del runbook" <<EOF
> [!NOTA] Servizio, responsabile e reperibilità sono all'inizio: chi apre il runbook durante un incidente deve trovarli subito.

| | |
|---|---|
| Servizio | Portale clienti B2B ($WEB, $API) |
| Responsabile | Anna Moretti |
| Reperibilità | turno Web, 08-20 nei giorni lavorativi |

## Panoramica
Il frontend $WEB chiama le API $API; l'accesso passa da $IAM con SPID e CIE.

## Avvio e arresto
1. Fermare il bilanciatore per il nodo da aggiornare.
2. Riavviare il servizio \`portale-b2b\` e attendere il controllo di salute.
3. Rimettere il nodo nel bilanciatore.

## Controlli di salute
- \`GET /healthz\` risponde 200 in meno di 300 ms
- coda delle notifiche sotto le 500 voci

## Incidenti tipici
### Accesso con SPID non disponibile
**Causa probabile:** certificato del service provider scaduto.

**Rimedio:**
\`\`\`
regia-iam cert status --sp portale-b2b
\`\`\`

> [!ATTENZIONE] Il rinnovo del certificato va concordato con il team Identity: interrompe gli accessi per circa due minuti.
EOF
save dev $RB "Riavvio dopo il rilascio" <<EOF
$(call dev GET /api/v1/kb/pages/$RB | py 'd["page"]["body"]')

## Riavvio dopo un rilascio
Procedura di riavvio del Portale B2B dopo un rilascio: svuotare la cache delle sessioni, poi riavviare un nodo alla volta.
EOF
for k in $WEB $API; do call arch POST /api/v1/kb/pages/$RB/links "{\"element\":\"$k\"}" >/dev/null; done

DEC=$(page arch "Decisioni di architettura" </dev/null)
ADR=$(page arch "ADR-001 Autenticazione dei clienti con SPID e CIE" parent=$DEC template=adr </dev/null)
save arch $ADR "Decisione accettata" <<EOF
| | |
|---|---|
| Stato | Accettata |
| Autore | Elena Conti |
| Richiesta | $B2B |

## Contesto
I clienti business del portale ($B2B) devono accedere con identità digitali riconosciute. Il portale non deve gestire password proprie.

## Decisione
L'accesso passa dal sistema di identità $IAM, che fa da service provider SPID e CIE; il portale riceve un token OIDC.

## Alternative considerate
| alternativa | pro | contro |
|---|---|---|
| Credenziali proprie del portale | semplice da avviare | password da custodire, nessuna identità verificata |
| SPID e CIE tramite $IAM | identità verificate, un solo punto di controllo | dipendenza da $IAM |

## Conseguenze
- $WEB delega l'accesso a $IAM
- [x] valutazione di sicurezza
- [ ] test di carico dell'accesso
EOF
call arch POST /api/v1/kb/pages/$ADR/links "{\"element\":\"$B2B\"}" >/dev/null

# --- Policy controllata: revisione di Giulia Sala, approvazione di Andrea Fontana ------------
POLS=$(page arch "Policy" </dev/null)
POL=$(page arch "Policy di sicurezza delle applicazioni web" parent=$POLS template=policy </dev/null)
call arch PUT /api/v1/kb/pages/$POL/control "{\"controlled\":true,\"review_months\":12,\"reviewers\":[\"$SEC\"],\"approvers\":[\"$COMP\"]}" >/dev/null
save arch $POL "Testo da sottoporre a revisione" <<'EOF'
> [!NOTA] Documento controllato: vale la versione approvata. Le modifiche passano da revisori e approvatori.

## Scopo
Stabilire i requisiti minimi di sicurezza per le applicazioni web esposte su internet.

## Ambito di applicazione
Tutte le applicazioni web dell'organizzazione e dei fornitori che trattano dati di clienti.

## Riferimenti normativi
- DORA, art. 9 (protezione e prevenzione)
- ISO/IEC 27001:2022, controlli A.8.25-A.8.28
- NIS2, art. 21

## Regole
1. Accesso dei clienti solo con identità verificate (SPID, CIE o federazione aziendale).
2. Cifratura TLS 1.2 o superiore per ogni comunicazione.
3. Analisi statica e delle dipendenze a ogni rilascio; vulnerabilità critiche corrette entro 7 giorni.
4. Registrazione degli accessi conservata per 12 mesi.

## Ruoli e responsabilità
| ruolo | responsabilità |
|---|---|
| Responsabile dell'applicazione | applica la policy e ne documenta le eccezioni |
| Sicurezza | verifica e rivede la policy ogni anno |
| Compliance | approva la policy e le deroghe |

## Eccezioni
Le deroghe si chiedono con una richiesta di deroga sul requisito, con rischio residuo e scadenza.
EOF
POLID=$(call arch GET /api/v1/kb/pages/$POL | py 'd["page"]["id"]')
call arch POST /api/v1/elements/$POLID/transitions '{"to":"In revisione"}' >/dev/null
for who in sicurezza compliance; do
  STEP=$(call $who GET /api/v1/approvals/inbox | py "next(i['step_id'] for i in d['items'] if i['element_key']=='$POL')")
  call $who POST /api/v1/approvals/steps/$STEP/decision '{"decision":"approved"}' >/dev/null
done
echo "  $POL approvata: $(call arch GET /api/v1/kb/pages/$POL | py 'd["page"]["state"] + " v" + str(d["page"]["approved_version"])')"
# Si lavora alla versione successiva: la v approvata resta in vigore e consultabile.
save arch $POL "Aggiunta la regola sulle intestazioni di sicurezza" <<EOF
$(call arch GET /api/v1/kb/pages/$POL | py 'd["page"]["body"].replace("conservata per 12 mesi.", "conservata per 12 mesi.\n5. Intestazioni di sicurezza (CSP, HSTS) obbligatorie su tutte le pagine.")')
EOF

GUIDE=$(page arch "Guida all'accesso dei clienti" parent=$B2BP template=guida </dev/null)
VERB=$(page arch "Verbale di avvio del Portale B2B" parent=$B2BP template=verbale </dev/null)
save arch $VERB "Verbale approvato in riunione" <<EOF
| | |
|---|---|
| Data | 18/11/2026 |
| Redatto da | Elena Conti |
| Partecipanti | Sara Villa, Paolo Riva, Anna Moretti, Giulia Sala |

## Decisioni
- Si procede con l'accesso SPID e CIE (vedi ADR-001).
- @Sara Villa aggiorna la stima di $B2B entro venerdì.

## Azioni
| azione | responsabile | scadenza |
|---|---|---|
| Stima aggiornata | Sara Villa | 27/11/2026 |
| Test di carico dell'accesso | Anna Moretti | 15/12/2026 |
EOF

# --- Spazio riservato: solo il gruppo Sicurezza --------------------------------------------
call admin POST /api/v1/kb/spaces '{"key":"SOC","name":"Sicurezza operativa","description":"Procedure riservate del gruppo Sicurezza","access":"restricted"}' | py '"Spazio " + d["key"] + " (riservato)"'
GRP=$(call admin GET /api/v1/groups | py 'next(g["id"] for g in d["items"] if g["name"]=="Sicurezza")')
call admin POST /api/v1/kb/spaces/SOC/access "{\"role\":\"editor\",\"group_id\":\"$GRP\"}" >/dev/null
SPACE=SOC
IR=$(page admin "Piano di risposta agli incidenti" </dev/null)
save admin $IR "Prima versione" <<EOF
> [!PERICOLO] Documento riservato al gruppo Sicurezza.

1. Classificare l'incidente secondo i criteri DORA e NIS2.
2. Avvisare il CISO e il DPO entro un'ora.
3. Aprire l'incidente e collegarlo alle applicazioni coinvolte ($API).
EOF
plus
rm -rf $T
echo "Documenti di esempio creati: DOC ($RB runbook, $ADR ADR, $POL policy), SOC (riservato)."
