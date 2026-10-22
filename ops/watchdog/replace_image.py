#!/usr/bin/env python3
"""改 compose 里的 image 行（并把注释里的版本串一起更新）。幂等，重复跑不会叠加注释。"""
import argparse
import re
import sys
from pathlib import Path

p_default = '/opt/bootimus/docker-compose.yml'


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument('--digest', required=True, help='sha256:...')
    ap.add_argument('--version', default='', help='例如 0.1.77+g8bedb3d')
    ap.add_argument('--file', default=p_default)
    a = ap.parse_args()

    if not re.fullmatch(r'sha256:[0-9a-f]{64}', a.digest):
        print(f'digest 格式不对：{a.digest}', file=sys.stderr)
        return 1

    path = Path(a.file)
    src = path.read_text(encoding='utf-8')
    lines = src.splitlines(keepends=True)

    # 先删旧的说明注释，避免重复
    lines = [ln for ln in lines if '固定到具体构建' not in ln and '由 watchdog 自动固定' not in ln]

    out, done = [], False
    for ln in lines:
        if ln.lstrip().startswith('image:') and 'bootimus' in ln:
            indent = ln[:len(ln) - len(ln.lstrip())]
            if a.version:
                tagver = a.version.replace('+g', '-g')
                out.append(f'{indent}# 由 watchdog 自动固定：{a.version}（可读 tag {tagver}）。不要用 latest，会漂移\n')
            else:
                out.append(f'{indent}# 由 watchdog 自动固定到 digest。不要用 latest，会漂移\n')
            out.append(f'{indent}image: ghcr.io/goovoz/bootimus@{a.digest}\n')
            done = True
        else:
            out.append(ln)

    if not done:
        print('没找到 bootimus 的 image 行', file=sys.stderr)
        return 1

    path.write_text(''.join(out), encoding='utf-8')
    print(f'compose 已指向 {a.digest[:19]}…')
    return 0


if __name__ == '__main__':
    sys.exit(main())
