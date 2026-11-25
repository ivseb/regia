#!/usr/bin/env bash
# Work management di esempio nel tenant "demo" (piano, passi 10 e 18; solo sviluppo e prova):
# spazi dei team Web e Integrazioni, epic nati dall'accettazione degli impatti del Portale
# clienti B2B, story, task e bug, uno sprint attivo con un po' di storia, qualche ora lavorata.
# Uso: scripts/dev-demo-work.sh [url]  — dopo dev-demo-architecture.sh e dev-demo-finance.sh.
set -euo pipefail
B=${1:-http://127.0.0.1:8080}; T=$(mktemp -d)
eval "$(grep -m1 '^PW=' "$(dirname "$0")/dev-seed.sh")"   # stessa password di sviluppo del seed
login() { curl -s -c $T/$1 -H 'Content-Type: application/json' -d "{\"email\":\"$1@demo.test\",\"password\":\"$PW\"}" $B/api/v1/auth/login > $T/$1.me; }
call() { local who=$1 m=$2 p=$3 body=${4:-} o=$T/.risposta c; if [ -n "$body" ]; then c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m -H 'Content-Type: application/json' -d "$body" "$B$p"); else c=$(curl -s -o $o -w '%{http_code}' -b $T/$who -X $m "$B$p"); fi; [ "$c" -lt 400 ] 2>/dev/null || echo "ATTENZIONE: $m $p ha risposto HTTP $c: $(head -c 300 $o)" >&2; cat $o; }
py() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
for u in admin dm lead dev dev2 finanza; do login $u; done
DEMO=$(py 'next(t["tenant_id"] for t in d["tenants"] if t["slug"]=="demo")' < $T/admin.me)
call admin POST /api/v1/me/tenant "{\"tenant_id\":\"$DEMO\"}" > /dev/null
me() { py 'd["active_tenant"]["member_id"]' < $T/$1.me; }
DEV=$(me dev); DEV2=$(me dev2); LEAD=$(me lead)
day() { python3 -c "import datetime,sys; print((datetime.date.today()+datetime.timedelta(days=int(sys.argv[1]))).isoformat())" "$1"; }

# Spazi dei team Web e Integrazioni (i team arrivano dall'import del catalogo).
call admin GET /api/v1/work/spaces | py '"\n".join(t["id"]+"|"+t["name"] for t in d["teams_without_space"])' > $T/teams
while IFS='|' read -r tid tname; do
  case "$tname" in Web|Integrazioni) call admin POST /api/v1/work/spaces "{\"team_id\":\"$tid\"}" | py '"Spazio " + d.get("key", "?") + " (" + d.get("team", "") + ")"';; esac
done < $T/teams

# Il Portale clienti B2B: gli impatti su Web e Integrazioni arrivano al team, che li accetta.
# All'accettazione la regola del processo crea l'epic nello spazio del team.
B2B=$(call dm GET "/api/v1/elements?q=$(python3 -c 'import urllib.parse; print(urllib.parse.quote("tipo: business_request portale clienti B2B"))')" | py 'd["items"][0]["key"]')
impact_of() { call dm GET /api/v1/demand/requests/$B2B/plan | py "next(t['id'] for t in d['tasks'] if t['application']=='$1')"; }
for app in "Frontend area riservata" "Backend API clienti"; do
  ID=$(impact_of "$app")
  for to in "In analisi" "Stimato"; do call dm POST /api/v1/elements/$ID/transitions "{\"to\":\"$to\"}" >/dev/null; done
  call lead POST /api/v1/elements/$ID/transitions '{"to":"Accettato"}' >/dev/null
done
epic_of() {
  local id; id=$(impact_of "$1")
  # Sulle istanze dove l'impatto è stato accettato con una versione precedente del processo l'epic non nasce da solo: lo si crea a mano.
  call admin GET /api/v1/work/elements/$id/delivery | py 'd["epics"][0]["key"]' 2>/dev/null \
    || call admin POST /api/v1/work/impacts/$id/epic '{}' | py 'd.get("key") or d["epic"]["key"]'
}
EWEB=$(epic_of "Frontend area riservata"); EINT=$(epic_of "Backend API clienti")
echo "Epic: $EWEB (Web), $EINT (Integrazioni)"
call dev POST /api/v1/elements/$(call admin GET /api/v1/elements/$EWEB | py 'd["element"]["id"]')/transitions '{"to":"In corso"}' >/dev/null

