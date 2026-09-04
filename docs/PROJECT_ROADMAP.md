# Egress Manager
## Codex Master Execution Roadmap

**Document role:** This is the authoritative implementation roadmap for the Egress Manager project.

**Execution model:** Long-running, checkpointed, interruption-safe autonomous implementation.

**Primary commands from the user:**

- `START`
- `CONTINUE`

---

# 1. EXECUTION CONTRACT

## Before START

When this roadmap is first provided:

1. Read it completely once.
2. Save it inside the repository as:

```text
docs/PROJECT_ROADMAP.md
```

3. Do not begin implementing product features until the user explicitly says:

```text
START
```

4. Do not repeatedly ask the user to reconfirm decisions already defined by this roadmap.

---

# 2. MEANING OF START

When the user says:

```text
START
```

begin the project.

From that point forward:

- execute the roadmap sequentially;
- continue from phase to phase without asking for routine confirmations;
- make reasonable technical decisions consistent with this roadmap;
- checkpoint frequently;
- test continuously;
- fix discovered defects before proceeding;
- do not stop merely to give progress reports;
- continue for as much of the roadmap as the current Codex session/environment allows.

Stop only when one of these is genuinely required:

- an OS/tool permission must be granted by the user;
- an external credential or secret is required;
- an irreversible destructive operation affecting user-owned data is required;
- the environment prevents further execution;
- a critical requirement is impossible and no safe fallback exists.

If execution is interrupted by:

- internet loss;
- power loss;
- VS Code crash;
- Codex restart;
- machine restart;
- terminal crash;
- context/session termination;
- tool failure;

the project must remain resumable.

---

# 3. MEANING OF CONTINUE

When the user later says only:

```text
CONTINUE
```

DO NOT restart planning.

DO NOT re-read the entire repository.

DO NOT repeat project onboarding unless repository state is missing or corrupted.

Perform this recovery sequence:

```text
1. Read AGENTS.md
2. Read .project/STATE.yaml
3. Run git status --short --branch
4. Inspect current HEAD
5. Inspect only the current git diff
6. Read the active specification referenced by STATE.yaml
7. Read only memories/docs referenced by the active task
8. Check whether an incomplete network transaction exists
9. Check the last recorded test status
10. Resume exactly from STATE.yaml -> next_action
```

If a Spec Kit workflow is active and available:

```text
specify workflow status <run_id>
```

and resume it when appropriate.

Spec Kit is optional infrastructure.

`.project/STATE.yaml + Git` are the authoritative recovery mechanism.

Never make the project dependent on an external AI-memory tool.

---

# 4. SOURCE-OF-TRUTH PRIORITY

Use this priority order:

```text
1. User instructions
2. AGENTS.md
3. Project Constitution
4. Active specification
5. ADRs
6. Tested code
7. .project/STATE.yaml
8. Serena durable memories
9. Conversation history
```

Conversation history is never the primary project database.

---

# 5. PRODUCT MISSION

Build a lightweight Linux web application named **Egress Manager**.

The system manages traffic routing and forwarding on VPN/proxy gateway servers.

It does NOT replace Marzban, 3x-ui, Eylan Panel, or other user-management panels.

Its responsibility is:

```text
Traffic Source
      ↓
Routing / Forwarding Policy
      ↓
Selected Egress
```

The system must provide four main engines.

### NAT / Firewall Engine

For:

```text
Port → Remote IP
```

Using nftables primarily, with iptables compatibility where appropriate.

### HAProxy Engine

For:

```text
TCP Listener → Backend / Backend Pool
```

Including:

- health checking;
- failover;
- load balancing;
- traffic statistics.

### Proxy Egress Engine

Using sing-box for:

```text
Interface / Subnet → Proxy Outbound
```

### Native Xray Integration

For compatible Xray installations:

```text
Xray inboundTag → Xray outboundTag
```

without unnecessary double proxying.

---

# 6. V1 SCOPE

V1 must include:

## Web Panel

- random available listen port selected during installation;
- configurable listen address/port;
- secure authentication;
- modern responsive dashboard;
- dark-first design;
- no separate HAProxy statistics port;
- no unnecessary management ports.

## NAT / Port Forward

Support:

- single port;
- multiple ports;
- port ranges;
- TCP;
- UDP;
- TCP + UDP;
- same-port forwarding;
- port remapping;
- source CIDR restrictions;
- enable/disable;
- clone;
- counters;
- all-ports-except mode;
- protected management ports.

