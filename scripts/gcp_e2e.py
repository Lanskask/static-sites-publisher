"""End-to-end test of the self-hosting steps for real: a throwaway GCP VM, real DNS (DuckDNS), real Let's Encrypt
certificates and real GitHub login. Creates the VM, deploys, checks, and destroys the VM at the end.

Usage: python3 scripts/gcp_e2e.py [--keep]     (or: make gcp-e2e, KEEP=1 to leave the VM running)

Needs these environment variables (it lists every missing one, and every missing tool, before doing anything):
  GCP_PROJECT                                  a GCP project with billing on; `gcloud auth application-default login`
  DUCKDNS_NAME, DUCKDNS_TOKEN                  the test domain is DUCKDNS_NAME.duckdns.org
  GITHUB_CLIENT_ID, GITHUB_CLIENT_SECRET       a GitHub OAuth or GitHub App whose callback URL is
                                               https://admin.DUCKDNS_NAME.duckdns.org/auth/github/callback
  ALLOWLIST                                    who may log in, e.g. github:12345678 (`gh api user --jq .id`)
Also checks that a page survives a redeploy and a reboot. At the end it clears the DuckDNS address.
It writes deploy/.env for the test and deletes it afterwards (kept with --keep); it refuses to overwrite a deploy/.env it did not write.
The login step waits for you in the browser; without a terminal it is skipped.
"""

import argparse
import json
import os
import secrets
import shlex
import shutil
import subprocess
import sys
import time
import urllib.request
from collections.abc import Callable
from pathlib import Path

import dns_check
import ssh_deploy

ROOT = Path(__file__).resolve().parent.parent
TF_DIR = ROOT / "deploy" / "e2e" / "gcp"
ENV = ROOT / "deploy" / ".env"
MARK = "# written by scripts/gcp_e2e.py; deleted when it ends\n"
VARS = [
    "GCP_PROJECT",
    "DUCKDNS_NAME",
    "DUCKDNS_TOKEN",
    "GITHUB_CLIENT_ID",
    "GITHUB_CLIENT_SECRET",
    "ALLOWLIST",
]
TOOLS = ["terraform", "ssh", "tar", "curl"]
ADC = Path.home() / ".config/gcloud/application_default_credentials.json"
SSH_KEY = Path.home() / ".ssh/id_ed25519.pub"


def missing(env: dict[str, str], have_tool, exists) -> list[str]:
    """Everything the run needs and does not have, as messages; empty means ready."""
    out = [f"environment variable {v}" for v in VARS if not env.get(v)]
    out += [f"command {t}" for t in TOOLS if not have_tool(t)]
    if not (exists(ADC) or env.get("GOOGLE_APPLICATION_CREDENTIALS")):
        out.append("GCP credentials: run gcloud auth application-default login")
    if not exists(SSH_KEY):
        out.append(f"ssh key {SSH_KEY} (the VM lets it in): ssh-keygen -t ed25519")
    if exists(ENV) and not ENV.read_text().startswith(MARK):
        out.append(
            "deploy/.env is yours (not written by this script): move it away first"
        )
    return out


def env_file(e: dict[str, str]) -> str:
    domain = f"{e['DUCKDNS_NAME']}.duckdns.org"
    return MARK + "".join(
        f"{k}={v}\n"
        for k, v in {
            "PAGES_DOMAIN": domain,
            "ADMIN_HOST": f"admin.{domain}",
            "ACME_EMAIL": f"e2e@{domain}",  # Let's Encrypt wants one; no one reads it
            "ALLOWLIST": e["ALLOWLIST"],
            "SESSION_KEY": secrets.token_hex(32),
            "GITHUB_CLIENT_ID": e["GITHUB_CLIENT_ID"],
            "GITHUB_CLIENT_SECRET": e["GITHUB_CLIENT_SECRET"],
        }.items()
    )


def duckdns_url(name: str, token: str, ip: str = "") -> str:
    """The DuckDNS update URL: point name at ip, or clear its address when ip is empty."""
    q = f"domains={name}&token={token}&" + (f"ip={ip}" if ip else "clear=true")
    return f"https://www.duckdns.org/update?{q}"


