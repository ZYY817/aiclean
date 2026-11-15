/* ===== 10-model · 数据整理：分类、汇总、格子分配、加载与批量操作 ===== */
const KINDS = {
  clean: { name: '可清理', desc: '缓存，删了会自动重建' },
  mig:   { name: '建议迁移', desc: '搬到其他盘，照常使用' },
  rev:   { name: '待确认', desc: '需要你看一眼' },
  stat:  { name: '仅统计', desc: '只汇总占用，本身不可操作' },
  lock:  { name: '正在使用', desc: '关掉程序后可处理' },
  quar:  { name: '清理残留', desc: '上次没删干净，还占着 C 盘' },
  big:   { name: '大文件', desc: '单个就占 300MB+ 的散落文件' },
  moved: { name: '已迁移', desc: '已搬到其他盘' },
  sys:   { name: '系统和其他文件', desc: '' },
  free:  { name: '可用空间', desc: '' },
};
/* 规则写明“只删 N 天前的旧文件”的目录（如系统 Temp）：整目录删除是危险的，只能按龄清理。
   后端现在给这些项标了 prunable + pruneDays（olderThanDays 规则也折算进来）。 */
const needsPrune = it => it.prunable === true || /只删|天前的|天以前/.test(it.note || '');
/* 后端字段 → 界面上的五种状态 */
function kindOf(it) {
  if (it.big) return 'big';
  if (it.reportOnly) return 'stat';          // 汇总统计目录：只展示占用，不进可清理/一键清理
  if (it.state === 'migrated') return 'moved';
  if (it.state === 'locked') return 'lock';
  if (it.special) return 'rev';              // 回收站/休眠/更新缓存：只许逐项确认，不进一键清理
  if (it.action === 'clean') return needsPrune(it) ? 'rev' : 'clean';
  if (it.action === 'migrate') return 'mig';
  return 'rev';
}
const sum = (arr, f) => arr.reduce((a, x) => a + (f ? f(x) : x), 0);
const byKind = k => ST.items.filter(it => kindOf(it) === k);

/* 首页要用的全部数字 */
function summary() {
  const c = ST.c || { total: 0, free: 0 };
  const used = Math.max(0, c.total - c.free);
  const s = { total: c.total, free: c.free, used, pct: c.total ? used / c.total * 100 : 0, k: {} };
  ['clean', 'mig', 'rev', 'stat', 'lock', 'moved'].forEach(k => { const a = byKind(k); s.k[k] = { n: a.length, size: sum(a, x => x.size) }; });
  // 已迁移项自身 size=0（源位置只剩链接），体积按迁移账本记账
  s.k.moved.size = sum(byKind('moved'), x => (ST.migs && ST.migs[x.path] && ST.migs[x.path].size) || 0);
  s.k.quar = { n: ST.quar.length, size: sum(ST.quar, q => q.size) };
  s.ai = s.k.clean.size + s.k.mig.size + s.k.rev.size + s.k.stat.size + s.k.lock.size;
  s.count = s.k.clean.n + s.k.mig.n + s.k.rev.n + s.k.lock.n;
  /* 数据页「全部」里的总条数（含已迁移/仅统计/大文件）——导航徽标也用同一口径 */
  s.listAll = s.count + s.k.moved.n + s.k.stat.n + ST.bigfiles.length;
  return s;
}

/* 选一个好读的"每格大小"，让整盘落在 ~120–216 格之间 */
function pickUnit(totalBytes) {
  const g = totalBytes / GB;
  for (const u of [0.25, 0.5, 1, 1.5, 2, 2.5, 3, 4, 5, 8, 10, 15, 20, 25, 40, 50, 100]) if (g / u <= 216) return u;
  return Math.ceil(g / 216 / 50) * 50;
}

