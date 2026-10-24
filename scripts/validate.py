#!/usr/bin/env python3
"""Run the repository's local validation checks without external Python packages."""

from __future__ import annotations

import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
GATEWAY = ROOT / "gateway"
SUPPORTED_PYTHON = {(3, 11), (3, 12)}
MINIMUM_GO = (1, 27, 2)


def find_go() -> str | None:
    go = shutil.which("go")
    if go:
        return go

    if os.name == "nt":
        program_files = os.environ.get("ProgramFiles", r"C:\Program Files")
        candidate = Path(program_files) / "Go" / "bin" / "go.exe"
        if candidate.is_file():
            return str(candidate)

    return None


def run_check(name: str, command: list[str], cwd: Path, env: dict[str, str]) -> bool:
    print(f"\n==> {name}", flush=True)
    try:
        completed = subprocess.run(command, cwd=cwd, env=env, check=False)
    except OSError as error:
        print(f"FAILED: {name}: {error}", file=sys.stderr)
        return False

    if completed.returncode:
        print(f"FAILED: {name} (exit code {completed.returncode})", file=sys.stderr)
        return False
    return True


def main() -> int:
    python_version = sys.version_info[:2]
    if python_version not in SUPPORTED_PYTHON:
        supported = " or ".join(f"{major}.{minor}" for major, minor in sorted(SUPPORTED_PYTHON))
        print(
            f"RESCUE validation supports Python {supported}; "
            f"the active interpreter is {sys.version.split()[0]} ({sys.executable}).",
            file=sys.stderr,
        )
        return 2

    go = find_go()
    if not go:
        print(
            "Go was not found on PATH or in its standard Windows install location. "
            "Install Go 1.27.2 or newer, then reopen the terminal.",
            file=sys.stderr,
        )
        return 2

    version_output = subprocess.run(
        [go, "version"], capture_output=True, text=True, check=False
    )
    match = re.search(r"\bgo(\d+)\.(\d+)\.(\d+)\b", version_output.stdout)
    if version_output.returncode or not match:
        print(f"Could not determine Go version: {version_output.stderr}", file=sys.stderr)
        return 2
    go_version = tuple(map(int, match.groups()))
    if go_version < MINIMUM_GO:
        print(
            f"RESCUE requires Go 1.27.2 or newer; found {version_output.stdout.strip()}.",
            file=sys.stderr,
        )
        return 2

    print(f"Python: {sys.version.split()[0]} ({sys.executable})")
    print(f"Go: {version_output.stdout.strip()} ({go})")

    python_checks = [
        (
            "Compile Python sources",
            [
                "-m",
                "compileall",
                "-q",
                str(ROOT / "harness"),
                str(ROOT / "scripts"),
            ],
        ),
        (
            "Python unit tests",
            [
                "-m",
                "unittest",
                "discover",
                "-s",
                str(ROOT / "harness"),
                "-p",
                "*_test.py",
            ],
        ),
        (
            "Transaction checker differential suite",
            [str(ROOT / "harness" / "checker_differential_test.py")],
        ),
        (
            "Scheduling simulation checks",
            [str(ROOT / "harness" / "scheduling_bound_test.py")],
        ),
        (
            "Admission mechanism checks",
            [str(ROOT / "harness" / "baseline_ladder_test.py")],
        ),
    ]
    go_checks = [
        ("Go package tests", ["test", "-count=1", "./..."]),
        ("Go race tests", ["test", "-race", "-count=1", "./..."]),
        ("Go vet", ["vet", "./..."]),
    ]

    failures = []
    python_env = os.environ.copy()
    for name, arguments in python_checks:
        if not run_check(name, [sys.executable, *arguments], ROOT, python_env):
            failures.append(name)

    go_env = os.environ.copy()
    if os.name == "nt":
        go_env["CGO_ENABLED"] = "1"
        if "CC" not in go_env:
            candidate = Path(r"C:\msys64\ucrt64\bin\gcc.exe")
            if candidate.is_file():
                go_env["CC"] = str(candidate)

        if go_env.get("CC"):
            print(f"Go race-test C compiler: {go_env['CC']}")
            compiler_path = Path(go_env["CC"])
            if compiler_path.is_file():
                go_env["PATH"] = (
                    str(compiler_path.parent)
                    + os.pathsep
                    + go_env.get("PATH", "")
                )
        else:
            print(
                "Go race tests need MSYS2 UCRT64 GCC; "
                "install it or use the Linux CI runner.",
                file=sys.stderr,
            )

    for name, arguments in go_checks:
        if not run_check(name, [go, *arguments], GATEWAY, go_env):
            failures.append(name)

    if failures:
        print("\nValidation incomplete; failed checks:", file=sys.stderr)
        for failure in failures:
            print(f" - {failure}", file=sys.stderr)
        print(
            "On Windows, the Go race detector requires cgo and GCC. "
            "If Application Control blocks generated test executables, do not bypass "
            "that policy; use the repository's GitHub Actions CI run.",
            file=sys.stderr,
        )
        return 1

    print("\nAll local validation checks passed.")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
