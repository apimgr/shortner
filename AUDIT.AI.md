# Project Audit

Started: 2026-11-23

## Pass 5: Spec Compliance
- [ ] PART 7/11/18: `blocklist_update` is a registered failing placeholder. Permanent IP blocking and temporary abuse blocks work, but remote source downloads, parsing/atomic refresh/freshness, and domain-feed matching are missing.
- [ ] PART 11/18: PGP keyserver publishing has no durable retry schedule/state and does not implement the host-specific VKS/HKP contract.
- [x] PART 12: hostname entries in `trusted_proxies.additional` resolved only at startup; added five-minute DNS refresh.
- [x] PART 13/16: HTML health template was implemented but health comments described HTML as deferred; corrected comments and localized health page labels.
- [ ] PART 0/16: docs/source refer to absent `TODO.AI.md`; current audit/TODO tracking needs reconciliation.
- [ ] PART 0/IDEA: primary session is resolving required IDEA variables and owner-cookie compatibility; cookie changes are intentionally untouched.
- [ ] FINAL: AI.md final checklist contradicts PART 28 about the container for Go unit tests.

## Pass 4: Documentation Completeness
- [x] User docs overstated missing IP blocking and frontend form behavior; clarified current enforcement and actual form feedback.
- [ ] User docs contain stale TODO references and need a complete feature-to-code comparison.
- [x] Frontend content negotiation lacked the PART 16 client-classification/conversion behavior; implemented and added focused tests.

