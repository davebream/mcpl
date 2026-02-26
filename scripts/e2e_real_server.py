#!/usr/bin/env python3
"""
E2E comparison: Direct MCP server vs mcpl-shared MCP server.

Spawns 1..N concurrent sessions in both modes and compares:
  - Memory (RSS)
  - Startup time
  - Session survival / reconnect speed
  - Correctness (tools work in every session)

Usage:
    python3 scripts/e2e_real_server.py
"""

import json
import os
import select
import shutil
import subprocess
import sys
import tempfile
import time

# ── Config ──────────────────────────────────────────────────────────────

MCPL_BINARY = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "mcpl")
SERVER_CMD = ["npx", "-y", "@modelcontextprotocol/server-filesystem", "/tmp"]
TIMEOUT = 60
SESSION_COUNTS = [2, 3, 5, 8, 13]


# ── Line reader with timeout ───────────────────────────────────────────

class LineReader:
    def __init__(self, fd):
        self.fd = fd
        self.buf = b""

    def readline(self, timeout=TIMEOUT):
        deadline = time.time() + timeout
        while b"\n" not in self.buf:
            remaining = deadline - time.time()
            if remaining <= 0:
                raise TimeoutError(f"No response within {timeout}s")
            ready, _, _ = select.select([self.fd], [], [], min(remaining, 1.0))
            if ready:
                chunk = os.read(self.fd.fileno(), 65536)
                if not chunk:
                    if self.buf:
                        line = self.buf.decode()
                        self.buf = b""
                        return line
                    raise EOFError("stdout closed")
                self.buf += chunk
        idx = self.buf.index(b"\n")
        line = self.buf[:idx].decode()
        self.buf = self.buf[idx + 1 :]
        return line


# ── MCP protocol ────────────────────────────────────────────────────────

def send(proc, msg):
    data = (json.dumps(msg) + "\n").encode()
    proc.stdin.write(data)
    proc.stdin.flush()


def mcp_initialize(proc, reader):
    t0 = time.time()
    send(proc, {
        "jsonrpc": "2.0", "id": 1, "method": "initialize",
        "params": {
            "protocolVersion": "2024-11-05",
            "clientInfo": {"name": "e2e-test", "version": "1.0"},
            "capabilities": {},
        },
    })
    resp = json.loads(reader.readline())
    elapsed = time.time() - t0
    send(proc, {"jsonrpc": "2.0", "method": "initialized"})
    return resp, elapsed


def mcp_tools_call(proc, reader, tool, args, req_id=3):
    send(proc, {
        "jsonrpc": "2.0", "id": req_id, "method": "tools/call",
        "params": {"name": tool, "arguments": args},
    })
    return json.loads(reader.readline())


# ── Memory measurement ──────────────────────────────────────────────────

def tree_rss_kb(root_pid):
    """Total RSS in KB for a process and all descendants."""
    try:
        out = subprocess.check_output(
            ["ps", "-eo", "pid,ppid,rss"], stderr=subprocess.DEVNULL
        ).decode()
    except subprocess.CalledProcessError:
        return 0
    procs = {}
    for line in out.strip().split("\n")[1:]:
        parts = line.split()
        if len(parts) >= 3:
            try:
                procs[int(parts[0])] = (int(parts[1]), int(parts[2]))
            except ValueError:
                pass
    pids = {root_pid}
    changed = True
    while changed:
        changed = False
        for p, (pp, _) in procs.items():
            if pp in pids and p not in pids:
                pids.add(p)
                changed = True
    return sum(procs.get(p, (0, 0))[1] for p in pids if p in procs)


def single_rss_kb(pid):
    """RSS in KB for a single process."""
    try:
        out = subprocess.check_output(
            ["ps", "-o", "rss=", "-p", str(pid)], stderr=subprocess.DEVNULL
        ).decode().strip()
        return int(out)
    except (subprocess.CalledProcessError, ValueError):
        return 0


def vmmap_dirty_kb(pid):
    """Private dirty memory in KB via vmmap (macOS). More accurate than RSS."""
    try:
        out = subprocess.check_output(
            ["vmmap", "-summary", str(pid)], stderr=subprocess.DEVNULL, timeout=10,
        ).decode()
        # Find TOTAL line: "TOTAL  <virtual>  <resident>  <dirty>  ..."
        for line in out.split("\n"):
            if line.startswith("TOTAL") and "minus" not in line:
                parts = line.split()
                # Dirty is the 4th column (index 3) — parse "173.7M" or "1234K"
                if len(parts) >= 4:
                    return _parse_vmmap_size(parts[3])
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired):
        pass
    return 0


