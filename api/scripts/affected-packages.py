#!/usr/bin/env python3
"""Print the Go packages a pull request can affect, as ./relative/dirs, or ./... for all of them.

A package is affected when one of its own files changed or when it, or its tests, import a
package whose non-test source changed. Anything the import graph cannot see, such as go.mod or a
testdata file, selects the whole tree.

Usage (from api/): python3 scripts/affected-packages.py <base-sha>
"""

from __future__ import annotations

import json
import subprocess
import sys
from pathlib import Path

ALL = "./..."


def run(*args: str) -> str:
    return subprocess.run(args, check=True, capture_output=True, text=True).stdout


def main() -> int:
    if len(sys.argv) != 2:
        print(__doc__, file=sys.stderr)
        return 2
    base = sys.argv[1]
    api = Path.cwd()
    repo = Path(run("git", "rev-parse", "--show-toplevel").strip())
    prefix = api.relative_to(repo).as_posix() + "/"
    changed = [
        path[len(prefix):]
        for path in run("git", "diff", "--name-only", base, "HEAD").splitlines()
        if path.startswith(prefix)
    ]
    if not changed:
        print(ALL)
        return 0
    # Only Go sources map onto the import graph; any other api/ file may be read by any package.
    if any(not path.endswith(".go") for path in changed):
        print(ALL)
        return 0

    module = run("go", "list", "-m").strip()

    def package(path: str) -> str:
        directory = str(Path(path).parent)
        return module if directory == "." else f"{module}/{directory}"

    changed_packages = {package(path) for path in changed}
    # Nothing imports a _test.go file, so a test-only change stays inside its own package.
    imported = {package(path) for path in changed if not path.endswith("_test.go")}

    # -test adds each package's test variants, whose Deps include what its tests import.
    decoder = json.JSONDecoder()
    listing = run("go", "list", "-e", "-test", "-json=ImportPath,Deps,ForTest,Dir", "./...")
    affected: set[str] = set()
    index = 0
    while index < len(listing):
        while index < len(listing) and listing[index].isspace():
            index += 1
        if index >= len(listing):
            break
        entry, index = decoder.raw_decode(listing, index)
        path = entry["ImportPath"].split(" ", 1)[0]
        if path.endswith(".test"):
            continue
        owner = entry.get("ForTest") or path.removesuffix("_test")
        if owner in changed_packages or imported & set(entry.get("Deps", [])):
            affected.add(owner)

    # A deleted package has no listing entry; its importers fail to build, so test everything.
    known = set(run("go", "list", "-e", "./...").split())
    if not changed_packages <= known:
        print(ALL)
        return 0

    dirs = sorted("./" + p[len(module) + 1:] if p != module else "." for p in affected)
    print(" ".join(dirs) if dirs else ALL)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
