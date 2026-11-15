/* ===== 00-core · 工具 / 桥接 / 状态 / 路由 / 弹窗 / 主题 ===== */
window.AIC_PACKED = 1;
const $ = (s, el) => (el || document).querySelector(s);
const $$ = (s, el) => [...(el || document).querySelectorAll(s)];
const esc = s => String(s ?? '').replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const GB = 2 ** 30, MB = 2 ** 20, KB = 1024;
/* 统一的大小格式：≥10GB 一位小数，<10GB 两位，MB/KB 取整 */
function fmt(b) {
  b = Number(b) || 0;
  if (b >= GB) return (b / GB).toFixed(b >= 10 * GB ? 1 : 2) + ' GB';
  if (b >= MB) return Math.round(b / MB) + ' MB';
  if (b >= KB) return Math.round(b / KB) + ' KB';
  return b > 0 ? '1 KB' : '0';
}
const gb1 = b => (b / GB).toFixed(1);
const CHECK = '<svg viewBox="0 0 24 24"><path d="M5 12.5 10 17.5 19 7"/></svg>';
const CROSS = '<svg viewBox="0 0 24 24"><path d="M7 7l10 10M17 7 7 17"/></svg>';
/* 等浏览器真正画完一帧：后端调用会卡住界面线程，调用前必须先让"进行中"的画面上屏 */
const paint = () => new Promise(r => requestAnimationFrame(() => setTimeout(r, 30)));
const sleep = ms => new Promise(r => setTimeout(r, ms));
const calm = () => document.documentElement.classList.contains('calm') || matchMedia('(prefers-reduced-motion:reduce)').matches;

/* ---------- 全局状态 ---------- */
const ST = {
  version: '', drives: [], c: null,      // 磁盘
  items: [], rulesPath: '',              // 扫描结果
  quar: [], logs: [],                    // 清理残留 / 日志
  phase: 'boot',                         // boot | scanning | ready | error
  error: '', scannedAt: null, stale: false,
  page: 'home', demo: false, elevated: false,
  migs: {},                              // 迁移账本：源路径 → {dst,bak,size}
  bigfiles: [],                          // 大文件猎手结果（≥300MB 散文件，含 dup 标记）
};

/* ---------- 桥接：所有后端调用都走这里，绝不抛异常 ----------
   exe 里（真 WebView2）：Go 端异步执行 —— 调用时尾参带 "__aic:<id>"，立即返回，
   结果经 window.__aicDone(id, {v|e}) 送回；期间窗口不卡死。
   浏览器调试：window[name] 是 installMock 的普通函数，直接 await。 */
const LIVE = !!(window.chrome && window.chrome.webview);
let __aicSeq = 0;
const __aicPend = new Map();
window.__aicDone = (id, payload) => {
  const p = __aicPend.get(id);
  if (!p) return;
  __aicPend.delete(id);
  p(payload && payload.e ? { ok: false, error: payload.e } : (payload ? payload.v : {}));
};
/* 各桥调用的超时：默认 90s；长操作（扫描/迁移/删除大目录/大文件猎取）给 10 分钟。
   有进度回调的操作按"闲置超时"算——__aicProg 每次推送都把计时归零，
   几十 GB 的迁移不会因为总时长超限被误杀；只有后端真挂死（没进度也没结果）才超时。 */
const GO_TIMEOUTS = { scanItems: 600000, findBigFiles: 600000, migrateItem: 600000, rollbackMigration: 600000, autoCleanNow: 600000, deleteItem: 600000, pruneItem: 600000, finishMigration: 600000, restoreItem: 300000, purgeItem: 300000 };
const PROG_OPS = { scanItems: 1, migrateItem: 1, findBigFiles: 1, rollbackMigration: 1, deleteItem: 1, pruneItem: 1, finishMigration: 1 };
async function go(name, ...args) {
  if (typeof window[name] !== 'function') return { ok: false, error: '后端接口不可用：' + name };
  try {
    if (LIVE) {
      const id = '__aic:' + (++__aicSeq);
      const pr = new Promise(res => __aicPend.set(id, res));
      window[name](...args, id);           // Go 端立即返回 null，结果走 __aicDone
      const to = GO_TIMEOUTS[name] ?? 90000;
      if (!to) return await pr;
      const progAware = !!PROG_OPS[name];
      const winner = await Promise.race([pr, new Promise(res => {
        let lastAct = Date.now();
        const tick = setInterval(() => {
          if (progAware && window.__aicLastProg > lastAct) lastAct = window.__aicLastProg;
          if (Date.now() - lastAct >= to) { clearInterval(tick); res('__aic_timeout'); }
        }, 1000);
        pr.finally(() => clearInterval(tick));
      })]);
      if (winner === '__aic_timeout') { __aicPend.delete(id); return { ok: false, error: '操作超时，请重试' }; }
      return winner;
    }
    const r = await window[name](...args);
    return r == null ? {} : r;
  } catch (e) {
    return { ok: false, error: String(e && e.message || e) };
  }
}

