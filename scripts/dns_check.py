"""Check DNS before deploying, with any DNS provider: PAGES_DOMAIN, a random name under it (proves the wildcard
record) and ADMIN_HOST must all resolve to the same IPv4 address, the server's.

Usage: python3 scripts/dns_check.py [SERVER_IP]
Reads PAGES_DOMAIN and ADMIN_HOST from deploy/.env. Without SERVER_IP, the address of PAGES_DOMAIN is expected.
"""

import secrets
import socket
import sys
from collections.abc import Callable
from pathlib import Path

ENV = Path(__file__).resolve().parent.parent / "deploy" / ".env"


def read_env(path: Path) -> dict[str, str]:
    out = {}
    for line in path.read_text().splitlines():
        line = line.strip()
        if line and not line.startswith("#") and "=" in line:
            k, v = line.split("=", 1)
            out[k.strip()] = v.strip().strip("\"'")
    return out


def resolve(name: str) -> set[str]:
    try:
        return {str(a[4][0]) for a in socket.getaddrinfo(name, None, socket.AF_INET)}
    except socket.gaierror:
        return set()


def check(
    pages: str, admin: str, want: str | None, res: Callable[[str], set[str]] = resolve
) -> list[str]:
    """Returns the problems found; empty means DNS is ready."""
    names = [pages, f"probe-{secrets.token_hex(4)}.{pages}", admin]
    got = {n: res(n) for n in names}
    if want is None:
        if not got[pages]:
            return [f"{pages} does not resolve: add an A record for it"]
        want = min(got[pages])
    problems = []
    for n, addrs in got.items():
        if want not in addrs:
            hint = f"*.{pages}" if n.startswith("probe-") else n
            problems.append(
                f"{n} resolves to {sorted(addrs) or 'nothing'}, want {want}: add an A record for {hint}"
            )
    return problems


def main(argv: list[str]) -> int:
    if not ENV.is_file():
        print(
            "deploy/.env is missing: copy deploy/.env.example and fill it in",
            file=sys.stderr,
        )
        return 1
    env = read_env(ENV)
    problems = check(env["PAGES_DOMAIN"], env["ADMIN_HOST"], argv[0] if argv else None)
    for p in problems:
        print("FAIL", p)
    if not problems:
        print(
            f"OK: {env['PAGES_DOMAIN']}, *.{env['PAGES_DOMAIN']} and {env['ADMIN_HOST']} point at one server"
        )
    return 1 if problems else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
