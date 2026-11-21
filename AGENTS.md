# Project SPEC

Project: shortner
Role: Efficient loader for AI.md

⚠️ **THIS FILE IS AUTO-LOADED EVERY CONVERSATION. FOLLOW IT EXACTLY.** ⚠️

## Source of truth

- `AI.md` is the complete, read-only implementation specification. Read the relevant PART and its FINAL checklist before changing code or docs.
- `IDEA.md` defines project intent and variables. Do not edit `## Project variables` without user confirmation.
- Never treat existing code, `TODO.*`, or `AUDIT.*` as authoritative over `AI.md`.
- Record AI.md-only contradictions in `/root/Projects/github/apimgr/TODO.API.md` after searching it first. Project work follows the project TODO rules.

## Project requirements

- `shortner` is the server; `shortner-cli` is its required companion client.
- Use server-side Go templates, progressive enhancement, and mobile-first responsive CSS. Core features must work without JavaScript.
- Validate all inputs. Use Argon2id for config/backup passwords and SHA-256 for API tokens. Never use CGO; set `CGO_ENABLED=0`.
- Support all eight targets: linux, darwin, windows, and freebsd on amd64 and arm64.
- Tor support auto-enables when available. I2P is opt-in. Overlay addresses always use HTTP and never receive HTTPS redirects, HSTS, or insecure-request upgrades.
- Use the built-in scheduler; do not add external cron or premium tiers.
- Keep Docker files under `docker/`; CI uses explicit commands, never Makefile targets.

## Working rules

- Read the current file and relevant AI.md PART before editing. Follow the spec exactly; do not guess product behavior.
- Keep source and documentation in this repository except the explicitly named API-template issue log.
- Keep user changes. Do not use destructive cleanup, history rewriting, force pushes, or hook bypasses.
- Do not add stubs, TODO/FIXME/HACK comments, commented-out code, credentials, or AI attribution.
- Update documentation and server-rendered pages along with implementation changes.
- The primary agent alone runs validation and the commit workflow. Commit only through `gitcommit --dir {project_dir} all` after required gates and `.git/COMMIT_MESS` preparation.

## Rule references

Read the applicable `.claude/rules/*.md` guide together with its referenced AI.md PART:

- `ai-rules.md`: PART 0–1
- `project-rules.md`: PART 2–4
- `config-rules.md`: PART 5–6, 12
- `binary-rules.md`: PART 7–8, 32
- `backend-rules.md`: PART 9–11, 31
- `api-rules.md`: PART 13–15
- `frontend-rules.md`: PART 16
- `features-rules.md`: PART 17–22
- `service-rules.md`: PART 23–24
- `makefile-rules.md`: PART 25
- `docker-rules.md`: PART 26
- `cicd-rules.md`: PART 27
- `testing-rules.md`: PART 28–30

The root `AGENTS.md` must remain an exact real-file copy of this loader.