def tree_dirty_kb(root_pid):
    """Total dirty memory in KB for a process and all descendants."""
    try:
        out = subprocess.check_output(
            ["ps", "-eo", "pid,ppid"], stderr=subprocess.DEVNULL
        ).decode()
    except subprocess.CalledProcessError:
        return 0
    procs = {}
    for line in out.strip().split("\n")[1:]:
        parts = line.split()
        if len(parts) >= 2:
            try:
                procs[int(parts[0])] = int(parts[1])
            except ValueError:
                pass
    pids = {root_pid}
    changed = True
    while changed:
        changed = False
        for p, pp in procs.items():
            if pp in pids and p not in pids:
                pids.add(p)
                changed = True
    return sum(vmmap_dirty_kb(p) for p in pids if p in procs or p == root_pid)


def _parse_vmmap_size(s):
    """Parse vmmap size like '173.7M', '1234K', '512B' to KB."""
    s = s.strip()
    if s.endswith("G"):
        return int(float(s[:-1]) * 1024 * 1024)
    if s.endswith("M"):
        return int(float(s[:-1]) * 1024)
    if s.endswith("K"):
        return int(float(s[:-1]))
    if s.endswith("B"):
        return max(1, int(float(s[:-1]) / 1024))
    try:
        return int(s)
    except ValueError:
        return 0


# ── Process helpers ─────────────────────────────────────────────────────

def spawn_direct():
    proc = subprocess.Popen(
        SERVER_CMD,
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
        bufsize=0,
    )
    return proc, LineReader(proc.stdout)


def spawn_mcpl_shim(env):
    proc = subprocess.Popen(
        [MCPL_BINARY, "connect", "filesystem"],
        stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        bufsize=0, env=env,
    )
    return proc, LineReader(proc.stdout)


def kill_proc(proc):
    if proc and proc.poll() is None:
        proc.terminate()
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait(timeout=3)


def fmt_mb(kb):
    return f"{kb / 1024:.0f}"


# ── Test: Direct mode (N sessions) ─────────────────────────────────────

def test_direct_n(n, measure_dirty=False):
    """Spawn N direct server instances. Returns dict with rss_kb, dirty_kb, times, ok."""
    procs = []
    readers = []
    times = []
    all_ok = True
    try:
        for i in range(n):
            proc, reader = spawn_direct()
            procs.append(proc)
            readers.append(reader)
            _, elapsed = mcp_initialize(proc, reader)
            times.append(elapsed)
            resp = mcp_tools_call(proc, reader, "list_directory", {"path": "/tmp"}, req_id=i + 10)
            if "error" in resp or "result" not in resp:
                all_ok = False

        time.sleep(0.5)
        total_rss = sum(tree_rss_kb(p.pid) for p in procs)
        total_dirty = sum(tree_dirty_kb(p.pid) for p in procs) if measure_dirty else 0
        return {"rss_kb": total_rss, "dirty_kb": total_dirty, "times": times, "ok": all_ok}
    finally:
        for p in procs:
            kill_proc(p)


# ── Test: mcpl mode (N sessions) ───────────────────────────────────────

def test_mcpl_n(n, env, config_dir, measure_dirty=False):
    """Spawn N mcpl shim sessions. Returns dict with rss_kb, dirty_kb, times, ok."""
    shims = []
    readers = []
    times = []
    all_ok = True
    daemon_pid = None
    try:
        for i in range(n):
            proc, reader = spawn_mcpl_shim(env)
            shims.append(proc)
            readers.append(reader)
            _, elapsed = mcp_initialize(proc, reader)
            times.append(elapsed)
            resp = mcp_tools_call(proc, reader, "list_directory", {"path": "/tmp"}, req_id=i + 10)
            if "error" in resp or "result" not in resp:
                all_ok = False

        time.sleep(0.5)

        pid_file = os.path.join(config_dir, "mcpl.pid")
        if os.path.exists(pid_file):
            daemon_pid = int(open(pid_file).read().strip())

        # RSS: daemon tree + each shim individually (avoid double-count)
        daemon_tree_rss = tree_rss_kb(daemon_pid) if daemon_pid else 0
        shim_rss = sum(single_rss_kb(s.pid) for s in shims)
        total_rss = daemon_tree_rss + shim_rss

        # Dirty: same approach
        total_dirty = 0
        if measure_dirty:
            daemon_tree_dirty = tree_dirty_kb(daemon_pid) if daemon_pid else 0
            shim_dirty = sum(vmmap_dirty_kb(s.pid) for s in shims)
            total_dirty = daemon_tree_dirty + shim_dirty

        return {"rss_kb": total_rss, "dirty_kb": total_dirty, "times": times, "ok": all_ok}
    finally:
        for s in shims:
            kill_proc(s)


