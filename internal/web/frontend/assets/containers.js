(() => {
  "use strict";
  const $ = name => document.getElementById("container-" + name);
  const page = name => document.getElementById("containers-" + name);
  const dialog = $("dialog");
  let apps = [], editing = null, polling = false, saving = false, loadingEnv = false, logID = "", originalEnv = "", dirty = false;
  let renderedState = "";
  const error = (element, message) => { element.textContent = message || ""; element.hidden = !message; };
  function setWorking() {
    const working = saving || loadingEnv;
    $("fields").disabled = working;
    for (const name of ["submit", "close", "cancel"]) $(name).disabled = working;
    $("form").setAttribute("aria-busy", String(working));
  }
  function environment(text) {
    const result = Object.create(null), lines = text.split(/\r?\n/);
    for (let index = 0; index < lines.length; index++) {
      const line = lines[index].trim();
      if (!line || line.startsWith("#")) continue;
      const match = /^(?:export\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*=(.*)$/.exec(line);
      if (!match) throw new Error("Variável na linha " + (index + 1) + ": use NOME=valor.");
      const key = match[1]; let value = match[2].trim();
      if (Object.hasOwn(result, key)) throw new Error("Variável repetida: " + key);
      if (value.startsWith('"')) {
        try { value = JSON.parse(value); } catch (_) { throw new Error("Confira as aspas e os caracteres especiais da variável " + key + ". Para importar um .env, use Carregar arquivo."); }
        if (typeof value !== "string") throw new Error("Valor inválido: " + key);
      } else if (value.startsWith("'")) {
        if (!value.endsWith("'") || value.length < 2) throw new Error("Feche as aspas da variável " + key);
        value = value.slice(1, -1);
      } else value = value.replace(/\s+#.*$/, "");
      result[key] = value;
    }
    return result;
  }
  async function request(path, method = "GET", body) {
    const response = await fetch("/api/v1/container-applications" + path, { method, headers: { "Content-Type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body) });
    const data = await response.json();
    if (response.status === 401) { location.replace("/login"); throw new Error("Entre novamente no painel."); }
    if (!response.ok) throw new Error(data.error || "Não foi possível concluir a operação.");
    return data;
  }
  function button(label, handler, disabled = false) {
    const element = document.createElement("button"); element.type = "button"; element.className = "btn"; element.textContent = label;
    element.disabled = disabled; element.addEventListener("click", handler); return element;
  }
  function render() {
    const nextState = JSON.stringify([window.pulsoCanManage, apps.map(app => ({id:app.id,config:app.config,phase:app.phase,state:app.state,ports:app.ports,error:app.error,container:app.container_id}))]);
    if (nextState === renderedState) return;
    renderedState = nextState;
    const focused = document.activeElement?.dataset.focusKey;
    page("list").replaceChildren();
    page("total").textContent = apps.length;
    page("total").nextElementSibling.textContent = apps.length === 1 ? "projeto" : "projetos";
    page("running").textContent = apps.filter(app => app.state === "running").length;
    if (!apps.length) {
      const empty = document.createElement("div"); empty.className = "container-empty";
      const heading = document.createElement("h3"); heading.textContent = "Seu próximo projeto começa aqui.";
      const text = document.createElement("p"); text.textContent = "Use uma imagem Docker ou selecione a pasta de um projeto Node.js, Python ou Dockerfile.";
      empty.append(heading, text); page("list").append(empty);
    }
    for (const app of apps) {
      const row = document.createElement("article"); row.className = "container-row";
      const description = document.createElement("div"); description.className = "container-description";
      const title = document.createElement("h3"); title.textContent = app.config.name;
      const source = document.createElement("p"); source.textContent = app.config.source ? ({node:"Node.js",python:"Python",dockerfile:"Dockerfile"}[app.config.source.kind] || app.config.source.kind) + (app.config.source.kind === "dockerfile" ? "" : " " + app.config.source.version) + (app.config.source.path ? " · " + app.config.source.path : "") : app.config.image;
      const state = document.createElement("span"); state.className = "container-state " + (app.phase === "failed" ? "failed" : app.state);
      const states = { running: "Em execução", restarting: "Reiniciando após falha", stopped: "Parado", not_created: "Aguardando implantação", starting: "Iniciando", unhealthy: "Healthcheck com falha", unavailable: "Contêiner indisponível" };
      state.textContent = app.phase === "deploying" ? "Implantando…" : app.phase === "failed" ? "Implantação com falha" : states[app.state] || app.state;
      const resource = document.createElement("p"); resource.textContent = app.config.memory_mb + " MiB · " + app.config.cpus + " CPU";
      description.append(title, source, state, resource);
      if (app.error) { const message = document.createElement("p"); message.className = "container-failure"; message.textContent = app.error; description.append(message); }
      const links = document.createElement("div"); links.className = "container-links";
      for (const port of [...new Set(app.ports || [])]) { const link = document.createElement("a"); link.href = "http://" + location.hostname + ":" + port; link.textContent = "Abrir porta " + port; link.target = "_blank"; link.rel = "noopener noreferrer"; links.append(link); }
      description.append(links); row.append(description);
      if (window.pulsoCanManage) {
        const actions = document.createElement("div"); actions.className = "container-actions";
        const busy = app.phase === "deploying";
        const edit = button("Editar", () => open(app), busy); edit.dataset.appEdit = app.id;
        actions.append(edit, button("Logs", () => showLogs(app.id, app.config.name)));
        const lifecycle = app.state === "stopped" ? [["start", "Iniciar"]] : [["stop", "Parar"], ["restart", "Reiniciar"]];
        for (const [action, label] of app.container_id ? lifecycle : []) {
          const item = button(label, () => actionOn(app, action), busy || !app.container_id); item.setAttribute("aria-label", label + " " + app.config.name); actions.append(item);
        }
        actions.append(button("Excluir", () => actionOn(app, "delete"), busy));
        for (const item of actions.querySelectorAll("button")) item.dataset.focusKey = app.id + ":" + item.textContent;
        row.append(actions);
      }
      page("list").append(row);
    }
    if (focused) [...page("list").querySelectorAll("button")].find(item => item.dataset.focusKey === focused)?.focus({ preventScroll: true });
  }
  async function refresh() {
    if (polling) return; polling = true;
    try {
      const data = await request(""); apps = data.applications || [];
      page("status").textContent = data.enabled ? "Estado atualizado automaticamente. Cada projeto executa em seu próprio contêiner." : data.reason;
      $("new").disabled = !data.enabled; error(page("error"), ""); render();
      if (logID) { const data = await request("/" + logID + "/logs"); page("logs").textContent = data.logs || "Nenhum log disponível."; }
    } catch (err) { error(page("error"), err.message); }
    finally { polling = false; }
  }
  function kindChanged(version = "") {
    const kind = $("kind").value, image = kind === "image";
    $("image-fields").hidden = !image; $("image").required = image;
    $("source-fields").hidden = image; $("source").required = !image;
    $("version-field").hidden = image || kind === "dockerfile";
    $("rebuild-field").hidden = image || !editing;
    $("version").replaceChildren();
    for (const value of kind === "node" ? ["24", "22", "26"] : ["3.11", "3.12", "3.13", "3.14"]) $("version").add(new Option(value, value));
    if (version) $("version").value = version;
  }
  $("kind").addEventListener("change", () => kindChanged());
  async function open(app = null) {
    if (!window.pulsoCanManage) return;
    if(app){try{app=await request("/"+app.id);}catch(err){error(page("error"),err.message);return;}}
    $("form").reset(); editing = app; error($("form-error"), ""); $("form-status").textContent = "";
    const cfg = app?.config;
    $("title").textContent = app ? "Editar " + cfg.name : "Novo projeto";
    $("submit").textContent = app ? "Aplicar alterações" : "Implantar projeto";
    $("kind").value = cfg?.source?.kind || "image"; kindChanged(cfg?.source?.version);
    if (cfg) {
      for (const name of ["name", "image"]) $(name).value = cfg[name] || "";
      $("source").value = cfg.source?.path || ""; $("memory").value = cfg.memory_mb; $("cpus").value = cfg.cpus;
      $("port").value = cfg.port || ""; $("host-port").value = cfg.host_port || ""; $("data").value = cfg.data_path || "";
      $("command").value = (cfg.command || []).join("\n");
      $("env").value = Object.entries(cfg.env || {}).map(([key, value]) => key + "=" + JSON.stringify(value)).join("\n");
    }
    originalEnv = $("env").value; dirty = false; dialog.showModal(); $("name").focus();
  }
  async function actionOn(app, action) {
    if (action === "delete" && !confirm("Excluir " + app.config.name + "? O contêiner será removido; o volume persistente será mantido.")) return;
    try { await request("/" + app.id + "/" + action, "POST", {}); await refresh(); }
    catch (err) { error(page("error"), err.message); }
  }
  async function showLogs(id, name) {
    logID = id; page("log-panel").hidden = false; page("log-title").textContent = "Logs de " + name;
    try { page("logs").textContent = (await request("/" + id + "/logs")).logs || "Nenhum log disponível."; page("log-panel").scrollIntoView({ block: "nearest" }); }
    catch (err) { page("logs").textContent = err.message; }
  }
  page("log-close").addEventListener("click", () => { logID = ""; page("log-panel").hidden = true; });
  $("source-choose").addEventListener("click", async () => { const path = await window.PulsoPaths.choose({ field: "cwd", value: $("source").value }); if (path) { $("source").value = path; dirty = true; } });
  $("env-choose").addEventListener("click", async () => { const path = await window.PulsoPaths.choose({ field: "env-path", value: $("env-path").value, cwd: $("source").value }); if (path) { $("env-path").value = path; dirty = true; } });
  $("env-load").addEventListener("click", async () => {
    if (saving || loadingEnv) return;
    if (!$("env-path").value.trim()) { error($("form-error"), "Selecione um arquivo .env para carregar."); return; }
    if ($("env").value && !confirm("Substituir as variáveis pelo conteúdo do arquivo selecionado?")) return;
    loadingEnv = true; setWorking(); error($("form-error"), ""); $("form-status").textContent = "Carregando arquivo…";
    try {
      const response = await fetch("/api/v1/env-files/preview", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ path: $("env-path").value, cwd: $("source").value }) });
      const data = await response.json(); if (!response.ok) throw new Error(data.error);
      if (!data.exists) throw new Error("O arquivo não existe. Informe as variáveis abaixo ou selecione outro arquivo.");
      $("env").value = Object.entries(data.values || environment(data.content)).map(([key, value]) => key + "=" + JSON.stringify(value)).join("\n"); dirty = true; $("form-status").textContent = "Arquivo carregado. Os valores serão aplicados na implantação.";
    } catch (err) { error($("form-error"), err.message); $("form-status").textContent = ""; }
    finally { loadingEnv = false; setWorking(); }
  });
  $("form").addEventListener("submit", async event => {
    event.preventDefault(); if (saving || loadingEnv || !$("form").reportValidity()) return;
    saving = true; setWorking(); error($("form-error"), ""); $("form-status").textContent = "Preparando a implantação…";
    try {
      const cfg = { name: $("name").value.trim(), image: $("image").value.trim(), port: Number($("port").value), host_port: Number($("host-port").value), memory_mb: Number($("memory").value), cpus: Number($("cpus").value), data_path: $("data").value.trim(), pull: $("pull").checked, command: $("command").value.split(/\r?\n/).filter(value => value.trim()), env: editing?.config.env || {} };
      if (!editing || $("env").value !== originalEnv) cfg.env = environment($("env").value);
      if ($("kind").value !== "image") cfg.source = { kind: $("kind").value, path: $("source").value.trim(), version: $("version").value, rebuild: $("rebuild").checked };
      await request(editing ? "/" + editing.id : "", editing ? "PUT" : "POST", cfg);
      dirty = false; dialog.close(); await refresh(); window.PulsoUI.notify("Implantação iniciada. Acompanhe o estado e os logs do projeto.");
    } catch (err) { error($("form-error"), err.message); $("form-status").textContent = ""; }
    finally { saving = false; setWorking(); }
  });
  $("form").addEventListener("input", () => { dirty = true; });
  function close() { if (!saving && !loadingEnv && (!dirty || confirm("Descartar as alterações que ainda não foram implantadas?"))) dialog.close(); }
  $("close").addEventListener("click", close); $("cancel").addEventListener("click", close);
  dialog.addEventListener("cancel", event => { event.preventDefault(); close(); });
  dialog.addEventListener("close", () => { const target = editing ? [...page("list").querySelectorAll("button")].find(item => item.dataset.appEdit === editing.id) : $("new"); target?.focus({preventScroll:true}); });
  $("new").addEventListener("click", () => open()); page("refresh").addEventListener("click", refresh);
  document.addEventListener("pulso:access-ready", () => { if (!window.pulsoCanManage) { logID = ""; page("log-panel").hidden = true; } refresh(); });
  setInterval(() => { if (!document.hidden) refresh(); }, 5000);
  refresh();
})();
