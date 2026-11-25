#!/usr/bin/env bash
# Architettura enterprise di esempio nel tenant "demo" (piano, passo 20; solo sviluppo e
# prova): mappa delle capability di una banca, dipendenze tra le 8 applicazioni demo,
# tecnologie con fine supporto, valutazioni TIME e un diagramma.
# Uso: scripts/dev-demo-ea.sh [url]  — dopo dev-demo-architecture.sh (lo richiama dev-demo-data.sh).
set -euo pipefail
B=${1:-http://127.0.0.1:8080}; PW=${REGIA_DEV_PASSWORD:-'prova-locale-2026!'}; T=$(mktemp -d)
login() { curl -s -c $T/$1 -H 'Content-Type: application/json' -d "{\"email\":\"$1@demo.test\",\"password\":\"$PW\"}" $B/api/v1/auth/login > $T/$1.me; }
call() { local who=$1 m=$2 p=$3 body=${4:-} o=$T/.risposta c; if [ -n "$body" ]; then c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m -H 'Content-Type: application/json' -d "$body" "$B$p"); else c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m "$B$p"); fi; [ "$c" -lt 400 ] 2>/dev/null || echo "ATTENZIONE: $m $p ha risposto HTTP $c: $(head -c 300 $o)" >&2; cat $o; }
py() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
login admin
DEMO=$(py 'next(t["tenant_id"] for t in d["tenants"] if t["slug"]=="demo")' < $T/admin.me)
call admin POST /api/v1/me/tenant "{\"tenant_id\":\"$DEMO\"}" > /dev/null

