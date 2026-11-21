#!/usr/bin/env python3
"""Exercise the nightly workflow's Git steps on disposable local repos.

Run with Python 3 and PyYAML from a checkout with the workflow's base commit.
No network, GitHub token, or real branch updates are used.
"""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile

import yaml


ROOT = Path(__file__).resolve().parents[1]
FLOOR = "9cc0aecbe7fbf00ea5870992d4c3761127e1be88"
WINDOWS_CI = ".github/workflows/go-ci-windows.yml"
JOBS = yaml.safe_load((ROOT / ".github/workflows/fork-sync.yml").read_text())["jobs"]
ENV = {
    **os.environ,
    "GIT_CONFIG_GLOBAL": os.devnull,
    "GIT_AUTHOR_NAME": "Fixture",
    "GIT_AUTHOR_EMAIL": "fixture@example.net",
    "GIT_COMMITTER_NAME": "Fixture",
    "GIT_COMMITTER_EMAIL": "fixture@example.net",
}


def run(*args, cwd, env=ENV, reject=None):
    result = subprocess.run(args, cwd=cwd, env=env, capture_output=True, text=True)
    log = result.stdout + result.stderr
    if reject is None:
        assert result.returncode == 0, log
    else:
        assert result.returncode != 0 and reject in log, log
    return result.stdout.strip()


def git(cwd, *args):
    return run("git", *map(str, args), cwd=cwd)


def shell(job, step, cwd, env, reject=None):
    script = next(
        s["run"] for s in JOBS[job]["steps"] if step in (s.get("id"), s.get("name"))
    )
    run("bash", "-eo", "pipefail", "-c", script, cwd=cwd, env=env, reject=reject)


def edit(cwd, filename, text):
    path = cwd / filename
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text + "\n")
    git(cwd, "add", filename)


def commit(cwd, filename, text):
    edit(cwd, filename, text)
    git(cwd, "commit", "-m", f"Edit {filename}")
    return git(cwd, "rev-parse", "HEAD")


def make_merge(cwd):
    base = git(cwd, "rev-parse", "HEAD")
    git(cwd, "switch", "-c", "fixture-side")
    commit(cwd, "fixture-side.txt", "Side branch content")
    git(cwd, "switch", "--detach", base)
    commit(cwd, "fixture-first-parent.txt", "First-parent content")
    git(cwd, "merge", "--no-ff", "--no-commit", "fixture-side")
    edit(cwd, "fixture-merge-only.txt", "Present only in the merge tree")
    git(cwd, "commit", "-m", "Merge with a merge-only amendment")
    return git(cwd, "rev-parse", "HEAD")


