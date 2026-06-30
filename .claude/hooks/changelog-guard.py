#!/usr/bin/env python3
"""PreToolUse(Bash) guard: every `git commit` must include a docs/changelogs entry.

Reads the hook payload from stdin, detects whether the Bash command is a real
`git commit`, and blocks (exit 2) unless a changelog file is staged or the
command itself stages one. Non-commit commands pass through (exit 0).
"""
import json
import os
import re
import shlex
import subprocess
import sys

GLOBAL_OPTS_WITH_ARG = {"-C", "-c", "--git-dir", "--work-tree", "--namespace", "--exec-path"}


def read_command() -> str:
    try:
        data = json.load(sys.stdin)
    except Exception:
        return ""
    return (data.get("tool_input") or {}).get("command", "") or ""


def is_git_commit(command: str) -> bool:
    """True if any shell segment runs `git ... commit` as the subcommand."""
    for segment in re.split(r"&&|\|\||;|\|", command):
        try:
            tokens = shlex.split(segment)
        except ValueError:
            tokens = segment.split()
        if "git" not in tokens:
            continue
        i = tokens.index("git") + 1
        while i < len(tokens):
            tok = tokens[i]
            if tok in GLOBAL_OPTS_WITH_ARG:
                i += 2
                continue
            if tok.startswith("-"):
                i += 1
                continue
            break
        if i < len(tokens) and tokens[i] == "commit":
            return True
    return False


def main() -> int:
    command = read_command()
    if not command or not is_git_commit(command):
        return 0
    # Let help / completion invocations through.
    if re.search(r"--help|(^|\s)-h(\s|$)", command):
        return 0

    repo_root = subprocess.run(
        ["git", "rev-parse", "--show-toplevel"],
        capture_output=True, text=True,
    ).stdout.strip() or os.getcwd()

    staged = subprocess.run(
        ["git", "diff", "--cached", "--name-only"],
        cwd=repo_root, capture_output=True, text=True,
    ).stdout

    changelog_staged = any(
        line.startswith("docs/changelogs/") for line in staged.splitlines()
    )
    if changelog_staged or "docs/changelogs/" in command:
        return 0

    sys.stderr.write(
        "提交被钩子拦截：本次 git commit 未包含变更日志条目。\n\n"
        "请在提交前完成以下步骤（与本次改动放入同一个 commit）：\n"
        "1. 更新 README.md（若本次改动影响功能 / 用法 / 架构 / 部署）。\n"
        "2. 在 docs/changelogs/ 新增一个文件，文件名格式：年-月-日:时-分-秒:变更摘要.md\n"
        '   生成时间戳：date "+%Y-%m-%d:%H-%M-%S"\n'
        "   例如：2026-06-30:14-23-05:新增提交变更日志钩子.md\n"
        "   文件内容建议包含：变更摘要、动机 / 背景、涉及文件或模块。\n"
        "3. git add README.md 'docs/changelogs/<新文件>'，然后重新提交。\n"
    )
    return 2


if __name__ == "__main__":
    sys.exit(main())
