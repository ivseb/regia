#!/usr/bin/env bash
# Campi personalizzati di esempio nel tenant "demo" (sdlc.md §10; solo sviluppo e prova):
# sulle richieste: "Canale" (scelta multipla), "Data go-live richiesta" (data,
# obbligatoria da Pianificazione), "Referente IT" (persona), "Link documento" (indirizzo web)
# e "Classificazione" (scelta, visibile solo se driver: normativo), con qualche valore sulle
# richieste già create. Si può rilanciare: i campi esistenti restano, le regole si riapplicano.
# Uso: scripts/dev-demo-fields.sh [url]  — lo richiama dev-demo-data.sh.
set -euo pipefail
B=${1:-http://127.0.0.1:8080}; PW=${REGIA_DEV_PASSWORD:-'prova-locale-2026!'}; T=$(mktemp -d)
login() { curl -s -c $T/$1 -H 'Content-Type: application/json' -d "{\"email\":\"$1@demo.test\",\"password\":\"$PW\"}" $B/api/v1/auth/login > $T/$1.me; }
call() { local who=$1 m=$2 p=$3 body=${4:-} o=$T/.risposta c; if [ -n "$body" ]; then c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m -H 'Content-Type: application/json' -d "$body" "$B$p"); else c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m "$B$p"); fi; [ "$c" -lt 400 ] 2>/dev/null || echo "ATTENZIONE: $m $p ha risposto HTTP $c: $(head -c 300 $o)" >&2; cat $o; }
py() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
login admin
DEMO=$(py 'next(t["tenant_id"] for t in d["tenants"] if t["slug"]=="demo")' < $T/admin.me)
call admin POST /api/v1/me/tenant "{\"tenant_id\":\"$DEMO\"}" > /dev/null

# Campi (422 se esistono già: si va avanti).
field() { call admin POST /api/v1/fields "$1" > /dev/null; }
field '{"key":"canale","name":"Canale","data_type":"enum","multi":true,"aliases":["channel"],"help":"Da dove è arrivata la richiesta.",
  "options":[{"name":"Email","color":"accent"},{"name":"Telefono","color":"ok"},{"name":"Portale","color":"flag"},{"name":"Incontro","color":"muted"}]}'
field '{"key":"data_golive","name":"Data go-live richiesta","data_type":"date","aliases":["golive"],"help":"Quando il business vuole la soluzione in produzione."}'
field '{"key":"referente_it","name":"Referente IT","data_type":"member","help":"Chi segue la richiesta per l'"'"'IT."}'
field '{"key":"link_documento","name":"Link documento","data_type":"url","help":"Documento di analisi, capitolato o pagina di riferimento."}'
field '{"key":"classificazione","name":"Classificazione","data_type":"enum","help":"Classificazione delle informazioni trattate (solo per le richieste normative).",
  "options":[{"name":"Pubblica","color":"ok"},{"name":"Interna","color":""},{"name":"Riservata","color":"flag"},{"name":"Segreta","color":"danger"}],"default":"Interna"}'

# Associazioni alla richiesta, con sezioni, obblighi e visibilità.
assoc() { call admin PUT /api/v1/fields/$1/types/business_request "$2" > /dev/null; }
assoc canale          '{"section":"Origine","required":false,"required_from":"","visible_when":""}'
assoc referente_it    '{"section":"Persone","required":false,"required_from":"","visible_when":""}'
assoc data_golive     '{"section":"Pianificazione","required":false,"required_from":"Pianificazione","visible_when":""}'
assoc link_documento  '{"section":"Origine","required":false,"required_from":"","visible_when":""}'
assoc classificazione '{"section":"Conformità","required":false,"required_from":"Triage","visible_when":"driver: normativo"}'
call admin PUT /api/v1/types/business_request/fields/order '{"fields":["canale","link_documento","urgenza","benefici_attesi","referente_it","data_golive","classificazione"]}' > /dev/null

# Valori sulle richieste di esempio (per titolo), con il lock ottimistico.
ARCH=$(call admin GET /api/v1/members | py 'next(m["id"] for m in d["items"] if m["display_name"]=="Elena Conti")')
set_fields() {
  local q=$1 fields=$2
  local key; key=$(call admin GET "/api/v1/elements?q=$(python3 -c 'import sys,urllib.parse; print(urllib.parse.quote("tipo: business_request " + sys.argv[1]))' "$q")" | py 'd["items"][0]["key"] if d.get("items") else ""')
  [ -z "$key" ] && return 0
  local el; el=$(call admin GET /api/v1/elements/$key)
  local id ver; id=$(echo "$el" | py 'd["element"]["id"]'); ver=$(echo "$el" | py 'd["element"]["version"]')
  call admin PATCH /api/v1/elements/$id "{\"version\":$ver,\"fields\":$fields}" > /dev/null
}
set_fields "registro informazioni fornitori" "{\"canale\":[\"Email\",\"Portale\"],\"classificazione\":\"Riservata\",\"referente_it\":\"$ARCH\",\"link_documento\":\"https://www.eiopa.europa.eu/digital-operational-resilience-act-dora_en\",\"data_golive\":\"2027-03-31\"}"
set_fields "incidenti significativi" "{\"canale\":[\"Incontro\"],\"classificazione\":\"Riservata\",\"data_golive\":\"2026-12-20\"}"
set_fields "portale clienti" "{\"canale\":[\"Portale\",\"Telefono\"],\"referente_it\":\"$ARCH\",\"data_golive\":\"2027-06-30\"}"
set_fields "firma digitale" "{\"canale\":[\"Email\"],\"data_golive\":\"2027-02-28\",\"link_documento\":\"https://www.agid.gov.it/it/piattaforme/firma-elettronica-qualificata\"}"
set_fields "data warehouse" "{\"canale\":[\"Telefono\"]}"
rm -rf $T
echo "Campi personalizzati di esempio pronti."
