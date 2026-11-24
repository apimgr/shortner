# AI-owned tasks

- [ ] Replace the three pre-existing `strconv.ParseBool` calls in `src/cache/redis.go` with the project parser.
- [ ] Confirm and resolve the pre-existing `Makefile` linker flag convention finding for `-trimpath`.
- [ ] Align `src/common/version/version.go` build date handling with the project Go lint convention.
- [ ] Resolve the pre-existing ignored return-value findings reported by `golangci-lint`; prioritize production code, then tests.
- [ ] Resolve the two pre-existing staticcheck findings in `src/httpserver/securityreport.go` and `src/httpserver/wellknown.go`.
