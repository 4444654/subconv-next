#!/usr/bin/env python3
"""Installer regressions in an isolated directory; no host service changes."""
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


class NativeInstallerTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory(prefix="scn-regression-")
        self.root = Path(self.tmp.name)
        source = (Path(__file__).resolve().parents[1] / "subconv-next-onekey.sh").read_text()
        for path in ("/usr/local/bin", "/usr/bin/scn", "/etc/subconv-next",
                     "/etc/systemd/system", "/var/lib/subconv-next", "/opt/subconv-next", "/run/lock"):
            source = source.replace(path, str(self.root) + path)
        source = source.replace('RUN_USER="subconv-next"', 'RUN_USER="root"')
        self.script = self.root / "installer.sh"
        self.script.write_text(source)
        self.manager = self.root / "usr/local/bin/scn"
        self.binary = self.root / "usr/local/bin/subconv-next"
        self.config = self.root / "etc/subconv-next/config.json"
        self.env_file = self.root / "etc/subconv-next/subconv-next.env"
        self.unit = self.root / "etc/systemd/system/subconv-next.service"
        for path in (self.root / "usr/bin", self.unit.parent, self.root / "run/lock"):
            path.mkdir(parents=True, exist_ok=True)
        mocks = self.root / "mocks"
        mocks.mkdir()
        systemctl = mocks / "systemctl"
        systemctl.write_text("""#!/usr/bin/env bash
set -e
printf '%s\\n' "$*" >> "$SCN_TEST_ROOT/calls"
case "$1" in
  is-active) test -f "$SCN_TEST_ROOT/active" ;;
  is-enabled) test -f "$SCN_TEST_ROOT/enabled" ;;
  start|restart) touch "$SCN_TEST_ROOT/active" ;;
  stop) rm -f "$SCN_TEST_ROOT/active" ;;
  enable) touch "$SCN_TEST_ROOT/enabled" ;;
  disable) rm -f "$SCN_TEST_ROOT/enabled" ;;
esac
""")
        systemctl.chmod(0o755)
        self.env = dict(os.environ, PATH=str(mocks) + ":" + os.environ["PATH"],
                        SCN_TEST_ROOT=str(self.root))
        self.overrides = """
check_system() { ARCH=amd64; }
install_deps() { :; }
port_available() { :; }
show_access() { :; }
health_check() { [[ ! -e "$SCN_TEST_ROOT/fail-health" ]]; }
prepare_binary() {
  WORK_DIR=$(mktemp -d "$SCN_TEST_ROOT/work.XXXXXX")
  printf '#!/usr/bin/env bash\\necho new-test-version\\n' > "$WORK_DIR/candidate"
  chmod +x "$WORK_DIR/candidate"
}
"""

    def tearDown(self):
        self.tmp.cleanup()

    def run_shell(self, body, expected=0, input=None):
        code = f'source "{self.script}"\n' + self.overrides + "\n" + body
        result = subprocess.run(["bash", "-c", code], env=self.env, text=True, input=input,
                                stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        if expected == 0:
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout + result.stderr)
        return result

    def install(self):
        self.run_shell("main install")

    def test_fresh_install_and_menu_reopen(self):
        self.install()
        self.assertTrue(self.manager.stat().st_mode & 0o111)
        self.assertTrue((self.root / "enabled").exists())
        self.assertIn("restart subconv-next.service", (self.root / "calls").read_text())
        token = next(line.split("=", 1)[1] for line in self.env_file.read_text().splitlines()
                     if line.startswith("SUBCONV_ACCESS_TOKEN="))
        self.assertEqual(len(token), 48)
        self.assertEqual(self.env_file.stat().st_mode & 0o777, 0o640)
        result = subprocess.run(["bash", str(self.manager)], input="0\n", env=self.env,
                                text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("管理 v1.4.1", result.stdout)
        result = subprocess.run(["bash", str(self.manager)], input="bad\n\n0\n",
                                env=self.env, text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertGreaterEqual(result.stdout.count("管理 v1.4.1"), 2)

    def test_stream_execution_saves_manager_even_if_download_fails(self):
        source = self.script.read_text()
        source = source.rsplit('if [[ "${BASH_SOURCE[0]}" == "$0" ]]', 1)[0]
        source += self.overrides + '\nprepare_binary() { return 42; }\nmain install\n'
        streamed = self.root / "streamed.sh"
        streamed.write_text(source)
        result = subprocess.run(["bash", "-c", 'bash <(cat "$1")', "bash", str(streamed)],
                                env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 42, result.stdout + result.stderr)
        self.assertTrue(self.manager.is_file())
        result = subprocess.run(["bash", str(self.manager), "--help"],
                                env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("repair", result.stdout)

    def test_update_preserves_token_config_and_data(self):
        self.install()
        original_env = self.env_file.read_bytes()
        self.config.write_text('{"subscriptions": [], "render": {"emoji": true}}\n')
        original_config = self.config.read_bytes()
        data = self.root / "var/lib/subconv-next/important-data"
        data.write_text("keep me")
        self.run_shell("main update")
        self.assertEqual(self.env_file.read_bytes(), original_env)
        self.assertEqual(self.config.read_bytes(), original_config)
        self.assertEqual(data.read_text(), "keep me")

    def test_failed_update_restores_files_and_service(self):
        self.install()
        self.binary.write_text("#!/usr/bin/env bash\necho old-version\n")
        originals = {p: p.read_bytes() for p in (self.binary, self.env_file, self.config, self.unit)}
        (self.root / "fail-health").touch()
        self.run_shell("main update", expected=1)
        for path, content in originals.items():
            self.assertEqual(path.read_bytes(), content)
        self.assertTrue((self.root / "active").exists())
        self.assertTrue((self.root / "enabled").exists())

    def test_failed_first_install_does_not_report_success(self):
        (self.root / "fail-health").touch()
        result = self.run_shell("main install", expected=1)
        self.assertNotIn("安装/更新成功", result.stdout)
        self.assertFalse(self.binary.exists())
        self.assertFalse((self.root / "active").exists())
        self.assertTrue(self.manager.exists())

    def test_port_leading_zero_and_settings_rollback(self):
        self.install()
        self.run_shell("main port 09876")
        self.assertIn("SUBCONV_PORT=9876", self.env_file.read_text())
        original = self.env_file.read_bytes()
        self.run_shell("main port 65536", expected=1)
        self.assertEqual(self.env_file.read_bytes(), original)
        (self.root / "fail-health").touch()
        self.run_shell("main port 12345", expected=1)
        self.assertEqual(self.env_file.read_bytes(), original)

    def test_environment_values_are_never_executed(self):
        self.install()
        marker = self.root / "unexpected-command"
        with self.env_file.open("a") as handle:
            handle.write(f"SUBCONV_PUBLIC_BASE_URL=$(touch {marker})\n")
        self.run_shell("env_get SUBCONV_PUBLIC_BASE_URL >/dev/null")
        self.assertFalse(marker.exists())
        self.run_shell('env_set SUBCONV_PUBLIC_BASE_URL "https://example.com/a?x=1&y=2"')
        self.assertIn("SUBCONV_PUBLIC_BASE_URL=https://example.com/a?x=1&y=2", self.env_file.read_text())

    def test_registration_toggle_preserves_settings_and_rolls_back(self):
        self.install()
        original = self.env_file.read_bytes()
        self.run_shell("main registration off")
        self.assertIn("SUBCONV_REGISTRATION_ENABLED=false\n", self.env_file.read_text())
        before = [line for line in original.decode().splitlines()
                  if not line.startswith("SUBCONV_REGISTRATION_ENABLED=")]
        after = [line for line in self.env_file.read_text().splitlines()
                 if not line.startswith("SUBCONV_REGISTRATION_ENABLED=")]
        self.assertEqual(before, after)
        disabled = self.env_file.read_bytes()
        self.run_shell("main registration invalid", expected=1)
        self.assertEqual(self.env_file.read_bytes(), disabled)
        (self.root / "fail-health").touch()
        self.run_shell("main registration on", expected=1)
        self.assertEqual(self.env_file.read_bytes(), disabled)

    def test_registration_menu_changes_setting_and_refreshes_status(self):
        self.install()
        settings = [line for line in self.env_file.read_text().splitlines()
                    if not line.startswith("SUBCONV_REGISTRATION_ENABLED=")]
        for selection, enabled, status in (("2", "false", "已关闭"),
                                            ("1", "true", "已开启")):
            result = subprocess.run(["bash", str(self.manager)],
                                    input=f"15\n{selection}\n\n0\n", env=self.env,
                                    text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            self.assertIn("当前注册开关", result.stdout)
            self.assertIn(f"15. 开启 / 关闭注册（{status}）", result.stdout)
            self.assertIn(f"SUBCONV_REGISTRATION_ENABLED={enabled}\n",
                          self.env_file.read_text())
            self.assertEqual(settings, [line for line in self.env_file.read_text().splitlines()
                                       if not line.startswith("SUBCONV_REGISTRATION_ENABLED=")])
        original = self.env_file.read_bytes()
        for selection in ("0", "bad"):
            result = subprocess.run(["bash", str(self.manager)],
                                    input=f"15\n{selection}\n\n0\n", env=self.env,
                                    text=True, capture_output=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual(self.env_file.read_bytes(), original)
        self.env_file.write_text("\n".join(settings) + "\n")
        self.config.write_text('{"service":{"registration_enabled":false}}\n')
        result = subprocess.run(["bash", str(self.manager)], input="0\n", env=self.env,
                                text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("15. 开启 / 关闭注册（已关闭）", result.stdout)

    def test_proxy_url_change_preserves_credentials_and_rolls_back_on_failure(self):
        self.install()
        self.prepare_account_binary()
        self.run_shell("main account", input="operator\npassphrase123\npassphrase123\n")
        data = self.root / "var/lib/subconv-next/important-data"
        data.write_text("keep me")
        originals = {p: p.read_bytes() for p in (self.binary, self.config, self.unit, data)}
        settings = [line for line in self.env_file.read_text().splitlines()
                    if not line.startswith("SUBCONV_PUBLIC_BASE_URL=")]
        self.run_shell('main url "https://sub.example.com:8443/"')
        self.assertIn("SUBCONV_PUBLIC_BASE_URL=https://sub.example.com:8443\n",
                      self.env_file.read_text())
        self.assertEqual(settings, [line for line in self.env_file.read_text().splitlines()
                                   if not line.startswith("SUBCONV_PUBLIC_BASE_URL=")])
        for path, content in originals.items():
            self.assertEqual(path.read_bytes(), content)
        original_env = self.env_file.read_bytes()
        (self.root / "fail-health").touch()
        self.run_shell('main url "https://other.example.com"', expected=1)
        self.assertEqual(self.env_file.read_bytes(), original_env)
        for path, content in originals.items():
            self.assertEqual(path.read_bytes(), content)
        self.assertTrue((self.root / "active").exists())

    def prepare_account_binary(self):
        hashed = "$2a$10$" + "A" * 53
        self.binary.write_text("#!/usr/bin/env bash\n"
                               'test "$1" = hash-password || exit 2\n'
                               'cat > "$SCN_TEST_ROOT/password-stdin"\n'
                               f"printf '%s\\n' '{hashed}'\n")
        return hashed

    def test_account_password_is_hashed_and_token_is_preserved(self):
        self.install()
        hashed = self.prepare_account_binary()
        original_token = next(line for line in self.env_file.read_text().splitlines()
                              if line.startswith("SUBCONV_ACCESS_TOKEN="))
        password = "  passphrase $ with spaces!  "
        result = self.run_shell("main account", input=f"operator\n{password}\n{password}\n")
        contents = self.env_file.read_text()
        self.assertIn("SUBCONV_MANAGEMENT_USERNAME=operator", contents)
        self.assertIn("SUBCONV_MANAGEMENT_PASSWORD_HASH=" + hashed, contents)
        self.assertIn(original_token, contents)
        self.assertNotIn(password, contents + result.stdout + result.stderr)
        self.assertEqual((self.root / "password-stdin").read_text(), password)
        original_env = self.env_file.read_bytes()
        self.run_shell("main update")
        self.assertEqual(self.env_file.read_bytes(), original_env)

    def test_failed_account_change_restores_previous_settings(self):
        self.install()
        self.prepare_account_binary()
        original = self.env_file.read_bytes()
        (self.root / "fail-health").touch()
        self.run_shell("main account", expected=1, input="operator\npassphrase123\npassphrase123\n")
        self.assertEqual(self.env_file.read_bytes(), original)
        self.assertTrue((self.root / "active").exists())

    def test_mismatched_account_passwords_do_not_change_settings(self):
        self.install()
        original = self.env_file.read_bytes()
        self.run_shell("main account", expected=1, input="operator\npassphrase123\notherpass123\n")
        self.assertEqual(self.env_file.read_bytes(), original)


if __name__ == "__main__":
    unittest.main(verbosity=2)
