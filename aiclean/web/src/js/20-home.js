/* ===== 20-home · 总览页：大数字、结论、格子阵、按住整理 ===== */
const Home = { cells: [], prev: null, cols: 18, lastSay: '', lastPct: null, note: null, busy: false, centers: null };

/* 格子阵排版：在可用区域里挑一个让格子最大的列数 */
function layoutGrid(n) {
  const box = $('#h-gridbox'), w = box.clientWidth, h = box.clientHeight;
  if (!n || w < 50 || h < 50) return;
  let best = { cs: 0, cols: 18, gap: 5 };
  for (let cols = 10; cols <= 36; cols++) {
    const rows = Math.ceil(n / cols);
    for (const gap of [5, 4, 3, 2]) {
      const cs = Math.floor(Math.min((w - gap * (cols - 1)) / cols, (h - gap * (rows - 1)) / rows));
      if (cs >= gap * 4 && cs > best.cs) best = { cs, cols, gap };
    }
  }
  if (!best.cs) best = { cs: 8, cols: Math.max(10, Math.floor(w / 10)), gap: 2 };
  best.cs = Math.min(best.cs, 44);
  const g = $('#grid');
  g.style.setProperty('--cs', best.cs + 'px'); g.style.setProperty('--cols', best.cols); g.style.setProperty('--gap', best.gap + 'px');
  Home.cols = best.cols; Home.centers = null;
}

/* 目标状态 → 每个格子的 {kind, run}。扫描中：已用部分统一显示"扫描波" */
function targetCells() {
  if (ST.phase !== 'ready') {
    const c = ST.c; if (!c || !c.total) return { list: [], unit: 1 };
    const unit = pickUnit(c.total), N = Math.max(1, Math.round(c.total / (unit * GB))), freeN = Math.min(N, Math.round(c.free / (unit * GB)));
    const scanning = ST.phase === 'scanning' || ST.phase === 'boot';
    const used = { kind: scanning ? 'scan' : 'sys', label: scanning ? '正在扫描…' : '已用空间', sizeTxt: fmt(c.total - c.free) };
    const free = { kind: 'free', label: KINDS.free.name, sizeTxt: fmt(c.free) };
    return { list: Array.from({ length: N }, (_, i) => i < N - freeN ? used : free), unit };
  }
  const { runs, unit } = buildRuns(), list = [];
  runs.forEach(r => { for (let i = 0; i < r.n; i++) list.push(r); });
  return { list, unit };
}

function renderGrid() {
  const g = $('#grid'), { list, unit } = targetCells(), n = list.length;
  const prev = Home.cells, same = prev.length === n && g.children.length === n;
  layoutGrid(n);
  if (!same) {
    /* 首次或格数变化：整体重建 + 波浪入场 */
    const cols = Home.cols;
    g.innerHTML = list.map((r, i) => `<span class="cell c-${r.kind}" style="animation-delay:${(i % cols) * 12 + Math.floor(i / cols) * 30}ms"></span>`).join('');
    g.classList.add('enter'); clearTimeout(Home.enterT);
    Home.enterT = setTimeout(() => { g.classList.remove('enter'); $$('.cell', g).forEach(c => c.style.animationDelay = ''); }, 1600);
  } else {
    /* 原地更新：只动变化了的格子，并给出"去哪了"的动效 */
    const els = g.children, cols = Home.cols, changed = [];
    for (let i = 0; i < n; i++) if (prev[i].kind !== list[i].kind) changed.push(i);
    const still = calm() || changed.length > 400;
    changed.forEach((i, j) => {
      const el = els[i], from = prev[i].kind, to = list[i].kind;
      const delay = still ? 0 : (from === 'scan' ? (i % cols) * 10 + Math.floor(i / cols) * 24 : Math.min(j * 45, 1400));
      setTimeout(() => {
        el.className = 'cell c-' + to;
        if (!still && to === 'free' && from !== 'scan') { el.classList.add('fresh'); setTimeout(() => el.classList.remove('fresh'), 1150); }
      }, delay);
    });
  }
  Home.cells = list;
  const cName = (ST.c && ST.c.letter || 'C') + ' 盘';
  $('#h-title').textContent = n ? `${cName}的 ${n} 个格子` : cName;
  $('#h-unit').textContent = n ? `1 格 ≈ ${unit} GB` + (ST.phase === 'ready' ? '，鼠标移上去看是谁' : '') : '';
}

