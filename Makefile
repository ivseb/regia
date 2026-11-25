# Comandi di sviluppo di Regia. Richiede Go, pnpm e i binari di Postgres (>= 16; il pacchetto usa la 18).
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo sconosciuto)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)
DATA    ?= ./data
GO_LICENSES ?= github.com/google/go-licenses/v2@v2.0.1

.PHONY: all web build e2e test vet run seed licenses licenses-report docker doctor backup ci clean

all: web build

web:
	cd web && pnpm install --frozen-lockfile && pnpm build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/regia ./cmd/regia

# Test end-to-end: ricompila il frontend e il binario (make build da solo non ricompila il frontend:
# test contro un'interfaccia vecchia danno falsi errori) e poi esegue Playwright.
# Porta del server di prova: E2E_PORT (8140-8149).
e2e: web build
	cd web && pnpm e2e

vet:
	go vet ./...
	cd web && pnpm typecheck
	node scripts/check-contrast.mjs --quiet

test:
	go test -race -count=1 ./...
	cd web && pnpm test

run: build
	./bin/regia serve --data $(DATA) --dev --http 127.0.0.1:8080

# Crea i tenant di prova (vedi scripts/dev-seed.sh).
seed: build
	DATA=$(DATA) ./scripts/dev-seed.sh

licenses:
	go run $(GO_LICENSES) check ./... --ignore regia --disallowed_types=forbidden,restricted,unknown
	cd web && node ../scripts/check-npm-licenses.mjs

# Elenco delle dipendenze con le licenze (docs/licenza.md).
licenses-report:
	GO_LICENSES=$(GO_LICENSES) node scripts/licenses-report.mjs > docs/licenze-dipendenze.md

# Controlli e backup dell'istanza locale (docs/operativo.md).
doctor: build
	./bin/regia doctor --data $(DATA)

backup: build
	./bin/regia backup --data $(DATA)

# Gli stessi passi della CI (.github/workflows/ci.yml), tranne l'immagine Docker.
ci: web vet test build licenses

docker:
	docker build -f deploy/Dockerfile --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t regia:$(VERSION) .

clean:
	rm -rf bin internal/webui/dist/app
