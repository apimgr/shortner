(() => {
  const root = document.querySelector("[data-openapi-url]");
  const output = document.getElementById("openapi-document");
  const status = document.getElementById("openapi-status");
  if (!root || !output || !status) return;
  fetch(root.dataset.openapiUrl, { headers: { Accept: "application/json" } })
    .then((response) => {
      if (!response.ok) throw new Error("OpenAPI document unavailable");
      return response.json();
    })
    .then((documentData) => {
      output.textContent = JSON.stringify(documentData, null, 2);
      status.textContent = "OpenAPI document loaded.";
    })
    .catch(() => {
      status.textContent = "Unable to load the document. Use the JSON link above.";
    });
})();
