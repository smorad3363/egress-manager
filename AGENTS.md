# Egress Manager agent rules

- Read `.project/STATE.yaml` before substantial work.
- Treat specs as authoritative. Priority: user, this file, constitution, active spec, ADRs, tested code, state, optional memory, conversation.
- Use targeted retrieval (`rg`, `git diff`, symbol lookup) instead of rereading the repository.
- Never flush firewall rules not owned by this project. Never run `nft flush ruleset` or `iptables -F` as a normal operation.
- Every network mutation requires inspect, plan, snapshot, native validation, management-path protection, apply, verify, commit, and rollback.
- Protect SSH, loopback, required management routes, and the Egress Manager panel from self-lockout.
- Keep privileged networking inside `egressd`; `egress-web` stays unprivileged and uses typed authenticated IPC.
- Preserve foreign nftables, iptables, UFW, Docker, Kubernetes, Marzban, Xray, WireGuard, and OpenVPN state.
- Never expose secrets in logs, APIs, diffs, fixtures, or generated diagnostics.
- Every bug fix gets a regression test when practical. Never disable tests or linters to obtain green output.
- Before claiming completion, run the verification gate and record unavailable checks honestly.
- After each green atomic milestone, update `.project/STATE.yaml` and commit code, tests, and state together.
- `START` executes the roadmap. `CONTINUE` recovers from state and current diff, then resumes `next_action`.
- Keep durable Markdown Obsidian-compatible. Store decisions in ADRs, not in state.
- V2 backlog stays deferred until V1 is fully green.
