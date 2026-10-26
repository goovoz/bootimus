import { expect, loginAsAdmin, openTab, test } from './helpers';

// [bootimus-fork L0] fork 自有的前端逻辑搬到了 web/static/fork-ui.js，
// 靠 index.html 里一行 script 标签挂上去。这个用例守住两件事：
//   1. 那 13 个函数在页面里仍然可见（少一个就是 script 标签被删/顺序被改）
//   2. 设置页里的代理表单还在（fork 的 DOM 片段没被上游改动顶掉）
const FORK_FUNCS = [
  'loadProxyConfig',
  'saveProxyConfig',
  'openAutoInstallTemplateModal',
  'currentAiTemplate',
  'onAutoInstallTemplateChange',
  'renderAiTemplateForm',
  'randomHostnameFor',
  'randomizeAiHostname',
  'toggleAiPerClient',
  'scheduleAiTemplatePreview',
  'collectAiTemplateValues',
  'previewAutoInstallTemplate',
  'saveAutoInstallTemplate',
];

test('fork-ui.js 的函数都挂在全局，代理表单也在', async ({ page }) => {
  await loginAsAdmin(page);
  await openTab(page, 'settings');

  // eval 才能看到顶层 const（它们在全局词法环境里，不在 window 上）
  const kinds = await page.evaluate(
    (names) => names.map((n) => {
      try {
        // eslint-disable-next-line no-eval
        return eval('typeof ' + n);
      } catch {
        return 'undefined';
      }
    }),
    FORK_FUNCS,
  );

  const broken = FORK_FUNCS.filter((_, i) => kinds[i] !== 'function');
  expect(broken, '这些 fork 函数在页面里不可见：' + broken.join(', ')).toEqual([]);

  await expect(page.locator('#proxy-address')).toBeVisible();
  await expect(page.locator('#proxy-form button[type="submit"]')).toBeVisible();
});
