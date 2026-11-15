/* ============================================================
   main.js · 入口
   全部原生 ES module，无依赖。
   ============================================================ */
import { initTheme } from './theme.js';
import { initReveal } from './reveal.js';
import { initSteps } from './steps.js';
import { initCountUp } from './countup.js';
import { initHold } from './hold.js';
import { buildGrid, buildMini, initLens, initLegend } from './grid.js';

const $ = s => document.querySelector(s);

/* ---------- Hero 格子图 ---------- */
const gmap = $('#gmap');
if (gmap) {
  buildGrid(gmap);
  initLens(gmap, $('#gtip'));
  initLegend($('#legend'), gmap);
  gmap.classList.add('run');          // 触发波浪入场（各格延迟写在 --d）
}

/* ---------- 装饰小图：进入视口才播入场 ---------- */
function runOnView(el) {
  if (!el) return;
  if (!('IntersectionObserver' in window)) { el.classList.add('run'); return; }
  const io = new IntersectionObserver(entries => {
    for (const en of entries) {
      if (en.isIntersecting) { en.target.classList.add('run'); io.disconnect(); }
    }
  }, { threshold: 0.3 });
  io.observe(el);
}

/* 步骤 pane1：扫描中的迷你盘（sys 灰块 + 蓝带刚扫出来） */
buildMini($('#minimap1'), 5, 14, [
  'ssssssssssssss',
  'ssssssssssssss',
  'sssssssssstttt',
  'ttttttcccccccc',
  'cccccccccmmmwb',
]);
/* 步骤 pane4：清完的盘（几乎全回到可用色） */
buildMini($('#minimap2'), 5, 14, [
  'ssssssssssssss',
  'sssssssstttttt',
  'tttt..........',
  '..............',
  'bb..........tt',
]);
/* 格子地图卡的迷你示意 */
buildMini($('#cardmap'), 3, 22, [
  'ssssssssssssssssssssss',
  'ssssssssccccccccccmmwt',
  'ffffffttttttttbb......',
]);
runOnView($('#minimap1'));
runOnView($('#minimap2'));
runOnView($('#cardmap'));

/* ---------- 卡片指针光斑：写 --mx/--my ----------
   这两个属性已在 tokens 里注册 @property，
   CSS 里 transition 它们得到"追光"迟滞感。 */
document.querySelectorAll('.card').forEach(card => {
  card.addEventListener('pointermove', e => {
    const r = card.getBoundingClientRect();
    card.style.setProperty('--mx', (e.clientX - r.left) + 'px');
    card.style.setProperty('--my', (e.clientY - r.top) + 'px');
  });
});

/* ---------- 导航毛玻璃：哨兵越过视口顶就切态 ---------- */
const sentinel = $('.sentinel'), nav = $('#nav');
if (sentinel && nav && 'IntersectionObserver' in window) {
  new IntersectionObserver(([en]) => {
    nav.classList.toggle('nav--glass', !en.isIntersecting);
  }, { threshold: 0 }).observe(sentinel);
} else if (nav) {
  nav.classList.add('nav--glass');
}

initTheme();
initReveal();
initSteps();
initCountUp();
initHold();

/* ---------- 锚点重校 ----------
   格子图等是 JS 后构建的，浏览器在脚本前完成的锚点跳转
   会因布局增长而偏出目标。构建完成后立即重锚一次。 */
if (location.hash) {
  const t = document.querySelector(location.hash);
  if (t) {
    requestAnimationFrame(() =>
      t.scrollIntoView({ behavior: 'instant', block: 'start' }));
  }
}
