# Configuration Rules (PART 5, 6, 12)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never commit generated `server.yml`, `cli.yml`, secrets, or runtime config.
- Never add runtime configuration APIs or an admin UI for settings; configuration is file-based.
- Never bypass validation, path security, secret handling, or precedence rules.
- Never silently change defaults or config key names.

## CRITICAL - ALWAYS DO
- Use canonical `server.*` keys and the exact config paths defined in AI.md.
- Validate config values and environment/flag overrides before use.
- Preserve precedence: command-line flags, environment variables, config files, embedded defaults.
- Keep server config, area files, secret files, help, and documentation synchronized.
- Respect mode-specific behavior and server/operator allowlists.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Where is operator configuration stored? | Generated OS config directory files. | PART 5, 12 |
| Can settings be changed through a runtime API? | No; file-only configuration. | PART 5 |

## QUICK REFERENCE
Read configuration defaults and path security before changing fields; update validation, tests, CLI help, and docs together.

---
For complete details, see AI.md PART 5, 6, 12
