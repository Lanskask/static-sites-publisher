"""Tests for ssh_deploy.py and dns_check.py (stdlib unittest; run with make test)."""

import tempfile
import unittest
from pathlib import Path

import dns_check
import ssh_deploy


class DeployTest(unittest.TestCase):
    def test_commands_copy_then_compose(self):
        cmds = ssh_deploy.commands("root@1.2.3.4", "my dir")
        self.assertEqual(cmds[0], ["ssh", "root@1.2.3.4", "mkdir -p 'my dir'"])
        self.assertEqual(cmds[1][-1], "root@1.2.3.4:my dir/")
        self.assertTrue(cmds[1][-3].endswith("src") and cmds[1][-2].endswith("deploy"))
        self.assertIn("docker compose up -d --build", cmds[2][2])


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


if __name__ == "__main__":
    unittest.main()
