#!/usr/bin/env python3
"""Loopback one-click update sidecar for ic2.

Binds 127.0.0.1:25693. No token (same as canvas overlay).
Apply path: git fetch + update.sh pull (atomic npm rebuild + restart).
Never touches edit / infinite-canvas-ic / canvas / studio / chatgpt2api / comfy / Clash.
"""
from __future__ import annotations

import json
import logging
import os
import re
import subprocess
import threading
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

ROOT = Path("/home/box/ic2")
OVERLAY = ROOT / "overlay"
UPDATE_SH = ROOT / "update.sh"
START_SH = ROOT / "start.sh"
INJECT_PY = ROOT / "bin" / "inject-update-widget.py"
LOGFILE = OVERLAY / "update-api.log"
CACHE_FILE = OVERLAY / "latest.cache"
STAMP_FILE = OVERLAY / "deployed.sha"

HOST = "127.0.0.1"
PORT = 25693
WEB_PORT = 25692

apply_lock = threading.Lock()
status_lock = threading.Lock()
STATUS: dict = {
    "running": False,
    "step": "",
    "message": "",
    "done": False,
    "ok": True,
    "error": "",
    "local": "",
    "remote": "",
}


def setup_logging() -> None:
    OVERLAY.mkdir(parents=True, exist_ok=True)
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(message)s",
        handlers=[logging.FileHandler(LOGFILE, encoding="utf-8")],
    )


def set_status(**kwargs) -> None:
    with status_lock:
        STATUS.update(kwargs)


def get_status() -> dict:
    with status_lock:
        return dict(STATUS)


def run(cmd, cwd=None, timeout=60):
    logging.info("run %s (cwd=%s)", " ".join(str(x) for x in cmd), cwd)
    proc = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, timeout=timeout)
    if proc.stdout:
        logging.info(proc.stdout[-4000:])
    if proc.returncode != 0:
        err = (proc.stderr or proc.stdout or "").strip()
        raise RuntimeError(f"{' '.join(str(x) for x in cmd)} failed: {err[-2000:]}")
    return proc.stdout


def git_out(args, timeout=30) -> str:
    return run(["git", *args], cwd=str(ROOT), timeout=timeout).strip()


def short_sha(sha: str) -> str:
    sha = (sha or "").strip()
    return sha[:7] if sha else ""


def sha_equal(a: str, b: str) -> bool:
    a = (a or "").strip().lower()
    b = (b or "").strip().lower()
    if not a or not b:
        return False
    n = min(len(a), len(b), 40)
    return n >= 7 and a[:n] == b[:n]


def default_remote_ref() -> str:
    try:
        ref = git_out(["rev-parse", "--abbrev-ref", "origin/HEAD"], timeout=15)
        if ref.startswith("origin/") and ref != "origin/HEAD":
            return ref
    except Exception:
        pass
    return "origin/main"


def ensure_origin_main() -> str:
    """Shallow tag clones lack origin/main; fetch the tip of main into remotes."""
    try:
        return git_out(["rev-parse", "origin/main"], timeout=15)
    except Exception:
        pass
    run(
        ["git", "fetch", "--prune", "origin", "+refs/heads/main:refs/remotes/origin/main"],
        cwd=str(ROOT),
        timeout=120,
    )
    return git_out(["rev-parse", "origin/main"], timeout=15)


def remote_tip_sha() -> tuple[str, str]:
    """Return (sha, ref) for upstream main tip."""
    try:
        sha = ensure_origin_main()
        return sha, "origin/main"
    except Exception:
        pass
    # Fallback: ls-remote without updating local refs
    out = run(["git", "ls-remote", "origin", "refs/heads/main"], cwd=str(ROOT), timeout=60)
    line = (out or "").strip().splitlines()[0] if out.strip() else ""
    sha = line.split()[0] if line else ""
    return sha, "origin/main"


