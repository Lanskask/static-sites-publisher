"""End-to-end test of deploy/ on the local Docker: build the admin image, start Caddy, and get real certificates.

Certificates come from Pebble, Let's Encrypt's test ACME server, over the same HTTP-01 challenge and on-demand
/tls-check flow as production. Let's Encrypt itself cannot reach a laptop, so this is as close as a local run gets.
Run: make e2e (add KEEP=1 to leave the stack running).
"""

import os
import shutil
import subprocess
import sys
import tempfile
import time
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
E2E = ROOT / "deploy" / "e2e"
PROJECT = "sites-e2e"
PEBBLE = "ghcr.io/letsencrypt/pebble:2.7.0"
PAGES, ADMIN = "sites.test", "admin.sites.test"
HTTP, HTTPS = 18080, 18443
ANCHOR = "\temail {$ACME_EMAIL}\n"


def with_acme_ca(caddyfile: str) -> str:
    """Point Caddy's ACME client at Pebble and trust Pebble's API certificate."""
    if ANCHOR not in caddyfile:
        raise ValueError(
            "deploy/Caddyfile has no 'email {$ACME_EMAIL}' line in its global options"
        )
    extra = (
        "\tacme_ca https://pebble:14000/dir\n\tacme_ca_root /e2e/pebble.minica.pem\n"
    )
    return caddyfile.replace(ANCHOR, ANCHOR + extra, 1)


def compose_cmd() -> list[str]:
    if (
        subprocess.run(
            ["docker", "compose", "version"], check=False, capture_output=True
        ).returncode
        == 0
    ):
        return ["docker", "compose"]
    if shutil.which("docker-compose"):
        return ["docker-compose"]
    sys.exit("needs docker compose (the plugin or the docker-compose binary)")


def run(*cmd: str, **kw) -> subprocess.CompletedProcess:
    return subprocess.run(cmd, check=True, text=True, **kw)


def curl(url: str, host: str, port: int, ca: Path, *extra: str) -> tuple[int, str, str]:
    """Returns (curl exit code, HTTP status, body); exit 35/60 mean the TLS handshake or chain failed."""
    cmd = [
        "curl",
        "-sS",
        "--max-time",
        "60",
        "--resolve",
        f"{host}:{port}:127.0.0.1",
        "--cacert",
        str(ca),
    ]
    cmd += ["-w", "\n%{http_code} %{redirect_url}", *extra, url]
    r = subprocess.run(cmd, check=False, capture_output=True, text=True)
    body, _, status = r.stdout.rpartition("\n")
    return r.returncode, status, body


def seed(name: str) -> None:
    """Publish a page by writing what the admin would write: the files and the meta record."""
    meta = f'{{"name":"{name}","owner":"github:1","created":"2026-01-01T00:00:00Z","updated":"2026-01-01T00:00:00Z"}}'
    sh = (
        f"mkdir -p /s/pages/{name} /s/meta && echo 'hello from {name}' > /s/pages/{name}/index.html"
        f" && echo '{meta}' > /s/meta/{name}.json && chown -R 65532:65532 /s"
    )
    run(
        "docker",
        "run",
        "--rm",
        "-v",
        f"{PROJECT}_sites:/s",
        "alpine:3.22",
        "sh",
        "-c",
        sh,
    )


def main() -> None:
    keep = "--keep" in sys.argv
    compose = [
        *compose_cmd(),
        "-p",
        PROJECT,
        "-f",
        str(ROOT / "deploy/compose.yaml"),
        "-f",
        str(E2E / "compose.yaml"),
    ]
    (E2E / "Caddyfile").write_text(
        with_acme_ca((ROOT / "deploy/Caddyfile").read_text())
    )
    cid = run("docker", "create", PEBBLE, capture_output=True).stdout.strip()
    try:  # the root that signs Pebble's own API certificate
        run(
            "docker",
            "cp",
            f"{cid}:/test/certs/pebble.minica.pem",
            str(E2E / "pebble.minica.pem"),
        )
    finally:
        run("docker", "rm", cid, capture_output=True)

    env = {**os.environ, "ADMIN_HOST": ADMIN}
    failures: list[str] = []
    try:
        run(*compose, "up", "-d", "--build", "--wait", env=env)
        seed("demo")
        tmp = Path(tempfile.mkdtemp())
        ca = (
            tmp / "pebble-root.pem"
        )  # Pebble makes a new root at each start; issued certificates chain to it
        run("curl", "-sSk", "-o", str(ca), "https://localhost:15000/roots/0")

        def expect(what: str, ok: bool, got: object) -> None:
            print(("PASS " if ok else "FAIL ") + what + ("" if ok else f": got {got}"))
            if not ok:
                failures.append(what)

        code, status, body = curl(
            f"https://demo.{PAGES}:{HTTPS}/", f"demo.{PAGES}", HTTPS, ca
        )
        expect(
            "published page gets a Pebble certificate and is served",
            code == 0 and status.startswith("200") and "hello from demo" in body,
            (code, status, body[:200]),
        )
        code, status, _ = curl(
            f"https://nope.{PAGES}:{HTTPS}/", f"nope.{PAGES}", HTTPS, ca
        )
        expect(
            "unpublished name gets no certificate (TLS error)",
            code in (35, 60),
            (code, status),
        )
        code, status, body = curl(f"https://{ADMIN}:{HTTPS}/", ADMIN, HTTPS, ca)
        expect(
            "admin has a certificate and is proxied",
            code == 0 and "Sites admin" in body,
            (code, status),
        )
        code, status, body = curl(f"https://{PAGES}:{HTTPS}/", PAGES, HTTPS, ca)
        expect(
            "bare domain shows the 404 page with the admin link",
            code == 0 and status.startswith("404") and f"https://{ADMIN}/" in body,
            (code, status, body[:200]),
        )
        code, status, _ = curl(
            f"http://demo.{PAGES}:{HTTP}/", f"demo.{PAGES}", HTTP, ca
        )
        expect(
            "http redirects to https",
            status.startswith("308 https://demo."),
            (code, status),
        )
        seed("later")
        time.sleep(1)
        code, status, body = curl(
            f"https://later.{PAGES}:{HTTPS}/", f"later.{PAGES}", HTTPS, ca
        )
        expect(
            "page published after start gets its certificate",
            code == 0 and "hello from later" in body,
            (code, status),
        )
        if failures:
            run(*compose, "logs", "--tail", "40", "caddy", env=env)
    finally:
        if keep:
            print(f"left running; stop with: {' '.join(compose)} down -v")
        else:
            run(
                *compose, "down", "-v", "--remove-orphans", env=env, capture_output=True
            )
    sys.exit(f"{len(failures)} check(s) failed" if failures else 0)


if __name__ == "__main__":
    main()
