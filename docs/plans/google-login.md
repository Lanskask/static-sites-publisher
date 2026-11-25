# Google login: findings and plan

Status 2026-11-24. Repos: `static-sites-publish` (the admin) and `VPC/OracleCloud` (the k3s production deployment).

## Summary

- **Code:** Google login is fully wired. It needs no new code to work. One small defect: the scope is only `openid`, so
  Google never returns a name and the UI shows the raw `google:<sub>`.
- **Compose self-host stack:** already passes `GOOGLE_*` through `env_file: .env`. Nothing is missing.
- **k3s production:** the wiring exists (manifest plus secrets script), but Google is **not configured**. The Keychain
  has no `SITES_GOOGLE_CLIENT_ID` or `SITES_GOOGLE_CLIENT_SECRET` entry. Live
  `GET https://nex-sites-admin.duckdns.org/api/providers` returns `["github"]`, and `/auth/google/start` returns 404.
- **Never tested end to end.** Nothing shows a Google client was ever created. No test runs a successful OAuth callback
  for any provider.

Most of the remaining work is manual (Google Cloud Console, Keychain, allowlist). The code change is one word plus one
test.

## 1. What exists today (paths relative to `static-sites-publish/`)

| Piece                       | Where                                                 | What it does                                                                                                                                                                                         |
| --------------------------- | ----------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Provider config             | `src/main.go:54-64`                                   | `add("google", "GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "https://openidconnect.googleapis.com/v1/userinfo", endpoints.Google, "openid")`. It registers only when `GOOGLE_CLIENT_ID` is not empty. |
| Redirect URL                | `src/main.go:58`                                      | `BASE_URL + "/auth/google/callback"`                                                                                                                                                                 |
| Routes                      | `src/main.go:91-92`                                   | `GET /auth/{provider}/start` and `GET /auth/{provider}/callback`. Both are generic and return 404 for a provider that is not configured (`src/auth.go:150-153`, `163-166`).                          |
| Start                       | `src/auth.go:149-158`                                 | Random state in a `__Host-state` cookie that lives 600 s, then a redirect to `AuthCodeURL(state)`. No PKCE (not needed for a confidential server-side client).                                       |
| Callback                    | `src/auth.go:161-188`                                 | Constant-time state check, code exchange, GET userinfo with the access token, allowlist check, then the signed session cookie and a display-name cookie.                                             |
| Identity format             | `src/auth.go:232-248`                                 | `provider + ":" + id + sub`, so Google gives `google:<sub>`. `display` is the first non-empty of `login`, `name`, `email`, cut to 80 runes.                                                          |
| "Not on the allowlist" page | `src/auth.go:179-183`                                 | 403 page that shows the refused identity, so the owner can copy their `google:<sub>`.                                                                                                                |
| `/api/providers`            | `src/main.go:107-113`                                 | Returns the keys of `auth.Providers`, which means every provider whose `*_CLIENT_ID` env var is set. The secret is not checked.                                                                      |
| Login buttons               | `src/static/app.js:37-42`                             | One "Log in with Google" link per name from `/api/providers`, shown only after a 401.                                                                                                                |
| Name shown in the header    | `src/static/app.js:17-18`                             | `"<name> · Google"`, or the raw id when the name is empty.                                                                                                                                           |
| Ownership                   | `src/store.go:186-190`, `301-310`, `348-362`          | Pages, quotas and API tokens are keyed by the full identity string.                                                                                                                                  |
| Tests                       | `src/auth_test.go:91-113`                             | `TestParseAllowlist` (includes `google:abc`) and `TestParseUser` (Google bodies with `name` and `email`). `TestCallbackChecksState` (`:78-89`) covers only the bad-state path, for GitHub.           |
| Compose stack               | `deploy/compose.yaml:5`, `deploy/.env.example:10-19`  | `env_file: .env` carries `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET`. The example documents `google:<sub>` and the callback path.                                                                  |
| e2e stack                   | `deploy/e2e/compose.yaml:4-11`                        | GitHub env only. Google is never started.                                                                                                                                                            |
| README                      | `README.md:11`, `102-105`, `172`                      | Claims GitHub or Google login. The Google step names only Credentials and the redirect URI. It says nothing about the consent screen, test users or scopes.                                          |
| k3s manifest                | `OracleCloud/pyinfra/files/sites.yaml:124`            | `envFrom: secretRef sites-admin`, so every key in the Secret becomes env.                                                                                                                            |
| k3s secrets                 | `OracleCloud/scripts/sites_secrets.py:19-26`, `46-51` | Adds `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` only when the Keychain entry `SITES_GOOGLE_CLIENT_ID` exists. Tested in `OracleCloud/tests/test_sites_scripts.py:13-35`.                          |
| Runbook                     | `OracleCloud/docs/RUNBOOK-sites.md:12-13`, `22-23`    | Google step marked optional, redirect URI only.                                                                                                                                                      |

