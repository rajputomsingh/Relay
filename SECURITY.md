# Security Policy

Relay takes the security of its event ingestion and webhook delivery infrastructure seriously. We appreciate responsible security research and ask that potential vulnerabilities be reported privately.

## Supported Versions

Security fixes are applied to the latest maintained version of Relay.

Version Security support

---

Latest maintained release Supported Older releases Not guaranteed

If you are unsure whether a version is supported, include the version or commit hash in your report.

## Reporting a Vulnerability

**Please do not disclose security vulnerabilities through public GitHub issues, discussions, or pull requests.**

Use GitHub's private vulnerability reporting for this repository:

https://github.com/rajputomsingh/Relay/security/advisories

Select **Report a vulnerability** and include:

- A clear description of the vulnerability and its potential impact.
- The affected version, release tag, or commit.
- Reproduction steps or a proof of concept.
- Any relevant logs, requests, or configuration details.
- Whether the issue is known to be exploitable in a deployed
  environment.

Do not include real credentials, production secrets, or other users' private data in a report. Use redacted examples wherever possible.

If private vulnerability reporting is unavailable, contact the repository maintainer privately through GitHub rather than opening a public issue.

## Scope

Reports are especially useful when they identify security issues
affecting:

- Authentication, API-key handling, or authorization boundaries.
- Event ingestion, request validation, or idempotency controls.
- Webhook delivery, destination validation, or server-side request forgery (SSRF).
- HMAC signature generation, verification, or replay protection.
- Tenant or endpoint isolation and unauthorized access to event data.
- Retry, replay, or dead-letter workflows that expose or alter another user's data.
- Denial of service caused by unbounded payloads, concurrency, retries, or resource consumption.
- Exposure of API keys, signing secrets, database credentials, or other sensitive information.

This list is not exhaustive. If you are unsure whether an issue is
security-related, report it privately.

## Response and Disclosure

- **Acknowledgment:** We aim to acknowledge reports within 72 hours.
- **Assessment:** We will review the report, validate the issue where possible, and communicate its severity and status.
- **Updates:** We will keep the reporter informed as remediation progresses.
- **Disclosure:** We prefer coordinated disclosure. The disclosure date and any public credit will be agreed with the reporter after a fix or mitigation is available.

Please allow reasonable time for investigation and remediation. Response and remediation timelines may vary depending on severity, complexity, and maintainer availability.

## Researcher Recognition

With the reporter's permission, we are happy to credit security researchers in the relevant advisory or release notes.

Thank you for helping keep Relay and its users secure.
