#!/usr/bin/env python3
"""Copy the minimal runnable Tesseract set out of a 7z-extracted
UB-Mannheim installer (NSIS) into a bundle dir.

Walks the DLL import closure of tesseract.exe with objdump, copies
tesseract.exe + every non-system DLL it (transitively) needs, using a
case-insensitive filename lookup (the installer ships libLerc.dll with
a capital L, which a plain lowercase lookup misses).

Usage: bundle-tesseract.py --src <extracted-dir> --dest <dest-dir>
Exits non-zero if tesseract.exe or any closure DLL is missing.
"""
import argparse
import collections
import os
import shutil
import subprocess
import sys

SYSTEM_DLLS = {
    "kernel32.dll", "msvcrt.dll", "advapi32.dll", "bcrypt.dll",
    "crypt32.dll", "gdi32.dll", "iphlpapi.dll", "secur32.dll",
    "user32.dll", "wldap32.dll", "ws2_32.dll",
}


def imports(pe_path):
    out = subprocess.run(
        ["objdump", "-p", pe_path], capture_output=True, text=True
    ).stdout
    return [
        line.split("DLL Name:")[1].strip()
        for line in out.splitlines()
        if "DLL Name:" in line
    ]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--src", required=True)
    ap.add_argument("--dest", required=True)
    args = ap.parse_args()

    index = {
        f.lower(): f
        for f in os.listdir(args.src)
        if os.path.isfile(os.path.join(args.src, f))
    }
    if "tesseract.exe" not in index:
        print("bundle-tesseract: tesseract.exe not found in", args.src)
        return 1

    seen = set()
    queue = collections.deque(["tesseract.exe"])
    while queue:
        name = queue.popleft()
        if name.lower() in seen:
            continue
        seen.add(name.lower())
        real = index.get(name.lower())
        if not real:
            print("bundle-tesseract: missing file:", name)
            return 1
        for dep in imports(os.path.join(args.src, real)):
            low = dep.lower()
            if low.startswith(("api-ms-", "ucrtbase", "vcruntime", "msvcp")):
                continue
            if low in SYSTEM_DLLS:
                continue
            if low not in seen:
                queue.append(dep)

    os.makedirs(args.dest, exist_ok=True)
    for name in sorted(seen):
        real = index[name]
        shutil.copy2(os.path.join(args.src, real), os.path.join(args.dest, real))
    print(f"bundle-tesseract: copied {len(seen)} files to {args.dest}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
