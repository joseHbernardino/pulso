(() => {
  "use strict";
  const $=id=>document.getElementById(id);
  const list=$("user-list"),feedback=$("users-feedback"),dialog=$("user-dialog"),form=$("user-form"),error=$("user-error"),save=$("user-save");
  let editing=null,busy=false,users=[],status="all",loaded=false;
  async function request(path,method="GET",body){
    const response=await fetch(path,{method,headers:{"Content-Type":"application/json"},...(body?{body:JSON.stringify(body)}:{})});
    const data=await response.json().catch(()=>({}));
    if(response.status===401){location.replace("/login");throw new Error("Sua sessão expirou.");}
    if(!response.ok)throw new Error(({"this username already exists":"Este nome de usuário já está em uso.","keep at least one enabled administrator":"Mantenha pelo menos um administrador ativo.","invalid user data":"Confira os dados: nome de usuário válido e senha com 12 a 72 caracteres.","user not found":"Usuário não encontrado.","account storage unavailable":"Não foi possível acessar as contas. Tente novamente.","administrator permission required":"Esta ação exige um administrador."}[data.error]||data.error||"Não foi possível concluir a ação."));return data;
  }
  function notice(message,failed=false){feedback.hidden=false;feedback.classList.toggle("error",failed);feedback.textContent=message;PulsoUI.notify(message,failed?"error":"success");}
  function render(){
    const query=$("user-search").value.trim().toLocaleLowerCase("pt-BR"),role=$("user-role-filter").value;
    const shown=users.filter(u=>u.username.toLocaleLowerCase("pt-BR").includes(query)&&(role==="all"||u.role===role)&&(status==="all"||(status==="disabled")===u.disabled));
    $("users-total").textContent=users.length;$("users-count").textContent=users.length;$("users-active").textContent=users.filter(u=>!u.disabled).length;$("users-admin").textContent=users.filter(u=>u.role==="admin").length;$("users-view").textContent=users.filter(u=>u.role==="view").length;$("users-visible-count").textContent=shown.length+" de "+users.length+" usuários";
    list.replaceChildren();
    if(!shown.length){const cell=list.insertRow().insertCell();cell.colSpan=5;const empty=document.createElement("div");empty.className="empty-state";const symbol=document.createElement("div");symbol.className="empty-symbol";symbol.append(PulsoUI.icon("users"));const title=document.createElement("h3");title.textContent="Nenhum usuário encontrado";const desc=document.createElement("p");desc.textContent="Ajuste a busca ou selecione outro perfil ou status.";empty.append(symbol,title,desc);cell.append(empty);return;}
    for(const user of shown){
      const row=list.insertRow(),meta=document.createElement("div");meta.className="user-cell";
      const avatar=document.createElement("span");avatar.className="avatar avatar-"+user.role;avatar.textContent=user.username.slice(0,2).toLocaleUpperCase("pt-BR");avatar.setAttribute("aria-hidden","true");
      const text=document.createElement("div"),name=document.createElement("strong");name.textContent=user.username;
      if(user.username===window.pulsoCurrentAccount?.username){const self=document.createElement("span");self.className="self-label";self.textContent="Você";name.append(self);}
      const sub=document.createElement("small");sub.textContent=user.role==="admin"?"Gerenciamento completo":"Acompanhamento do painel";text.append(name,sub);meta.append(avatar,text);row.insertCell().append(meta);
      const roleBadge=document.createElement("span");roleBadge.className="badge badge-"+user.role;roleBadge.append(PulsoUI.icon(user.role==="admin"?"shield":"eye"),document.createTextNode(user.role==="admin"?"Administrador":"Visualização"));row.insertCell().append(roleBadge);
      const badge=document.createElement("span");badge.className="badge badge-"+(user.disabled?"disabled":"active");const dot=document.createElement("span");dot.className="dot";dot.setAttribute("aria-hidden","true");badge.append(dot,document.createTextNode(user.disabled?"Desativado":"Ativo"));row.insertCell().append(badge);
      const date=row.insertCell();date.className="user-date";const created=new Date(user.created_at);date.textContent=Number.isNaN(created.getTime())?"—":created.toLocaleDateString("pt-BR",{day:"2-digit",month:"short",year:"numeric"});
      const actions=document.createElement("div");actions.className="actions";
      const edit=document.createElement("button");edit.className="btn btn-icon";edit.type="button";edit.title="Editar usuário";edit.setAttribute("aria-label","Editar "+user.username);edit.append(PulsoUI.icon("edit"));edit.onclick=()=>open(user);
      const toggle=document.createElement("button");toggle.className="btn text-action"+(user.disabled?"":" btn-danger");toggle.type="button";const label=user.disabled?"Ativar":"Desativar";toggle.setAttribute("aria-label",label+" "+user.username);toggle.title=label+" usuário";const labelNode=document.createElement("span");labelNode.textContent=label;toggle.append(PulsoUI.icon(user.disabled?"check":"lock"),labelNode);
      toggle.onclick=async()=>{toggle.disabled=true;try{await request("/api/v1/users/"+user.id,"PATCH",{disabled:!user.disabled});notice(user.disabled?"Acesso ativado.":"Acesso desativado. As sessões deste usuário foram encerradas.");await load();}catch(e){notice(e.message,true);}finally{toggle.disabled=false;}};
      actions.append(edit,toggle);row.insertCell().append(actions);list.append(row);
    }
  }
  async function load(){try{const next=await request("/api/v1/users");if(!loaded||JSON.stringify(next)!==JSON.stringify(users)){users=next||[];render();loaded=true;}$("last-updated").textContent="Atualizado às "+new Date().toLocaleTimeString("pt-BR");}catch(e){notice(e.message,true);}}
  function open(user){
    editing=user||null;form.reset();error.hidden=true;$("user-title").textContent=user?"Editar usuário":"Novo usuário";
    const username=$("user-username"),password=$("user-password");username.value=user?.username||"";username.readOnly=Boolean(user);$("user-role").value=user?.role||"view";password.required=!user;
    $("user-password-help").textContent=user?"Deixe vazio para manter a senha. Uma nova senha encerra as sessões existentes.":"Use pelo menos 12 caracteres.";
    save.textContent=user?"Salvar alterações":"Criar usuário";dialog.showModal();(user?$("user-role"):username).focus();
  }
  $("user-new").onclick=()=>open();for(const id of ["user-cancel","user-close"])$(id).onclick=()=>{if(!busy)dialog.close();};dialog.addEventListener("cancel",e=>{if(busy)e.preventDefault();});dialog.addEventListener("close",()=>form.reset());
  form.addEventListener("submit",async event=>{
    event.preventDefault();if(busy||!form.reportValidity())return;busy=true;error.hidden=true;
    const payload={role:$("user-role").value},password=$("user-password").value;if(password)payload.password=password;if(!editing)payload.username=$("user-username").value.trim();
    form.querySelectorAll("input,select,button").forEach(el=>el.disabled=true);$("user-close").disabled=true;save.textContent="Salvando…";
    try{await request(editing?"/api/v1/users/"+editing.id:"/api/v1/users",editing?"PATCH":"POST",payload);dialog.close();notice(editing?"Alterações salvas.":"Usuário criado.");await load();}
    catch(e){error.textContent=e.message;error.hidden=false;}
    finally{busy=false;form.querySelectorAll("input,select,button").forEach(el=>el.disabled=false);$("user-close").disabled=false;save.textContent=editing?"Salvar alterações":"Criar usuário";}
  });
  $("user-search").addEventListener("input",render);$("user-role-filter").addEventListener("change",render);
  document.querySelectorAll("[data-user-status]").forEach(btn=>btn.onclick=()=>{status=btn.dataset.userStatus;document.querySelectorAll("[data-user-status]").forEach(tab=>{const active=btn===tab;tab.classList.toggle("is-active",active);tab.setAttribute("aria-pressed",String(active));});render();});
  const applyAccess=state=>{if(state.role!=="admin"){location.replace("/");return;}load();};
  document.addEventListener("pulso:access-ready",event=>applyAccess(event.detail));if(window.pulsoCurrentAccount)applyAccess(window.pulsoCurrentAccount);
})();
