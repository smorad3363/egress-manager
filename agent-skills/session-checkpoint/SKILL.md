---
name: session-checkpoint
description: Create compact green checkpoints that remain resumable without chat history.
---

# Session Checkpoint

1. Run smallest complete gate for active task.
2. Update `.project/STATE.yaml` with phase, task, next action, exact test commands, result, and touched areas.
3. Keep long rationale in ADRs, not state.
4. Commit code, tests, docs, and state together only when green.
5. Set `last_green_ref` to resulting commit in the next state update.
