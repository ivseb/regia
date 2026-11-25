#!/usr/bin/env bash
# Vulnerabilità governate di esempio nel tenant "demo" (DevSecOps fase 2; solo sviluppo e prova).
# Parte dalle vulnerabilità che dev-demo-devsecops.sh ha già mandato: scrive la politica di SLA
# (escalation con i gruppi Sicurezza e Risk management, esposizione da un campo delle
# applicazioni), sceglie chi approva le eccezioni, poi lavora alcune vulnerabilità come farebbe
# un team (in carico, in correzione, risolta, falso positivo) e chiede tre eccezioni: una
# approvata e collegata a un rischio, due in attesa.
# Uso: scripts/dev-demo-vulnerabilita.sh [url]  — dopo dev-demo-devsecops.sh, dev-demo-risk.sh
# e dev-demo-architecture.sh (lo richiama dev-demo-data.sh). Si può rilanciare.
set -euo pipefail
B=${1:-http://127.0.0.1:8080}
PW=${REGIA_DEV_PASSWORD:-'prova-locale-2026!'}
python3 - "$B" "$PW" <<'PY'
import datetime as dt, http.cookiejar, json, sys, urllib.request

base, pw = sys.argv[1], sys.argv[2]

class Session:
    def __init__(self, email):
        self.web = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))
        me = self.call("POST", "/api/v1/auth/login", {"email": email, "password": pw})
        demo = next(t["tenant_id"] for t in me["tenants"] if t["slug"] == "demo")
        self.call("POST", "/api/v1/me/tenant", {"tenant_id": demo})

    def call(self, method, path, body=None, tolerate=()):
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(base + path, data=data, method=method)
        if body is not None:
            req.add_header("Content-Type", "application/json")
        try:
            with self.web.open(req) as r:
                raw = r.read()
                return json.loads(raw) if raw else {}
        except urllib.error.HTTPError as e:
            detail = e.read().decode(errors="replace")
            if e.code in tolerate:
                return {"_error": detail}
            raise SystemExit(f"{method} {path}: {e.code} {detail}")

admin = Session("admin@demo.test")
dm = Session("dm@demo.test")          # Sara Villa: chiede le eccezioni e lavora le vulnerabilità
arch = Session("arch@demo.test")      # Elena Conti: approva
V = "/api/v1/devsecops/vuln"

# --- Campo «Esposta su Internet» sulle applicazioni, con qualche valore.
admin.call("POST", "/api/v1/fields", {"key": "esposta_internet", "name": "Esposta su Internet", "data_type": "boolean",
                                       "help": "Raggiungibile da Internet: accorcia lo SLA delle vulnerabilità."}, tolerate=(409, 422))
admin.call("PUT", "/api/v1/fields/esposta_internet/types/application", {"section": "Sicurezza", "required": False, "required_from": "", "visible_when": ""}, tolerate=(404, 422))
for code in ("WEB-AR", "API-CLI", "PAY", "APP-MOB"):
    found = admin.call("GET", f"/api/v1/architecture/applications?q={code}")["items"]
    app = next((a for a in found if a.get("code") == code), None)
    if not app:
        continue
    el = admin.call("GET", f"/api/v1/elements/{app['key']}")["element"]
    admin.call("PATCH", f"/api/v1/elements/{el['id']}", {"version": el["version"], "fields": {"esposta_internet": True}}, tolerate=(409, 422))

# --- Politica: SLA di partenza, esposizione dal campo, escalation fino ai gruppi.
cfg = admin.call("GET", V + "/config")
pol = cfg["policy"]
group = lambda n: next((g["id"] for g in cfg["groups"] if g["name"] == n), None)
steps = [
    {"name": "Preavviso", "days": -7, "to": ["assignee", "team_lead"]},
    {"name": "Scaduta", "days": 0, "to": ["assignee", "team", "team_lead"]},
    {"name": "Sicurezza informata", "days": 7, "to": [f"group:{group('Sicurezza')}"] if group("Sicurezza") else ["team_lead"]},
    {"name": "Comitato rischi", "days": 21, "to": [f"group:{group('Risk management')}"] if group("Risk management") else ["team_lead"]},
]
pol.update({"exposure_field": "esposta_internet", "exposure_values": ["true", "sì", "si", "internet"], "escalation": steps})
saved = admin.call("PUT", V + "/config/policy", pol)

# --- Chi approva le eccezioni, per gravità.
member = lambda n: next(m["id"] for m in cfg["members"] if m["name"] == n)
elena, federica = member("Elena Conti"), member("Federica Gatti")
st = lambda *ids: [{"approvers": [{"member": i}]} for i in ids]
admin.call("PUT", V + "/config/exception-policy", {"rules": [
    {"when": {"risk_lte": 2}, "steps": st(elena)},
    {"when": {"risk_lte": 3}, "steps": st(elena, federica)},
    {"when": {}, "steps": st(federica)},
]})

# --- Si lavora qualche vulnerabilità.
items = dm.call("GET", V + "/items?stage=open&limit=200&sort=severity")["items"]
by = lambda sev: [i for i in items if i["severity"] == sev and i["application_id"]]
crit, high, med, low = by("critical"), by("high"), by("medium"), by("low")
bulk = lambda ids, action, **kw: dm.call("POST", V + "/bulk", {"ids": [i["id"] for i in ids], "action": action, **kw}, tolerate=(422,)) if ids else None
bulk(crit[:2] + high[:2], "take")
bulk(crit[:1] + high[:1], "start")
bulk(low[:1], "resolve", note="Aggiornato il pacchetto nel prossimo rilascio")
bulk(med[1:2], "false_positive", note="Il modulo non è caricato dall'applicazione")

# --- Eccezioni: una approvata (collegata a un rischio), due in attesa.
risks = dm.call("GET", V + "/risks")["items"]
risk = risks[0]["key"] if risks else ""
until = lambda days: (dt.date.today() + dt.timedelta(days=days)).isoformat()
asked = 0
def ask(findings, reason, days, comp="", with_risk=False):
    global asked
    if not findings:
        return None
    r = dm.call("POST", V + "/exceptions", {"ids": [i["id"] for i in findings], "reason": reason, "until": until(days), "compensating": comp,
                                            "risk": risk if with_risk and risk else ""}, tolerate=(422,))
    if "_error" in r:
        return None
    asked += len(r["items"])
    return r["items"][0]

if med:
    e = ask(med[:1], "La libreria è usata solo da uno strumento interno non raggiungibile dall'esterno.", 60,
            "Strumento dietro VPN, accesso limitato al team di piattaforma.", True)
    if e and e["status"] == "pending":
        full = arch.call("GET", f"/api/v1/elements/{e['application_key']}")
        step = next((s["id"] for a in full.get("approvals", []) if a["status"] == "pending" for s in a["steps"] if s["decision"] == "pending"), None)
        if step:
            arch.call("POST", f"/api/v1/approvals/steps/{step}/decision", {"decision": "approved", "comment": "Rischio accettabile per due mesi."}, tolerate=(403, 409, 422))
if high[2:3]:
    ask(high[2:3], "La correzione richiede un aggiornamento maggiore del framework: pianificato nel prossimo trimestre.", 90, "Regola WAF attiva sul percorso interessato.", True)
if med[2:4]:
    ask(med[2:4], "Falsi allarmi dello scanner in attesa di una regola di esclusione condivisa.", 30)

print(f"Vulnerabilità: politica salvata ({saved['recomputed']} scadenze ricalcolate), {asked} eccezioni richieste.")
PY
