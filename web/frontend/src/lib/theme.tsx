import { createContext, useContext, useEffect, useState } from "react";

type Theme = "dark" | "light" | "system";

interface ThemeProviderState {
  theme: Theme;
  resolved: "dark" | "light";
  setTheme: (theme: Theme) => void;
}

const ThemeProviderContext = createContext<ThemeProviderState | undefined>(
  undefined,
);

const STORAGE_KEY = "meshcore-theme";

function readSystem(): "dark" | "light" {
  return window.matchMedia("(prefers-color-scheme: dark)").matches
    ? "dark"
    : "light";
}

export function ThemeProvider({ children }: { children: React.ReactNode }) {
  const [theme, setThemeState] = useState<Theme>(() => {
    // localStorage can throw when storage is disabled (strict privacy modes);
    // never let that take down the whole tree at mount.
    try {
      const stored = localStorage.getItem(STORAGE_KEY) as Theme | null;
      return stored ?? "dark";
    } catch {
      return "dark";
    }
  });
  const [resolved, setResolved] = useState<"dark" | "light">(() =>
    theme === "system" ? readSystem() : (theme as "dark" | "light"),
  );

  useEffect(() => {
    const root = document.documentElement;
    const apply = () => {
      const next = theme === "system" ? readSystem() : (theme as "dark" | "light");
      root.classList.remove("dark", "light");
      root.classList.add(next);
      setResolved(next);
    };
    apply();

    if (theme === "system") {
      const mql = window.matchMedia("(prefers-color-scheme: dark)");
      mql.addEventListener("change", apply);
      return () => mql.removeEventListener("change", apply);
    }
  }, [theme]);

  const setTheme = (t: Theme) => {
    try {
      localStorage.setItem(STORAGE_KEY, t);
    } catch {
      // Persistence is best-effort; the in-memory theme still applies.
    }
    setThemeState(t);
  };

  return (
    <ThemeProviderContext.Provider value={{ theme, resolved, setTheme }}>
      {children}
    </ThemeProviderContext.Provider>
  );
}

export function useTheme() {
  const ctx = useContext(ThemeProviderContext);
  if (!ctx) throw new Error("useTheme must be used within ThemeProvider");
  return ctx;
}
