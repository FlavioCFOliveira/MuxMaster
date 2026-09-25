#!/usr/bin/env python3
"""Nearest-owner attribution of a pprof profile (waste hunt, rmp #249).

Reads the text produced by `go tool pprof -traces` and attributes every
sampled stack to the FIRST frame, walking from the leaf towards the root,
that belongs to an "owner":

  MuxMaster/middleware  - github.com/FlavioCFOliveira/MuxMaster/middleware.*
  MuxMaster/root        - github.com/FlavioCFOliveira/MuxMaster.*
  example               - main.*            (the example's own application code)
  net/http              - net/http.*        (excluding net/http/pprof)

Library code (time, strconv, encoding/json, crypto, runtime.mallocgc, ...) has
no owner of its own: its cost is charged to whoever called it. The same holds
for net/http HELPER APIs called by handlers or middleware (Header.Set,
(*Request).WithContext, Error, Redirect, ... — see NET_HTTP_HELPERS); only
net/http server machinery (serving the connection, parsing the request,
writing the response) is owned by net/http. A stack with no
owner at all (GC workers, scheduler, netpoll) is reported as "runtime (no owner)".

Usage: owner.py <traces.txt> [top_n]
"""
import re
import sys
from collections import defaultdict

UNIT = {"ns": 1e-9, "us": 1e-6, "µs": 1e-6, "ms": 1e-3, "s": 1.0, "hrs": 3600.0,
        "B": 1.0, "kB": 1e3, "MB": 1e6, "GB": 1e9, "TB": 1e12, "": 1.0, "k": 1e3, "M": 1e6, "G": 1e9}
VAL = re.compile(r"^\s*([0-9.]+)([a-zA-Zµ]*)\s+(\S.*)$")


# net/http functions that are helper APIs invoked BY handlers/middleware
# (their cost belongs to the caller), as opposed to server machinery
# (connection serving, request parsing, response writing), which stays
# owned by net/http.
NET_HTTP_HELPERS = (
    "net/http.Header.",
    "net/http.(*Request).WithContext",
    "net/http.(*Request).Clone",
    "net/http.(*Request).Context",
    "net/http.(*Request).BasicAuth",
    "net/http.Error",
    "net/http.Redirect",
    "net/http.NotFound",
    "net/http.HandlerFunc.ServeHTTP",
    "net/http.StatusText",
    "net/http.CanonicalHeaderKey",
)


def owner_of(fn):
    if fn.startswith(NET_HTTP_HELPERS):
        return None
    if "FlavioCFOliveira/MuxMaster/middleware." in fn:
        return "MuxMaster/middleware"
    if "FlavioCFOliveira/MuxMaster." in fn:
        return "MuxMaster/root"
    if fn.startswith("main."):
        return "example"
    if fn.startswith("net/http.") and not fn.startswith("net/http/pprof"):
        return "net/http"
    return None


def short(fn):
    fn = fn.split(" ")[0]
    return fn.replace("github.com/FlavioCFOliveira/MuxMaster/", "").replace("github.com/FlavioCFOliveira/MuxMaster.", "muxmaster.")


def parse(path):
    samples = []
    cur_val, cur_stack = None, []
    with open(path, encoding="utf-8") as f:
        for line in f:
            line = line.rstrip("\n")
            if line.startswith("-----------+"):
                if cur_val is not None:
                    samples.append((cur_val, cur_stack))
                cur_val, cur_stack = None, []
                continue
            m = VAL.match(line)
            if m and cur_val is None and not line.startswith(" " * 12):
                cur_val = float(m.group(1)) * UNIT.get(m.group(2), 1.0)
                cur_stack.append(m.group(3).strip())
            elif cur_val is not None and line.strip():
                cur_stack.append(line.strip())
    if cur_val is not None:
        samples.append((cur_val, cur_stack))
    return samples


def main():
    path = sys.argv[1]
    top_n = int(sys.argv[2]) if len(sys.argv) > 2 else 25
    samples = parse(path)
    total = sum(v for v, _ in samples) or 1.0
    by_owner = defaultdict(float)
    by_func = defaultdict(float)
    for v, stack in samples:
        o, f = None, None
        for fr in stack:
            o = owner_of(fr)
            if o:
                f = fr
                break
        if o is None:
            by_owner["runtime (no owner)"] += v
        else:
            by_owner[o] += v
            if o.startswith("MuxMaster"):
                by_func[short(f)] += v
    print("owner share (nearest owning frame, leaf -> root):")
    for o, v in sorted(by_owner.items(), key=lambda kv: -kv[1]):
        print(f"  {o:<24} {100 * v / total:6.2f}%")
    print(f"\ntop MuxMaster owning functions (share of whole profile):")
    for fn, v in sorted(by_func.items(), key=lambda kv: -kv[1])[:top_n]:
        print(f"  {100 * v / total:6.2f}%  {fn}")


if __name__ == "__main__":
    main()
