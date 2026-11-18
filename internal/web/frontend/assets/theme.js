(() => {
  "use strict";
  const system = matchMedia("(prefers-color-scheme: dark)");
  let choice = "dark";
  try { const saved = localStorage.getItem("pulso-theme"); if (["dark", "light", "system"].includes(saved)) choice = saved; } catch (_) { /* Storage can be disabled. */ }
  const icons = {
    dark:'<path d="M20.9 13.1A9 9 0 0 1 10.9 3.1a9 9 0 1 0 10 10Z"/>',
    light:'<circle cx="12" cy="12" r="4"/><path d="M12 2v2m0 16v2M2 12h2m16 0h2M5 5l1.5 1.5m11 11L19 19M5 19l1.5-1.5m11-11L19 5"/>',
    system:'<rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8m-4-4v4"/>'
  };
  function refresh() {
    const theme = choice === "system" ? (system.matches ? "dark" : "light") : choice;
    document.documentElement.dataset.theme = theme;
    document.documentElement.classList.toggle("dark", theme === "dark");
    document.querySelector('meta[name="theme-color"]')?.setAttribute("content", theme === "dark" ? "#09090b" : "#fafafa");
    document.querySelectorAll("[data-set-theme]").forEach(button => button.setAttribute("aria-pressed", String(button.dataset.setTheme === choice)));
    document.querySelectorAll("[data-theme-icon]").forEach(host => { host.innerHTML = '<svg class="icon" viewBox="0 0 24 24" aria-hidden="true">' + icons[choice] + '</svg>'; });
  }
  document.addEventListener("click", event => {
    const button = event.target.closest("[data-set-theme]");
    if (button && ["dark", "light", "system"].includes(button.dataset.setTheme)) {
      choice = button.dataset.setTheme;
      try { localStorage.setItem("pulso-theme", choice); } catch (_) { /* Keep the current page usable. */ }
      refresh(); const menu = button.closest("details"); if (menu) { menu.open = false; menu.querySelector("summary")?.focus(); }
    }
    document.querySelectorAll(".theme-menu[open]").forEach(menu => { if (!menu.contains(event.target)) menu.open = false; });
  });
  document.addEventListener("keydown", event => { if (event.key === "Escape") document.querySelectorAll(".theme-menu[open]").forEach(menu => { menu.open = false; menu.querySelector("summary")?.focus(); }); });
  system.addEventListener("change", refresh);
  window.PulsoTheme = { refresh };
  refresh(); document.addEventListener("DOMContentLoaded", refresh);
})();
