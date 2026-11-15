/* ===== 30-data · 数据页：筛选 / 搜索 / 勾选 / 清理 / 迁移 ===== */
const Data = { filter: 'all', q: '', sort: 'size', dir: -1, sel: new Set(), open: '', built: false };
const WHY = {
  clean: '这是缓存，删除后应用会在需要时自动重建，可以放心清理。',
  mig: '这是你的数据，不建议删除。迁移会把它搬到其他磁盘，并在原位置留一个链接，应用照常使用。',
  rev: '用途不确定，需要你自己判断。清理是永久删除、无法恢复，拿不准就先不要动。',
  lock: '对应的程序正在运行，现在动它可能损坏数据。关闭程序后重新扫描，就可以处理了。',
  moved: '已经迁到其他磁盘，原位置是一个链接，不再占用 C 盘空间。',
};
function copyText(s) {
  const done = () => toast('路径已复制');
  try { if (navigator.clipboard && window.isSecureContext) return navigator.clipboard.writeText(s).then(done, fallback); } catch (e) {}
  fallback();
  function fallback() { const t = document.createElement('textarea'); t.value = s; t.style.cssText = 'position:fixed;opacity:0'; document.body.appendChild(t); t.select(); let ok = false; try { ok = document.execCommand('copy'); } catch (e) {} t.remove(); ok ? done() : toast('复制失败，请手动选中路径复制'); }
}

RENDER.data = function (arg) {
  if (arg) { const [k, app] = String(arg).split('|'); Data.filter = KINDS[k] ? k : 'all'; Data.q = app || ''; Data.open = ''; }
  const el = $('#p-data');
  if (!Data.built) {
    el.innerHTML = `<div class="pg">
      <div class="pg-h"><div><h1>数据</h1><p id="d-sub"></p></div><div class="big n" id="d-big"></div></div>
      <div class="bar" id="d-bar"></div>
      <div class="scroll" id="d-scroll"></div>
      <div class="batch" id="d-batch"></div></div>`;
    Data.built = true;
    el.addEventListener('click', onDataClick);
    el.addEventListener('input', e => { if (e.target.id === 'd-q') { Data.q = e.target.value; drawRows(); } });
  }
  drawData();
};

function visibleItems() {
  const q = Data.q.trim().toLowerCase();
  const pool = Data.filter === 'all' ? ST.items.concat(ST.bigfiles) : Data.filter === 'big' ? ST.bigfiles : ST.items;
  let a = pool.filter(it => (Data.filter === 'all' || kindOf(it) === Data.filter) &&
    (!q || (it.app + ' ' + it.path + ' ' + (it.note || '')).toLowerCase().includes(q)));
  a.sort(Data.sort === 'app' ? (x, y) => Data.dir * x.app.localeCompare(y.app, 'zh') || y.size - x.size : (x, y) => Data.dir * (x.size - y.size));
  return a;
}
const findIt = p => ST.items.find(x => x.path === p) || ST.bigfiles.find(x => x.path === p);
/* 批量勾选准入：特殊项（回收站/休眠）和超大文件只放行"行内单独确认"，
   不进批量——避免它们藏在"还有 N 项"的折叠里被一次确认带走 */
const selectable = it => { const k = kindOf(it); return !it.reportOnly && !it.needsAdmin && !needsPrune(it) && !it.special && !it.big && (k === 'clean' || k === 'mig' || k === 'rev'); };

function drawData() {
  const s = summary();
  /* 已不存在的勾选项清掉 */
  const alive = new Set(ST.items.concat(ST.bigfiles).filter(selectable).map(x => x.path));
  [...Data.sel].forEach(p => alive.has(p) || Data.sel.delete(p));
  const bigN = ST.bigfiles.length;
  $('#d-sub').textContent = ST.phase === 'scanning' ? '正在扫描…' : ST.phase === 'error' ? '扫描没有完成' :
    `扫描到 ${s.count} 项可处理数据` + (s.k.moved.n ? `，另有 ${s.k.moved.n} 项已迁移` : '') + (s.k.stat.n ? `，${s.k.stat.n} 项仅统计占用` : '') + (bigN ? `，大文件猎手找到 ${bigN} 个超大文件` : '');
  $('#d-big').innerHTML = ST.phase === 'ready' ? `${fmt(s.ai)}<small>已识别的 C 盘占用</small>` : '';
  const chips = [['all', '全部', s.listAll], ['clean', KINDS.clean.name, s.k.clean.n], ['mig', KINDS.mig.name, s.k.mig.n], ['rev', KINDS.rev.name, s.k.rev.n], ['stat', KINDS.stat.name, s.k.stat.n], ['lock', KINDS.lock.name, s.k.lock.n]];
  if (bigN) chips.push(['big', KINDS.big.name, bigN]);
  if (s.k.moved.n) chips.push(['moved', KINDS.moved.name, s.k.moved.n]);
  if (!chips.some(c => c[0] === Data.filter)) Data.filter = 'all';
  const q = $('#d-q'), hadFocus = q && document.activeElement === q;
  $('#d-bar').innerHTML = chips.map(c => `<button class="fchip ${Data.filter === c[0] ? 'on' : ''}" data-f="${c[0]}">${c[0] === 'all' || c[0] === 'moved' ? '' : `<span class="sq cell c-${c[0]}"></span>`}${c[1]}<small class="n">${c[2]}</small></button>`).join('') +
    `<span class="sp"></span><button class="btn sm" data-a="findbig">${bigN ? '重新查找大文件' : '查找大文件'}</button><input class="search" id="d-q" placeholder="搜索应用或路径" spellcheck="false" value="${esc(Data.q)}">`;
  if (hadFocus) { const n = $('#d-q'); n.focus(); n.setSelectionRange(n.value.length, n.value.length); }
  drawRows();
}

