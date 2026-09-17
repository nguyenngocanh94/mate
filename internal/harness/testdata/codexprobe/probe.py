#!/usr/bin/env python3
"""Codex instruction-chain probe (ADR 0004).

Runs the installed `codex` CLI over the fixtures in scenarios.json and
extracts, per scenario, the instruction text Codex renders between
`<INSTRUCTIONS>\n` and the final `\n</INSTRUCTIONS>` of its
`debug prompt-input` output. That extracted text is the committed evidence in
captures/ for what `project_doc_max_bytes` meters and what the marker and
per-file joiners are.

Modes:
  --check  (default) regenerate every capture into a temp dir and diff it
           against the committed one; exit 1 on any mismatch. Run this against
           a new Codex release to find out whether the metering contract in
           internal/harness/codex.go still matches reality.
  --write  overwrite captures/ and manifest.json from the live CLI. Only for
           deliberately re-baselining after a verified behavior change.

No completion is requested (`debug prompt-input` renders the prompt locally)
and CODEX_HOME always points at a scenario-owned directory, so the operator's
~/.codex is never read.
"""

import argparse
import datetime
import json
import pathlib
import subprocess
import sys
import tempfile

HERE = pathlib.Path(__file__).resolve().parent


def load_scenarios():
    with open(HERE / "scenarios.json", encoding="utf-8") as fh:
        return json.load(fh)["scenarios"]


def run_scenario(sc):
    with tempfile.TemporaryDirectory() as tmp:
        tmp = pathlib.Path(tmp)
        home = tmp / "codex-home"
        home.mkdir()
        for rel, content in sorted(sc["global_files"].items()):
            p = home / rel
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_text(content, encoding="utf-8")
        root = tmp / "tree"
        for rel, content in sorted(sc["files"].items()):
            p = root / rel
            p.parent.mkdir(parents=True, exist_ok=True)
            p.write_text(content, encoding="utf-8")
        write_git_markers(root, sc)
        cwd = root / sc["cwd"]
        cwd.mkdir(parents=True, exist_ok=True)
        argv = ["codex"]
        if sc["project_doc_max_bytes"] > 0:
            argv += ["-c", f"project_doc_max_bytes={sc['project_doc_max_bytes']}"]
        argv += ["debug", "prompt-input", "x"]
        out = subprocess.run(
            argv,
            cwd=cwd,
            env={"PATH": path_env(), "CODEX_HOME": str(home), "HOME": str(tmp)},
            capture_output=True,
            check=True,
        )
        return extract_instructions(out.stdout.decode("utf-8"))


# A git worktree's .git is a regular file holding a `gitdir:` pointer. Codex
# 0.151.0 only stats the marker (file or dir) and never resolves the pointer:
# a real `git worktree add` tree and this unresolvable pointer produced the
# same capture (ADR 0004), so the fixture does not need git.
WORKTREE_GIT_FILE = "gitdir: ../../../main/.git/worktrees/crew-1\n"


def write_git_markers(root, sc):
    marker = root / sc["git_root"] / ".git"
    marker.parent.mkdir(parents=True, exist_ok=True)
    kind = sc.get("git_root_kind", "dir")
    if kind == "dir":
        marker.mkdir(exist_ok=True)
    elif kind == "file":
        marker.write_text(WORKTREE_GIT_FILE, encoding="utf-8")
    else:
        raise SystemExit(f"scenario {sc['id']}: unknown git_root_kind {kind!r}")
    # An enclosing repo (.git directory) that discovery must stop short of.
    outer = sc.get("outer_git_dir")
    if outer:
        (root / outer / ".git").mkdir(parents=True, exist_ok=True)


def path_env():
    import os

    return os.environ.get("PATH", "")


def extract_instructions(stdout):
    # The prompt JSON carries one string starting with "# AGENTS.md
    # instructions". Decode that JSON string, then keep the text between
    # "<INSTRUCTIONS>\n" and the final "\n</INSTRUCTIONS>".
    marker = '"# AGENTS.md instructions'
    i = stdout.find(marker)
    if i < 0:
        raise SystemExit("no AGENTS.md instructions block in codex output")
    decoder = json.JSONDecoder()
    text, _ = decoder.raw_decode(stdout[i:])
    open_tag = "<INSTRUCTIONS>\n"
    close_tag = "\n</INSTRUCTIONS>"
    start = text.index(open_tag) + len(open_tag)
    end = text.rindex(close_tag)
    return text[start:end]


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--check", action="store_true", help="(default) diff live captures against the committed ones")
    ap.add_argument("--write", action="store_true", help="re-baseline captures/ and manifest.json")
    args = ap.parse_args()

    version = subprocess.run(["codex", "--version"], capture_output=True, check=True).stdout.decode().strip()
    failures = []
    for sc in load_scenarios():
        got = run_scenario(sc)
        capture = HERE / "captures" / sc["capture"]
        if args.write:
            capture.write_text(got, encoding="utf-8")
            print(f"wrote {capture.name} ({len(got)} bytes)")
            continue
        want = capture.read_text(encoding="utf-8")
        if got != want:
            failures.append(sc["id"])
            print(f"MISMATCH {sc['id']}:\n  committed: {want!r}\n  live:      {got!r}")
        else:
            print(f"ok {sc['id']} ({len(got)} bytes)")

    if args.write:
        manifest = {
            "codex_version": version,
            "generated_at": datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds"),
            "command": "codex [-c project_doc_max_bytes=N] debug prompt-input x (cwd per scenario, CODEX_HOME per scenario)",
            "extraction": "JSON-decode the prompt string starting with '# AGENTS.md instructions', keep text between '<INSTRUCTIONS>\\n' and the final '\\n</INSTRUCTIONS>'",
        }
        (HERE / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n", encoding="utf-8")
        print(f"manifest.json: {version}")
        return

    print(f"live CLI: {version}")
    if failures:
        print(f"FAILED: {', '.join(failures)} — Codex behavior no longer matches the committed evidence; "
              "re-verify internal/harness/codex.go's metering contract before re-baselining with --write")
        sys.exit(1)


if __name__ == "__main__":
    main()
