# Decision log

## 2026-11-23: One Go binary, files on disk, no database

- The admin is one Go binary with the standard library plus `golang.org/x/oauth2`: small to audit, a static binary for
  any server, nothing to install (best practice: fewest dependencies for a security-facing service).
- Pages and metadata are plain files: `pages/<name>/` (served as is), `meta/<name>.json`, and `tmp/` for uploads in
  progress. A publish writes to `tmp/` and swaps the page in with renames, so a visitor never sees half a page and a
  failed upload leaves the old version. Backup is one `tar` of the volume. A database would add a service to run for 30
  pages per user.
- Each page is its own subdomain, `<name>.<PAGES_DOMAIN>`, not a path: pages cannot read each other's cookies or
  storage, and absolute links (`/style.css`) inside a page work unchanged (industry standard: GitHub Pages, Netlify).

## 2026-11-23: Login with GitHub or Google and an allowlist; no passwords

- OAuth only, with an allowlist of `github:<id>` or `google:<sub>` identities (owner's requirement: no password store to
  protect, reset or leak). The allowlist keeps the service private; opening it to the public is a separate decision, see
  `docs/PUBLIC-SERVICE-nex-sites.md`.
- Identities are the provider's numeric id or `sub`, not the login name, which can be renamed and reused. A GitHub and a
  Google login of the same person are two owners.
- The session is an HMAC-signed `__Host-session` cookie: no server-side session store, and the `__Host-` prefix means a
  page subdomain can never set or overwrite it. Writes from a session must carry the admin's own `Origin` (CSRF check).
- `DEV_IDENTITY` logs in without OAuth for local runs, and is ignored unless `BASE_URL` is `localhost`.

## 2026-11-23: Owner-scoped pages with quotas; `admin` and other names reserved

- Every page records its owner; publish, tag and delete check it, and another owner's page reads as "name taken". Names
  are DNS labels (`a-z`, `0-9`, `-`), and `admin`, `www`, `api`, `mail` and `static` are reserved so a page can never
  take the admin's host or a conventional service name.
- Limits per owner: 30 pages, 20 MB per page (counted while unzipping, so a zip bomb stops early), 300 MB in total, 2000
  files per page. They bound the disk a single account can use on a small VM.

## 2026-11-24: Upload scanning, API tokens, tags

- Uploads are scanned by built-in rules (`src/scan.go`): programs and server-side scripts by extension and by their
  first bytes, and HTML or SVG with a password field (the usual phishing page). ClamAV is optional (`CLAMD_ADDR`)
  because `clamd` needs about 1 GB of RAM for its signatures, more than a free-tier VM has. A failed scan fails the
  upload.
- API tokens for scripts and AI agents are stored as SHA-256 hashes and shown once. A token can publish, list, tag and
  delete pages but cannot create, list or revoke tokens; that needs a browser session, so a leaked token cannot make
  itself permanent.
- Tags live in the page's metadata and survive republishing; they are lowercase DNS-like words, up to 10 per page.

## 2026-11-24: Go code in `src/`, generic self-host with Docker Compose and Caddy

- Code moved to `src/` so the repo root holds only docs, `Makefile`, `deploy/` and `scripts/` (owner's request).
- The self-host stack is Docker Compose with Caddy, not k3s and Traefik as on the author's Oracle VM: it runs on any
  Linux server with Docker, and Caddy handles HTTPS with no extra setup.
- Certificates are issued per page with Caddy's on-demand TLS (HTTP-01), not one wildcard certificate (DNS-01). DNS-01
  needs a Caddy build with a plugin and an API token for each DNS provider; HTTP-01 works with any provider, which the
  owner asked for. The cost is one certificate per page against Let's Encrypt's weekly limit per registered domain, and
  a TLS error instead of the 404 page for unpublished names. The admin's `GET /tls-check` lets Caddy issue only for
  published pages, so strangers cannot make it request certificates for random names.
- Deploy and DNS helpers are stdlib Python (`scripts/`), run over ssh, so they need no cloud SDK.

## 2026-11-24: End-to-end test with Pebble; `/tls-check` allows the admin host

- `make e2e` runs the real `deploy/` stack with Pebble, Let's Encrypt's test ACME server, because Let's Encrypt cannot
  reach a development machine. It uses the same HTTP-01 challenge and on-demand flow as production.
- Its first run found a bug: with `ADMIN_HOST=admin.PAGES_DOMAIN` (the example default), the admin host also matches the
  on-demand `*.PAGES_DOMAIN` site, so Caddy asked `/tls-check`, which refused the reserved name `admin`, and the admin
  never got a certificate. `/tls-check` now also allows the host of `BASE_URL`.

## 2026-11-24: Separate repository, MIT licence, stdlib Python scripts

- The admin moved out of the OracleCloud infrastructure repo into its own repo, so it can be self-hosted anywhere
  without the author's k3s setup; OracleCloud now builds from a fresh clone of this repo's `main`.
- MIT licence: the shortest permissive licence, so anyone can self-host or fork it.
- Helper scripts are Python with only the standard library, not shell: testable with `unittest`, no packages to install,
  and the same on macOS and Linux (BSD and GNU tools differ).

## 2026-11-25: Real-world test on a throwaway GCP VM

- `deploy/e2e/gcp/main.tf` creates a plain Debian VM with Docker (`make tf-plan-apply`, `make tf-destroy`) to test what
  Pebble cannot: real DNS, real Let's Encrypt and real OAuth. It is throwaway infrastructure, with local Terraform state
  that is never committed.
- Its first run found three bugs, all fixed:
  - `make deploy` used `rsync`, which a plain Debian server does not have. It now streams `tar` over ssh, so the server
    needs only ssh, `tar` and Docker; `deploy/e2e` (local test files and Terraform state) is not copied.
  - A redeploy did not apply a changed `Caddyfile`: the file is replaced by a new one, and the running container keeps
    the old one bind-mounted. `docker compose up` now runs with `--force-recreate`.
  - Caddy 2.10 gives no certificate of its own to a name covered by a wildcard site, and the `*.PAGES_DOMAIN` site's
    certificates are on-demand, so `admin.PAGES_DOMAIN` got none ("no certificate available"). The admin's site block is
    now `on_demand` too; `/tls-check` already allows the admin host.
