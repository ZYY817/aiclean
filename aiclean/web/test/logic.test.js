/* 前端纯逻辑单测：node web/test/logic.test.js
   把 web/src/js/*.js（除启动文件 90-init.js）按打包顺序拼进 vm，用 DOM stub 加载，
   然后直接断言 kindOf / summary / visibleItems / pickUnit / fmt / esc / humanErr 等纯逻辑。 */
'use strict';
const fs = require('fs'), path = require('path'), vm = require('vm');

const jsDir = path.join(__dirname, '..', 'src', 'js');
const files = fs.readdirSync(jsDir).filter(f => f.endsWith('.js') && f !== '90-init.js').sort();
const src = files.map(f => `\n/* ---- ${f} ---- */\n` + fs.readFileSync(path.join(jsDir, f), 'utf8')).join('\n');

/* ---- 最小 DOM/BOM stub ---- */
const mkEl = () => ({
  innerHTML: '', textContent: '', value: '', title: '', disabled: false, children: [],
  style: {}, dataset: {},
  classList: { add() {}, remove() {}, toggle() {}, contains: () => false },
  addEventListener() {}, removeEventListener() {}, appendChild() {}, removeChild() {}, remove() {},
  focus() {}, setSelectionRange() {}, click() {},
  querySelector: () => mkEl(), querySelectorAll: () => [],
});
const store = {};
const ctx = {
  console, setTimeout, clearTimeout, setInterval, clearInterval,
  requestAnimationFrame: f => setTimeout(f, 0),
  ResizeObserver: class { observe() {} unobserve() {} disconnect() {} },
  MutationObserver: class { observe() {} disconnect() {} },
  window: { addEventListener() {}, chrome: undefined },
  document: {
    getElementById: () => mkEl(), querySelector: () => mkEl(), querySelectorAll: () => [],
    createElement: () => mkEl(), addEventListener() {}, activeElement: null,
    documentElement: mkEl(), body: mkEl(), head: mkEl(),
  },
  localStorage: {
    getItem: k => (k in store ? store[k] : null),
    setItem: (k, v) => { store[k] = String(v); },
    removeItem: k => { delete store[k]; },
  },
  navigator: {},
};
let pass = 0, fail = 0;
ctx.t = (name, cond) => { if (cond) pass++; else { fail++; console.error('  FAIL:', name); } };
ctx.globalThis = ctx;

