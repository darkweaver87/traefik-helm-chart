# Migrate values from chart v40 to v41

Chart **v41.0.0** renamed the logging keys to match upstream Traefik
([PR #1887](https://github.com/traefik/traefik-helm-chart/pull/1887)). This Go helper
rewrites your own values override accordingly — **no `yq`, no Docker, no shell/PowerShell
scripts**: a single static binary that runs anywhere Go runs.

| Before (v40) | After (v41) |
|---|---|
| `logs.general.*` | `log.*` |
| `logs.access.*` | `accessLog.*` |
| `logs.access.filters.statuscodes` | `accessLog.filters.statusCodes` |
| `logs.access.filters.retryattempts` | `accessLog.filters.retryAttempts` |
| `logs.access.filters.minduration` | `accessLog.filters.minDuration` |
| `logs.access.fields.general.defaultmode` | `accessLog.fields.defaultMode` |
| `logs.access.fields.general.names` | `accessLog.fields.names` |
| `logs.access.fields.headers.defaultmode` | `accessLog.fields.headers.defaultMode` |
| `logs.access.fields.queryParameters.defaultmode` | `accessLog.fields.queryParameters.defaultMode` |

## Usage

Run against **your override file** (the one you pass with `helm -f`), not the chart's `values.yaml`.

```sh
# dry-run: print the migrated YAML to stdout
go run github.com/traefik/traefik-helm-chart/hack/migrate@latest myvalues.yaml

# rewrite in place (backup: myvalues.yaml.bak)
go run github.com/traefik/traefik-helm-chart/hack/migrate@latest -i myvalues.yaml
```

Or build once and reuse the binary (works on Linux, macOS, Windows — no runtime deps):

```sh
go build -o migrate . && ./migrate -i myvalues.yaml
```

## Notes

- **Comments and key order are preserved** (the file is rewritten via the YAML node
  tree, not re-serialized from scratch).
- **Idempotent**: running it again on an already-migrated file is a no-op.
- **`providers.file.content`** changed from a string to an object in v41. The tool
  **converts it for you**: it parses the embedded YAML and inlines it as an object
  (comments preserved). If the value can't be parsed as a YAML object it's left
  untouched with a warning, so you can migrate that one by hand.

`sample-v40-values.yaml` exercises every mapped key; see `main_test.go`.
