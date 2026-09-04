# Egress Manager
## نقشه راه اصلی اجرای پروژه توسط Codex

**نقش این سند:** این سند مرجع اصلی پیاده‌سازی پروژه Egress Manager است.

**مدل اجرا:** اجرای طولانی‌مدت، مرحله‌ای، دارای Checkpoint و قابل ادامه پس از هر نوع قطعی.

**فرمان‌های اصلی کاربر:**

```text
START
CONTINUE
```

---

# ۱. قرارداد اجرای پروژه

وقتی این سند برای اولین بار در اختیار Codex قرار می‌گیرد:

1. آن را یک بار کامل بخوان.
2. آن را داخل Repository در مسیر زیر ذخیره کن:

```text
docs/PROJECT_ROADMAP.md
```

3. تا زمانی که کاربر صراحتاً نگوید:

```text
START
```

پیاده‌سازی Featureهای اصلی پروژه را شروع نکن.

4. تصمیم‌هایی را که در این سند مشخص شده‌اند دوباره از کاربر نپرس.

---

# ۲. معنی START

وقتی کاربر گفت:

```text
START
```

پروژه را شروع کن.

از این لحظه:

- مراحل Roadmap را به ترتیب اجرا کن؛
- بدون گرفتن تأیید برای تصمیم‌های معمولی از Phaseای به Phase بعد برو؛
- تصمیم‌های فنی منطقی و سازگار با این Roadmap را خودت بگیر؛
- مرتب Checkpoint ایجاد کن؛
- دائماً تست انجام بده؛
- باگ‌های کشف‌شده را قبل از ادامه برطرف کن؛
- صرفاً برای گزارش پیشرفت اجرای پروژه را متوقف نکن؛
- تا جایی که Session و محیط Codex اجازه می‌دهد اجرای پروژه را ادامه بده.

فقط در شرایط واقعی زیر توقف مجاز است:

- نیاز به Permission سیستم‌عامل باشد؛
- Credential یا Secret خارجی لازم باشد؛
- عملیات غیرقابل‌بازگشت روی اطلاعات متعلق به کاربر لازم باشد؛
- محیط اجرای Codex واقعاً امکان ادامه ندهد؛
- Requirement حیاتی غیرممکن باشد و جایگزین امنی وجود نداشته باشد.

اگر اجرای پروژه به دلیل موارد زیر قطع شد:

```text
قطع اینترنت
قطع برق
هنگ VS Code
بسته شدن Codex
Restart سیستم
Crash ترمینال
اتمام Session
مشکل Tool
```

پروژه باید کاملاً قابل Resume باشد.

---

# ۳. معنی CONTINUE

اگر کاربر بعداً فقط گفت:

```text
CONTINUE
```

از اول برنامه‌ریزی نکن.

کل Repository را دوباره نخوان.

Onboarding پروژه را تکرار نکن مگر اینکه State پروژه خراب یا حذف شده باشد.

این ترتیب Recovery را اجرا کن:

```text
1. AGENTS.md را بخوان
2. .project/STATE.yaml را بخوان
3. git status --short --branch
4. HEAD فعلی را بررسی کن
5. فقط git diff فعلی را بررسی کن
6. Spec فعال ثبت‌شده در STATE.yaml را بخوان
7. فقط Documentation/Memory مرتبط با Task فعلی را بخوان
8. Transaction شبکه نیمه‌تمام را بررسی کن
9. آخرین وضعیت تست را بررسی کن
10. دقیقاً next_action را ادامه بده
```

اگر Spec Kit نصب است و Workflow فعالی ثبت شده:

```text
specify workflow status <run_id>
```

را بررسی و در صورت نیاز Resume کن.

Spec Kit یک ابزار کمکی است.

مرجع اصلی Resume همیشه:

```text
Git
+
.project/STATE.yaml
```

است.

پروژه نباید به حافظه یک ابزار AI خارجی وابسته باشد.

---

# ۴. ترتیب اعتبار اطلاعات

ترتیب مرجع‌ها:

