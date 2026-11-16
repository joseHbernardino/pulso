(() => {
  "use strict";
  window.pulsoCanManage = false;
  async function loadAccess() {
    try {
      const response = await fetch("/api/auth/status", {cache:"no-store"});
      if(!response.ok) throw new Error("account status unavailable");
      const state = await response.json();
      if(state.mode !== "disabled" && !state.authenticated) { location.replace("/login"); return; }
      window.pulsoCanManage = state.mode === "disabled" || state.role === "admin";
      window.pulsoCurrentAccount = state;
      document.querySelectorAll("[data-admin-only]").forEach(el => el.hidden = !window.pulsoCanManage);
      const name = document.getElementById("account-name"),role=document.getElementById("account-role"),logout=document.getElementById("account-logout");
      if(name) name.textContent=state.username || "Acesso local";
      document.querySelectorAll("[data-account-initials]").forEach(el=>el.textContent=(state.username||"PL").slice(0,2).toLocaleUpperCase("pt-BR"));
      if(role) role.textContent=window.pulsoCanManage ? "Administrador" : "Visualização";
      if(logout) logout.hidden=state.mode==="disabled";
      if(state.mode!=="users") document.querySelectorAll(".nav-users").forEach(el=>el.hidden=true);
      document.dispatchEvent(new CustomEvent("pulso:access-ready", {detail:state}));
    } catch(_) { if(document.getElementById("account-role")) document.getElementById("account-role").textContent="Verificando acesso…"; }
  }
  document.getElementById("account-logout")?.addEventListener("click", async () => {
    const response=await fetch("/api/auth/logout",{method:"POST"});
    if(response.ok) location.replace("/login");
  });
  loadAccess();
  setInterval(loadAccess,15000);
})();
