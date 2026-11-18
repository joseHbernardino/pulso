(() => {
  "use strict";
  const $ = name => document.getElementById("project-" + name);
  let report = null, generation = 0, busy = false, controller = null;
  function reset() {
    generation++; controller?.abort(); controller = null; report = null; busy = false;
    $("inspect").disabled = false; $("assistant").hidden = false; $("result").hidden = true;
    $("status").textContent = ""; $("assistant").setAttribute("aria-busy", "false");
  }
  function updateSelection() {
    const suggestion = report?.suggestions[Number($("command").value)];
    $("use").disabled = !suggestion?.available;
  }
  $("command").addEventListener("change", updateSelection);
  $("choose").addEventListener("click", async () => {
    const path = await window.PulsoPaths.choose({ field: "cwd", value: $("path").value });
    if (path) { $("path").value = path; $("inspect").click(); }
  });
  $("path").addEventListener("input", () => {
    reset();
  });
  $("inspect").addEventListener("click", async () => {
    if (busy) return;
    const path = $("path").value.trim();
    if (!path) { $("status").textContent = "Selecione ou informe a pasta do projeto."; $("path").focus(); return; }
    const token = ++generation;
    controller = new AbortController();
    busy = true; $("inspect").disabled = true; $("result").hidden = true;
    $("status").textContent = "Analisando arquivos e comandos disponíveis…";
    $("assistant").setAttribute("aria-busy", "true");
    try {
      const response = await fetch("/api/v1/projects/inspect", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ path }), signal: controller.signal });
      const data = await response.json();
      if (token !== generation) return;
      if (!response.ok) throw new Error(data.error || "Não foi possível analisar o projeto.");
      report = data;
      $("notes").replaceChildren(); $("command").replaceChildren(); $("env").replaceChildren();
      for (const note of report.notes) { const item = document.createElement("li"); item.textContent = note; $("notes").append(item); }
      report.suggestions.forEach((suggestion, index) => {
        $("command").add(new Option(suggestion.title + (suggestion.available ? "" : " · indisponível"), String(index)));
        if (suggestion.missing?.length) {
          const item = document.createElement("li"); item.textContent = suggestion.title + ": falta " + suggestion.missing.join(", ") + " no ambiente do Pulso."; $("notes").append(item);
        }
      });
      const available = report.suggestions.findIndex(suggestion => suggestion.available);
      if (available >= 0) $("command").value = String(available);
      $("env").add(new Option("Sem arquivo .env", ""));
      for (const file of report.env_files) $("env").add(new Option(file, file));
      if (report.env_files.length === 1) $("env").value = report.env_files[0];
      $("status").textContent = report.suggestions.length ? "Análise concluída. Confira o comando e o ambiente antes de usar." : "Informe o comando manualmente na seção Execução.";
      $("result").hidden = false; updateSelection();
    } catch (error) { if (token === generation) $("status").textContent = error.message; }
    finally { if (token === generation) { busy = false; $("inspect").disabled = false; $("assistant").setAttribute("aria-busy", "false"); } }
  });
  $("use").addEventListener("click", () => {
    const suggestion = report?.suggestions[Number($("command").value)];
    if (!suggestion?.available) return;
    document.dispatchEvent(new CustomEvent("pulso:project-selected", { detail: { config: suggestion.config, envFile: $("env").value } }));
  });
  window.PulsoProjects = {
    reset,
    editing() { $("assistant").hidden = true; }
  };
})();