```text
1. دستور مستقیم کاربر
2. AGENTS.md
3. Constitution پروژه
4. Specification فعال
5. ADRها
6. کد تست‌شده
7. .project/STATE.yaml
8. Serena Memories
9. تاریخچه Chat
```

تاریخچه چت دیتابیس پروژه نیست.

---

# ۵. هدف محصول

یک Web Application سبک برای Linux با نام:

```text
Egress Manager
```

بساز.

هدف آن مدیریت Routing و Forwarding روی سرورهای VPN/Proxy است.

این پروژه نباید جایگزین:

```text
Marzban
3x-ui
Eylan Panel
```

یا پنل‌های مدیریت User شود.

وظیفه پروژه:

```text
Traffic Source
      ↓
Routing / Forwarding Policy
      ↓
Selected Egress
```

است.

چهار Engine اصلی خواهیم داشت.

### NAT / Firewall Engine

برای:

```text
Port → Remote IP
```

ترجیحاً با nftables و در صورت نیاز compatibility با iptables.

### HAProxy Engine

برای:

```text
TCP Listener → Backend / Backend Pool
```

شامل:

- Health Check؛
- Failover؛
- Load Balancing؛
- Traffic Statistics.

### Proxy Egress Engine

با sing-box برای:

```text
Interface / Subnet → Proxy Outbound
```

### Xray Native Integration

برای نصب‌های سازگار:

```text
Xray inboundTag → Xray outboundTag
```

بدون Proxy دوباره و غیرضروری.

---

# ۶. Scope نسخه V1

## Web Panel

باید داشته باشد:

- انتخاب یک پورت آزاد تصادفی هنگام نصب؛
- امکان تغییر Listen Address/Port؛
- Authentication امن؛
- Dashboard مدرن و Responsive؛
- طراحی Dark-first؛
- بدون HAProxy Stats Port اضافه؛
- بدون Management Port غیرضروری.

---

## NAT / Port Forward

پشتیبانی از:

```text
Single Port
Multiple Ports
Port Range
TCP
UDP
TCP + UDP
Same Port Forwarding
Port Remapping
Source CIDR
Enable / Disable
Clone
Counters
All Ports Except
Protected Management Ports
```

---

## HAProxy

پشتیبانی از:

```text
TCP Frontend
Single Backend
Backend Pool
Round Robin
Least Connections
Weighted Servers
Primary / Backup
Health Check
Runtime Statistics
Enable / Disable
Graceful Reload
Unix Runtime Socket
```

UDP عمومی را از طریق HAProxy تبلیغ نکن مگر اینکه Version نصب‌شده و Integration Testها صریحاً رفتار موردنیاز را تأیید کنند.

برای Forward عمومی UDP از NAT استفاده کن.

---

## sing-box

Outboundهای اولیه:

```text
VLESS
Trojan
Shadowsocks
VMess در صورت پشتیبانی
Hysteria2
TUIC در صورت پشتیبانی
SOCKS5
WireGuard
```

Import کانفیگ و URI در صورت امکان انجام شود.

---

## Sourceهای Routing

پشتیبانی از:

```text
Linux Interface
Source Subnet
Listener / Port در موارد فنی مناسب
Xray Inbound از طریق Adapter
```

---

## VPN Integration

تشخیص:

```text
OpenVPN
WireGuard
TUN interfaces
Xray
Marzban
3x-ui
```

در ادامه توسعه V1 امکان استفاده از کانفیگ OpenVPN/WireGuard به‌عنوان Interface Outbound نیز اضافه شود.

---

# ۷. مواردی که در V1 نباید اضافه شوند

در V1 اضافه نکن:

```text
VPN User Management
Reseller Management
Billing
Quota
Multi-node Controller
Browser Shell
Docker Management Platform
General Linux Admin Panel
Full Raw nftables Editor
Full Raw iptables Editor
```

Firewall UI فقط Ruleهایی را مدیریت کند که متعلق به Egress Manager هستند.

این پروژه نباید تبدیل به Webmin شود.

---

# ۸. معماری امنیتی

Privilege Separation اجباری است.

ساختار پیشنهادی:

```text
egress-web
    │
    │ Typed IPC
    ▼
Unix Control Socket
    │
    ▼
egressd
```

