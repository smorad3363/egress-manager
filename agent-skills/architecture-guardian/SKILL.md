---
name: architecture-guardian
description: Review Egress Manager changes for privilege, ownership, module-boundary, and V1-scope violations.
---

# Architecture Guardian

1. Read active spec and relevant ADRs.
2. Keep `egress-web` unprivileged and all privileged network operations in typed `egressd` handlers.
3. Reject hidden direct fallback, foreign configuration ownership, and V2 scope creep.
4. Require an ADR for substantial boundary changes.
5. Report exact file and symbol for each violation.
