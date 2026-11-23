#!/usr/bin/env python3
"""Exercise the Compose deployment using real Docker Hosts and Redis."""

import json
import os
from pathlib import Path
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid


ROOT = Path(__file__).resolve().parents[1]
BASE = ""
PROJECT = f"harness-smoke-{uuid.uuid4().hex[:8]}"


def run(*args, env=None):
    result = subprocess.run(args, cwd=ROOT, env=env, text=True, capture_output=True, check=False)
    if result.returncode:
        raise RuntimeError(f"{args[0]} {args[1]} failed: {result.stderr.strip()}")
    return result.stdout.strip()


def call(method, path, user=None, payload=None, expected=200):
    headers = {"Content-Type": "application/json"}
    if user:
        headers["X-User-ID"] = user
    data = json.dumps(payload).encode() if payload is not None else None
    request = urllib.request.Request(BASE + path, data, headers, method=method)
    try:
        with urllib.request.urlopen(request, timeout=35) as response:
            status, body = response.status, response.read()
    except urllib.error.HTTPError as error:
        status, body = error.code, error.read()
    if status != expected:
        raise AssertionError(f"{method} {path} returned {status}, expected {expected}: {body!r}")
    if not body or status >= 400:
        return None
    if path == "/":
        return body.decode()
    return json.loads(body)


def wait_for(check, timeout=35):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            if check():
                return
        except (AssertionError, RuntimeError, urllib.error.URLError):
            pass
        time.sleep(0.3)
    raise AssertionError("timed out waiting for deployment")