`egress-web`:

- بدون root اجرا شود؛
- API و Frontend را سرو کند؛
- روی Panel Port تصادفی Listen کند؛
- دستور Shell دلخواه با Root اجرا نکند.

`egressd`:

- عملیات Privileged شبکه را انجام دهد؛
- Firewall و Routing را مدیریت کند؛
- فقط Operationهای Typed و محدود قبول کند.

کل Web Application را Root اجرا نکن.

---

# ۹. Stack فنی پیشنهادی

Backend:

```text
Go
SQLite
systemd
journald
```

تا جای ممکن از Standard Library Go استفاده شود.

Dependency غیرضروری اضافه نکن.

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

Package Manager:

```text
pnpm
```

Lockfileها Commit شوند.

Version ابزارهای CI Pin شوند.

در حین یک Feature نامرتبط Dependencyها را بی‌دلیل Upgrade نکن.

---

# ۱۰. زیرساخت AI / Agent پروژه

ایجاد کن:

```text
AGENTS.md
.project/
agent-skills/
specs/
docs/
```

ابزارهای کمکی پیشنهادی:

```text
GitHub Spec Kit
Serena
Repomix
Superpowers منتخب
UI/UX Pro Max
```

خرابی یا نبود یک ابزار کمکی نباید پروژه را متوقف کند.

Fallback داشته باش.

---

# ۱۱. قوانین AGENTS.md

Root `AGENTS.md` را کوچک نگه دار.

هدف:

```text
کمتر از 6KB
```

فقط Ruleهای دائمی داخل آن باشند.

محتوای لازم:

```text
قبل از کار جدی STATE.yaml را بخوان.

Specها مرجع اصلی هستند.

اگر Retrieval هدفمند کافی است کل Repository را نخوان.

در صورت وجود Serena از Symbol Retrieval استفاده کن.

در صورت نبود Serena از rg/git diff/targeted reads استفاده کن.

هیچ Firewall Rule خارجی را Flush نکن.

nft flush ruleset ممنوع است.

iptables -F عملیات عادی پروژه نیست.

تغییر شبکه بدون Plan + Snapshot + Validate + Rollback ممنوع است.

SSH و Panel را از Self-lockout محافظت کن.

برای Bug Fix در صورت امکان Regression Test ایجاد کن.

برای سبز شدن CI تست یا Linter را Disable نکن.

قبل از اعلام Completion Verification Gate را اجرا کن.

بعد از هر Milestone سبز STATE.yaml را Update و Commit کن.

START یعنی اجرای Roadmap.

CONTINUE یعنی Resume از State، نه Plan مجدد.
```

Nested `AGENTS.md` فقط اگر یک Directory واقعاً Rule خاص نیاز داشت ایجاد شود.

Ruleها را Duplicate نکن.

---

# ۱۲. Skillهای اختصاصی

Skillهای کوچک و مستقل بساز:

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

هر Skill یک `SKILL.md` متمرکز داشته باشد.

یک Rule را در هشت Skill تکرار نکن.

---

# ۱۳. سیاست کاهش Token و Context

این پروژه باید فعالانه Context مصرفی را کم کند.

شروع Session معمولی فقط با این موارد:

```text
AGENTS.md
.project/STATE.yaml
Active Spec
git status
Relevant git diff
```

سپس فقط در صورت نیاز اطلاعات بیشتری بخوان.

قوانین:

- Symbol Lookup مقدم بر Full File Read؛
- `git diff` مقدم بر دوباره‌خوانی فایل تغییرکرده؛
- `rg` مقدم بر باز کردن Directory؛
- فایل بدون تغییر را دوباره نخوان؛
- Spec تمام‌شده را مرتب دوباره نخوان؛
- برای Backend Task فایل‌های UI را Load نکن؛
- برای CSS Task مستندات Routing را Load نکن؛
- در صورت وجود Serena از Symbol Retrieval استفاده کن؛
- Repomix `--compress` فقط برای تحلیل معماری؛
- Full Repository Dump برای کار روزمره ممنوع؛
- STATE.yaml کوتاه بماند؛
- Memoryها کوتاه بمانند؛
- Build Log بزرگ داخل Memory ذخیره نکن؛
- فقط Error Summary و مسیر Log ثبت شود؛
- Fact پایدار را یک بار در ADR/Memory ثبت کن تا هر Session دوباره Research نشود.

