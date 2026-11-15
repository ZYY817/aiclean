/* ===== 90-init · 启动 ===== */
(async function () {
  /* 启动参数：exe 用 #home / #items / #quar… 指定起始页；调试时还可以带 theme-ikb、calm、fast */
  const tokens = decodeURIComponent(location.hash.slice(1)).split(/[&,/ ]+/).filter(Boolean);
  const tk = p => (tokens.find(t => t.startsWith(p)) || '').slice(p.length);
  await Prefs.ready;                     // 等后端 prefs 合进来再读主题（exe 里 localStorage 存不住）
  setTheme(tk('theme-') || Prefs.get('theme', 'fog'));
  setMotion(tokens.includes('calm') ? 'calm' : Prefs.get('motion', 'full'));
  if (typeof window.getState !== 'function') installMock();

  $('#btn-rescan').addEventListener('click', () => { if (ST.phase !== 'scanning') { Home.note = null; doScan(); } });

  const start = tokens.find(t => PAGES.includes(t) || ALIAS[t]) || 'home';
  ST.phase = 'boot';
  goPage(start);
  await doScan();            // 打开即扫描一次；之后只在用户点"重新扫描"时才扫
  if (shouldShowWelcome(Prefs._mem)) await welcomeDlg(false); // 首次启动：了解完再使用
  const f0 = tk('f-');       // 调试：#items f-stat → 数据页预设筛选
  if (f0 && typeof drawData === 'function' && (start === 'data' || ALIAS[start] === 'data')) { Data.filter = f0; drawData(); }
})();