function rowHTML(it, max) {
  const k = kindOf(it), on = Data.sel.has(it.path), open = Data.open === it.path;
  const mv = k === 'moved' && ST.migs ? ST.migs[it.path] : null;
  /* 数据盘未连接：迁移/迁回/删备份全部置灰（后端同样拦截） */
  const pinDis = ST.pinnedMissing ? ' disabled title="数据目录所在盘未连接，暂不可进行迁移操作"' : '';
  const act = k === 'moved' ? `<button class="btn sm" data-a="rb"${pinDis}>迁回</button>` : k === 'lock' || it.needsAdmin || it.state === 'locked' ? `<span style="font-size:12.5px;color:var(--ink3)">${it.needsAdmin ? '需管理员' : '使用中'}</span>` : it.reportOnly ? (it.noMigrate ? '' : `<button class="btn sm" data-a="m"${pinDis}>搬到其他盘</button>`) : needsPrune(it) ? `<button class="btn sm" data-a="pr">清理旧文件</button>` : k === 'clean' || k === 'rev' || k === 'big' ? `<button class="btn sm" data-a="q">清理</button>` : k === 'mig' ? `<button class="btn sm" data-a="m"${pinDis}>迁移</button>` : `<span style="font-size:12.5px;color:var(--ink3)">已迁移</span>`;
  let h = `<div class="tr ${open ? 'open' : ''}" data-p="${esc(it.path)}">
    <button class="ck ${on ? 'on' : ''}" data-a="ck" ${selectable(it) ? '' : 'disabled'} aria-label="选择">${CHECK}</button>
    <span class="sq cell c-${k === 'moved' ? 'free' : k}"></span>
    <span class="app">${esc(it.app)}<small>${esc(it.dir || baseName(it.path))}</small></span>
    <span class="note">${it.dup ? `<b style="color:var(--warn)">可能重复</b> · ` : ''}${esc(it.note || (it.big ? '大文件猎手找到的单个超大文件' : ''))}</span>
    <span class="mod n">${esc(it.lastMod || '–')}</span>
    <span class="size n">${k === 'moved' ? (mv && mv.size ? fmt(mv.size) + '<small style="font-weight:400">→' + (it.movedTo && it.movedTo[0] || '?') + '盘</small>' : it.movedTo ? `<small style="font-weight:400">已迁到 ${esc(it.movedTo[0])} 盘</small>` : '–') : fmt(it.size)}<span class="mini"><i style="width:${max ? Math.max(1.5, it.size / max * 100) : 0}%"></i></span></span>
    <span class="act">${act}</span></div>`;
  if (open) h += `<div class="det"><dl>
      <dt>位置</dt><dd class="path sel">${esc(it.path)}</dd>
      <dt>说明</dt><dd>${esc(it.note || '没有额外说明')}</dd>
      <dt>内容</dt><dd class="n">${k === 'moved' ? `已迁移，原位置是链接${mv && mv.size ? `；数据 ${fmt(mv.size)} 在 ${esc(mv.dst || it.movedTo || '目标盘')}` : ''}` : `${(it.files || 0).toLocaleString()} 个文件，${fmt(it.size)}` + (it.skipped ? `（跳过 ${it.skipped} 个链接）` : '')}</dd>
      ${k === 'moved' && it.movedTo ? `<dt>迁移到</dt><dd class="path sel">${esc(it.movedTo)}</dd>` : ''}
      <dt>最近修改</dt><dd class="n">${esc(it.lastMod || '未知')}</dd>
      <dt>风险</dt><dd>${esc(it.riskTxt || '')}</dd></dl>
    ${k === 'moved' && mv && !mv.done ? `<div class="callout warn" style="margin:0 14px 10px">原位置的备份还留着（占 ${fmt(mv.size || 0)}）。确认软件运行正常后可以删除。<div style="margin-top:6px"><button class="btn sm dang" data-a="fb"${pinDis}>删除备份</button></div></div>` : ''}
    <div class="why">${it.reportOnly ? `这是汇总统计目录——让你看到这个大目录占了多少空间，本身不可清理。里面的具体内容会作为单独项目列出来。想把它整体搬到其他盘腾 C 盘空间，可以点行尾的「搬到其他盘」——数据搬过去、原位置留链接，软件照常使用，还能迁回来。` : it.needsAdmin ? `这项在系统目录里，需要管理员权限才能处理。<b>以管理员身份重新启动 AIClean</b> 后即可清理。<div style="margin-top:10px"><button class="btn pri sm" data-a="elev">以管理员身份运行</button></div>` : needsPrune(it) ? `这个文件夹只应该清掉旧文件，不能整个删除。用「清理旧文件」只删 ${it.pruneDays || 7} 天前的内容${it.prunableSize ? `，预计释放 ${fmt(it.prunableSize)}` : ''}，新文件不受影响。` : k === 'big' ? `${it.dup ? '<b>这个文件和另一个文件的尺寸、内容指纹一致，很可能是重复副本。</b><br>' : ''}这是大文件猎手找到的单个超大文件，不属于任何已知规则。建议先「打开位置」确认它是什么（安装包、视频、虚拟机镜像、旧备份……），确认不需要了再删。删除不能恢复。` : k === 'lock' && it.lockedBy && it.lockedBy.length ? `对应的程序（${it.lockedBy.map(esc).join('、')}）正在运行，现在动它可能损坏数据。关闭后重新扫描即可处理。` : WHY[k]}${it.needsAdmin ? '' : `<div style="margin-top:10px"><button class="btn sm" data-a="copy">复制路径</button><button class="btn sm" data-a="open" style="margin-left:8px">打开位置</button>${k === 'big' ? '' : `<button class="btn sm" data-a="ig" style="margin-left:8px">不再提示</button>`}</div>`}</div></div>`;
  return h;
}