اگر Serena نبود:

```text
rg
git grep
git diff
Language Server
Targeted Reads
```

کافی است.

برای تعمیر Tool اختیاری مقدار زیادی زمان و Token هدر نده.

---

# ۱۴. فایل State

بساز:

```text
.project/STATE.yaml
```

ساختار:

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

آن را تبدیل به دفتر خاطرات نکن.

تصمیم‌های مهم و دائمی داخل ADR قرار بگیرند.

---

# ۱۵. Recovery بعد از قطعی

قبل از شروع هر Subtask مهم:

- STATE.yaml وضعیت Task را نشان دهد؛
- تغییرات کوچک و Atomic باشند؛
- تست انجام شود؛
- Checkpoint ایجاد شود.

بعد از سبز شدن یک Subtask:

```text
Update STATE.yaml
Commit Code + Tests + State
```

`HEAD` Commit‌شده آخرین Green Checkpoint است.

اگر کار با تغییرات Commit‌نشده قطع شد:

1. فوراً آن‌ها را حذف نکن.
2. `git diff` را بررسی کن.
3. مشخص کن متعلق به Active Task هستند یا نه.
4. ابتدا سعی کن Partial Work را کامل یا Repair کنی.
5. اگر Revert لازم بود، Patch را ابتدا در:

```text
.project/recovery/
```

ذخیره کن.
6. `git reset --hard` اولین اقدام Recovery نباشد.

---

# ۱۶. سیاست کنترل Bug

Systematic Debugging استفاده شود.

برای هر Failure:

```text
1. Reproduce
2. Error واقعی را کامل بخوان
3. Layer خراب را مشخص کن
4. Recent Diff مرتبط را بررسی کن
5. یک Hypothesis بساز
6. همان Hypothesis را تست کن
7. Root Cause را Fix کن
8. Regression Test اضافه کن
9. Test Suite مرتبط را اجرا کن
```

چند Fix حدسی و نامرتبط را همزمان اجرا نکن.

در مسیرهای حیاتی Failure را با:

```text
|| true
```

مخفی نکن.

برای تمام شدن Phase تست را Comment نکن.

برای پاس شدن تست Security را ضعیف نکن.

بعد از چند Fix ناموفق، Patch زدن به Symptom را متوقف و Architecture را دوباره بررسی کن.

---

# ۱۷. قوانین امنیت شبکه

این قوانین مطلق هستند.

به‌عنوان عملیات معمول پروژه اجرا نکن:

```text
iptables -F
iptables -t nat -F
nft flush ruleset
```

Ruleهای مربوط به:

```text
UFW
Docker
Kubernetes
Marzban
WireGuard
Xray
User
```

را Reset نکن.

تمام Ruleهای Managed باید Namespace/Tag مخصوص Egress Manager داشته باشند.

مثلاً:

```text
EGM_INPUT
EGM_FORWARD
EGM_PREROUTING
EGM_POSTROUTING
```

یا ساختار معادل nftables.

هر Mutation:

```text
Inspect
→ Plan
→ Snapshot
→ Validate
→ Protect Management
→ Apply
→ Verify
→ Commit
```

در صورت Failure:

```text
Rollback
```

---

# ۱۸. Runtime Transaction Journal

ممکن است برق هنگام تغییر Firewall توسط خود Egress Manager قطع شود.

بنابراین Operation Journal دائمی پیاده‌سازی کن.

قبل از Mutation ذخیره شود:

```text
Operation ID
Requested Change
Previous Snapshot
Candidate Config
Current Phase
Timestamp
```

Stateهای Operation:

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

هنگام Startup daemon:

```text
اگر Operation نیمه‌تمام وجود داشت:
    وضعیت واقعی سیستم را Inspect کن
    Reconcile امن انجام بده
    اگر Verify شد Commit
    در غیر این صورت Rollback
```

