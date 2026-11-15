(() => {
  const form = document.querySelector("[data-graphql-url]");
  const query = document.getElementById("graphql-query");
  const output = document.getElementById("graphql-result");
  const status = document.getElementById("graphql-status");
  if (!form || !query || !output || !status) return;
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    status.textContent = "Running query…";
    try {
      const response = await fetch(form.dataset.graphqlUrl, {
        method: "POST",
        headers: { "Content-Type": "application/json", Accept: "application/json" },
        body: JSON.stringify({ query: query.value }),
      });
      const result = await response.json();
      output.textContent = JSON.stringify(result, null, 2);
      status.textContent = response.ok ? "Query complete." : "The query returned an error.";
    } catch {
      status.textContent = "Unable to run the query. Submit the form without JavaScript.";
    }
  });
})();
