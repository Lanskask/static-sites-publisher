"""Deploy to any Linux server you can ssh into, on any cloud: copy src/ and deploy/, then run docker compose there.

Usage: python3 scripts/ssh_deploy.py user@host [--dir static-sites-publisher]

The server needs Docker with the compose plugin and open ports 80 and 443; the ssh user must be able to run docker.
Configuration is deploy/.env (copy deploy/.env.example). Pages and certificates live in Docker volumes, so
redeploying keeps them.
"""

import argparse
import os
import shlex
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent
# Streamed into the first command: tar is on every server, rsync is not.
TAR = ["tar", "-C", str(ROOT), "--exclude", "deploy/e2e", "-czf", "-", "src", "deploy"]


def commands(host: str, remote_dir: str) -> list[list[str]]:
    d = shlex.quote(remote_dir)
    return [
        [
            "ssh",
            host,
            f"rm -rf {d}/src {d}/deploy && mkdir -p {d} && tar -xzf - -C {d}",
        ],
        [
            "ssh",
            host,
            f"cd {d}/deploy && chmod 600 .env && docker compose up -d --build --remove-orphans --force-recreate",
        ],
    ]


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument(
        "host", help="ssh target, e.g. root@203.0.113.7 or a Host from ~/.ssh/config"
    )
    p.add_argument(
        "--dir",
        default="static-sites-publisher",
        help="directory on the server, relative to home",
    )
    args = p.parse_args(argv)
    if not (ROOT / "deploy" / ".env").is_file():
        print(
            "deploy/.env is missing: copy deploy/.env.example and fill it in",
            file=sys.stderr,
        )
        return 1
    copy, up = commands(args.host, args.dir)
    print("+", shlex.join(TAR), "|", shlex.join(copy))
    # COPYFILE_DISABLE: macOS tar would add ._* files for extended attributes
    tar = subprocess.Popen(
        TAR, stdout=subprocess.PIPE, env={**os.environ, "COPYFILE_DISABLE": "1"}
    )
    subprocess.run(copy, stdin=tar.stdout, check=True)
    tar.stdout.close()
    if tar.wait():
        raise SystemExit("tar failed")
    print("+", shlex.join(up))
    subprocess.run(up, check=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
