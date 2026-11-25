#!/usr/bin/env bash
# Requisiti di esempio nel tenant "demo" (piano, passi 5 e 6; solo sviluppo e prova):
# baseline del catalogo applicate al portale B2B, un requisito di progetto inserito a mano,
# requisiti accettati, pronti per la verifica e verificati con evidenza, una deroga in vigore
# e una in approvazione. I requisiti nati dalle prescrizioni della security review li ha già
# creati l'esito di dev-demo-reviews.sh; quelli della richiesta DORA le regole in Valutazione.
# Uso: scripts/dev-demo-requirements.sh [url]  — dopo dev-demo-reviews.sh (lo richiama dev-demo-data.sh).
set -euo pipefail
B=${1:-http://127.0.0.1:8080}; PW=${REGIA_DEV_PASSWORD:-'prova-locale-2026!'}; T=$(mktemp -d)
login() { curl -s -c $T/$1 -H 'Content-Type: application/json' -d "{\"email\":\"$1@demo.test\",\"password\":\"$PW\"}" $B/api/v1/auth/login > $T/$1.me; }
call() { local who=$1 m=$2 p=$3 body=${4:-} o=$T/.risposta c; if [ -n "$body" ]; then c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m -H 'Content-Type: application/json' -d "$body" "$B$p"); else c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m "$B$p"); fi; [ "$c" -lt 400 ] 2>/dev/null || echo "ATTENZIONE: $m $p ha risposto HTTP $c: $(head -c 300 $o)" >&2; cat $o; }
py() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
USERS="admin dm sicurezza architetture"
for u in $USERS; do login $u; done
DEMO=$(py 'next(t["tenant_id"] for t in d["tenants"] if t["slug"]=="demo")' < $T/admin.me)
for u in $USERS; do call $u POST /api/v1/me/tenant "{\"tenant_id\":\"$DEMO\"}" > /dev/null; done
req_key() { call dm GET "/api/v1/elements?q=$(python3 -c 'import sys,urllib.parse; print(urllib.parse.quote("tipo: business_request " + sys.argv[1]))' "$1")" | py 'd["items"][0]["key"]'; }
impact_of() { call dm GET /api/v1/demand/requests/$1/analysis | py "next(i['key'] for i in d['impacts'] if i['application']=='$2')"; }
# Requisito di un impatto (o di progetto con impatto vuoto) per inizio del titolo: "chiave id".
req_of() { call dm GET /api/v1/elements/$1/requirements | py "next(r['key']+' '+r['id'] for r in d['rows'] if r['title'].startswith('''$2''') and (r.get('impact_key') or '')=='$3')"; }
move() { call admin POST /api/v1/elements/$1/transitions "{\"to\":\"$2\"${3:+,\"comment\":\"$3\"}}" > /dev/null; }
ready() { call admin POST /api/v1/requirements/$1/cells/$2/ready "{\"note\":\"$3\"}" > /dev/null; }
verify() { call $1 POST /api/v1/requirements/$2/cells/$3/verify "$4" > /dev/null; }

B2B=$(req_key "portale clienti B2B")
API=$(impact_of $B2B "Backend API clienti"); WEB=$(impact_of $B2B "Frontend area riservata")

# Il portale B2B è passato dalla Valutazione prima delle regole: le baseline si applicano a mano.
call dm POST /api/v1/elements/$B2B/requirements/baseline '{"catalog":"sicurezza_applicativa"}' > /dev/null
call dm POST /api/v1/elements/$B2B/requirements '{"title":"Manuale operativo del portale aggiornato","text":"Avvio, arresto, ripristino e contatti di reperibilità del portale B2B.","category":"Documentazione","scope":"project","verifier_team":"architetture","verification_criterion":"Manuale pubblicato e collegato al requisito"}' > /dev/null

# Backend API: TLS accettato, realizzato e verificato con evidenza; segreti pronti per la verifica.
read -r TLS TLSID <<<"$(req_of $B2B 'Cifratura dei dati in transito' $API)"
move $TLSID "Accettato"; move $TLSID "In realizzazione"
ready $TLS $API "TLS 1.3 sul bilanciatore e tra i servizi interni"
verify sicurezza $TLS $API '{"outcome":"verified","note":"Configurazione controllata sugli endpoint pubblici e interni.","links":["https://scanner.example.test/report/tls-b2b"]}'
read -r SEG SEGID <<<"$(req_of $B2B 'Segreti fuori dal codice sorgente' $API)"
move $SEGID "Accettato"
ready $SEG $API "Segreti spostati nel vault, rotazione mensile"
read -r ARC ARCID <<<"$(req_of $B2B 'Architettura documentata' $API)"
move $ARCID "Accettato"
ready $ARC $API "Diagramma aggiornato nella wiki di architettura"
verify architetture $ARC $API '{"outcome":"rejected","note":"Manca il flusso verso il sistema dei contratti."}'

# Frontend: l'MFA per gli amministratori arriva con la migrazione all'IdP, in deroga fino a fine marzo.
read -r MFA MFAID <<<"$(req_of $B2B 'Autenticazione a più fattori' $WEB)"
EX=$(call admin POST /api/v1/requirements/$MFA/exceptions "{\"impacts\":[\"$WEB\"],\"reason\":\"Il secondo fattore per gli amministratori arriva con la migrazione all'IdP aziendale, prevista a marzo.\",\"risk_level\":\"medio\",\"expires_on\":\"$(date -v+120d +%F 2>/dev/null || date -d '+120 days' +%F)\",\"compensating\":\"Accesso amministrativo solo da VPN, con registrazione delle sessioni.\"}" | py 'd["key"]')
STEP=$(call sicurezza GET /api/v1/elements/$EX | py 'next(s["id"] for a in d["approvals"] if a["status"]=="pending" for s in a["steps"] if s["decision"]=="pending")')
call sicurezza POST /api/v1/approvals/steps/$STEP/decision '{"decision":"approved","comment":"Ok con le misure compensative."}' > /dev/null
# Una seconda deroga, a rischio alto, aspetta il team Sicurezza e poi lo sponsor.
read -r SCA SCAID <<<"$(req_of $B2B 'Nessuna vulnerabilità critica' $WEB)"
EX2=$(call admin POST /api/v1/requirements/$SCA/exceptions "{\"impacts\":[\"$WEB\"],\"reason\":\"Una libreria di grafici ha una vulnerabilità alta senza versione corretta per il nostro framework.\",\"risk_level\":\"alto\",\"expires_on\":\"$(date -v+60d +%F 2>/dev/null || date -d '+60 days' +%F)\",\"compensating\":\"Componente usato solo nelle pagine autenticate, input validati lato server.\"}" | py 'd["key"]')

rm -rf $T
echo "Requisiti di esempio: $TLS verificato, $SEG da verificare, $ARC non superato, deroghe $EX (in vigore) e $EX2 (in approvazione) su $B2B."
