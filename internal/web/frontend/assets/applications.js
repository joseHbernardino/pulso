(() => {
  "use strict";

  const dialog = document.getElementById("application-dialog");
  const form = document.getElementById("application-form");
  const fields = document.getElementById("application-fields");
  const error = document.getElementById("application-error");
  const submit = document.getElementById("application-submit");
  const close = document.getElementById("application-close");
  const cancel = document.getElementById("application-cancel");
  const example = document.getElementById("application-example");
  let submitting = false;

  function showError(message) {
    error.textContent = message;
    error.hidden = false;
    error.scrollIntoView({ block: "nearest" });
  }

  function clearError() {
    error.textContent = "";
    error.hidden = true;
  }

  function setSubmitting(value) {
    submitting = value;
    fields.disabled = value;
    submit.disabled = value;
    close.disabled = value;
    cancel.disabled = value;
    example.disabled = value;
    submit.textContent = value ? "Iniciando…" : "Iniciar aplicação";
    form.setAttribute("aria-busy", String(value));
  }

  function closeForm() {
    if (!submitting) dialog.close();
  }

  function parseEnvironment(text) {
    const result = Object.create(null);
    for (const [index, line] of text.split(/\r?\n/).entries()) {
      if (!line.trim()) continue;
      const equals = line.indexOf("=");
      const key = equals < 0 ? "" : line.slice(0, equals).trim();
      if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(key)) {
        throw new Error("Variável de ambiente na linha " + (index + 1) + ": use NOME=valor.");
      }
      if (Object.hasOwn(result, key)) throw new Error('A variável "' + key + '" aparece mais de uma vez.');
      result[key] = line.slice(equals + 1);
    }
    return result;
  }

  document.getElementById("new-application").addEventListener("click", () => { if(window.pulsoCanManage) dialog.showModal(); });
  close.addEventListener("click", closeForm);
  cancel.addEventListener("click", closeForm);
  dialog.addEventListener("cancel", event => {
    if (submitting) event.preventDefault();
  });
  dialog.addEventListener("close", () => {
    form.reset();
    document.getElementById("application-options").open = false;
    clearError();
  });
  form.addEventListener("input", clearError);
  example.addEventListener("click", () => {
    form.reset();
    document.getElementById("application-name").value = "demonstracao-web";
    document.getElementById("application-runtime").value = "process";
    document.getElementById("application-entrypoint").value = "sleep";
    document.getElementById("application-args").value = "3600";
    document.getElementById("application-restart").value = "never";
    document.getElementById("application-options").open = true;
    clearError();
    document.getElementById("application-name").focus();
  });

  form.addEventListener("submit", async event => {
    event.preventDefault();
    if (submitting || !window.pulsoCanManage || !form.reportValidity()) return;
    clearError();
    let payload;
    try {
      payload = {
        name: document.getElementById("application-name").value.trim(),
        entrypoint: document.getElementById("application-entrypoint").value.trim(),
        runtime: document.getElementById("application-runtime").value,
        cwd: document.getElementById("application-cwd").value.trim(),
        args: document.getElementById("application-args").value.split(/\r?\n/).filter(line => line.trim() !== ""),
        env: parseEnvironment(document.getElementById("application-env").value),
        restart_policy: document.getElementById("application-restart").value,
      };
    } catch (validationError) {
      showError(validationError.message);
      return;
    }

    setSubmitting(true);
    try {
      const response = await fetch("/api/v1/processes", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      const data = await response.json().catch(() => ({}));
      if (response.status === 401) {
        showError("Sua sessão expirou. Entre novamente no painel antes de iniciar a aplicação.");
        return;
      }
      if (response.status === 409) {
        showError("Já existe uma aplicação com esse nome. Escolha outro nome.");
        return;
      }
      if (!response.ok) {
        showError(data.error || "Não foi possível iniciar. Confira o programa e a pasta de trabalho.");
        return;
      }
      document.dispatchEvent(new CustomEvent("pulso:application-created", { detail: data }));
      dialog.close();
    } catch (_) {
      showError("A conexão foi interrompida. Confira a lista de aplicações antes de tentar novamente.");
    } finally {
      setSubmitting(false);
    }
  });
})();
