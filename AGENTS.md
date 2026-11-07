# kiso — shared core for zumen and tomo

This file applies to agents working on the kiso repo itself.

## Non-negotiable rules

- Go >= 1.22, standard library only — no third-party runtime or build deps.
  `go test ./...` must stay green from a clean checkout.
- The core knows nothing about specific tools, phases, folders or personas.
  Each tool passes in its own configuration. Migrations from a tool's old
  layouts live in that tool, not here.
- This is a public API with two consumers. A breaking change needs a new tag
  and a bump in both zumen and tomo.

## Layout

```
scaffold/    embedded-FS scaffold engine: init, update, check, manifest hashes
settings/    defaults ← global ← project merge
workspace/   the local .<tool>/ directory and active-change pointer
changelog/   append-only log writer
version/     version reporting (build info)
```

## Development

- `go test ./...` — run all tests.
- `go vet ./...` — run static checks.
