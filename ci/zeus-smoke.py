#!/usr/bin/env python3
"""Run the built application with a disposable DB and real Zeus completions.

No tasks or scans are started. No production account or database is modified.
The temporary setup password, JWT and server log are never published.
"""
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
ZEUS = os.environ.get("ARTEX_SMOKE_ZEUS_URL", "http://127.0.0.1:3188/v1").rstrip("/")
MODELS = [
    "experientiallabs/deepseek-v4.1-flash-uncensored",
    "experientiallabs/deepseek-v4.1-flash",
    "orcarouter/orca/orcacyber-zero-1.5",
]
report = {"source": os.environ.get("CI_COMMIT_SHA"), "gateway": "zeus.int", "models": [], "scan_started": False}
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

def http(url, body=None, token=None):
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = "Bearer " + token
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(url, data=data, headers=headers)
    try:
        with opener.open(req, timeout=45) as response:
            return response.status, json.load(response)
    except urllib.error.HTTPError as error:
        return error.code, {}

def command(args, **kwargs):
    return subprocess.run(args, check=True, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, **kwargs)

def main():
    bindir = Path(subprocess.check_output(["pg_config", "--bindir"], text=True).strip())
    for tool in ("initdb", "pg_ctl", "createdb"):
        if not (bindir / tool).is_file():
            raise RuntimeError("Missing PostgreSQL fixture tool: " + tool)
    status, catalog = http(ZEUS + "/models")
    if status != 200:
        raise RuntimeError("Zeus model catalog unavailable")
    listed = {item.get("id") for item in catalog.get("data", [])}
    if any(model not in listed for model in MODELS):
        raise RuntimeError("A requested model is absent from Zeus; no model substitution permitted")
    runtime = ROOT / ".runtime"
    runtime.mkdir(exist_ok=True)
    # Keep the Unix socket path short; the project worktree can already be long.
    with tempfile.TemporaryDirectory(prefix="s", dir=runtime) as folder:
        work = Path(folder)
        work.chmod(0o700)
        pgdata, pgsocket = work / "pg", work / "s"
        pgsocket.mkdir(mode=0o700)
        command([str(bindir / "initdb"), "-D", str(pgdata), "-U", "artex_fixture", "--auth-local=trust", "--auth-host=reject", "--no-locale", "--encoding=UTF8"])
        pg_started = False
        app = None
        try:
            command([str(bindir / "pg_ctl"), "-D", str(pgdata), "-l", str(work / "postgres.log"), "-o", "-h '' -k " + str(pgsocket), "-w", "start"])
            pg_started = True
            command([str(bindir / "createdb"), "-h", str(pgsocket), "-U", "artex_fixture", "artex_smoke"])
            with socket.socket() as sock:
                sock.bind(("127.0.0.1", 0))
                port = sock.getsockname()[1]
            base = "http://127.0.0.1:" + str(port)
            env = {"PATH": os.environ["PATH"], "HOME": str(work), "ARTEX_PG_DSN": "postgresql:///artex_smoke?" + urllib.parse.urlencode({"host": str(pgsocket), "user": "artex_fixture", "sslmode": "disable"}), "ARTEX_JWT_KEY_DIR": str(work / "keys"), "ARTEX_SKILL_DIR": str(ROOT / "skills"), "ARTEX_UPDATE_CHECK": "0"}
            log = work / "artex.log"
            with log.open("wb") as output:
                app = subprocess.Popen([str(ROOT / "dist/artex-linux-amd64/artex"), "-addr", "127.0.0.1:" + str(port), "-proxy", "", "-data", str(work / "data")], cwd=ROOT, env=env, stdout=output, stderr=output)
            setup = None
            for _ in range(120):
                if app.poll() is not None:
                    raise RuntimeError("Artex exited before startup")
                found = re.search(r"Setup token for the /setup page:\s*([a-f0-9]{32})", log.read_text(errors="replace"))
                if found:
                    setup = found.group(1)
                    break
                time.sleep(0.5)
            if setup is None:
                raise RuntimeError("Artex did not finish initializing its disposable fixture")
            status, auth = http(base + "/api/auth/init", {"password": secrets.token_urlsafe(32), "setup_token": setup})
            if status != 200 or not auth.get("token"):
                raise RuntimeError("Disposable fixture setup failed")
            token = auth["token"]
            status, _ = http(base + "/api/health")
            report["health_status"] = status
            status, _ = http(base + "/api/llm")
            report["unauthenticated_api_status"] = status
            if status != 401:
                raise RuntimeError("Protected API unexpectedly accepted an anonymous request")
            for model in MODELS:
                for streaming in (True, False):
                    # Ollama is Artex's keyless OpenAI-compatible transport.
                    # Zeus retains its own provider credentials; none are copied.
                    status, result = http(base + "/api/llm/test", {"provider": "ollama", "model": model, "base_url": ZEUS, "streaming": streaming}, token)
                    row = {"model": model, "streaming": streaming, "http_status": status, "ok": result.get("ok") is True, "latency_ms": result.get("latency_ms"), "reply": result.get("reply", "")}
                    if not row["ok"]:
                        row["error"] = "Artex connection test failed; private runtime logs retained only for this job"
                    report["models"].append(row)
                    print(json.dumps(row), flush=True)
        finally:
            if app is not None and app.poll() is None:
                app.terminate()
                try:
                    app.wait(timeout=30)
                except subprocess.TimeoutExpired:
                    app.kill()
                    app.wait()
            if pg_started:
                command([str(bindir / "pg_ctl"), "-D", str(pgdata), "-m", "fast", "-w", "stop"])
    return all(row["ok"] for row in report["models"]) and len(report["models"]) == 6

passed = False
try:
    passed = main()
except Exception as error:
    report["failure"] = type(error).__name__ + ": " + str(error)
    print(report["failure"], flush=True)
finally:
    report["passed"] = passed
    (ROOT / "dist").mkdir(exist_ok=True)
    (ROOT / "dist/model-smoke.json").write_text(json.dumps(report, indent=2) + "\n")
raise SystemExit(0 if passed else 1)
