#!/usr/bin/env python3
"""Run one command pinned to a core set and record what it cost.

Writes a JSON object with wall seconds, user and system CPU seconds, peak RSS
(the kernel's ru_maxrss for the process), and the host's load average and
CPU busy share sampled every second while it ran, so a run on a busy shared
machine can be told apart from a run on a quiet one.

  scripts/bench/measure.py -out run.json -cores 8-11,24-27 -label goempo -- cmd args...
"""
import argparse
import json
import os
import socket
import subprocess
import threading
import time


def parse_cores(spec):
    cores = set()
    for part in spec.split(","):
        if "-" in part:
            lo, hi = part.split("-")
            cores.update(range(int(lo), int(hi) + 1))
        elif part:
            cores.add(int(part))
    return sorted(cores)


def cpu_times():
    with open("/proc/stat") as f:
        fields = f.readline().split()[1:]
    vals = [int(x) for x in fields]
    return sum(vals), vals[3] + vals[4]


class Sampler(threading.Thread):
    def __init__(self, interval):
        super().__init__(daemon=True)
        self.interval = interval
        self.loads = []
        self.busy = []
        self.stop = threading.Event()

    def run(self):
        total0, idle0 = cpu_times()
        while not self.stop.wait(self.interval):
            with open("/proc/loadavg") as f:
                self.loads.append(float(f.read().split()[0]))
            total1, idle1 = cpu_times()
            if total1 > total0:
                self.busy.append(1.0 - (idle1 - idle0) / (total1 - total0))
            total0, idle0 = total1, idle1


def summary(xs):
    if not xs:
        return None
    return {"min": round(min(xs), 3), "mean": round(sum(xs) / len(xs), 3), "max": round(max(xs), 3)}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("-out", required=True)
    ap.add_argument("-cores", required=True, help="cpu list for taskset, e.g. 8-11,24-27")
    ap.add_argument("-label", default="")
    ap.add_argument("-log", default="", help="file for the command's stdout and stderr")
    ap.add_argument("cmd", nargs=argparse.REMAINDER)
    a = ap.parse_args()
    cmd = a.cmd[1:] if a.cmd and a.cmd[0] == "--" else a.cmd
    if not cmd:
        ap.error("no command")
    cores = parse_cores(a.cores)
    log = open(a.log, "ab") if a.log else subprocess.DEVNULL
    sampler = Sampler(1.0)
    sampler.start()
    t0 = time.monotonic()
    proc = subprocess.Popen(cmd, stdout=log, stderr=subprocess.STDOUT, preexec_fn=lambda: os.sched_setaffinity(0, cores))
    _, status, ru = os.wait4(proc.pid, 0)
    wall = time.monotonic() - t0
    sampler.stop.set()
    sampler.join()
    res = {
        "label": a.label,
        "cmd": cmd,
        "cores": a.cores,
        "ncores": len(cores),
        "exit": os.waitstatus_to_exitcode(status),
        "wall_s": round(wall, 3),
        "user_s": round(ru.ru_utime, 3),
        "sys_s": round(ru.ru_stime, 3),
        "cpu_s": round(ru.ru_utime + ru.ru_stime, 3),
        "max_rss_mb": round(ru.ru_maxrss / 1024, 1),
        "host": socket.gethostname(),
        "host_load": summary(sampler.loads),
        "host_busy": summary(sampler.busy),
        "started": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime(time.time() - wall)),
    }
    with open(a.out, "w") as f:
        json.dump(res, f, indent=1)
        f.write("\n")
    print(json.dumps(res))
    raise SystemExit(res["exit"])


if __name__ == "__main__":
    main()
