# Project Audit

Started: 2026-11-23

## Pass 5: Spec Compliance
- [ ] PART 7/11/18: `blocklist_update` is a registered failing placeholder. Permanent IP blocking and temporary abuse blocks work, but remote source downloads, parsing/atomic refresh/freshness, and domain-feed matching are missing.
- [x] PART 11/18: PGP keyserver publishing lacked durable retry schedule/state and host-specific VKS/HKP behavior; implemented.
- [x] PART 12: hostname entries in `trusted_proxies.additional` resolved only at startup; added five-minute DNS refresh.
- [x] PART 13/16: health comments described HTML as deferred; corrected comments and localized the health page.
- [ ] PART 0/IDEA: primary session is resolving required IDEA variables and owner-cookie compatibility; cookie changes are intentionally untouched.

## Pass 4: Documentation Completeness
- [x] User docs overstated missing IP blocking and frontend form behavior; clarified current enforcement and actual form feedback.
- [ ] User docs need a complete feature-to-code comparison.
- [x] Frontend content negotiation lacked the PART 16 client-classification/conversion behavior; implemented and added focused tests.
