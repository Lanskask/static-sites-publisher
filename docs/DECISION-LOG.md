# Decision log

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