const tests = `
;(function () {
  const it = o => Object.assign({ app: 'x', path: 'C:\\\\x', size: 0, state: 'active', risk: 'safe' }, o);

  /* kindOf：后端字段 → 五类状态 */
  t('kindOf big',        kindOf(it({ big: true })) === 'big');
  t('kindOf moved',      kindOf(it({ state: 'migrated' })) === 'moved');
  t('kindOf locked',     kindOf(it({ state: 'locked' })) === 'lock');
  t('kindOf special',    kindOf(it({ action: 'clean', special: 'recycleBin' })) === 'rev');
  t('kindOf prunable',   kindOf(it({ action: 'clean', prunable: true })) === 'rev');
  t('kindOf clean',      kindOf(it({ action: 'clean' })) === 'clean');
  t('kindOf migrate',    kindOf(it({ action: 'migrate' })) === 'mig');
  t('kindOf default',    kindOf(it({ action: 'keep' })) === 'rev');
  t('kindOf reportOnly', kindOf(it({ action: 'clean', reportOnly: true })) === 'stat');
  t('kindOf stat>lock',  kindOf(it({ reportOnly: true, state: 'locked' })) === 'stat');

  /* needsPrune */
  t('prune flag',  needsPrune(it({ prunable: true })) === true);
  t('prune note',  needsPrune(it({ note: '只删 30 天前的旧文件' })) === true);
  t('not prune',   needsPrune(it({})) === false);

  /* selectable：锁定/已迁移/按龄项不可勾选 */
  t('sel clean',   selectable(it({ action: 'clean' })) === true);
  t('sel lock',    selectable(it({ state: 'locked' })) === false);
  t('sel moved',   selectable(it({ state: 'migrated' })) === false);
  t('sel prune',   selectable(it({ action: 'clean', prunable: true })) === false);
  t('sel big',     selectable(it({ big: true })) === false);        // 大文件只放行单独确认
  t('sel special', selectable(it({ action: 'clean', special: 'hiberOff' })) === false); // 特殊操作不进批量
  t('sel reportOnly', selectable(it({ action: 'clean', reportOnly: true })) === false); // 汇总目录禁勾选

  /* summary：数量/体积汇总 + 已迁移按账本记账 */
  ST.c = { total: 100 * GB, free: 40 * GB };
  ST.items = [it({ action: 'clean', size: 10 * GB, path: 'C:\\\\a' }),
              it({ action: 'migrate', size: 5 * GB, path: 'C:\\\\b' }),
              it({ state: 'locked', size: 3 * GB, path: 'C:\\\\c' }),
              it({ action: 'keep', size: 2 * GB, path: 'C:\\\\d' }),
              it({ state: 'migrated', size: 0, path: 'C:\\\\e' }),
              it({ action: 'review', reportOnly: true, size: 20 * GB, path: 'C:\\\\docs' })];
  ST.bigfiles = []; ST.quar = [{ size: 1 * GB }];
  ST.migs = { 'C:\\\\e': { size: 8 * GB } };
  const s = summary();
  t('sum used',    s.used === 60 * GB);
  t('sum clean',   s.k.clean.n === 1 && s.k.clean.size === 10 * GB);
  t('sum mig',     s.k.mig.size === 5 * GB);
  t('sum lock',    s.k.lock.size === 3 * GB);
  t('sum rev',     s.k.rev.n === 1);
  t('sum moved n', s.k.moved.n === 1);
  t('sum moved size=账本', s.k.moved.size === 8 * GB);   // size=0 的 junction 必须按账本记
  t('sum quar',    s.k.quar.size === 1 * GB);
  t('sum count',   s.count === 4);                     // 仅统计项不进可处理数
  t('sum stat',    s.k.stat.n === 1 && s.k.stat.size === 20 * GB);
  t('sum ai 含 stat', s.ai === 40 * GB);               // 10+5+2+3+20
  t('sum listAll', s.listAll === 6);                   // 4 可处理 + 1 已迁移 + 1 仅统计

  /* visibleItems：过滤 + 搜索 + 排序 */
  Data.filter = 'all'; Data.q = ''; Data.sort = 'size'; Data.dir = -1;
  t('vis all',     visibleItems().length === 6);
  Data.filter = 'clean';
  t('vis clean',   visibleItems().length === 1 && visibleItems()[0].path === 'C:\\\\a');
  Data.filter = 'stat';
  t('vis stat',    visibleItems().length === 1 && visibleItems()[0].path === 'C:\\\\docs');
  Data.filter = 'all'; Data.q = 'b';
  t('vis search',  visibleItems().some(x => x.path === 'C:\\\\b'));
  Data.q = ''; Data.sort = 'app'; Data.dir = 1;
  t('vis sort',    visibleItems().length === 6);
  ST.bigfiles = [it({ big: true, path: 'C:\\\\big1' })];
  Data.filter = 'big';
  t('vis big filter', visibleItems().length === 1 && visibleItems()[0].path === 'C:\\\\big1');
  Data.filter = 'all';
  t('vis all+big', visibleItems().length === 7);

  /* rowHTML：reportOnly 行给「搬到其他盘」、绝不给「清理」；普通缓存给清理；已迁移给迁回 */
  const rhStat = rowHTML(it({ action: 'review', reportOnly: true, path: 'C:\\\\docs', app: '文档', size: 20 * GB }), 0);
  t('row stat 搬家',  rhStat.includes('搬到其他盘') && rhStat.includes('data-a="m"'));
  t('row stat 无清理', !rhStat.includes('>清理<'));
  const rhClean = rowHTML(it({ action: 'clean', path: 'C:\\\\a', app: 'a', size: GB }), 0);
  t('row clean 清理', rhClean.includes('data-a="q"'));
  const rhMoved = rowHTML(it({ state: 'migrated', path: 'C:\\\\e', app: 'e' }), 0);
  t('row moved 迁回', rhMoved.includes('data-a="rb"'));
  /* 锁定/需管理员项不渲染任何操作按钮（即使可 prune） */
  const rhLockPrune = rowHTML(it({ action: 'clean', state: 'locked', note: '只删 7 天前的', path: 'C:\\\\lp', app: 'lp', size: GB }), 0);
  t('row lock 无按钮', !rhLockPrune.includes('data-a="pr"') && !rhLockPrune.includes('data-a="q"') && rhLockPrune.includes('使用中'));
  const rhAdmin = rowHTML(it({ action: 'clean', needsAdmin: true, path: 'C:\\\\adm', app: 'ad', size: GB }), 0);
  t('row admin 无清理', !rhAdmin.includes('data-a="q"') && rhAdmin.includes('需管理员'));
  t('sel admin 禁选', selectable(it({ action: 'clean', needsAdmin: true })) === false);
  /* 已迁移项即使有 prune 语义也只给迁回 */
  const rhMovedPrune = rowHTML(it({ state: 'migrated', note: '只删 7 天前的', path: 'C:\\\\mp', app: 'mp' }), 0);
  t('row moved 无prune', rhMovedPrune.includes('data-a="rb"') && !rhMovedPrune.includes('data-a="pr"'));

  /* pickUnit：格子单位单调、落在范围内 */
  t('unit small',  pickUnit(50 * GB) === 0.25);
  t('unit mid',    pickUnit(500 * GB) >= 2);
  t('unit huge',   pickUnit(4 * 1024 * GB) >= 15);

  /* fmt / esc / baseName / humanErr */
  t('fmt zero',    fmt(0).indexOf('0') === 0);
  t('fmt gb',      fmt(2.5 * GB).includes('GB'));
  t('esc html',    esc('<a "b">&') === '&lt;a &quot;b&quot;&gt;&amp;');
  t('baseName',    baseName('C:\\\\foo\\\\bar') === 'bar');
  t('humanErr zh', typeof humanErr('access denied') === 'string' && humanErr('access denied').length > 0);

  /* 首次启动说明：无 welcomeAcked → 弹；已确认 → 不弹；版本升级后旧确认仍有效 */
  t('welcome 无记录要弹',  shouldShowWelcome({}) === true);
  t('welcome 空prefs要弹', shouldShowWelcome(null) === true);
  t('welcome 已确认不弹',  shouldShowWelcome({ welcomeAcked: '1.0.0' }) === false);
  t('welcome 内容完整', welcomeBody().includes('不可恢复') && welcomeBody().includes('AIClean-Moved') && welcomeBody().includes('本机运行') && welcomeBody().includes('默认关闭'));
  t('welcome 无emoji',   !/[\u{1F300}-\u{1FAFF}\u{2600}-\u{27BF}]/u.test(welcomeBody()));
})();
`;

try {
  vm.runInNewContext(src + tests, ctx, { filename: 'bundle.js' });
} catch (e) {
  console.error('LOAD/RUN ERROR:', e && e.stack || e);
  process.exit(2);
}
console.log(`frontend logic: ${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
