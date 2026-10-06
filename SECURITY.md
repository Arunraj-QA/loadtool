# Security policy

## Reporting a vulnerability

Please do not report security problems in public issues.

**How to report:** use GitHub's private vulnerability reporting, under
[Security → Report a vulnerability](https://github.com/Arunraj-QA/loadtool/security/advisories/new).

If that page is not available, open an issue that asks only for a
private contact. Do not include any details in it.

**What to include:**

- what an attacker can do;
- how to reproduce it;
- the LoadTool version (`loadtool --version`).

**What happens next:** you will get an answer within a week. Fixes are
released as soon as they are ready, and you are credited unless you ask
not to be.

## Supported versions

LoadTool is in early development and has no stable release yet. Fixes go
into the latest version.

## Scope

LoadTool runs the scripts you give it and sends the requests they make.

- **Scripts are trusted code.** Running an untrusted script is like
  running an untrusted program. They run in an embedded JavaScript
  runtime with no file-system or process access, but they can make HTTP
  requests and use as much CPU and memory as you allow.
- **Load is your responsibility.** Only test systems you are allowed to
  test.
- **Reports are safe to open.** The HTML report is self-contained and
  escapes text from scripts.