صرفاً به خاطر اینکه Process قبل از Crash شروع شده، فرض نکن تمام شده است.

---

# ۱۹. اعتبارسنجی Config

قبل از Apply از Validatorهای Native استفاده کن.

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

برای Xray/OpenVPN/WireGuard نیز Validation مناسب اجرا شود.

Candidate Config ابتدا در فایل موقت ساخته شود.

قبل از جایگزینی Active Config آن را Validate کن.

در صورت امکان از Atomic File Replacement استفاده کن.

---

# ۲۰. ساختار Repository

ساختار هدف:

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

فقط با دلیل معماری واضح تغییر بده.

تغییر مهم معماری باید ADR داشته باشد.

---

# ۲۱. Phase 0 — Bootstrap

ایجاد:

```text
Git baseline
AGENTS.md
STATE.yaml
Project Constitution
Initial Specs
ADR mechanism
Project Skills
CI skeleton
Go module
Frontend workspace
Lockfiles
Linting
Formatting
Unit test commands
Development scripts
```

در صورت وجود Serena را Config کن.

در صورت وجود Spec Kit را Config کن.

در صورت نیاز Repomix را Config کن.

عدم نصب آن‌ها پروژه را متوقف نکند.

### Exit Gate

باید پاس شوند:

```text
go test ./...
go vet ./...
frontend typecheck
frontend lint
frontend build
```

Green Baseline Commit شود.

---

# ۲۲. Phase 1 — Architecture & Network Test Lab

قبل از Network Mutation واقعی یک Lab امن ایجاد کن.

استفاده از:

```text
Linux network namespaces
veth pairs
local TCP echo server
local UDP echo server
dummy interfaces
```

Integration Helpers برای:

```text
DNAT
SNAT/MASQUERADE
TCP forwarding
UDP forwarding
Source CIDR
Routing
Counters
Rollback
```

تست‌ها تا حد ممکن Firewall واقعی Developer را تغییر ندهند.

Typed Modelهای اصلی:

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

### Exit Gate

Network Lab چند بار پشت‌سرهم Repeatable اجرا شود.

---

# ۲۳. Phase 2 — Backend Foundation

پیاده‌سازی:

```text
Configuration loader
SQLite
Embedded migrations
Storage layer
Structured logging
Operation IDs
API errors
Health endpoint
Privileged IPC
Authentication
Secure sessions
CSRF protection
Login throttling
Secret redaction
```

Panel Config:

```text
listen_address
listen_port
```

Installer پورت تصادفی آزاد انتخاب کند.

قبل از انتخاب:

- TCP availability؛
- SSH exclusion؛
- Active service exclusion؛
- Persistence.

### Exit Gate

Backend/API Testها سبز.

Privilege Boundary تست شود.

---

# ۲۴. Phase 3 — Frontend Design System

قبل از ساخت تمام Pageها Design System بساز.

استفاده از:

```text
React
TypeScript
Tailwind
shadcn/ui
Motion
ECharts
```

ایجاد:

```text
Dark-first theme
Typography
Spacing tokens
Status colors
Cards
Forms
Dialogs
Tables
Badges
Toasts
Skeletons
Responsive navigation
Error states
Empty states
Loading states
```

Animation:

```text
Subtle
Fast
Functional
Non-blocking
```

از:

```text
prefers-reduced-motion
```

پشتیبانی کن.

Navigation:

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

### Exit Gate

Playwright Smoke/Visual Test برای Layout اصلی.

هنوز Network Mutation محصول انجام نشود.

---

# ۲۵. Phase 4 — Read-only Network Inventory

اول Inspection امن را بساز.

تشخیص:

```text
Interfaces
IP addresses
Routes
TCP listeners
UDP listeners
Default gateway
DNS
nftables
iptables backend
HAProxy
sing-box
Xray
OpenVPN
WireGuard
Marzban
3x-ui
```

نمایش:

```text
Process
Port
Protocol
Interface
Subnet
Service
```

Conflict Detection پیاده شود.

### Exit Gate

Inventory کاملاً Non-destructive و تست‌شده باشد.

---

# ۲۶. Phase 5 — NAT / Port Forward