def read_local_sha() -> str:
    if STAMP_FILE.is_file():
        stamp = STAMP_FILE.read_text(encoding="utf-8").strip()
        if stamp:
            return stamp
    try:
        return git_out(["rev-parse", "HEAD"], timeout=15)
    except Exception:
        return ""


def cache_get() -> tuple[str, str]:
    if not CACHE_FILE.is_file():
        return "", ""
    try:
        data = json.loads(CACHE_FILE.read_text(encoding="utf-8"))
        return str(data.get("remote") or ""), str(data.get("ref") or "")
    except Exception:
        return "", ""


def cache_set(remote: str, ref: str) -> None:
    CACHE_FILE.write_text(
        json.dumps({"remote": remote, "ref": ref, "ts": int(time.time())}),
        encoding="utf-8",
    )


def check_payload() -> dict:
    local = read_local_sha()
    ref = default_remote_ref()
    fetch_error = ""
    remote = ""
    try:
        remote, ref = remote_tip_sha()
        if remote:
            cache_set(remote, ref)
    except Exception as exc:
        fetch_error = str(exc)
        remote, cached_ref = cache_get()
        if cached_ref:
            ref = cached_ref
    latest = sha_equal(local, remote) if remote else True
    if not remote:
        latest = True
    return {
        "ok": True,
        "local": local,
        "remote": remote,
        "latest": latest,
        "short": short_sha(local),
        "local_short": short_sha(local),
        "remote_short": short_sha(remote),
        "branch": ref.replace("origin/", "", 1) if ref.startswith("origin/") else ref,
        "ref": ref,
        "message": fetch_error,
    }


def port_up(port: int) -> bool:
    try:
        out = subprocess.check_output(["ss", "-tln"], text=True)
    except (subprocess.CalledProcessError, OSError):
        return False
    return re.search(rf"127\.0\.0\.1:{port}\b", out) is not None


def do_apply() -> tuple[bool, str, dict]:
    info = check_payload()
    local, remote = info["local"], info["remote"]
    if not remote:
        return True, f"无法确认远端 SHA，保持当前 {info['local_short']}", info
    if sha_equal(local, remote):
        return True, f"已是最新版本（{info['local_short']}）", info
    ref = info.get("ref") or default_remote_ref()
    branch = info.get("branch") or "main"
    set_status(running=True, done=False, ok=True, error="", step="update",
               message="正在拉取并重建前端…", local=local, remote=remote)
    # Ensure origin/main exists, then rebuild from that tip (SPA only).
    ensure_origin_main()
    run(["git", "checkout", "-B", "main", "origin/main"], cwd=str(ROOT), timeout=60)
    run(["bash", str(UPDATE_SH), "rebuild"], cwd=str(ROOT), timeout=900)
    set_status(step="inject", message="正在注入更新控件…")
    if INJECT_PY.is_file():
        run([os.environ.get("PYTHON", "python3"), str(INJECT_PY)], cwd=str(ROOT), timeout=30)
    # refresh runtime config without full bounce if possible
    if START_SH.is_file():
        run(["bash", str(START_SH)], cwd=str(ROOT), timeout=60)
    for _ in range(40):
        if port_up(WEB_PORT):
            break
        time.sleep(0.15)
    local = git_out(["rev-parse", "HEAD"], timeout=15)
    STAMP_FILE.write_text(local + "\n", encoding="utf-8")
    info = {
        "ok": True,
        "local": local,
        "remote": remote,
        "latest": sha_equal(local, remote),
        "short": short_sha(local),
        "local_short": short_sha(local),
        "remote_short": short_sha(remote),
        "branch": branch,
        "ref": ref,
        "message": "",
    }
    return True, f"已更新到 {info['local_short']}", info