## HAProxy

Support:

- TCP frontend;
- single backend;
- backend pools;
- round robin;
- least connections;
- weighted servers;
- primary/backup failover;
- TCP health checks;
- runtime statistics;
- enable/disable;
- graceful configuration reload;
- Unix runtime socket.

Do not advertise generic UDP support through HAProxy unless the installed HAProxy version and integration tests explicitly support the requested behavior.

Use NAT for generic UDP forwarding.

## sing-box

Initial supported outbound types:

- VLESS;
- Trojan;
- Shadowsocks;
- VMess where supported;
- Hysteria2;
- TUIC where supported;
- SOCKS5;
- WireGuard.

Support URI/config import where practical.

## Routing Sources

Support:

- Linux interface;
- source subnet;
- listener/port where technically appropriate;
- Xray inbound through the Xray adapter.

## VPN Integration

Support detection of:

- OpenVPN interfaces;
- WireGuard interfaces;
- common TUN interfaces;
- Xray;
- Marzban;
- 3x-ui.

Later in V1 development also support OpenVPN/WireGuard configurations as interface-based outbound implementations.

---

# 7. EXPLICIT NON-GOALS

Do NOT add these to V1:

- VPN user management;
- reseller management;
- subscription billing;
- quota systems;
- multi-node central controller;
- arbitrary shell terminal in the browser;
- Docker orchestration platform;
- general-purpose Linux administration panel;
- complete raw nftables editor;
- complete raw iptables editor.

The firewall UI manages Egress Manager-owned rules.

It must not become Webmin.

---

# 8. CORE SECURITY ARCHITECTURE

Use privilege separation.

Recommended processes:

```text
egress-web
    │
    │ authenticated typed IPC
    ▼
Unix control socket
    │
    ▼
egressd
```

`egress-web`:

- runs unprivileged;
- serves API and frontend;
- listens on the random panel TCP port;
- never executes arbitrary root shell commands.

`egressd`:

- performs privileged network operations;
- owns firewall/routing mutations;
- exposes only typed internal operations.

Do not run the entire web application as root.

---

# 9. RECOMMENDED TECHNICAL STACK

Backend:

```text
Go
SQLite
systemd
journald
```

Prefer Go standard library where reasonable.

Avoid unnecessary dependencies.

Frontend:

```text
React
TypeScript
Vite
Tailwind CSS
shadcn/ui
Motion
Apache ECharts
```

Testing:

```text
Go test
Go race detector
golangci-lint
Playwright
Linux network namespaces
Trivy
```

Networking:

```text
nftables
iptables compatibility adapter
iproute2
sing-box
Xray
HAProxy
WireGuard
OpenVPN
```

Package manager:

```text
pnpm
```

Commit lockfiles.

Pin tool/runtime versions used by CI.

Do not silently upgrade dependencies while implementing unrelated features.

---

# 10. PROJECT AI / AGENT INFRASTRUCTURE

Create:

```text
AGENTS.md
.project/
agent-skills/
specs/
docs/
```

Recommended external helpers:

- GitHub Spec Kit;
- Serena;
- Repomix;
- selected Superpowers workflows;
- UI/UX Pro Max.

External helper failure must never block development.

Fallbacks must exist.

---

# 11. AGENTS.md RULES

Keep root `AGENTS.md` small.

Target:

```text
< 6 KB
```

It must contain only durable rules.

Include rules equivalent to:

```text
Read .project/STATE.yaml before substantial work.

Specs are authoritative.

Never read the entire repository when targeted retrieval is sufficient.

Use Serena symbolic tools when available.

Use rg/git diff/targeted reads when Serena is unavailable.

Never flush firewall rules not owned by this project.

Never run nft flush ruleset.

Never use iptables -F as a normal operation.

Never modify networking without plan, validation, snapshot and rollback.

Protect SSH and the Egress Manager panel from self-lockout.

Every bug fix requires a regression test when practical.

Never disable tests or linters merely to obtain green CI.

Before claiming completion, run the verification gate.

After each green atomic milestone, update .project/STATE.yaml and commit.

User instruction START means execute the roadmap.

User instruction CONTINUE means recover from STATE.yaml and resume, not re-plan.
```

Nested `AGENTS.md` files may be added only when a directory genuinely needs additional specialized rules.

Avoid instruction duplication.

---

# 12. CUSTOM PROJECT SKILLS

