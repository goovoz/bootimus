#!/usr/bin/env python3
"""清理 Docker Hub 上多余的 tag，策略与 ghcr-retention 对齐：每个版本号只留最新一次构建。

bootimus 每次发布在 Hub 上产生 3 个 tag：
  <base>-g<sha>   不可变，一次构建一个
  <base>          漂移，同一基线指向最新那次
  latest          漂移

所以「每个版本号只留最新一次构建」在 tag 维度上就是：每个 base 只保留 last_updated
最新的那个 <base>-g<sha>；纯版本号和 latest 全留。

环境变量：
  DOCKERHUB_USERNAME / DOCKERHUB_TOKEN  必需（token 是 Hub 的 access token）
  REGISTRY_IMAGE                        仓库名，默认 bootimus
  DRY_RUN                               true 只打印计划
  HUB_KEEP_TAGS                         显式保留集合（逗号分隔），优先于默认策略
"""
import json
import os
import re
import sys
import urllib.error
import urllib.request

API = 'https://hub.docker.com/v2'
USER = os.environ.get('DOCKERHUB_USERNAME', '').strip()
TOKEN = os.environ.get('DOCKERHUB_TOKEN', '').strip()
REPO = os.environ.get('REGISTRY_IMAGE', 'bootimus').strip()
DRY = os.environ.get('DRY_RUN', '').lower() in ('1', 'true', 'yes')
KEEP_INPUT = os.environ.get('HUB_KEEP_TAGS', '').strip()

# 允许 REGISTRY_IMAGE 写成 "deemop/bootimus"；Hub API 要的是 namespace 与 repo 分开，
# 不拆的话 URL 会变成 deemop/deemop/bootimus（404）。
if '/' in REPO:
    ns, _, name = REPO.partition('/')
    USER = USER or ns
    REPO = name

REV_TAG = re.compile(r'^(\d+\.\d+\.\d+)-g([0-9a-f]{7,})$')
VER_TAG = re.compile(r'^\d+\.\d+\.\d+$')


def request(method, url, payload=None, headers=None):
    data = json.dumps(payload).encode() if payload is not None else None
    h = {'Content-Type': 'application/json'}
    h.update(headers or {})
    req = urllib.request.Request(url, data=data, headers=h, method=method)
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            raw = resp.read()
            return json.loads(raw) if raw else {}
    except urllib.error.HTTPError as e:
        body = ''
        try:
            body = e.read().decode('utf-8', 'replace')[:200]
        except Exception:
            pass
        raise RuntimeError('HTTP %s %s' % (e.code, body)) from None


def login():
    try:
        out = request('POST', API + '/users/login', {'username': USER, 'password': TOKEN})
    except RuntimeError as e:
        print('::error::Docker Hub 登录失败（%s）——检查 DOCKERHUB_TOKEN 是否有效/过期' % e)
        sys.exit(1)
    jwt = out.get('token')
    if not jwt:
        print('::error::登录没有返回 token')
        sys.exit(1)
    return jwt


def list_tags(jwt):
    tags, page = [], 1
    while True:
        url = '%s/repositories/%s/%s/tags/?page_size=100&page=%d' % (API, USER, REPO, page)
        data = request('GET', url, headers={'Authorization': 'JWT ' + jwt})
        chunk = data.get('results') or []
        tags.extend(chunk)
        if len(chunk) < 100:
            return tags
        page += 1


def plan(tags):
    """返回 (保留集合, 删除列表, 说明)。"""
    if KEEP_INPUT:
        keep = {x.strip() for x in KEEP_INPUT.split(',') if x.strip()}
        drop = [t for t in tags if t['name'] not in keep]
        return keep, drop, '显式保留集合'

    # 每个版本号挑 last_updated 最新的那次构建
    newest = {}
    for t in tags:
        m = REV_TAG.match(t['name'])
        if not m:
            continue
        base = m.group(1)
        cur = newest.get(base)
        if cur is None or (t.get('last_updated') or '') > (cur.get('last_updated') or ''):
            newest[base] = t

    keep = {'latest'}
    keep |= {t['name'] for t in tags if VER_TAG.match(t['name'])}
    keep |= {t['name'] for t in newest.values()}


    drop = [t for t in tags if t['name'] not in keep]
    note = '每个版本号只留最新一次构建' + ('（%d 个版本）' % len(newest) if newest else '')
    return keep, drop, note


def main():
    if not USER or not TOKEN:
        print('::error::缺少 DOCKERHUB_USERNAME / DOCKERHUB_TOKEN')
        return 1

    jwt = login()
    try:
        tags = list_tags(jwt)
    except RuntimeError as e:
        print('::error::读取 tag 列表失败：%s' % e)
        return 1

    print('Docker Hub %s/%s：%d 个 tag' % (USER, REPO, len(tags)))
    for t in sorted(tags, key=lambda x: x['name']):
        print('   %-22s %s  %s' % (t['name'], (t.get('digest') or '')[:19],
                                   (t.get('last_updated') or '')[:19]))

    if not tags:
        print('没有 tag，结束')
        return 0

    keep, drop, note = plan(tags)
    print('\n策略：%s' % note)
    print('保留 %d 个：%s' % (len(keep), ', '.join(sorted(keep))))

    if 'latest' not in keep:
        print('::error::保留集合里没有 latest，中止（以免把线上入口删掉）')
        return 1
    if not drop:
        print('没有需要删除的 tag')
        _summary(len(tags), 0, keep)
        return 0

    print('\n将删除 %d 个：%s' % (len(drop), ', '.join(t['name'] for t in drop)))
    if DRY:
        print('DRY RUN：未实际删除')
        _summary(len(tags), len(drop), keep)
        return 0

    fail = 0
    for t in drop:
        name = t['name']
        if name == 'latest':
            print('   跳过 latest（双保险）')
            continue
        try:
            request('DELETE', '%s/repositories/%s/%s/tags/%s' % (API, USER, REPO, name),
                    headers={'Authorization': 'JWT ' + jwt})
            print('   已删除 %-22s %s' % (name, (t.get('digest') or '')[:19]))
        except RuntimeError as e:
            fail += 1
            print('::error::删除 %s 失败：%s' % (name, e))

    try:
        left = list_tags(jwt)
    except RuntimeError:
        left = []
    print('\n清理后剩 %d 个 tag：%s' % (len(left), ', '.join(sorted(x['name'] for x in left))))
    _summary(len(left), len(drop), keep)
    return 1 if fail else 0


def _summary(remaining, deleted, keep):
    out = os.environ.get('GITHUB_STEP_SUMMARY')
    if not out:
        return
    try:
        with open(out, 'a', encoding='utf-8') as fh:
            fh.write('### Docker Hub tag 清理\n\n')
            fh.write('| 项 | 值 |\n|---|---|\n')
            fh.write('| 保留集合 | %s |\n' % ', '.join('`%s`' % k for k in sorted(keep)))
            fh.write('| 删除 tag 数 | %d |\n' % deleted)
            fh.write('| 剩余 tag 数 | %d |\n' % remaining)
            fh.write('| 模式 | %s |\n' % ('dry-run' if DRY else '实际删除'))
            fh.write('\n> Hub 只开放删 tag 的接口，未标记的底层镜像由 Hub 自行回收。\n')
    except OSError:
        pass


if __name__ == '__main__':
    sys.exit(main())