def seed_page_cmd(name: str, owner: str) -> str:
    """Shell command for the VM that publishes a page straight into the sites volume, as an upload would leave it."""
    meta = json.dumps(
        {
            "name": name,
            "owner": owner,
            "created": "2026-01-01T00:00:00Z",
            "updated": "2026-01-01T00:00:00Z",
            "bytes": 15,
        }
    )
    script = (
        f"mkdir -p /s/pages/{name} /s/meta && echo '<h1>e2e</h1>' > /s/pages/{name}/index.html"
        f" && echo {shlex.quote(meta)} > /s/meta/{name}.json"
        f" && chown -R 65532:65532 /s/pages/{name} /s/meta/{name}.json"
    )
    # the Caddy image is already on the VM; the sites volume is found by its compose label
    return (
        'docker run --rm -v "$(docker volume ls -q -f label=com.docker.compose.volume=sites)":/s'
        f" caddy:2.10-alpine sh -c {shlex.quote(script)}"
    )


def tf(*args: str, **kw) -> subprocess.CompletedProcess:
    cmd = ["terraform", f"-chdir={TF_DIR}", *args]
    print("+", " ".join(cmd))
    return subprocess.run(cmd, check=True, text=True, **kw)


def curl(url: str, *extra: str) -> tuple[int, str, str]:
    """(curl exit code, "status redirect", body), verifying certificates against the system's CAs."""
    cmd = ["curl", "-sS", "--max-time", "60", "-w", "\n%{http_code} %{redirect_url}"]
    r = subprocess.run([*cmd, *extra, url], check=False, capture_output=True, text=True)
    body, _, status = r.stdout.rpartition("\n")
    return r.returncode, status, body


def wait(what: str, ok: Callable[[], bool], seconds: int, sleep: Callable[[float], None] = time.sleep) -> int:
    """Poll ok() every 10s until it is true or `seconds` pass; returns how many retries it needed."""
    print(f"waiting for {what} (up to {seconds}s)")
    end = time.time() + seconds
    retries = 0
    while not ok():
        if time.time() > end:
            raise SystemExit(f"timed out waiting for {what}")
        sleep(10)
        retries += 1
    print(f"  ready after {retries} retries")
    return retries


def forget_host(ip: str, run: Callable[..., object] = subprocess.run) -> None:
    """Drop the VM's old key from known_hosts: every run gets a new VM, and a reused IP would fail the key check."""
    run(["ssh-keygen", "-R", ip], check=False, capture_output=True)