Create small modular Agent Skills rather than one huge skill.

Recommended:

```text
architecture-guardian
session-checkpoint
context-budget
network-safety
systematic-debugging
upstream-research
ui-dashboard
verification-gate
```

Each skill should have a focused `SKILL.md`.

Do not duplicate the same instruction in every skill.

---

# 13. TOKEN / CONTEXT BUDGET POLICY

This project must actively minimize unnecessary context consumption.

At the beginning of a normal session read only:

```text
AGENTS.md
.project/STATE.yaml
active spec
git status
relevant diff
```

Then retrieve additional context only when needed.

Rules:

- prefer symbol lookup over whole-file reads;
- prefer `git diff` over rereading modified files;
- prefer `rg` before opening directories;
- do not repeatedly inspect unchanged code;
- do not repeatedly read completed specifications;
- do not load frontend context for backend-only work;
- do not load networking documentation for CSS-only work;
- use Serena symbolic retrieval when available;
- use Repomix `--compress` only for architecture-level analysis;
- never generate a full repository dump as routine context;
- keep STATE.yaml terse;
- keep durable memories terse;
- never store giant build logs in memory;
- record only error summaries and log paths;
- after discovering a durable fact, record it once rather than re-researching it every session.

If Serena is unavailable:

```text
rg
git grep
git diff
language server
targeted file reads
```

are sufficient.

Do not spend large amounts of time repairing optional AI tooling.

---

# 14. STATE FILE

Create:

```text
.project/STATE.yaml
```

Suggested structure:

```yaml
schema_version: 1

project: egress-manager

phase: 0
phase_name: bootstrap

active_spec: specs/000-bootstrap/spec.md
active_task: repository-bootstrap
status: ready

last_green_ref: HEAD

next_action: initialize repository structure

dirty_work_expected: false

tests:
  last_run: null
  result: unknown
  commands: []

network_transaction:
  status: none
  id: null

speckit:
  run_id: null

blockers: []

recent_decisions: []

touched_areas: []
```

Keep it compact.

Do not turn it into a diary.

Long-lived decisions belong in ADRs.

---

# 15. INTERRUPTION RECOVERY

Whenever beginning a meaningful subtask:

- ensure STATE.yaml describes the task;
- make changes in small atomic increments;
- test;
- checkpoint.

Whenever a subtask becomes green:

```text
Update STATE.yaml
Commit code + tests + state together
```

A committed `HEAD` represents the last known green checkpoint.

If interrupted with uncommitted changes:

1. Never discard them immediately.
2. Inspect `git diff`.
3. Determine whether they correspond to `active_task`.
4. Attempt to finish or repair the partial work.
5. If recovery requires reverting, first preserve the patch under:

```text
.project/recovery/
```

6. Never use destructive reset as the first recovery action.

---

# 16. DEVELOPMENT BUG POLICY

Use systematic debugging.

For every failure:

```text
1. Reproduce
2. Read the actual error
3. Determine the failing layer
4. Inspect relevant recent diff
5. Form one hypothesis
6. Test the hypothesis
7. Fix root cause
8. Add regression test
9. Run surrounding test suite
```

Never perform multiple unrelated speculative fixes simultaneously.

Never suppress failures with:

```text
|| true
```

in critical paths.

Never comment out tests to finish a phase.

Never loosen security controls merely to make an integration test pass.

After repeated failed fixes, stop patching symptoms and reconsider the architecture.

---

# 17. NETWORK SAFETY RULES

These are absolute.

Never execute as a normal feature operation:

```text
iptables -F
iptables -t nat -F
nft flush ruleset
```

Never reset unrelated UFW, Docker, Kubernetes, Marzban, WireGuard, Xray or user firewall state.

All managed firewall objects must be namespaced/tagged as Egress Manager-owned.

Examples:

```text
EGM_INPUT
EGM_FORWARD
EGM_PREROUTING
EGM_POSTROUTING
```

or equivalent nftables tables/chains.

Every mutation must implement:

```text
Inspect
→ Plan
→ Snapshot
→ Validate
→ Protect management paths
→ Apply
→ Verify
→ Commit
```

On failure:

```text
Rollback
```

---

# 18. RUNTIME TRANSACTION JOURNAL

Power loss can occur while Egress Manager itself is modifying networking.

Therefore implement a durable operation journal.

Before a network mutation persist:

```text
operation ID
requested change
previous state/snapshot
generated candidate configuration
phase
timestamp
```

