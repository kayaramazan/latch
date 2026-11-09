import { createContext, useContext } from "react";
import en from "./en.json";
import tr from "./tr.json";

export type Lang = "en" | "tr";
const dicts: Record<Lang, Record<string, string>> = { en, tr };

export function translate(lang: Lang, key: string, vars?: Record<string, string | number>): string {
  let s = dicts[lang][key] ?? dicts.en[key] ?? key;
  if (vars) for (const [k, v] of Object.entries(vars)) s = s.replace(`{${k}}`, String(v));
  return s;
}

export const LangContext = createContext<{ lang: Lang; setLang: (l: Lang) => void }>({
  lang: "en",
  setLang: () => {},
});

export function useT() {
  const { lang } = useContext(LangContext);
  return (key: string, vars?: Record<string, string | number>) => translate(lang, key, vars);
}
export function useLang() {
  return useContext(LangContext);
}