nftables Engine اصلی باشد.

برای سیستم‌های سازگار Adapter iptables داشته باش.

پشتیبانی از:

```text
Single Port
Multi Port
Range
TCP
UDP
TCP+UDP
Port Remap
Source CIDR
All Ports Except
Enable
Disable
Clone
Delete
Traffic Counters
```

همیشه محافظت کن:

```text
SSH
Panel Port
Loopback
Management Routes
```

Foreign Rule را تغییر نده.

قبل از Apply امکان Internal Dry-run/Preview وجود داشته باشد.

### Failure Testهای اجباری

```text
Remote unreachable
Duplicate rule
Port conflict
Invalid CIDR
Invalid range
SSH in all-ports rule
Panel port in all-ports rule
Service restart
Interrupted apply
Rollback
```

### Exit Gate

TCP و UDP در Namespace Lab پاس شوند.

Foreign Firewall Preservation پاس شود.

---

# ۲۷. Phase 6 — HAProxy Engine

HAProxy یک Module مستقل TCP Proxy/Load Balancer باشد.

پشتیبانی:

```text
TCP Frontend
Backend
Backend Pool
Health Check
Round Robin
Least Connections
Weight
Backup
Enable/Disable
Connection Stats
Traffic Stats
```

HAProxy Runtime API از طریق Unix Socket.

Stats Web Port جدید باز نکن.

Workflow:

```text
Generate Candidate
→ Validate
→ Atomic Update
→ Graceful Reload
→ Verify
→ Commit
```

UI به‌طور پیش‌فرض Outcome نمایش دهد، نه Syntax خام HAProxy.

### Exit Gate

تست:

```text
Single backend
Multi backend
Backend failure
Failover
Recovery
Invalid config
Graceful reload
```

---

# ۲۸. Phase 7 — sing-box Outbound Engine

یک Abstraction عمومی با نام:

```text
Outbound
```

بساز.

Outbound مساوی sing-box نیست.

اول Sing-box Adapter را بساز.

برای هر Outbound:

```text
Name
Type
Server
Capabilities
Health
Latency
External IP
TCP Support
UDP Support
Secret Metadata
```

Secretها در Log/API غیرضروری نمایش داده نشوند.

عملیات:

```text
Test
Save
Enable
Disable
Delete
Clone
```

Health Check تفاوت این‌ها را نشان دهد:

```text
Config valid
Transport reachable
Internet reachable
External IP
TCP
UDP when testable
Latency
```

### Exit Gate

Parser Fixture و Integration coverage مناسب برای Protocolهای Supported.

---

# ۲۹. Phase 8 — Interface/Subnet Egress Routing

پیاده‌سازی:

```text
Interface → Outbound
Subnet → Outbound
```

مثال:

```text
tun0 / 10.8.0.0/24
        ↓
VLESS-DE
```

شامل:

```text
TUN routing
Policy routing
Loop prevention
Outbound server bypass
DNS policy
IPv4 policy
Explicit IPv6 policy
Kill switch
MTU/MSS when needed
```

Default Failure Policy برای VPN Egress:

```text
BLOCK
```

باشد.

Direct Fallback به صورت Silent ممنوع.

بعداً می‌توان explicit داشت:

```text
Block
Failover
Direct
```

ولی Direct هرگز Default مخفی نباشد.

### Leak Test

Outbound عمداً Kill شود.

بررسی کن:

- Client مستقیم از ایران خارج نشود؛
- DNS Leak نداشته باشد؛
- IPv6 مسیر را دور نزند.

از Packet Capture داخل Lab استفاده شود.

### Exit Gate

No-Leak Tests پاس شوند.

---

# ۳۰. Phase 9 — Xray / Marzban / 3x-ui

Installationهای Xray سازگار را Detect کن.

مالکیت Config خارجی را فرض نکن.

Adapter Boundary واضح داشته باش.

در صورت امن بودن:

```text
inboundTag
→ outboundTag
```

از Native Xray Routing استفاده کن.

اگر Native Xray کافی است مسیر:

```text
Xray
→ Transparent interception
→ sing-box
```

نساز.