Operation states:

```text
PREPARED
VALIDATED
APPLYING
VERIFYING
COMMITTED
ROLLING_BACK
ROLLED_BACK
FAILED
```

On daemon startup:

```text
if unfinished operation exists:
    inspect actual system state
    reconcile safely
    commit if verified
    otherwise rollback
```

Never assume that a process completed just because it started before the crash.

---

# 19. CONFIG VALIDATION

Before applying configuration use native validators.

Examples:

HAProxy:

```text
haproxy -c
```

sing-box:

```text
sing-box check
```

nftables:

```text
nft -c
```

Use appropriate validation for Xray/OpenVPN/WireGuard configurations.

Generate candidate configuration into temporary files.

Validate before replacing the active configuration.

Use atomic file replacement when practical.

---

# 20. REPOSITORY STRUCTURE

Target structure:

```text
egress-manager/
├── AGENTS.md
├── README.md
├── LICENSE
├── SECURITY.md
├── CHANGELOG.md
│
├── .project/
│   ├── STATE.yaml
│   ├── KNOWN_ISSUES.md
│   ├── recovery/
│   └── logs/
│
├── agent-skills/
│   ├── architecture-guardian/
│   ├── session-checkpoint/
│   ├── context-budget/
│   ├── network-safety/
│   ├── systematic-debugging/
│   ├── upstream-research/
│   ├── ui-dashboard/
│   └── verification-gate/
│
├── specs/
│
├── docs/
│   ├── PROJECT_ROADMAP.md
│   ├── architecture/
│   ├── adr/
│   ├── networking/
│   ├── recovery/
│   ├── testing/
│   └── ui/
│
├── cmd/
│   ├── egressd/
│   └── egress-web/
│
├── internal/
│   ├── api/
│   ├── auth/
│   ├── config/
│   ├── database/
│   ├── firewall/
│   ├── haproxy/
│   ├── outbound/
│   ├── routing/
│   ├── xray/
│   ├── system/
│   └── transaction/
│
├── web/
│
├── migrations/
│
├── tests/
│   ├── integration/
│   ├── network/
│   └── e2e/
│
└── scripts/
    ├── dev/
    ├── install/
    └── test/
```

Adjust only when a clear architectural reason exists.

Record substantial changes in an ADR.

---

# 21. PHASE 0 — BOOTSTRAP

Create:

- Git repository baseline;
- AGENTS.md;
- STATE.yaml;
- project Constitution;
- initial specifications;
- ADR mechanism;
- project skills;
- CI skeleton;
- Go workspace/module;
- frontend workspace;
- lockfiles;
- linting;
- formatting;
- unit-test commands;
- development scripts.

Configure Serena if available.

Configure Spec Kit if available.

Configure Repomix if useful.

Do not block the project if they cannot be installed.

### Exit gate

Must pass:

```text
go test ./...
go vet ./...
frontend typecheck
frontend lint
frontend build
```

Commit green baseline.

---

# 22. PHASE 1 — ARCHITECTURE & NETWORK TEST LAB

Before implementing destructive networking functionality, build a safe test environment.

Use Linux:

```text
network namespaces
veth pairs
local TCP echo server
local UDP echo server
dummy interfaces
```

Create integration helpers capable of validating:

- DNAT;
- SNAT/MASQUERADE;
- TCP forwarding;
- UDP forwarding;
- source CIDR restrictions;
- route changes;
- counters;
- rollback.

Tests must not alter the developer's real firewall where avoidable.

Implement core typed models:

```text
Route
Outbound
PortForward
FirewallRule
HAProxyFrontend
HAProxyBackend
Transaction
HealthStatus
```

### Exit gate

Network test lab must execute repeatably.

---

# 23. PHASE 2 — BACKEND FOUNDATION

Implement:

- configuration loader;
- SQLite;
- embedded schema migrations;
- repository/storage layer;
- structured logging;
- operation IDs;
- API errors;
- health endpoint;
- privileged agent IPC;
- authentication;
- secure session management;
- CSRF protection where applicable;
- login throttling;
- secret redaction.

Panel configuration must include:

```text
listen_address
listen_port
```

Installer selects a random free port.

Before choosing it:

- check TCP availability;
- exclude SSH;
- exclude known active service ports;
- record selection persistently.

### Exit gate

Backend/API tests green.

Privilege boundary tested.

---

# 24. PHASE 3 — FRONTEND DESIGN SYSTEM

