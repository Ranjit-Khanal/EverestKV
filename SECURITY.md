# Security

## Current security model

EverestKV is early-stage software. At the moment it has **no security features**:

- **No authentication.** The server doesn't support `AUTH`, and the dashboard has no login.
  Anyone who can reach a port can read and overwrite every key.
- **No TLS.** Traffic, including all keys and values, travels in plaintext.
- **Binds to all interfaces.** The server always listens on `:6379`, which can't be changed yet
  without editing code. The dashboard defaults to `:8080`.
- **No resource limits.** There are no connection limits and no request-size limits. A client can
  declare a very large bulk-string length and make the server try to allocate that much memory.

## Deploying safely

Until authentication exists, treat EverestKV as a **trusted-network-only** service:

- Firewall port `6379` so that only your application hosts can reach it. For example:
  `ufw deny 6379` plus an allow rule for your app server, or bind it inside a private
  Docker network.
- Run the dashboard with `-addr 127.0.0.1:8080` and reach it through an SSH tunnel
  (`ssh -L 8080:localhost:8080 your-vps`) or an authenticating reverse proxy.
- Run it as an unprivileged user.

## Reporting a vulnerability

Please **do not open a public issue** for security problems. Report them privately using
GitHub's [private vulnerability reporting](https://github.com/Ranjit-Khanal/everestkv/security/advisories/new)
on this repository.

Include what you found, how to reproduce it, and the impact you expect. We'll acknowledge the
report, work on a fix, and credit you in the release notes unless you'd rather not be named.
