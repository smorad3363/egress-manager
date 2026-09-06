import { createContext, useContext, useLayoutEffect, useMemo, useState } from "react";
import type { ReactNode } from "react";
import { faEntries } from "./translations-fa";

export type Language = "en" | "fa";

type LanguageContextValue = {
  language: Language;
  setLanguage: (language: Language) => void;
};

const STORAGE_KEY = "egress.language";
const LanguageContext = createContext<LanguageContextValue | null>(null);
const fa = new Map<string, string>(faEntries);

function initialLanguage(): Language {
  try {
    const saved = window.localStorage.getItem(STORAGE_KEY);
    if (saved === "fa" || saved === "en") return saved;
  } catch {
    // Storage is optional.
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

function translateText(value: string): string {
  return fa.get(value) || translateDynamic(value);
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

function useLanguage() {
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
