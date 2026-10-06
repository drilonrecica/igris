# Security policy

## Supported versions

igris is pre-1.0. Only the latest release gets security fixes.

| Version | Supported |
|---|---|
| latest 0.1.x | yes |
| older | no — please upgrade |

## Reporting a vulnerability

Please **don't open a public issue** for a security problem. Report it privately through GitHub: go to the [Security tab](https://github.com/drilonrecica/igris/security) and choose **Report a vulnerability** ([private vulnerability reporting](https://docs.github.com/en/code-security/security-advisories/guidance-on-reporting-and-writing-information-about-vulnerabilities/privately-reporting-a-security-vulnerability)).

Include the igris version (`igris version`), your OS, and the steps or the plan/config that shows the problem. Please use a synthetic plan, not one from a private project, and never send credentials or notification URLs.

You'll get an answer within a week. Fixes are released as a patch version and credited in the release notes unless you'd rather stay anonymous.

## What counts

igris starts Claude Code sessions that act on your repository, so the interesting boundaries are:

- plan text, notes or command output reaching a shell or a terminal unescaped;
- a session changing igris's own behavior (config, signals, state, the plan outside Status cells);
- credentials or notification secrets being read, logged or sent anywhere;
- the skip-permissions confirmation being bypassed;
- file permissions or lock/state handling that lets another user interfere with a run.

What a Claude Code session itself does inside your project is governed by the run mode you choose (see `SPEC.md` §7), not by igris.