function drawRows() {
  const box = $('#d-scroll'), list = visibleItems(), st = box.scrollTop;
  if (ST.phase !== 'ready') {
    box.innerHTML = `<div class="empty"><div class="eg">${'<i></i>'.repeat(9)}</div><h3>${ST.phase === 'error' ? '扫描没有完成' : '正在扫描…'}</h3><p>${ST.phase === 'error' ? esc(humanErr(ST.error)) : '扫描完成后，这里会列出每一项数据。'}</p>${ST.phase === 'error' ? '<button class="btn pri" data-a="rescan">重试扫描</button>' : ''}</div>`;
    drawBatch(); return;
  }
  if (!list.length) {
    const any = ST.items.length > 0;
    box.innerHTML = `<div class="empty"><div class="eg">${'<i></i>'.repeat(9)}</div><h3>${any ? '没有符合条件的项目' : '没有发现可以管理的数据'}</h3><p>${any ? '换个筛选条件，或清空搜索框试试。' : '安装新应用或系统产生新数据后，可以再来扫描一次。'}</p>${any ? '<button class="btn" data-a="reset">清除筛选</button>' : ''}</div>`;
    drawBatch(); return;
  }
  const max = Math.max(...list.map(x => x.size), 1);
  const ar = Data.sort === 'size' ? (Data.dir < 0 ? ' ↓' : ' ↑') : '', an = Data.sort === 'app' ? (Data.dir < 0 ? ' ↓' : ' ↑') : '';
  const pickable = list.filter(selectable), allOn = pickable.length > 0 && pickable.every(x => Data.sel.has(x.path));
  let h = `<div class="thead"><button class="ck ${allOn ? 'on' : ''}" data-a="all" ${pickable.length ? '' : 'disabled'} aria-label="全选">${CHECK}</button><span></span><span class="srt" data-a="sort-app">应用${an}</span><span>说明</span><span>最近修改</span><span class="srt" data-a="sort-size" style="text-align:right">大小${ar}</span><span></span></div>`;
  if (Data.filter === 'all' && Data.sort === 'size') {
    ['clean', 'mig', 'rev', 'stat', 'big', 'lock', 'moved'].forEach(k => {
      const g = list.filter(x => kindOf(x) === k); if (!g.length) return;
      const gsz = k === 'moved' ? sum(g, x => (ST.migs && ST.migs[x.path] && ST.migs[x.path].size) || 0) : sum(g, x => x.size);
      h += `<div class="grp">${KINDS[k].name}<small class="n">${g.length} 项 · ${fmt(gsz)}${k === 'moved' ? ' 已迁出 C 盘' : ''} — ${KINDS[k].desc}</small></div>` + g.map(x => rowHTML(x, max)).join('');
    });
  } else h += list.map(x => rowHTML(x, max)).join('');
  box.innerHTML = h + '<div class="pg-foot"></div>';
  box.scrollTop = st;
  drawBatch();
}

