/* ===== 70-welcome · 首次使用说明 + 彻底移除流程 ===== */

/* shouldShowWelcome: 纯函数（可测）——prefs 里没有 welcomeAcked 就要弹 */
function shouldShowWelcome(prefs) { return !(prefs && prefs.welcomeAcked); }

function welcomeBody() {
  return `
    <div class="set" style="border:0;padding:0">
      <div class="row"><div class="tx"><b>AIClean 做什么</b><small>扫描 C 盘找出占用空间的缓存和数据 → 清掉能自动重建的缓存 → 把体积大、但不能删的用户数据搬到其他盘。</small></div></div>
      <div class="row"><div class="tx"><b>这些操作不可恢复</b><small>直接删除的清理、清空回收站、关闭休眠、删除迁移备份——都是按住确认才会执行，按之前请想清楚。</small></div></div>
      <div class="row"><div class="tx"><b>迁移的原理</b><small>数据搬到 <span class="mono">其他盘:\AIClean-Moved</span>，原位置留一个目录链接，软件照常使用，随时可迁回。请不要手动删除 AIClean-Moved 目录。</small></div></div>
      <div class="row"><div class="tx"><b>自动清理默认关闭</b><small>在设置页开启后，由 Windows 计划任务定时执行，只清理标记为低风险的缓存，不碰用户数据。</small></div></div>
      <div class="row"><div class="tx"><b>完全本机运行</b><small>不联网、不上传任何文件或路径，没有统计上报。</small></div></div>
      <div class="row"><div class="tx"><b>请先备份重要数据</b><small>删除类操作不可恢复。软件按规则和风险分级尽量保守，但不能替代你自己的判断。</small></div></div>
    </div>`;
}

/* welcomeDlg: 首次启动（manual=false）必须勾选"我已了解"才能开始；关于页重看（manual=true）可直接关 */
function welcomeDlg(manual) {
  return new Promise(res => {
    const d = Dlg.open(`<h3>欢迎使用 AIClean</h3><div class="sub">花一分钟了解它能做什么、哪些操作要小心。</div>
      <div class="body" style="max-height:56vh;overflow-y:auto">${welcomeBody()}
      <label style="display:flex;gap:10px;margin-top:14px;align-items:center;cursor:pointer">
        <input type="checkbox" id="wc-ack" style="width:16px;height:16px"><span style="font-size:13.5px">我已了解以上内容</span></label></div>
      <div class="foot"><button class="btn pri" data-ok ${manual ? '' : 'disabled'}>${manual ? '关闭' : '开始使用'}</button></div>`,
      { onEsc() { if (manual) { Dlg.close(); res(false); } } });
    const ok = $('[data-ok]', d), ack = $('#wc-ack', d);
    if (!manual) ack.addEventListener('change', () => { ok.disabled = !ack.checked; });
    else ack.closest('label').style.display = 'none';
    ok.onclick = () => {
      if (!manual) Prefs.set('welcomeAcked', ST.version || '1');
      Dlg.close(); res(true);
    };
  });
}

/* ---------- 彻底移除：前置检查 → 选择 → 按住确认 → 退出 ---------- */

/* uninstallMigDlg: 有已迁移项时的三选对话框 → 'rollback' | 'keep' | null */
function uninstallMigDlg(list) {
  return new Promise(res => {
    const d = Dlg.open(`<h3>还有 ${list.length} 项数据已迁移</h3>
      <div class="sub">原位置现在是链接，数据本体在其他盘。</div>
      <div class="body"><div class="list">${list.map(m => `<div class="li"><span class="ic"></span><div><small class="mono sel">${esc(m.src)}</small></div><span class="r n">${m.size ? fmt(m.size) : ''}</span></div>`).join('')}</div>
        <div class="callout warn" style="margin-top:10px">选「保留迁移」：数据仍在 <span class="mono">其他盘:\AIClean-Moved</span> 下，原位置的链接继续有效，软件照常使用——以后不要手动删除 AIClean-Moved 目录。</div></div>
      <div class="foot"><button class="btn" data-x>取消</button><button class="btn" data-keep>保留迁移</button><button class="btn pri" data-rb>先全部迁回</button></div>`,
      { onEsc() { Dlg.close(); res(null); } });
    $('[data-x]', d).onclick = () => { Dlg.close(); res(null); };
    $('[data-keep]', d).onclick = () => { Dlg.close(); res('keep'); };
    $('[data-rb]', d).onclick = () => { Dlg.close(); res('rollback'); };
  });
}