def scenario(case, directory):
    prep = directory / "prepare"
    upstream = directory / "upstream"
    fork = directory / "fork"
    for repo in (upstream, fork):
        repo.mkdir()
        git(repo, "init", "--bare", "--initial-branch=main")
    prep.mkdir()
    git(prep, "init")
    git(prep, "fetch", "--no-tags", ROOT, FLOOR)
    git(prep, "checkout", "--detach", FLOOR)
    common = make_merge(prep) if case == "upstream-merge" else FLOOR
    removes_windows_ci = case in ("windows-ci-deleted", "windows-ci-upstream-edit")
    if removes_windows_ci:
        git(prep, "rm", WINDOWS_CI)
    if case == "fork-only-merge":
        old = make_merge(prep)
        assert git(prep, "rev-list", "HEAD", "--", "fixture-merge-only.txt") == old
    else:
        old = commit(prep, "fixture-fork.txt", "Fork edit")
    git(prep, "push", fork, "HEAD:refs/heads/main")

    git(prep, "checkout", "--detach", common)
    if case == "new-upstream-merge":
        make_merge(prep)
    upstream_path = {
        "conflict": "fixture-fork.txt",
        "workflow-change": ".github/workflows/fixture.yml",
        "windows-ci-upstream-edit": WINDOWS_CI,
    }.get(case, "fixture-upstream.txt")
    patch_equivalent = case in ("upstream-copy-present", "upstream-copy-reverted")
    if case == "already-current":
        base = common
    elif patch_equivalent:
        # Same patch as the fork commit, but a different commit identity.
        edit(prep, "fixture-fork.txt", "Fork edit")
        git(prep, "commit", "-m", "Independent upstream copy of fork edit")
        if case == "upstream-copy-reverted":
            git(prep, "revert", "--no-edit", "HEAD")
            assert not (prep / "fixture-fork.txt").exists()
        base = git(prep, "rev-parse", "HEAD")
        assert base != old
        assert git(prep, "cherry", base, old) == f"- {old}"
    else:
        base = commit(prep, upstream_path, "Upstream edit")
    git(prep, "push", upstream, "HEAD:refs/heads/main")
    git(prep, "checkout", "--detach", old)
    output = directory / "output"
    env = {
        **ENV,
        "GITHUB_SHA": old,
        "GITHUB_OUTPUT": str(output),
        "GITHUB_STEP_SUMMARY": str(directory / "summary"),
        "RUNNER_TEMP": str(directory),
        "UPSTREAM_URL": str(upstream),
    }
    rejects = {
        "fork-only-merge": f"Fork-only merge {old} requires maintainer review",
        "conflict": "Unresolved rebase conflict",
        "workflow-change": "The rebase changes workflow files",
        "windows-ci-upstream-edit": "Unresolved rebase conflict",
    }
    shell("prepare", "rebase", prep, env, reject=rejects.get(case))
    if case in rejects or case == "already-current":
        assert git(fork, "rev-parse", "main") == old
        assert not output.exists(), "unexpected candidate output"
        if removes_windows_ci:
            assert not git(fork, "ls-tree", "main", "--", WINDOWS_CI)
        if case == "fork-only-merge":
            assert git(prep, "rev-parse", "HEAD") == old
            assert git(fork, "show", "main:fixture-merge-only.txt") == "Present only in the merge tree"
        return

    shell("prepare", "bundle", prep, env)
    outputs = dict(line.split("=", 1) for line in output.read_text().splitlines())
    candidate = outputs["candidate"]
    assert outputs["upstream"] == base
    if case == "upstream-copy-present":
        # Replaying an already-present edit must not create a redundant commit.
        assert candidate == base
    else:
        assert git(prep, "rev-parse", f"{candidate}^") == base, "fork patch was not replayed"
    assert git(prep, "show", f"{candidate}:fixture-fork.txt") == "Fork edit"
    if case in ("upstream-merge", "new-upstream-merge"):
        assert git(prep, "show", f"{candidate}:fixture-merge-only.txt") == "Present only in the merge tree"
        assert git(prep, "rev-list", "--merges", f"{base}..{candidate}") == ""

    (directory / "fork-sync").mkdir()
    shutil.copyfile(directory / "candidate.bundle", directory / "fork-sync/candidate.bundle")
    env.update(CANDIDATE=candidate, UPSTREAM=base)
    for name in ("test", "writer"):
        cwd = directory / name
        cwd.mkdir()
        git(cwd, "init")
        git(cwd, "remote", "add", "origin", fork)
        git(cwd, "fetch", "origin", "main")
        git(cwd, "checkout", "--detach", old)
    tester = directory / "test"
    writer = directory / "writer"
    shell("test", "Verify and check out the preselected candidate", tester, env)
    assert git(tester, "rev-parse", "HEAD") == candidate

    expected, reject = candidate, None
    if case == "fork-race":
        git(prep, "checkout", "--detach", old)
        expected = commit(prep, "fixture-human.txt", "New fork main work")
        git(prep, "push", fork, "HEAD:refs/heads/main")
        reject = "stale info"
    if case == "upstream-race":
        git(prep, "checkout", "--detach", base)
        commit(prep, "fixture-new-upstream.txt", "New upstream work")
        git(prep, "push", upstream, "HEAD:refs/heads/main")
        expected, reject = old, "Upstream main moved"
    shell("update-main", "Verify candidate and compare-and-swap fork main", writer, env, reject)
    assert git(writer, "rev-parse", "HEAD") == old
    assert git(fork, "rev-parse", "main") == expected
    if patch_equivalent:
        assert git(fork, "show", "main:fixture-fork.txt") == "Fork edit"
    if removes_windows_ci:
        assert not git(fork, "ls-tree", "main", "--", WINDOWS_CI)


if __name__ == "__main__":
    for case in (
        "fork-only-merge",
        "upstream-merge",
        "new-upstream-merge",
        "upstream-copy-reverted",
        "upstream-copy-present",
        "clean",
        "already-current",
        "conflict",
        "workflow-change",
        "windows-ci-deleted",
        "windows-ci-upstream-edit",
        "fork-race",
        "upstream-race",
    ):
        with tempfile.TemporaryDirectory(prefix="llama-sync-test-") as temp:
            scenario(case, Path(temp))
        print(f"{case}: PASS", flush=True)