function drawBatch() {
  const b = $('#d-batch'), pool = ST.items.concat(ST.bigfiles), sel = pool.filter(x => Data.sel.has(x.path));
  const q = sel.filter(x => kindOf(x) !== 'mig'), m = sel.filter(x => kindOf(x) === 'mig');
  b.classList.toggle('show', sel.length > 0);
  if (!sel.length) return;
  b.innerHTML = `<span>已选 <b class="n">${sel.length}</b> 项，共 <b class="n">${fmt(sum(sel, x => x.size))}</b></span><span class="sp"></span>
    <button class="btn" data-a="none">取消选择</button>
    ${m.length ? `<button class="btn" data-a="bm"${ST.pinnedMissing ? ' disabled title="数据目录所在盘未连接，暂不可迁移"' : ''}>迁移 ${m.length} 项</button>` : ''}
    ${q.length ? `<button class="btn pri" data-a="bq">清理 ${q.length} 项</button>` : ''}`;
}

async function onDataClick(e) {
  const f = e.target.closest('[data-f]');
  if (f) { Data.filter = f.dataset.f; Data.open = ''; Data.sel.clear(); drawData(); return; } // 切筛选清空勾选：批量条上的已选项必须看得见
  const a = e.target.closest('[data-a]'), tr = e.target.closest('.tr'), det = e.target.closest('.det');
  const item = p => findIt(p);
  if (a) {
    const act = a.dataset.a, it = tr ? item(tr.dataset.p) : det ? item(Data.open) : null;
    if (act === 'ck' && it) { Data.sel.has(it.path) ? Data.sel.delete(it.path) : Data.sel.add(it.path); a.classList.toggle('on'); drawBatch(); syncAll(); }
    else if (act === 'all') { const p = visibleItems().filter(selectable), on = p.every(x => Data.sel.has(x.path)); p.forEach(x => on ? Data.sel.delete(x.path) : Data.sel.add(x.path)); drawRows(); }
    else if (act === 'none') { Data.sel.clear(); drawRows(); }
    else if (act === 'sort-size' || act === 'sort-app') { const k = act.slice(5); if (Data.sort === k) Data.dir *= -1; else { Data.sort = k; Data.dir = k === 'size' ? -1 : 1; } drawRows(); }
    else if (act === 'reset') { Data.filter = 'all'; Data.q = ''; drawData(); }
    else if (act === 'rescan') doScan();
    else if (act === 'copy' && it) copyText(it.path);
    else if (act === 'q' && it) quarantineFlow([it]);
    else if (act === 'pr' && it) pruneFlow([it]);
    else if (act === 'open' && it) go('openPath', { path: kindOf(it) === 'moved' && it.movedTo ? it.movedTo : it.path });
    else if (act === 'rb' && it) rollbackFlow(it);
    else if (act === 'fb' && it) finishBakFlow(it);
    else if (act === 'findbig') runFindBig();
    else if (act === 'ig' && it) ignoreFlow(it);
    else if (act === 'elev') {
      const r = await go('relaunchElevated');
      if (!r.ok) toast(humanErr(r.error || '提权失败'));
      // 提权成功时本窗口会自动退出，UAC 后开管理员实例
    }
    else if (act === 'm' && it) migrateFlow([it]);
    else if (act === 'bq') quarantineFlow(ST.items.concat(ST.bigfiles).filter(x => Data.sel.has(x.path) && kindOf(x) !== 'mig'));
    else if (act === 'bm') migrateFlow(ST.items.filter(x => Data.sel.has(x.path) && kindOf(x) === 'mig'));
    return;
  }
  if (tr && !window.getSelection().toString()) { Data.open = Data.open === tr.dataset.p ? '' : tr.dataset.p; drawRows(); }
}
function syncAll() {
  const all = $('#d-scroll [data-a="all"]'); if (!all) return;
  const p = visibleItems().filter(selectable); all.classList.toggle('on', p.length > 0 && p.every(x => Data.sel.has(x.path)));
}

