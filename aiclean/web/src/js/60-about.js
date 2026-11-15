/* ===== 60-about · 关于与作者 ===== */
/* ✏️ 作者信息在这里改 */
const AUTHOR = {
  name: '智宇丶817',
  mail: '',
  site: 'space.bilibili.com/41173761',
  siteUrl: 'https://space.bilibili.com/41173761?spm_id_from=333.40164.0.0',
  sign: 'B 站 UP 主 · 分享资源软件、游戏技巧与问题解决方案',
};

const About = { built: false, dir: '' };
RENDER.about = async function () {
  const el = $('#p-about');
  if (!About.built) {
    el.innerHTML = `<div class="pg"><div class="pg-h"><div><h1>关于</h1><p>AIClean · C 盘空间管家 — 作者、功能与隐私说明。</p></div><div class="big n">v<span id="ab-ver"></span><small>当前版本</small></div></div><div class="bar" style="display:none"></div><div class="scroll"><div class="about" id="ab-body"></div></div></div>`;
    About.built = true;
    el.addEventListener('click', e => {
      if (e.target.closest('[data-a="opendir"]')) go('openDataDir');
      if (e.target.closest('[data-a="openrules"]') && ST.rulesPath) go('openPath', { path: ST.rulesPath });
      if (e.target.closest('[data-a="elev"]')) go('relaunchElevated').then(r => { if (!r || !r.ok) toast(humanErr(r && r.error || '提权失败')); });
      if (e.target.closest('[data-a="site"]')) go('openUrl', { url: AUTHOR.siteUrl });
      if (e.target.closest('[data-a="welcome"]')) welcomeDlg(true);
      const mail = e.target.closest('[data-a="mail"]');
      if (mail) { try { navigator.clipboard.writeText(AUTHOR.mail); toast('邮箱地址已复制'); } catch (_) { toast(AUTHOR.mail); } }
      const tip = e.target.closest('.tipbox img');
      if (tip) tipZoom(tip.src, tip.alt || '赞赏码');
    });
  }
  const feats = [
    ['整盘扫描', '规则库 + 启发式自动发现 + 大文件猎手，打开即扫'],
    ['安全清理', '缓存类一键清、按龄清理、逐项确认，不动用户数据'],
    ['无损迁移', '数据搬去其他盘 + 目录链接回跳，软件照常使用，可迁回'],
    ['残留还原', '删除失败/中断的残留可恢复可再删，保留期满自动清（设置页可改保留期）'],
    ['自动清理', '接入 Windows 计划任务，定时静默清缓存（设置页开启）'],
    ['可解释', '每一项都写清是什么、能不能删、有什么风险'],
  ];
  $('#ab-ver').textContent = ST.version || '?';
  $('#ab-body').innerHTML = `
    <div class="set"><h3>作者</h3>
      <div class="row"><div class="tx"><b>${esc(AUTHOR.name)}</b>${AUTHOR.sign ? `<small>${esc(AUTHOR.sign)}</small>` : ''}</div><button class="btn sm" data-a="site">访问主页</button></div>
      ${AUTHOR.mail ? `<div class="row"><div class="tx"><b>联系</b><small>${esc(AUTHOR.mail)}</small></div><button class="btn sm" data-a="mail">复制邮箱</button></div>` : ''}
    </div>

    <div class="set"><h3>赞赏支持</h3>
      <div class="row"><div class="tx"><b>觉得这个工具好用？</b><small>请作者喝杯咖啡，让 AIClean 持续更新（点二维码可放大）</small></div></div>
      <div class="tiprow">${typeof TIP_WX !== 'undefined' ? `<div class="tipbox"><img src="${TIP_WX}" alt="微信赞赏码"><span>微信</span></div>` : ''}${typeof TIP_ZFB !== 'undefined' ? `<div class="tipbox"><img src="${TIP_ZFB}" alt="支付宝赞赏码"><span>支付宝</span></div>` : ''}</div>
    </div>

    <div class="set"><h3>它能做什么</h3>
      ${feats.map(f => `<div class="row"><div class="tx"><b>${f[0]}</b><small>${f[1]}</small></div></div>`).join('')}
    </div>

    <div class="set"><h3>使用说明</h3>
      <div class="row"><div class="tx"><b>再看一遍首次启动说明</b><small>功能、不可恢复操作和迁移原理的完整介绍</small></div><button class="btn sm" data-a="welcome">查看使用说明</button></div>
    </div>

    <div class="set"><h3>安全与隐私</h3>
      <div class="row"><div class="tx"><b>完全本地</b><small>不联网、不收集、不上传任何数据；所有判断在你电脑上完成</small></div></div>
      <div class="row"><div class="tx"><b>路径校验</b><small>只对本轮扫描到的路径操作；锁定、迁移中、链接项不会被删</small></div></div>
      <div class="row"><div class="tx"><b>用户数据不碰</b><small>文档、存档、聊天记录只提示不自动删，一键清理只碰缓存类</small></div></div>
      <div class="row"><div class="tx"><b>特殊操作单独确认</b><small>回收站 / 休眠文件 / 更新缓存需要逐项按住确认</small></div></div>
    </div>

    <div class="set"><h3>数据与环境</h3>
      <div class="row"><div class="tx"><b>数据目录</b><small class="mono">${esc(About.dir || '…')}</small></div><button class="btn sm" data-a="opendir">打开位置</button></div>
      <div class="row"><div class="tx"><b>规则库</b><small class="mono">${esc(ST.rulesPath || '内嵌')}</small></div><button class="btn sm" data-a="openrules" ${ST.rulesPath ? '' : 'disabled'}>打开位置</button></div>
      <div class="row"><div class="tx"><b>权限</b><small>${ST.elevated ? '管理员模式' : '标准模式（系统目录需要管理员）'}</small></div>${ST.elevated ? '' : '<button class="btn sm" data-a="elev">以管理员身份运行</button>'}</div>
      <div class="row"><div class="tx"><b>技术</b><small>Go + WebView2 · 单文件 exe · 规则库可独立更新</small></div></div>
    </div>
    <div class="pg-foot">AIClean · 清理的是缓存，留住的是数据</div>`;
  if (!About.dir) {
    const r = await go('dataDir');
    About.dir = (r && r.path) || '不可用';
    if (ST.page === 'about') RENDER.about();
  }
};

/* 点赞赏码放大看，方便手机扫 */
function tipZoom(src, label) {
  const d = Dlg.open(`<h3>${esc(label)}</h3><div class="body" style="text-align:center"><img class="tipbig" src="${src}" alt="${esc(label)}"></div>
    <div class="foot"><button class="btn pri" data-x autofocus>关闭</button></div>`);
  $('[data-x]', d).onclick = () => Dlg.close();
}
