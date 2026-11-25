#!/usr/bin/env bash
# Pianificazione di esempio nel tenant "demo" (Gantt e roadmap, solo sviluppo e prova):
# capacità dei team, dipendenze tra impatti, piani automatici, finestre delle richieste.
# Uso: scripts/dev-demo-planning.sh [url]  — dopo dev-demo-architecture.sh.
set -euo pipefail
B=${1:-http://127.0.0.1:8080}; PW=${REGIA_DEV_PASSWORD:-'prova-locale-2026!'}; T=$(mktemp -d)
login() { curl -s -c $T/$1 -H 'Content-Type: application/json' -d "{\"email\":\"$1@demo.test\",\"password\":\"$PW\"}" $B/api/v1/auth/login > $T/$1.me; }
call() { local who=$1 m=$2 p=$3 body=${4:-} o=$T/.risposta c; if [ -n "$body" ]; then c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m -H 'Content-Type: application/json' -d "$body" "$B$p"); else c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m "$B$p"); fi; [ "$c" -lt 400 ] 2>/dev/null || echo "ATTENZIONE: $m $p ha risposto HTTP $c: $(head -c 300 $o)" >&2; cat $o; }
py() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
for u in admin dm; do login $u; done
DEMO=$(py 'next(t["tenant_id"] for t in d["tenants"] if t["slug"]=="demo")' < $T/admin.me)
call admin POST /api/v1/me/tenant "{\"tenant_id\":\"$DEMO\"}" > /dev/null
req_id() { call dm GET "/api/v1/elements?q=$(python3 -c 'import sys,urllib.parse; print(urllib.parse.quote("tipo: business_request " + sys.argv[1]))' "$1")" | py 'd["items"][0]["id"]'; }
# id dell'impatto di una richiesta su un'applicazione (per nome)
impact_of() { call dm GET /api/v1/demand/requests/$1/plan | py "next(t['id'] for t in d['tasks'] if t['application']=='$2')"; }
depends() { call dm PATCH /api/v1/impacts/$1 "{\"depends_on\":[\"$(call dm GET /api/v1/elements/$2 | py 'd["element"]["key"]')\"]}" >/dev/null; }
window() { call dm PUT /api/v1/demand/requests/$(req_id "$1")/schedule "{\"start\":\"$2\",\"end\":\"$3\"}" >/dev/null; }

call admin GET /api/v1/teams | py '"\n".join(t["id"]+"|"+t["name"]+"|"+(t["lead_id"] or "") for t in d["items"])' > $T/teams
while IFS='|' read -r tid tname tlead; do
  case "$tname" in Web) c=10;; Integrazioni) c=8;; Identity) c=3;; Payments) c=4;; *) c=5;; esac
  call admin PATCH /api/v1/teams/$tid "{\"name\":\"$tname\",\"lead_id\":\"$tlead\",\"capacity_days_week\":$c}" >/dev/null
done < $T/teams

B2B=$(req_id "portale clienti B2B")
# Impatti attesi (se lo script del catalogo era una versione precedente, si aggiungono; i doppioni sono rifiutati).
app_id() { call admin GET "/api/v1/architecture/applications?q=$1" | py "d['items'][0]['id']"; }
for spec in "PAY|8" "IAM|4" "API-CLI|18" "WEB-AR|25"; do
  IFS='|' read -r code days <<< "$spec"
  call dm POST /api/v1/demand/requests/$B2B/impacts "{\"application_id\":\"$(app_id $code)\",\"change_kind\":\"change\",\"estimate_effort_days\":$days}" >/dev/null
done
IAM=$(impact_of $B2B "IAM / SSO"); API=$(impact_of $B2B "Backend API clienti")
WEB=$(impact_of $B2B "Frontend area riservata"); PAY=$(impact_of $B2B "Modulo pagamenti")
depends $API $IAM; depends $WEB $API; depends $PAY $API
call dm POST /api/v1/demand/requests/$B2B/plan/auto '{"replan":true}' >/dev/null
call dm POST /api/v1/demand/requests/$(req_id "registro informazioni fornitori")/plan/auto '{"replan":true}' >/dev/null

# Finestre delle richieste senza impatti (la NIS2 sfora apposta la scadenza: si vede in rosso).
window "incidenti significativi" 2026-12-01 2027-01-29
window "migrazione data warehouse" 2027-02-01 2027-06-30
window "firma digitale" 2027-01-11 2027-03-12
window "conservazione a norma" 2027-03-01 2027-05-28
window "licenze produttività" 2027-04-05 2027-04-30
rm -rf $T
echo "Pianificazione di esempio creata."
