#!/usr/bin/env bash
# Viste salvate di esempio nel tenant "demo" (piano, passo 16; solo sviluppo e prova):
# personali, condivise con il gruppo "Demand management" e con tutta l'organizzazione, con
# preferiti nel menu e una vista predefinita per le applicazioni. Si può rilanciare: le viste
# con lo stesso nome non si duplicano.
# Uso: scripts/dev-demo-viste.sh [url]  — lo richiama dev-demo-data.sh.
set -euo pipefail
B=${1:-http://127.0.0.1:8080}; PW=${REGIA_DEV_PASSWORD:-'prova-locale-2026!'}; T=$(mktemp -d)
login() { curl -s -c $T/$1 -H 'Content-Type: application/json' -d "{\"email\":\"$1@demo.test\",\"password\":\"$PW\"}" $B/api/v1/auth/login > $T/$1.me; }
call() { local who=$1 m=$2 p=$3 body=${4:-} o=$T/.risposta c; if [ -n "$body" ]; then c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m -H 'Content-Type: application/json' -d "$body" "$B$p"); else c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m "$B$p"); fi; [ "$c" -lt 400 ] 2>/dev/null || echo "ATTENZIONE: $m $p ha risposto HTTP $c: $(head -c 300 $o)" >&2; cat $o; }
py() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
for u in admin dm; do login $u; done
DEMO=$(py 'next(t["tenant_id"] for t in d["tenants"] if t["slug"]=="demo")' < $T/admin.me)
for u in admin dm; do call $u POST /api/v1/me/tenant "{\"tenant_id\":\"$DEMO\"}" > /dev/null; done

# view WHO PAGE NOME KIND QUERY SETTINGS SCOPE [GRUPPO] → id (esistente se il nome c'è già).
view() {
  local who=$1 page=$2 name=$3 kind=$4 query=$5 settings=$6 scope=$7 group=${8:-}
  local have; have=$(call $who GET "/api/v1/views?page=$page" | NAME="$name" py 'next((v["id"] for v in d["items"] if v["name"]==__import__("os").environ["NAME"]), "")')
  if [ -n "$have" ]; then echo "$have"; return; fi
  local body; body=$(python3 -c "import json,sys; a=sys.argv; d={'page':a[1],'name':a[2],'kind':a[3],'query':a[4],'settings':json.loads(a[5]),'scope':a[6]}; d.update({'group_id':a[7]} if a[7] else {}); print(json.dumps(d))" "$page" "$name" "$kind" "$query" "$settings" "$scope" "$group")
  call $who POST /api/v1/views "$body" | py 'd["id"]'
}

DM_GROUP=$(call dm GET /api/v1/views/groups | py 'next(g["id"] for g in d["items"] if g["name"]=="Demand management")')

# Richieste.
V1=$(view admin richieste "In lavorazione, per fase" table "tipo: business_request stato: aperta ordina per: aggiornata desc" '{"cols":["key","title","state","assignee","updated"],"group":"state"}' private)
V2=$(view admin richieste "Urgenza alta" table "tipo: business_request stato: aperta urgenza: Alta ordina per: aggiornata desc" '{"cols":["key","title","state","f:urgenza","f:canale","updated"],"group":"f:urgenza"}' tenant)
V3=$(view dm richieste "Da triare (Demand management)" table "tipo: business_request fase: Inviata, Triage ordina per: creata asc" '{"cols":["key","title","state","reporter","created"]}' group "$DM_GROUP")
# Applicazioni e impatti.
V4=$(view admin applicazioni "Applicazioni per fase" table "tipo: application ordina per: titolo asc" '{"cols":["key","title","state","updated"],"group":"state"}' tenant)
V5=$(view admin impatti "Impatti aperti per assegnatario" table "tipo: impact stato: aperta ordina per: aggiornata desc" '{"cols":["key","title","state","assignee","updated"],"group":"assignee"}' private)

# Preferiti nel menu e predefinite.
call admin PUT /api/v1/views/$V1/favorite '{"favorite":true}' > /dev/null
call admin PUT /api/v1/views/$V2/favorite '{"favorite":true}' > /dev/null
call dm PUT /api/v1/views/$V3/favorite '{"favorite":true}' > /dev/null
call admin PUT /api/v1/views/default "{\"page\":\"applicazioni\",\"view_id\":\"$V4\",\"for\":\"tenant\"}" > /dev/null
rm -rf $T
echo "Viste salvate di esempio create (personali, di gruppo e dell'organizzazione)."