# Catalogo esteso in formato regia.ea/v1: lo stesso che si esporta e reimporta.
cat > $T/ea.json <<'JSON'
{
  "format": "regia.ea/v1",
  "capabilities": [
    {"code": "CAP-CLI", "name": "Gestione clienti", "criticality": "alta"},
    {"code": "CAP-ONB", "name": "Onboarding e adeguata verifica", "parent": "CAP-CLI", "criticality": "critica", "applications": ["API-CLI"]},
    {"code": "CAP-CRM", "name": "Relazione e assistenza", "parent": "CAP-CLI", "criticality": "media"},
    {"code": "CAP-CAN", "name": "Canali digitali", "criticality": "alta"},
    {"code": "CAP-WEB", "name": "Home banking web", "parent": "CAP-CAN", "criticality": "alta", "applications": ["WEB-AR"], "assets": ["Area riservata"]},
    {"code": "CAP-MOB", "name": "Mobile banking", "parent": "CAP-CAN", "criticality": "alta", "applications": ["APP-MOB"], "assets": ["App mobile"]},
    {"code": "CAP-PAG", "name": "Pagamenti", "criticality": "critica"},
    {"code": "CAP-BON", "name": "Bonifici e giroconti", "parent": "CAP-PAG", "criticality": "critica", "applications": ["PAY"]},
    {"code": "CAP-ISTA", "name": "Bonifici istantanei", "parent": "CAP-BON", "criticality": "critica", "applications": ["PAY"]},
    {"code": "CAP-CAR", "name": "Carte di pagamento", "parent": "CAP-PAG", "criticality": "alta", "applications": ["PAY"]},
    {"code": "CAP-SIC", "name": "Sicurezza e identità", "criticality": "critica"},
    {"code": "CAP-SCA", "name": "Autenticazione forte (SCA)", "parent": "CAP-SIC", "criticality": "critica", "applications": ["IAM"]},
    {"code": "CAP-DAT", "name": "Dati e reporting", "criticality": "media"},
    {"code": "CAP-REP", "name": "Reportistica direzionale", "parent": "CAP-DAT", "criticality": "media", "applications": ["DWH", "BI"], "assets": ["Reportistica direzionale"]},
    {"code": "CAP-VIG", "name": "Segnalazioni di vigilanza", "parent": "CAP-DAT", "criticality": "alta", "applications": ["DWH"]},
    {"code": "CAP-ACQ", "name": "Acquisti e fornitori", "criticality": "media"},
    {"code": "CAP-ICT", "name": "Gestione dei fornitori ICT", "parent": "CAP-ACQ", "criticality": "alta", "applications": ["FORN"], "assets": ["Portale fornitori"]}
  ],
  "dependencies": [
    {"source": "WEB-AR", "target": "API-CLI", "type": "calls_api", "attrs": {"protocol": "REST", "criticality": "high", "data": "anagrafica, rapporti e contratti", "classification": "personali"}},
    {"source": "APP-MOB", "target": "API-CLI", "type": "calls_api", "attrs": {"protocol": "REST", "criticality": "high", "data": "anagrafica, rapporti e contratti", "classification": "personali"}},
    {"source": "WEB-AR", "target": "IAM", "type": "uses_app", "attrs": {"protocol": "OIDC", "criticality": "critical", "data": "identità e sessioni", "classification": "riservati"}},
    {"source": "APP-MOB", "target": "IAM", "type": "uses_app", "attrs": {"protocol": "OIDC", "criticality": "critical", "data": "identità e sessioni", "classification": "riservati"}},
    {"source": "FORN", "target": "IAM", "type": "uses_app", "attrs": {"protocol": "OIDC", "criticality": "medium", "data": "identità dei fornitori", "classification": "interni"}},
    {"source": "API-CLI", "target": "PAY", "type": "calls_api", "attrs": {"protocol": "REST", "criticality": "critical", "data": "disposizioni di pagamento", "classification": "riservati"}},
    {"source": "API-CLI", "target": "IAM", "type": "calls_api", "attrs": {"protocol": "REST", "criticality": "high", "data": "verifica dei token", "classification": "riservati"}},
    {"source": "PAY", "target": "DWH", "type": "sends_events_to", "attrs": {"protocol": "Kafka", "criticality": "medium", "data": "movimenti contabili", "classification": "personali"}},
    {"source": "IAM", "target": "DWH", "type": "sends_events_to", "attrs": {"protocol": "Kafka", "criticality": "low", "data": "registro degli accessi", "classification": "riservati"}},
    {"source": "BI", "target": "DWH", "type": "reads_data_from", "attrs": {"protocol": "JDBC", "criticality": "low", "data": "indicatori aggregati", "classification": "interni"}},
    {"source": "API-CLI", "target": "DWH", "type": "reads_data_from", "attrs": {"protocol": "JDBC", "criticality": "medium", "data": "storico dei movimenti", "classification": "personali"}}
  ],
  "technologies": [
    {"name": "Java", "version": "8", "vendor": "Oracle", "category": "linguaggio", "end_of_support": "2026-12-31", "applications": ["API-CLI", "PAY"]},
    {"name": "Angular", "version": "15", "vendor": "Google", "category": "framework", "end_of_support": "2024-11-18", "applications": ["WEB-AR"]},
    {"name": "PostgreSQL", "version": "16", "category": "database", "end_of_support": "2028-11-09", "applications": ["API-CLI"]},
    {"name": "Oracle Database", "version": "19c", "vendor": "Oracle", "category": "database", "end_of_support": "2029-12-31", "applications": ["PAY", "DWH"]},
    {"name": "IBM MQ", "version": "9.1", "vendor": "IBM", "category": "middleware", "end_of_support": "2025-09-30", "applications": ["PAY"]},
    {"name": "Red Hat Enterprise Linux", "version": "8", "vendor": "Red Hat", "category": "sistema operativo", "end_of_support": "2029-05-31", "applications": ["IAM", "DWH"]}
  ],
  "time": [
    {"application": "WEB-AR", "time_rating": "invest", "note": "canale principale, nuova area riservata in corso"},
    {"application": "APP-MOB", "time_rating": "invest", "note": "canale in crescita"},
    {"application": "API-CLI", "time_rating": "invest", "note": "base delle integrazioni con i canali"},
    {"application": "PAY", "time_rating": "tolerate", "note": "stabile; migrazione del middleware da pianificare"},
    {"application": "IAM", "time_rating": "invest", "note": "piattaforma condivisa"},
    {"application": "DWH", "time_rating": "migrate", "note": "migrazione su cloud europeo (richiesta in triage)"},
    {"application": "BI", "time_rating": "eliminate", "note": "sostituita dai cruscotti del nuovo data warehouse"},
    {"application": "FORN", "time_rating": "tolerate", "note": "in attesa del registro DORA"}
  ]
}
JSON
BODY=$(python3 -c "import json,sys; print(json.dumps({'json': json.load(open(sys.argv[1]))}))" $T/ea.json)
call admin POST /api/v1/architecture/catalog/import "$BODY" | py '"Architettura: %d elementi nuovi, %d aggiornati, %d errori" % (d["created"], d["updated"], d["errors"])'

