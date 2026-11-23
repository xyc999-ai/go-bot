#!/usr/bin/env python3
"""Test the installer and source launcher with local HTTP files and real image tools.

Registry pulls are replaced with local image checks so CI never needs a published
GHCR version. Container checks still run the actual Controller image.
"""
import functools
import http.server
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import threading
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]


class QuietHandler(http.server.SimpleHTTPRequestHandler):
    def log_message(self, *_):
        pass


def main():
    work = ROOT / ".cache"
    work.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="install-test-", dir=work) as directory:
        stage = Path(directory)
        source = stage / "source"
        for name in ("compose.yaml", ".env.example", "configs/default.yaml", "scripts/install.sh", "scripts/start.sh"):
            target = source / name
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(ROOT / name, target)
        handler = functools.partial(QuietHandler, directory=str(source))
        server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        wrapper = stage / "tools"
        wrapper.mkdir()
        real_docker = shutil.which("docker")
        assert real_docker
        (wrapper / "docker").write_text(
            "#!/usr/bin/env python3\n"
            "import os,sys\n"
            "a=sys.argv[1:]\n"
            "if a[:1]==['pull']:\n"
            "  with open(os.environ['PULL_LOG'],'a') as f: f.write(a[1]+'\\n')\n"
            "  a=['image','inspect',a[1]]\n"
            "  sys.stdout=open(os.devnull,'w'); os.dup2(sys.stdout.fileno(),1)\n"
            "os.execv(os.environ['REAL_DOCKER'],['docker',*a])\n"
        )
        (wrapper / "docker").chmod(0o755)
        log = stage / "pulls"
        env = dict(os.environ, PATH=str(wrapper) + os.pathsep + os.environ["PATH"],
                   REAL_DOCKER=real_docker, PULL_LOG=str(log),
                   GO_BOT_INSTALL_SOURCE=f"http://127.0.0.1:{server.server_port}")
        for line in (ROOT / ".env.example").read_text().splitlines():
            if line and not line.startswith("#"):
                env.pop(line.split("=", 1)[0], None)
        for key in ("GO_BOT_ENV_DIR", "GO_BOT_ENV_NAME", "GO_BOT_CONFIG_FILE", "COMPOSE_FILE", "COMPOSE_PROJECT_NAME"):
            env.pop(key, None)

        def run(script, *args, expected=0, overrides=None):
            result = subprocess.run(["bash", str(script), *args], cwd=stage,
                                    env=dict(env, **(overrides or {})), capture_output=True, text=True)
            if (result.returncode == 0) != (expected == 0):
                raise AssertionError(f"{script.name} returned {result.returncode}: {result.stderr}\n{result.stdout}")
            return result

        try:
            installed = stage / "installed"
            run(ROOT / "scripts/install.sh", "--dir", str(installed))
            assert (installed / "compose.yaml").read_bytes() == (ROOT / "compose.yaml").read_bytes()
            assert (installed / "configs/default.yaml").read_bytes() == (ROOT / "configs/default.yaml").read_bytes()
            assert (installed / ".env").read_bytes() == (ROOT / ".env.example").read_bytes()
            assert (installed / ".env").stat().st_mode & 0o777 == 0o600
            assert not (installed / ".env.eci").exists() and not (installed / "start.sh").exists()
            assert not (installed / "scripts/start.sh").exists()
            pulls = log.read_text().splitlines()
            assert len(pulls) == 3 and any("go-bot-host:" in image for image in pulls)
            controller_image = next(image for image in pulls if "go-bot-controller:" in image)
            # A root Controller must not turn a user's 0600 profile into a root-owned file.
            subprocess.run([real_docker, "run", "--rm", "--pull=never", "--entrypoint", "/bin/sh",
                            controller_image, "-ec",
                            'mkdir /tmp/profile; printf "CONTROLLER_LISTEN=127.0.0.1:8080\\n" > /tmp/profile/.env; '
                            'chown 1234:1234 /tmp/profile/.env; '
                            'CONTROLLER_LISTEN=127.0.0.1:8099 prepare-env --env-file /tmp/profile/.env; '
                            'test "$(stat -c %u:%g /tmp/profile/.env)" = 1234:1234'], check=True)

            # Preserve user tuning and literal credentials, including '$' and spaces.
            profile = installed / ".env"
            text = profile.read_text().replace("ALIBABA_CLOUD_ACCESS_KEY_SECRET=", "ALIBABA_CLOUD_ACCESS_KEY_SECRET=fixture$literal #value=1")
            profile.write_text(text)
            tuning = installed / "configs/default.yaml"
            tuning.write_text(tuning.read_text().replace("start_timeout: 5m", "start_timeout: 7m"))
            saved_tuning = tuning.read_bytes()
            run(ROOT / "scripts/install.sh", "--dir", str(installed))
            assert profile.read_text() == text and tuning.read_bytes() == saved_tuning
            run(ROOT / "scripts/install.sh", "--dir", str(installed), overrides={"CONTROLLER_LISTEN": "127.0.0.1:8099"})
            assert "CONTROLLER_LISTEN=127.0.0.1:8099" in profile.read_text()
            assert "fixture$literal #value=1" in profile.read_text()

            broken = stage / "incomplete"
            run(ROOT / "scripts/install.sh", "--dir", str(broken), expected=1,
                overrides={"GO_BOT_INSTALL_SOURCE": env["GO_BOT_INSTALL_SOURCE"] + "/missing"})
            assert not (broken / ".env").exists()

            # The independent source launcher initializes without install.sh,
            # and fails for a missing Host image without trying to pull/build it.
            run(source / "scripts/start.sh", "--init-env")
            assert (source / ".env.eci").is_file()
            assert "HARNESS_RUNTIME=eci" in (source / ".env.eci").read_text()
            private = source / ".env"
            private.write_text(private.read_text().replace(
                next(line for line in private.read_text().splitlines() if line.startswith("HOST_IMAGE=")),
                "HOST_IMAGE=go-bot/missing-installer-test:never"))
            before = log.read_text()
            result = run(source / "scripts/start.sh", expected=1)
            assert "Host image is missing" in result.stderr
            assert log.read_text() == before
            run(source / "scripts/start.sh", "--init-env")
            eci = (source / ".env.eci").read_text()
            assert "HARNESS_RUNTIME=eci" in eci
            assert "HOST_IMAGE=ghcr.io/xyc999-ai/go-bot-host:latest" in eci
            # Both entry paths boot actual Controller/Redis containers on isolated
            # random ports. The installed directory has no source, Go or start.sh.
            ports = stage / "ports.yaml"
            ports.write_text('services:\n  controller:\n    ports: !override ["127.0.0.1::8080"]\n'
                             '    environment:\n      CONTROLLER_LISTEN: "127.0.0.1:8098"\n'
                             '  redis:\n    ports: !override ["127.0.0.1::6379"]\n')
            (source / ".env").write_bytes((ROOT / ".env.example").read_bytes())
            for deployment, source_start in ((installed, False), (source, True)):
                project = "install-qa-" + uuid.uuid4().hex[:8]
                launch_env = dict(env, COMPOSE_PROJECT_NAME=project,
                                  COMPOSE_FILE=str(deployment / "compose.yaml") + os.pathsep + str(ports))
                compose = [real_docker, "compose", "--project-directory", str(deployment)]
                original_owner = (deployment / ".env").stat().st_uid
                try:
                    if source_start:
                        result = subprocess.run(["bash", str(source / "scripts/start.sh")], env=launch_env,
                                                text=True, capture_output=True)
                        assert result.returncode == 0, result.stderr + result.stdout
                    else:
                        subprocess.run([*compose, "up", "-d", "--wait"], env=launch_env, check=True,
                                       stdout=subprocess.DEVNULL)
                    address = subprocess.check_output([*compose, "port", "controller", "8080"],
                                                      env=launch_env, text=True).strip()
                    with urllib.request.urlopen("http://" + address, timeout=10) as response:
                        assert b"Go Bot" in response.read()
                    saved = (deployment / ".env").read_text()
                    assert (deployment / ".env").stat().st_uid == original_owner
                    assert "CONTROLLER_LISTEN=127.0.0.1:8098" in saved
                    if not source_start:
                        assert "fixture$literal #value=1" in saved
                finally:
                    subprocess.run([*compose, "down", "--volumes", "--remove-orphans"],
                                   env=launch_env, check=True, stdout=subprocess.DEVNULL)
            assert log.read_text() == before, "startup unexpectedly pulled an image"
            print("Install/start checks passed: shared files, private profile, literal values, repeat install, missing image and interrupted download.")
        finally:
            server.shutdown()
            server.server_close()


if __name__ == "__main__":
    main()
