/* ===== 50-settings · 设置：主题 / 动效 / 关于 ===== */
const Settings = { built: false, dir: '', sched: { freq: 'off', time: '03:00', next: '' }, boot: false, loaded: false, ignN: 0, retain: '' };
RENDER.settings = async function () {
  const el = $('#p-settings');
  if (!Settings.built) {
    el.innerHTML = `<div class="pg"><div class="pg-h"><div><h1>设置</h1><p>外观和数据。</p></div></div><div class="bar" style="display:none"></div><div class="scroll"><div class="set" id="s-body"></div><div class="pg-foot"></div></div></div>`;
    Settings.built = true;
    el.addEventListener('click', async e => {
      const t = e.target.closest('[data-theme-id]'), m = e.target.closest('[data-motion]');
      if (t) { setTheme(t.dataset.themeId, true); drawSettings(); }
      if (m) { setMotion(m.dataset.motion, true); drawSettings(); }
      const sf = e.target.closest('[data-sched]');
      if (sf) { Settings.sched.freq = sf.dataset.sched; drawSettings(); }
      const rt = e.target.closest('[data-retain]');
      if (rt) { Settings.retain = rt.dataset.retain; Prefs.set('quarRetainDays', Settings.retain); drawSettings(); }
      const qv = e.target.closest('[data-a="quarview"]');
      if (qv) goPage('quar');
      const bt = e.target.closest('[data-boot]');
      if (bt) {
        const on = bt.dataset.boot === 'on';
        const r = await go('autostartSet', { on });
        if (r.ok) { Settings.boot = on; Prefs.set('boot', on); drawSettings(); toast(on ? '已设为开机自启' : '已关闭开机自启'); }
        else toast('设置失败：' + humanErr(r.error));
      }
      if (e.target.closest('[data-a="rescan"]')) { goPage('home'); doScan(); }
      if (e.target.closest('[data-a="opendir"]')) go('openDataDir');
      if (e.target.closest('[data-a="openlogs"]')) go('openLogsDir');
      if (e.target.closest('[data-a="uninstall"]')) uninstallFlow();
      if (e.target.closest('[data-a="elev"]')) {
        const r = await go('relaunchElevated');
        if (!r.ok) toast(humanErr(r.error || '提权失败'));
      }
      if (e.target.closest('[data-a="about"]')) goPage('about');
      if (e.target.closest('[data-a="openrules"]') && ST.rulesPath) go('openPath', { path: ST.rulesPath });
      if (e.target.closest('[data-a="unignore"]')) {
        const ok = await confirmDlg({
          title: '清空忽略名单？',
          sub: `当前忽略了 ${Settings.ignN} 条规则/路径。`,
          body: '<div class="callout">恢复后这些内容会在下次扫描时重新出现。</div>',
          okText: '恢复全部', hold: false,
        });
        if (!ok) return;
        const r = await go('unignoreAll');
        if (r.ok) { Settings.ignN = 0; drawSettings(); toast('忽略名单已清空，重新扫描后会全部显示'); }
        else toast('操作失败：' + humanErr(r.error));
      }
      if (e.target.closest('[data-a="savesched"]')) {
        const tm = ($('#s-sctime') || {}).value || '03:00';
        const freq = Settings.sched.freq;
        /* 开启自动清理前必须确认一次：说明它到底会清什么 */
        if (freq !== 'off') {
          const ok = await confirmDlg({
            title: `开启${freq === 'daily' ? '每天' : '每周'}自动清理？`,
            sub: `Windows 计划任务会在 ${esc(tm)} 静默执行，不需要打开界面。`,
            body: `<div class="callout">每次只清理<b>标记为低风险缓存</b>的内容（应用会自动重建的缓存、过期的旧文件）。<br>用户数据、待确认项、使用中的目录一律不碰；你点过「不再提示」的项目也会跳过。</div>`,
            okText: '确认开启', hold: false,
          });
          if (!ok) return;
        }
        const r = await go('schedSet', { freq, time: tm });
        if (r.ok) {
          Settings.sched.time = tm; Prefs.set('schedFreq', freq); Prefs.set('schedTime', tm);
          const st = await go('schedStatus'); Settings.sched.next = st.next || '';
          drawSettings(); toast(freq === 'off' ? '自动清理已关闭' : `已设为${freq === 'daily' ? '每天' : '每周'} ${tm} 自动清理`);
        } else toast('计划任务设置失败：' + humanErr(r.error));
      }
      if (e.target.closest('[data-a="cleanow"]')) {
        const b = e.target.closest('[data-a="cleanow"]'); b.disabled = true; b.textContent = '正在清理…'; await paint();
        const r = await go('autoCleanNow');
        b.disabled = false; b.textContent = '立即清理一次';
        if (r.ok) { toast(r.count ? `自动清理完成：处理 ${r.count} 项，释放 ${fmt(r.freed)}` : '没有需要清理的内容'); ST.stale = true; }
        else toast('清理失败：' + humanErr(r.error));
      }
    });
  }
  drawSettings();
  if (!Settings.dir) {
    const r = await go('dataDir');
    Settings.dir = (r && r.path) || '不可用';
    if (ST.page === 'settings') drawSettings();
  }
  if (!Settings.loaded) {
    Settings.loaded = true;
    const [st, bt] = await Promise.all([go('schedStatus'), go('autostartGet')]);
    Settings.sched.next = st.next || '';
    if (!st.on) Settings.sched.freq = 'off';
    else Settings.sched.freq = Prefs.get('schedFreq', 'daily');
    Settings.sched.time = Prefs.get('schedTime', '03:00');
    Settings.boot = !!bt.on;
    const pr = await go('getPrefs');
    Settings.ignN = ((pr && pr.ignoreRules) || []).length + ((pr && pr.ignorePaths) || []).length;
    if (ST.page === 'settings') drawSettings();
  }
};
function drawSettings() {
  const cur = document.documentElement.dataset.theme, calmOn = document.documentElement.classList.contains('calm');
  const s = summary(), retain = Settings.retain || Prefs.get('quarRetainDays', '30');
  Settings.retain = retain;
  $('#s-body').innerHTML = `
    <h4>外观</h4>
    <dl class="srow"><dt>主题</dt><dd><div class="themes">${THEMES.map(t => `<button class="theme ${t.id === cur ? 'on' : ''}" data-theme-id="${t.id}">
      <span class="pv" style="background:${t.pv[0]}">${[1, 1, 2, 2, 3, 0].map(i => `<i style="background:${i === 0 ? 'transparent;box-shadow:0 0 0 1px ' + t.pv[3] + ' inset;opacity:.4' : t.pv[i]}"></i>`).join('')}</span>
      <b>${t.name}</b><small>${t.desc}</small></button>`).join('')}</div></dd></dl>
    <dl class="srow"><dt>动效</dt><dd><span class="seg"><button class="${calmOn ? '' : 'on'}" data-motion="full">完整</button><button class="${calmOn ? 'on' : ''}" data-motion="calm">简化</button></span>
      <small style="margin-top:8px">“简化”会关掉格子入场、透镜和飞行动画，电脑较慢或不喜欢动画时使用。</small></dd></dl>
    <h4>数据</h4>
    <dl class="srow"><dt>扫描</dt><dd>${ST.scannedAt ? `上次扫描于 ${ST.scannedAt.toLocaleString('zh-CN', { hour12: false })}，识别到 ${s.count} 项。` : '还没有完成过扫描。'}
      <div style="margin-top:8px"><button class="btn sm" data-a="rescan">重新扫描</button></div><small style="margin-top:8px">只在你打开软件或点“重新扫描”时才会扫描，不会在后台运行。</small></dd></dl>
    <dl class="srow"><dt>规则库</dt><dd class="mono sel" style="font-size:12.5px">${esc(ST.rulesPath || '未加载（使用内置规则）')}${ST.rulesPath ? '<div style="margin-top:8px"><button class="btn sm" data-a="openrules">打开位置</button></div>' : ''}</dd></dl>
    <dl class="srow"><dt>数据目录</dt><dd><span class="mono sel" style="font-size:12.5px" id="s-datadir">${esc(Settings.dir || '读取中…')}</span>
      <div style="margin-top:8px"><button class="btn sm" data-a="opendir">打开位置</button><button class="btn sm" data-a="openlogs" style="margin-left:8px">打开日志目录</button></div><small>操作记录、迁移账本和偏好都存在这里，删除它不影响已迁移的数据。有其他本地盘时会自动放到非 C 盘，不让清理工具自己占 C 盘。</small></dd></dl>
    <dl class="srow"><dt>清理方式</dt><dd>直接永久删除，立刻腾出空间，无法恢复。<small>一键清理只处理标记为“缓存”的内容（应用会自动重建）；其他数据需要你逐项按住确认。</small></dd></dl>
    <dl class="srow"><dt>清理残留</dt><dd>删除时没删干净的残留会进入「清理残留」，可恢复、可再删；保留期满自动清理。
      <div style="margin-top:8px"><span class="seg">${[['7', '7 天'], ['30', '30 天'], ['90', '90 天']].map(x => `<button class="${retain === x[0] ? 'on' : ''}" data-retain="${x[0]}">${x[1]}</button>`).join('')}</span>
      <button class="btn sm" data-a="quarview" style="margin-left:10px">查看残留（${ST.quar.length} 项）</button></div></dd></dl>
    <dl class="srow"><dt>自动清理</dt><dd>
      <span class="seg">${[['off', '关闭'], ['daily', '每天'], ['weekly', '每周']].map(x => `<button class="${Settings.sched.freq === x[0] ? 'on' : ''}" data-sched="${x[0]}">${x[1]}</button>`).join('')}</span>
      <input type="time" id="s-sctime" value="${esc(Settings.sched.time)}" style="margin-left:10px" ${Settings.sched.freq === 'off' ? 'disabled' : ''}>
      <div style="margin-top:8px"><button class="btn sm" data-a="savesched">保存计划</button><button class="btn sm" data-a="cleanow" style="margin-left:8px">立即清理一次</button></div>
      <small style="margin-top:8px">到点由 Windows 计划任务静默执行：只清缓存类目录和过期的旧文件，锁定/待确认/用户数据一律不碰。${Settings.sched.next ? `<br>下次运行：${esc(Settings.sched.next)}` : ''}</small></dd></dl>
    <dl class="srow"><dt>开机自启</dt><dd><span class="seg"><button class="${Settings.boot ? '' : 'on'}" data-boot="off">关</button><button class="${Settings.boot ? 'on' : ''}" data-boot="on">开</button></span>
      <small style="margin-top:8px">登录 Windows 后自动打开 AIClean。只扫描展示，不会自动删除任何东西。</small></dd></dl>
    <dl class="srow"><dt>迁移位置</dt><dd><span class="mono sel" style="font-size:12.5px">目标盘:\\AIClean-Moved\\工具名\\</span><small>原位置会留下一个目录链接，应用照常使用。</small></dd></dl>
    <dl class="srow"><dt>已忽略</dt><dd>${Settings.ignN ? `已忽略 <b class="n">${Settings.ignN}</b> 条规则/路径，扫描时自动跳过。` : '没有忽略任何项目。'}
      ${Settings.ignN ? '<div style="margin-top:8px"><button class="btn sm" data-a="unignore">恢复全部</button></div>' : ''}<small>在数据明细里点「不再提示」可以把某项加进忽略名单，之后扫描不再显示。</small></dd></dl>
    <h4>权限</h4>
    <dl class="srow"><dt>运行模式</dt><dd>${ST.elevated ? '<b style="color:var(--ok,#1a7f37)">管理员模式</b> — 可以处理系统级目录' : '普通模式 — 系统目录（Windows、ProgramData）无法清理'}
      ${ST.elevated ? '' : '<div style="margin-top:8px"><button class="btn sm" data-a="elev">以管理员身份重新启动</button></div><small style="margin-top:8px">会弹 UAC 确认，确认后本窗口关闭并打开管理员实例。</small>'}</dd></dl>
    <h4>关于</h4>
    <dl class="srow"><dt>产品</dt><dd><b>AIClean</b> — C 盘空间管理工具<div style="margin-top:8px"><button class="btn sm" data-a="about">关于与作者信息</button></div></dd></dl>
    <dl class="srow"><dt>版本</dt><dd class="n">AIClean ${esc(ST.version || '')}${ST.demo ? '（浏览器演示模式，数据是模拟的）' : ''}</dd></dl>
    <dl class="srow"><dt>隐私</dt><dd>完全在本机运行，不联网，不上传任何文件或路径。</dd></dl>
    <h4>危险操作</h4>
    <dl class="srow"><dt>彻底移除 AIClean</dt><dd>删除计划任务、开机自启、数据目录、日志和清理残留；已迁移的数据和链接会保留。
      <div style="margin-top:8px"><button class="btn sm dang" data-a="uninstall"${ST.pinnedMissing ? ' disabled title="数据目录所在盘未连接，暂不可移除"' : ''}>彻底移除…</button></div>
      <small>${ST.pinnedMissing ? '数据目录所在盘未连接，迁移记录暂时无法检查，请连接后再移除。' : 'AIClean.exe 本体需要你手动删除。此操作不可恢复。'}</small></dd></dl>`;
}