/* 一个小方块从格子飞到目标元素（备用：以后有"迁移去 D 盘"之类的去向动画时使用） */
function flyCell(el, target, color) {
  const a = el.getBoundingClientRect(), b = target.getBoundingClientRect();
  const f = document.createElement('div'); f.className = 'fly';
  f.style.cssText = `left:${a.left}px;top:${a.top}px;width:${a.width}px;height:${a.height}px;background:${color}`;
  document.body.appendChild(f);
  const dx = b.left + b.width / 2 - a.left - a.width / 2, dy = b.top + b.height / 2 - a.top - a.height / 2;
  const an = f.animate([
    { transform: 'translate(0,0) scale(1)', opacity: 1 },
    { transform: `translate(${dx * .35}px,${dy * .9 - 30}px) scale(.8) rotate(40deg)`, opacity: 1, offset: .55 },
    { transform: `translate(${dx}px,${dy}px) scale(.25) rotate(90deg)`, opacity: .2 }], { duration: 700, easing: 'cubic-bezier(.5,0,.3,1)' });
  an.onfinish = an.oncancel = () => { f.remove(); const bd = $('#nc-quar'); bd.classList.remove('bump'); void bd.offsetWidth; bd.classList.add('bump'); };
}

/* 数字滚动 */
function rollPct(to) {
  const el = $('#h-pct'), from = Home.lastPct;
  Home.lastPct = to; cancelAnimationFrame(Home.rollRaf);
  if (from == null || calm() || Math.round(from) === Math.round(to)) { el.textContent = Math.round(to); return; }
  const t0 = performance.now(), ms = Math.min(1400, 500 + Math.abs(to - from) * 30);
  const f = t => { const k = Math.min(1, (t - t0) / ms), e = 1 - Math.pow(1 - k, 3); el.textContent = Math.round(from + (to - from) * e); if (k < 1) Home.rollRaf = requestAnimationFrame(f); };
  Home.rollRaf = requestAnimationFrame(f);
}

/* 左侧三四行结论。只有内容变了才重新播放上滑动画 */
function say(lines) {
  const key = lines.join('|'); if (key === Home.lastSay) return; Home.lastSay = key;
  $('#h-say').innerHTML = lines.filter(Boolean).map((l, i) => `<span class="line"><span style="animation-delay:${i * 90 + 80}ms">${l}</span></span>`).join('');
}
const hl = b => `<span class="hl n">${fmt(b)}</span>`;
const num = b => `<span class="n">${fmt(b)}</span>`;

function homeMode() {
  if (Home.busy) return 'busy';
  if (ST.phase === 'boot' || ST.phase === 'scanning') return 'scanning';
  if (ST.phase === 'error') return 'error';
  const s = summary();
  if (s.k.clean.n > 0) return 'clean';
  if (s.k.quar.n > 0) return 'purge';
  return 'idle';
}

