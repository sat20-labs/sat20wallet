#!/usr/bin/python3
"""Fixed SDK-only, read-only Git diff with bounded JSON pages."""
import base64
import hashlib
import json
import math
import os
import re
import subprocess
import sys

SDK = "/Users/yingfeng/github/sat20wallet/sdk"
PAGE_CHARS = 32768
PATTERN = r"(staged|unstaged)-(manifest|[1-9][0-9]{0,5})"


def parse_variant(value):
    match = re.fullmatch(PATTERN, value)
    if not match:
        raise ValueError("Expected staged-manifest, unstaged-manifest, or <mode>-<positive page>.")
    mode, part = match.groups()
    return mode, None if part == "manifest" else int(part)


def diff_command(mode):
    if mode not in ("staged", "unstaged"):
        raise ValueError("Unsupported diff mode.")
    command = ["/usr/bin/git", "-c", "core.quotePath=false", "diff"]
    if mode == "staged":
        command.append("--cached")
    command.extend([
        "--no-ext-diff", "--no-textconv", "--no-renames", "--no-color",
        "--submodule=short", "--binary", "--full-index", "--unified=80",
        "--relative", "--", ".",
    ])
    return command


def load_diff(mode):
    env = dict(os.environ)
    env.update({"GIT_OPTIONAL_LOCKS": "0", "GIT_CONFIG_NOSYSTEM": "1",
                "GIT_CONFIG_GLOBAL": "/dev/null", "GIT_PAGER": "cat"})
    result = subprocess.run(diff_command(mode), cwd=SDK, env=env,
                            stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                            stderr=subprocess.PIPE, shell=False, timeout=45)
    if result.returncode:
        raise RuntimeError("git diff exited %s: %s" % (
            result.returncode, result.stderr.decode("utf-8", errors="replace")))
    return result.stdout


def make_response(raw, mode, page):
    try:
        payload = raw.decode("utf-8")
        encoding = "utf-8"
    except UnicodeDecodeError:
        payload = base64.b64encode(raw).decode("ascii")
        encoding = "base64"
    total = max(1, math.ceil(len(payload) / PAGE_CHARS))
    if page is not None and not 1 <= page <= total:
        raise ValueError("Page outside 1..%s; request %s-manifest." % (total, mode))
    response = {
        "scope": SDK, "mode": mode, "kind": "manifest" if page is None else "page",
        "diffSha256": hashlib.sha256(raw).hexdigest(), "diffBytes": len(raw),
        "payloadEncoding": encoding, "payloadChars": len(payload),
        "pageSizeChars": PAGE_CHARS, "totalPages": total, "page": page,
        "untrackedIncluded": False,
        "nextVariant": "%s-%s" % (mode, 1 if page is None else page + 1)
            if page is None or page < total else None,
    }
    if page is not None:
        response["data"] = payload[(page - 1) * PAGE_CHARS:page * PAGE_CHARS]
    return response


def main(argv):
    # CommandRunner appends its fixed timeout argument; it cannot change scope.
    if len(argv) not in (2, 3) or (len(argv) == 3 and not argv[2].isdigit()):
        raise ValueError("Only one configured variant is accepted.")
    mode, page = parse_variant(argv[1])
    response = make_response(load_diff(mode), mode, page)
    print(json.dumps(response, ensure_ascii=True, separators=(",", ":")))


if __name__ == "__main__":
    try:
        main(sys.argv)
    except (ValueError, RuntimeError, subprocess.TimeoutExpired) as error:
        print(str(error), file=sys.stderr)
        sys.exit(2)
