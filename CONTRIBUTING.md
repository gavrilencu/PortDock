# Contributing to PortDock

Thanks for helping! Bug reports, ideas, translations and pull requests are all welcome.

## Reporting a bug or asking for a feature

Open an [issue](https://github.com/gavrilencu/PortDock/issues) with:

- the PortDock version (Settings → About) and how it runs (installer, `docker run`, remote servers…);
- what you did, what you expected and what happened;
- relevant logs: `docker logs portdock`, the deployment log, or the browser console.

Security problems don't go in public issues — see [SECURITY.md](SECURITY.md).

## Working on the code

```bash
make dev-api   # backend on :8000, data in ./.data
make dev-ui    # Vite on :5173, proxying /api to :8000
make test      # go vet, go test and the TypeScript check
```

- **Backend** (Go, `backend/`): keep the standard library style of the surrounding code, add a test next to what you change (`go test ./...`).
- **Frontend** (React + TypeScript, `frontend/`): run `npx tsc -b`. Every text shown to users goes through `t("…")`; add its Romanian and Russian translations in `frontend/src/locales/` and check with `npm run i18n:check`.
- **Releases** take their description from `docs/releases/vX.Y.Z.md`.

## Pull requests

1. Fork the repository and create a branch from `main`.
2. Keep a pull request focused on one change, with a short description of what and why.
3. Make sure `make test` passes.

By contributing, you agree that your contributions are licensed under the [Apache License 2.0](LICENSE), like the rest of PortDock.
