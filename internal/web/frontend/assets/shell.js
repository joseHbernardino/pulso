(() => {
  "use strict";
  const paths = {
    panel:'<rect x="3" y="3" width="18" height="18" rx="2"/><path d="M9 3v18"/>',
    more:'<circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/>',
    package:'<path d="m12 3 9 5v8l-9 5-9-5V8l9-5Zm0 9 9-4m-9 4-9-4m9 4v9m-5-15 10 5"/>',
    external:'<path d="M15 3h6v6m0-6-10 10M9 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-4"/>',
    folder:'<path d="M3 7h6l2 2h10v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Zm0 0V5a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v2"/>',
    grid:'<rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/>',
    users:'<path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2m20 0v-2a4 4 0 0 0-3-3.9M16 3a4 4 0 0 1 0 8"/><circle cx="9" cy="7" r="4"/>',
    user:'<circle cx="12" cy="8" r="4"/><path d="M4 21v-2a8 8 0 0 1 16 0v2"/>',
    server:'<rect x="3" y="3" width="18" height="7" rx="2"/><rect x="3" y="14" width="18" height="7" rx="2"/><path d="M7 6.5h.01M7 17.5h.01M17 7h-4m4 11h-4"/>',
    chevron:'<path d="m8 10 4 4 4-4"/>',menu:'<path d="M4 6h16M4 12h16M4 18h16"/>',close:'<path d="m6 6 12 12M6 18 18 6"/>',
    plus:'<path d="M12 5v14M5 12h14"/>',search:'<circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 4.5 4.5"/>',
    refresh:'<path d="M20 11a8 8 0 0 0-14-5L3 9m0-6v6h6M4 13a8 8 0 0 0 14 5l3-3m0 6v-6h-6"/>',
    play:'<path d="m9 7 8 5-8 5V7Z"/><circle cx="12" cy="12" r="10"/>',stop:'<rect x="7" y="7" width="10" height="10" rx="1"/><circle cx="12" cy="12" r="10"/>',
    alert:'<path d="M10.3 3.8 1.8 18.5a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.8a2 2 0 0 0-3.4 0Z"/><path d="M12 9v4m0 4h.01"/>',
    terminal:'<rect x="2" y="4" width="20" height="16" rx="3"/><path d="m6 9 3 3-3 3m7 0h5"/>',
    logs:'<path d="M8 3H5a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2V8l-5-5H8Z"/><path d="M15 3v6h6M7 13h10M7 17h7"/>',
    trash:'<path d="M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7m4-7v7"/>',
    edit:'<path d="m16 3 5 5M4 16 17 3a2 2 0 0 1 4 4L8 20l-5 1 1-5Z"/>',
    shield:'<path d="m12 3 8 3v6c0 5-8 9-8 9s-8-4-8-9V6l8-3Z"/><path d="m8 12 3 3 5-6"/>',
    eye:'<path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z"/><circle cx="12" cy="12" r="3"/>',
    check:'<path d="m5 12 4 4L19 6"/>',lock:'<rect x="5" y="10" width="14" height="11" rx="2"/><path d="M8 10V7a4 4 0 0 1 8 0v3m-4 5v2"/>',
    logout:'<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4m7 14 5-5-5-5m5 5H9"/>',
    arrow:'<path d="M5 12h14m-6-6 6 6-6 6"/>',clock:'<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>'
  };
  function icon(name){const el=document.createElementNS("http://www.w3.org/2000/svg","svg");el.setAttribute("viewBox","0 0 24 24");el.setAttribute("class","icon");el.setAttribute("aria-hidden","true");el.innerHTML=paths[name]||paths.grid;return el;}
  function hydrate(scope=document){scope.querySelectorAll("[data-icon]").forEach(el=>{el.replaceChildren(icon(el.dataset.icon));});}
  function notify(message,type="success"){let host=document.getElementById("toast-container");if(!host){host=document.createElement("div");host.id="toast-container";host.setAttribute("role","status");host.setAttribute("aria-live","polite");document.body.append(host);}const toast=document.createElement("div");toast.className="toast toast-"+type;const label=document.createElement("span");label.textContent=message;toast.append(icon(type==="error"?"alert":"check"),label);host.append(toast);setTimeout(()=>{toast.classList.add("toast-out");setTimeout(()=>toast.remove(),200);},4500);}
  window.PulsoUI={icon,hydrate,notify};
  const mount = document.getElementById("app-sidebar");
  if (mount) {
    const section = document.body.dataset.page || "applications";
    const users = section === "users", projects = section === "projects";
    const navItem = (href, label, symbol, active, extra = "") => `<a class="nav-item ${active ? "is-active" : ""}" href="${href}" title="${label}" ${active ? 'aria-current="page"' : ""} ${extra}><span data-icon="${symbol}"></span><span class="nav-label">${label}</span></a>`;
    mount.innerHTML = `<a class="brand" href="/" aria-label="Pulso, início"><span class="brand-mark"><img src="/assets/pulso-mark.svg" alt=""></span><span class="brand-copy"><span class="brand-word">pulso</span><span class="brand-caption">Gerenciador de aplicações</span></span></a>
      <div class="workspace"><span class="workspace-symbol" data-icon="server"></span><div><strong>Servidor local</strong><small>Espaço de trabalho</small></div><span class="workspace-indicator" aria-hidden="true"></span></div>
      <nav aria-label="Navegação principal"><p class="nav-group-label">Espaço de trabalho</p>${navItem("/projects", "Projetos", "folder", projects)}${navItem("/processes", "Processos", "terminal", !users && !projects)}<p class="nav-group-label settings-label" data-user-nav-only hidden>Equipe</p>${navItem("/users", "Usuários e acesso", "users", users, 'data-admin-only data-user-nav-only hidden')}</nav>
      <div class="sidebar-bottom"><div class="sidebar-health"><span class="status-dot" aria-hidden="true"></span>Ambiente local</div><details class="account-menu" id="account-menu"><summary title="Menu da sua conta"><span class="avatar account-avatar" aria-hidden="true" data-account-initials>P</span><span class="account-label"><strong id="account-name">Sua conta</strong><small id="account-role">Verificando acesso…</small></span><span data-icon="chevron"></span></summary><div class="account-dropdown"><p>Conectado ao Pulso</p><button id="account-logout" type="button" hidden><span data-icon="logout"></span>Sair da conta</button></div></details><div class="sidebar-footer"><span>Pulso</span><span>Gerenciador de aplicações</span></div></div>`;
    mount.querySelector('a[href="/users"]').classList.add("nav-users");
    const header = document.getElementById("app-header");
    if (header) header.innerHTML = `<button class="btn btn-icon mobile-menu" id="navigation-toggle" aria-label="Recolher navegação" aria-expanded="true" aria-controls="app-sidebar"><span data-icon="panel"></span></button><span class="header-separator" aria-hidden="true"></span><nav class="breadcrumb" aria-label="Localização"><span>Espaço de trabalho</span><span class="breadcrumb-divider">/</span><strong>${users ? "Usuários e acesso" : projects ? "Projetos" : "Processos"}</strong></nav><div class="header-right"><span class="server-label"><span data-icon="server"></span>Local</span><details class="theme-menu"><summary class="btn btn-icon btn-ghost" aria-label="Escolher tema" title="Escolher tema"><span data-theme-icon></span></summary><div class="theme-options"><button type="button" data-set-theme="dark">Escuro</button><button type="button" data-set-theme="light">Claro</button><button type="button" data-set-theme="system">Sistema</button></div></details></div>`;
    const toggle = document.getElementById("navigation-toggle");
    const mobile = matchMedia("(max-width:920px)");
    try { document.body.classList.toggle("sidebar-collapsed", localStorage.getItem("pulso-sidebar-collapsed") === "true"); } catch (_) { /* Optional preference. */ }
    function syncMenu() {
      const open = document.body.classList.contains("navigation-open");
      mount.inert = mobile.matches && !open;
      const expanded = mobile.matches ? open : !document.body.classList.contains("sidebar-collapsed");
      toggle?.setAttribute("aria-expanded", String(expanded));
      toggle?.setAttribute("aria-label", expanded ? "Recolher navegação" : "Abrir navegação");
    }
    function closeMenu() {
      const wasOpen = document.body.classList.contains("navigation-open");
      document.body.classList.remove("navigation-open"); syncMenu();
      if (wasOpen) toggle?.focus();
    }
    mobile.addEventListener("change", () => { document.body.classList.remove("navigation-open"); syncMenu(); });
    toggle?.addEventListener("click", () => {
      if (mobile.matches) {
        const open = document.body.classList.toggle("navigation-open"); syncMenu();
        if (open) mount.querySelector("a")?.focus();
      } else {
        const collapsed = document.body.classList.toggle("sidebar-collapsed");
        try { localStorage.setItem("pulso-sidebar-collapsed", String(collapsed)); } catch (_) { /* Optional preference. */ }
        syncMenu();
      }
    });
    document.getElementById("navigation-overlay")?.addEventListener("click", closeMenu);
    document.addEventListener("keydown", event => {
      if (event.key === "Tab" && mobile.matches && document.body.classList.contains("navigation-open")) {
        const items = [...mount.querySelectorAll('a[href],button,summary')].filter(el => {
          const closed = el.closest("details:not([open])");
          return !el.disabled && el.getClientRects().length && (!closed || closed.querySelector(":scope > summary") === el);
        });
        const first = items[0], last = items[items.length - 1];
        if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus(); }
        else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus(); }
      }
      if (event.key === "Escape") { closeMenu(); document.getElementById("account-menu")?.removeAttribute("open"); }
    });
    document.addEventListener("click", event => {
      const menu = document.getElementById("account-menu");
      if (menu?.open && !menu.contains(event.target)) menu.open = false;
    });
    syncMenu(); window.PulsoTheme?.refresh();
  }
  hydrate();
})();
