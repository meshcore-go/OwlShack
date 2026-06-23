import React from "react";
import ReactDOM from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import { App } from "./App";
import { Toaster } from "@/components/ui/sonner";
import { TooltipProvider } from "@/components/ui/tooltip";
import { ThemeProvider } from "@/lib/theme";
import { registerServiceWorker } from "@/lib/registerSW";
import "leaflet/dist/leaflet.css";
import "./index.css";

ReactDOM.createRoot(document.getElementById("app")!).render(
  <React.StrictMode>
    <ThemeProvider>
      <TooltipProvider delayDuration={150}>
        <BrowserRouter>
          <App />
          <Toaster
            position="bottom-right"
            theme="system"
            toastOptions={{
              classNames: {
                toast:
                  "!bg-card !border !border-border !text-foreground !rounded-none !font-mono !text-xs",
              },
            }}
          />
        </BrowserRouter>
      </TooltipProvider>
    </ThemeProvider>
  </React.StrictMode>,
);

registerServiceWorker();