# ── Survival + reconnect test ───────────────────────────────────────────

def test_mcpl_survival(env, config_dir):
    """Test that server survives session disconnect and reconnect is instant."""
    shim1 = shim2 = shim3 = None
    try:
        # Session 1: cold start
        shim1, reader1 = spawn_mcpl_shim(env)
        mcp_initialize(shim1, reader1)
        resp1 = mcp_tools_call(shim1, reader1, "list_directory", {"path": "/tmp"})
        ok1 = "result" in resp1 and "error" not in resp1

        # Session 2: warm
        shim2, reader2 = spawn_mcpl_shim(env)
        mcp_initialize(shim2, reader2)

        # Kill session 1
        kill_proc(shim1)
        shim1 = None
        time.sleep(0.3)

        # Session 2 still works?
        resp2 = mcp_tools_call(shim2, reader2, "list_directory", {"path": "/tmp"}, req_id=20)
        survival_ok = "result" in resp2 and "error" not in resp2

        # Kill session 2
        kill_proc(shim2)
        shim2 = None
        time.sleep(0.3)

        # Session 3: reconnect (server should still be running)
        shim3, reader3 = spawn_mcpl_shim(env)
        _, reconnect_time = mcp_initialize(shim3, reader3)
        resp3 = mcp_tools_call(shim3, reader3, "list_directory", {"path": "/tmp"}, req_id=30)
        reconnect_ok = "result" in resp3 and "error" not in resp3

        return survival_ok, reconnect_ok, reconnect_time
    finally:
        kill_proc(shim1)
        kill_proc(shim2)
        kill_proc(shim3)


# ── Setup mcpl environment ──────────────────────────────────────────────