/* ---------- 清理流程（永久删除）：按住确认 → 逐个执行 → 结果 ---------- */
async function quarantineFlow(list) {
  list = list.filter(x => ['clean', 'rev', 'big'].includes(kindOf(x)) && !needsPrune(x)); if (!list.length) return;
  const risky = list.filter(x => kindOf(x) === 'rev' || kindOf(x) === 'big'), total = sum(list, x => x.size);
  const names = list.slice(0, 6).map(x => `<div class="li wait"><span class="ic"></span><div><b>${esc(x.app)}</b><small class="mono">${esc(x.path)}</small></div><span class="r n">${fmt(x.size)}</span></div>`).join('') + (list.length > 6 ? `<div class="li wait"><span class="ic"></span><div>还有 ${list.length - 6} 项</div><span class="r"></span></div>` : '');
  const ok = await confirmDlg({
    title: `永久删除这 ${list.length} 项？`,
    sub: `共 ${fmt(total)}。删除后立刻腾出 C 盘空间，但无法恢复。`,
    body: (risky.length ? `<div class="callout warn">其中 ${risky.length} 项是“待确认”数据，可能是你还在用的东西（比如模型文件、聊天记录）。删除后找不回来，请确认不需要了再继续。</div>` : '<div class="callout">这些都是缓存，应用下次需要时会自动重建。</div>') + `<div class="list">${names}</div>`,
    okText: '按住，永久删除', hold: true, danger: risky.length > 0,
  });
  if (!ok) return;
  if (ST.op) return toast('有操作正在进行，请稍候');
  ST.op = 'clean';
  let res;
  try {
    const d = Dlg.open(`<h3>正在清理</h3><div class="sub" id="qf-sub">准备中…</div><div class="body"></div><div class="foot"></div>`, { onEsc() {} });
    Dlg.locked = true;
    try {
      res = await cleanItems(list, (i, n, it) => { const s = $('#qf-sub', d); if (s) s.textContent = `${i + 1} / ${n} · ${it.app}（大文件夹可能要等一会儿）`; });
    } finally { Dlg.locked = false; Dlg.close(); }
  } finally { ST.op = null; }
  if (!res) return;
  res.ok.forEach(x => Data.sel.delete(x.path)); if (res.ok.some(x => x.path === Data.open)) Data.open = '';
  RENDER[ST.page] && RENDER[ST.page]();
  if (res.bad.length) await infoDlg({ title: `${res.bad.length} 项没有成功`, sub: res.ok.length ? `其余 ${res.ok.length} 项已清理，释放 ${fmt(sum(res.ok, x => x.size))}。` : '', body: failList(res.bad) });
  else if (res.resid) toast(`已清理 ${res.ok.length} 项，其中 ${res.resid} 项有残留已移入「清理残留」`, '查看', () => goPage('quar'));
  else toast(`已清理 ${res.ok.length} 项，释放 ${fmt(sum(res.ok, x => x.size))}`);
}

/* ---------- 按龄清理流程：说明 → 确认 → 只删旧文件 ---------- */
async function pruneFlow(list) {
  list = list.filter(needsPrune); if (!list.length) return;
  const it = list[0], days = it.pruneDays || 7;
  const ok = await confirmDlg({
    title: `清理 ${it.app} 的旧文件？`,
    sub: `只删除 ${days} 天前的文件${it.prunableSize ? `，预计释放 ${fmt(it.prunableSize)}` : ''}；新文件和文件夹本身都保留。删除后无法恢复。`,
    body: `<div class="callout">${esc(it.note || '')}</div><div class="list"><div class="li wait"><span class="ic"></span><div><b>${esc(it.app)}</b><small class="mono">${esc(it.path)}</small></div><span class="r n">${fmt(it.size)}</span></div></div>`,
    okText: `按住，清理 ${days} 天前的文件`, hold: true,
  });
  if (!ok) return;
  if (ST.op) return toast('有操作正在进行，请稍候');
  ST.op = 'prune';
  let res;
  try { res = await pruneItems(list); } finally { ST.op = null; }
  if (!res) return;
  RENDER[ST.page] && RENDER[ST.page]();
  if (res.bad.length) await infoDlg({ title: '没有清理干净', body: failList(res.bad) });
  else toast(`已清理 ${res.ok.length} 项旧文件，释放 ${fmt(sum(res.ok, x => x._freed || 0))}`);
}

/* ---------- 迁回：已迁移的目录搬回 C 盘原位（拆 junction、数据回移） ---------- */
async function rollbackFlow(it) {
  /* 预检空间：备份还在且未改动 → 直接改名不需要空间；
     需要跨盘复制时 C 盘得留出一整份，快满会失败——提前告诉用户 */
  const pre = await go('rollbackMigration', { path: it.path, checkOnly: true });
  let spaceNote = '';
  if (pre && pre.ok && pre.need > 0) {
    const lack = pre.need > pre.free;
    spaceNote = `<div class="callout ${lack ? 'danger' : 'warn'}">迁回需要 C 盘 ${fmt(pre.need)} 可用空间，当前可用 ${fmt(pre.free)}。${lack ? '空间不足，请先释放一些再迁回。' : ''}</div>`;
    if (lack) { await infoDlg({ title: 'C 盘空间不足，暂时无法迁回', sub: `需要 ${fmt(pre.need)}，当前只有 ${fmt(pre.free)} 可用。数据仍在 ${esc(it.movedTo || '目标盘')}，可以稍后再试。` }); return; }
  }
  const ok = await confirmDlg({
    title: `把 ${it.app} 迁回 C 盘？`,
    sub: `数据会从 ${esc(it.movedTo || '其他磁盘')} 搬回原位置，目录链接移除。`,
    body: spaceNote + `<div class="list"><div class="li wait"><span class="ic"></span><div><b>${esc(it.app)}</b><small class="mono">${esc(it.path)}</small>${it.movedTo ? `<small class="mono">→ ${esc(it.movedTo)}</small>` : ''}</div><span class="r"></span></div></div>`,
    okText: '按住，迁回原位', hold: true,
  });
  if (!ok) return;
  if (ST.op) return toast('有操作正在进行，请稍候');
  ST.op = 'mig';
  let r;
  try { r = await go('rollbackMigration', { path: it.path }); } finally { ST.op = null; }
  if (r.ok) { it.state = 'active'; it.movedTo = ''; delete ST.migs[it.path]; toast('已迁回原位置，重新扫描后可查看'); doScan(); }
  else toast('迁回失败：' + humanErr(r.error));
}

