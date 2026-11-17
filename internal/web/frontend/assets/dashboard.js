      // --- State ---
      let ws;
      let currentLogId = null;
      let currentLogStream = "all";
      let logRequestNumber = 0;
      let logRefreshTimer = null;
      let confirmCallback = null;
      let previousFocus = null;
      let allProcesses = [];

      // --- WebSocket ---
      function connect() {
        const proto = location.protocol === "https:" ? "wss:" : "ws:";
        ws = new WebSocket(proto + "//" + location.host + "/ws");

        ws.onopen = () => {
          setConnectionStatus(true);
        };

        ws.onclose = () => {
          setConnectionStatus(false);
          setTimeout(connect, 3000);
        };

        ws.onmessage = (e) => {
          const msg = JSON.parse(e.data);
          if (msg.type === "process_list") {
            allProcesses = msg.processes || [];
            renderProcesses(allProcesses);
            updateSummary(allProcesses);
            updateTimestamp();
            if(currentLogId){const process=allProcesses.find(p=>p.id===currentLogId);if(process)renderDiagnostic(process);}
          }
        };
      }

      function setConnectionStatus(connected) {
        const el = document.getElementById("connection-status");
        if (connected) {
          el.className = "connection connected";
          el.querySelector(".label").textContent = "Ao vivo";
        } else {
          el.className = "connection disconnected";
          el.querySelector(".label").textContent = "Reconectando…";
        }
      }

      function updateTimestamp() {
        const el = document.getElementById("last-updated");
        const now = new Date();
        el.textContent = "Atualizado às " + now.toLocaleTimeString("pt-BR");
      }

      // --- Summary ---
      function updateSummary(procs) {
        let running = 0, stopped = 0, crashed = 0;
        (procs || []).forEach((p) => {
          switch (p.state) {
            case "running":
            case "starting":
              running++;
              break;
            case "crashed":
            case "errored":
              crashed++;
              break;
            default:
              stopped++;
          }
        });
        document.getElementById("stat-total").textContent = procs ? procs.length : 0;
        document.getElementById("stat-running").textContent = running;
        document.getElementById("stat-stopped").textContent = stopped;
        document.getElementById("stat-crashed").textContent = crashed;
      }

      // --- Render ---
      let statusFilter = "all";
      const stateLabels = {running:"Em execução",starting:"Iniciando",stopped:"Parada",crashed:"Com falha",errored:"Com erro",stopping:"Parando",waiting:"Aguardando",unknown:"Indefinido",created:"Criada"};
      const groupState = p => ["running","starting"].includes(p.state)?"running":["crashed","errored"].includes(p.state)?"crashed":"stopped";
      function renderProcesses(procs) {
        const tbody=document.getElementById("process-list");
        const active=document.activeElement?.closest("button[data-process-action]");
        const focusKey=active?.dataset.processAction;
        const query=document.getElementById("process-search").value.trim().toLocaleLowerCase("pt-BR");
        const filtered=(procs||[]).filter(p=>(statusFilter==="all"||groupState(p)===statusFilter)&&[p.name,p.runtime,stateLabels[p.state],p.namespace].join(" ").toLocaleLowerCase("pt-BR").includes(query));
        document.getElementById("application-total").textContent=(procs||[]).length;
        document.getElementById("process-count").textContent=filtered.length+" de "+(procs||[]).length+" aplicações";
        tbody.replaceChildren();
        if(!filtered.length){
          const row=tbody.insertRow(),cell=row.insertCell();cell.colSpan=13;
          const empty=document.createElement("div");empty.className="empty-state";
          const symbol=document.createElement("div");symbol.className="empty-symbol";symbol.append(PulsoUI.icon("grid"));
          const title=document.createElement("h3");title.textContent=query||statusFilter!=="all"?"Nenhuma aplicação encontrada":"Sua próxima aplicação começa aqui";
          const desc=document.createElement("p");desc.textContent=query||statusFilter!=="all"?"Ajuste a busca ou selecione outro status.":"Adicione um programa deste servidor para acompanhar sua execução, seus recursos e seus logs.";
          empty.append(symbol,title,desc);
          if(!query&&statusFilter==="all"&&window.pulsoCanManage){const btn=document.createElement("button");btn.className="btn btn-primary";btn.textContent="Adicionar aplicação";btn.onclick=()=>document.getElementById("new-application").click();empty.append(btn);}
          cell.append(empty);return;
        }
        for(const p of filtered){
          const row=tbody.insertRow(),id=String(p.id||"");
          const state=Object.hasOwn(stateLabels,p.state)?p.state:"unknown";
          const values=[id.slice(0,8),p.name||"Sem nome",p.runtime||"process",state,p.ready?"Sim":"Não",["running","starting","stopping"].includes(p.state)&&p.pid>0?p.pid:"—",Number(p.cpu_percent||0).toFixed(1)+"%",formatBytes(p.memory_bytes||0),p.restarts||0,formatUptime(p.uptime||0),p.last_event||"—",p.last_reason||"—"];
          values.forEach((value,i)=>{
            const cell=row.insertCell();cell.textContent=String(value);
            if([0,4,5,6,7,8,9].includes(i))cell.className="mono";
            if(i===1){const wrap=document.createElement("div");wrap.className="application-name";const symbol=document.createElement("span");symbol.className="application-symbol";symbol.append(PulsoUI.icon("terminal"));const text=document.createElement("div"),name=document.createElement("strong"),detail=document.createElement("span");name.textContent=value;detail.className="process-id";detail.textContent=id.slice(0,8);text.append(name,detail);if(p.pending_changes){const pending=document.createElement("span");pending.className="application-pending";pending.textContent="Reinício pendente";text.append(pending);}wrap.append(symbol,text);cell.replaceChildren(wrap);}
            if(i===2){const tag=document.createElement("span");tag.className="runtime-tag";tag.textContent=value;cell.replaceChildren(tag);}
            if(i===3){const badge=document.createElement("span");badge.className="badge badge-"+state;const dot=document.createElement("span");dot.className="dot";dot.setAttribute("aria-hidden","true");badge.append(dot,document.createTextNode(stateLabels[state]));cell.replaceChildren(badge);
              if(["crashed","errored","waiting"].includes(state)){
                const reason=document.createElement("button");reason.type="button";reason.className="process-failure-link";
                reason.textContent=p.last_failure?.message||p.last_reason||("Encerrada (código "+(p.exit_code??"desconhecido")+"). Ver detalhes.");
                reason.dataset.processAction=id+"failure";reason.title=reason.textContent;reason.setAttribute("aria-label",reason.textContent+" Ver detalhes de "+p.name);reason.onclick=()=>showLogs(id);cell.append(reason);
              }}
            if(i===11)cell.title=String(value);
          });
          const actions=document.createElement("div");actions.className="actions";
          const running=["running","starting","stopping"].includes(p.state);
          for(const [label,ic,style,handler] of [["Editar","edit","",()=>window.openApplicationSettings(id)],[running?"Reiniciar":"Iniciar",running?"refresh":"play","",()=>restartProc(id)],["Parar","stop","",()=>stopProc(id)],["Ver logs","logs","",()=>showLogs(id)],["Excluir","trash","btn-danger",()=>deleteProc(id,p.name)]]){
            if(label!=="Ver logs"&&!window.pulsoCanManage)continue;
            const btn=document.createElement("button");btn.type="button";btn.className="btn btn-icon "+style;btn.append(PulsoUI.icon(ic));btn.title=label;btn.setAttribute("aria-label",label+" "+p.name);btn.dataset.processAction=id+ic;btn.onclick=handler;if(label==="Parar"&&!running)btn.disabled=true;actions.append(btn);
          }
          row.insertCell().append(actions);
        }
        if(focusKey){const btn=[...tbody.querySelectorAll("[data-process-action]")].find(b=>b.dataset.processAction===focusKey);btn?.focus({preventScroll:true});}
      }
      document.querySelectorAll("[data-status]").forEach(btn=>btn.addEventListener("click",()=>{statusFilter=btn.dataset.status;document.querySelectorAll("[data-status]").forEach(tab=>{const active=tab===btn;tab.classList.toggle("is-active",active);tab.setAttribute("aria-pressed",String(active));});renderProcesses(allProcesses);}));
      function formatUptime(ns) {
        if (!ns || ns <= 0) return "-";
        const s = Math.floor(ns / 1e9);
        if (s < 60) return s + "s";
        const m = Math.floor(s / 60);
        if (m < 60) return m + "m";
        const h = Math.floor(m / 60);
        if (h < 24) return h + "h";
        return Math.floor(h / 24) + "d";
      }

      function formatBytes(b) {
        if (b <= 0) return "-";
        if (b < 1024) return b + "b";
        if (b < 1048576) return (b / 1024).toFixed(1) + "kb";
        if (b < 1073741824) return (b / 1048576).toFixed(1) + "mb";
        return (b / 1073741824).toFixed(1) + "gb";
      }

      function esc(s) {
        const d = document.createElement("div");
        d.textContent = s || "";
        return d.innerHTML;
      }

      function truncate(s, max) {
        if (!s || s.length <= max) return s || "";
        return s.slice(0, max - 1) + "...";
      }

      // --- Toast ---
      function showToast(message, type) {
        type = type || "info";
        const container = document.getElementById("toast-container");
        const toast = document.createElement("div");
        toast.className = "toast toast-" + type;
        toast.textContent = message;
        container.appendChild(toast);
        setTimeout(() => {
          toast.classList.add("toast-out");
          setTimeout(() => toast.remove(), 200);
        }, 3000);
      }

      // --- Confirm dialog ---
      function showConfirm(title, message, onConfirm) {
        document.getElementById("confirm-title").textContent = title;
        document.getElementById("confirm-message").textContent = message;
        previousFocus = document.activeElement;
        confirmCallback = onConfirm;
        document.getElementById("confirm-overlay").classList.add("visible");
        document.querySelector(".app-shell").inert = true;
        document.querySelector("#confirm-dialog .btn-cancel").focus();
      }

      function confirmOk() {
        document.getElementById("confirm-overlay").classList.remove("visible");
        document.querySelector(".app-shell").inert = false;
        if (previousFocus?.isConnected) previousFocus.focus();
        else document.getElementById("refresh-processes").focus();
        if (confirmCallback) {
          confirmCallback();
          confirmCallback = null;
        }
      }

      function confirmCancel() {
        document.getElementById("confirm-overlay").classList.remove("visible");
        document.querySelector(".app-shell").inert = false;
        confirmCallback = null;
        if (previousFocus?.isConnected) previousFocus.focus();
        else document.getElementById("refresh-processes").focus();
      }

      document.getElementById("confirm-ok").addEventListener("click", confirmOk);

      document.getElementById("confirm-overlay").addEventListener("click", (e) => {
        if (e.target === e.currentTarget) confirmCancel();
      });

      document.addEventListener("keydown", (e) => {
        if (document.getElementById("application-dialog").open) return;
        if (e.key === "Tab" && document.getElementById("confirm-overlay").classList.contains("visible")) {
          const buttons = [...document.querySelectorAll("#confirm-dialog button")];
          const first = buttons[0], last = buttons[buttons.length - 1];
          if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
          else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
        }
        if (e.key === "Escape") {
          const overlay = document.getElementById("confirm-overlay");
          if (overlay.classList.contains("visible")) {
            confirmCancel();
          } else {
            closeLogs();
          }
        }
      });

      // --- API calls ---
      async function apiCall(path, method) {
        try {
          const resp = await fetch(path, { method: method || "POST" });
          if (!resp.ok) {
            const data = await resp.json().catch(() => ({}));
            showToast(data.error || "Não foi possível concluir a ação.", "error");
            return false;
          }
          return true;
        } catch (e) {
          showToast("Falha de conexão: " + e.message, "error");
          return false;
        }
      }

      async function restartProc(id) {
        showToast("Iniciando ou reiniciando aplicação…", "info");
        if (await apiCall("/api/v1/processes/" + id + "/restart")) {
          showToast("Aplicação iniciada ou reiniciada.", "success");
        }
      }

      async function stopProc(id) {
        showToast("Parando aplicação…", "info");
        if (await apiCall("/api/v1/processes/" + id + "/stop")) {
          showToast("Aplicação parada.", "success");
        }
      }

      function deleteProc(id, name) {
        showConfirm(
          "Excluir aplicação",
          'Excluir a aplicação "' + (name || id) + '"? Ela será removida do gerenciamento do Pulso.',
          async () => {
            if (await apiCall("/api/v1/processes/" + id, "DELETE")) {
              showToast("Aplicação excluída.", "success");
            }
          }
        );
      }

      // --- Logs and failure details ---
      const eventLabels={started:"Iniciada",restarted:"Reiniciada",reloaded:"Recarregada",stopped:"Parada",exited:"Encerrada",restart_scheduled:"Reinício agendado",restart_skipped:"Reinício não agendado",start_failed:"Falha ao iniciar",restart_failed:"Falha ao reiniciar",readiness_failed:"Falha na verificação de prontidão",environment_failed:"Falha ao carregar ambiente",pre_start_failed:"Falha antes de iniciar"};
      const diagnosticTime=value=>value?new Date(value).toLocaleString("pt-BR"):"—";
      function renderDiagnostic(process){
        const panel=document.getElementById("process-diagnostic");
        if(!process){panel.hidden=true;return;}
        panel.hidden=false;
        const failure=process.last_failure,failed=["crashed","errored","waiting"].includes(process.state);
        panel.classList.toggle("has-failure",failed);
        document.getElementById("process-diagnostic-title").textContent=failed?"A aplicação não está em execução":failure?"Última falha registrada":"Resultado da aplicação";
        document.getElementById("process-diagnostic-message").textContent=failure?.message||process.last_reason||(failed?"O processo encerrou. Confira o código de saída e os registros abaixo.":"Acompanhe as saídas da aplicação e os eventos de execução.");
        const facts=document.getElementById("process-diagnostic-facts");facts.replaceChildren();
        const code=failure?.exit_code??((process.finished_at||failed)?process.exit_code:null);
        for(const [label,value] of [["Status",stateLabels[process.state]||process.state],["Código de saída",code??"—"],["Reinícios",process.restarts??0],["Último evento",eventLabels[process.last_event]||process.last_event||"—"],["Falha registrada em",diagnosticTime(failure?.occurred_at)],["Último encerramento",diagnosticTime(process.finished_at)]]){
          const group=document.createElement("div"),term=document.createElement("dt"),description=document.createElement("dd");term.textContent=label;description.textContent=String(value);group.append(term,description);facts.append(group);
        }
      }
      async function loadLogs(id,initial=false){
        const token=++logRequestNumber,content=document.getElementById("log-content"),lines=document.getElementById("log-lines");
        const selectedStream=currentLogStream,limit=document.getElementById("log-limit").value;
        if(initial){content.textContent="Carregando registros…";lines.textContent="Carregando…";}
        const warning=document.getElementById("log-warning");
        try{
          const response=await fetch("/api/v1/processes/"+encodeURIComponent(id)+"/logs?stream="+selectedStream+"&lines="+limit,{cache:"no-store"});
          const data=await response.json();
          if(token!==logRequestNumber||currentLogId!==id)return;
          if(response.status===401){location.href="/login";return;}
          if(data.process)renderDiagnostic(data.process);
          if(!response.ok)throw new Error(data.error||"Não foi possível carregar os logs.");
          warning.textContent=(data.warnings||[]).join(" ");warning.hidden=!warning.textContent;
          const text=data.logs||(selectedStream==="stderr"?"A aplicação ainda não escreveu em stderr. Confira também Todos e o resultado acima.":"A aplicação não escreveu nesta saída. Mesmo sem logs, o resultado e o código de saída aparecem acima.");
          const follow=initial||content.scrollHeight-content.scrollTop-content.clientHeight<50;
          if(content.textContent!==text){content.textContent=text;if(follow)content.scrollTop=content.scrollHeight;}
          lines.textContent="Até "+(data.lines||limit)+" linhas por saída · atualização automática";
        }catch(error){
          if(token!==logRequestNumber||currentLogId!==id)return;
          warning.textContent=error.message;warning.hidden=false;
          if(initial)content.textContent="Não foi possível carregar os registros.";
          lines.textContent="Falha ao atualizar";
        }
      }
      async function showLogs(id){
        const changed=currentLogId!==id;currentLogId=id;
        document.getElementById("log-panel").classList.add("visible");
        document.getElementById("log-title").textContent="Logs de "+(allProcesses.find(p=>p.id===id)?.name||id.slice(0,8));
        renderDiagnostic(allProcesses.find(p=>p.id===id));
        clearInterval(logRefreshTimer);logRefreshTimer=null;
        await loadLogs(id,true);
        if(currentLogId===id&&!logRefreshTimer)logRefreshTimer=setInterval(()=>{if(!document.hidden)loadLogs(id);},3000);
        if(changed)document.getElementById("log-panel").scrollIntoView({block:"nearest",behavior:"auto"});
      }
      async function refreshLogs(){if(currentLogId)await loadLogs(currentLogId);}
      function copyLogs(){
        const diagnostic=document.getElementById("process-diagnostic").innerText;
        navigator.clipboard.writeText(diagnostic+"\n\n"+document.getElementById("log-content").textContent).then(()=>showToast("Diagnóstico e logs copiados.","success")).catch(()=>showToast("Não foi possível copiar os logs.","error"));
      }
      function closeLogs(){document.getElementById("log-panel").classList.remove("visible");currentLogId=null;logRequestNumber++;clearInterval(logRefreshTimer);logRefreshTimer=null;}
      document.querySelectorAll("[data-log-stream]").forEach(button=>button.addEventListener("click",()=>{
        currentLogStream=button.dataset.logStream;
        document.querySelectorAll("[data-log-stream]").forEach(tab=>{const active=tab===button;tab.classList.toggle("is-active",active);tab.setAttribute("aria-pressed",String(active));});
        if(currentLogId)loadLogs(currentLogId,true);
      }));
      document.getElementById("log-limit").addEventListener("change",()=>{if(currentLogId)loadLogs(currentLogId,true);});

      async function refreshProcesses() {
        const button = document.getElementById("refresh-processes"); button.disabled = true;
        try {
          const response = await fetch("/api/v1/processes");
          if (response.status === 401) { location.href = "/login"; return; }
          if (!response.ok) throw new Error("Não foi possível atualizar as aplicações.");
          allProcesses = await response.json() || [];
          renderProcesses(allProcesses); updateSummary(allProcesses); updateTimestamp();
        } catch (error) { showToast(error.message, "error"); }
        finally { button.disabled = false; }
      }
      document.getElementById("process-search").addEventListener("input", () => renderProcesses(allProcesses));
      document.getElementById("refresh-processes").addEventListener("click", refreshProcesses);
      document.addEventListener("pulso:application-created", (event) => {
        const created = event.detail;
        if(!created?.id){refreshProcesses();return;}
        allProcesses = [...allProcesses.filter(p => p.id !== created.id), created];
        document.getElementById("process-search").value = "";
        renderProcesses(allProcesses); updateSummary(allProcesses); updateTimestamp();
        showToast('Aplicação "' + created.name + '" adicionada.', "success");
      });
      document.addEventListener("pulso:access-ready", () => renderProcesses(allProcesses));
      // --- Init ---
      refreshProcesses();
      connect();
