# static-sites-publisher as a stand-alone public service

How the drag-and-drop publisher (`src/` plus a static file server) could run as its own public product, and what
protects its reputation. Facts about outside rules (Let's Encrypt limits, Public Suffix List, Safe Browsing) were
written from memory of their public documentation and are unverified: check each source before you rely on it.

## 1. What changes when strangers upload

Today one trusted owner uploads. With the public, every uploaded file is hostile until shown otherwise. The two real
risks are not hackers breaking in but **abuse of the domain's reputation**: phishing pages, malware downloads and spam
sites hosted on `*.<your domain>`. One flagged page can get the whole domain blocked in browsers, or the hosting account
closed for a terms-of-use breach. Everything below serves that one goal.

## 2. Domains

| Name role     | Example                   | Why it is separate                                                                   |
| ------------- | ------------------------- | ------------------------------------------------------------------------------------ |
| Brand and web | `sitepublisher.dev`       | Marketing site, login, admin, email. Must never share a registered domain with pages |
| Pages         | `sitepublisher-pages.app` | Only user content. If it is flagged, the brand domain and its email are untouched    |
| Custom (user) | `blog.customer.com`       | Optional, later: the customer's own name pointing at their page                      |

Rules:

- **Two registered domains, not one with two subdomains.** Browsers and blocklists judge reputation per registered
  domain (or per Public Suffix List entry, below). User content on `pages.sitepublisher.dev` shares fate with
  `sitepublisher.dev`.
- Register both for several years and enable registrar lock. A cheap TLD with a bad reputation (`.xyz`, `.top`) is
  blocked by many mail and security filters; prefer `.app`, `.dev`, `.page` or `.com`.
- DuckDNS does not work for this: you do not control the zone, you cannot add an NS or TXT record at will, and it cannot
  be on the Public Suffix List. Move DNS to a provider where you control the zone (Cloudflare, Route 53, Hetzner DNS).

## 3. Marking every page as "not the same site": the Public Suffix List

The **Public Suffix List** (PSL, `publicsuffix.org`) is the list browsers use to decide where "one site" ends.
`github.io`, `vercel.app` and `pages.dev` are on it. Entries on it get three protections:

1. **Cookies and storage are isolated.** `a.sitepublisher-pages.app` cannot set a cookie for `b.sitepublisher-pages.app`
   or for the whole domain (the "supercookie" rule). This is already why the admin lives on another name today.
2. **Reputation is judged per subdomain.** Chrome and Firefox Safe Browsing and Microsoft SmartScreen treat each
   `<page>.sitepublisher-pages.app` as its own site, so a phishing page blocks that one address, not the whole domain.
   Without a PSL entry the blocklist can flag the registered domain and every page goes down at once.
3. Browsers and mail tools stop treating the pages as one organisation (no shared "same-site" credentials, separate
   address-bar highlighting of the registrable part).

How: open a pull request against `publicsuffix/list` adding `sitepublisher-pages.app` to the private section, with a
`_psl.<domain>` TXT record proving you control it and a short description of the service. Their guidelines ask for a
domain registered for a long period and a real, running service; read the current `CONTRIBUTING` before applying. Review
takes weeks and browsers pick the change up over their release cycle, so submit **before** launch.

Do it together with these cheap, immediate signals:

- `X-Robots-Tag: noindex` on the free tier by default (search spam is a main motive for abuse), removable on paid pages.
- `Referrer-Policy` and `X-Content-Type-Options: nosniff` on every page (Caddy already sends the second).
- A visible, **Report abuse** link on the 404 page and, if possible, a one-line footer injected by the platform on free
  pages. Reviewers at Google and registrars look for a working abuse channel.
- A `security.txt` and an `abuse@` mailbox for the brand domain, answered within a day. Registrars and hosting providers
  forward complaints to it.

## 4. Certificates

- **Pages: one certificate per page, on demand**, as the self-host stack does now: Caddy orders it by HTTP-01 on the
  first visit, and the admin's `/tls-check` allows only published names. No DNS API token is needed. Two limits: Let's
  Encrypt's weekly limit per registered domain (see the notes under [Operations](../README.md#operations) in the
  README), and every page name appears in public Certificate Transparency logs. A PSL entry (section 3) makes each page
  its own registered domain for that limit; until it is accepted, more than about 50 new pages a week needs a wildcard.
