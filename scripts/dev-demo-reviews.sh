#!/usr/bin/env bash
# Valutazioni di esempio nel tenant "demo" (piano, passo 4; solo sviluppo e prova):
# responsabili dei team valutatori, questionari in compilazione, inviati, in esame, con
# chiarimenti e con esito. Le valutazioni della richiesta DORA le hanno aperte le regole
# quando è nato l'impatto sul portale fornitori; quelle del portale B2B si aprono a mano.
# Uso: scripts/dev-demo-reviews.sh [url]  — dopo dev-demo-architecture.sh (lo richiama dev-demo-data.sh).
set -euo pipefail
B=${1:-http://127.0.0.1:8080}; PW=${REGIA_DEV_PASSWORD:-'prova-locale-2026!'}; T=$(mktemp -d)
login() { curl -s -c $T/$1 -H 'Content-Type: application/json' -d "{\"email\":\"$1@demo.test\",\"password\":\"$PW\"}" $B/api/v1/auth/login > $T/$1.me; }
call() { local who=$1 m=$2 p=$3 body=${4:-} o=$T/.risposta c; if [ -n "$body" ]; then c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m -H 'Content-Type: application/json' -d "$body" "$B$p"); else c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m "$B$p"); fi; [ "$c" -lt 400 ] 2>/dev/null || echo "ATTENZIONE: $m $p ha risposto HTTP $c: $(head -c 300 $o)" >&2; cat $o; }
py() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
USERS="admin dm lead sicurezza architetture dpo compliance"
for u in $USERS; do login $u; done
DEMO=$(py 'next(t["tenant_id"] for t in d["tenants"] if t["slug"]=="demo")' < $T/admin.me)
for u in $USERS; do call $u POST /api/v1/me/tenant "{\"tenant_id\":\"$DEMO\"}" > /dev/null; done
me() { call $1 GET /api/v1/me | py 'd["active_tenant"]["member_id"]'; }
req_key() { call dm GET "/api/v1/elements?q=$(python3 -c 'import sys,urllib.parse; print(urllib.parse.quote("tipo: business_request " + sys.argv[1]))' "$1")" | py 'd["items"][0]["key"]'; }

# Responsabili dei team valutatori (ricevono le escalation; il DPO decide la DPIA).
for t in sicurezza:sicurezza architetture:architetture privacy:dpo compliance:compliance; do
  call admin PATCH /api/v1/review-config/teams/${t%%:*} "{\"lead_id\":\"$(me ${t##*:})\"}" > /dev/null
done

# Valutazioni di una richiesta (o di un suo impatto) per tipo: chiave e id dell'elemento.
review_of() { call dm GET /api/v1/elements/$1/reviews | py "next(r['key'] for r in d['items'] if r['type_key']=='$2' and not r['superseded'] and ('$3'=='' or r['application']=='$3'))"; }
id_of() { call dm GET /api/v1/reviews/$1 | py 'd["element"]["id"]'; }
answers() { call $1 PUT /api/v1/reviews/$2/answers "{\"answers\":$3}" > /dev/null; }
move() { call $1 POST /api/v1/elements/$(id_of $2)/transitions "{\"to\":\"$3\"${4:+,\"comment\":\"$4\"}}" > /dev/null; }
open() { call dm POST /api/v1/elements/$1/reviews "{\"type\":\"$2\"${3:+,\"impact\":\"$3\"}}" | py 'd["review"]["key"]'; }
impact_of() { call dm GET /api/v1/demand/requests/$1/analysis | py "next(i['key'] for i in d['impacts'] if i['application']=='$2')"; }

# --- Adeguamento DORA: le regole hanno aperto il parere compliance (funzione critica) e la design review del portale fornitori.
DORA=$(req_key "registro informazioni fornitori")
# Sulle istanze dove la richiesta era già in Valutazione prima delle regole, le valutazioni si aprono a mano.
PAR=$(review_of $DORA parere_compliance "" 2>/dev/null || open $DORA parere_compliance)
answers dm $PAR '{"nuovo_fornitore": true, "fornitore": "Provider SaaS per il registro delle informazioni"}'
DES=$(review_of $DORA design_review "" 2>/dev/null || open $DORA design_review "$(impact_of $DORA "Portale fornitori")")
answers lead $DES '{"tipo_intervento": "modifica", "descrizione": "Nuova sezione del portale fornitori per raccogliere i dati del registro DORA.", "hosting": "cloud_ue", "nuove_tecnologie": false, "nuove_api": true, "standard_api": false, "volumi": 5000}'
move lead $DES "Inviata al team"
move architetture $DES "In esame"

# --- Portale clienti B2B: valutazioni aperte a mano dal demand manager.
B2B=$(req_key "portale clienti B2B")
API=$(impact_of $B2B "Backend API clienti"); WEB=$(impact_of $B2B "Frontend area riservata")
SEC=$(open $B2B security_review $API)
answers lead $SEC '{"esposto_internet": true, "autenticazione": "sso", "mfa": false, "utenti_esterni": true, "cifratura_riposo": true, "segreti": "config", "terze_parti": false}'
move lead $SEC "Inviata al team"
move sicurezza $SEC "In esame"
move sicurezza $SEC "Chiarimenti" "Le API per i partner passano anch'esse dall'SSO? Serve il dettaglio dei client."
call dm POST /api/v1/elements/$SEC/comments '{"body":"Sì: i partner usano il client credentials dell'\''SSO aziendale, un client per partner."}' > /dev/null
move lead $SEC "In esame"
for p in $(call sicurezza GET /api/v1/reviews/$SEC | py '" ".join(p["id"]+":"+(p["rule_key"] or "") for p in d["prescriptions"] if p["status"]=="proposed")'); do
  case "${p##*:}" in pentest) s=rejected;; *) s=accepted;; esac
  call sicurezza PATCH /api/v1/reviews/$SEC/prescriptions/${p%%:*} "{\"status\":\"$s\"}" > /dev/null
done
call sicurezza POST /api/v1/reviews/$SEC/prescriptions '{"text":"Inviare i log di accesso delle API partner al SIEM aziendale.","category":"Monitoraggio"}' > /dev/null
call sicurezza POST /api/v1/reviews/$SEC/decision '{"outcome":"favorevole_con_prescrizioni","note":"Favorevole: MFA per gli utenti, WAF e vault prima del rilascio."}' > /dev/null

WDES=$(open $B2B design_review $WEB)
answers lead $WDES '{"descrizione": "Nuove pagine del profilo cliente e dei contratti."}'
open $B2B security_review $WEB > /dev/null

DPIA=$(open $B2B dpia_preliminare)
answers dm $DPIA '{"finalita": "Gestione del profilo dei clienti B2B e dei loro contratti.", "interessati": ["clienti"], "dati_particolari": false, "profilazione": false, "larga_scala": true, "base_giuridica": "contratto", "conservazione_mesi": 120, "extra_ue": false}'
move dm $DPIA "Inviata al team"

rm -rf $T
echo "Valutazioni di esempio create: $PAR e $DES ($DORA), $SEC, $WDES e $DPIA ($B2B)."