def setup_mcpl_env():
    tmpdir = tempfile.mkdtemp(prefix="mcpl-e2e-")
    config_dir = os.path.join(tmpdir, "config")
    sock_dir = os.path.join(tmpdir, "tmp")
    os.makedirs(config_dir, mode=0o700, exist_ok=True)
    os.makedirs(sock_dir, mode=0o700, exist_ok=True)

    cfg = {
        "server_idle_timeout": "5m",
        "idle_timeout": "5m",
        "log_level": "info",
        "servers": {
            "filesystem": {
                "command": SERVER_CMD[0],
                "args": SERVER_CMD[1:],
            }
        },
    }
    cfg_path = os.path.join(config_dir, "config.json")
    fd = os.open(cfg_path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as f:
        json.dump(cfg, f, indent=2)

    env = os.environ.copy()
    env["MCPL_CONFIG_DIR"] = config_dir
    env["TMPDIR"] = sock_dir
    return tmpdir, config_dir, env


def stop_mcpl(env):
    try:
        subprocess.run(
            [MCPL_BINARY, "stop"], env=env,
            capture_output=True, text=True, timeout=10,
        )
    except Exception:
        pass


# ── Warmup ──────────────────────────────────────────────────────────────

def warmup():
    print("  Warming up npx cache...", end="", flush=True)
    try:
        proc = subprocess.Popen(
            SERVER_CMD,
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
            bufsize=0,
        )
        reader = LineReader(proc.stdout)
        send(proc, {
            "jsonrpc": "2.0", "id": 1, "method": "initialize",
            "params": {
                "protocolVersion": "2024-11-05",
                "clientInfo": {"name": "warmup"},
                "capabilities": {},
            },
        })
        reader.readline(timeout=TIMEOUT)
        kill_proc(proc)
        print(" done.")
    except Exception as e:
        print(f" warning: {e}")


# ── Main ────────────────────────────────────────────────────────────────

def main():
    if not os.path.isfile(MCPL_BINARY):
        print(f"ERROR: mcpl binary not found at {MCPL_BINARY}")
        sys.exit(1)
    if subprocess.run(["which", "npx"], capture_output=True).returncode != 0:
        print("ERROR: npx not found in PATH")
        sys.exit(1)

    print()
    print("  mcpl E2E Test — Direct vs Shared MCP Server")
    print("  Server: @modelcontextprotocol/server-filesystem")
    print()

    warmup()
    print()

    # ── Collect data ────────────────────────────────────────────────────

    direct_data = {}  # n -> {rss_kb, times, ok}
    mcpl_data = {}    # n -> {rss_kb, times, ok}

    # Direct mode: test each N independently (clean slate each time)
    print("  PHASE 1: Direct mode (each session = separate server process)")
    print("  " + "-" * 56)
    for n in SESSION_COUNTS:
        label = f"  {n} session{'s' if n > 1 else ' '}"
        print(f"{label:16s}...", end="", flush=True)
        try:
            result = test_direct_n(n)
            direct_data[n] = result
            avg_time = sum(result["times"]) / len(result["times"])
            print(f" {fmt_mb(result['rss_kb']):>5s} MB   avg init: {avg_time:.2f}s   {'OK' if result['ok'] else 'FAIL'}")
        except Exception as e:
            direct_data[n] = {"rss_kb": 0, "dirty_kb": 0, "times": [], "ok": False}
            print(f" ERROR: {e}")
        time.sleep(1)

    print()

    # mcpl mode: start daemon once, incrementally add sessions
    print("  PHASE 2: mcpl mode (shared daemon, one server, N shims)")
    print("  " + "-" * 56)

    tmpdir, config_dir, env = setup_mcpl_env()
    try:
        for n in SESSION_COUNTS:
            label = f"  {n} session{'s' if n > 1 else ' '}"
            print(f"{label:16s}...", end="", flush=True)
            try:
                result = test_mcpl_n(n, env, config_dir)
                mcpl_data[n] = result
                avg_time = sum(result["times"]) / len(result["times"])
                print(f" {fmt_mb(result['rss_kb']):>5s} MB   avg init: {avg_time:.2f}s   {'OK' if result['ok'] else 'FAIL'}")
            except Exception as e:
                mcpl_data[n] = {"rss_kb": 0, "dirty_kb": 0, "times": [], "ok": False}
                print(f" ERROR: {e}")

        # Survival test
        print()
        print("  PHASE 3: Session survival & reconnect")
        print("  " + "-" * 56)
        try:
            survival_ok, reconnect_ok, reconnect_time = test_mcpl_survival(env, config_dir)
            print(f"  Disconnect shim, other shim still works:  {'PASS' if survival_ok else 'FAIL'}")
            print(f"  Reconnect after all shims closed:         {'PASS' if reconnect_ok else 'FAIL'}")
            print(f"  Reconnect time (server alive):            {reconnect_time:.3f}s")
        except Exception as e:
            print(f"  ERROR: {e}")
            survival_ok = reconnect_ok = False
            reconnect_time = 0

        # Calibration: measure actual private (dirty) memory via vmmap
        print()
        print("  PHASE 4: Memory calibration via vmmap (private dirty memory)")
        print("  " + "-" * 56)
        # Pick a mid-range count for calibration
        cal_n = min(3, SESSION_COUNTS[-1])
        print(f"  Measuring {cal_n} direct sessions with vmmap...", end="", flush=True)
        try:
            cal_direct = test_direct_n(cal_n, measure_dirty=True)
            direct_data[f"cal_{cal_n}"] = cal_direct
            print(f" RSS={fmt_mb(cal_direct['rss_kb'])} MB, Dirty={fmt_mb(cal_direct['dirty_kb'])} MB")
        except Exception as e:
            cal_direct = {"rss_kb": 0, "dirty_kb": 0}
            print(f" ERROR: {e}")

        print(f"  Measuring {cal_n} mcpl sessions with vmmap...", end="", flush=True)
        try:
            cal_mcpl = test_mcpl_n(cal_n, env, config_dir, measure_dirty=True)
            mcpl_data[f"cal_{cal_n}"] = cal_mcpl
            print(f" RSS={fmt_mb(cal_mcpl['rss_kb'])} MB, Dirty={fmt_mb(cal_mcpl['dirty_kb'])} MB")
        except Exception as e:
            cal_mcpl = {"rss_kb": 0, "dirty_kb": 0}
            print(f" ERROR: {e}")

    finally:
        stop_mcpl(env)
        time.sleep(0.5)
        shutil.rmtree(tmpdir, ignore_errors=True)

    # ── Results table ───────────────────────────────────────────────────

    print()
    print("=" * 70)
    print("  RESULTS")
    print("=" * 70)

    col_w = 9
    header = f"  {'Sessions':<10}"
    for n in SESSION_COUNTS:
        header += f"  {n:>{col_w - 2}}"
    sep = "  " + "-" * (10 + col_w * len(SESSION_COUNTS))

    # Memory table (RSS)
    print()
    print("  Memory — RSS (what ps reports)")
    print(header)
    print(sep)

    row_d = f"  {'Direct':<10}"
    row_m = f"  {'mcpl':<10}"
    row_r = f"  {'Ratio':<10}"
    for n in SESSION_COUNTS:
        d_mb = direct_data.get(n, {}).get("rss_kb", 0) / 1024
        m_mb = mcpl_data.get(n, {}).get("rss_kb", 0) / 1024
        row_d += f"  {d_mb:>{col_w - 4}.0f} MB"
        row_m += f"  {m_mb:>{col_w - 4}.0f} MB"
        if m_mb > 0:
            ratio = d_mb / m_mb
            row_r += f"  {ratio:>{col_w - 3}.1f}x "
        else:
            row_r += f"  {'?':>{col_w - 2}}"
    print(row_d)
    print(row_m)
    print(row_r)

    # Calibration: dirty memory
    cal_n = min(3, SESSION_COUNTS[-1])
    cal_d = direct_data.get(f"cal_{cal_n}", {})
    cal_m = mcpl_data.get(f"cal_{cal_n}", {})
    d_rss_cal = cal_d.get("rss_kb", 0)
    d_dirty_cal = cal_d.get("dirty_kb", 0)
    m_rss_cal = cal_m.get("rss_kb", 0)
    m_dirty_cal = cal_m.get("dirty_kb", 0)

    # Calculate correction factors
    d_factor = d_dirty_cal / d_rss_cal if d_rss_cal > 0 and d_dirty_cal > 0 else 1.0
    m_factor = m_dirty_cal / m_rss_cal if m_rss_cal > 0 and m_dirty_cal > 0 else 1.0

    if d_dirty_cal > 0:
        print()
        print(f"  Memory — Private dirty (vmmap, calibrated at N={cal_n})")
        print(f"  RSS→Dirty ratio: direct={d_factor:.0%}, mcpl={m_factor:.0%}")
        print(header)
        print(sep)

        row_d2 = f"  {'Direct':<10}"
        row_m2 = f"  {'mcpl':<10}"
        row_r2 = f"  {'Ratio':<10}"
        for n in SESSION_COUNTS:
            d_mb = direct_data.get(n, {}).get("rss_kb", 0) / 1024 * d_factor
            m_mb = mcpl_data.get(n, {}).get("rss_kb", 0) / 1024 * m_factor
            row_d2 += f"  {d_mb:>{col_w - 4}.0f} MB"
            row_m2 += f"  {m_mb:>{col_w - 4}.0f} MB"
            if m_mb > 0:
                ratio = d_mb / m_mb
                row_r2 += f"  {ratio:>{col_w - 3}.1f}x "
            else:
                row_r2 += f"  {'?':>{col_w - 2}}"
        print(row_d2)
        print(row_m2)
        print(row_r2)

    # Startup time table
    print()
    print("  Startup time (Nth session)")
    print(header)
    print(sep)

    row_dt = f"  {'Direct':<10}"
    row_mt = f"  {'mcpl':<10}"
    for n in SESSION_COUNTS:
        d_times = direct_data.get(n, {}).get("times", [])
        m_times = mcpl_data.get(n, {}).get("times", [])
        d_last = d_times[-1] if d_times else 0
        m_last = m_times[-1] if m_times else 0
        row_dt += f"  {d_last:>{col_w - 3}.2f}s "
        row_mt += f"  {m_last:>{col_w - 3}.2f}s "
    print(row_dt + "  (always cold)")
    print(row_mt + "  (1st cold, rest cached)")

    # Qualitative results
    print()
    print("  Qualitative")
    print("  " + "-" * 50)
    print(f"  Session survival (disconnect/reconnect):  {'PASS' if survival_ok else 'FAIL'}")
    print(f"  Reconnect time (server stays alive):      {reconnect_time:.3f}s")
    d_cold = direct_data.get(SESSION_COUNTS[0], {}).get("times", [0])[0]
    if d_cold > 0 and reconnect_time > 0:
        print(f"  Reconnect vs cold start:                 {d_cold / reconnect_time:.0f}x faster")

    # Methodology note
    print()
    print("  Methodology")
    print("  " + "-" * 50)
    print("  RSS = Resident Set Size (ps -o rss). Overcounts shared")
    print("  library pages across processes — inflates direct mode more")
    print("  than mcpl mode (N large Node processes share more than N")
    print("  tiny Go shims).")
    if d_dirty_cal > 0:
        print(f"  Private dirty (vmmap) corrects for this: {d_factor:.0%} of RSS")
        print("  is truly private for direct; rest is shared libs counted N times.")
    print("  Server: @modelcontextprotocol/server-filesystem via npx")

    print()


if __name__ == "__main__":
    main()
