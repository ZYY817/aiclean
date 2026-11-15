/* ============================================================
   grid.js · Hero 格子图 demo
   198 格（18×11），分布参照 docs/home.png：
   上部系统占用灰块 → 中部可清理蓝带 → 待确认描边排 → 底部
   可用/仅统计/正在使用，零散大文件橙格。
   另负责：波浪入场延迟、鼠标透镜、tooltip、图例联动。
   ============================================================ */

const COLS = 18, ROWS = 11, LENS_R = 85, LENS_MAX = 1.22;

/* 状态 → 类名 + 悬停文案（少量假数据） */
const TIP = {
  sys:     ['系统占用', 'Windows 与未识别 · 1 GB'],
  clean:   ['可清理', ['NVIDIA DXCache · 10.5 GB', 'DirectX 着色器缓存 · 4.05 GB',
                       'Playwright · 718 MB', 'Go 编译缓存 · 358 MB',
                       'Gradle caches · 321 MB', 'VS Code 缓存 · 88 MB',
                       'NuGet 缓存 · 275 MB', '浏览器缓存 · 642 MB']],
  move:    ['建议迁移', ['HuggingFace 模型 · 1.9 GB', '微信聊天记录 · 620 MB', '项目依赖 · 370 MB']],
  confirm: ['待确认', ['旧游戏存档 · 2.1 GB', '过期下载 · 1.36 GB', 'WorkBuddy AI · 1.48 GB',
                       '旧版安装包 · 980 MB', '导出视频 · 2.4 GB']],
  busy:    ['正在使用', ['微信/QQ 运行中 · 6.58 GB', '已装应用 · 4.65 GB', 'Docker 镜像 · 2.2 GB']],
  stat:    ['仅统计', ['文档与桌面 · 2.2 GB', '已装应用 · 4.65 GB', '系统组件 · 8.1 GB', '照片 · 1.3 GB']],
  free:    ['可用', '可用空间 · 约 1 GB'],
  warn:    ['大文件', ['安装包缓存 · 1.36 GB', '视频工程文件 · 4.2 GB', '虚拟机镜像 · 7.8 GB']],
  res:     ['清理残留', '上次未删净 · 待重试'],
  scan:    ['扫描中', '正在识别…'],
};

/* 逐格状态表：行主序，长度必须 = COLS*ROWS。
   s=sys c=clean m=move f=confirm(待确认) b=busy t=stat(仅统计)
   .=free w=warn r=res(清理残留) ~=scan */
const MAP = [
  'ssssssssssssssssss',
  'ssssssssssssssssss',
  'ssssssssssssssssss',
  'ssssssssssssssssss',
  'ssssssssssssssssss',
  'ssssssssssssssssss',
  'sssstttttttttttttt',   // 系统占用尾排 + 仅统计描边
  'ttttttttttttttcccc',   // 仅统计 + 蓝带右起
  'ccccccccccccccmmmw',   // 蓝带 + 建议迁移黑格 + 大文件
  'fffffffffffwwttttt',   // 待确认描边排 + 大文件 + 仅统计
  'bbbbttt.....r~.bbb',   // 正在使用(点) + 仅统计 + 可用 + 残留/扫描 + 正在使用
];
const CLS = { s:'sys', c:'clean', m:'move', f:'confirm', b:'busy',
              t:'stat', '.':'free', w:'warn', r:'res', '~':'scan' };

let pick_i = 0;
function tipFor(state) {
  const t = TIP[state];
  const detail = Array.isArray(t[1]) ? t[1][pick_i++ % t[1].length] : t[1];
  return { name: t[0], detail };
}

export function buildGrid(el, { wave = true } = {}) {
  const frag = document.createDocumentFragment();
  const cells = [];
  for (let r = 0; r < ROWS; r++) {
    const row = MAP[r];
    for (let c = 0; c < COLS; c++) {
      const state = CLS[row[c]];
      const d = document.createElement('i');
      d.className = 'cell cell--' + state;
      d.dataset.s = state;
      const tip = tipFor(state);
      d.dataset.tip = tip.detail;
      d.dataset.cat = tip.name;
      if (wave) d.style.setProperty('--d', (c * 12 + r * 30) + 'ms');
      frag.appendChild(d);
      cells.push(d);
    }
  }
  el.appendChild(frag);
  return cells;
}

