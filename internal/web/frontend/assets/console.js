(() => {
  "use strict";
  const $ = id => document.getElementById("console-" + id);
  const form = $("form");
  if (!form) return;

  const HISTORY_KEY = "pulso-console-history";
  const HISTORY_LIMIT = 20;
  const OUTPUT_CHUNK_LIMIT = 3000;
  // Terminal colour codes make the output unreadable in a plain <pre>.
  const ANSI = /\u001b\[[0-9;?]*[ -/]*[@-~]|\u001b[@-Z\\-_]/g;

  let running = null, history = [];

  const setState = (label, variant) => {
    const badge = $("state");
    badge.textContent = label;
    badge.className = "console-state" + (variant ? " is-" + variant : "");
  };

  function showError(message, issue) {
    const target = $("error");
    target.textContent = message;
    target.hidden = false;
    if (issue) window.PulsoPaths?.showGuidance(issue);
  }
  const clearError = () => { $("error").hidden = true; document.getElementById("application-path-guidance").hidden = true; };

  function append(text, kind) {
    const output = $("output");
    const atBottom = output.scrollHeight - output.scrollTop - output.clientHeight < 40;
    const chunk = document.createElement("span");
    chunk.className = kind;
    chunk.textContent = text.replace(ANSI, "");
    output.append(chunk);
    while (output.childElementCount > OUTPUT_CHUNK_LIMIT) output.firstElementChild.remove();
    if (atBottom) output.scrollTop = output.scrollHeight;
  }

  // History lives in this browser only: a command may carry a path or a value
  // that belongs to the person who typed it, not to the server.
  function loadHistory() {
    try { history = JSON.parse(localStorage.getItem(HISTORY_KEY) || "[]").filter(entry => typeof entry === "string"); }
    catch (_) { history = []; }
    renderHistory();
  }
  function renderHistory() {
    const select = $("history");
    select.replaceChildren();
    const placeholder = document.createElement("option");
    placeholder.value = ""; placeholder.textContent = history.length ? "Histórico de comandos…" : "Histórico vazio";
    select.append(placeholder);
    for (const entry of history) {
      const option = document.createElement("option");
      option.value = entry;
      option.textContent = entry.split("\n")[0].slice(0, 70) + (entry.includes("\n") ? " …" : "");
      select.append(option);
    }
  }
  function remember(command) {
    history = [command, ...history.filter(entry => entry !== command)].slice(0, HISTORY_LIMIT);
    try { localStorage.setItem(HISTORY_KEY, JSON.stringify(history)); } catch (_) { /* Optional convenience. */ }
    renderHistory();
  }

  async function loadApplications() {
    try {
      const response = await fetch("/api/v1/processes", { cache: "no-store" });
      if (!response.ok) return;
      const processes = await response.json();
      const select = $("app");
      const chosen = select.value;
      select.replaceChildren();
      const server = document.createElement("option");
      server.value = ""; server.textContent = "Servidor (pasta acima)";
      select.append(server);
      for (const process of processes || []) {
        const option = document.createElement("option");
        option.value = process.name;
        option.textContent = process.name + (process.config?.cwd ? " · " + process.config.cwd : "");
        select.append(option);
      }
      select.value = chosen;
    } catch (_) { /* The context list is a convenience, not a requirement. */ }
  }

  function setRunning(active) {
    $("run").disabled = active;
    $("stop").hidden = !active;
    $("command").readOnly = active;
  }

  async function run(command) {
    clearError();
    const payload = {
      command,
      cwd: $("cwd").value.trim(),
      app: $("app").value,
      shell: $("shell").value.trim(),
      timeout_seconds: Number($("timeout").value)
    };

    setRunning(true);
    setState("executando", "running");
    $("summary").textContent = "Em execução…";
    append(($("output").childElementCount ? "\n" : "") + "$ " + command + "\n", "note");
    remember(command);

    let response;
    try {
      response = await fetch("/api/v1/exec", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload)
      });
    } catch (error) {
      setRunning(false); setState("falhou", "fail");
      showError("Não foi possível falar com o painel: " + error.message);
      return;
    }

    if (!response.ok || !response.body) {
      let message = "Não foi possível executar o comando.";
      let issue = null;
      try { const data = await response.json(); message = data.error || message; issue = data.code ? data : null; } catch (_) { /* Not JSON. */ }
      setRunning(false); setState("falhou", "fail");
      showError(message, issue);
      return;
    }

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "", ended = false;

    const handle = event => {
      if (event.type === "start") {
        running = event.id;
        $("context").textContent = "Executando em " + event.cwd;
        $("shell-info").textContent = event.shell + " · " + event.cwd;
      } else if (event.type === "output") {
        append(event.text, event.stream === "stderr" ? "err" : "out");
      } else if (event.type === "error") {
        ended = true;
        setState("falhou", "fail");
        $("summary").textContent = "O comando não pôde ser executado.";
        showError(event.error);
      } else if (event.type === "end") {
        ended = true;
        const seconds = (event.duration_ms / 1000).toFixed(event.duration_ms < 10000 ? 2 : 0);
        const parts = ["código de saída " + event.exit_code, seconds + "s"];
        if (event.timed_out) parts.push("encerrado pelo tempo limite");
        if (event.stopped) parts.push("interrompido");
        if (event.output_limited) parts.push("saída truncada na exibição");
        $("summary").textContent = parts.join(" · ");
        const ok = event.exit_code === 0 && !event.timed_out && !event.stopped;
        setState(ok ? "concluído" : "falhou", ok ? "ok" : "fail");
        append("\n[" + parts.join(" · ") + "]\n", "note");
      }
    };

    try {
      for (;;) {
        const { value, done } = await reader.read();
        if (done) break;
        buffer += decoder.decode(value, { stream: true });
        let newline;
        while ((newline = buffer.indexOf("\n")) >= 0) {
          const line = buffer.slice(0, newline);
          buffer = buffer.slice(newline + 1);
          if (!line.trim()) continue;
          try { handle(JSON.parse(line)); } catch (_) { /* A partial line is read on the next pass. */ }
        }
      }
      if (buffer.trim()) { try { handle(JSON.parse(buffer)); } catch (_) { /* Incomplete tail. */ } }
    } catch (error) {
      showError("A saída foi interrompida: " + error.message);
    } finally {
      running = null;
      setRunning(false);
      if (!ended) { setState("interrompido", "fail"); $("summary").textContent = "A conexão terminou antes do fim do comando. Ele pode ainda estar em execução no servidor."; }
      loadApplications();
    }
  }

  form.addEventListener("submit", event => {
    event.preventDefault();
    if (!window.pulsoCanManage || running !== null) return;
    const command = $("command").value.trim();
    if (!command) { showError("Escreva um comando."); $("command").focus(); return; }
    run(command);
  });

  $("command").addEventListener("keydown", event => {
    if ((event.ctrlKey || event.metaKey) && event.key === "Enter") { event.preventDefault(); form.requestSubmit(); }
  });

  $("stop").addEventListener("click", async () => {
    if (running === null) return;
    $("stop").disabled = true;
    try {
      const response = await fetch("/api/v1/exec/" + encodeURIComponent(running) + "/stop", { method: "POST" });
      if (!response.ok) {
        const data = await response.json().catch(() => ({}));
        window.PulsoUI?.notify(data.error || "Não foi possível parar o comando.", "error");
      } else {
        append("\n[parada solicitada]\n", "note");
      }
    } finally { $("stop").disabled = false; }
  });

  for (const chip of document.querySelectorAll(".console-chip")) {
    chip.addEventListener("click", () => {
      const field = $("command");
      field.value = chip.dataset.command;
      field.focus();
    });
  }

  $("history").addEventListener("change", event => {
    if (!event.target.value) return;
    $("command").value = event.target.value;
    event.target.value = "";
    $("command").focus();
  });

  document.querySelectorAll("[data-browse-path]").forEach(button => button.addEventListener("click", async () => {
    const selected = await window.PulsoPaths.choose({ field: "cwd", value: $("cwd").value, cwd: $("cwd").value });
    if (selected) $("cwd").value = selected;
  }));

  $("copy").addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText($("output").textContent);
      window.PulsoUI?.notify("Saída copiada.");
    } catch (_) { window.PulsoUI?.notify("O navegador não liberou a área de transferência.", "error"); }
  });

  $("download").addEventListener("click", () => {
    const blob = new Blob([$("output").textContent], { type: "text/plain;charset=utf-8" });
    const link = document.createElement("a");
    link.href = URL.createObjectURL(blob);
    link.download = "pulso-console-" + new Date().toISOString().slice(0, 19).replace(/[:T]/g, "-") + ".txt";
    link.click();
    URL.revokeObjectURL(link.href);
  });

  $("clear").addEventListener("click", () => {
    $("output").replaceChildren();
    $("summary").textContent = "A saída aparece aqui enquanto o comando executa.";
    $("context").textContent = "Nenhum comando executado nesta sessão.";
    $("shell-info").textContent = "";
    setState("pronto", "");
  });

  function applyAccess() {
    const allowed = !!window.pulsoCanManage;
    $("denied").hidden = allowed;
    for (const element of form.elements) element.disabled = !allowed;
    if (allowed) { $("stop").hidden = running === null; $("run").disabled = running !== null; }
  }
  document.addEventListener("pulso:access-ready", applyAccess);

  applyAccess();
  loadHistory();
  loadApplications();
})();
