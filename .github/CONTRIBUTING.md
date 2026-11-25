# Contributing

## Development

Use the maintained Go toolchain container. `make dev` builds the server and
companion CLI into a project-scoped temporary directory. `make test` checks
version precedence, runs the Go unit suite, and enforces 60% coverage. Run
`tests/run_tests.sh` for the Docker integration suite and `tests/e2e.sh` when
changing server-rendered pages or browser behavior.
Build the documentation with `mkdocs build --strict` after editing `docs/`.

Keep changes focused, add or update tests with code changes, and update the
relevant documentation and server-rendered pages. Pull requests should target
`main`; include the commands run and their results in the pull request.

## Security reports

Do not report vulnerabilities in public issues. Use [GitHub private
vulnerability reporting](https://github.com/apimgr/shortner/security/advisories/new)
as the primary channel. See [SECURITY.md](SECURITY.md) for the complete
reporting and disclosure process.