/* ---------- 删除迁移备份：已迁移项留在源盘的 .aiclean-bak-* ---------- */
async function finishBakFlow(it) {
  const m = ST.migs && ST.migs[it.path]; if (!m) return;
  const ok = await confirmDlg({
    title: `删除 ${it.app} 的迁移备份？`,
    sub: `备份占 ${fmt(m.size || 0)}，删除后原位置只有链接，数据都在 ${esc(m.dst || it.movedTo || '目标盘')}。`,
    body: `<div class="callout warn">删除后无法恢复。请先打开应用确认一切正常。</div><div class="list"><div class="li wait"><span class="ic"></span><div><small class="mono sel">${esc(m.bak)}</small></div><span class="r n">${fmt(m.size || 0)}</span></div></div>`,
    okText: '按住，永久删除备份', hold: true, danger: true,
  });
  if (!ok) return;
  const r = await go('finishMigration', { bak: m.bak });
  if (r.ok) {
    ST.migs[it.path].done = true; // 记录保留（已迁移记账），只是备份清掉了
    toast(`备份已删除，释放 ${fmt(r.freed || m.size || 0)}`);
    await loadState(); refreshChrome(); RENDER[ST.page] && RENDER[ST.page]();
  } else toast('删除备份失败：' + humanErr(r.error));
}

/* ---------- 迁移流程：选盘 → 安全检查 → 按住确认 → 逐个迁移 → 结果 ---------- */
async function migrateFlow(list) {
  list = list.filter(x => (kindOf(x) === 'mig' || x.reportOnly) && !x.noMigrate); if (!list.length) return;
  if (ST.op) return toast('有操作正在进行，请稍候');
  ST.op = 'migrate';
  try { await migrateFlowInner(list); } finally { ST.op = null; }
}