Implement the design system before building every page independently.

Use:

```text
React
TypeScript
Tailwind
shadcn/ui
Motion
ECharts
```

Create:

- dark-first theme;
- typography;
- spacing tokens;
- status colors;
- cards;
- forms;
- dialogs;
- tables;
- badges;
- command palette if useful;
- toasts;
- skeleton states;
- responsive navigation;
- error states;
- empty states;
- loading states.

Animation principles:

- subtle;
- fast;
- functional;
- never obstruct operations.

Respect:

```text
prefers-reduced-motion
```

Main navigation:

```text
Dashboard
Outbounds
Routes
Port Forward
HAProxy
Firewall
Network
Logs
Settings
```

### Exit gate

Run Playwright visual/smoke tests for core layout.

No product networking mutation yet.

---

# 25. PHASE 4 — READ-ONLY NETWORK INVENTORY

Implement safe inspection first.

Detect:

- interfaces;
- addresses;
- routes;
- listening TCP ports;
- listening UDP ports;
- default gateway;
- DNS state;
- nftables availability;
- iptables backend;
- HAProxy;
- sing-box;
- Xray;
- OpenVPN;
- WireGuard;
- Marzban indicators;
- 3x-ui indicators.

Display:

```text
process
port
protocol
interface
subnet
service
```

Implement conflict detection.

### Exit gate

Inventory must be non-destructive and covered by tests.

---

# 26. PHASE 5 — NAT / PORT FORWARD ENGINE

Prefer nftables as the native internal engine.

Implement an adapter for compatible iptables environments.

Support:

```text
single port
multi-port
range
TCP
UDP
TCP+UDP
port remap
source CIDR
all ports except
enable
disable
clone
delete
traffic counters
```

Always protect:

```text
SSH
panel port
loopback
required local management routes
```

Never modify foreign firewall rules.

Create a dry-run preview showing generated changes before internal apply.

### Required failure tests

- remote unreachable;
- duplicate rule;
- conflicting port;
- invalid CIDR;
- invalid port range;
- SSH port included in all-ports forwarding;
- panel port included in all-ports forwarding;
- service restart;
- simulated interrupted operation;
- rollback.

### Exit gate

TCP and UDP namespace integration tests pass.

Foreign firewall-preservation test passes.

---

# 27. PHASE 6 — HAPROXY ENGINE

Implement HAProxy as an independent TCP proxy/load-balancer module.

Support:

- TCP frontend;
- backend;
- backend pool;
- health checks;
- roundrobin;
- leastconn;
- weights;
- backup servers;
- enable/disable;
- connection statistics;
- traffic statistics.

Use HAProxy Runtime API over Unix socket.

Do not open a separate stats web port.

Workflow:

```text
Generate candidate
→ haproxy config validation
→ atomic configuration update
→ graceful reload
→ verify
→ commit
```

UI should present outcomes, not raw HAProxy syntax by default.

Advanced raw details may be view-only.

### Exit gate

Integration tests prove:

- single backend;
- multi-backend distribution;
- backend failure;
- failover;
- backend recovery;
- config rejection;
- graceful reload.

---

# 28. PHASE 7 — SING-BOX OUTBOUND ENGINE

Create a generic Outbound abstraction.

An outbound is not synonymous with sing-box.

Implement sing-box adapter first.

Support parsing/import for initial protocols.

For each outbound maintain:

```text
name
type
server
capabilities
health
latency
external_ip
TCP support
UDP support
secret metadata
```

Never expose secrets unnecessarily in logs or API output.

Implement:

```text
Test
Save
Enable
Disable
Delete
Clone
```

Health check should distinguish:

```text
configuration valid
transport reachable
internet reachable
external IP
TCP status
UDP status when testable
latency
```

### Exit gate

Every supported outbound has fixture-based parser tests and integration coverage where feasible.

---

# 29. PHASE 8 — INTERFACE/SUBNET EGRESS ROUTING

Implement:

```text
Interface → Outbound
Subnet → Outbound
```

Example:

```text
tun0 / 10.8.0.0/24
        ↓
VLESS-DE
```

Include:

- TUN routing;
- policy routing where required;
- loop prevention;
- outbound server bypass;
- DNS policy;
- IPv4 policy;
- explicit IPv6 policy;
- kill switch;
- MTU/MSS handling where necessary.

Default failure policy for VPN client egress:

```text
BLOCK
```