- **Wildcard, if needed:** `*.<pages domain>` by DNS-01. The stock `caddy` image has no DNS provider; build one with
  `xcaddy` and the provider's module, and give it an API token **scoped to that one zone**.
- **Brand and admin: a normal certificate** per host, as the stack's admin has now.
- **Custom customer domains:** a customer points a CNAME at `custom.<brand domain>`. Each name gets its own certificate
  on demand, with `/tls-check` extended to names a user has registered. Verify domain ownership with a TXT record before
  accepting the name. Leave this for later.
- **Storage and renewal:** Caddy renews certificates by itself well before expiry and keeps them in the `caddy-data`
  volume. Back it up (encrypted). Try any change to TLS settings against Let's Encrypt's staging CA first (the `acme_ca`
  global option) so a mistake does not use up the real limit.
- **More than one Caddy instance:** give them the same storage (a shared volume, or a storage module such as Redis or
  S3); Caddy then coordinates who orders and renews.
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
4. **Suspend and takedown:** a per-page "suspended" flag that makes Caddy answer `451` or `410` and a branded page;
   admin-only action; owner notified by email. Deleting a user suspends all of their pages. Keep the files for evidence
   for a fixed period. Report channel: a form plus `abuse@`.
5. **Signup friction:** require a GitHub or Google account at least a few weeks old, verified email, rate limit new
   accounts (for example 3 pages in the first day), and a block list of names that imitate brands (`paypal`, `google`,
   `login`, `secure`, bank names) in the page-name check. Most phishing is dropped here.
6. **Rate and bandwidth limits:** per-IP request limits (the CDN below, or a rate-limit module: the standard Caddy build
   has none), a per-user daily upload cap, and a monthly traffic cap per page from the access logs. A static site that
   goes viral can otherwise use the VM's whole network quota.
7. **Hide the origin:** put a CDN (Cloudflare free) in front of the pages domain. It absorbs traffic spikes and attacks,
   and many abuse reports go to the CDN first, which gives you time. Wildcard DNS-01 still works behind it.

What stays impossible to prevent: a page that looks harmless on upload and changes later (JavaScript fetches malware
from another host). The answer is steps 3 and 4: recheck, and be able to cut a page off within minutes.

## 6. Hosting and legal

- Check the hosting provider's **Acceptable Use Policy** and whether the account (free tiers especially) may run a
  public hosting service. Many providers close accounts after phishing complaints. A paid VM whose policy allows it is
  the usual home for such a service.
- Terms of service (no malware, phishing, illegal content, copyright infringement), privacy notice (the service stores
  the login identity and IP logs; GDPR if EU users), and a **copyright takedown process** (a published agent, a
  counter-notice path) before launch. Ask a lawyer; this document is not legal advice.
- Logs: keep access logs short (for example 14 days) and write down who may read them.
- Payment (paywall above 30 pages): Stripe Checkout with webhooks; the admin stays a single binary, the quota moves from
  a constant to a field in the owner's record.

## 7. Multi-tenant hardening already in the design

Caddy mounts the pages volume read-only and serves each name only from its own directory; uploads containing symlinks
are refused; the admin writes through a temp directory and two renames; zip bombs stop at 20 MB while streaming; the
session cookie is `__Host-` prefixed and POST checks `Origin`; ownership is checked on every delete. For the public add:
per-user directories or names, a pod per tenant is **not** needed because nothing uploaded is ever executed.

## 8. Launch checklist

1. Buy the two domains, move DNS to an API provider, submit the PSL pull request.
2. Certificates: per page on demand until the PSL entry lands, or a wildcard by DNS-01; staging first.
3. Reputation check, suspend action, abuse form and mailbox, brand-name block list.
4. Signup rules, rate limits, CDN in front.
5. Terms, privacy notice, takedown process, a provider whose policy allows hosting.
6. Backups of the pages and the certificate store, and a restore test: see [Operations](../README.md#operations) in the
   README.
7. Only then open the allowlist (`ALLOWLIST` to a signup rule).
