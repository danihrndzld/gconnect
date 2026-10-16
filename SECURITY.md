# Security policy

## Reporting a problem

Report security problems privately through GitHub: open the repository's **Security** tab and choose **Report a vulnerability**. Please do not open a public issue for them. I aim to answer within 7 days.

Examples of what to report: a token, password or cookie that reaches a log, an error text or the disk; a request sent to a host other than the configured `Hosts`; or a way to make the library read files or environment variables.

## What never to send

Never include real tokens, cookies, passwords, emails or captured Garmin answers in a report, an issue or a pull request, even redacted by hand. Describe the shape of the data instead, or build a small fixture with `gconnecttest`.

## Supported versions

Only the latest tagged release gets fixes while the module is at v0.
