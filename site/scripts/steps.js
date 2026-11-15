/* ============================================================
   steps.js · 四步滚动叙事
   IO 监测每个 step 是否进入视口中线（上下各裁 45%），
   命中即切 .active + 右栏对应 pane crossfade。
   ============================================================ */

export function initSteps() {
  const steps = document.querySelectorAll('.step');
  const panes = document.querySelectorAll('.stepviz__pane');
  if (!steps.length || !panes.length) return;

  const activate = i => {
    steps.forEach((s, j) => s.classList.toggle('active', j === i));
    panes.forEach((p, j) => p.classList.toggle('is-on', j === i));
  };

  if (!('IntersectionObserver' in window)) { activate(0); return; }

  const io = new IntersectionObserver(entries => {
    for (const en of entries) {
      if (en.isIntersecting) activate([...steps].indexOf(en.target));
    }
  }, {
    // 只在视口中央一条窄带内算"进入"：滚过哪步高亮哪步
    rootMargin: '-45% 0px -45% 0px',
    threshold: 0,
  });
  steps.forEach(s => io.observe(s));
  activate(0);
}
