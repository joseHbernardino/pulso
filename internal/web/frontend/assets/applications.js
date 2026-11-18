(() => {
  "use strict";
  const $ = id => document.getElementById("application-" + id);
  const dialog = $("dialog"), form = $("form");
  let busy = false, editing = null, config = {}, revision = "", envDocument = null, restoreFocus = null;
  let dirty = false, loading = false, generation = 0, originalEnvText = "";

  function showError(message, issue) {
    window.PulsoPaths.showGuidance(issue);
    $("error").textContent = message;
    $("error").hidden = false;
    $("error").scrollIntoView({ block: "nearest" });
  }
  function clearError() { $("error").hidden = true; $("error").textContent = ""; window.PulsoPaths.showGuidance(null); }
  function notice(message) { $("save-status").textContent = message; $("save-status").hidden = !message; }
  function setBusy(value, label) {
    if (value && !busy) restoreFocus = document.activeElement;
    busy = value;
    $("fields").disabled = value;
    for (const id of ["submit", "close", "cancel", "example"]) $(id).disabled = value;
    $("restart-now").disabled = value || dirty;
    $("submit").textContent = value ? label || (editing ? "Salvando…" : "Iniciando…") : (editing ? "Salvar alterações" : "Iniciar aplicação");
    form.setAttribute("aria-busy", String(value));
    if (!value && dialog.open && restoreFocus?.isConnected && !restoreFocus.disabled && dialog.contains(restoreFocus)) restoreFocus.focus({ preventScroll: true });
    if (!value) restoreFocus = null;
  }
  function changed() {
    dirty = true;
    $("restart-now").disabled = true;
    notice(editing ? "Há alterações não salvas. Salve antes de reiniciar." : "");
    clearError();
  }
  function parseEnvironment(text) {
    const result = Object.create(null);
    for (const [index, line] of text.split(/\r?\n/).entries()) {
      if (!line.trim()) continue;
      const equals = line.indexOf("=");
      const key = equals < 0 ? "" : line.slice(0, equals).trim();
      if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) throw new Error("Variável na linha " + (index + 1) + ": use NOME=valor.");
      if (Object.hasOwn(result, key)) throw new Error('A variável "' + key + '" aparece mais de uma vez.');
      result[key] = line.slice(equals + 1);
    }
    return result;
  }
  async function request(url, options) {
    const response = await fetch(url, options);
    const data = await response.json().catch(() => ({}));
    if (response.status === 401) throw new Error("Sua sessão expirou. Entre novamente no painel.");
    if (!response.ok) {
      const error = new Error(data.error || "Não foi possível concluir. Confira os dados e tente novamente.");
      error.issue = data;
      throw error;
    }
    return data;
  }
  const jsonOptions = (method, body) => ({ method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });

  function applyDocument(doc) {
    if (doc.path) $("env-path").value = doc.path;
    if (doc.cwd) $("cwd").value = doc.cwd;
    envDocument = { ...doc, inputPath: $("env-path").value.trim(), cwd: $("cwd").value.trim() };
    $("env-content").disabled = false;
    $("env-content").value = doc.content;
    $("env-status").textContent = doc.exists ? "Arquivo carregado: " + doc.path : "Arquivo ainda não existe. Ele será criado ao salvar: " + doc.path;
    $("env-load").textContent = "Recarregar arquivo";
  }
  function reset() {
    generation++;
    originalEnvText = "";
    form.reset();
    window.PulsoProjects?.reset();
    editing = null; config = {}; revision = ""; envDocument = null; dirty = false; loading = false;
    $("name").readOnly = false;
    $("env-path").required = false;
    $("env-content").disabled = true;
    $("env-status").textContent = "Se a aplicação usa .env, informe o caminho e carregue o arquivo.";
    $("env-load").textContent = "Carregar arquivo";
    $("options").open = false;
    $("example").hidden = false;
    $("submit").hidden = false;
    $("restart-now").hidden = true;
    $("title").textContent = "Nova aplicação";
    $("description").textContent = "Inicie um programa e configure seu ambiente neste servidor.";
    clearError(); notice(""); setBusy(false);
  }
  function closeForm() {
    if (busy || loading) return;
    if (dirty && !window.confirm("Descartar as alterações que ainda não foram salvas?")) return;
    dialog.close();
  }
  $("close").addEventListener("click", closeForm);
  $("cancel").addEventListener("click", closeForm);
  dialog.addEventListener("cancel", event => { event.preventDefault(); closeForm(); });
  dialog.addEventListener("close", () => {
    const id = editing;
    reset();
    const trigger = id ? [...document.querySelectorAll("[data-process-action]")].find(button => button.dataset.processAction === id + "edit") : document.getElementById("new-application");
    trigger?.focus({ preventScroll: true });
  });
  form.addEventListener("input", changed);
  for (const id of ["env-path", "cwd"]) $(id).addEventListener("input", () => {
    const path = $("env-path").value.trim();
    const matches = envDocument && envDocument.inputPath === path && envDocument.cwd === $("cwd").value.trim();
    $("env-content").disabled = !matches;
    $("env-status").textContent = matches ? "Arquivo carregado: " + envDocument.path : path ? "Carregue o arquivo deste caminho antes de salvar." : "A aplicação não usará um arquivo .env.";
  });
  document.getElementById("new-application").addEventListener("click", () => {
    if (!window.pulsoCanManage) return;
    reset(); dialog.showModal();
  });

  window.openApplicationSettings = async id => {
    if (!window.pulsoCanManage || dialog.open) return;
    reset(); editing = id;
    window.PulsoProjects?.editing();
    $("title").textContent = "Editar aplicação";
    $("description").textContent = "Salve a configuração e reinicie quando quiser aplicar.";
    $("example").hidden = true;
    $("restart-now").hidden = false;
    dialog.showModal(); setBusy(true, "Carregando…");
    try {
      const data = await request("/api/v1/processes/" + encodeURIComponent(id) + "/settings");
      config = data.config; revision = data.revision;
      // Older applications already contain a resolved executable; keep direct execution.
      const launch = config.launch || { ...config, runtime: "process" };
      $("name").value = config.name; $("name").readOnly = true;
      $("runtime").value = launch.runtime || "process";
      $("entrypoint").value = launch.entrypoint;
      $("cwd").value = config.cwd || "";
      $("args").value = (launch.args || []).join("\n");
      $("interpreter").value = launch.interpreter || "";
      $("env").value = Object.entries(config.env || {}).map(([key, value]) => key + "=" + value).join("\n");
      originalEnvText = $("env").value;
      $("restart").value = config.autorestart === false ? "never" : config.restart_policy || "on-failure";
      $("env-path").value = config.env_file || "";
      // Dotenv is optional, including for existing applications.
      $("env-path").required = false;
      if (data.env_file) applyDocument(data.env_file);
      if (data.env_error) showError(data.env_error, data.env_issue);
      notice(data.pending_changes ? "Alterações salvas. Reinicie a aplicação para aplicar." : "A configuração está em uso. Alterações salvas serão aplicadas na próxima inicialização.");
    } catch (err) { showError(err.message, err.issue); $("submit").hidden = true; $("restart-now").hidden = true; }
    finally { setBusy(false); if (dialog.open) $("entrypoint").focus({ preventScroll: true }); }
  };

  document.querySelectorAll("[data-browse-path]").forEach(button => button.addEventListener("click", async () => {
    if (busy || loading) return;
    const field = button.dataset.browsePath;
    const selected = await window.PulsoPaths.choose({ field, value: $(field).value, cwd: $("cwd").value });
    if (!selected) return;
    $(field).value = selected;
    if (field === "entrypoint" && !$("cwd").value.trim()) {
      const parent = selected.slice(0, selected.lastIndexOf("/"));
      $("cwd").value = parent === "container:" ? "container:/" : parent || "/";
      $("cwd").dispatchEvent(new Event("input", { bubbles: true }));
    }
    $(field).dispatchEvent(new Event("input", { bubbles: true }));
    if (field === "env-path") $("env-load").click();
  }));

  $("env-load").addEventListener("click", async () => {
    if (busy || loading) return;
    if (!$("env-path").value.trim()) { showError("Informe o caminho do .env."); $("env-path").focus(); return; }
    if (envDocument && $("env-content").value !== envDocument.content && !window.confirm("Substituir as edições do .env pelo conteúdo atual do servidor?")) return;
    const token = generation;
    loading = true; setBusy(true, "Carregando…"); clearError();
    try {
      const doc = await request("/api/v1/env-files/preview", jsonOptions("POST", { path: $("env-path").value.trim(), cwd: $("cwd").value.trim() }));
      if (token !== generation) return;
      applyDocument(doc); changed(); $("env-content").focus();
    } catch (err) { showError(err.message, err.issue); }
    finally { loading = false; setBusy(false); }
  });

  document.addEventListener("pulso:project-selected", async event => {
    if (editing || busy || loading || !dialog.open || !window.pulsoCanManage) return;
    const { config: suggestion, envFile } = event.detail;
    config = { ...config, max_restarts: suggestion.max_restarts };
    $("name").value = suggestion.name; $("runtime").value = suggestion.runtime;
    $("entrypoint").value = suggestion.entrypoint; $("cwd").value = suggestion.cwd;
    $("args").value = (suggestion.args || []).join("\n"); $("interpreter").value = suggestion.interpreter || "";
    $("env-path").value = envFile; envDocument = null;
    $("env-content").value = ""; $("env-content").disabled = true;
    $("env-load").textContent = "Carregar arquivo";
    $("env-status").textContent = envFile ? "Carregue o arquivo de ambiente antes de iniciar." : "A aplicação não usará um arquivo .env.";
    $("options").open = !!suggestion.args?.length || !!suggestion.interpreter;
    changed();
    if (envFile) $("env-load").click();
    else $("name").focus();
  });

  $("example").addEventListener("click", () => {
    $("name").value = "demonstracao-web"; $("runtime").value = "process";
    $("entrypoint").value = "sleep"; $("args").value = "3600";
    $("env-path").value = ""; $("restart").value = "never";
    $("options").open = true; envDocument = null; $("env-content").value = ""; $("env-content").disabled = true;
    $("env-status").textContent = "O exemplo não precisa de um arquivo .env.";
    changed(); $("name").focus();
  });

  form.addEventListener("submit", async event => {
    event.preventDefault();
    if (busy || loading || !window.pulsoCanManage || !form.reportValidity()) return;
    clearError();
    let payload;
    try {
      const path = $("env-path").value.trim(), cwd = $("cwd").value.trim();
      if (path && (!envDocument || envDocument.inputPath !== path || envDocument.cwd !== cwd)) throw new Error("Carregue o arquivo .env deste caminho e pasta antes de salvar.");
      const launch = config.launch || config;
      payload = { ...config, name: $("name").value.trim(), runtime: $("runtime").value,
        entrypoint: $("entrypoint").value.trim(), cwd, env_file: path,
        args: $("args").value.split(/\r?\n/).filter(line => line.trim() !== ""),
        env: editing && $("env").value === originalEnvText ? config.env : parseEnvironment($("env").value), restart_policy: $("restart").value, autorestart: $("restart").value !== "never",
        interpreter: $("interpreter").value.trim(), use_bundle: !!launch.use_bundle, revision };
      if (path && envDocument) { payload.env_content = $("env-content").value; payload.env_revision = envDocument.revision; }
    } catch (err) { showError(err.message, err.issue); return; }
    setBusy(true);
    try {
      const url = "/api/v1/processes" + (editing ? "/" + encodeURIComponent(editing) + "/settings" : "");
      const data = await request(url, jsonOptions(editing ? "PATCH" : "POST", payload));
      if (editing) {
        config = data.config; revision = data.revision; dirty = false; originalEnvText = $("env").value;
        $("env-path").value = config.env_file || "";
        if (data.env_file) applyDocument(data.env_file);
        else {
          envDocument = null; $("env-content").value = ""; $("env-content").disabled = true;
          $("env-load").textContent = "Carregar arquivo";
          $("env-status").textContent = "A aplicação não usa um arquivo .env.";
        }
        notice(data.pending_changes ? "Alterações salvas. Clique em Reiniciar aplicação para aplicar." : "Configuração salva. A aplicação já está usando estes valores.");
        document.dispatchEvent(new CustomEvent("pulso:application-created"));
      } else {
        dirty = false;
        document.dispatchEvent(new CustomEvent("pulso:application-created", { detail: data }));
        dialog.close();
      }
    } catch (err) { showError(err.message, err.issue); }
    finally { setBusy(false); }
  });

  $("restart-now").addEventListener("click", async () => {
    if (busy || dirty || !editing) return;
    setBusy(true, "Reiniciando…"); clearError();
    $("restart-now").textContent = "Reiniciando…";
    try {
      await request("/api/v1/processes/" + encodeURIComponent(editing) + "/restart", { method: "POST" });
      notice("Aplicação reiniciada. A configuração salva e o .env foram carregados.");
      document.dispatchEvent(new CustomEvent("pulso:application-created"));
    } catch (err) { showError(err.message, err.issue); }
    finally { $("restart-now").textContent = "Reiniciar aplicação"; setBusy(false); }
  });
  reset();
})();