/* uninstallBakDlg: 有未删除迁移备份时的选择 → true(删除) | false(保留) | null */
function uninstallBakDlg(list) {
  const total = list.reduce((a, b) => a + (b.size || 0), 0);
  return new Promise(res => {
    const d = Dlg.open(`<h3>还有 ${list.length} 个迁移备份占着 C 盘</h3>
      <div class="sub">共 ${fmt(total)}。删除后无法再迁回这些目录。</div>
      <div class="body"><div class="list" style="max-height:30vh;overflow-y:auto">${list.map(b => `<div class="li"><span class="ic"></span><div><small class="mono sel">${esc(b.path)}</small></div><span class="r n">${fmt(b.size || 0)}</span></div>`).join('')}</div></div>
      <div class="foot"><button class="btn" data-x>取消</button><button class="btn" data-keep>保留备份</button><button class="btn pri" data-del>删除备份</button></div>`,
      { onEsc() { Dlg.close(); res(null); } });
    $('[data-x]', d).onclick = () => { Dlg.close(); res(null); };
    $('[data-keep]', d).onclick = () => { Dlg.close(); res(false); };
    $('[data-del]', d).onclick = () => { Dlg.close(); res(true); };
  });
}

async function uninstallFlow() {
  if (ST.op) { toast('有操作正在进行，请先等它结束'); return; }
  if (ST.pinnedMissing) { toast('数据目录所在盘未连接，暂不可移除'); return; }
  ST.op = 'uninstall';
  try {
    const ck = await go('uninstallCheck');
    if (!ck || !ck.ok) { toast('前置检查失败：' + humanErr((ck && ck.error) || '')); return; }
    if (ck.migrated && ck.migrated.length) {
      const ch = await uninstallMigDlg(ck.migrated);
      if (!ch) return;
      if (ch === 'rollback') {
        for (const m of ck.migrated) {
          const r = await go('rollbackMigration', { path: m.src });
          if (!r || !r.ok) {
            await infoDlg({ title: '迁回失败', body: `<span class="mono">${esc(m.src)}</span><br>${esc(humanErr((r && r.error) || '未知错误'))}<br><br>已中止移除，请解决问题后再试。` });
            return;
          }
        }
      }
    }
    let deleteBaks = true;
    if (ck.baks && ck.baks.length) {
      deleteBaks = await uninstallBakDlg(ck.baks);
      if (deleteBaks === null) return;
    }
    const ok = await confirmDlg({
      title: '彻底移除 AIClean？',
      sub: '计划任务、开机自启、偏好设置、操作记录和清理残留将一并删除。',
      body: `<div class="callout danger">此操作不可恢复。AIClean.exe 本体需要你手动删除。${ST.pinnedMissing ? '' : ''}</div>`,
      okText: '按住，彻底移除', hold: true, danger: true,
    });
    if (!ok) return;
    const r = await go('uninstallRun', { deleteBaks });
    if (!r || !r.ok) { toast('移除失败：' + humanErr((r && r.error) || '')); return; }
    /* 结果细节：校验没过没删成的备份 + 保留清单位置，都要让用户看见 */
    let extra = '';
    if (r.skipped && r.skipped.length) {
      extra += `<div class="callout warn" style="margin-top:10px;text-align:left">以下 ${r.skipped.length} 个备份<b>没有删除</b>（校验未通过，数据可能是唯一副本）：<div class="list" style="margin-top:6px;max-height:20vh;overflow-y:auto">${r.skipped.map(s => `<div class="li"><div><small class="mono sel">${esc(s.path)}</small><br><small>${esc(s.reason)}</small></div></div>`).join('')}</div></div>`;
    }
    if (r.keptList) {
      extra += `<div class="callout" style="margin-top:10px;text-align:left">保留项的清单已写到桌面：<br><span class="mono sel">${esc(r.keptList)}</span></div>`;
    }
    await infoDlg({ title: '已移除', body: `计划任务、自启和数据目录已清理。${extra}<br>可以直接删除 AIClean.exe 了。`, okText: '退出' });
    go('quitApp');
  } finally {
    ST.op = null;
  }
}