### Problems found

1. **Scope is too narrow (code defect).** `src/main.go:64` asks only for `openid`. With that scope, Google's userinfo
   returns only `{"sub": "..."}`. So `display` is empty and the header shows `google:1234...` instead of the user's
   name. Commit `2b0c39a` ("show ... Google name") never worked for Google. Its tests feed `name` and `email` bodies
   (`src/auth_test.go:101-102`) that this scope never produces. Login itself still works.
2. **Production not configured.** The Keychain has no Google entries, so `sites_secrets.py` silently skips Google. The
   live provider list is `["github"]`.
3. **Allowlist is GitHub-only.** Production `SITES_ALLOWLIST` (from the runbook example) holds only `github:<id>`. A
   Google login would get the 403 identity page until `google:<sub>` is added.
4. **Changing the Secret does not restart the pod.** `envFrom` is read only when the pod starts, and
   `make sites-secrets` does not restart it. After changing the Secret you also need
   `kubectl rollout restart deploy/sites-admin` (or `make sites-deploy`).
5. **Google and GitHub logins are separate owners.** For the same person, `google:<sub>` and `github:<id>` are different
   owners. Logged in with Google, you see none of your GitHub pages, get a separate quota, and are refused page names
   your GitHub identity owns (`src/store.go:186`). This is by design, but it is not documented.
6. **No happy-path callback test** for any provider. The exchange, userinfo and cookie path is not covered.

### Not problems (no change needed)

- **`email_verified` is not checked.** This is correct. The identity is `sub`, Google's stable account id, not the
  email, and access never depends on an email. Do not switch to email identities: emails can be renamed or reused, and
  an email check would need the `email` scope and a verified-flag check.
- **The ID token is not validated.** This is fine. The code is exchanged server-side over TLS directly with Google, and
  the identity comes from Google's userinfo endpoint using that access token.
- **Endpoints are correct.** `endpoints.Google` and the OIDC userinfo URL are right.
- **If the user cancels at Google,** Google redirects back with `?error=access_denied` and no code. The exchange fails
  and the user sees a 502 "login failed" page. This is cosmetic and left as is.

## 2. Plan

The change is small: one scope word, one test, docs, and manual console and secret steps. Do the tasks in order.

### Task 1: test first, then add the `profile` scope (`static-sites-publish`)

- **Test, written before the fix:** in `src/auth_test.go`, add `TestCallbackGoogleHappyPath`. It starts an
  `httptest.Server` that serves `POST /token` (returns `{"access_token":"t","token_type":"Bearer"}`) and `GET /userinfo`
  (returns `{"sub":"42","name":"Jo Doe"}`). It builds
  `Auth{Key: 32 bytes, Allow: {"google:42": true}, Providers: {"google": {UserURL: srv.URL+"/userinfo", Config: {Endpoint: {TokenURL: srv.URL+"/token"}, ...}}}}`,
  calls `Callback` with a matching state cookie and `?state=...&code=c`, and checks:
  - 302 to `/`
  - the `__Host-session` cookie verifies to `google:42`
  - `__Host-name` is `Jo%20Doe` (or whatever `QueryEscape` produces)
  - a second case with an empty allowlist returns 403 and the body contains `google:42`

  No refactor is needed, because `Provider` already takes `Config` and `UserURL`. This test passes before the fix (it
  bypasses `main`), so it protects the whole server-side flow rather than the scope.

- **Fix:** in `src/main.go:64`, change `"openid"` to `"openid", "profile"`. Do not add `email`; it is not needed.
- **Acceptance criteria:**
  - `make test` is green.
  - After Task 4, logging in with Google shows `"<Your Name> · Google"` in the header.
- **Not done:** a test that reads the scope from `main()`. That would mean pulling `add` out of `main` just to assert
  one string; the manual check in Task 6 covers it.

### Task 2: docs (`static-sites-publish`)

- In `README.md:102-105`, replace the Google bullet with the console steps from Task 3, in short form: consent screen,
  External, Testing, add yourself as a test user, Web client, exact redirect URI. Add one sentence: "GitHub and Google
  identities are separate owners: pages published as one are not visible to the other."
- In `docs/DECISION-LOG.md`, add an entry: Google identity is `google:<sub>`, not the email (stable, not reusable, needs
  no `email_verified` check). Scopes are `openid profile`. There is no account linking between providers (YAGNI).
  Revisit when the service opens to other users.
- **Acceptance criteria:** Prettier passes at width 120, and the README steps match what the console actually showed in
  Task 3.

### Task 3: Google Cloud Console (manual, by the human)

At <https://console.cloud.google.com>:

1. Create or pick a project, for example `nex-sites`.
2. Go to **Google Auth Platform**, then **Branding** (older UI: APIs and Services, then OAuth consent screen). Set:
   - App name `nex-sites`
   - User support email and developer contact: your address
   - Audience: **External**
   - Publishing status: leave it at **Testing**