/* 把整盘分成一串"连续段"：每段 = {kind, label, sizeTxt, n(格数), app} */
function buildRuns() {
  const s = summary();
  if (!s.total) return { runs: [], unit: 1, cells: 0 };
  const unit = pickUnit(s.total), ub = unit * GB;
  const N = Math.max(1, Math.round(s.total / ub));
  const want = b => b <= 0 ? 0 : (b < ub * 0.12 ? 0 : Math.max(1, Math.round(b / ub)));
  let freeN = Math.min(N, Math.round(s.free / ub));
  const kn = { clean: want(s.k.clean.size), mig: want(s.k.mig.size), rev: want(s.k.rev.size), stat: want(s.k.stat.size), lock: want(s.k.lock.size), quar: want(s.k.quar.size) };
  let aiN = sum(Object.values(kn));
  while (aiN + freeN > N) {                      // 极端情况兜底：格子不够就从最大的那类里扣
    const big = Object.keys(kn).sort((a, b) => kn[b] - kn[a])[0];
    if (kn[big] > 1) kn[big]--; else if (freeN > 0) freeN--; else break;
    aiN = sum(Object.values(kn));
  }
  const runs = [];
  const sysN = Math.max(0, N - aiN - freeN);
  if (sysN) runs.push({ kind: 'sys', label: KINDS.sys.name, sizeTxt: fmt(Math.max(0, s.used - s.ai - s.k.quar.size)), n: sysN });
  const appRuns = (kind, list, cells) => {       // 同一状态内按应用分段，零头并成"其余 N 项"
    if (!cells) return;
    const m = new Map();
    list.forEach(x => { const a = m.get(x.app) || { app: x.app, size: 0, n: 0 }; a.size += x.size; a.n++; m.set(x.app, a); });
    const apps = [...m.values()].sort((a, b) => b.size - a.size);
    let left = cells, restSize = 0, restN = 0;
    apps.forEach(a => {
      const c = Math.min(left, Math.floor(a.size / ub));
      if (c > 0) { runs.push({ kind, label: a.app, app: a.app, sizeTxt: fmt(a.size), n: c }); left -= c; }
      else { restSize += a.size; restN += a.n; }
    });
    if (left > 0) {
      if (restN > 0) runs.push({ kind, label: `其余 ${restN} 项`, sizeTxt: fmt(restSize), n: left });
      else { const last = runs[runs.length - 1]; if (last && last.kind === kind) last.n += left; else runs.push({ kind, label: KINDS[kind].name, sizeTxt: '', n: left }); }
    }
  };
  /* 清理残留（上次没删干净的）排在可清理前面 */
  appRuns('quar', ST.quar.map(q => ({ app: q.app || baseName(q.origPath), size: q.size })), kn.quar);
  ['clean', 'mig', 'rev', 'stat', 'lock'].forEach(k => appRuns(k, byKind(k), kn[k]));
  if (freeN) runs.push({ kind: 'free', label: KINDS.free.name, sizeTxt: fmt(s.free), n: freeN });
  return { runs, unit, cells: sum(runs, r => r.n) };
}
const baseName = p => String(p || '').split(/[\\/]/).filter(Boolean).pop() || '';

/* 可作为迁移目标的磁盘：固定盘、NTFS、不是 C */
function targetDrives() {
  return ST.drives.filter(d => d.letter !== 'C' && d.fixed !== false && (!d.fs || d.fs.toUpperCase() === 'NTFS')).sort((a, b) => b.free - a.free);
}

/* ---------- 加载 ---------- */
async function loadState() {
  const r = await go('getState');
  if (r && Array.isArray(r.drives)) {
    ST.drives = r.drives; ST.version = r.version || '';
    ST.elevated = !!r.elevated;
    ST.pinnedMissing = r.pinnedMissing || ''; // 指向盘未连接 → 顶部提示
    ST.c = r.drives.find(d => d.letter === 'C') || r.drives[0] || null;
  }
  return r;
}
async function loadQuar() { const r = await go('quarList'); ST.quar = Array.isArray(r.items) ? r.items : []; }
/* 迁移账本：源路径 → {dst,bak,size,at}，只含备份还在源盘的记录 */
async function loadMigs() {
  const r = await go('listMigrations');
  ST.migs = {};
  (Array.isArray(r.items) ? r.items : []).forEach(m => { if (m && m.src) ST.migs[m.src] = m; });
}
async function loadLogs() { const r = await go('getLogs'); ST.logs = Array.isArray(r.items) ? r.items : []; return r; }