not silent direct fallback.

Offer explicit user-configurable behavior later:

```text
Block
Failover
Direct
```

but Direct must never be implicit.

### Leak tests

When selected outbound is intentionally killed:

- client traffic must not silently use Iran direct egress;
- DNS must not leak when configured to follow outbound;
- IPv6 must not bypass a blocked IPv4 route.

Use packet capture/testing inside the isolated network lab.

### Exit gate

No-leak tests pass.

---

# 30. PHASE 9 — XRAY / MARZBAN / 3X-UI ADAPTER

Detect compatible Xray installations.

Do not assume ownership of arbitrary external Xray configurations.

Implement adapter boundaries.

Where safe:

```text
inboundTag
→ outboundTag
```

using native Xray routing.

Avoid:

```text
Xray inbound
→ transparent interception
→ sing-box
```

when native Xray routing is sufficient.

Never overwrite external panel-managed configuration blindly.

Implement:

- discovery;
- validation;
- managed fragment strategy;
- backup;
- change detection;
- rollback;
- external modification detection.

### Exit gate

Test against representative Xray configurations.

Document integration limitations explicitly.

---

# 31. PHASE 10 — INTERFACE OUTBOUNDS

Extend generic Outbound abstraction.

Support:

## Kernel WireGuard

```text
WireGuard config
→ managed interface
→ InterfaceOutbound
```

## OpenVPN Client

```text
.ovpn
→ managed systemd client
→ tun interface
→ InterfaceOutbound
```

Do not force these through sing-box when native interface routing is the better solution.

Implement:

- import;
- secret protection;
- lifecycle;
- health;
- route binding;
- cleanup;
- reboot persistence.

### Exit gate

OpenVPN and WireGuard test fixtures and disposable-host integration tests pass.

---

# 32. PHASE 11 — RELIABILITY & RECOVERY

Implement:

- configuration snapshots;
- restore;
- runtime journal;
- daemon crash recovery;
- restart recovery;
- reboot persistence;
- idempotent apply;
- operation locking;
- stale-lock recovery;
- dependency health monitoring.

Add CLI emergency recovery:

```text
egressctl status
egressctl recover
egressctl bypass
egressctl rollback
```

`bypass` must disable only Egress Manager-managed interception/routing.

It must never flush unrelated firewall configuration.

### Exit gate

Perform failure injection:

- kill egressd while applying;
- kill HAProxy;
- kill sing-box;
- corrupt candidate config;
- disconnect outbound;
- simulate DNS failure;
- restart services;
- reboot disposable VM.

System must recover predictably.

---

# 33. PHASE 12 — DASHBOARD & OPERATIONS

Dashboard should display real useful information.

Include:

```text
Active routes
Healthy outbounds
Failed outbounds
HAProxy backend health
Traffic counters
Latency
External egress IP
Recent failures
System warnings
```

Pages:

```text
Dashboard
Outbounds
Routes
Port Forward
HAProxy
Firewall
Network
Logs
Backups
Settings
```

Add:

- search;
- filters;
- status badges;
- confirmation for destructive actions;
- accessible keyboard navigation;
- reduced-motion support;
- mobile/tablet responsiveness.

Avoid excessive decorative animation.

---

# 34. FINAL SECURITY REVIEW

Run:

```text
Trivy filesystem scan
Trivy secret scan
dependency audit
Go race detector
golangci-lint
frontend lint
frontend typecheck
```

Review manually for:

- command injection;
- shell injection;
- path traversal;
- insecure file permissions;
- leaked passwords/private keys;
- unsafe config logging;
- unauthenticated control endpoints;
- CSRF;
- session fixation;
- brute-force login;
- privilege escalation;
- unsafe Unix socket permissions.

---

# 35. FINAL TEST MATRIX

Before declaring the project finished, perform the complete matrix.

## Backend

```text
unit tests
integration tests
race detector
lint
vet
database migration tests
API contract tests
```

## Frontend

```text
typecheck
lint
production build
component smoke tests
```

## Browser E2E

Use Playwright.

Test at least:

```text
Chromium
Firefox
WebKit
```

for release validation where environment permits.

Scenarios:

```text
login
logout
failed login
create outbound
test outbound
edit outbound
delete outbound
create NAT rule
disable NAT rule
enable NAT rule
delete NAT rule
create HAProxy frontend
backend failure
backend recovery
create interface route
outbound failure
kill-switch behavior
backup
restore
settings
panel port validation
```

