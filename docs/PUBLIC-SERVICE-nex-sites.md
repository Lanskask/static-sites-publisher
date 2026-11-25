# nex-sites as a stand-alone public service

How the drag-and-drop publisher (`src/` plus a static file server) could run as its own public product, and what
protects its reputation. Facts about outside rules (Let's Encrypt limits, Public Suffix List, Safe Browsing) were
written from memory of their public documentation and are unverified: check each source before you rely on it.

## 1. What changes when strangers upload

Today one trusted owner uploads. With the public, every uploaded file is hostile until shown otherwise. The two real
risks are not hackers breaking in but **abuse of the domain's reputation**: phishing pages, malware downloads and spam
sites hosted on `*.<your domain>`. One flagged page can get the whole domain blocked in browsers, or the Oracle account
closed for a terms-of-use breach. Everything below serves that one goal.

## 2. Domains

| Name role     | Example              | Why it is separate                                                                   |
| ------------- | -------------------- | ------------------------------------------------------------------------------------ |
| Brand and web | `nexsites.dev`       | Marketing site, login, admin, email. Must never share a registered domain with pages |
| Pages         | `nexsites-pages.app` | Only user content. If it is flagged, the brand domain and its email are untouched    |
| Custom (user) | `blog.customer.com`  | Optional, later: the customer's own name pointing at their page                      |

Rules:

- **Two registered domains, not one with two subdomains.** Browsers and blocklists judge reputation per registered
  domain (or per Public Suffix List entry, below). User content on `pages.nexsites.dev` shares fate with `nexsites.dev`.
- Register both for several years and enable registrar lock. A cheap TLD with a bad reputation (`.xyz`, `.top`) is
  blocked by many mail and security filters; prefer `.app`, `.dev`, `.page` or `.com`.
- DuckDNS does not work for this: you do not control the zone, you cannot add an NS or TXT record at will, and it cannot
  be on the Public Suffix List. Move DNS to a provider with an API (Cloudflare, Route 53, Hetzner DNS).

## 3. Marking every page as "not the same site": the Public Suffix List

The **Public Suffix List** (PSL, `publicsuffix.org`) is the list browsers use to decide where "one site" ends.
`github.io`, `vercel.app` and `pages.dev` are on it. Entries on it get three protections:

1. **Cookies and storage are isolated.** `a.nexsites-pages.app` cannot set a cookie for `b.nexsites-pages.app` or for
   the whole domain (the "supercookie" rule). This is already why the admin lives on another name today.
2. **Reputation is judged per subdomain.** Chrome and Firefox Safe Browsing and Microsoft SmartScreen treat each
   `<page>.nexsites-pages.app` as its own site, so a phishing page blocks that one address, not the whole domain.
   Without a PSL entry the blocklist can flag the registered domain and every page goes down at once.
3. Browsers and mail tools stop treating the pages as one organisation (no shared "same-site" credentials, separate
   address-bar highlighting of the registrable part).

How: open a pull request against `publicsuffix/list` adding `nexsites-pages.app` to the private section, with a
`_psl.<domain>` TXT record proving you control it and a short description of the service. Their guidelines ask for a
domain registered for a long period and a real, running service; read the current `CONTRIBUTING` before applying. Review
takes weeks and browsers pick the change up over their release cycle, so submit **before** launch.

Do it together with these cheap, immediate signals:

- `X-Robots-Tag: noindex` on the free tier by default (search spam is a main motive for abuse), removable on paid pages.
- `Referrer-Policy` and `X-Content-Type-Options: nosniff` on every page (nginx already sends the second).
- A visible, **Report abuse** link on the 404 page and, if possible, a one-line footer injected by the platform on free
  pages. Reviewers at Google and registrars look for a working abuse channel.
- A `security.txt` and an `abuse@` mailbox for the brand domain, answered within a day. Registrars and Oracle forward
  complaints to it.

## 4. Certificates

- **Pages: one wildcard certificate** `*.nexsites-pages.app` by DNS-01. A wildcard avoids one certificate per page,
  which would hit the Let's Encrypt limit on new certificates per registered domain per week (50 at the time of writing)
  and would publish every page name in Certificate Transparency logs. Today: Traefik resolver `le-dns` with the DuckDNS
  provider. Public: the same resolver with your DNS provider's API token, **scoped to that one zone**.
- **Brand and admin: normal HTTP-01** per host, as `nex-core` and `nex-sites-admin` do now.
- **Custom customer domains:** a customer points a CNAME at `custom.nexsites.dev`. Each name needs its own certificate,
  ordered the first time it is requested. Traefik cannot order on demand; Caddy can (`on_demand_tls` with an `ask`
  endpoint that answers 200 only for names a user has registered, which prevents anyone from burning your rate limit).
  Verify domain ownership with a TXT record before accepting the name. Leave this for later; the wildcard is enough for
  launch.
- **Storage and renewal:** certificates renew by themselves about 30 days before expiry. They live in `/data/acme.json`
  on a local-path volume. Back that file up (encrypted) and keep **staging first** when changing anything (see
  `docs/RUNBOOK-sites.md`: Traefik reuses a stored certificate and does not re-order it when you switch CA).
- **More than one Traefik replica** needs a shared certificate store, which open-source Traefik does not have. Use
  cert-manager (certificates as Secrets in the cluster) at that point.
- Contact email for the ACME account: a role address (`certs@`), not a personal one: it appears in logs.

## 5. Keeping abuse out and cutting it off fast

Layers, cheapest first. The first two exist in the code now.

1. **Built-in scan on upload (`src/scan.go`, done):** refuses programs and server-side scripts (`.exe`, `.php`, `.sh`,
   `.jar` and others), files whose first bytes say executable (PE, ELF, Mach-O, `#!`), and HTML or SVG with a password
   field. It cannot catch a clever fake login without a password field or a link to malware elsewhere.
2. **Optional ClamAV (`CLAMD_ADDR`, code done, daemon not deployed):** every file goes to `clamd`; a failed scan fails
   the upload. `clamd` needs about 1 GB of RAM for its signature database, more than this VM can spare today.
3. **Link and reputation check of published URLs:** ask Google Safe Browsing (Lookup API v4 is free for non-commercial
   use; Web Risk is the paid, commercial one) about each new page and recheck all pages daily. On a hit, suspend the
   page and tell the owner.
4. **Suspend and takedown:** a per-page "suspended" flag that makes nginx answer `451` or `410` and a branded page;
   admin-only action; owner notified by email. Deleting a user suspends all of their pages. Keep the files for evidence
   for a fixed period. Report channel: a form plus `abuse@`.
5. **Signup friction:** require a GitHub or Google account at least a few weeks old, verified email, rate limit new
   accounts (for example 3 pages in the first day), and a block list of names that imitate brands (`paypal`, `google`,
   `login`, `secure`, bank names) in the page-name check. Most phishing is dropped here.
6. **Rate and bandwidth limits:** per-IP `limit_req` in nginx, a per-user daily upload cap, and `limit_rate` or a
   monthly traffic cap per page. A static site that goes viral can otherwise use the VM's whole network quota.
7. **Hide the origin:** put a CDN (Cloudflare free) in front of the pages domain. It absorbs traffic spikes and attacks,
   and many abuse reports go to the CDN first, which gives you time. Wildcard DNS-01 still works behind it.

What stays impossible to prevent: a page that looks harmless on upload and changes later (JavaScript fetches malware
from another host). The answer is steps 3 and 4: recheck, and be able to cut a page off within minutes.

## 6. Hosting and legal

- Check **Oracle Cloud's Acceptable Use Policy** and whether a free-tier account may run a public hosting service. Many
  providers close accounts after phishing complaints. A paid Hetzner or similar VM with a clearer policy is the usual
  home for such a service.
- Terms of service (no malware, phishing, illegal content, copyright infringement), privacy notice (the service stores
  the login identity and IP logs; GDPR if EU users), and a **copyright takedown process** (a published agent, a
  counter-notice path) before launch. Ask a lawyer; this document is not legal advice.
- Logs: keep access logs short (for example 14 days) and write down who may read them.
- Payment (paywall above 30 pages): Stripe Checkout with webhooks; the admin stays a single binary, the quota moves from
  a constant to a field in the owner's record.

## 7. Multi-tenant hardening already in the design

nginx serves only `/srv/sites/pages` read-only with `disable_symlinks on`; the admin writes through a temp directory and
two renames; zip bombs stop at 20 MB while streaming; the session cookie is `__Host-` prefixed and POST checks `Origin`;
ownership is checked on every delete. For the public add: per-user directories or names, a pod per tenant is **not**
needed because nothing uploaded is ever executed.

## 8. Launch checklist

1. Buy the two domains, move DNS to an API provider, submit the PSL pull request.
2. Wildcard certificate by DNS-01 with a zone-scoped token; staging first.
3. Reputation check, suspend action, abuse form and mailbox, brand-name block list.
4. Signup rules, rate limits, CDN in front.
5. Terms, privacy notice, takedown process, a provider whose policy allows hosting.
6. Backups of `/srv/sites` and the certificate file; a restore test.
7. Only then open the allowlist (`ALLOWLIST` to a signup rule).