# Story, task e bug. item SPAZIO tipo "titolo" punti priorità genitore assegnatario
item() {
  local body
  body=$(python3 -c "import json,sys; a=sys.argv; d={'type':a[1],'title':a[2]}; f={}
if a[3]: f['punti']=float(a[3])
if a[4]: f['priorita']=a[4]
if f: d['fields']=f
if a[5]: d['parent']=a[5]
if a[6]: d['assignee_id']=a[6]
print(json.dumps(d))" "$2" "$3" "${4:-}" "${5:-}" "${6:-}" "${7:-}")
  call dev POST /api/v1/work/spaces/$1/items "$body" | py 'd["key"]'
}
S1=$(item WEB story "Accesso con SPID e CIE" 5 Alta $EWEB $DEV)
S2=$(item WEB story "Elenco dei contratti con filtri" 3 Normale $EWEB $DEV2)
S3=$(item WEB story "Scarico delle fatture in PDF" 3 Normale $EWEB $DEV)
S4=$(item WEB story "Cambio del referente aziendale" 5 Alta $EWEB)
S5=$(item WEB story "Notifiche delle scadenze contrattuali" 8 Bassa $EWEB)
S6=$(item WEB story "Pagina dei ticket di assistenza" 5 Normale $EWEB $DEV2)
S7=$(item WEB story "Versione accessibile dei grafici dei consumi" 3 Normale $EWEB)
T1=$(item WEB task "Configurare il client OIDC verso l'IdP" "" "" $S1 $DEV)
T2=$(item WEB task "Gestire la scadenza della sessione" "" "" $S1 $DEV)
T3=$(item WEB task "Test end-to-end del login" "" "" $S1 $DEV2)
B1=$(item WEB bug "Il logout non chiude la sessione su Safari" 2 Bloccante "" $DEV2)
B2=$(item WEB bug "Date dei contratti nel fuso sbagliato" 1 Alta $EWEB)
for t in "API contratti: paginazione a cursore|5|Alta" "API fatture: download firmato|3|Normale" "Webhook delle scadenze|5|Normale" "Limitazione delle chiamate per cliente|3|Bassa"; do
  IFS='|' read -r title pts pr <<< "$t"; item INT story "$title" "$pts" "$pr" $EINT >/dev/null
done
item INT bug "Timeout sulle anagrafiche con più di 500 contratti" 3 Alta >/dev/null
id_of() { call admin GET /api/v1/elements/$1 | py 'd["element"]["id"]'; }

# Board di Web: limite WIP sulla colonna In corso.
call admin GET /api/v1/work/spaces/WEB/board | python3 -c "import json,sys; d=json.load(sys.stdin); s=d['settings']
for c in s['columns']:
  c['wip'] = 3 if c['name']=='In corso' else (2 if c['name']=='In revisione' else 0)
print(json.dumps(s))" > $T/settings.json
call admin PUT /api/v1/work/spaces/WEB/board/settings "$(cat $T/settings.json)" >/dev/null

# Sprint 12 attivo (iniziato da una settimana) e sprint 13 pianificato.
SP12=$(call dev POST /api/v1/work/spaces/WEB/sprints "{\"title\":\"Sprint 12\",\"goal\":\"Accesso e contratti nell'area riservata\",\"start\":\"$(day -7)\",\"end\":\"$(day 6)\"}" | py 'd["key"]')
SP13=$(call dev POST /api/v1/work/spaces/WEB/sprints "{\"title\":\"Sprint 13\",\"goal\":\"Fatture e referenti\",\"start\":\"$(day 7)\",\"end\":\"$(day 20)\"}" | py 'd["key"]')
for k in $S1 $S2 $S3 $S6 $B1; do call dev PUT /api/v1/work/elements/$k/sprint "{\"sprint\":\"$SP12\"}" >/dev/null; done
for k in $S4 $B2; do call dev PUT /api/v1/work/elements/$k/sprint "{\"sprint\":\"$SP13\"}" >/dev/null; done
call dev POST /api/v1/work/sprints/$SP12/start >/dev/null
# Un po' di storia: task e story che avanzano, una story chiusa.
step() { call ${3:-dev} POST /api/v1/elements/$(id_of $1)/transitions "{\"to\":\"$2\"}" >/dev/null; }
step $T1 "In corso"; step $T1 "Fatto"; step $T2 "In corso"; step $T2 "Fatto"; step $T3 "In corso" dev2; step $T3 "Fatto" dev2
step $S1 "In corso"; step $S1 "In revisione"; step $S1 "Fatto"
step $S2 "In corso" dev2; step $S2 "In revisione" dev2
step $S3 "In corso"
step $B1 "In corso" dev2
step $S6 "In corso" dev2

# Ore lavorate negli ultimi giorni (con una tariffa personale diventano costi della richiesta).
call finanza POST /api/v1/finance/rates "{\"member_id\":\"$DEV\",\"hourly_cents\":6200,\"valid_from\":\"2026-01-01\"}" >/dev/null
call finanza POST /api/v1/finance/rates "{\"member_id\":\"$DEV2\",\"hourly_cents\":5800,\"valid_from\":\"2026-01-01\"}" >/dev/null
for h in "dev $T1 3h -6 Client OIDC" "dev $T1 2h30m -5 Ritocchi dopo la revisione" "dev $T2 4h -4 Rinnovo del token" "dev2 $T3 6h -3 Scenari di login" \
         "dev $S3 3h -1 Generazione del PDF" "dev2 $S2 5h -2 Filtri e ordinamento" "dev2 $B1 1h30m 0 Riproduzione del problema" "dev $S1 1h 0 Revisione"; do
  set -- $h; who=$1 key=$2 dur=$3 off=$4; shift 4
  call $who POST /api/v1/work/elements/$key/time "{\"duration\":\"$dur\",\"date\":\"$(day $off)\",\"text\":\"$*\"}" >/dev/null
done
rm -rf $T
echo "Work management di esempio creato: board WEB e INT, sprint $SP12 attivo, epic $EWEB e $EINT."
