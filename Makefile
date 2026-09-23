.PHONY: dev-api dev-ui build test docker

# Backend on :8000 with permissive CORS; data kept in ./.data
dev-api:
	cd backend && SHIPYARD_DEV=1 SHIPYARD_DATA_DIR=$(CURDIR)/.data go run ./cmd/shipyard

# Vite dev server on :5173 proxying /api to :8000
dev-ui:
	cd frontend && npm run dev

build:
	cd frontend && npm ci && npm run build
	rm -rf backend/web/dist && cp -r frontend/dist backend/web/dist
	cd backend && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ../bin/shipyard ./cmd/shipyard

test:
	cd backend && go vet ./... && go test ./...
	cd frontend && npx tsc -b

docker:
	docker compose build
