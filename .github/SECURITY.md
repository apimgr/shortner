# Security Policy

## Supported versions

Security fixes are provided for the latest published stable release. Update
to the latest stable release before reporting an issue against an older
version.

## Reporting a vulnerability

Use [GitHub private vulnerability reporting](https://github.com/apimgr/shortner/security/advisories/new)
as the primary reporting channel. Do not file vulnerabilities as public bug
reports or disclose them publicly before coordinated disclosure.

The running service also provides `/server/security` and
`/.well-known/security.txt`. For an instance-specific report, use
`/server/contact?security_id=...` after following the instructions on that
instance's security page. Email notifications are courtesy copies and are
not the primary reporting channel.

The maintainers coordinate triage, remediation, and a disclosure date with
the reporter through the private advisory. See the [security documentation](https://apimgr-shortner.readthedocs.io/en/latest/security/)
for details about report handling and disclosure.
