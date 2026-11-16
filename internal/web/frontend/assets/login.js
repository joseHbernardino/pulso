(() => {
  "use strict";
  const form=document.getElementById("login-form"),username=document.getElementById("username"),password=document.getElementById("password"),button=document.getElementById("login-btn"),error=document.getElementById("error-message"),errorText=document.getElementById("error-text"),toggle=document.getElementById("password-toggle");
  let setup=false,busy=false,ready=false;
  function showError(message){errorText.textContent=message;error.classList.add("visible");}
  function clearError(){error.classList.remove("visible");}
  function redirect(){ const raw=new URLSearchParams(location.search).get("redirect");try{const url=new URL(raw||"/",location.origin);location.href=url.origin===location.origin && (raw||"/").startsWith("/") && !(raw||"").startsWith("//") ? url.pathname+url.search+url.hash : "/";}catch(_){location.href="/";} }
  function loading(value){busy=value;button.disabled=value;button.classList.toggle("loading",value);username.disabled=value;password.disabled=value;}
  loading(true);
  fetch("/api/auth/status",{cache:"no-store"}).then(async response=>{
    if(!response.ok)throw new Error();const state=await response.json();
    if(state.authenticated){redirect();return;}
    setup=Boolean(state.setup_required);ready=true;
    if(setup){document.querySelector(".login-header h1").textContent="Configure seu Pulso";document.querySelector(".login-header p").textContent="Crie a primeira conta de administrador.";document.getElementById("setup-token-group").hidden=false;document.getElementById("setup-confirm-group").hidden=false;document.getElementById("setup-token").required=true;document.getElementById("setup-confirm").required=true;password.minLength=12;password.autocomplete="new-password";document.querySelector(".btn-text").textContent="Criar administrador";}
  }).catch(()=>showError("Não foi possível verificar o acesso. Atualize a página para tentar novamente.")).finally(()=>{loading(false);button.disabled=!ready;});
  toggle.addEventListener("click",()=>{const reveal=password.type==="password";password.type=reveal?"text":"password";toggle.querySelector(".eye-icon").style.display=reveal?"none":"block";toggle.querySelector(".eye-off-icon").style.display=reveal?"block":"none";toggle.setAttribute("aria-label",reveal?"Hide password":"Show password");});
  form.addEventListener("input",clearError);
  form.addEventListener("submit",async event=>{
    event.preventDefault();if(busy||!ready||!form.reportValidity())return;clearError();
    if(setup&&password.value!==document.getElementById("setup-confirm").value){showError("As senhas precisam ser iguais.");return;}
    const payload={username:username.value.trim(),password:password.value};if(setup)payload.token=document.getElementById("setup-token").value.trim();
    loading(true);
    try{const response=await fetch(setup?"/api/auth/setup":"/api/auth/login",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify(payload)});if(response.ok){redirect();return;}const data=await response.json().catch(()=>({}));showError(response.status===429?"Muitas tentativas. Aguarde um minuto.":data.error||"Usuário ou senha inválidos.");password.value="";password.focus();}catch(_){showError("Falha de conexão. Tente novamente.");}finally{loading(false);}
  });
})();