---

# 36. NETWORK ACCEPTANCE MATRIX

Must verify:

```text
TCP DNAT                  PASS
UDP DNAT                  PASS
Multi-port                PASS
Port range                PASS
Port remapping            PASS
Source CIDR               PASS
All-ports-except          PASS
SSH protection            PASS
Panel protection          PASS
Foreign firewall preserve PASS

HAProxy TCP               PASS
HAProxy health check      PASS
HAProxy failover          PASS
HAProxy load balancing    PASS

Outbound health           PASS
Interface → outbound      PASS
Subnet → outbound         PASS
DNS policy                PASS
IPv6 policy               PASS
Kill switch               PASS
No direct leak            PASS

Rollback                  PASS
Crash recovery            PASS
Reboot persistence        PASS
Clean uninstall           PASS
```

---

# 37. PERFORMANCE BASELINE

Record, do not guess:

- idle CPU;
- idle RSS;
- API response latency;
- dashboard load time;
- NAT throughput;
- NAT UDP throughput;
- HAProxy TCP throughput;
- proxy egress throughput where testable.

Use tools such as `iperf3` in the test lab.

Store baseline results.

Future regressions should be compared against measured baseline.

Do not invent performance claims.

---

# 38. INSTALLATION

Provide a predictable installation path for supported Ubuntu LTS systems.

Installer must:

- perform preflight checks;
- detect required kernel/network features;
- detect conflicts;
- install or validate dependencies;
- select safe random web port;
- create users/groups;
- create secure directories;
- install systemd services;
- initialize DB;
- start services;
- verify health.

Do not destroy existing networking configuration during installation.

---

# 39. UNINSTALLATION

Uninstall must:

- stop Egress Manager services;
- remove Egress Manager-owned firewall objects;
- remove Egress Manager-managed routes;
- stop Egress Manager-managed HAProxy/sing-box resources;
- leave unrelated system configuration untouched.

Offer explicit choice whether to retain:

```text
database
backups
logs
configuration
```

---

# 40. DEFINITION OF DONE

The project is NOT done because:

```text
the code compiles
```

The project is done only when:

- required V1 features are implemented;
- unit tests pass;
- integration tests pass;
- network namespace tests pass;
- Playwright E2E passes;
- release build succeeds;
- security scans are reviewed;
- rollback has been tested;
- crash recovery has been tested;
- reboot persistence has been tested;
- leak protection has been tested;
- foreign firewall preservation has been tested;
- install has been tested;
- uninstall has been tested;
- documentation is complete;
- no unresolved Critical/High defect remains.

---

# 41. FINAL DELIVERABLE

Create:

```text
.project/FINAL_REPORT.md
```

It must contain:

```text
Implemented features
Architecture summary
Supported environments
External dependencies
Test commands
Test results
Security scan results
Failure-injection results
Performance baseline
Known limitations
Deferred V2 items
Installation verification
Uninstallation verification
Final git commit
```

Also ensure:

```text
git status
```

is clean.

---

# 42. FINAL CODEX BEHAVIOR

Never say:

```text
Done
```

based only on visual inspection.

Never say:

```text
Should work
```

instead of testing.

Use evidence.

If a test cannot be executed because of environment limitations, state exactly which test was not run and why in `FINAL_REPORT.md`.

Do not silently mark it successful.

---

# 43. V2 BACKLOG — DO NOT IMPLEMENT BEFORE V1 IS GREEN

Possible V2 items:

```text
Multi-node controller
Outbound groups
Advanced automatic failover
Lowest-latency selection
Subscription synchronization
Docker/cgroup routing
UID/process routing
Advanced domain routing
Scheduling
Notifications
Prometheus metrics
Role-based users
API tokens
Cluster management
```

Do not allow V2 scope to delay V1 completion.

---

# 44. START RULE

When the user says:

```text
START
```

begin at Phase 0.

Do not stop after creating the plan.

Do not stop after scaffolding.

Continue implementing, testing, checkpointing and progressing through phases.

---

# 45. CONTINUE RULE

Whenever the user says:

```text
CONTINUE
```

the first goal is recovery, not conversation.

Read the persisted state.

Inspect the current diff.

Recover incomplete work.

Run the smallest relevant verification.

Resume `next_action`.

Continue until another unavoidable external interruption occurs or the complete Definition of Done is satisfied.

The user must never need to explain the project again merely because a development session was interrupted.