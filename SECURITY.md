# Security policy

## Reporting a vulnerability

Report vulnerabilities privately through GitHub's
[private vulnerability reporting](https://github.com/heynemann/agnthub/security/advisories/new).
Do not open a public issue, pull request or discussion for them.

Include the agnthub version (`agnthub --version`), your platform, the steps or a minimal source
repository that reproduces the problem, and the impact you expect. You will get an
acknowledgement within a few days, and a fix or a decision before any public disclosure is
agreed with you.

## Supported versions

Fixes go into the latest release. Upgrade to it before reporting, unless the problem is that
you cannot.

## Scope

In scope: the `agnthub` CLI and its packages in this repository, the release archives and
`SHA256SUMS` published on this repository's GitHub Releases, and the `agnthub` npm launcher.
Examples: a source repository that makes agnthub execute code, read or write outside the
destinations a plan shows, overwrite files agnthub did not install, or leak credentials into
locks, output or install reports; a launcher that runs a binary whose checksum does not match.

The trust boundaries are deliberate: the configured skill installer is trusted code that
agnthub runs as configured, and an installed agent or skill runs inside your Harness with
whatever that Harness grants it. agnthub does not sandbox either; see
[ADR 0003](docs/adr/0003-presence-dependencies-and-trusted-skill-backends.md).

Problems with the hosted registry at app.agnthub.sh or api.agnthub.sh can be reported the same
way.