/* ---------- 鼠标透镜 + tooltip ---------- */
export function initLens(gmap, tip) {
  const cells = [...gmap.children];
  let centers = [], raf = 0, px = -1e4, py = -1e4, inside = false;

  const measure = () => {
    centers = cells.map(el => [el.offsetLeft + el.offsetWidth / 2,
                               el.offsetTop + el.offsetHeight / 2]);
  };
  measure();
  new ResizeObserver(measure).observe(gmap);

  const paint = () => {
    raf = 0;
    if (!inside) return;
    for (let i = 0; i < cells.length; i++) {
      const [cx, cy] = centers[i];
      const d = Math.hypot(cx - px, cy - py);
      // 透镜：圆心处放到 1.22，边缘线性回落到 1
      const s = d < LENS_R ? 1 + (LENS_MAX - 1) * (1 - d / LENS_R) : 1;
      cells[i].style.transform = s > 1.005 ? `scale(${s.toFixed(3)})` : '';
    }
  };
  const queue = () => { if (!raf) raf = requestAnimationFrame(paint); };

  gmap.addEventListener('pointermove', e => {
    const r = gmap.getBoundingClientRect();
    px = e.clientX - r.left; py = e.clientY - r.top;
    inside = true;
    queue();
  });
  gmap.addEventListener('pointerleave', () => {
    inside = false;
    cells.forEach(c => { c.style.transform = ''; });
    tip.classList.remove('on');
  });

  /* tooltip：跟随悬停格，读出假数据。
     gtip 定位参照 .panel，需加上 gmap 在面板内的偏移 */
  gmap.addEventListener('pointerover', e => {
    const c = e.target.closest('.cell');
    if (!c) { tip.classList.remove('on'); return; }
    tip.textContent = `${c.dataset.tip} · ${c.dataset.cat}`;
    const x = gmap.offsetLeft + c.offsetLeft + c.offsetWidth / 2;
    tip.style.left = Math.min(Math.max(x, 78), gmap.offsetLeft + gmap.clientWidth - 78) + 'px';
    // 顶部行的格子向上弹会被面板裁掉，翻转到格下弹出
    const below = c.offsetTop < 46;
    tip.classList.toggle('below', below);
    tip.style.top = (gmap.offsetTop + c.offsetTop + (below ? c.offsetHeight : 0)) + 'px';
    tip.classList.add('on');
  });
}

/* ---------- 图例 hover 联动 ---------- */
export function initLegend(legendEl, gmap) {
  const clear = () => {
    gmap.classList.remove('dim');
    for (const c of gmap.children) c.classList.remove('hit');
  };
  legendEl.addEventListener('pointerover', e => {
    const li = e.target.closest('[data-hit]');
    if (!li) { clear(); return; }   // 指针落在条目间隙时别留残留高亮
    gmap.classList.add('dim');
    for (const c of gmap.children)
      c.classList.toggle('hit', c.dataset.s === li.dataset.hit);
  });
  legendEl.addEventListener('pointerleave', clear);
}

/* ---------- 装饰用小图（步骤视觉 / bento 卡） ----------
   给一段状态字符画，生成小号格子阵 */
export function buildMini(el, rows, cols, states, { wave = true } = {}) {
  const frag = document.createDocumentFragment();
  for (let r = 0; r < rows; r++) {
    for (let c = 0; c < cols; c++) {
      const state = states[Math.min(r, states.length - 1)][c] || '.';
      const d = document.createElement('i');
      d.className = 'cell cell--' + (CLS[state] || 'free');
      if (wave) d.style.setProperty('--d', (c * 14 + r * 34) + 'ms');
      frag.appendChild(d);
    }
  }
  el.appendChild(frag);
}