/* 后端进度事件：扫描进度 / 迁移复制进度（异步桥推送）。
   __aicLastProg 供 go() 的闲置超时判定：有进度就不算挂死。 */
window.__aicProg = (name, p) => {
  window.__aicLastProg = Date.now();
  if (name === 'scan' && ST.phase === 'scanning') {
    const t = $('#h-when'); if (t) t.textContent = `扫描中 ${p.i} / ${p.n}`;
  } else if (name === 'migrate' && typeof window.__aicMigCb === 'function') {
    window.__aicMigCb(p);
  }
};
/* 计划任务→界面代执行完毕：静默刷新状态（界面闲着才动） */
window.__aicAutoDone = () => {
  if (!ST.op && ST.phase !== 'scanning') { refreshLight().then(() => { RENDER[ST.page] && RENDER[ST.page](); }); toast('计划任务已自动清理一次'); }
};
/* 完整扫描。后端异步执行，窗口全程不卡；进度经 __aicProg 实时回来 */
async function doScan() {
  if (ST.phase === 'scanning') return;
  ST.phase = 'scanning'; ST.error = ''; ST.stale = false;
  await loadState();
  refreshChrome(); RENDER[ST.page] && RENDER[ST.page]();
  await paint();
  const t0 = performance.now();
  await loadMigs();
  const r = await go('scanItems');
  const wait = 900 - (performance.now() - t0);          // 扫得太快也让动画至少完整走一遍，避免闪一下
  if (wait > 0 && !calm()) await sleep(wait);
  if (r.error && !(Array.isArray(r.items) && r.items.length)) {
    ST.phase = 'error'; ST.error = r.error; ST.items = [];
  } else {
    if (r.cancelled) toast(`已取消，显示扫到的 ${r.count || 0} 项`);
    ST.items = (Array.isArray(r.items) ? r.items : []).filter(x => x && x.path);
    ST.rulesPath = r.rulesPath || ''; ST.phase = 'ready'; ST.scannedAt = new Date();
  }
  await loadQuar(); await loadState();
  refreshChrome(); RENDER[ST.page] && RENDER[ST.page]();
}

/* 轻量刷新：操作之后只重读磁盘和残留/迁移账本，不重新全盘扫描（全盘扫描很慢） */
async function refreshLight() { await loadState(); await loadQuar(); await loadMigs(); refreshChrome(); }

/* 顶栏上跟数据有关的小东西 */
function refreshChrome() {
  const s = summary();
  $('#nc-data').textContent = ST.phase === 'ready' && s.listAll ? s.listAll : '';
  const b = $('#nc-quar'); b.textContent = ST.quar.length; b.classList.toggle('show', ST.quar.length > 0);
  $('#nav-quar').style.display = ST.quar.length || ST.page === 'quar' ? '' : 'none';   // 没有残留时不显示这个入口
  const t = targetDrives()[0], chip = $('#chip-drive');
  chip.classList.toggle('show', !!t);
  if (t) chip.innerHTML = `<i></i>${esc(t.letter)} 盘 <span class="n" id="chip-free">${gb1(t.free)}</span> GB 可用`;
  const rs = $('#btn-rescan');
  rs.classList.toggle('busy', ST.phase === 'scanning'); rs.classList.toggle('stale', ST.stale && ST.phase !== 'scanning');
  renderTicker();
}
function renderTicker() {
  const run = $('#tick-run'), m = new Map();
  ST.items.forEach(x => { const k = kindOf(x); if (k === 'moved') return; const a = m.get(x.app) || { app: x.app, size: 0, kind: k }; a.size += x.size; m.set(x.app, a); });
  const top = [...m.values()].sort((a, b) => b.size - a.size).slice(0, 10);
  if (!top.length) {
    run.classList.add('still');
    run.innerHTML = `<span><b>${ST.phase === 'scanning' ? '正在扫描，稍等一下…' : ST.phase === 'error' ? '扫描没有完成' : '暂无数据'}</b></span>`;
    return;
  }
  const one = top.map((t, i) => `<span>${i + 1}　${esc(t.app)}　<em class="n">${fmt(t.size)}</em>　<b>${KINDS[t.kind].name}</b></span><span style="opacity:.35">/</span>`).join('');
  run.classList.remove('still');
  run.innerHTML = one + one;
  run.style.animationDuration = Math.max(30, top.length * 7) + 's';
}