RENDER.home = function () {
  const s = summary(), c = ST.c, mode = homeMode();
  /* 数据目录所在盘未连接 → 顶部警示条（指向文件仍在，盘回来自动恢复） */
  let pinbar = $('#h-pinbar');
  if (ST.pinnedMissing) {
    if (!pinbar) {
      pinbar = document.createElement('div');
      pinbar.id = 'h-pinbar';
      pinbar.className = 'callout warn';
      pinbar.style.margin = '0 0 12px';
      $('#p-home').prepend(pinbar);
    }
    pinbar.innerHTML = `数据目录所在的盘未连接：<b class="mono">${esc(ST.pinnedMissing)}</b> —— 迁移记录和残留暂时不可用，本次使用临时目录。盘重新连接后自动恢复。`;
  } else if (pinbar) pinbar.remove();
  $('#h-drive').textContent = c ? `${c.letter} 盘 · ${c.fs || '本地磁盘'}` : '本地磁盘';
  $('#h-when').textContent = ST.phase === 'scanning' ? '正在扫描' : ST.scannedAt ? '扫描于 ' + ST.scannedAt.toTimeString().slice(0, 5) : '';
  if (c && c.total) { rollPct(s.pct); $('#h-used').textContent = '已用 ' + fmt(s.used); $('#h-total').textContent = '共 ' + fmt(s.total); }
  else { $('#h-pct').textContent = '–'; $('#h-used').textContent = ''; $('#h-total').textContent = ''; }

  /* 结论文案 */
  const freeLine = !c ? '' : s.pct >= 85 ? `${c.letter} 盘只剩 ${num(s.free)}。` : `${c.letter} 盘还有 ${num(s.free)} 可用。`;
  const note = Home.note;
  if (mode === 'scanning') say([`正在扫描 ${c ? c.letter : 'C'} 盘…`, '逐个检查各类应用和系统的占用数据，', '通常要几秒到一两分钟。', '<span class="g">扫描期间可以照常拖动窗口、切页面。</span>']);
  else if (mode === 'error') say(['没能完成扫描。', `<span class="g err">${esc(/rules\.json/.test(ST.error) ? '找不到规则文件 rules.json，请确认它和程序放在同一个文件夹里。' : humanErr(ST.error))}</span>`]);
  else if (mode === 'busy') { /* 进行中的文案由操作流程自己写 */ }
  else if (note && note.type === 'purged') say([`已释放 ${hl(note.size)}。`, freeLine, note.bad ? `<span class="g">另有 ${note.bad} 项没有成功，可以关闭相关应用后再试。</span>` : s.k.mig.n ? `<span class="g">还有 ${fmt(s.k.mig.size)} 用户数据可以<a data-go="data" data-arg="mig">迁到其他盘</a>。</span>` : '']);
  else if (mode === 'clean') say([freeLine, `已识别 ${num(s.ai)} 占用数据，`, `其中 ${hl(s.k.clean.size)} 是缓存，可以放心清走。`,
    `<span class="g">${s.k.mig.n ? `另有 ${fmt(s.k.mig.size)} 用户数据建议<a data-go="data" data-arg="mig">迁到其他盘</a>。` : '删除后，应用会在需要时自动重建这些缓存。'}</span>`]);
  else if (mode === 'purge') say([freeLine, `有 ${hl(s.k.quar.size)} 上次没删干净，`, '它们现在还占着 C 盘。', '<span class="g">关闭相关应用后，按住下面的按钮再删一次。</span>']);
  else if (s.count > 0) say([freeLine, `已识别 ${num(s.ai)} 占用数据，`, '没有可以直接清理的缓存了。',
    `<span class="g">${s.k.mig.n ? `还有 ${fmt(s.k.mig.size)} 用户数据可以<a data-go="data" data-arg="mig">迁到其他盘</a>。` : '剩下的需要你逐项确认，或关闭正在运行的程序后再扫描。'}</span>`]);
  else say([freeLine, '没有发现可以管理的数据。', '<span class="g">安装新应用或系统产生新数据后，可以再来扫描一次。</span>']);

  /* 主按钮 */
  const btn = $('#h-hold'), t = $('#h-holdt');
  btn.classList.remove('danger');
  if (mode === 'scanning') { btn.disabled = false; btn.classList.add('plain'); t.innerHTML = '取消扫描'; }
  else if (mode === 'busy') { btn.disabled = true; btn.classList.add('plain'); }
  else if (mode === 'error') { btn.disabled = false; btn.classList.add('plain'); t.innerHTML = '重试扫描'; }
  else if (mode === 'clean') { btn.disabled = false; btn.classList.remove('plain'); t.innerHTML = `按住，开始清理<small class="n">${fmt(s.k.clean.size)}</small>`; }
  else if (mode === 'purge') { btn.disabled = false; btn.classList.remove('plain'); t.innerHTML = `按住，删除残留<small class="n">${fmt(s.k.quar.size)}</small>`; }
  else { btn.disabled = false; btn.classList.add('plain'); t.innerHTML = '重新扫描'; }
  $('#h-ghost').disabled = ST.phase !== 'ready' || !s.count;

  /* 图例 */
  const rows = ['clean', 'mig', 'rev', 'lock']; if (s.k.stat.n) rows.push('stat'); if (s.k.quar.n) rows.push('quar');
  $('#h-key').innerHTML = rows.map(k => `<div class="k ${s.k[k].n ? '' : 'zero'}" data-k="${k}"><span class="sq cell c-${k}"></span><span class="nm">${KINDS[k].name}<span class="d">${KINDS[k].desc}</span></span><span class="c n">${ST.phase === 'ready' ? s.k[k].n + ' 项' : '–'}</span><span class="v n">${ST.phase === 'ready' ? fmt(s.k[k].size) : '–'}</span></div>`).join('');

  renderGrid();
};

