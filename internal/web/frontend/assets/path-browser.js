(() => {
  "use strict";
  const $ = id => document.getElementById("path-browser-" + id);
  const dialog = document.getElementById("path-browser");
  let finish = null, field = "", cwd = "", current = "", parent = "", offset = 0, busy = false, requestNumber = 0;
  const isDotenv = name => name === ".env" || name.startsWith(".env.") || name.endsWith(".env");
  const button = (label, handler) => {
    const element = document.createElement("button");
    element.type = "button"; element.className = "btn"; element.textContent = label;
    element.addEventListener("click", handler); return element;
  };
  function showGuidance(issue) {
    const panel = document.getElementById("application-path-guidance");
    const visible = !!issue?.container && ["container_path_unavailable", "path_not_found", "permission_denied", "read_only", "path_mapping_invalid"].includes(issue.code);
    panel.hidden = !visible;
    if (!visible) return;
    document.getElementById("application-path-explanation").textContent = issue.host_access ? "O acesso ao host está ativo. Confira a existência do caminho e as permissões do volume montado em /host." : issue.code === "permission_denied" ? "O caminho foi localizado, mas o acesso foi negado ao usuário do Pulso. A seleção de uma pasta não altera suas permissões." : "O Docker só disponibiliza as pastas montadas nesta instalação. Selecionar mostra os caminhos que o Pulso consegue acessar.";
    document.getElementById("application-path-action").textContent = issue.host_access ? "Confira o nome da pasta e as permissões do arquivo selecionado. O volume do servidor já está disponível em /host." : "Para usar uma pasta do servidor em Docker, configure PULSO_APPS_DIR com o caminho absoluto da pasta raiz no Compose e recrie o contêiner. Para vários diretórios fora de uma raiz comum, use compose.host.yaml.";
    const roots = document.getElementById("application-path-roots"); roots.replaceChildren();
    for (const root of issue.roots || []) {
      const line = document.createElement("p");
      line.textContent = root.host ? root.host + " → " + root.container : "Pasta de projetos no contêiner: " + root.container;
      roots.append(line);
    }
  }
  async function load(path, append = false) {
    const token = ++requestNumber;
    busy = true; $("form").setAttribute("aria-busy", "true");
    $("use").disabled = true; $("error").hidden = true; $("status").textContent = "Carregando pasta…";
    try {
      const response = await fetch("/api/v1/filesystem/browse", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ path, cwd, offset: append ? offset : 0 }) });
      const data = await response.json();
      if (token !== requestNumber || !dialog.open) return;
      if (!response.ok) {
        // Keep configured roots available even when the requested directory is unavailable.
        renderRoots(data.roots || []);
        if (!append) $("entries").replaceChildren();
        $("more").hidden = true;
        throw new Error(data.error || "Não foi possível abrir esta pasta.");
      }
      current = data.path; parent = data.parent; offset = data.next_offset;
      $("location").value = current;
      $("context").textContent = data.host_access ? "Acesso ao servidor · /host representa a raiz do servidor. Ambiente do Pulso mostra os arquivos e interpretadores da imagem." : data.container ? "Docker · Selecione um projeto nas pastas disponíveis. Caminhos do servidor com mapeamento configurado são reconhecidos automaticamente." : "Selecione um projeto neste servidor.";
      renderRoots(data.roots || []);
      if (!append) $("entries").replaceChildren();

      for (const entry of data.entries || []) {
        if (field === "env-path" && !entry.directory && !isDotenv(entry.name)) continue;
        if (field === "cwd" && !entry.directory) continue;
        const item = document.createElement("li");
        const select = button(entry.name, () => entry.directory ? load(entry.path) : selectPath(entry.path));
        select.className = "path-browser-entry";
        const icon = window.PulsoUI.icon(entry.directory ? "folder" : "logs");
        select.prepend(icon);
        if (entry.symlink && !entry.directory && field === "env-path") { select.disabled = true; select.title = "Links simbólicos não são aceitos para o .env."; }
        item.append(select); $("entries").append(item);
      }
      $("more").hidden = !data.has_more;
      $("status").textContent = $("entries").children.length ? "Selecione uma pasta para abrir ou um arquivo para usar." : data.has_more ? "Nenhum arquivo compatível nesta página. Carregue mais resultados." : "Nenhum arquivo compatível nesta pasta.";
      $("parent").disabled = current === parent;
      $("use").disabled = false;
    } catch (error) {
      if (token !== requestNumber || !dialog.open) return;
      $("error").textContent = error.message; $("error").hidden = false; $("status").textContent = "";
      $("use").disabled = true;
    } finally {
      if (token === requestNumber) { busy = false; $("form").setAttribute("aria-busy", "false"); }
    }
  }
  function renderRoots(roots) {
    $("roots").replaceChildren();
    for (const root of roots) {
      const entry = button((root.label ? root.label + " · " : "") + (root.host ? root.host + " → " + root.container : root.container) + (root.read_only ? " (somente leitura)" : ""), () => load(root.container));
      entry.title = "Abrir pasta de projetos"; $("roots").append(entry);
    }
  }
  function selectPath(path) { const callback = finish; finish = null; dialog.close(); callback?.(path); }
  $("close").addEventListener("click", () => dialog.close());
  $("cancel").addEventListener("click", () => dialog.close());
  dialog.addEventListener("close", () => {
    requestNumber++; busy = false;
    const callback = finish; finish = null; callback?.(null);
  });
  $("form").addEventListener("submit", event => { event.preventDefault(); load($("location").value); });
  $("parent").addEventListener("click", () => load(parent));
  $("more").addEventListener("click", () => { if (!busy) load(current, true); });
  $("use").addEventListener("click", () => { if (!busy && field === "cwd") selectPath(current); });
  window.PulsoPaths = {
    showGuidance,
    choose(options) {
      if (dialog.open || !window.pulsoCanManage) return Promise.resolve(null);
      field = options.field; cwd = options.cwd || ""; current = ""; parent = ""; offset = 0;
      $("entries").replaceChildren(); $("roots").replaceChildren(); $("context").textContent = ""; $("error").hidden = true;
      $("use").hidden = field !== "cwd"; $("more").hidden = true;
      $("title").textContent = field === "cwd" ? "Selecionar pasta de trabalho" : field === "env-path" ? "Selecionar arquivo .env" : "Selecionar executável ou script";
      const value = (options.value || "").trim();
      const slash = value.lastIndexOf("/");
      const path = field === "cwd" ? value : slash >= 0 ? (value.slice(0, slash) === "container:" ? "container:/" : value.slice(0, slash) || "/") : cwd;
      const result = new Promise(resolve => { finish = resolve; });
      dialog.showModal(); $("location").value = path; $("location").focus(); load(path);
      return result;
    }
  };
})();
