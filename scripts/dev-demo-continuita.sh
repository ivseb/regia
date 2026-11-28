#!/usr/bin/env bash
# Continuità operativa di esempio nel tenant "demo" (docs/sviluppo/continuita.md; solo sviluppo e
# prova): prepara il modulo, dichiara RTO e RPO sulle applicazioni, approva la BIA dei bonifici con
# una catena incoerente (l'home banking dipende da API più lente) e pianifica un test di ripristino.
# Uso: scripts/dev-demo-continuita.sh [url]  — dopo dev-demo-ea.sh (lo richiama dev-demo-data.sh).
set -euo pipefail
B=${1:-http://127.0.0.1:8080}; PW=${REGIA_DEV_PASSWORD:-'prova-locale-2026!'}; T=$(mktemp -d)
login() { curl -s -c $T/$1 -H 'Content-Type: application/json' -d "{\"email\":\"$1@demo.test\",\"password\":\"$PW\"}" $B/api/v1/auth/login > $T/$1.me; }
call() { local who=$1 m=$2 p=$3 body=${4:-} o=$T/.risposta c; if [ -n "$body" ]; then c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m -H 'Content-Type: application/json' -d "$body" "$B$p"); else c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m "$B$p"); fi; [ "$c" -lt 400 ] 2>/dev/null || echo "ATTENZIONE: $m $p ha risposto HTTP $c: $(head -c 300 $o)" >&2; cat $o; }
py() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
login admin
DEMO=$(py 'next(t["tenant_id"] for t in d["tenants"] if t["slug"]=="demo")' < $T/admin.me)
call admin POST /api/v1/me/tenant "{\"tenant_id\":\"$DEMO\"}" > /dev/null

call admin POST /api/v1/continuity/setup > /dev/null

# RTO e RPO dichiarati (minuti): l'area riservata promette 1 ora ma chiama API più lente.
call admin GET /api/v1/architecture/applications > $T/apps.json
appid() { py "next(a['id'] for a in d['items'] if a.get('code')=='$1')" < $T/apps.json; }
for a in "WEB-AR 60 15" "APP-MOB 60 15" "API-CLI 240 60" "PAY 120 5" "IAM 60 5" "DWH 1440 240"; do
  set -- $a
  call admin PATCH /api/v1/architecture/applications/$(appid $1) "{\"rto_minutes\":$2,\"rpo_minutes\":$3,\"supports_critical_function\":true}" > /dev/null
done

# BIA dei bonifici: intollerabile a 4 ore per l'economico e i clienti.
BIA=$(call admin POST /api/v1/continuity/bia '{"capability":"CAP-BON"}')
if echo "$BIA" | grep -q '"key"'; then
  KEY=$(echo "$BIA" | py 'd["key"]'); ID=$(echo "$BIA" | py 'd["id"]')
  GRID='{"economico":{"1h":2,"4h":4,"1g":5,"3g":5,"1s":5},"normativo":{"1h":1,"4h":3,"1g":4,"3g":5,"1s":5},"reputazionale":{"1h":2,"4h":3,"1g":4,"3g":5,"1s":5},"clienti":{"1h":3,"4h":4,"1g":5,"3g":5,"1s":5}}'
  call admin PUT /api/v1/continuity/bia/$KEY "{\"impacts\":$GRID,\"critical\":true,\"critical_reason\":\"Pagamenti dei clienti: funzione importante ai sensi DORA (da verificare)\",\"mtpd_minutes\":240,\"rto_minutes\":60,\"rpo_minutes\":15}" > /dev/null
  call admin POST /api/v1/elements/$ID/transitions '{"to":"In revisione"}' > /dev/null
  call admin POST /api/v1/continuity/bia/$KEY/approve '{"comment":"Approvata dal comitato di continuità (dati di esempio)"}' > /dev/null
fi

# Un test di ripristino da backup pianificato fra un mese sui pagamenti.
WHEN=$(python3 -c "import datetime; print((datetime.date.today()+datetime.timedelta(days=30)).isoformat())")
call admin POST /api/v1/continuity/tests "{\"subject\":\"PAY\",\"type\":\"restore\",\"planned_on\":\"$WHEN\"}" > /dev/null

N=$(call admin GET /api/v1/continuity/overview | py 'len(d["issues"])')
echo "continuità di esempio pronta: $N incoerenze da guardare in /architettura/continuita"
rm -rf $T