def main():
    global BASE
    work = ROOT / ".cache"
    work.mkdir(exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="compose-smoke-", dir=work) as directory:
        profile = Path(directory) / ".env"
        profile_text = ("HOST_IMAGE=" + os.environ.get("HARNESS_TEST_HOST_IMAGE", "ghcr.io/xyc999-ai/go-bot-host:latest") + "\n"
                        "ALIBABA_CLOUD_ACCESS_KEY_SECRET=literal$not_interpolated #fixture=1\n")
        profile.write_text(profile_text)
        profile.chmod(0o600)
        ports = Path(directory) / "ports.yaml"
        ports.write_text(
            'services:\n'
            '  controller:\n'
            '    ports: !override ["127.0.0.1::8080"]\n'
            '  redis:\n'
            '    ports: !override ["127.0.0.1::6379"]\n'
        )
        env = dict(os.environ, GO_BOT_ENV_DIR=str(profile.parent), GO_BOT_ENV_NAME=profile.name,
                   GO_BOT_CONFIG_FILE=str(ROOT / "configs/default.yaml"))
        alice, bob = (f"{PROJECT}-{name}" for name in ("alice", "bob"))
        names = {}
        sessions = {}
        main_sessions = {}

        def compose(*args):
            return run("docker", "compose", "--project-directory", str(profile.parent), "-p", PROJECT,
                       "-f", str(ROOT / "compose.yaml"), "-f", str(ports), *args, env=env)

        def redis(*args):
            return compose("exec", "-T", "redis", "redis-cli", "--raw", *args)

        def record(user):
            raw = compose("exec", "-T", "redis", "redis-cli", "--json", "HGETALL", f"user:{user}")
            return {key: value for key, value in json.loads(raw).items() if value != ""}

        try:
            compose("up", "-d", "--no-build", "--pull", "never", "--wait")
            BASE = "http://" + compose("port", "controller", "8080")
            wait_for(lambda: call("GET", "/readyz", expected=200) is None)
            assert profile.read_text() == profile_text
            assert "Go Bot" in call("GET", "/")
            assert redis("DBSIZE") == "0"
            call("POST", "/v1/host/start", "unknown", expected=404)

            for account in (alice, bob):
                model = {"apiKey": f"fixture-{account}", "baseUrl": f"https://models.example/{account}/v1", "name": account, "models": [account]}
                payload = {"userId": account}
                call("POST", "/v1/accounts", payload=payload, expected=201)
                call("POST", "/v1/accounts", payload=payload, expected=409)
                assert redis("TYPE", f"user:{account}") == "hash"
                created = record(account)
                assert created["desiredState"] == "stopped"
                main_sessions[account] = created["mainSessionId"]
                uuid.UUID(main_sessions[account])
                defaults = json.loads(created["plugins"])["packages"]
                assert set(defaults) == {"@go-bot/plugin-officecli", "@go-bot/plugin-agent-browser"}
                assert all(package["enabled"] for package in defaults.values())
                assert redis("TTL", f"user:{account}") == "-1"
                assert call("POST", "/v1/host/start", account) == {"mainSessionId": main_sessions[account]}
                state = call("GET", "/v1/host", account)
                assert state["status"] == "ready"
                names[account] = f"go-bot-{state['hostId']}"
                port = run("docker", "port", names[account], "8080/tcp").rsplit(":", 1)[-1]
                saved = record(account)
                assert saved["hostEndpoint"] == f"http://host.docker.internal:{port}"
                call("POST", "/v1/sessions", account, expected=409)
                endpoint = call("POST", "/v1/settings/models/endpoints", account, model, expected=201)
                session = call("POST", "/v1/sessions", account, expected=201)
                sessions[account] = session["sessionId"]
                rows = call("GET", "/v1/sessions", account)["sessions"]
                assert {row["sessionId"] for row in rows} == {sessions[account], main_sessions[account]}
                assert next(row for row in rows if row["sessionId"] == main_sessions[account])["isMain"]
                selection = call("GET", f"/v1/sessions/{sessions[account]}/model", account)
                assert selection["selection"] == {"endpointId": endpoint["endpointId"], "modelId": account}
                configured = call("GET", "/v1/settings/models", account)["endpoints"][endpoint["endpointId"]]
                assert "apiKey" not in configured and configured["hasApiKey"]
                fields = record(account)
                assert json.loads(fields["llm"])["endpoints"][endpoint["endpointId"]]["apiKey"] == model["apiKey"]
                assert json.loads(fields["sessionModels"]) == {sessions[account]: selection["selection"]}
                assert fields["modelRevision"] == "2"
            assert sessions[alice] != sessions[bob]
            call("GET", f"/v1/sessions/{sessions[alice]}", bob, expected=404)
            search = {"provider": "deepseek", "apiKey": "fixture-search"}
            image = {"provider": "openai", "apiKey": "fixture-image", "model": "gpt-image-1"}
            for route, field, settings in (("web-search", "webSearch", search),
                                            ("image-generation", "imageGeneration", image)):
                view = call("PUT", "/v1/settings/" + route, bob, settings)
                provider = settings["provider"]
                assert view["provider"] == provider and view["entries"][provider]["hasApiKey"]
                assert "apiKey" not in view["entries"][provider]
                assert json.loads(record(bob)[field]) == {
                    "provider": provider,
                    "entries": {provider: {key: value for key, value in settings.items() if key != "provider"}},
                }
                assert field not in record(alice)
            assert set(redis("KEYS", "*").splitlines()) == {f"user:{alice}", f"user:{bob}"}
            assert names[alice] != names[bob]
            host_ports = {
                run("docker", "port", name, "8080/tcp").rsplit(":", 1)[-1]
                for name in names.values()
            }
            assert len(host_ports) == 2 and "8080" not in host_ports
            for port in host_ports:
                request = urllib.request.Request(f"http://127.0.0.1:{port}/readyz")
                try:
                    urllib.request.urlopen(request, timeout=3).close()
                except urllib.error.HTTPError as error:
                    assert error.code == 401
                else:
                    raise AssertionError("direct Host request bypassed its bearer token")
            for name in names.values():
                metadata = json.loads(run("docker", "inspect", name))[0]
                assert "com.docker.compose.project" not in metadata["Config"]["Labels"]
            old_bob = record(bob)
            old_alice = record(alice)
            call("GET", f"/v1/hosts/{old_alice['hostId']}", bob, expected=404)

            # Recreate Controller while the Host containers keep running.
            compose("up", "-d", "--force-recreate", "--no-deps", "controller")
            BASE = "http://" + compose("port", "controller", "8080")
            wait_for(lambda: call("GET", "/readyz") is None)
            rows = call("GET", "/v1/sessions", alice)["sessions"]
            assert {row["sessionId"] for row in rows} == {sessions[alice], main_sessions[alice]}
            assert call("POST", "/v1/host/start", alice) == {"mainSessionId": main_sessions[alice]}
            assert record(alice)["hostId"] == old_alice["hostId"]

            compose("stop", "controller")
            compose("restart", "redis")
            wait_for(lambda: redis("PING") == "PONG")
            compose("start", "controller")
            BASE = "http://" + compose("port", "controller", "8080")
            wait_for(lambda: call("GET", "/readyz") is None)
            assert record(bob) == old_bob
            assert call("GET", "/v1/host", bob)["status"] == "ready"
            selection = call("GET", f"/v1/sessions/{sessions[bob]}/model", bob)["selection"]
            assert selection == json.loads(old_bob["sessionModels"])[sessions[bob]]
            call("POST", "/v1/host/stop", bob, expected=204)
            assert call("GET", "/v1/host", bob)["status"] == "stopped"
            stopped_bob = record(bob)
            assert stopped_bob["hostId"] == old_bob["hostId"] and "hostEndpoint" not in stopped_bob
            assert call("POST", "/v1/host/start", bob) == {"mainSessionId": main_sessions[bob]}
            assert record(bob)["hostId"] == old_bob["hostId"]
            for account in names:
                host_id = record(account)["hostId"]
                call("DELETE", "/v1/host/instance", account, expected=204)
                released = record(account)
                assert released["desiredState"] == "stopped"
                assert {"hostId", "hostToken", "hostEndpoint"}.isdisjoint(released)
                settings_fields = {"mainSessionId", "plugins", "llm", "sessionModels", "modelRevision", "webSearch", "imageGeneration"}
                before = old_bob if account == bob else old_alice
                assert {key: value for key, value in released.items() if key in settings_fields} == {
                    key: value for key, value in before.items() if key in settings_fields
                }
                call("GET", f"/v1/hosts/{host_id}", account, expected=404)
            assert redis("DBSIZE") == "2"
            print("Compose smoke passed: account Hashes, isolated Hosts, Redis restart, stop and release.")
        except Exception:
            print(compose("logs", "--tail", "25", "controller"))
            raise
        finally:
            for account in (alice, bob):
                if account not in names:
                    try:
                        saved = record(account)
                    except (ValueError, RuntimeError):
                        continue
                    host_id = saved.get("hostId")
                    if host_id:
                        names[account] = f"go-bot-{host_id}"
            for name in names.values():
                subprocess.run(["docker", "rm", "-f", name], capture_output=True, check=False)
            for account in (alice, bob):
                subprocess.run(["docker", "volume", "rm", f"go-bot-{account}-data"], capture_output=True, check=False)
            compose("down", "--volumes", "--remove-orphans")


if __name__ == "__main__":
    main()
