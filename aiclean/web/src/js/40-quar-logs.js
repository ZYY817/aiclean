/* ===== 40-quar-logs · 清理残留（上次没删干净的）+ 操作记录 ===== */
const Quar = { built: false };

/* 逐个永久删除。home=true 时由总览页的按钮触发，顺带锁住总览按钮 */
async function purgeItems(list, onStep, home) {
  const out = { ok: [], bad: [] };
  if (home) { Home.busy = true; Home.note = null; RENDER.home(); }
  for (let i = 0; i < list.length; i++) {
    const q = list[i]; onStep && onStep(i, list.length, q); await paint();
    const r = await go('purgeItem', q.id);
    if (r.ok) { out.ok.push(q); ST.quar = ST.quar.filter(x => x.id !== q.id); } else out.bad.push({ item: q, error: r.error || '未知错误' });
  }
  await refreshLight();
  return out;
}

RENDER.quar = async function () {
  const el = $('#p-quar');
  if (!Quar.built) {
    el.innerHTML = `<div class="pg"><div class="pg-h"><div><h1>清理残留</h1><p id="q-sub"></p></div><div class="big n" id="q-big"></div></div>
      <div class="bar" id="q-bar"></div><div class="scroll" id="q-scroll"></div><div class="pg-foot"></div></div>`;
    Quar.built = true;
    el.addEventListener('click', onQuarClick);
  }
  drawQuar();
  await loadQuar(); refreshChrome(); if (ST.page === 'quar') drawQuar();
};

function drawQuar() {
  const total = sum(ST.quar, q => q.size), n = ST.quar.length;
  $('#q-sub').textContent = n ? '这些内容已经移出原位置，但上次没有删干净，还占着 C 盘。可以再删一次，或者放回原处。' : '';
  $('#q-big').innerHTML = n ? `${fmt(total)}<small>${n} 项，仍在 C 盘上</small>` : '';
  $('#q-bar').innerHTML = n ? `<span style="font-size:13.5px;color:var(--ink2)">通常是因为当时有程序占用。关闭相关应用后再试。</span><span class="sp"></span><button class="btn dang" data-a="purge-all">全部删除，释放 ${fmt(total)}</button>` : '';
  $('#q-bar').style.display = n ? '' : 'none';
  const box = $('#q-scroll');
  if (!n) { box.innerHTML = `<div class="empty"><div class="eg">${'<i></i>'.repeat(9)}</div><h3>没有残留</h3><p>清理都已经完成，没有留下没删干净的内容。</p></div>`; return; }
  box.innerHTML = ST.quar.map(q => {
    return `<div class="qrow" data-id="${esc(q.id)}">
      <span class="app">${esc(q.app || baseName(q.origPath))}</span>
      <span class="path sel" title="${esc(q.origPath)}">&lrm;${esc(q.origPath)}</span>
      <span class="size n">${fmt(q.size)}</span>
      <span class="keep n">${esc(q.quarAt || '')}</span>
      <span class="ops"><button class="btn sm" data-a="restore">放回原处</button><button class="btn sm dang" data-a="purge">再删一次</button></span></div>`;
  }).join('');
}

async function onQuarClick(e) {
  const a = e.target.closest('[data-a]'); if (!a) return;
  const row = e.target.closest('.qrow'), q = row && ST.quar.find(x => x.id === row.dataset.id);
  if (a.dataset.a === 'restore' && q) {
    a.disabled = true; a.textContent = '放回中…'; await paint();
    const r = await go('restoreItem', q.id);
    if (r.ok) {
      ST.quar = ST.quar.filter(x => x.id !== q.id); ST.stale = true; Home.note = null;
      await refreshLight(); drawQuar();
      toast(`已放回原位置：${q.app || baseName(q.origPath)}`, '重新扫描', doScan);
    } else { drawQuar(); infoDlg({ title: '没有放回成功', sub: esc(q.origPath), body: `<div class="callout warn">${esc(humanErr(r.error))}</div>` }); }
  } else if (a.dataset.a === 'purge' && q) {
    const ok = await confirmDlg({ title: '永久删除这一项？', sub: `${esc(q.app || '')} · ${fmt(q.size)}<br><span class="mono">${esc(q.origPath)}</span>`, body: '<div class="callout warn">删除后无法恢复。</div>', okText: '按住，永久删除', hold: true, danger: true });
    if (!ok) return;
    const res = await purgeItems([q]); drawQuar();
    res.ok.length ? toast(`已释放 ${fmt(q.size)}`) : infoDlg({ title: '没有删除成功', body: `<div class="callout warn">${esc(humanErr(res.bad[0].error))}</div>` });
  } else if (a.dataset.a === 'purge-all') {
    const total = sum(ST.quar, x => x.size), n = ST.quar.length;
    const ok = await confirmDlg({ title: `删除全部 ${n} 项残留？`, sub: `将永久删除 ${fmt(total)}，C 盘会腾出相应空间。`, body: '<div class="callout warn">删除后无法恢复。</div>', okText: '按住，全部永久删除', hold: true, danger: true });
    if (!ok) return;
    const bar = $('#q-bar'); bar.innerHTML = '<span id="q-prog" style="font-size:13.5px">正在删除…</span>';
    const res = await purgeItems(ST.quar.slice(), (i, m) => { const p = $('#q-prog'); if (p) p.textContent = `正在删除 ${i + 1} / ${m}…`; });
    Home.note = res.ok.length ? { type: 'purged', size: sum(res.ok, x => x.size) } : Home.note;
    drawQuar();
    res.bad.length ? infoDlg({ title: `${res.bad.length} 项没有删除成功`, body: failList(res.bad.map(b => ({ item: { app: b.item.app, origPath: b.item.origPath, size: b.item.size }, error: b.error }))) }) : toast(`已释放 ${fmt(sum(res.ok, x => x.size))}`);
  }
}

