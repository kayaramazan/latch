import React, { useEffect, useState } from "react";
import ReactDOM from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter } from "react-router-dom";
import { LangContext, Lang } from "./i18n";
import { App } from "./app/App";
import "./index.css";

const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 2000 } } });

function Root() {
  const [lang, setLang] = useState<Lang>(() => (localStorage.getItem("lang") as Lang) || "en");
  useEffect(() => { localStorage.setItem("lang", lang); document.documentElement.lang = lang; }, [lang]);
  return (
    <LangContext.Provider value={{ lang, setLang }}>
      <QueryClientProvider client={qc}>
        <BrowserRouter>
          <App />
        </BrowserRouter>
      </QueryClientProvider>
    </LangContext.Provider>
  );
}

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode><Root /></React.StrictMode>,
);
