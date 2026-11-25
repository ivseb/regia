import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { router } from "./router";
import { applyTheme, getTheme } from "./theme";
import { applyAppearance, followServerAppearance, storedAppearance } from "./appearance";
import { followServerLanguage, initLanguage } from "./i18n/core";
import { useLang } from "./i18n/react";
import "./styles.css";

// Chiaro/scuro, tema e font dal browser prima del primo disegno; il font si aspetta
// al massimo per un attimo (appearance.ts), poi arriva da solo. Lo stesso per la lingua:
// quella ricordata nel browser o quella del browser, finché non arriva /me.
applyTheme(getTheme());
const ready = Promise.all([applyAppearance(storedAppearance()), initLanguage()]);

const queryClient = new QueryClient({
  defaultOptions: { queries: { staleTime: 10_000, retry: false, refetchOnWindowFocus: false } },
});

// Quando arriva l'utente (caricamento, accesso, cambio di organizzazione) si applica la
// sua scelta salvata sul server, che vale anche sugli altri dispositivi.
queryClient.getQueryCache().subscribe((e) => {
  if (e.type === "updated" && e.action.type === "success" && e.query.queryKey[0] === "me") {
    followServerAppearance(e.query.state.data);
    followServerLanguage(e.query.state.data);
  }
});

/** Al cambio di lingua l'albero si ricostruisce, così anche date e numeri formattati nel render si aggiornano. */
function App() {
  const lang = useLang();
  return <RouterProvider key={lang} router={router} />;
}

void ready.then(() =>
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <QueryClientProvider client={queryClient}>
        <App />
      </QueryClientProvider>
    </StrictMode>,
  ),
);
