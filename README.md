# sites-admin

The admin for nex-sites: a small Go service where the owner drops a folder, a `.zip` or an `.html` file and gets it
published at `https://<page>.<PAGES_DOMAIN>`. It has no database; pages and metadata are plain files under `SITES_DIR`.

## What it does

- Logs in with GitHub or Google. Identity is `github:<id>` or `google:<sub>`; only identities on `ALLOWLIST` get in.
- Publishes, lists and deletes pages, with a per-user quota and an optional ClamAV scan (`CLAMD_ADDR`).
- Issues API tokens (hashed on disk) so scripts can deploy; tokens cannot create more tokens.

## Run it

```bash
make          # list the commands
make run      # http://localhost:8080 with a fake login (dev:me)
make test     # go vet, golangci-lint, go test -race
make build    # linux/arm64 static binary for the VM
make deploy   # copy the binary to the VM and restart the pod (VPS_HOST, default k3s-oracle)
```

The VM, k3s manifests (`pyinfra/files/sites.yaml`), Secrets and backups live in the separate `OracleCloud` repo.

## Configuration

| Variable       | Meaning                                                    |
| -------------- | ---------------------------------------------------------- |
| `BASE_URL`     | The admin's own origin, used for OAuth callbacks and CSRF  |
| `PAGES_DOMAIN` | Domain pages are served under                              |
| `SITES_DIR`    | Where pages, metadata and tokens live (default `./data`)   |
| `ALLOWLIST`    | Comma-separated identities allowed in                      |
| `SESSION_KEY`  | HMAC key for session cookies                               |
| `DEV_IDENTITY` | Fixed login for local runs; ignored on a public `BASE_URL` |
| `CLAMD_ADDR`   | Optional `host:port` of a clamd daemon                     |
| `ADDR`         | Listen address (default `:8080`)                           |

OAuth client IDs and secrets come from further environment variables, read in `main.go`.

## Layout

| Path        | Purpose                                                 |
| ----------- | ------------------------------------------------------- |
| `main.go`   | Config and routes                                       |
| `auth.go`   | OAuth login, signed sessions, allowlist, CSRF           |
| `store.go`  | Publish, replace, list and delete pages; quota          |
| `tokens.go` | API tokens                                              |
| `scan.go`   | Optional ClamAV scan                                    |
| `static/`   | The admin UI (embedded in the binary)                   |
| `docs/`     | `PUBLIC-SERVICE-nex-sites.md`, the public-service notes |

Operations are in `docs/RUNBOOK-sites.md` at the repo root.