/* ---------- 浏览器调试用的模拟后端（exe 里不会启用） ---------- */
function installMock() {
  ST.demo = true;
  const U = 'C:\\Users\\me';
  const it = (app, dir, path, gb, note, risk, action, state, lastMod) => ({
    ruleId: app, app, dir, path, size: Math.round(gb * GB), files: Math.round(gb * 4200) + 12, note, risk,
    riskTxt: state === 'locked' ? '运行中' : risk === 'cache' ? '低风险' : risk === 'userData' ? '用户数据' : '待确认',
    action, state, statusTxt: '', lastMod });
  const db = {
    free: 19.4 * GB, total: 198.2 * GB, dfree: 61.5 * GB,
    items: [
      it('Devin Desktop', 'Roaming\\Devin', U + '\\AppData\\Roaming\\Devin', 13.0, '含 sessions.db（会话库，可能 10GB+）。只能在 Devin 完全退出后迁移。', 'userData', 'migrate', 'locked', '2026-11-11'),
      it('NVIDIA', 'NVIDIA\\DXCache', U + '\\AppData\\Local\\NVIDIA\\DXCache', 10.3, 'NVIDIA 着色器缓存，删除后游戏和应用会自动重建。', 'cache', 'clean', 'active', '2026-11-10'),
      it('微信/QQ', 'Documents\\xwechat_files', U + '\\Documents\\xwechat_files', 6.51, '聊天图片、视频和文件。请在微信内清理，不建议直接删除。', 'review', 'none', 'locked', '2026-11-11'),
      it('系统', 'Local\\Temp', U + '\\AppData\\Local\\Temp', 5.04, '临时文件，只清理较旧的部分更安全。', 'review', 'none', 'active', '2026-11-11'),
      it('DirectX', 'Local\\D3DSCache', U + '\\AppData\\Local\\D3DSCache', 3.83, 'DirectX 着色器缓存，删除后自动重建。', 'cache', 'clean', 'active', '2026-11-09'),
      it('OpenAI Codex', '.codex\\sessions', U + '\\.codex\\sessions', 1.66, '会话历史与归档，迁移后原路径照常可用。', 'userData', 'migrate', 'active', '2026-11-08'),
      it('OpenAI Codex', '.cache\\codex-runtimes', U + '\\.cache\\codex-runtimes', 1.35, 'Codex 下载的运行时，可重新下载。', 'cache', 'clean', 'active', '2026-10-30'),
      it('Claude Desktop', 'Roaming\\Claude', U + '\\AppData\\Roaming\\Claude', 1.08, '桌面客户端数据与会话。', 'userData', 'migrate', 'active', '2026-11-11'),
      it('Cursor', '.cursor', U + '\\.cursor', 2.3, '项目索引、插件与模型缓存。', 'userData', 'migrate', 'locked', '2026-11-11'),
      it('LM Studio', '.lmstudio\\models', U + '\\.lmstudio\\models', 4.2, '本地模型文件，请确认是否还在使用。', 'review', 'none', 'active', '2026-09-14'),
      it('Ollama', '.ollama\\models', U + '\\.ollama\\models', 3.1, '本地模型文件，请确认是否还在使用。', 'review', 'none', 'active', '2026-08-02'),
      it('Claude Code', '.claude\\projects', U + '\\.claude\\projects', 0.58, '会话记录与项目历史。', 'userData', 'migrate', 'active', '2026-11-11'),
      it('Grok', '.grok', U + '\\.grok', 0.43, '本地数据。', 'userData', 'migrate', 'active', '2026-10-21'),
      it('系统', 'Local\\CrashDumps', U + '\\AppData\\Local\\CrashDumps', 0.61, '崩溃转储文件，一般用不到。', 'cache', 'clean', 'active', '2026-10-02'),
      it('Antigravity', 'Local\\antigravity-updater', U + '\\AppData\\Local\\antigravity-updater', 0.32, '更新器缓存的安装包。', 'cache', 'clean', 'active', '2026-10-18'),
      it('Gradle', '.gradle\\caches', U + '\\.gradle\\caches', 0.29, 'Gradle 依赖缓存，构建时会重新下载。', 'cache', 'clean', 'active', '2026-07-30'),
      it('npm', 'npm-cache', U + '\\AppData\\Local\\npm-cache', 0.21, 'npm 包缓存。', 'cache', 'clean', 'active', '2026-11-01'),
      it('Kimi Code', '.kimi', U + '\\.kimi', 0.12, '本地数据。', 'userData', 'migrate', 'active', '2026-10-11'),
      it('Windsurf', '.codeium\\windsurf', U + '\\.codeium\\windsurf', 0.66, '索引与缓存。', 'userData', 'migrate', 'locked', '2026-11-11'),
      it('pip', 'pip\\cache', U + '\\AppData\\Local\\pip\\cache', 0.07, 'pip 下载缓存。', 'cache', 'clean', 'active', '2026-09-03'),
      { ruleId: 'recycle-bin', app: '系统', dir: '$Recycle.Bin', path: 'C:\\$Recycle.Bin', size: Math.round(2.1 * GB), files: 812, note: '回收站。删除后无法恢复。', risk: 'cache', riskTxt: '低风险', action: 'clean', state: 'active', statusTxt: '', special: 'recycleBin', lastMod: '2026-11-11' },
      { ruleId: 'win-update-dl', app: '系统', dir: 'SoftwareDistribution\\Download', path: 'C:\\Windows\\SoftwareDistribution\\Download', size: Math.round(4.6 * GB), files: 2400, note: 'Windows Update 下载缓存。', risk: 'cache', riskTxt: '低风险', action: 'clean', state: 'locked', statusTxt: '需要管理员', needsAdmin: true, special: 'winUpdate', lastMod: '2026-11-10' },
      { ruleId: 'documents', app: '系统', dir: 'Documents', path: U + '\\Documents', size: Math.round(8.4 * GB), files: 5200, note: '文档目录，工具只统计占用不处理，里面的具体内容会单独列出。', risk: 'review', riskTxt: '用户数据', action: 'review', state: 'active', statusTxt: '', reportOnly: true, lastMod: '2026-11-11' },
    ],
    quar: [], logs: [], seq: 1,
  };
  const now = () => { const d = new Date(), p = n => String(n).padStart(2, '0'); return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`; };
  const log = (op, path, size, ok, detail) => db.logs.unshift({ t: now(), op, path, size, ok, detail });
  if (location.hash.includes('seed')) {          // 调试：预先放几条残留和日志，方便看残留/记录页
    [1, 4, 6].forEach((i, j) => { const x = db.items.splice(i - j, 1)[0]; db.quar.push({ id: 'qar_seed' + j + '_' + x.path.split(/[/\\]/).pop(), origPath: x.path, app: x.app, size: x.size, files: x.files, quarAt: ['2026-11-10 21:14:03', '2026-10-28 09:30:00', '2026-10-09 18:02:41'][j], retainDays: 30, note: x.note, _item: x }); db.logs.push({ t: '2026-11-10 21:14:0' + j, op: 'quarantine', path: x.path, size: x.size, ok: true, detail: '→ quarantine' }); });
    db.logs.push({ t: '2026-11-09 10:02:11', op: 'migrate', path: U + '\\.gemini', size: 0.8 * GB, ok: false, detail: 'junction 创建失败: Access is denied.' }, { t: '2026-11-08 16:40:00', op: 'restore', path: U + '\\.cache\\huggingface', size: 2.2 * GB, ok: true, detail: '' }, { t: '2026-11-08 16:31:20', op: 'purge', path: 'qar_20261101_101500_Temp', size: 1.4 * GB, ok: true, detail: '' });
  }
  const fail = Number((location.search.match(/fail=(\d)/) || [])[1] || 0);
  window.getState = async () => ({ version: '0.3.0-demo', elevated: false, drives: [
    { letter: 'C', free: db.free, total: db.total, fs: 'NTFS', fixed: true },
    { letter: 'D', free: db.dfree, total: 731.2 * GB, fs: 'NTFS', fixed: true },
    { letter: 'E', free: 53.2 * GB, total: 130.6 * GB, fs: 'exFAT', fixed: true },
  ] });
  window.scanItems = async () => {
    await sleep(location.hash.includes('fast') ? 10 : 2400);
    if (fail === 1) return { items: [], error: 'rules.json 未找到' };
    if (fail === 2) return { items: [], count: 0 };
    return { items: db.items.map(x => ({ ...x })).sort((a, b) => b.size - a.size), rulesPath: 'C:\\AIClean\\rules.json', count: db.items.length };
  };
  window.cleanItem = async a => {
    await sleep(160);
    const i = db.items.findIndex(x => x.path === a.path);
    if (i < 0) return { ok: false, error: '路径不存在' };
    const x = db.items.splice(i, 1)[0];
    const id = 'qar_' + (db.seq++) + '_' + x.path.split('\\').pop();
    db.quar.unshift({ id, origPath: x.path, app: x.app, size: x.size, files: x.files, quarAt: now(), retainDays: 30, note: x.note, _item: x });
    log('quarantine', x.path, x.size, true, '→ quarantine\\' + id);
    return { ok: true, id, size: x.size };
  };
  window.quarList = async () => ({ items: db.quar.length ? db.quar.map(({ _item, ...m }) => m) : null });
  window.restoreItem = async id => {
    await sleep(160);
    const i = db.quar.findIndex(q => q.id === id); if (i < 0) return { ok: false, error: '元数据缺失: ' + id };
    const q = db.quar.splice(i, 1)[0]; db.items.push(q._item); log('restore', q.origPath, q.size, true, ''); return { ok: true };
  };
  window.purgeItem = async id => {
    await sleep(160);
    const i = db.quar.findIndex(q => q.id === id); if (i < 0) return { ok: false, error: '不存在' };
    const q = db.quar.splice(i, 1)[0]; db.free += q.size; log('purge', id, q.size, true, ''); return { ok: true };
  };
  window.getLogs = async () => ({ items: db.logs.length ? db.logs.slice(0, 200) : null });
  window.deleteItem = async a => {
    await sleep(180);
    const i = db.items.findIndex(x => x.path === a.path);
    if (i < 0) return { ok: false, error: '该路径不在最近一次扫描结果中，拒绝操作' };
    const x = db.items.splice(i, 1)[0];
    db.free += x.size; log('purge', x.path, x.size, true, '');
    return { ok: true, size: x.size };
  };
  window.pruneItem = async a => {
    await sleep(220);
    const x = db.items.find(i => i.path === a.path);
    if (!x) return { ok: false, error: '该路径不在最近一次扫描结果中，拒绝操作' };
    const days = a.days || x.pruneDays || 7, bytes = Math.round(x.size * 0.62), files = Math.round(x.files * 0.6);
    x.size -= bytes; x.files -= files; db.free += bytes;
    log('prune', x.path, bytes, true, days + 'd: ' + files + ' 文件');
    return { ok: true, bytes, files, days };
  };
  window.migPreflight = async a => {
    await sleep(120);
    const x = db.items.find(i => i.path === a.path), ntfs = a.drive === 'D';
    return { dst: a.drive + ':\\AIClean-Moved\\' + (x ? x.ruleId : 'misc') + '\\' + a.path.split('\\').pop(), checks: [
      { name: '源目录存在', ok: !!x }, { name: '源不是 junction/symlink', ok: true },
      { name: `目标盘可用 ${(db.dfree / 1e9).toFixed(1)} GB ≥ 所需 ${((x ? x.size : 0) / 1e9).toFixed(1)} GB`, ok: true },
      { name: '目标盘为 NTFS', ok: ntfs, msg: '仅 NTFS 支持目录联结' }, { name: '目标位置未被占用', ok: true }] };
  };
  window.migrateItem = async a => {
    await sleep(500);
    const x = db.items.find(i => i.path === a.path); if (!x) return { ok: false, error: '参数无效' };
    const dst = a.drive + ':\\AIClean-Moved\\' + x.ruleId + '\\' + a.path.split('\\').pop(), size = x.size;
    const bak = a.path + '.aiclean-bak-20261111223000';
    x.state = 'migrated'; x.size = 0; x.files = 0; x.movedTo = dst; db.dfree -= size;
    db.migs = db.migs || []; db.migs.push({ src: a.path, dst, bak, size, at: now(), done: false });
    log('migrate', a.path, size, true, '→ ' + dst);
    return { ok: true, dst, bak, size };
  };
  window.listMigrations = async () => ({ items: (db.migs || []).map(m => ({ ...m })) });
  window.finishMigration = async a => {
    await sleep(160);
    const m = (db.migs || []).find(x => x.bak === a.bak || x.src === a.path);
    if (!m) return { ok: false, error: '没有该路径的迁移记录' };
    m.done = true; db.free += m.size; log('migrate', m.src, m.size, true, '已清理备份');
    return { ok: true, freed: m.size };
  };
  window.rollbackMigration = async a => {
    await sleep(400);
    const i = (db.migs || []).findIndex(x => x.src === a.path);
    if (i < 0) return { ok: false, error: '没有该路径的迁移记录' };
    const m = db.migs.splice(i, 1)[0];
    const x = db.items.find(it => it.path === m.src);
    if (x) { x.state = 'active'; x.size = m.size; delete x.movedTo; }
    db.dfree += m.size; log('migrate', m.src, m.size, true, '已迁回原位');
    return { ok: true };
  };
  window.getPrefs = async () => db.prefs || {};
  window.setPrefs = async a => { db.prefs = Object.assign(db.prefs || {}, a); return { ok: true }; };
  window.openPath = async () => ({ ok: true });
  window.dataDir = async () => ({ path: U + '\\AppData\\Local\\AIClean' });
  window.openDataDir = async () => ({ ok: true });
  window.openUrl = async () => ({ ok: true });
  
  window.cancelScan = async () => ({ ok: true });
  window.autoCleanNow = async () => ({ ok: true, freed: 1.2 * GB, count: 4 });
  window.schedSet = async a => { db.sched = a.freq === 'off' ? null : a; return { ok: true, on: a.freq !== 'off' }; };
  window.schedStatus = async () => db.sched ? { on: true, next: '2026/11/12 ' + (db.sched.time || '03:00') + ':00', status: '准备就绪' } : { on: false };
  window.autostartGet = async () => ({ on: !!db.boot });
  window.autostartSet = async a => { db.boot = !!a.on; return { ok: true, on: db.boot }; };
  window.relaunchElevated = async () => ({ ok: false, error: '浏览器演示模式不支持提权' });
  window.findBigFiles = async () => {
    await sleep(1200);
    const u = U + '\\';
    return { items: [
      { ruleId: 'bigfile', app: '大文件', dir: 'Downloads', path: u + 'Downloads\\vmware-workstation-full-17.5.2.exe', size: 1.62 * GB, files: 1, lastMod: '2025-09-18', risk: 'review', riskTxt: '超大文件', action: 'clean', state: 'active', statusTxt: '超大文件', big: true, note: '' },
      { ruleId: 'bigfile', app: '大文件', dir: 'Videos', path: u + 'Videos\\旅行-2024-原片.mp4', size: 4.31 * GB, files: 1, lastMod: '2025-06-02', risk: 'review', riskTxt: '超大文件', action: 'clean', state: 'active', statusTxt: '超大文件', big: true, note: '' },
      { ruleId: 'bigfile', app: '大文件', dir: 'Desktop', path: u + 'Desktop\\备份-2024-12.zip', size: 2.05 * GB, files: 1, lastMod: '2024-12-30', risk: 'review', riskTxt: '超大文件', action: 'clean', state: 'active', statusTxt: '超大文件', big: true, dup: true, note: '' },
      { ruleId: 'bigfile', app: '大文件', dir: 'Downloads', path: u + 'Downloads\\备份-2024-12.zip', size: 2.05 * GB, files: 1, lastMod: '2024-12-30', risk: 'review', riskTxt: '超大文件', action: 'clean', state: 'active', statusTxt: '超大文件', big: true, note: '' },
    ], count: 4 };
  };
  window.ignoreItem = async a => {
    db.prefs = db.prefs || {};
    db.prefs.ignoreRules = (db.prefs.ignoreRules || []).concat(a.ruleId || '');
    db.prefs.ignorePaths = (db.prefs.ignorePaths || []).concat(a.path || '');
    return { ok: true };
  };
  window.unignoreAll = async () => { db.prefs = db.prefs || {}; db.prefs.ignoreRules = []; db.prefs.ignorePaths = []; return { ok: true }; };
  window.winMin = window.winMax = window.winClose = window.winDrag = window.winBorder = async () => ({ ok: true });
}

/* ---------- 提示条 ---------- */
let toastT = 0;
function toast(msg, action, fn) {
  const t = $('#toast');
  t.innerHTML = `<span>${esc(msg)}</span>` + (action ? `<a>${esc(action)}</a>` : '');
  if (action) $('a', t).onclick = () => { t.classList.remove('show'); fn && fn(); };
  t.classList.add('show');
  clearTimeout(toastT);
  toastT = setTimeout(() => t.classList.remove('show'), action ? 6000 : 3000);
}

/* ---------- 按住确认：按住 ms 毫秒才触发，松手即取消（破坏性操作的统一确认方式） ---------- */
function makeHold(btn, fn, ms) {
  ms = ms || 1000;
  let pips = $('.pips', btn);
  if (pips && !pips.children.length) pips.innerHTML = '<i></i>'.repeat(10);
  const dots = pips ? [...pips.children] : [];
  let t0 = 0, raf = 0;
  const set = p => { btn.style.setProperty('--p', p); dots.forEach((d, i) => d.classList.toggle('f', i < Math.round(p * 10))); };
  /* 用定时器而不是 requestAnimationFrame：窗口被遮挡、动画帧被限速时，按住确认也必须照常完成 */
  const stop = () => { clearInterval(raf); raf = 0; set(0); };
  const tick = () => {
    const p = Math.min(1, (performance.now() - t0) / ms); set(p);
    if (p >= 1) { clearInterval(raf); raf = 0; setTimeout(() => set(0), 250); fn(); }
  };
  const start = e => {
    if (btn.disabled || raf) return;
    if (btn.classList.contains('plain')) return;            // 普通点击模式由 click 处理
    e.preventDefault(); t0 = performance.now(); raf = setInterval(tick, 33);
  };
  btn.addEventListener('pointerdown', start);
  ['pointerup', 'pointerleave', 'pointercancel', 'blur'].forEach(ev => btn.addEventListener(ev, () => raf && stop()));
  btn.addEventListener('keydown', e => { if ((e.key === ' ' || e.key === 'Enter') && !e.repeat) start(e); });
  btn.addEventListener('keyup', e => { if (e.key === ' ' || e.key === 'Enter') raf && stop(); });
  btn.addEventListener('click', () => { if (btn.classList.contains('plain') && !btn.disabled) fn(); });
}

/* ---------- 弹窗 ---------- */
const Dlg = {
  root: null, onEsc: null,
  open(html, opts) {
    this.root = $('#dlg-root');
    this.root.innerHTML = `<div class="dlg" role="dialog" aria-modal="true">${html}</div>`;
    this.root.classList.add('show');
    this.locked = false; this.onEsc = (opts && opts.onEsc) || (() => this.close());
    const f = $('.dlg [autofocus], .dlg .btn, .dlg .hold', this.root); f && f.focus();
    return $('.dlg', this.root);
  },
  close() { if (!this.root) return; this.root.classList.remove('show'); this.root.innerHTML = ''; this.onEsc = null; },
};
document.addEventListener('keydown', e => { if (e.key === 'Escape' && Dlg.onEsc && !Dlg.locked) Dlg.onEsc(); });
document.addEventListener('pointerdown', e => { if (e.target.id === 'dlg-root' && Dlg.onEsc && !Dlg.locked) Dlg.onEsc(); });

/* 确认弹窗：返回 Promise<boolean>。hold=true 时主按钮需要按住 */
function confirmDlg({ title, sub, body, okText, cancelText, hold, danger }) {
  return new Promise(res => {
    const d = Dlg.open(`<h3>${esc(title)}</h3>${sub ? `<div class="sub">${sub}</div>` : ''}<div class="body">${body || ''}</div>
      <div class="foot"><button class="btn" data-x>${esc(cancelText || '取消')}</button>
      <button class="hold ${hold ? '' : 'plain'} ${danger ? 'danger' : ''}" data-ok><span class="holdt">${esc(okText || '确定')}</span><span class="pips"></span></button></div>`,
      { onEsc: () => { Dlg.close(); res(false); } });
    $('[data-x]', d).onclick = () => { Dlg.close(); res(false); };
    makeHold($('[data-ok]', d), () => { Dlg.close(); res(true); }, 900);
  });
}
function infoDlg({ title, sub, body, okText }) {
  return new Promise(res => {
    const done = () => { Dlg.close(); res(); };
    const d = Dlg.open(`<h3>${esc(title)}</h3>${sub ? `<div class="sub">${sub}</div>` : ''}<div class="body">${body || ''}</div>
      <div class="foot"><button class="btn pri" data-ok autofocus>${esc(okText || '知道了')}</button></div>`, { onEsc: done });
    $('[data-ok]', d).onclick = done;
  });
}

/* ---------- 主题 / 动效偏好（exe 里 localStorage 存不住 → 走后端 prefs.json；浏览器下用 localStorage） ---------- */
const THEMES = [
  { id: 'fog', name: '雾白', desc: '默认，最护眼', pv: ['#ECEEEA', '#D3D8D3', '#1638E0', '#141A1C'] },
  { id: 'ikb', name: '克莱因蓝', desc: '满版蓝 + 荧光黄绿', pv: ['#0B2FC2', 'rgba(255,255,255,.2)', '#D9FF3F', '#F5F7FF'] },
  { id: 'ink', name: '夜墨', desc: '深色，适合晚上', pv: ['#121516', '#2B3132', '#5B7CFF', '#E8ECEA'] },
];
const Prefs = {
  _mem: {},
  /* 启动时先等后端 prefs 合进来再读主题；localStorage 只是本地缓存 */
  ready: (async () => {
    try {
      const p = await go('getPrefs');
      if (p && typeof p === 'object') Object.assign(Prefs._mem, p);
    } catch (e) { /* 没有后端就用本地 */ }
  })(),
  get(k, d) {
    if (k in Prefs._mem) return Prefs._mem[k];
    try { const v = localStorage.getItem('aic.' + k); return v == null ? d : v; } catch (e) { return d; }
  },
  set(k, v) {
    Prefs._mem[k] = v;
    try { localStorage.setItem('aic.' + k, v); } catch (e) { /* exe 里没有 localStorage，无妨 */ }
    if (LIVE) go('setPrefs', { [k]: v });   // 后台持久化，不等结果
    return true;
  },
};
function setTheme(id, save) {
  const th = THEMES.find(t => t.id === id) || THEMES[0];
  document.documentElement.dataset.theme = th.id;
  if (LIVE) go('winBorder', { rgb: th.pv[0] });   // 窗口 1px 边框同步主题底色
  if (save) Prefs.set('theme', th.id);
}
function setMotion(mode, save) {
  document.documentElement.classList.toggle('calm', mode === 'calm');
  if (save) Prefs.set('motion', mode);
}

/* ---------- 路由 ---------- */
const PAGES = ['home', 'data', 'quar', 'logs', 'settings', 'about'];
const RENDER = {};                       // 各页的渲染函数，由后面的文件注册
const ALIAS = { items: 'data', mig: 'data', report: 'home', sched: 'home' };  // 兼容旧版 exe 启动参数
function goPage(p, arg) {
  p = ALIAS[p] || p; if (!PAGES.includes(p)) p = 'home';
  ST.page = p;
  $$('.page').forEach(el => el.classList.toggle('on', el.id === 'p-' + p));
  $$('#nav a').forEach(a => a.classList.toggle('on', a.dataset.go === p));
  $('#btn-settings').classList.toggle('on', p === 'settings');
  RENDER[p] && RENDER[p](arg);
}
document.addEventListener('click', e => {
  const a = e.target.closest('[data-go]');
  if (a && !a.disabled) goPage(a.dataset.go, a.dataset.arg);
});

/* ---------- 无边框窗口控制：拖拽区 + 最小化/最大化/关闭（仅真 exe 生效） ----------
   顶栏 [data-drag] 的矩形 + 内部控件排除区上报给原生窗口，命中测试据此报
   HTCAPTION —— 系统原生拖动/双击最大化，UI 线程完成，不经过异步桥（旧的
   winDrag 桥跨线程 ReleaseCapture 不生效，已废弃只留兜底）。 */
function pushDrag() {
  const bar = document.querySelector('header.top[data-drag]');
  if (!bar || !LIVE) return;
  const dpr = window.devicePixelRatio || 1;
  const b = bar.getBoundingClientRect();
  const exc = [];
  bar.querySelectorAll('a,button,input,select,textarea,[data-nodrag]').forEach(el => {
    const r = el.getBoundingClientRect();
    if (r.width > 0 && r.height > 0) exc.push([r.left * dpr, r.top * dpr, r.right * dpr, r.bottom * dpr]);
  });
  go('setDrag', { l: b.left * dpr, t: b.top * dpr, r: b.right * dpr, b: b.bottom * dpr, exc });
}
if (LIVE) {
  window.addEventListener('resize', () => requestAnimationFrame(pushDrag));
  document.addEventListener('DOMContentLoaded', () => {
    pushDrag();
    const bar = document.querySelector('header.top[data-drag]');
    if (bar && typeof MutationObserver === 'function') {
      const mo = new MutationObserver(() => requestAnimationFrame(pushDrag));
      mo.observe(bar, { subtree: true, attributes: true, childList: true, characterData: true });
    }
  });
  document.addEventListener('pointerdown', e => {
    if (e.button !== 0) return;
    const z = e.target.closest('[data-drag]');
    if (!z || e.target.closest('button,a,input,select,textarea,[data-nodrag]')) return;
    /* 移动超过阈值才算拖拽——否则最大化状态下单击顶栏就会误触发还原 */
    const x0 = e.clientX, y0 = e.clientY;
    let started = false;
    const mv = ev => {
      if (started) return;
      if (Math.abs(ev.clientX - x0) + Math.abs(ev.clientY - y0) > 6) { started = true; go('winDrag'); }
    };
    const up = () => { removeEventListener('pointermove', mv); removeEventListener('pointerup', up); removeEventListener('pointercancel', up); };
    addEventListener('pointermove', mv);
    addEventListener('pointerup', up);
    addEventListener('pointercancel', up);
  });
  document.addEventListener('dblclick', e => {
    const z = e.target.closest('[data-drag]');
    if (z && !e.target.closest('button,a,input,select,textarea,[data-nodrag]')) go('winMax');
  });
} else {
  const wc = document.getElementById('wcap'); if (wc) wc.style.display = 'none';   // 浏览器调试时隐藏
}
const WB_MAX_SVG = '<svg viewBox="0 0 24 24"><rect x="6" y="6" width="12" height="12"/></svg>';
const WB_RST_SVG = '<svg viewBox="0 0 24 24"><rect x="5" y="9" width="10" height="10"/><path d="M9 9V5h10v10h-4"/></svg>';
document.addEventListener('DOMContentLoaded', () => {
  const b = (id, fn) => { const el = document.getElementById(id); if (el) el.onclick = () => go(fn); };
  b('wb-min', 'winMin'); b('wb-close', 'winClose');
  const mx = document.getElementById('wb-max');
  if (mx) mx.onclick = async () => {
    const r = await go('winMax');
    if (r && r.ok) { mx.innerHTML = r.max ? WB_RST_SVG : WB_MAX_SVG; mx.title = r.max ? '还原' : '最大化'; }
  };
});