# Diagramma "Canali digitali e pagamenti" con le posizioni fissate e una nota.
call admin GET /api/v1/architecture/graph > $T/graph.json
LAYOUT=$(python3 - $T/graph.json <<'PY'
import json, sys
g = json.load(open(sys.argv[1]))
code = {n.get("code"): n["id"] for n in g["nodes"] if n["type"] == "application"}
pos = {"WEB-AR": (0, 0), "APP-MOB": (0, 220), "API-CLI": (300, 110), "IAM": (300, 380), "PAY": (600, 0), "DWH": (600, 260)}
nodes = [{"id": code[c], "x": x, "y": y} for c, (x, y) in pos.items() if c in code]
notes = [{"id": "n1", "x": 720, "y": 360, "w": 230, "text": "Java 8 e IBM MQ in fine supporto: piano di aggiornamento entro il 2027"}]
print(json.dumps({"name": "Canali digitali e pagamenti", "kind": "dependency",
  "description": "Dai canali ai pagamenti e allo storico: chi chiama chi e con quali dati.",
  "layout": {"nodes": nodes, "notes": notes}}))
PY
)
EXISTS=$(call admin GET /api/v1/architecture/diagrams | py 'any(x["name"]=="Canali digitali e pagamenti" for x in d["items"])')
if [ "$EXISTS" = "False" ]; then
  call admin POST /api/v1/architecture/diagrams "$LAYOUT" | py '"Diagramma: %s (%d elementi, %d collegamenti)" % (d["name"], len(d["nodes"]), len(d["edges"]))'
fi
# Diagrammi C4 generati dal grafo: contesto e container dell'API clienti, componenti dell'asset
# dei canali digitali (se ci sono). Rigenerare non duplica.
C4=$(python3 - $T/graph.json <<'PY'
import json, sys
g = json.load(open(sys.argv[1]))
code = {n.get("code"): n["id"] for n in g["nodes"] if n["type"] == "application"}
asset = next((n["id"] for n in g["nodes"] if n["type"] == "logical_asset"), "")
print(code.get("API-CLI", ""), asset)
PY
)
APIID=${C4% *}; ASSETID=${C4#* }
if [ -n "$APIID" ]; then
  for L in context container; do
    call admin POST /api/v1/architecture/diagrams/c4 "{\"root_id\":\"$APIID\",\"level\":\"$L\"}" | py '"Diagramma C4 %s: %d riquadri, %d frecce" % (d["name"], len(d["c4"]["nodes"]), len(d["c4"]["edges"]))'
  done
fi
if [ -n "$ASSETID" ]; then
  call admin POST /api/v1/architecture/diagrams/c4 "{\"root_id\":\"$ASSETID\",\"level\":\"component\"}" | py '"Diagramma C4 %s: %d riquadri, %d frecce" % (d["name"], len(d["c4"]["nodes"]), len(d["c4"]["edges"]))'
fi
# Gerarchia di gruppi per i permessi: il ruolo "Architetto" dato a "Tecnologia" vale anche per
# chi sta solo in "Squadra canali"; "Board architetturale digitale" ha il gruppo come membro.
if [ "$(call admin GET /api/v1/admin/groups | py 'any(g["name"]=="Tecnologia" for g in d["items"])')" = "False" ]; then
  PARENT=$(call admin POST /api/v1/admin/groups '{"name":"Tecnologia"}' | py 'd["id"]')
  CHILD=$(call admin POST /api/v1/admin/groups "{\"name\":\"Squadra canali\",\"parent_id\":\"$PARENT\"}" | py 'd["id"]')
  ROLE=$(call admin GET /api/v1/admin/roles | py 'next(r["id"] for r in d["items"] if r["key"]=="architect")')
  call admin POST /api/v1/admin/roles/$ROLE/assignments "{\"group_id\":\"$PARENT\"}" > /dev/null
  MEMBER=$(call admin GET /api/v1/admin/members | py 'next(m["id"] for m in d["items"] if m["email"]=="dev@demo.test")')
  call admin POST /api/v1/admin/groups/$CHILD/members "{\"member_id\":\"$MEMBER\"}" > /dev/null
  CHAIR=$(call admin GET /api/v1/admin/members | py 'next(m["id"] for m in d["items"] if m["email"]=="arch@demo.test")')
  call admin POST /api/v1/governance/bodies "{\"name\":\"Board architetturale digitale\",\"kind\":\"architecture_board\",\"chair_id\":\"$CHAIR\",\"groups\":[{\"group_id\":\"$PARENT\"}]}" | py '"Organo %s: %d membri effettivi" % (d["name"], len(d["effective"]))'
fi
rm -rf $T
echo "Architettura enterprise di esempio creata."
