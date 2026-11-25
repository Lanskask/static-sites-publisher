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

## 2026-11-24: End-to-end test with Pebble; `/tls-check` allows the admin host

- `make e2e` runs the real `deploy/` stack with Pebble, Let's Encrypt's test ACME server, because Let's Encrypt cannot
  reach a development machine. It uses the same HTTP-01 challenge and on-demand flow as production.
- Its first run found a bug: with `ADMIN_HOST=admin.PAGES_DOMAIN` (the example default), the admin host also matches the
  on-demand `*.PAGES_DOMAIN` site, so Caddy asked `/tls-check`, which refused the reserved name `admin`, and the admin
  never got a certificate. `/tls-check` now also allows the host of `BASE_URL`.

## 2026-11-24: Page editor: source editor with live preview, server-side drafts

- The editor edits the HTML source (CodeMirror 6) next to a live preview, not a WYSIWYG or page-builder library. Pages
  are mostly single AI-generated files with inline `<style>` and `<script>`; rich-text editors and builders (TipTap,
  Quill, CKEditor, GrapesJS) parse them into their own model and drop or rewrite those parts. A source editor keeps the
  page byte for byte. Neocities (Ace) and GitHub's web editor work the same way. Owner's choice from
  `docs/plans/wysiwyg-editor.md`.
- Edits are saved as per-owner drafts (`drafts/<owner hash>/<name>.json`) and go live only on Publish, so a half-done
  edit never reaches the live page (owner's request). Publish refuses with 412 when the page changed after the draft was
  started, so an upload made meanwhile (for example with an API token) is not overwritten silently.
- The preview is `/preview`, served with `Content-Security-Policy: sandbox allow-scripts ...` and no
  `allow-same-origin`: the page runs its own scripts with an opaque origin, so it has no admin cookies and its writes
  carry `Origin: null`, which the admin refuses. A `srcdoc` iframe would inherit the admin's CSP and block the page's
  inline styles and scripts.
- CodeMirror is vendored as one esbuild bundle (`src/static/vendor/codemirror.js`, `make vendor-editor`), because the
  admin's CSP (`default-src 'self'`) allows no CDN. It is mounted in a shadow root: there it styles itself with
  constructable stylesheets, while on the document it would add `<style>` tags that the CSP blocks (measured in Chrome,
  `scripts/editor_check.py`).
- Favicons are set by the server with the `golang.org/x/net/html` tokenizer and byte offsets, so the rest of the page is
  untouched; only `data:image/...` URLs under 64 KB are accepted.

## 2026-11-25: Visual text editing: `designMode` on the real page, result goes into the source

- **Edit text visually** (owner asked for WYSIWYG; Phase 2 of `docs/plans/wysiwyg-editor.md`) turns on `designMode` for
  the page itself, not a rich-text library, so `<head>`, `<style>` and the doctype survive.
- Offered only for pages without `<script>`: the editable copy runs no scripts. `/preview?mode=edit` has a CSP
  `script-src 'nonce-...'` (new nonce per request) that lets only the shell run; the page is parsed with `DOMParser` and
  its scripts are removed. Still `sandbox allow-scripts` without `allow-same-origin`.
- Each edit posts `outerHTML` back into the source buffer instead of saving: `outerHTML` reformats the markup (quotes,
  whitespace), so the owner sees the change in the source before Publish. Byte-exact output is not possible in this
  mode; the source editor stays the lossless path.