/* ---------- 交互：透镜、提示、图例高亮 ---------- */
(function () {
  const g = $('#grid'), tip = $('#tip'), R = $('#h-r');
  const lit = pred => { g.classList.add('dim'); const els = g.children; Home.cells.forEach((r, i) => els[i] && els[i].classList.toggle('lit', pred(r))); };
  const unlit = () => { g.classList.remove('dim'); };
  let raf = 0, last = null, lastRun = null;
  const frame = () => {
    raf = 0; const e = last; if (!e) return;
    const els = g.children;
    if (!calm() && els.length <= 600) {
      if (!Home.centers) Home.centers = [...els].map(c => { const b = c.getBoundingClientRect(); return [b.left + b.width / 2, b.top + b.height / 2]; });
      const cs = Home.centers, rad = 85;
      for (let i = 0; i < cs.length; i++) {
        const dx = cs[i][0] - e.clientX, dy = cs[i][1] - e.clientY;
        if (dx > rad || dx < -rad || dy > rad || dy < -rad) { if (els[i]._s) { els[i].style.removeProperty('--s'); els[i]._s = 0; } continue; }
        const d = Math.hypot(dx, dy), sc = d < rad ? 1 + .22 * (1 - d / rad) : 1;
        if (sc > 1) { els[i].style.setProperty('--s', sc.toFixed(3)); els[i]._s = 1; } else if (els[i]._s) { els[i].style.removeProperty('--s'); els[i]._s = 0; }
      }
    }
    const c = e.target.closest ? e.target.closest('.cell') : null;
    if (!c || c.parentNode !== g) { tip.style.opacity = 0; return; }
    const i = [].indexOf.call(els, c), r = Home.cells[i]; if (!r) return;
    if (r !== lastRun) { lastRun = r; lit(x => x === r); tip.innerHTML = `${esc(r.label)}<small>${[r.sizeTxt, r.kind === 'sys' || r.kind === 'free' || r.kind === 'scan' ? '' : KINDS[r.kind].name].filter(Boolean).join(' · ')}</small>`; }
    const b = R.getBoundingClientRect();
    tip.style.left = Math.max(4, Math.min(e.clientX - b.left + 14, b.width - tip.offsetWidth - 8)) + 'px';
    tip.style.top = Math.min(e.clientY - b.top + 18, b.height - tip.offsetHeight - 6) + 'px'; tip.style.opacity = 1;
  };
  g.addEventListener('mousemove', e => { last = e; if (!raf) raf = requestAnimationFrame(frame); });
  g.addEventListener('mouseleave', () => { last = null; lastRun = null; unlit(); tip.style.opacity = 0; [...g.children].forEach(c => { if (c._s) { c.style.removeProperty('--s'); c._s = 0; } }); });
  g.addEventListener('click', e => {
    const c = e.target.closest('.cell'); if (!c || ST.phase !== 'ready') return;
    const r = Home.cells[[].indexOf.call(g.children, c)]; if (!r) return;
    if (r.kind === 'quar') goPage('quar'); else if (KINDS[r.kind] && !['sys', 'free'].includes(r.kind)) goPage('data', r.kind + (r.app ? '|' + r.app : ''));
  });
  const key = $('#h-key');
  key.addEventListener('mouseover', e => { const k = e.target.closest('.k'); if (k) lit(r => r.kind === k.dataset.k); });
  key.addEventListener('mouseleave', unlit);
  key.addEventListener('click', e => { const k = e.target.closest('.k'); if (!k || ST.phase !== 'ready') return; k.dataset.k === 'quar' ? goPage('quar') : goPage('data', k.dataset.k); });
  let rz = 0;
  new ResizeObserver(() => { clearTimeout(rz); rz = setTimeout(() => { if (ST.page === 'home') layoutGrid(Home.cells.length); }, 80); }).observe($('#h-gridbox'));

  /* 主按钮：同一个按钮，按当前状态分派。
     按下瞬间锁定意图：扫描恰好在按下到触发之间完成时，不会因为 mode 已变成
     'clean' 而误执行一键清理（取消扫描变永久删除）。 */
  const hbtn = $('#h-hold');
  let pressMode = '';
  hbtn.addEventListener('pointerdown', () => { pressMode = homeMode(); });
  makeHold(hbtn, async () => {
    const mode = pressMode || homeMode(); pressMode = '';
    if (mode === 'scanning') return go('cancelScan');
    if (mode === 'error' || mode === 'idle') return doScan();
    if (mode === 'clean') return homeClean();
    if (mode === 'purge') return homePurge();
  }, 1000);
})();

