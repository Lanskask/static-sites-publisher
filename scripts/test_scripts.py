"""Tests for ssh_deploy.py, dns_check.py and e2e.py (stdlib unittest; run with make test)."""

import tempfile
import unittest
from pathlib import Path

import dns_check
import e2e
import ssh_deploy


class DeployTest(unittest.TestCase):
    def test_commands_copy_then_compose(self):
        # the server needs only ssh, tar and Docker: no rsync (a plain Debian image has none)
        copy, up = ssh_deploy.commands("root@1.2.3.4", "my dir")
        self.assertEqual(ssh_deploy.TAR[-2:], ["src", "deploy"])
        self.assertIn(
            "deploy/e2e", ssh_deploy.TAR
        )  # local test files and Terraform state stay home
        self.assertEqual(copy[:2], ["ssh", "root@1.2.3.4"])
        self.assertIn("rm -rf 'my dir'/src 'my dir'/deploy", copy[2])
        self.assertIn("tar -xzf - -C 'my dir'", copy[2])
        self.assertIn("docker compose up -d --build", up[2])
        # a new Caddyfile is a new file: a running container would keep the old one bind-mounted
        self.assertIn("--force-recreate", up[2])


class DnsCheckTest(unittest.TestCase):
    def test_read_env_skips_comments_and_quotes(self):
        with tempfile.TemporaryDirectory() as d:
            p = Path(d) / ".env"
            p.write_text(
                "# c\nPAGES_DOMAIN=sites.example.com\nADMIN_HOST='admin.x'\n\nBAD\n"
            )
            self.assertEqual(
                dns_check.read_env(p),
                {"PAGES_DOMAIN": "sites.example.com", "ADMIN_HOST": "admin.x"},
            )

    def test_all_on_one_ip_is_ok(self):
        self.assertEqual(
            dns_check.check("s.test", "admin.s.test", None, lambda n: {"1.2.3.4"}), []
        )

    def test_missing_wildcard_is_reported(self):
        res = lambda n: set() if n.startswith("probe-") else {"1.2.3.4"}
        problems = dns_check.check("s.test", "admin.s.test", "1.2.3.4", res)
        self.assertEqual(len(problems), 1)
        self.assertIn("*.s.test", problems[0])

    def test_wrong_ip_and_unresolved_domain(self):
        self.assertEqual(
            len(
                dns_check.check("s.test", "a.s.test", "9.9.9.9", lambda n: {"1.2.3.4"})
            ),
            3,
        )
        self.assertIn(
            "does not resolve",
            dns_check.check("s.test", "a.s.test", None, lambda n: set())[0],
        )


class E2ETest(unittest.TestCase):
    def test_acme_ca_goes_into_the_real_caddyfile_global_options(self):
        out = e2e.with_acme_ca((e2e.ROOT / "deploy/Caddyfile").read_text())
        head = out.split("\n}\n", 1)[0]  # the global options block
        self.assertIn("acme_ca https://pebble:14000/dir", head)
        self.assertIn("acme_ca_root /e2e/pebble.minica.pem", head)

    def test_fails_loudly_when_the_anchor_line_moves(self):
        with self.assertRaises(ValueError):
            e2e.with_acme_ca("{\n}\n")


if __name__ == "__main__":
    unittest.main()
