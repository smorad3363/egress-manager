import { createContext, useContext, useLayoutEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";

export type Language = "en" | "fa";

type LanguageContextValue = {
  language: Language;
  setLanguage: (language: Language) => void;
};

const STORAGE_KEY = "egress.language";
const LanguageContext = createContext<LanguageContextValue | null>(null);

const fa: Record<string, string> = {
  "Language": "زبان",
  "English": "English",
  "Persian": "فارسی",
  "Skip to content": "رفتن به محتوای اصلی",
  "Primary navigation": "منوی اصلی",
  "Open navigation": "باز کردن منو",
  "Close navigation": "بستن منو",
  "Console": "پنل",
  "Search console": "جست‌وجو در پنل",
  "Search": "جست‌وجو",
  "Dashboard": "داشبورد",
  "Outbounds": "خروجی‌ها",
  "Routes": "مسیرها",
  "Xray": "Xray",
  "Port Forward": "انتقال پورت",
  "HAProxy": "HAProxy",
  "Firewall": "دیواره آتش",
  "Network": "شبکه",
  "Logs": "گزارش‌ها",
  "Settings": "تنظیمات",
  "Soon": "به‌زودی",
  "Console connected": "پنل متصل است",
  "Server management": "مدیریت این سرور",
  "New route": "مسیر جدید",
  "Import outbound": "افزودن اتصال",
  "New binding": "قانون جدید",
  "New forward": "انتقال پورت جدید",
  "New frontend": "ورودی جدید",
  "LIVE OVERVIEW": "وضعیت کلی",
  "Traffic is flowing normally.": "ارتباط‌ها عادی هستند.",
  "Policy and transport health across this gateway.": "وضعیت مسیرها و اتصال‌های این سرور.",
  "Last reconciled": "آخرین بررسی",
  "12 seconds ago": "چند لحظه قبل",
  "Network summary": "خلاصه شبکه",
  "Active routes": "مسیرهای فعال",
  "+2 this week": "+۲ این هفته",
  "Traffic · 24h": "ترافیک ۲۴ ساعت",
  "8.2% below limit": "کمتر از حد تعیین‌شده",
  "Healthy outbounds": "خروجی‌های سالم",
  "1 in standby": "۱ مورد آماده‌به‌کار",
  "Blocked requests": "درخواست‌های مسدود",
  "Last 24 hours": "۲۴ ساعت گذشته",
  "THROUGHPUT": "ترافیک",
  "Gateway traffic": "ترافیک سرور",
  "Live": "زنده",
  "total · 24 hours": "مجموع ۲۴ ساعت",
  "SERVICES": "سرویس‌ها",
  "System health": "سلامت سرویس‌ها",
  "View network": "مشاهده وضعیت شبکه",
  "30-day availability": "دردسترس‌بودن ۳۰ روزه",
  "Privileged agent": "سرویس مدیریتی",
  "Firewall engine": "دیواره آتش",
  "SQLite": "پایگاه داده",
  "POLICY": "مسیرها",
  "View all": "مشاهده همه",
  "Route": "مسیر",
  "Source": "مبدأ",
  "Outbound": "خروجی",
  "Status": "وضعیت",
  "Traffic": "ترافیک",
  "Healthy": "سالم",
  "Standby": "آماده‌به‌کار",
  "Create a route": "ساخت مسیر",
  "Define where traffic should leave this server. You can review everything before it is applied.": "مشخص کنید ترافیک از کدام اتصال خارج شود. قبل از اجرا می‌توانید تغییرات را بررسی کنید.",
  "Route name": "نام مسیر",
  "Source CIDR": "شبکه مبدأ (CIDR)",
  "Canonical IPv4 or IPv6 CIDR": "مثال: 10.20.0.0/16",
  "Select an outbound": "یک خروجی انتخاب کنید",
  "Cancel": "انصراف",
  "Review": "بررسی",
  "The change was prepared. Review it before applying.": "تغییر آماده شد. قبل از اجرا آن را بررسی کنید.",

  "This page is not available yet": "این بخش هنوز آماده نیست",
  "Firewall controls are not exposed in this version. Existing firewall rules are left untouched.": "در این نسخه تنظیم مستقیم دیواره آتش از پنل فعال نیست. قوانین فعلی سرور بدون تغییر می‌مانند.",
  "Use Port Forward or Routes for Egress Manager-owned networking rules.": "برای قوانین مربوط به این برنامه از «انتقال پورت» یا «مسیرها» استفاده کنید.",
  "The log viewer is not available in the browser yet.": "نمایش گزارش‌ها داخل مرورگر هنوز آماده نیست.",
  "Run sudo egress-manager logs on the server to view service logs.": "برای دیدن گزارش‌ها روی سرور دستور sudo egress-manager logs را اجرا کنید.",
  "Browser settings are not available yet.": "تنظیمات داخل مرورگر هنوز آماده نیست.",
  "Run sudo egress-manager config on the server to change service settings safely.": "برای تغییر امن تنظیمات روی سرور دستور sudo egress-manager config را اجرا کنید.",
  "Nothing to create on this page": "در این صفحه موردی برای ساخت وجود ندارد",

  "SECURE CONSOLE": "پنل امن",
  "Sign in to this gateway": "ورود به پنل سرور",
  "Use the administrator account provisioned on this server.": "با حساب مدیر این سرور وارد شوید.",
  "Username": "نام کاربری",
  "Password": "رمز عبور",
  "Signing in…": "در حال ورود…",
  "Sign in": "ورود",
  "Too many attempts. Try again in one minute.": "تلاش‌های ورود زیاد بوده است. یک دقیقه بعد دوباره امتحان کنید.",
  "Sign in failed.": "ورود ناموفق بود. نام کاربری و رمز را بررسی کنید.",
  "This panel uses HTTPS. Keep the certificate valid and do not share administrator credentials.": "این پنل با HTTPS کار می‌کند. اطلاعات حساب مدیر را در اختیار دیگران قرار ندهید.",

  "READ ONLY": "فقط نمایش",
  "Host network inventory": "وضعیت شبکه سرور",
  "No host changes are made by this view.": "این صفحه چیزی روی سرور تغییر نمی‌دهد.",
  "Live interfaces, routes, listeners, DNS, and installed network services.": "کارت‌های شبکه، مسیرها، پورت‌های باز و DNS فعلی سرور.",
  "Observed": "زمان بررسی",
  "Partial inventory": "اطلاعات ناقص است",
  "Interfaces": "کارت‌های شبکه",
  "up": "فعال",
  "Listeners": "پورت‌های در حال استفاده",
  "occupied ports": "پورت در حال استفاده",
  "Default routes": "مسیرهای پیش‌فرض",
  "Capabilities": "امکانات شبکه",
  "available": "در دسترس",
  "INTERFACES": "کارت‌های شبکه",
  "Addresses & subnets": "آدرس‌ها و شبکه‌ها",
  "detected": "پیدا شد",
  "unknown": "بررسی‌نشده",
  "No address": "بدون آدرس",
  "RESOLUTION": "DNS و دروازه",
  "DNS & gateways": "DNS و دروازه اینترنت",
  "DNS source": "منبع DNS",
  "Nameservers": "DNSها",
  "Search domains": "دامنه‌های جست‌وجو",
  "Default gateway": "دروازه پیش‌فرض",
  "None": "هیچ‌کدام",
  "SOCKETS": "پورت‌ها",
  "Listening ports": "پورت‌های در حال گوش‌دادن",
  "Review conflicts before binding": "قبل از استفاده، تداخل پورت را بررسی کنید",
  "Process": "برنامه",
  "Protocol": "پروتکل",
  "Address": "آدرس",
  "Port": "پورت",
  "Service": "سرویس",
  "Unavailable": "نامشخص",
  "Occupied": "در حال استفاده",
  "CAPABILITIES": "سرویس‌های شبکه",
  "Network services": "سرویس‌های شبکه",
  "Running": "در حال اجرا",
  "Available": "نصب شده",
  "Absent": "نصب نیست",
  "not detected": "پیدا نشد",
  "Inventory unavailable": "اطلاعات شبکه در دسترس نیست",
  "Inventory is temporarily unavailable.": "فعلاً نمی‌توان اطلاعات شبکه را دریافت کرد.",
  "Sign in to inspect this gateway.": "برای دیدن وضعیت شبکه دوباره وارد شوید.",
  "Try again": "تلاش دوباره",

  "TRANSACTIONAL NAT": "انتقال پورت",
  "Port forwarding": "انتقال پورت",
  "Desired rules stay staged until review and atomic apply.": "قانون را می‌سازید، بررسی می‌کنید و بعد اجرا می‌شود.",
  "Address family": "نوع آدرس",
  "Refresh": "تازه‌سازی",
  "Review & apply": "بررسی و اجرا",
  "Configured": "تعریف‌شده",
  "Maximum 16 per apply": "حداکثر ۱۶ مورد در هر اجرا",
  "Enabled": "فعال",
  "Desired rules": "قانون فعال",
  "Accepted": "عبور کرده",
  "Packets since last apply": "بسته از آخرین اجرا",
  "Dropped": "مسدود شده",
  "Source policy rejects": "به‌دلیل محدودیت مبدأ",
  "No port forwards": "هنوز انتقال پورتی ساخته نشده",
  "Create desired NAT intent, review native changes, then apply.": "پورت ورودی و مقصد را مشخص کنید؛ سپس تغییر را بررسی و اجرا کنید.",
  "Create forward": "ساخت انتقال پورت",
  "Name": "نام",
  "Listener": "ورودی",
  "Destination": "مقصد",
  "Packets": "بسته‌ها",
  "Actions": "عملیات",
  "Any source": "هر مبدأ",
  "same": "همان پورت",
  "Disable": "غیرفعال",
  "Enable": "فعال",
  "Clone": "کپی",
  "Delete": "حذف",
  "Create port forward": "ساخت انتقال پورت",
  "Define desired intent. Host safety checks run during plan.": "آدرس و پورت ورودی و مقصد را وارد کنید. قبل از اجرا، ایمنی تنظیمات بررسی می‌شود.",
  "Listen address": "آدرس ورودی",
  "Port mode": "حالت پورت",
  "Selected ports": "پورت‌های مشخص",
  "All ports except": "همه پورت‌ها به‌جز",
  "Ports": "پورت‌ها",
  "Excluded ports": "پورت‌های مستثنا",
  "Remote address": "آدرس مقصد",
  "Remote port start": "پورت مقصد",
  "Same ports": "همان پورت ورودی",
  "Source CIDRs": "مبدأهای مجاز (CIDR)",
  "Optional comma-separated canonical CIDRs": "اختیاری؛ چند شبکه را با ویرگول جدا کنید",
  "Protocols": "پروتکل‌ها",
  "Save intent": "ذخیره قانون",
  "Review NAT plan": "بررسی انتقال پورت",
  "Only Egress Manager-owned nftables objects will change.": "فقط قوانین ساخته‌شده توسط Egress Manager تغییر می‌کنند.",
  "Generated native changes": "جزئیات فنی تغییرات",
  "Apply atomically": "اجرای امن",
  "Port forwards unavailable": "انتقال پورت در دسترس نیست",
  "Select TCP, UDP, or both.": "حداقل TCP یا UDP را انتخاب کنید.",
  "Enter at least one port or range.": "حداقل یک پورت یا بازه پورت وارد کنید.",

  "TCP LOAD BALANCER": "توزیع بار TCP",
  "Health-aware pools with graceful, transactional reloads.": "ترافیک را بین چند سرور مقصد پخش می‌کند و وضعیت آن‌ها را بررسی می‌کند.",
  "HAProxy unavailable": "HAProxy در دسترس نیست",
  "New backend": "سرور مقصد جدید",
  "Frontends": "ورودی‌ها",
  "TCP listeners": "ورودی TCP",
  "Backends": "سرورهای مقصد",
  "Pool servers": "سرور مقصد",
  "Runtime health": "وضعیت فعلی",
  "Connections": "اتصال‌های فعلی",
  "total": "مجموع",
  "TRAFFIC ENTRY": "ورودی ترافیک",
  "TCP frontends": "ورودی‌های TCP",
  "Not running": "در حال اجرا نیست",
  "No frontends": "هنوز ورودی ساخته نشده",
  "Create a TCP listener after adding at least one backend.": "ابتدا یک سرور مقصد اضافه کنید و بعد ورودی TCP بسازید.",
  "Create frontend": "ساخت ورودی",
  "Bind": "آدرس ورودی",
  "Balance": "روش تقسیم",
  "Pool": "سرورهای مقصد",
  "servers": "سرور",
  "Weight": "سهم",
  "Role": "نقش",
  "Backup": "پشتیبان",
  "Active": "اصلی",
  "Sessions": "اتصال‌ها",
  "Create backend": "افزودن سرور مقصد",
  "Add a TCP server to one or more frontend pools.": "سروری را که باید ترافیک به آن فرستاده شود اضافه کنید.",
  "Host": "آدرس سرور",
  "Behavior": "رفتار",
  "Health check": "بررسی سلامت",
  "Backup server": "سرور پشتیبان",
  "Save backend": "ذخیره سرور",
  "Bind a TCP listener to a managed backend pool.": "آدرس و پورتی را که کاربران به آن وصل می‌شوند مشخص کنید.",
  "Bind address": "آدرس ورودی",
  "Balance algorithm": "روش تقسیم ترافیک",
  "Round robin": "تقسیم نوبتی",
  "Least connections": "کمترین اتصال",
  "Backend pool": "سرورهای مقصد",
  "Add a backend first.": "ابتدا یک سرور مقصد اضافه کنید.",
  "Save frontend": "ذخیره ورودی",
  "Review HAProxy plan": "بررسی تغییرات HAProxy",
  "Candidate is validated before atomic install and graceful reload.": "تنظیمات قبل از اجرا بررسی می‌شوند و سپس بدون قطع غیرضروری سرویس اعمال می‌شوند.",
  "Advanced read-only configuration": "جزئیات فنی تنظیمات",
  "Apply & reload": "اجرا و بارگذاری دوباره",

  "SING-BOX ADAPTER": "اتصال‌های خروجی",
  "Outbound connections": "اتصال‌های خروجی",
  "Encrypted credentials, layered health, and transactional runtime apply.": "اتصال‌هایی که ترافیک سرور از طریق آن‌ها به اینترنت می‌رود.",
  "Outbounds unavailable": "اتصال‌های خروجی در دسترس نیست",
  "Total": "مجموع",
  "Stored adapters": "اتصال ذخیره‌شده",
  "Desired state": "فعال برای استفاده",
  "Last tested": "آخرین بررسی",
  "Attention": "نیازمند بررسی",
  "Degraded or unhealthy": "اتصال مشکل‌دار",
  "No outbounds": "هنوز اتصال خروجی اضافه نشده",
  "Import a URI or sing-box JSON configuration. Credentials remain encrypted and never return through the API.": "یک لینک اتصال اضافه کنید. اطلاعات محرمانه به‌صورت امن ذخیره می‌شوند.",
  "Paste one supported URI or a bounded sing-box JSON document.": "لینک اتصال را اینجا بچسبانید. در صورت نیاز می‌توانید JSON مربوط به sing-box را هم وارد کنید.",
  "URI or sing-box JSON": "لینک اتصال یا JSON sing-box",
  "Supported: VLESS, Trojan, Shadowsocks, VMess, Hysteria2, TUIC, SOCKS5, WireGuard.": "پشتیبانی‌شده: VLESS، Trojan، Shadowsocks، VMess، Hysteria2، TUIC، SOCKS5 و WireGuard.",
  "Test connection": "بررسی اتصال",
  "Save outbound": "ذخیره اتصال",
  "Clone outbound": "کپی اتصال",
  "Credentials are re-encrypted for the new ID. Clone starts disabled.": "اطلاعات محرمانه برای نسخه جدید دوباره به‌صورت امن ذخیره می‌شوند. کپی جدید ابتدا غیرفعال است.",
  "Outbound ID": "شناسه اتصال",
  "Display name": "نام نمایشی",
  "Create clone": "ساخت کپی",
  "Review sing-box plan": "بررسی تغییرات اتصال‌های خروجی",
  "Only public actions and hashes are shown. Credentials and candidate configuration remain inside egressd.": "فقط خلاصه تغییرات نمایش داده می‌شود و اطلاعات محرمانه داخل سرویس امن باقی می‌ماند.",
  "Capabilities": "قابلیت‌ها",
  "External IP": "IP خروجی",
  "Latency": "تاخیر",
  "Secrets": "اطلاعات محرمانه",
  "Not tested": "بررسی نشده",
  "protected fields": "فیلد محافظت‌شده",
  "Test": "بررسی",
  "Config": "تنظیمات",
  "Transport": "ارتباط با سرور",
  "Internet": "دسترسی اینترنت",
  "passed": "موفق",
  "failed": "ناموفق",
  "unsupported": "پشتیبانی نمی‌شود",
  "untestable": "قابل بررسی نیست",
  "healthy": "سالم",
  "degraded": "نیازمند بررسی",
  "unhealthy": "مشکل‌دار",
  "disabled": "غیرفعال",
  "Request failed.": "درخواست انجام نشد. دوباره تلاش کنید.",

  "EGRESS ROUTING": "مسیریابی خروجی",
  "Egress routes": "مسیرهای خروجی",
  "New route": "مسیر جدید",
  "Saved policies": "قانون ذخیره‌شده",
  "Desired routes": "مسیر فعال",
  "Kill switches": "محافظ قطع اتصال",
  "Leak protected": "جلوگیری از خروج ناخواسته",
  "Available targets": "خروجی قابل استفاده",
  "No egress routes": "هنوز مسیر خروجی ساخته نشده",
  "Bind an interface or subnet to an enabled outbound. Every policy is reviewed before privileged apply.": "مشخص کنید ترافیک یک کارت شبکه یا یک محدوده IP از کدام اتصال خروجی عبور کند.",
  "Create route": "ساخت مسیر",
  "Edit egress route": "ویرایش مسیر خروجی",
  "Create egress route": "ساخت مسیر خروجی",
  "All failure, DNS, and IP-family behavior is explicit.": "رفتار مسیر در زمان قطع اتصال، DNS و IPv4/IPv6 را مشخص کنید.",
  "Route ID": "شناسه مسیر",
  "Source type": "نوع مبدأ",
  "Interface": "کارت شبکه",
  "Subnet": "محدوده IP",
  "Canonical CIDR": "شبکه (CIDR)",
  "Primary outbound": "خروجی اصلی",
  "Failure behavior": "اگر خروجی قطع شد",
  "Block": "ترافیک را قطع کن",
  "Fail over": "از خروجی پشتیبان استفاده کن",
  "Direct (explicit)": "مستقیم از اینترنت سرور",
  "Fallback outbound": "خروجی پشتیبان",
  "Select fallback": "خروجی پشتیبان را انتخاب کنید",
  "DNS policy": "روش DNS",
  "Follow outbound": "DNS از همان خروجی",
  "System / direct": "DNS خود سرور",
  "DNS servers": "DNSها",
  "1–4 comma-separated IP addresses": "۱ تا ۴ آدرس IP با ویرگول جدا کنید",
  "Kill switch": "جلوگیری از نشت ترافیک",
  "Save route": "ذخیره مسیر",
  "Review egress route plan": "بررسی تغییرات مسیر",
  "Only public actions and authenticated hashes are shown. Native candidates remain inside egressd.": "فقط خلاصه تغییرات نمایش داده می‌شود؛ جزئیات حساس داخل سرویس مدیریتی می‌ماند.",
  "Atomic mutation": "اجرای امن",
  "Failure": "هنگام قطع",
  "DNS": "DNS",
  "IP families": "نسخه‌های IP",
  "Edit": "ویرایش",
  "block": "قطع",
  "failover": "پشتیبان",
  "direct": "مستقیم",
  "follow_outbound": "همراه خروجی",
  "system": "سیستم",

  "XRAY ADAPTER": "مدیریت Xray",
  "Native Xray routing": "مسیریابی Xray",
  "Discover Xray, Marzban, and 3x-ui; bind tags without double proxying.": "Xray، Marzban و 3x-ui موجود روی سرور را پیدا می‌کند و مسیر بین ورودی و خروجی‌های خود Xray می‌سازد.",
  "No standalone Xray installation has a proven managed-fragment boundary.": "برای ساخت مسیر Xray باید یک Xray مستقل و سازگار روی سرور پیدا شود. Marzban و 3x-ui در این بخش فقط قابل مشاهده‌اند.",
  "Installations": "نصب‌های پیدا شده",
  "Detected safely": "بدون تغییر سرور",
  "Writable": "قابل مدیریت",
  "Proven confdir": "آماده مدیریت",
  "Bindings": "قوانین مسیر",
  "Native routes": "مسیر فعال",
  "DISCOVERY": "شناسایی Xray",
  "Compatible installations": "نصب‌های سازگار",
  "No Xray installation detected": "Xray قابل استفاده‌ای پیدا نشد",
  "Known standalone Xray, Marzban, and 3x-ui paths were checked without changing host state.": "مسیرهای معمول Xray، Marzban و 3x-ui بررسی شدند و هیچ تغییری روی سرور انجام نشد.",
  "Scan again": "دوباره بررسی کن",
  "NATIVE ROUTING": "مسیریابی داخل Xray",
  "Inbound bindings": "قوانین ورودی به خروجی",
  "Managed fragment": "قابل مدیریت",
  "Read only": "فقط مشاهده",
  "No native bindings": "هنوز قانونی ساخته نشده",
  "Bind one discovered inbound tag directly to an Xray outbound tag.": "یک ورودی Xray را به یکی از خروجی‌های Xray متصل کنید.",
  "Bindings require a proven standalone confdir. Panel-managed configurations remain read-only.": "برای ساخت قانون، Xray مستقل و سازگار لازم است. تنظیمات Marzban و 3x-ui فقط نمایش داده می‌شوند.",
  "Create binding": "ساخت قانون",
  "Binding": "قانون",
  "Inbound": "ورودی Xray",
  "Edit Xray binding": "ویرایش قانون Xray",
  "Create Xray binding": "ساخت قانون Xray",
  "Traffic stays inside Xray: one inboundTag is routed directly to one outboundTag.": "ترافیک داخل خود Xray می‌ماند: یک ورودی را به یک خروجی متصل کنید.",
  "Binding ID": "شناسه قانون",
  "Inbound tag": "ورودی Xray",
  "Outbound tag": "خروجی Xray",
  "Select inbound": "ورودی را انتخاب کنید",
  "Select outbound": "خروجی را انتخاب کنید",
  "Save binding": "ذخیره قانون",
  "Review native Xray plan": "بررسی تغییرات Xray",
  "Only owned actions and authenticated hashes are reviewed. Foreign configuration and the candidate remain private.": "فقط تغییرات مربوط به Egress Manager بررسی می‌شوند و فایل‌های اصلی Xray دست‌نخورده می‌مانند.",
  "Service restart": "Xray دوباره راه‌اندازی می‌شود",
  "install fragment": "افزودن قانون",
  "remove fragment": "حذف قانون",
  "Foreign files stay untouched": "فایل‌های اصلی Xray تغییر نمی‌کنند",
  "Apply aborts if the confdir or owned fragment changed since this review.": "اگر تنظیمات Xray بعد از این بررسی تغییر کرده باشد، اجرا متوقف می‌شود تا چیزی ناخواسته تغییر نکند.",
  "Apply & restart Xray": "اجرا و راه‌اندازی دوباره Xray",
  "Service": "سرویس",
  "Loaded": "بارگذاری شده",
  "Version unavailable": "نسخه نامشخص",
  "Inbounds": "ورودی‌ها",
  "Outbounds": "خروجی‌ها",

  "Loading traffic chart": "در حال بارگذاری نمودار ترافیک"
};

function initialLanguage(): Language {
  try {
    const saved = window.localStorage.getItem(STORAGE_KEY);
    if (saved === "fa" || saved === "en") return saved;
  } catch {
    // Ignore blocked storage and keep a usable default.
  }
  return window.navigator.language.toLowerCase().startsWith("fa") ? "fa" : "en";
}

function translateDynamic(value: string): string {
  let match = value.match(/^Delete (.+)\?$/);
  if (match) return `«${match[1]}» حذف شود؟`;
  match = value.match(/^(\d+) enabled$/);
  if (match) return `${match[1]} فعال`;
  match = value.match(/^(\d+) detected$/);
  if (match) return `${match[1]} مورد`;
  match = value.match(/^(\d+) servers$/);
  if (match) return `${match[1]} سرور`;
  match = value.match(/^(\d+) frontends$/);
  if (match) return `${match[1]} ورودی`;
  match = value.match(/^(\d+) enabled backends$/);
  if (match) return `${match[1]} سرور مقصد فعال`;
  match = value.match(/^(\d+) routes · (\d+) outbounds$/);
  if (match) return `${match[1]} مسیر · ${match[2]} خروجی`;
  match = value.match(/^(\d+) enabled bindings · (.+)$/);
  if (match) return `${match[1]} قانون فعال · ${translateText(match[2])}`;
  match = value.match(/^REV (\d+)$/);
  if (match) return `نسخه ${match[1]}`;
  match = value.match(/^Invalid port range: (.+)$/);
  if (match) return `بازه پورت معتبر نیست: ${match[1]}`;
  return value;
}

export function translateText(value: string): string {
  return fa[value] || translateDynamic(value);
}

function translateTextNode(node: Text) {
  const raw = node.nodeValue || "";
  const trimmed = raw.trim();
  if (!trimmed) return;
  const parent = node.parentElement;
  if (parent && ["CODE", "PRE", "SCRIPT", "STYLE", "TEXTAREA"].includes(parent.tagName)) return;
  const translated = translateText(trimmed);
  if (translated === trimmed) return;
  const start = raw.match(/^\s*/)?.[0] || "";
  const end = raw.match(/\s*$/)?.[0] || "";
  node.nodeValue = `${start}${translated}${end}`;
}

function translateElement(root: Node) {
  if (root.nodeType === Node.TEXT_NODE) {
    translateTextNode(root as Text);
    return;
  }
  if (!(root instanceof Element)) return;
  for (const name of ["placeholder", "aria-label", "title"]) {
    const value = root.getAttribute(name);
    if (!value) continue;
    const translated = translateText(value);
    if (translated !== value) root.setAttribute(name, translated);
  }
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  let current: Node | null = walker.nextNode();
  while (current) {
    translateTextNode(current as Text);
    current = walker.nextNode();
  }
}

export function LanguageProvider({ children }: { children: ReactNode }) {
  const [language, setLanguageState] = useState<Language>(initialLanguage);

  const setLanguage = (next: Language) => {
    try { window.localStorage.setItem(STORAGE_KEY, next); } catch { /* Storage is optional. */ }
    setLanguageState(next);
    window.location.reload();
  };

  useLayoutEffect(() => {
    document.documentElement.lang = language === "fa" ? "fa" : "en";
    document.documentElement.dir = language === "fa" ? "rtl" : "ltr";
    if (language !== "fa") return;

    translateElement(document.body);
    const observer = new MutationObserver((records) => {
      for (const record of records) {
        if (record.type === "characterData") translateElement(record.target);
        for (const node of record.addedNodes) translateElement(node);
        if (record.type === "attributes") translateElement(record.target);
      }
    });
    observer.observe(document.body, {
      subtree: true,
      childList: true,
      characterData: true,
      attributes: true,
      attributeFilter: ["placeholder", "aria-label", "title"],
    });

    const nativeConfirm = window.confirm.bind(window);
    window.confirm = (message?: string) => nativeConfirm(translateText(String(message ?? "")));
    return () => {
      observer.disconnect();
      window.confirm = nativeConfirm;
    };
  }, [language]);

  const value = useMemo(() => ({ language, setLanguage }), [language]);
  return <LanguageContext.Provider value={value}>{children}</LanguageContext.Provider>;
}

export function useLanguage() {
  const value = useContext(LanguageContext);
  if (!value) throw new Error("useLanguage must be used inside LanguageProvider");
  return value;
}

export function LanguageSwitcher({ compact = false }: { compact?: boolean }) {
  const { language, setLanguage } = useLanguage();
  return (
    <label className={`language-switcher${compact ? " language-switcher--compact" : ""}`}>
      <span className="sr-only">Language</span>
      <select aria-label="Language" value={language} onChange={(event) => setLanguage(event.target.value as Language)}>
        <option value="en">English</option>
        <option value="fa">فارسی</option>
      </select>
    </label>
  );
}