/* 一键：直接删除所有"可清理"（按住即确认） */
async function homeClean() {
  const list = byKind('clean'); if (!list.length) return;
  if (ST.op) return toast('有操作正在进行，请稍候');
  ST.op = 'clean'; Home.busy = true; Home.note = null; RENDER.home();
  try {
    const t = $('#h-holdt');
    const res = await cleanItems(list, (i, n, it) => { t.innerHTML = `正在清理<small class="n">${i + 1} / ${n} · ${esc(it.app)}</small>`; });
    Home.note = { type: 'purged', size: sum(res.ok, x => x.size), n: res.ok.length, bad: res.bad.length };
    if (!res.ok.length) Home.note = null;
    if (res.bad.length) infoDlg({ title: `${res.bad.length} 项没有成功`, sub: res.ok.length ? `其余 ${res.ok.length} 项已清理。` : '', body: failList(res.bad) });
    else if (res.resid) toast(`已清理 ${res.ok.length} 项，其中 ${res.resid} 项有残留已移入「清理残留」`, '查看', () => goPage('quar'));
  } finally { ST.op = null; Home.busy = false; RENDER.home(); }
}

/* 一键：把上次没删干净的残留再删一次 */
async function homePurge() {
  if (ST.op) return toast('有操作正在进行，请稍候');
  ST.op = 'purge'; Home.busy = true;
  try {
    const res = await purgeItems(ST.quar.slice(), (i, n) => { $('#h-holdt').innerHTML = `正在删除残留<small class="n">${i + 1} / ${n}</small>`; }, true);
    Home.note = res.ok.length ? { type: 'purged', size: sum(res.ok, x => x.size) } : null;
    if (res.bad.length) infoDlg({ title: `${res.bad.length} 项没有删除成功`, body: failList(res.bad.map(b => ({ item: { app: b.item.app, origPath: b.item.origPath, size: b.item.size }, error: b.error }))) });
  } finally { ST.op = null; Home.busy = false; RENDER.home(); }
}
