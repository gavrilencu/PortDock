.PHONY: dev-api dev-ui build test docker docs-sync

# Backend on :8000 with permissive CORS; data kept in ./.data
dev-api:
	cd backend && PORTDOCK_DEV=1 PORTDOCK_DATA_DIR=$(CURDIR)/.data go run ./cmd/portdock

# Vite dev server on :5173 proxying /api to :8000
dev-ui:
	cd frontend && npm run dev

build:
	cd frontend && npm ci && npm run build
	rm -rf backend/web/dist && cp -r frontend/dist backend/web/dist
	cd backend && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ../bin/portdock ./cmd/portdock

test:
	cd backend && go vet ./... && go test ./...
	cd frontend && npx tsc -b

docker:
	docker compose build

# The AI assistant embeds the documentation (backend/internal/assistant/docs);
# TestDocsInSync fails when these copies drift from the sources.
docs-sync:
	cp README.md backend/internal/assistant/docs/README.md
	cp backend/internal/templates/services/README.md backend/internal/assistant/docs/service-templates.md
