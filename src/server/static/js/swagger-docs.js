(() => {
  const root = document.querySelector("[data-openapi-url]");
  const output = document.getElementById("openapi-document");
  const status = document.getElementById("openapi-status");
  const operations = document.getElementById("openapi-operations");
  const tokenInput = document.getElementById("openapi-token");
  if (!root || !output || !status || !operations) return;
  const specificationURL = new URL(root.dataset.openapiUrl, window.location.href);
  const applicationPrefix = specificationURL.pathname.replace(/\/api\/swagger\/?$/, "");

  const addLabel = (form, text, control) => {
    const label = document.createElement("label");
    label.textContent = text;
    label.htmlFor = control.id;
    form.append(label, control);
  };

  const parameterInput = (operationId, parameter, index, apiVersion) => {
    const input = document.createElement("input");
    input.id = `${operationId}-parameter-${index}`;
    input.name = parameter.name;
    input.required = Boolean(parameter.required);
    input.value = parameter.name === "api_version" ? apiVersion : "";
    input.placeholder = parameter.schema && parameter.schema.type === "integer" ? "0" : "";
    if (parameter.description) input.setAttribute("aria-description", parameter.description);
    return input;
  };

  const attachmentFilename = (header) => {
    const match = /filename\*?=(?:UTF-8''|"?)([^";]+)/i.exec(header || "");
    const raw = match ? match[1].replace(/"$/, "") : "api-response";
    let name = raw;
    try {
      name = decodeURIComponent(raw);
    } catch {
      // Keep the server-provided filename when it is not URI encoded.
    }
    return name.split(/[\\/]/).pop().replace(/[\u0000-\u001f\u007f]/g, "").slice(0, 255) || "api-response";
  };

  const renderOperation = (path, method, operation, apiVersion) => {
    const details = document.createElement("details");
    details.className = `api-operation api-operation-${method}`;
    const summary = document.createElement("summary");
    summary.textContent = `${method.toUpperCase()} ${path} — ${operation.summary || operation.operationId}`;
    details.append(summary);

    const form = document.createElement("form");
    form.className = "api-operation-form";
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      const result = details.querySelector(".api-operation-result");
      const resultStatus = details.querySelector(".api-operation-status");
      resultStatus.textContent = "Sending request…";
      try {
        let requestPath = path.replace(/\{([^}]+)\}/g, (_, name) => {
          const field = form.elements.namedItem(`path:${name}`);
          if (!field || !field.value.trim()) throw new Error(`Enter a value for ${name}.`);
          return encodeURIComponent(field.value.trim());
        });
        const target = new URL(`${applicationPrefix}/${requestPath.replace(/^\/+/, "")}`, window.location.origin);
        for (const parameter of operation.parameters || []) {
          if (parameter.in !== "query") continue;
          const value = form.elements.namedItem(`query:${parameter.name}`)?.value;
          if (value !== undefined && value !== "") target.searchParams.set(parameter.name, value);
        }
        const headers = { Accept: "application/json, text/plain;q=0.9" };
        const token = tokenInput ? tokenInput.value.trim() : "";
        if (token) headers.Authorization = token.startsWith("Bearer ") ? token : `Bearer ${token}`;
        const request = { method: method.toUpperCase(), headers };
        if (operation.requestBody) {
          const body = form.elements.namedItem("request-body");
          if (body && body.value.trim()) {
            headers["Content-Type"] = "application/json";
            request.body = body.value;
          }
        }
        const response = await fetch(target, request);
        const contentType = response.headers.get("content-type") || "";
        const disposition = response.headers.get("content-disposition") || "";
        if (contentType.toLowerCase().includes("application/octet-stream") || /attachment/i.test(disposition)) {
          if (method.toUpperCase() === "HEAD") {
            result.textContent = [...response.headers.entries()].map(([name, value]) => `${name}: ${value}`).join("\n") || "No response body (HEAD).";
            resultStatus.textContent = `HTTP ${response.status} ${response.statusText} — response metadata`;
            return;
          }
          const objectURL = URL.createObjectURL(await response.blob());
          const download = document.createElement("a");
          download.href = objectURL;
          download.download = attachmentFilename(disposition);
          download.textContent = `Download ${download.download}`;
          result.replaceChildren(download);
          resultStatus.textContent = `HTTP ${response.status} ${response.statusText} — binary response ready`;
          window.setTimeout(() => URL.revokeObjectURL(objectURL), 60_000);
          return;
        }
        const responseText = await response.text();
        let display = responseText;
        try {
          display = JSON.stringify(JSON.parse(responseText), null, 2);
        } catch {
          // Preserve text and binary response messages as received.
        }
        result.textContent = display;
        resultStatus.textContent = `HTTP ${response.status} ${response.statusText}`;
      } catch (error) {
        resultStatus.textContent = error.message || "Request failed.";
      }
    });

    for (const [index, parameter] of (operation.parameters || []).entries()) {
      const input = parameterInput(operation.operationId, parameter, index, root.dataset.apiVersion || "v1");
      input.dataset.location = parameter.in;
      input.dataset.parameterName = parameter.name;
      if (parameter.in === "path") {
        input.name = `path:${parameter.name}`;
      } else if (parameter.in === "query") {
        input.name = `query:${parameter.name}`;
      } else {
        continue;
      }
      addLabel(form, `${parameter.name}${parameter.required ? " (required)" : ""}`, input);
      if (parameter.description) {
        const description = document.createElement("p");
        description.textContent = parameter.description;
        form.append(description);
      }
    }

    if (operation.requestBody) {
      const textarea = document.createElement("textarea");
      textarea.id = `api-body-${operation.operationId}`;
      textarea.name = "request-body";
      textarea.rows = 7;
      textarea.spellcheck = false;
      const media = operation.requestBody.content && operation.requestBody.content["application/json"];
      textarea.value = JSON.stringify((media && (media.example || media.examples && Object.values(media.examples)[0]?.value)) || {}, null, 2);
      addLabel(form, "JSON request body", textarea);
    }

    const submit = document.createElement("button");
    submit.type = "submit";
    submit.textContent = "Try this operation";
    form.append(submit);
    const resultStatus = document.createElement("p");
    resultStatus.className = "api-operation-status";
    resultStatus.setAttribute("role", "status");
    resultStatus.setAttribute("aria-live", "polite");
    const result = document.createElement("pre");
    result.className = "api-operation-result";
    result.tabIndex = 0;
    result.setAttribute("aria-label", `${operation.operationId} response`);
    details.append(form, resultStatus, result);
    return details;
  };

  fetch(root.dataset.openapiUrl, { headers: { Accept: "application/json" } })
    .then((response) => {
      if (!response.ok) throw new Error("OpenAPI document unavailable");
      return response.json();
    })
    .then((documentData) => {
      output.textContent = JSON.stringify(documentData, null, 2);
      const rendered = document.createElement("div");
      rendered.className = "openapi-interactive-operations";
      for (const [path, methods] of Object.entries(documentData.paths || {}).sort(([a], [b]) => a.localeCompare(b))) {
        for (const [method, operation] of Object.entries(methods).sort(([a], [b]) => a.localeCompare(b))) {
          rendered.append(renderOperation(path, method, operation, root.dataset.apiVersion || "v1"));
        }
      }
      operations.replaceChildren(rendered);
      status.textContent = "OpenAPI document loaded. Choose an operation to inspect or run it.";
    })
    .catch(() => {
      status.textContent = "Unable to load the document. Use the JSON link above.";
    });
})();
