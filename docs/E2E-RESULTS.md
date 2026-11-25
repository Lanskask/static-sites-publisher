# E2E test results on GCP

This page records the two real runs of `make gcp-e2e` on 2026-11-24 and what to change so that the failing checks pass.
The test creates a throwaway GCP VM, points a DuckDNS name at it, deploys the stack, runs the checks, and destroys the
VM. Every number below comes from the run logs.

## Summary

| Check                                    | Run 1                    | Run 2            |
| ---------------------------------------- | ------------------------ | ---------------- |
| DuckDNS accepts the new IP               | PASS                     | PASS             |
| Admin has a trusted certificate          | PASS                     | PASS             |
| HTTP redirects to HTTPS                  | PASS                     | PASS             |
| GitHub login is offered                  | PASS                     | PASS             |
| API refuses without login                | PASS                     | PASS             |
| GitHub login redirects back to the admin | PASS                     | PASS             |
| Unpublished name gets no certificate     | PASS                     | PASS             |
| Bare domain shows the 404 page           | PASS                     | FAIL (`got 000`) |
| Page planted in the sites volume         | PASS                     | PASS             |
| Published page served with its own cert  | PASS                     | PASS             |
| Page survives a redeploy                 | FAIL (`got (7, '000 ')`) | PASS             |
| Page survives a reboot                   | PASS                     | PASS             |
| GitHub login and publish                 | SKIP (no terminal)       | PASS             |
| Destroy VM, clear DuckDNS                | PASS                     | PASS             |

Run 1 ended with `1 check(s) failed`, and so did run 2. The failing check differs between the runs.

## What worked

- **Infrastructure:** Terraform created and destroyed the network, firewall, VM and the Compute API each time. Both runs
  ended with `Destroy complete! Resources: 4 destroyed.`
- **DNS:** DuckDNS accepted the IP both times, and the name resolved before the deploy started.
- **TLS:** Let's Encrypt certificates were issued for the admin host and for the published page. Unknown names got no
  certificate.
- **Auth:** the GitHub login redirect works, and the API refuses requests without a login.
- **Persistence:** a page planted in the `sites` volume survived a container redeploy and a full VM reboot.
- **Login step (run 2):** with a real terminal, a page published after a real GitHub login was served with its own
  certificate.
- **Cleanup:** the DuckDNS address was cleared at the end of both runs.

## What did not work

### 1. Redeploy check fails right after the containers restart (run 1)

- **Symptom:** `FAIL page survives a redeploy: got (7, '000 ')`. curl exit 7 means the TCP connection was refused.
- **Cause (likely, not confirmed):** the check ran as soon as `docker compose up -d --force-recreate` returned. Caddy
  had just been recreated and was not listening on port 443 yet. The check had no retry.
- **Evidence it was timing:** the same check passed in run 2 with the same code, and the page and volume survived in
  both runs.

### 2. Bare domain 404 check fails right after the stack starts (run 2)

- **Symptom:** `FAIL bare domain shows the 404 page: got 000`. `000` means curl got no HTTP response.
- **Cause (likely, not confirmed):** the check ran before Caddy was ready to serve the bare domain. The admin and page
  checks wait for their certificates, but the bare-domain check did not.
- **Evidence it was timing:** the same check passed in run 1.

### 3. GitHub step skipped (run 1)

- **Symptom:** `SKIP GitHub login and publish: no terminal to wait in`.
- **Cause:** the run used `</dev/null`, so `sys.stdin.isatty()` was false, and the script skips the step by design.
- **Not a failure of the site.** It only means the step did not run.

## How to make the failures pass

### Done: wait before the checks that failed

The redeploy check and the bare-domain check now use the same `wait()` helper as the certificate and reboot checks. Each
one retries for up to 180s before its assertion runs. The change is in `scripts/gcp_e2e.py`.

- Redeploy: wait until `https://e2e-keep.<domain>/` answers `200`.
- Bare domain: wait until `https://<domain>/` answers `404`.

If the wait times out, `wait()` still raises, so a real failure still fails the run.

### Next step if `000` still appears

Read Caddy's log on the VM:

```bash
ssh sites@<vm-ip> 'cd site-publisher/deploy && docker compose logs --tail 200 caddy'
```

Look for certificate or ACME errors for the bare domain, which would mean the check is hitting a real problem and not
just a slow start.

### Run the full test with a terminal

Run it from a real terminal, without `</dev/null`, so the GitHub login step runs:

```bash
cd ~/Programming/My_Projects/static-sites-publish && ssh-keygen -R <vm-ip>; GCP_PROJECT=site-publisher-test DUCKDNS_NAME=site-publisher-test DUCKDNS_TOKEN=$(kpass NEX_CORE_DUCKDNS_TOKEN) GITHUB_CLIENT_ID=$(kpass SITES_E2E_GITHUB_ID) GITHUB_CLIENT_SECRET=$(kpass SITES_E2E_GITHUB_SECRET) ALLOWLIST=github:15252341 make gcp-e2e
```

Replace `<vm-ip>` with the IP from the previous run's `known_hosts` line, or skip the `ssh-keygen -R` if it is not
there. Each run creates a new VM and IP.

### Success looks like

- Every line in the summary table is `PASS`, including the GitHub login step.
- The last line is `Destroy complete!`, and `DuckDNS address cleared` is printed.
- No `1 check(s) failed` line.

## Open items

- The fixes above have not been run yet. Run 3 is needed to confirm them.
- The two timing causes are inferred from the logs. Caddy's log would confirm or rule them out.
- `known_hosts` must be cleared by hand each run. The script could do it.