/* 清理 = 一步永久删除（deleteItem）。路径由后端对照最近扫描结果二次校验，
   prunable 项后端也会拒绝整删。失败时目录留在原位，不会半截状态。 */
async function cleanItems(list, onStep) {
  const out = { ok: [], bad: [], resid: 0 };
  for (let i = 0; i < list.length; i++) {
    const it = list[i];
    onStep && onStep(i, list.length, it); await paint();
    const r = await go('deleteItem', { path: it.path, app: it.app });
    if (r.ok) {
      ST.items = ST.items.filter(x => x.path !== it.path); ST.bigfiles = ST.bigfiles.filter(x => x.path !== it.path);
      out.ok.push(it);
      if (r.residual) out.resid++; // 部分文件被占用，残余进了「清理残留」
    }
    else out.bad.push({ item: it, error: r.error || '未知错误' });
  }
  await refreshLight();
  return out;
}

/* 按龄清理：只删 N 天前的旧文件（系统 Temp 等），保留新文件 */
async function pruneItems(list, onStep) {
  const out = { ok: [], bad: [] };
  for (let i = 0; i < list.length; i++) {
    const it = list[i];
    onStep && onStep(i, list.length, it); await paint();
    const r = await go('pruneItem', { path: it.path });
    if (r.ok) {
      it.size = Math.max(0, it.size - (r.bytes || 0));
      it.files = Math.max(0, it.files - (r.files || 0));
      it._freed = r.bytes || 0;
      out.ok.push(it);
    } else out.bad.push({ item: it, error: r.error || '未知错误' });
  }
  await refreshLight();
  return out;
}
/* 失败原因翻译成人话（后端返回的是英文系统错误） */
function humanErr(e) {
  e = String(e || '');
  if (/used by another process|being used|Access is denied|拒绝访问|另一个程序/i.test(e)) return '没有权限，或文件正被其他程序占用。请先关闭对应的应用再试';
  if (/不是目录/.test(e)) return '这是单个文件，目前只支持清理文件夹';
  if (/reparse|symlink/i.test(e)) return '这是一个链接（可能已经迁移过），不能清理';
  if (/原路径已存在/.test(e)) return '原位置已经有同名文件夹，为避免覆盖没有放回。请先处理原位置的文件夹';
  if (/no such file|cannot find|找不到/i.test(e)) return '文件夹已经不存在了，可能已被应用自己清理';
  if (/后端接口不可用/.test(e)) return e;
  return e.length > 120 ? e.slice(0, 120) + '…' : e;
}
function failList(bad) {
  return `<div class="list">${bad.map(b => `<div class="li bad"><span class="ic">${CROSS}</span><div><b>${esc(b.item.app || baseName(b.item.origPath))}</b><small class="mono">${esc(b.item.path || b.item.origPath || '')}</small><small>${esc(humanErr(b.error))}</small></div><span class="r n">${fmt(b.item.size)}</span></div>`).join('')}</div>`;
}
