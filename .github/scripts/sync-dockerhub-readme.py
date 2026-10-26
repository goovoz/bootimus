#!/usr/bin/env python3
"""把仓库里的 DOCKERHUB.md 推到 Docker Hub 的仓库页面（full_description）+ 短描述。

环境变量：
  DOCKERHUB_USERNAME / DOCKERHUB_TOKEN  必需（token 是 Hub 的 access token，不是登录密码）
  REGISTRY_IMAGE                        仓库名，默认 bootimus
  HUB_FILE                              介绍正文来源，默认 DOCKERHUB.md
"""
import json
import os
import sys
import urllib.error
import urllib.request
from pathlib import Path

API = 'https://hub.docker.com/v2'
USER = os.environ.get('DOCKERHUB_USERNAME', '').strip()
TOKEN = os.environ.get('DOCKERHUB_TOKEN', '').strip()
REPO = os.environ.get('REGISTRY_IMAGE', 'bootimus').strip()
DOC = Path(os.environ.get('HUB_FILE', 'DOCKERHUB.md'))

# REGISTRY_IMAGE 可能写成 "bootimus" 也可能写成 "deemop/bootimus"。
# Hub 的 API 是 /repositories/<namespace>/<repo>/，所以带斜杠时要把 namespace 拆出来，
# 否则会拼成 deemop/deemop/bootimus 然后 404（踩过一次）。
if '/' in REPO:
    ns, _, name = REPO.partition('/')
    USER = USER or ns
    REPO = name

# Hub 页面上那一行短描述，超出会被截断
SHORT = 'Bootimus 的 PXE/HTTP 启动服务器镜像：中文界面 + 下载代理 + 断点续传'


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


def main():
    if not USER or not TOKEN:
        print('::error::缺少 DOCKERHUB_USERNAME / DOCKERHUB_TOKEN')
        return 1
    if not DOC.exists():
        print('::warning::没有 %s，跳过介绍同步' % DOC)
        return 0
    text = DOC.read_text(encoding='utf-8')
    if len(text) > 100_000:
        print('::error::%s 超过 Hub 的 100 KB 限制（%d 字节）' % (DOC, len(text)))
        return 1

    try:
        out = request('POST', API + '/users/login',
                      {'username': USER, 'password': TOKEN})
    except RuntimeError as e:
        print('::error::Docker Hub 登录失败（%s）——检查 DOCKERHUB_TOKEN 是否有效/过期' % e)
        return 1
    jwt = out.get('token')
    if not jwt:
        print('::error::登录没有返回 token')
        return 1

    try:
        request('PATCH', '%s/repositories/%s/%s/' % (API, USER, REPO),
                {'full_description': text, 'description': SHORT},
                headers={'Authorization': 'JWT ' + jwt})
    except RuntimeError as e:
        print('::error::更新 %s/%s 的介绍失败：%s' % (USER, REPO, e))
        return 1

    print('已同步 %s/%s：正文 %d 字符' % (USER, REPO, len(text)))
    summary = os.environ.get('GITHUB_STEP_SUMMARY')
    if summary:
        try:
            with open(summary, 'a', encoding='utf-8') as fh:
                fh.write('### Docker Hub 介绍\n\n')
                fh.write('| 项 | 值 |\n|---|---|\n')
                fh.write('| 仓库 | `%s/%s` |\n' % (USER, REPO))
                fh.write('| 正文 | %d 字符（来自 `%s`） |\n' % (len(text), DOC))
        except OSError:
            pass
    return 0


if __name__ == '__main__':
    sys.exit(main())
