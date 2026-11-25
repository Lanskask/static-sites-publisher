# E2E test results on GCP

This page records the three real runs of `make gcp-e2e` on 2026-11-24 and what changed between them. The test creates a
throwaway GCP VM, points a DuckDNS name at it, deploys the stack, runs the checks, and destroys the VM. Every number
below comes from the run logs.

## Summary

| Check                                    | Run 1                    | Run 2            | Run 3 |
| ---------------------------------------- | ------------------------ | ---------------- | ----- |
| DuckDNS accepts the new IP               | PASS                     | PASS             | PASS  |
| Admin has a trusted certificate          | PASS                     | PASS             | PASS  |
| HTTP redirects to HTTPS                  | PASS                     | PASS             | PASS  |
| GitHub login is offered                  | PASS                     | PASS             | PASS  |
| API refuses without login                | PASS                     | PASS             | PASS  |
| GitHub login redirects back to the admin | PASS                     | PASS             | PASS  |
| Unpublished name gets no certificate     | PASS                     | PASS             | PASS  |
| Bare domain shows the 404 page           | PASS                     | FAIL (`got 000`) | PASS  |
| Page planted in the sites volume         | PASS                     | PASS             | PASS  |
| Published page served with its own cert  | PASS                     | PASS             | PASS  |
| Page survives a redeploy                 | FAIL (`got (7, '000 ')`) | PASS             | PASS  |
| Page survives a reboot                   | PASS                     | PASS             | PASS  |
| GitHub login and publish                 | SKIP (no terminal)       | PASS             | PASS  |
| Destroy VM, clear DuckDNS                | PASS                     | PASS             | PASS  |

Run 1 and run 2 each ended with `1 check(s) failed`. Run 3 ended with every check passing,
`Destroy complete! Resources: 4 destroyed.` and `DuckDNS address cleared`.

## What worked

- **Infrastructure:** Terraform created and destroyed the network, firewall, VM and the Compute API in every run.
- **DNS:** DuckDNS accepted the IP every time, and the name resolved before the deploy started.
- **TLS:** Let's Encrypt certificates were issued for the admin host and for the published page. Unknown names got no
  certificate.
- **Auth:** the GitHub login redirect works, and the API refuses requests without a login.
- **Persistence:** a page planted in the `sites` volume survived a container redeploy and a full VM reboot.
- **Login step:** with a real terminal (runs 2 and 3), a page published after a real GitHub login was served with its
  own certificate.
- **Cleanup:** the DuckDNS address was cleared at the end of every run.

## What did not work

### 1. Redeploy check failed right after the containers restarted (run 1)

- **Symptom:** `FAIL page survives a redeploy: got (7, '000 ')`. curl exit 7 means the TCP connection was refused.
- **Cause (likely, not confirmed):** the check ran as soon as `docker compose up -d --force-recreate` returned. Caddy
  had just been recreated and was not listening on port 443 yet. The check had no retry.
- **Status:** fixed in code by the wait (see below). Run 3 passed this check.

### 2. Bare domain 404 check failed right after the stack started (run 2)

- **Symptom:** `FAIL bare domain shows the 404 page: got 000`. `000` means curl got no HTTP response.
- **Cause (likely, not confirmed):** the check ran before Caddy was ready to serve the bare domain. The admin and page
  checks wait for their certificates, but the bare-domain check did not.
- **Status:** fixed in code by the wait. Run 3 passed this check.

### 3. GitHub step skipped (run 1)

- **Symptom:** `SKIP GitHub login and publish: no terminal to wait in`.
- **Cause:** the run used `</dev/null`, so `sys.stdin.isatty()` was false, and the script skips the step by design.
- **Not a failure of the site.** It only means the step did not run. Run 3 ran it from a terminal and it passed.

## How the failures were made to pass

### The wait change

The redeploy check and the bare-domain check use the same `wait()` helper as the certificate and reboot checks. Each one
retries for up to 180s before its assertion runs. The change is in `scripts/gcp_e2e.py` and is on `main` (merged in PR
#6).

- Redeploy: wait until `https://e2e-keep.<domain>/` answers `200`.
- Bare domain: wait until `https://<domain>/` answers `404`.

If the wait times out, `wait()` still raises, so a real failure still fails the run.

### How to run it

Run it from a real terminal, without `</dev/null`, so the GitHub login step runs:

```bash
cd ~/Programming/My_Projects/static-sites-publish && GCP_PROJECT=site-publisher-test DUCKDNS_NAME=site-publisher-test DUCKDNS_TOKEN=$(kpass NEX_CORE_DUCKDNS_TOKEN) GITHUB_CLIENT_ID=$(kpass SITES_E2E_GITHUB_ID) GITHUB_CLIENT_SECRET=$(kpass SITES_E2E_GITHUB_SECRET) ALLOWLIST=github:15252341 make gcp-e2e
```

Each run creates a new VM and IP. The script removes that IP from `~/.ssh/known_hosts` itself (`ssh-keygen -R`), so no
manual cleanup is needed.

### Success looks like

- Every line in the summary table is `PASS`, including the GitHub login step.
- The last lines are `Destroy complete!` and `DuckDNS address cleared`.
- No `check(s) failed` line.

### If `000` appears again

Read Caddy's log on the VM. Use `KEEP=1 make gcp-e2e` so the VM still exists, then:

```bash
ssh sites@<vm-ip> 'cd site-publisher/deploy && docker compose logs --tail 200 caddy'
```

Look for certificate or ACME errors for the bare domain. Those would mean a real problem, not a slow start.

## Open items

- The two timing causes are still inferred. Run 3 passed with the waits, but its log did not show how many retries each
  wait needed, so it does not prove the cause. `wait()` now prints `ready after N retries`: a nonzero N in the next run
  confirms the race, and `0` means the waits were not needed that time.
- The next run is the first with the `known_hosts` cleanup and the retry count, and neither has run live yet.