def main() -> None:
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument(
        "--keep", action="store_true", help="leave the VM running at the end"
    )
    keep = p.parse_args().keep
    e = dict(os.environ)
    if problems := missing(e, shutil.which, Path.exists):
        sys.exit("missing:\n  " + "\n  ".join(problems))

    domain = f"{e['DUCKDNS_NAME']}.duckdns.org"
    admin = f"admin.{domain}"
    project = f"project={e['GCP_PROJECT']}"
    failures: list[str] = []

    def expect(what: str, ok: bool, got: object = "") -> None:
        print(("PASS " if ok else "FAIL ") + what + ("" if ok else f": got {got}"))
        if not ok:
            failures.append(what)

    tf("init", "-input=false", capture_output=True)
    try:
        tf("apply", "-auto-approve", "-input=false", "-var", project)
        ip = tf("output", "-raw", "ip", capture_output=True).stdout.strip()
        host = tf("output", "-raw", "ssh", capture_output=True).stdout.strip()
        forget_host(ip)

        with urllib.request.urlopen(
            duckdns_url(e["DUCKDNS_NAME"], e["DUCKDNS_TOKEN"], ip)
        ) as r:
            expect("DuckDNS accepted the new IP", r.read() == b"OK")
        wait(
            f"{domain} and *.{domain} to resolve to {ip}",
            lambda: not dns_check.check(domain, admin, ip),
            600,
        )
        ENV.write_text(env_file(e))
        ENV.chmod(0o600)
        ssh = [
            "ssh",
            "-o",
            "StrictHostKeyChecking=accept-new",
            "-o",
            "ConnectTimeout=10",
        ]
        wait(
            "the VM to install Docker",
            lambda: (
                subprocess.run(
                    [*ssh, host, "test -f /var/lib/sites-e2e-ready"],
                    check=False,
                    capture_output=True,
                ).returncode
                == 0
            ),
            600,
        )
        if ssh_deploy.main([host]):
            sys.exit("deploy failed")

        wait(
            "the admin's Let's Encrypt certificate",
            lambda: curl(f"https://{admin}/")[0] == 0,
            180,
        )
        code, status, body = curl(f"https://{admin}/")
        expect(
            "admin has a trusted certificate and is served",
            code == 0 and status.startswith("200"),
            (code, status),
        )
        _, status, _ = curl(f"http://{admin}/")
        expect(
            "http redirects to https",
            status == f"308 https://{admin}/",
            status,
        )
        _, status, body = curl(f"https://{admin}/api/providers")
        expect("GitHub login is offered", '"github"' in body, body)
        _, status, _ = curl(f"https://{admin}/api/me")
        expect("API refuses without login", status.startswith("401"), status)
        _, status, _ = curl(f"https://{admin}/auth/github/start")
        expect(
            "GitHub login redirects back to this admin",
            f"redirect_uri=https%3A%2F%2F{admin}%2Fauth%2Fgithub%2Fcallback" in status,
            status,
        )
        code, status, _ = curl(f"https://nope-{secrets.token_hex(3)}.{domain}/")
        expect("unpublished name gets no certificate", code in (35, 60), (code, status))
        wait("the bare domain's 404 page", lambda: curl(f"https://{domain}/")[1].startswith("404"), 180)
        code, status, body = curl(f"https://{domain}/")
        expect("bare domain shows the 404 page", status.startswith("404"), status)

        page = f"https://e2e-keep.{domain}/"
        r = subprocess.run(
            [*ssh, host, seed_page_cmd("e2e-keep", e["ALLOWLIST"].split(",")[0])],
            check=False,
        )
        expect("page planted in the sites volume", r.returncode == 0, r.returncode)
        wait("the page's certificate", lambda: curl(page)[0] == 0, 180)
        code, status, _ = curl(page)
        expect(
            "published page is served with its own certificate",
            code == 0 and status.startswith("200"),
            (code, status),
        )

        if ssh_deploy.main([host]):
            sys.exit("redeploy failed")
        wait("the page after the redeploy", lambda: curl(page)[1].startswith("200"), 180)
        code, status, _ = curl(page)
        expect(
            "page survives a redeploy",
            code == 0 and status.startswith("200"),
            (code, status),
        )

        subprocess.run([*ssh, host, "sudo reboot"], check=False, capture_output=True)
        time.sleep(20)
        wait(
            "the VM and the stack to come back after a reboot",
            lambda: curl(f"https://{admin}/")[1].startswith("200"),
            300,
        )
        code, status, _ = curl(page)
        expect(
            "page survives a reboot",
            code == 0 and status.startswith("200"),
            (code, status),
        )

        if sys.stdin.isatty():
            input(
                f"\nOpen https://{admin}/ , log in with GitHub and publish a page named e2e"
                " (upload any .html file). Press Enter when done. "
            )
            code, status, body = curl(f"https://e2e.{domain}/")
            expect(
                "page published after a real login is served with its own certificate",
                code == 0 and status.startswith("200"),
                (code, status),
            )
        else:
            print("SKIP GitHub login and publish: no terminal to wait in")
    finally:
        if keep:  # deploy/.env stays too, so make deploy works against the VM
            print(
                f"left running; delete with: make tf-destroy GCP_PROJECT={e['GCP_PROJECT']}"
            )
        else:
            if ENV.exists() and ENV.read_text().startswith(MARK):
                ENV.unlink()
            tf("destroy", "-auto-approve", "-input=false", "-var", project)
            # the released IP can go to a stranger; leave the name pointing nowhere
            with urllib.request.urlopen(
                duckdns_url(e["DUCKDNS_NAME"], e["DUCKDNS_TOKEN"])
            ) as r:
                print(
                    "DuckDNS address cleared"
                    if r.read() == b"OK"
                    else "WARN DuckDNS clear failed"
                )
    sys.exit(f"{len(failures)} check(s) failed" if failures else 0)


if __name__ == "__main__":
    main()
