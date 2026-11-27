# Docker Rules (PART 26)

⚠️ **These rules are NON-NEGOTIABLE. Violations are bugs.** ⚠️

## CRITICAL - NEVER DO
- Never put Dockerfiles or Compose files outside `docker/`.
- Never copy or symlink prebuilt host binaries into an image; build inside the image workflow.
- Never run production or development Compose files during AI testing.
- Never use project-directory runtime volumes or host networking for tests.

## CRITICAL - ALWAYS DO
- Use the specified multi-stage, static, non-root image design and exact runtime paths.
- Keep production, development, and test Compose configurations distinct.
- Use `docker/docker-compose.test.yml` only through the project test scripts and isolate runtime data in project-scoped temp directories.
- Scan built images and verify published image metadata and behavior.

## KEY DECISIONS (pre-answered)
| Question | Answer | Spec Reference |
|---|---|---|
| Where are Docker files? | `docker/`. | PART 26 |
| May AI run production Compose? | No. | PART 28 |

## QUICK REFERENCE
One-off containers must be named and self-cleaning; remove only resources created for this task.

---
For complete details, see AI.md PART 26