/* ---------- 操作记录 ---------- */
const Logs = { built: false, f: 'all' };
const OPS = { quarantine: '清理 · 移出', clean: '清理 · 移出', restore: '放回原处', purge: '清理 · 删除', migrate: '迁移', prune: '清理旧文件', autoclean: '自动清理' };
RENDER.logs = async function () {
  const el = $('#p-logs');
  if (!Logs.built) {
    el.innerHTML = `<div class="pg"><div class="pg-h"><div><h1>记录</h1><p id="l-sub">每一次改动都记在这里，最多显示最近 200 条。</p></div><div class="big n" id="l-big"></div></div>
      <div class="bar" id="l-bar"></div><div class="scroll" id="l-scroll"></div><div class="pg-foot"></div></div>`;
    Logs.built = true;
    el.addEventListener('click', e => { const f = e.target.closest('[data-f]'); if (f) { Logs.f = f.dataset.f; drawLogs(); } });
  }
  drawLogs();
  const r = await loadLogs(); Logs.err = r.error || '';
  if (ST.page === 'logs') drawLogs();
};
function drawLogs() {
  const all = ST.logs, kinds = [...new Set(all.map(l => OPS[l.op] || l.op))];
  if (Logs.f !== 'all' && !kinds.includes(Logs.f)) Logs.f = 'all';
  const list = all.filter(l => Logs.f === 'all' || (OPS[l.op] || l.op) === Logs.f);
  const bad = all.filter(l => l.ok === false).length;
  $('#l-big').innerHTML = all.length ? `${all.length}<small>条记录${bad ? `，其中 ${bad} 条失败` : ''}</small>` : '';
  const bar = $('#l-bar'); bar.style.display = all.length ? '' : 'none';
  bar.innerHTML = [['all', '全部', all.length], ...kinds.map(k => [k, k, all.filter(l => (OPS[l.op] || l.op) === k).length])].map(c => `<button class="fchip ${Logs.f === c[0] ? 'on' : ''}" data-f="${esc(c[0])}">${esc(c[1])}<small class="n">${c[2]}</small></button>`).join('');
  const box = $('#l-scroll');
  if (!all.length) { box.innerHTML = `<div class="empty"><div class="eg">${'<i></i>'.repeat(9)}</div><h3>${Logs.err ? '记录读取失败' : '还没有任何操作'}</h3><p>${Logs.err ? esc(humanErr(Logs.err)) : '清理或迁移之后，这里会留下记录，方便以后回查。'}</p></div>`; return; }
  box.innerHTML = list.map(l => `<div class="lrow">
    <span class="t n">${esc(l.t || '')}</span><span class="op">${esc(OPS[l.op] || l.op || '')}</span>
    <span class="path"><span class="sel" title="${esc(l.path)}">${l.app ? `<b style="font-weight:500">${esc(l.app)}</b> · ` : ''}${esc(l.path || '')}</span>${l.detail ? `<small title="${esc(l.detail)}">${esc(l.ok === false ? humanErr(l.detail) : l.detail)}</small>` : ''}</span>
    <span class="size n">${l.size ? fmt(l.size) : '–'}</span><span class="st ${l.ok === false ? 'bad' : ''}">${l.ok === false ? '失败' : '成功'}</span></div>`).join('');
}