3. Under **Audience**, then **Test users**, add the Google account(s) that will log in. In Testing mode, only these
   accounts can complete the login.
4. Under **Data access**, add the scopes `openid` and `.../auth/userinfo.profile`. Both are non-sensitive and need no
   verification.
5. Under **Clients**, then **Create client**, choose application type **Web application**, name `nex-sites-admin`.
   - Authorized JavaScript origins: none needed.
   - **Authorized redirect URIs**, exact strings with no trailing slash:
     - Production k3s: `https://nex-sites-admin.duckdns.org/auth/google/callback`
     - Compose self-host: `https://<ADMIN_HOST>/auth/google/callback`, for example
       `https://admin.sites.example.com/auth/google/callback`. Either add it to the same client or create a client per
       deployment.
6. Copy the Client ID (`...apps.googleusercontent.com`) and the Client secret.

**Acceptance criterion:** the client exists, its redirect URI matches `BASE_URL + "/auth/google/callback"` exactly, and
your account is listed as a test user.

### Task 4: production k3s secrets and allowlist (`OracleCloud`)

1. Store the client: `kpass set SITES_GOOGLE_CLIENT_ID` and `kpass set SITES_GOOGLE_CLIENT_SECRET`. These names are
   already read by `scripts/sites_secrets.py:48-49`.
2. Push and restart:
   - `make sites-secrets`
   - `ssh ubuntu@k3s-oracle sudo k3s kubectl rollout restart deploy/sites-admin`

   Deploy the Task 1 code first if it is merged: `make sites-deploy` builds GitHub `main` and also restarts the pod.

3. Open `https://nex-sites-admin.duckdns.org`, log out, and click "Log in with Google". The 403 page shows
   `google:<sub>`.
4. Update the allowlist: `kpass set SITES_ALLOWLIST` with `github:<id>,google:<sub>` (comma-separated, keep the GitHub
   entry). Then run `make sites-secrets` and the rollout restart again.
5. Optional, one line in `OracleCloud/docs/RUNBOOK-sites.md`: after `make sites-secrets`, run
   `kubectl rollout restart deploy/sites-admin`. Point step 3 at the README console steps.

**Acceptance criteria:**

- `curl -s https://nex-sites-admin.duckdns.org/api/providers` lists both `github` and `google`.
- Google login lands on the admin.
- GitHub login still works.

### Task 5: Compose self-host (no code change)

- Fill in `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` in `deploy/.env`.
- Run `docker compose up -d --force-recreate admin`. `env_file` is read only when the container is created.
- Log in once, copy `google:<sub>` into `ALLOWLIST`, and recreate the container again.

**Acceptance criterion:** same as Task 4, against `https://<ADMIN_HOST>`.

### Task 6: verify end to end

Run these checks against production:

- [ ] `/api/providers` returns `github` and `google`.
- [ ] In a private window, "Log in with Google", consent, then back on the admin. The header shows your name and
      "Google".
- [ ] Publish a test page as the Google identity, open `https://<page>.nex-sites.duckdns.org/`, then delete it.
- [ ] A Google account that is not allowlisted gets the 403 identity page and no session. An account that is not a test
      user is stopped by Google itself.
- [ ] GitHub login still works and still shows the GitHub pages.

## Risks

- **Separate owners per provider.** The owner's existing pages and API tokens belong to `github:<id>`. Logged in with
  Google, the owner sees an empty list and cannot reuse those page names. Workaround: keep publishing as GitHub, and
  treat Google as a backup login. Account linking or aliasing is deliberately left out until there are real users.
- **Allowlist migration.** If `SITES_ALLOWLIST` is overwritten with only `google:<sub>`, GitHub login stops working.
  Always keep both, comma-separated.
- **Testing mode.** Only listed test users (up to 100) can log in. That suits an owner-only allowlist. Publishing the
  app to "In production" with only `openid` and `profile` needs no Google verification, but may ask for a privacy policy
  URL and a homepage.
- **`duckdns.org` is a public suffix.** The console may ask for authorized domains or refuse `duckdns.org` when
  publishing. This is not verified. Testing mode is the fallback, and it has worked for this kind of setup.
- **Secret not picked up.** A missing pod restart after `make sites-secrets` looks like "Google not working". Check with
  `/api/providers`.
- **Code ships from GitHub.** `make sites-deploy` builds `SITES_REF` (default `main`) from GitHub, so the scope fix must
  be pushed and merged before it can reach production.

### Critical Files for Implementation

- `src/main.go` (line 64, scope)
- `src/auth_test.go` (new callback happy-path test)
- `README.md` (lines 102-105, Google setup and separate-owner note)
- `OracleCloud/scripts/sites_secrets.py` (already handles Google; push via `make sites-secrets`)
- `OracleCloud/docs/RUNBOOK-sites.md` (restart step after secrets)
