#!/usr/bin/env bash
# Catalogo applicativo di esempio nel tenant "demo" (passo 2, solo sviluppo e prova):
# import CSV, referenti dei team, asset citati e impatti su alcune richieste esistenti.
# Uso: scripts/dev-demo-architecture.sh [url]  — dopo dev-seed.sh e dev-demo-data.sh.
set -euo pipefail
B=${1:-http://127.0.0.1:8080}; PW=${REGIA_DEV_PASSWORD:-'prova-locale-2026!'}; T=$(mktemp -d)
login() { curl -s -c $T/$1 -H 'Content-Type: application/json' -d "{\"email\":\"$1@demo.test\",\"password\":\"$PW\"}" $B/api/v1/auth/login > $T/$1.me; }
call() { local who=$1 m=$2 p=$3 body=${4:-} o=$T/.risposta c; if [ -n "$body" ]; then c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m -H 'Content-Type: application/json' -d "$body" "$B$p"); else c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m "$B$p"); fi; [ "$c" -lt 400 ] 2>/dev/null || echo "ATTENZIONE: $m $p ha risposto HTTP $c: $(head -c 300 $o)" >&2; cat $o; }
py() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
for u in admin dm; do login $u; done
DEMO=$(py 'next(t["tenant_id"] for t in d["tenants"] if t["slug"]=="demo")' < $T/admin.me)
call admin POST /api/v1/me/tenant "{\"tenant_id\":\"$DEMO\"}" > /dev/null
req_id() { call dm GET "/api/v1/elements?q=$(python3 -c 'import sys,urllib.parse; print(urllib.parse.quote("tipo: business_request " + sys.argv[1]))' "$1")" | py 'd["items"][0]["id"]'; }
# Catalogo applicativo (passo 2): import CSV, referenti dei team, asset citati e impatti.
CSV=$(python3 -c "import json,sys; print(json.dumps({'csv': open(sys.argv[1]).read()}))" "$(dirname "$0")/demo-applicazioni.csv")
call admin POST /api/v1/architecture/applications/import "$CSV" | py '"Catalogo: %d applicazioni nuove, %d errori" % (d["created"], d["errors"])'
member_id() { call admin GET /api/v1/members | py "next(m['id'] for m in d['items'] if m['display_name']=='$1')"; }
LEAD=$(member_id "Paolo Riva"); PMO=$(member_id "Davide Costa")
call admin GET /api/v1/teams | py '"\n".join(t["id"]+"|"+t["name"] for t in d["items"])' > $T/teams
while IFS='|' read -r tid tname; do
  case "$tname" in Web|Integrazioni) l=$LEAD;; *) l=$PMO;; esac
  call admin PATCH /api/v1/teams/$tid "{\"name\":\"$tname\",\"lead_id\":\"$l\"}" >/dev/null
done < $T/teams
asset_id() { call admin GET /api/v1/architecture/assets | py "next(a['id'] for a in d['items'] if a['name']=='$1')"; }
app_id() { call admin GET "/api/v1/architecture/applications?q=$1" | py "d['items'][0]['id']"; }
cite() { call dm POST /api/v1/demand/requests/$1/assets "{\"asset_id\":\"$(asset_id "$2")\"}" >/dev/null; }
impact() { call dm POST /api/v1/demand/requests/$1/impacts "{\"application_id\":\"$(app_id $2)\",\"change_kind\":\"$3\"${4:+,\"estimate_effort_days\":$4}}" | py 'd["id"]'; }
B2B=$(req_id "portale clienti B2B"); cite $B2B "Area riservata"; cite $B2B "App mobile"
impact $B2B IAM config 4 >/dev/null; impact $B2B API-CLI change 18 >/dev/null; impact $B2B WEB-AR change 25 >/dev/null; impact $B2B PAY change 8 >/dev/null
DORA=$(req_id "registro informazioni fornitori"); cite $DORA "Portale fornitori"; impact $DORA FORN change 30 >/dev/null
DWH=$(req_id "migrazione data warehouse"); cite $DWH "Reportistica direzionale"
rm -rf $T
echo "Catalogo di esempio creato."