Config مدیریت‌شده توسط پنل خارجی را Blind Overwrite نکن.

پیاده‌سازی:

```text
Discovery
Validation
Managed fragments
Backup
External change detection
Rollback
```

### Exit Gate

با چند Config نماینده Xray تست شود.

Limitها مستند شوند.

---

# ۳۱. Phase 10 — Interface Outbounds

Outbound Abstraction را توسعه بده.

## Kernel WireGuard

```text
WireGuard config
→ Managed interface
→ InterfaceOutbound
```

## OpenVPN Client

```text
.ovpn
→ Managed systemd client
→ tun interface
→ InterfaceOutbound
```

اگر Native Interface راه مناسب‌تری است آن‌ها را به زور از sing-box عبور نده.

پیاده‌سازی:

```text
Import
Secrets
Lifecycle
Health
Route binding
Cleanup
Reboot persistence
```

### Exit Gate

Fixture و Disposable-host Integration Testهای OpenVPN/WireGuard پاس شوند.

---

# ۳۲. Phase 11 — Reliability & Recovery

پیاده‌سازی:

```text
Configuration snapshots
Restore
Runtime journal
Daemon crash recovery
Restart recovery
Reboot persistence
Idempotent apply
Operation locking
Stale lock recovery
Dependency health
```

CLI اضطراری:

```text
egressctl status
egressctl recover
egressctl bypass
egressctl rollback
```

`bypass` فقط Rule/Routeهای متعلق به Egress Manager را Disable کند.

هیچ Firewall خارجی Flush نشود.

### Exit Gate

Failure Injection:

```text
Kill egressd during apply
Kill HAProxy
Kill sing-box
Invalid candidate config
Outbound disconnect
DNS failure
Service restart
Disposable VM reboot
```

Recovery باید Predictable باشد.

---

# ۳۳. Phase 12 — Dashboard & Operations

Dashboard اطلاعات واقعی و مفید بدهد:

```text
Active Routes
Healthy Outbounds
Failed Outbounds
HAProxy Backend Health
Traffic Counters
Latency
External Egress IP
Recent Failures
System Warnings
```

صفحات:

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

اضافه شود:

```text
Search
Filters
Status Badges
Destructive confirmation
Keyboard accessibility
Reduced Motion
Mobile/Tablet responsive
```

Animation نمایشی بیش از حد ممنوع.

---

# ۳۴. Security Review نهایی

اجرا:

```text
Trivy filesystem scan
Trivy secret scan
Dependency audit
Go race detector
golangci-lint
Frontend lint
Frontend typecheck
```

Review دستی:

```text
Command injection
Shell injection
Path traversal
Unsafe permissions
Private key leakage
Password leakage
Unsafe config logs
Unauthenticated endpoints
CSRF
Session fixation
Brute-force login
Privilege escalation
Unix socket permissions
```

---

# ۳۵. Test Matrix نهایی

قبل از اعلام پایان پروژه همه اجرا شوند.

## Backend

```text
Unit tests
Integration tests
Race detector
Lint
Vet
Migration tests
API contract tests
```

## Frontend

```text
Typecheck
Lint
Production build
Component smoke tests
```

## Browser E2E

با Playwright.

در Release Validation در صورت امکان:

```text
Chromium
Firefox
WebKit
```

سناریوها:

```text
Login
Logout
Failed Login

Create Outbound
Test Outbound
Edit Outbound
Delete Outbound

Create NAT
Disable NAT
Enable NAT
Delete NAT

Create HAProxy
Backend failure
Backend recovery

Create Interface Route
Outbound failure
Kill-switch

Backup
Restore

Settings
Panel port validation
```

---

# ۳۶. Network Acceptance Matrix

همه باید Verify شوند:

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

# ۳۷. Performance Baseline

اعداد را Measure کن، حدس نزن.

ثبت:

```text
Idle CPU
Idle RSS
API latency
Dashboard load time
NAT TCP throughput
NAT UDP throughput
HAProxy TCP throughput
Proxy throughput when testable
```

در Lab از ابزارهایی مثل:

```text
iperf3
```

استفاده کن.

Baseline ذخیره شود.