async function migrateFlowInner(list) {
  await loadState();
  const need = sum(list, x => x.size), drives = targetDrives();
  if (!drives.length) return infoDlg({ title: '没有可以迁移到的磁盘', sub: '迁移需要另一块本地磁盘（固定磁盘、NTFS 格式）。U 盘、移动硬盘和 exFAT/FAT32 分区不支持。' });
  const fits = d => d.free > need * 1.05 + 512 * MB;
  let pick = (drives.find(fits) || {}).letter || '';
  const step1 = () => new Promise(res => {
    const d = Dlg.open(`<h3>把 ${list.length} 项迁到哪个盘？</h3>
      <div class="sub">共 ${fmt(need)}。迁移后原位置会留一个链接，应用不需要任何设置就能照常使用。</div>
      <div class="body" id="mf-opts"></div>
      <div class="foot"><button class="btn" data-x>取消</button><button class="btn pri" data-ok>下一步：安全检查</button></div>`, { onEsc: () => { Dlg.close(); res(false); } });
    const draw = () => {
      $('#mf-opts', d).innerHTML = drives.map(x => `<div class="opt ${x.letter === pick ? 'on' : ''} ${fits(x) ? '' : 'off'}" data-l="${x.letter}"><span class="rd"></span><div><b>${x.letter} 盘</b><small class="n">${fits(x) ? `可用 ${fmt(x.free)}，共 ${fmt(x.total)}` : `空间不够（可用 ${fmt(x.free)}）`}</small></div><span class="n" style="font-size:12.5px;color:var(--ink2)">${x.letter}:\\AIClean-Moved</span><span class="mini"><i style="width:${x.total ? (1 - x.free / x.total) * 100 : 0}%"></i></span></div>`).join('') +
        (pick ? '' : '<div class="callout warn">所有磁盘的可用空间都不够放下这些数据。可以少选几项再试。</div>');
      $('[data-ok]', d).disabled = !pick;
    };
    draw();
    $('#mf-opts', d).onclick = e => { const o = e.target.closest('.opt'); if (o) { pick = o.dataset.l; draw(); } };
    $('[data-x]', d).onclick = () => { Dlg.close(); res(false); };
    $('[data-ok]', d).onclick = () => res(true);
  });
  if (!await step1()) return;

  /* 安全检查 */
  const rows = list.map(x => ({ it: x, st: 'wait', msg: '', dst: '' }));
  const d = Dlg.open(`<h3 id="mf-t">安全检查</h3><div class="sub" id="mf-s">逐项确认可以安全迁移…</div><div class="body"><div class="list" id="mf-l"></div><div id="mf-c"></div></div><div class="foot" id="mf-f"></div>`, { onEsc() { if (!Dlg.locked) Dlg.close(); } });
  const draw = () => $('#mf-l', d).innerHTML = rows.map(r => `<div class="li ${r.st}"><span class="ic">${r.st === 'ok' ? CHECK : r.st === 'bad' ? CROSS : ''}</span><div><b>${esc(r.it.app)}</b><small class="mono">${esc(r.it.path)}</small>${r.msg ? `<small>${esc(r.msg)}</small>` : ''}</div><span class="r n">${fmt(r.size || r.it.size)}</span></div>`).join('');
  Dlg.locked = true; draw();
  try {
    for (const r of rows) {
      r.st = 'run'; draw(); await paint();
      const p = await go('migPreflight', { path: r.it.path, drive: pick });
      const bad = Array.isArray(p.checks) ? p.checks.filter(c => !c.ok) : null;
      if (!bad) { r.st = 'bad'; r.msg = '检查没有完成：' + humanErr(p.error); }
      else if (bad.length) { r.st = 'bad'; r.msg = '不能迁移：' + bad.map(c => c.name + (c.msg ? `（${c.msg}）` : '')).join('；'); }
      else { r.st = 'ok'; r.dst = p.dst; r.msg = '→ ' + p.dst; }
      draw();
    }
  } finally { Dlg.locked = false; }
  const good = rows.filter(r => r.st === 'ok');
  if (!good.length) {
    $('#mf-t', d).textContent = '没有可以迁移的项目'; $('#mf-s', d).textContent = '所有项目都没有通过安全检查，什么都没有改动。';
    $('#mf-f', d).innerHTML = '<button class="btn pri" data-x>知道了</button>'; $('[data-x]', d).onclick = () => Dlg.close(); return;
  }
  $('#mf-t', d).textContent = good.length === rows.length ? '检查通过，可以迁移' : `${good.length} 项可以迁移，${rows.length - good.length} 项跳过`;
  $('#mf-s', d).textContent = `将复制到 ${pick} 盘并逐一校验，校验通过后才会改动原位置；中途失败会自动还原。`;
  $('#mf-c', d).innerHTML = '<div class="callout">迁移前请先关闭这些应用。迁移过程中窗口可能没有响应，大文件夹需要几分钟，请不要关闭程序。</div>';
  $('#mf-f', d).innerHTML = `<button class="btn" data-x>取消</button><button class="hold" data-ok><span class="holdt">按住，开始迁移<small class="n">${good.length} 项 · ${fmt(sum(good, r => r.it.size))}</small></span><span class="pips"></span></button>`;
  const go2 = await new Promise(res => { $('[data-x]', d).onclick = () => { Dlg.close(); res(false); }; Dlg.onEsc = () => { Dlg.close(); res(false); }; makeHold($('[data-ok]', d), () => res(true), 1000); });
  if (!go2) return;

  /* 执行 */
  Dlg.locked = true; Dlg.onEsc = () => {};
  $('#mf-t', d).textContent = '正在迁移'; $('#mf-c', d).innerHTML = ''; $('#mf-f', d).innerHTML = '';
  rows.forEach(r => { if (r.st === 'ok') { r.st = 'wait'; r.todo = true; } });
  const baks = []; let i = 0;
  try {
    for (const r of rows.filter(x => x.todo)) {
      i++; r.st = 'run'; r.msg = '正在复制和校验…'; $('#mf-s', d).textContent = `${i} / ${good.length} · 请不要关闭程序`; draw(); await paint();
      window.__aicMigCb = p => { if (p.path === r.it.path && p.total) { r.msg = `正在复制 ${Math.round(p.done / p.total * 100)}%（${fmt(p.done)} / ${fmt(p.total)}）`; draw(); } };
      const m = await go('migrateItem', { path: r.it.path, drive: pick });
      window.__aicMigCb = null;
      if (m.ok) {
        r.st = 'ok'; r.size = m.size || r.it.size; r.msg = '已迁到 ' + (m.dst || r.dst); if (m.bak) baks.push(m.bak);
        const it = ST.items.find(x => x.path === r.it.path); if (it) { it.state = 'migrated'; it.size = 0; it.files = 0; }
        Data.sel.delete(r.it.path);
      } else { r.st = 'bad'; r.msg = humanErr(m.error) + (m.rolledBack ? '（已自动还原，原数据没有变化）' : '（原数据没有变化）'); }
      draw();
    }
    await refreshLight();
  } finally { Dlg.locked = false; window.__aicMigCb = null; }
  const okN = rows.filter(r => r.todo && r.st === 'ok').length;
  $('#mf-t', d).textContent = okN ? `已迁移 ${okN} 项` : '迁移没有成功';
  $('#mf-s', d).textContent = okN ? '应用现在可以照常打开使用。' : '所有项目都保持原样，没有任何改动。';
  $('#mf-c', d).innerHTML = baks.length ? `<div class="callout warn"><b>还差一步才腾出 C 盘空间：</b>为了安全，原文件夹的备份还留在 C 盘。请先打开应用确认一切正常，再点「删除备份」真正释放空间（也可以用「迁回」把数据搬回来）。
    <div class="list">${baks.map(b => `<div class="li wait" data-bak-row="${esc(b)}"><span class="ic"></span><div><small class="mono sel">${esc(b)}</small></div><span class="r"><button class="btn sm" data-copy="${esc(b)}">复制路径</button><button class="btn sm" data-delbak="${esc(b)}">删除备份</button></span></div>`).join('')}</div></div>` : '';
  $('#mf-f', d).innerHTML = '<button class="btn pri" data-x>完成</button>';
  const fin = () => { Dlg.close(); RENDER[ST.page] && RENDER[ST.page](); };
  $('[data-x]', d).onclick = fin; Dlg.onEsc = fin;
  d.addEventListener('click', async e => {
    const c = e.target.closest('[data-copy]');
    if (c) { copyText(c.dataset.copy); return; }
    const db = e.target.closest('[data-delbak]');
    if (db && !db.disabled) {
      const yes = await confirmDlg({ title: '永久删除这个备份？', sub: '删除后无法从备份恢复数据，请先打开应用确认一切正常。', okText: '按住，永久删除', hold: true, danger: true });
      if (!yes) return;
      db.disabled = true; db.textContent = '删除中…';
      const r = await go('finishMigration', { bak: db.dataset.delbak });
      if (r.ok) {
        const row = db.closest('[data-bak-row]');
        if (row) { row.classList.remove('wait'); row.classList.add('ok'); row.querySelector('.ic').innerHTML = CHECK; db.remove(); }
        toast(`备份已删除，释放 ${fmt(r.freed || 0)}`);
        await loadState(); refreshChrome();
      } else {
        db.disabled = false; db.textContent = '删除备份';
        toast('删除备份失败：' + humanErr(r.error));
      }
    }
  });
}