def apply_worker() -> None:
    try:
        ok, message, info = do_apply()
        set_status(
            running=False,
            done=True,
            ok=ok,
            error="" if ok else message,
            step="done",
            message=message,
            local=info.get("local", ""),
            remote=info.get("remote", ""),
        )
    except Exception as exc:
        logging.exception("apply failed")
        set_status(running=False, done=True, ok=False, error=str(exc),
                   step="error", message=str(exc))
    finally:
        if apply_lock.locked():
            apply_lock.release()


def normalize_path(path: str) -> str:
    path = path.split("?", 1)[0]
    for prefix in ("/update-api", "/update"):
        if path == prefix or path.startswith(prefix + "/"):
            path = path[len(prefix):] or "/"
            break
    if not path.startswith("/"):
        path = "/" + path
    if path != "/" and path.endswith("/"):
        path = path.rstrip("/")
    return path


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt: str, *args) -> None:
        logging.info("%s " + fmt, self.address_string(), *args)

    def _json(self, code: int, obj: dict) -> None:
        body = json.dumps(obj, ensure_ascii=False).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.send_header("Cache-Control", "no-store")
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header("Access-Control-Allow-Headers", "Content-Type")
        self.send_header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
        self.end_headers()
        self.wfile.write(body)

    def do_OPTIONS(self) -> None:
        self.send_response(204)
        self.send_header("Access-Control-Allow-Origin", "*")
        self.send_header("Access-Control-Allow-Headers", "Content-Type")
        self.send_header("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
        self.send_header("Content-Length", "0")
        self.end_headers()

    def do_GET(self) -> None:
        route = normalize_path(self.path)
        if route in ("/", "/health"):
            self._json(200, {"ok": True})
            return
        if route == "/status":
            self._json(200, get_status())
            return
        if route == "/check":
            try:
                self._json(200, check_payload())
            except Exception as exc:
                logging.exception("check failed")
                try:
                    local = read_local_sha()
                except Exception:
                    local = ""
                self._json(200, {
                    "ok": False,
                    "local": local,
                    "remote": "",
                    "latest": True,
                    "short": short_sha(local),
                    "local_short": short_sha(local),
                    "remote_short": "",
                    "message": str(exc),
                })
            return
        self._json(404, {"ok": False, "message": "not found"})

    def do_POST(self) -> None:
        route = normalize_path(self.path)
        if route != "/apply":
            self._json(404, {"ok": False, "message": "not found"})
            return
        length = int(self.headers.get("Content-Length") or 0)
        if length:
            self.rfile.read(length)
        if not apply_lock.acquire(blocking=False):
            self._json(200, {"ok": True, "started": False, "message": "更新已在进行中"})
            return
        try:
            info = check_payload()
            if info.get("latest"):
                apply_lock.release()
                self._json(200, {
                    "ok": True,
                    "started": False,
                    "latest": True,
                    "message": f"已是最新版本（{info.get('local_short') or ''}）",
                    **{k: info.get(k) for k in ("local", "remote", "short", "local_short", "remote_short")},
                })
                return
            set_status(running=True, done=False, ok=True, error="", step="queued",
                       message="已开始更新", local=info.get("local", ""), remote=info.get("remote", ""))
            threading.Thread(target=apply_worker, daemon=True).start()
            self._json(200, {"ok": True, "started": True, "message": "已开始更新",
                             "local_short": info.get("local_short"), "remote_short": info.get("remote_short")})
        except Exception as exc:
            if apply_lock.locked():
                apply_lock.release()
            self._json(200, {"ok": False, "started": False, "message": str(exc)})


def main() -> None:
    setup_logging()
    # seed stamp from current HEAD
    try:
        sha = git_out(["rev-parse", "HEAD"], timeout=15)
        STAMP_FILE.write_text(sha + "\n", encoding="utf-8")
    except Exception:
        pass
    server = ThreadingHTTPServer((HOST, PORT), Handler)
    logging.info("ic2 update-api listening on %s:%s", HOST, PORT)
    server.serve_forever()


if __name__ == "__main__":
    main()
