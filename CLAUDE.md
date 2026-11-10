# jenktui

Go + Bubble Tea terminal UI for Jenkins. Read [docs/HANDOFF.md](docs/HANDOFF.md) first: it has the layout, settled decisions, conventions and Jenkins gotchas. Remaining work is in [TODO.md](TODO.md); terms are in [GLOSSARY.md](GLOSSARY.md).

Before committing: `gofmt -l .`, `go vet ./...`, `go test -count=1 ./...` (Go is at `/opt/homebrew/bin/go`; add it to `PATH`).

- Screens never import each other. Dependencies come in through constructors and are wired in `main.go`.
- Never store credentials. Never push or release without the owner asking.
- Update README, GLOSSARY and `docs/ui-contract.md` in the same change as the behaviour they describe.
