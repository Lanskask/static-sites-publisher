"""Deploy to any Linux server you can ssh into, on any cloud: copy src/ and deploy/, then run docker compose there.

Usage: python3 scripts/ssh_deploy.py user@host [--dir static-sites-publish]

The server needs Docker with the compose plugin and open ports 80 and 443; the ssh user must be able to run docker.
Configuration is deploy/.env (copy deploy/.env.example). Pages and certificates live in Docker volumes, so
redeploying keeps them.
"""

import argparse
import shlex
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def commands(host: str, remote_dir: str) -> list[list[str]]:
    d = shlex.quote(remote_dir)
    return [
        ["ssh", host, f"mkdir -p {d}"],
        [
            "rsync",
            "-az",
            "--delete",
            str(ROOT / "src"),
            str(ROOT / "deploy"),
            f"{host}:{remote_dir}/",
        ],
        [
            "ssh",
            host,
            f"cd {d}/deploy && chmod 600 .env && docker compose up -d --build --remove-orphans",
        ],
    ]


def main(argv: list[str] | None = None) -> int:
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument(
        "host", help="ssh target, e.g. root@203.0.113.7 or a Host from ~/.ssh/config"
    )
    p.add_argument(
        "--dir",
        default="static-sites-publish",
        help="directory on the server, relative to home",
    )
    args = p.parse_args(argv)
    if not (ROOT / "deploy" / ".env").is_file():
        print(
            "deploy/.env is missing: copy deploy/.env.example and fill it in",
            file=sys.stderr,
        )
        return 1
    for cmd in commands(args.host, args.dir):
        print("+", shlex.join(cmd))
        subprocess.run(cmd, check=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
