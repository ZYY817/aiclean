/* ============================================================
   countup.js · 数字一次性 count-up
   IO 触发，1600ms easeOutExpo，终值写死在 data-n。
   ============================================================ */

const DUR = 1600;
const easeOutExpo = t => (t >= 1 ? 1 : 1 - Math.pow(2, -10 * t));

export function initCountUp() {
  const els = document.querySelectorAll('.cnt[data-n]');
  if (!els.length) return;

  const run = el => {
    const target = +el.dataset.n;
    // DOM 里写死的即是终值（无 JS/IO 兜底）；触发时从 0 数上去
    if (target === 0 ||
        matchMedia('(prefers-reduced-motion: reduce)').matches) {
      el.textContent = target;
      return;
    }
    el.textContent = 0;
    const t0 = performance.now();
    const tick = now => {
      const p = Math.min((now - t0) / DUR, 1);
      el.textContent = Math.round(target * easeOutExpo(p));
      if (p < 1) requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  };

  if (!('IntersectionObserver' in window)) { els.forEach(run); return; }
  const io = new IntersectionObserver(entries => {
    for (const en of entries) {
      if (en.isIntersecting) { run(en.target); io.unobserve(en.target); }
    }
  }, { threshold: 0.6 });
  els.forEach(el => io.observe(el));
}
