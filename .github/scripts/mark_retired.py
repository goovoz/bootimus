#!/usr/bin/env python3
"""[bootimus-fork L0] 把一个补丁标记为已退役：
  1. .github/fork-patches.json 里 status=retired + 退役时间/上游 commit
  2. docs/FORK-PATCHES.md 的「退役记录」表追加一行

用法：mark_retired.py <patch-id> <upstream-sha> [说明]
"""
import json
import sys
from datetime import datetime, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
MANIFEST = ROOT / ".github" / "fork-patches.json"
DOC = ROOT / "docs" / "FORK-PATCHES.md"
MARKER = "<!-- 退役记录由 workflow 自动追加 -->"


def main() -> int:
    if len(sys.argv) < 3:
        print("usage: mark_retired.py <patch-id> <upstream-sha> [note]", file=sys.stderr)
        return 2
    patch_id, upstream_sha = sys.argv[1], sys.argv[2]
    note = sys.argv[3] if len(sys.argv) > 3 else "上游已自行实现同等能力"

    now = datetime.now(timezone.utc).strftime("%Y-%m-%d")
    manifest = json.loads(MANIFEST.read_text(encoding="utf-8"))

    hit = None
    for p in manifest.get("patches", []):
        if p.get("id") == patch_id:
            hit = p
            break
    if hit is None:
        print(f"ERROR: 清单中没有补丁 {patch_id}", file=sys.stderr)
        return 1

    hit["status"] = "retired"
    hit["retired_at"] = now
    hit["retired_upstream_commit"] = upstream_sha
    MANIFEST.write_text(
        json.dumps(manifest, ensure_ascii=False, indent=2) + "\n", encoding="utf-8"
    )

    if DOC.exists():
        text = DOC.read_text(encoding="utf-8")
        row = f"| {now} | `{patch_id}` | `{upstream_sha[:8]}` | {note} |"
        if MARKER in text:
            text = text.replace(MARKER, f"{MARKER}\n{row}", 1)
        else:
            text = text.rstrip("\n") + f"\n{row}\n"
        DOC.write_text(text, encoding="utf-8")

    print(f"retired {patch_id} at upstream {upstream_sha[:8]} ({now})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
