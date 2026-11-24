(() => {
  const form = document.querySelector("[data-graphql-url]");
  const query = document.getElementById("graphql-query");
  const operationName = document.getElementById("graphql-operation-name");
  const variables = document.getElementById("graphql-variables");
  const token = document.getElementById("graphql-token");
  const output = document.getElementById("graphql-result");
  const status = document.getElementById("graphql-status");
  if (!form || !query || !output || !status) return;
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    status.textContent = form.dataset.statusRunning;
    try {
      const body = { query: query.value };
      if (operationName && operationName.value.trim()) body.operationName = operationName.value.trim();
      if (variables && variables.value.trim()) body.variables = JSON.parse(variables.value);
      const headers = { "Content-Type": "application/json", Accept: "application/json" };
      if (token && token.value.trim()) {
        headers.Authorization = token.value.trim().startsWith("Bearer ")
          ? token.value.trim()
          : `Bearer ${token.value.trim()}`;
      }
      const response = await fetch(form.dataset.graphqlUrl, {
        method: "POST",
        headers,
        body: JSON.stringify(body),
      });
      const result = await response.text();
      try {
        output.textContent = JSON.stringify(JSON.parse(result), null, 2);
      } catch {
        output.textContent = result;
      }
      status.textContent = response.ok
        ? form.dataset.statusComplete
        : form.dataset.statusHttp.replace("{status}", response.status);
    } catch (error) {
      status.textContent = error instanceof SyntaxError
        ? form.dataset.statusInvalidJson
        : form.dataset.statusFailed;
    }
  });
})();