Performance Claim بدون اندازه‌گیری ممنوع.

---

# ۳۸. Installation

برای Ubuntu LTSهای پشتیبانی‌شده Installation قابل پیش‌بینی بساز.

Installer:

```text
Preflight
Kernel/network feature detection
Conflict detection
Dependency validation
Random safe panel port
Users/groups
Secure directories
systemd services
Database initialization
Service start
Health verification
```

در Install شبکه فعلی کاربر را خراب نکن.

---

# ۳۹. Uninstall

Uninstall:

```text
Egress services stop
Owned firewall objects remove
Owned routes remove
Managed HAProxy/sing-box cleanup
```

Configuration خارجی دست‌نخورده بماند.

از کاربر انتخاب صریح برای نگه‌داشتن این‌ها:

```text
Database
Backups
Logs
Configuration
```

وجود داشته باشد.

---

# ۴۰. Definition of Done

صرف اینکه:

```text
Code Compiles
```

کافی نیست.

پروژه فقط وقتی تمام است که:

- Featureهای V1 کامل باشند؛
- Unit Test پاس؛
- Integration Test پاس؛
- Network Namespace Test پاس؛
- Playwright E2E پاس؛
- Release Build پاس؛
- Security Scan Review شود؛
- Rollback تست شده باشد؛
- Crash Recovery تست شده باشد؛
- Reboot Persistence تست شده باشد؛
- Leak Protection تست شده باشد؛
- Foreign Firewall Preservation تست شده باشد؛
- Installation تست شده باشد؛
- Uninstallation تست شده باشد؛
- Documentation کامل باشد؛
- Critical/High Bug حل‌نشده باقی نمانده باشد.

---

# ۴۱. خروجی نهایی

بساز:

```text
.project/FINAL_REPORT.md
```

شامل:

```text
Implemented Features
Architecture Summary
Supported Environments
Dependencies
Test Commands
Test Results
Security Scan Results
Failure Injection Results
Performance Baseline
Known Limitations
V2 Deferred Items
Install Verification
Uninstall Verification
Final Git Commit
```

در پایان:

```text
git status
```

باید Clean باشد.

---

# ۴۲. رفتار نهایی Codex

فقط با نگاه کردن به کد نگو:

```text
Done
```

به جای تست نگو:

```text
Should work
```

Evidence ارائه کن.

اگر تستی به دلیل محدودیت محیط قابل اجرا نبود، دقیقاً در `FINAL_REPORT.md` ثبت کن:

```text
چه تستی اجرا نشد
چرا اجرا نشد
چه چیزی برای اجرای آن لازم است
```

آن را به صورت Silent موفق حساب نکن.

---

# ۴۳. Backlog نسخه V2

تا زمانی که V1 کاملاً Green نشده این‌ها را پیاده‌سازی نکن:

```text
Multi-node Controller
Outbound Groups
Advanced Failover
Lowest Latency Selection
Subscription Sync
Docker/cgroup Routing
UID/Process Routing
Advanced Domain Routing
Scheduling
Notifications
Prometheus
Role-based Users
API Tokens
Cluster Management
```

Scope V2 نباید باعث ناقص ماندن V1 شود.

---

# ۴۴. قانون START

وقتی کاربر گفت:

```text
START
```

از Phase 0 شروع کن.

بعد از Plan متوقف نشو.

بعد از Scaffold متوقف نشو.

به Implementation، Testing، Checkpoint و Phaseهای بعدی ادامه بده.

---

# ۴۵. قانون CONTINUE

هر زمان کاربر گفت:

```text
CONTINUE
```

هدف اول Recovery است، نه Conversation.

State ذخیره‌شده را بخوان.

Diff را بررسی کن.

Partial Work را Recover کن.

کوچک‌ترین Verification مرتبط را اجرا کن.

`next_action` را ادامه بده.

تا زمانی ادامه بده که:

- یک مانع خارجی واقعی ایجاد شود؛ یا
- تمام Definition of Done تکمیل شود.

کاربر نباید صرفاً به خاطر قطع شدن یک Development Session مجبور شود پروژه را از اول برای Codex توضیح دهد.