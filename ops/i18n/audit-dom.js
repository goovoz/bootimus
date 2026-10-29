// DOM 审计：连到已经起好的实例，遍历所有 tab，抓出仍是纯英文的文本节点。
//
// 为什么要单独一个：静态审计（audit.py）只能看源码字面量，服务端返回的句子
// （`Ignored: vendor class ...`）和模板拼的（`Showing 4 of 4`）在源码里根本不存在，
// 只有真实渲染后才看得见。
//
// 用法：BASE=http://127.0.0.1:18081 ADMIN_PW=xxx node ops/i18n/audit-dom.js
// 输出：JSON 到 stdout，形如 {"missing":[...], "scanned":N, "tabs":[...]}
const { chromium } = require(process.env.PW_MODULE || 'playwright');

const BASE = process.env.BASE || 'http://127.0.0.1:18081';
const PW = process.env.ADMIN_PW;

// 过滤规则与 audit.py 共用 audit-rules.json —— 两边各写一份时已经跑偏过一次。
const _rules = JSON.parse(require('fs').readFileSync(require('path').join(__dirname, 'audit-rules.json'), 'utf8'));
const ALLOW = new Set(_rules.allow);
const NOISE = new RegExp(_rules.noise);
const HAS_LATIN = /[A-Za-z]/;
const HAS_CJK = /[\u4e00-\u9fa5]/;

function isCandidate(t) {
  if (!t || t.length > 200) return false;
  if (!HAS_LATIN.test(t) || HAS_CJK.test(t)) return false;
  if (ALLOW.has(t)) return false;
  if (NOISE.test(t)) return false;
  return true;
}

(async () => {
  if (!PW) { console.error('缺 ADMIN_PW'); process.exit(2); }
  const browser = await chromium.launch({ args: ['--no-sandbox'] });
  const ctx = await browser.newContext({ locale: 'zh-CN', viewport: { width: 1500, height: 1200 } });
  const page = await ctx.newPage();

  await page.goto(BASE, { timeout: 30000 });
  await page.evaluate(() => localStorage.setItem('bootimus_lang', 'zh-CN'));
  await page.reload();
  await page.waitForSelector('#login-password', { timeout: 20000 });
  await page.fill('#login-username', 'admin');
  await page.fill('#login-password', PW);
  await page.click('#login-form button[type="submit"]');
  await page.waitForSelector('[data-tab]', { timeout: 25000 });
  await page.waitForTimeout(2000);

  const tabs = await page.evaluate(() =>
    [...new Set([...document.querySelectorAll('[data-tab]')].map(e => e.getAttribute('data-tab')))]);

  const seen = new Map();   // text -> first tab where it appeared
  let scanned = 0;

  for (const t of tabs) {
    try {
      await page.click(`[data-tab="${t}"]`);
      await page.waitForTimeout(1500);
      const items = await page.evaluate(() => {
        const out = [];
        const skip = new Set(['SCRIPT', 'STYLE', 'CODE', 'PRE', 'NOSCRIPT', 'SVG']);
        const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
        let n;
        while ((n = walker.nextNode())) {
          const el = n.parentElement;
          if (!el || skip.has(el.tagName)) continue;
          if (el.closest('script,style,code,pre,noscript')) continue;
          const s = (n.textContent || '').replace(/\s+/g, ' ').trim();
          if (s) out.push(s);
        }
        return out;
      });
      scanned += items.length;
      for (const s of items) {
        if (!isCandidate(s)) continue;
        if (!seen.has(s)) seen.set(s, t);
      }
    } catch (e) {
      // 单个 tab 失败不影响整体
    }
  }

  await browser.close();

  const missing = [...seen.keys()].sort();
  console.log(JSON.stringify({
    missing,
    scanned,
    tabs,
    where: Object.fromEntries([...seen.entries()]),
  }));
})().catch(e => {
  console.error('扫描失败:', e.message);
  process.exit(1);
});
