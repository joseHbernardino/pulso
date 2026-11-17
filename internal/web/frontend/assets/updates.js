(() => {
  "use strict";
  const header=document.querySelector(".header-right");
  if(!header)return;
  const badge=document.createElement("button");badge.type="button";badge.className="version-badge";badge.textContent="Versão…";badge.title="Versão e atualizações";header.prepend(badge);
  const dialog=document.createElement("dialog");dialog.id="update-dialog";dialog.className="update-dialog";dialog.setAttribute("aria-labelledby","update-title");
  dialog.innerHTML=`<div class="update-heading"><div><h2 id="update-title">Versão e atualizações</h2><p>Acompanhe a versão instalada do Pulso.</p></div><button class="btn btn-icon" type="button" id="update-close" aria-label="Fechar atualizações"></button></div><div class="update-content"><div class="installed-version"><span class="installed-label">Versão instalada</span><strong id="installed-version">Carregando…</strong><span id="installed-date"></span><span id="installed-revision"></span></div><section id="update-admin" hidden><div class="update-available"><div><h3>Última versão estável</h3><p id="latest-version">Ainda não verificada</p></div><button type="button" class="btn" id="update-check">Verificar atualização</button></div><p id="update-guidance"></p><a id="update-release" target="_blank" rel="noopener noreferrer" hidden>Ver publicação da versão</a><p id="update-notice" role="status"></p><section id="update-progress" hidden aria-label="Progresso da atualização"><h3 id="update-progress-title"></h3><p id="update-progress-message" role="status"></p><ol><li data-update-step="downloading">Baixar e verificar imagens</li><li data-update-step="updating_portal">Atualizar o Pulso e recuperar aplicações</li><li data-update-step="updating_agent">Atualizar o serviço auxiliar</li></ol></section><div id="update-confirmation" class="update-confirmation" hidden><h3>Confirmar atualização</h3><p id="update-confirmation-text"></p><p>O Pulso e suas aplicações serão reiniciados. Volumes e configurações serão preservados. Se a nova imagem não passar no health check, o sistema tentará restaurar os contêineres anteriores.</p><div class="update-actions"><button class="btn" type="button" id="update-cancel">Voltar</button><button class="btn btn-primary" type="button" id="update-confirm">Confirmar atualização</button></div></div><div class="update-actions" id="update-actions"><button type="button" class="btn btn-primary" id="update-install" disabled>Atualizar Pulso</button></div></section><p id="update-viewer" hidden>Entre com uma conta de administrador para verificar e instalar atualizações.</p></div>`;
  document.body.append(dialog);
  const $=id=>document.getElementById(id);
  $("update-close").append(PulsoUI.icon("close"));
  let build=null,state=null,working=false,checking=false,poll=null,returnFocus=null;
  const isAdmin=()=>window.pulsoCurrentAccount?.mode==="users"&&window.pulsoCurrentAccount?.role==="admin";
  async function request(path,options={}){
    const response=await fetch("/api/v1/system/"+path,{cache:"no-store",...options,headers:{"Content-Type":"application/json",...options.headers}});
    if(response.status===401){location.replace("/login");throw new Error("Entre novamente para acompanhar a atualização.");}
    const data=await response.json();if(!response.ok)throw new Error(data.error||"Não foi possível concluir a operação.");return data;
  }
  function setNotice(message,error=false){$("update-notice").textContent=message;$("update-notice").classList.toggle("is-error",error);}
  async function loadVersion(){
    try{
      const data=await request("version");build=data;
      const b=data.build,revision=b.revision&&!["unknown","local"].includes(b.revision)?b.revision.slice(0,8):"";
      const label=b.version==="dev"?"dev"+(revision?" · "+revision:" · local"):b.version;
      badge.textContent=label;badge.setAttribute("aria-label",label+". Versão e atualizações do Pulso");
      $("installed-version").textContent="Pulso "+label;
      $("installed-revision").textContent=revision?"Código "+revision:"";
      const date=new Date(b.build_time);$("installed-date").textContent=Number.isNaN(date.getTime())?"":"Compilado em "+date.toLocaleString("pt-BR");
      const footer=document.querySelector(".sidebar-footer>span:last-child");if(footer)footer.textContent=label;
      $("update-admin").hidden=!isAdmin();$("update-viewer").hidden=isAdmin();
      if(isAdmin()&&!data.container){$("update-guidance").textContent="Esta instalação usa o binário. Atualize pelo terminal; a atualização pelo painel é destinada ao Docker.";$("update-check").disabled=true;}
    }catch(error){badge.textContent="Versão indisponível";badge.title=error.message;}
  }
  function render(){
    if(!state)return;
    const job=state.job,active=job?.state==="running";
    working=Boolean(active);
    badge.classList.toggle("has-update",Boolean(state.has_update));badge.classList.toggle("is-updating",working);
    badge.title=working?"Atualização em andamento":state.has_update?"Atualização disponível: "+state.latest_version:"Versão e atualizações";
    badge.setAttribute("aria-label",badge.textContent+". "+badge.title);
    $("latest-version").textContent=state.latest_version||"Ainda não verificada";
    $("update-guidance").textContent=!state.available?(state.reason||"Atualização indisponível nesta instalação."):state.has_update?"Uma nova versão está disponível para esta instalação.":state.latest_version?"A instalação já está nesta versão ou em uma versão mais recente.":"Verifique se existe uma nova versão publicada.";
    $("update-check").disabled=working||checking||!build?.container||!build?.updates_configured;
    $("update-install").disabled=working||!state.available||!state.has_update||Boolean(state.check_error);
    $("update-install").textContent=working?"Atualização em andamento":state.has_update?"Atualizar para "+state.latest_version:"Atualizar Pulso";
    const release=$("update-release");
    let safeURL=null;try{const u=new URL(state.release_url);if(u.protocol==="https:"&&u.hostname==="github.com")safeURL=u.href;}catch(_){/* No publication link yet. */}
    release.hidden=!safeURL;if(safeURL)release.href=safeURL;
    $("update-progress").hidden=!job;
    if(job){
      const titles={running:"Atualização em andamento",completed:"Atualização concluída",rolled_back:"Versão anterior restaurada",failed:"Atualização não concluída"};
      $("update-progress-title").textContent=titles[job.state]||"Última atualização";
      if($("update-progress-message").textContent!==job.message)$("update-progress-message").textContent=job.message;
      $("update-progress").classList.toggle("has-error",["failed","rolled_back"].includes(job.state));
      const stages=["downloading","updating_portal","updating_agent"],index=stages.indexOf(job.step==="queued"?"downloading":job.step);
      dialog.querySelectorAll("[data-update-step]").forEach((el,i)=>{el.classList.toggle("is-current",active&&i===index);el.classList.toggle("is-done",job.state==="completed"||i<index);});
      if(active){sessionStorage.setItem("pulso-active-update",job.id);startPolling();}
      else{stopPolling();if(sessionStorage.getItem("pulso-active-update")===job.id){sessionStorage.removeItem("pulso-active-update");loadVersion();if(job.state==="completed")setTimeout(()=>location.reload(),1500);}}
    }
    if(state.check_error)setNotice("Não foi possível verificar a publicação: "+state.check_error,true);
  }
  async function loadStatus(check=false){
    if(!isAdmin())return;
    try{state=await request(check?"updates/check":"updates",check?{method:"POST"}:{});setNotice("");render();}
    catch(error){setNotice(working?"O Pulso está reiniciando. Aguardando o retorno do painel…":error.message,!working);}
  }
  function startPolling(){if(!poll)poll=setInterval(()=>loadStatus(),3000);}
  function stopPolling(){clearInterval(poll);poll=null;}
  badge.addEventListener("click",async()=>{
    returnFocus=document.activeElement;dialog.showModal();$("update-close").focus();
    await loadVersion();if(isAdmin())await loadStatus();
  });
  $("update-close").addEventListener("click",()=>dialog.close());
  dialog.addEventListener("close",()=>{$("update-confirmation").hidden=true;$("update-actions").hidden=false;returnFocus?.focus();});
  $("update-check").addEventListener("click",async()=>{checking=true;$("update-check").disabled=true;setNotice("Verificando a publicação mais recente…");await loadStatus(true);checking=false;render();});
  $("update-install").addEventListener("click",()=>{
    $("update-confirmation-text").textContent="Instalar "+state.latest_version+" nesta máquina?";
    $("update-confirmation").hidden=false;$("update-actions").hidden=true;$("update-cancel").focus();
  });
  $("update-cancel").addEventListener("click",()=>{$("update-confirmation").hidden=true;$("update-actions").hidden=false;$("update-install").focus();});
  $("update-confirm").addEventListener("click",async()=>{
    $("update-confirm").disabled=true;$("update-cancel").disabled=true;
    try{
      const job=await request("updates",{method:"POST",body:JSON.stringify({version:state.latest_version})});
      state.job=job;$("update-confirmation").hidden=true;$("update-actions").hidden=false;render();
    }catch(error){setNotice(error.message,true);}
    finally{$("update-confirm").disabled=false;$("update-cancel").disabled=false;}
  });
  document.addEventListener("pulso:access-ready",async()=>{await loadVersion();if(isAdmin()&&!state)loadStatus(Boolean(build?.container&&build?.updates_configured));});
  loadVersion().then(()=>{if(isAdmin())loadStatus(Boolean(build?.container&&build?.updates_configured));});
  setInterval(()=>{if(isAdmin()&&build?.updates_configured&&!working&&!document.hidden)loadStatus(true);},15*60*1000);
})();
