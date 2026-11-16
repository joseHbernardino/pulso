(() => {
  "use strict";
  const paths = {
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
  const mount=document.getElementById("app-sidebar");
  if(mount){const section=document.body.dataset.page||"applications";const users=section==="users";mount.innerHTML=`<a class="brand" href="/" aria-label="Pulso, aplicações"><img src="/assets/pulso-mark.svg" alt=""><span class="brand-word">pulso</span></a><div class="workspace"><span class="workspace-symbol" data-icon="server"></span><div><strong>Este servidor</strong><small>Espaço de trabalho</small></div><span class="workspace-indicator" aria-hidden="true"></span></div><nav aria-label="Navegação principal"><p class="nav-group-label">Gerenciar</p><a class="nav-item ${!users?"is-active":""}" href="/" ${!users?'aria-current="page"':''}><span data-icon="grid"></span>Aplicações</a><a class="nav-item nav-users ${users?"is-active":""}" href="/users" data-admin-only hidden ${users?'aria-current="page"':''}><span data-icon="users"></span>Usuários e acesso</a></nav><div class="sidebar-bottom"><div class="sidebar-signature"><span class="signature-icon" data-icon="play"></span><div><strong>No ritmo certo.</strong><p>Suas aplicações, sob controle.</p></div></div><div class="sidebar-footer"><span>Pulso</span><span>Gerenciador de aplicações</span></div></div>`;
    const header=document.getElementById("app-header");if(header)header.innerHTML=`<button class="btn btn-icon mobile-menu" id="navigation-toggle" aria-label="Abrir navegação" aria-expanded="false" aria-controls="app-sidebar"><span data-icon="menu"></span></button><nav class="breadcrumb" aria-label="Localização"><span data-icon="grid"></span><span>Espaço de trabalho</span><span class="breadcrumb-divider">/</span><strong>${users?"Usuários e acesso":"Aplicações"}</strong></nav><div class="header-right"><span class="server-label"><span data-icon="server"></span>Servidor local</span><details class="account-menu" id="account-menu"><summary title="Menu da sua conta"><span class="avatar account-avatar" aria-hidden="true" data-account-initials>P</span><span class="account-label"><strong id="account-name">Sua conta</strong><small id="account-role">Verificando acesso…</small></span><span data-icon="chevron"></span></summary><div class="account-dropdown"><p>Conectado ao Pulso</p><button id="account-logout" type="button" hidden><span data-icon="logout"></span>Sair da conta</button></div></details></div>`;
    const toggle=document.getElementById("navigation-toggle");
    const mobile=matchMedia("(max-width:920px)");
    const overlay=document.getElementById("navigation-overlay");
    function syncMenu(){const open=document.body.classList.contains("navigation-open");mount.inert=mobile.matches&&!open;toggle?.setAttribute("aria-expanded",String(open));}
    function closeMenu(){const wasOpen=document.body.classList.contains("navigation-open");document.body.classList.remove("navigation-open");syncMenu();if(wasOpen)toggle?.focus();}
    mobile.addEventListener("change",()=>{document.body.classList.remove("navigation-open");syncMenu();});syncMenu();
    toggle?.addEventListener("click",()=>{const open=document.body.classList.toggle("navigation-open");syncMenu();if(open)mount.querySelector("a")?.focus();});document.getElementById("navigation-overlay")?.addEventListener("click",closeMenu);document.addEventListener("keydown",e=>{if(e.key==="Tab"&&mobile.matches&&document.body.classList.contains("navigation-open")){const links=[...mount.querySelectorAll("a[href]")].filter(el=>!el.hidden);const first=links[0],last=links[links.length-1];if(e.shiftKey&&document.activeElement===first){e.preventDefault();last.focus();}else if(!e.shiftKey&&document.activeElement===last){e.preventDefault();first.focus();}}if(e.key==="Escape"){closeMenu();document.getElementById("account-menu")?.removeAttribute("open");}});document.addEventListener("click",e=>{const menu=document.getElementById("account-menu");if(menu?.open&&!menu.contains(e.target))menu.open=false;});
  }
  hydrate();
})();