/* ---------- 大文件猎手：扫用户目录下 ≥300MB 的散文件（规则之外的大头） ---------- */
async function runFindBig() {
  if (ST.op) return toast('有操作正在进行，请稍候');
  ST.op = 'big';
  let r;
  try {
    const d = Dlg.open(`<h3>大文件猎手</h3><div class="sub">正在遍历用户目录，找单个 ≥300MB 的文件…<br>跳过了系统目录、OneDrive 云文件和已迁移数据。</div><div class="body"></div><div class="foot"></div>`, { onEsc() {} });
    Dlg.locked = true;
    try { r = await go('findBigFiles'); } finally { Dlg.locked = false; Dlg.close(); }
  } finally { ST.op = null; }
  if (!r || !Array.isArray(r.items)) { toast('查找失败：' + humanErr(r && r.error)); return; }
  ST.bigfiles = r.items.filter(x => x && x.path);
  if (ST.bigfiles.length) Data.filter = 'big';
  RENDER[ST.page] && RENDER[ST.page]();
  const dupN = ST.bigfiles.filter(x => x.dup).length;
  toast(ST.bigfiles.length ? `找到 ${ST.bigfiles.length} 个超大文件，共 ${fmt(sum(ST.bigfiles, x => x.size))}${dupN ? `，其中 ${dupN} 个疑似重复` : ''}` : '没有发现 ≥300MB 的散落大文件');
}

/* ---------- 不再提示：把这条规则/路径加进忽略名单，扫描时自动跳过 ---------- */
async function ignoreFlow(it) {
  const ok = await confirmDlg({
    title: `以后扫描不再提示「${it.app}」？`,
    sub: `路径：${esc(it.path)}`,
    body: `<div class="callout">这条规则（以及这个具体路径）会被记入忽略名单，之后扫描自动跳过。可以在「设置 → 已忽略项目」里恢复。</div>`,
    okText: '不再提示', hold: false,
  });
  if (!ok) return;
  const r = await go('ignoreItem', { ruleId: it.ruleId, path: it.path });
  if (r && r.ok) {
    /* 启发式子项 ruleId 是 "父::h"——只按路径隐藏这一条，不误伤同规则的其他 ::h 兄弟项
       （后端按规则 id 匹配，::h 永不命中，这些项下次扫描仍会回来，由 ignorePaths 兜底） */
    const hid = (it.ruleId || '').endsWith('::h');
    ST.items = ST.items.filter(x => x.path !== it.path && (hid || x.ruleId !== it.ruleId));
    Data.open = ''; refreshChrome(); RENDER[ST.page] && RENDER[ST.page]();
    toast('已加入忽略名单，之后扫描不再显示');
  } else toast('操作失败：' + humanErr(r && r.error));
}
