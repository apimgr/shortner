# Contributing

## Development

Use the maintained Go toolchain container. `make build` creates debug
binaries for all eight target platforms. `make test` runs lint and vulnerability
checks, the Go test suite with its 60% coverage gate, and the basic binary
suite through `tests/run_tests.sh`. Run `tests/incus.sh` for the full OS suite
and `tests/e2e.sh` when changing server-rendered pages or browser behavior.
Build the documentation with `mkdocs build --strict` after editing `docs/`.

Keep changes focused, add or update tests with code changes, and update the
relevant documentation and server-rendered pages. Pull requests should target
`main`; include the commands run and their results in the pull request.

## Security reports

Do not report vulnerabilities in public issues. Use [GitHub private
vulnerability reporting](https://github.com/apimgr/shortner/security/advisories/new)
as the primary channel. See [SECURITY.md](SECURITY.md) for the complete
reporting and disclosure process.
