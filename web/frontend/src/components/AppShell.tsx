import {
  Activity,
  AudioLines,
  LayoutDashboard,
  MapPinned,
  MessagesSquare,
  Moon,
  Radar,
  Radio,
  Sun,
  Users,
  Waves,
} from "lucide-react";
import { Link, useLocation } from "react-router-dom";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarInset,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarProvider,
  SidebarRail,
  SidebarTrigger,
  useSidebar,
} from "@/components/ui/sidebar";
import { Separator } from "@/components/ui/separator";
import { Button } from "@/components/ui/button";
import { useTheme } from "@/lib/theme";
import { cn } from "@/lib/utils";

type IconType = React.ComponentType<React.SVGProps<SVGSVGElement>>;

interface NavItem {
  to: string;
  label: string;
  icon: IconType;
  match: (path: string) => boolean;
}

const PRIMARY: NavItem[] = [
  {
    to: "/",
    label: "Overview",
    icon: LayoutDashboard,
    match: (p) => p === "/",
  },
  { to: "/peers", label: "Peers", icon: Users, match: (p) => p === "/peers" },
  { to: "/map", label: "Map", icon: MapPinned, match: (p) => p === "/map" },
];

const TELEMETRY: NavItem[] = [
  {
    to: "/packets",
    label: "Packets",
    icon: AudioLines,
    match: (p) => p === "/packets",
  },
  {
    to: "/traces",
    label: "Trace",
    icon: Radar,
    match: (p) => p === "/traces",
  },
];

const COMMS: NavItem[] = [
  {
    to: "/companions",
    label: "Companions",
    icon: MessagesSquare,
    match: (p) => p.startsWith("/companions"),
  },
];

function ThemeToggle() {
  const { resolved, setTheme } = useTheme();
  const isDark = resolved === "dark";
  return (
    <Button
      variant="ghost"
      size="sm"
      onClick={() => setTheme(isDark ? "light" : "dark")}
      className="w-full justify-start gap-2 text-xs uppercase tracking-[0.1em] font-mono text-muted-foreground hover:text-foreground"
    >
      {isDark ? (
        <Sun className="size-3.5" />
      ) : (
        <Moon className="size-3.5" />
      )}
      <span>{isDark ? "light" : "dark"} mode</span>
    </Button>
  );
}

function NavSection({
  label,
  items,
  pathname,
}: {
  label: string;
  items: NavItem[];
  pathname: string;
}) {
  const { isMobile, setOpenMobile } = useSidebar();
  return (
    <SidebarGroup>
      <SidebarGroupLabel className="font-mono text-[10px] uppercase tracking-[0.14em] text-muted-foreground/70">
        {label}
      </SidebarGroupLabel>
      <SidebarGroupContent>
        <SidebarMenu>
          {items.map((item) => {
            const active = item.match(pathname);
            const Icon = item.icon;
            return (
              <SidebarMenuItem key={item.to}>
                <SidebarMenuButton
                  asChild
                  isActive={active}
                  tooltip={item.label}
                  className={cn(
                    "rounded-sm font-mono text-[12px] uppercase tracking-[0.08em] transition-colors h-8",
                    active &&
                      "bg-primary/10 text-primary border-l-2 border-primary rounded-l-none data-[active=true]:bg-primary/10",
                  )}
                >
                  <Link
                    to={item.to}
                    onClick={() => isMobile && setOpenMobile(false)}
                  >
                    <Icon className="size-4" strokeWidth={1.6} />
                    <span>{item.label}</span>
                  </Link>
                </SidebarMenuButton>
              </SidebarMenuItem>
            );
          })}
        </SidebarMenu>
      </SidebarGroupContent>
    </SidebarGroup>
  );
}

export function AppShell({ children }: { children: React.ReactNode }) {
  const location = useLocation();
  const pathname = location.pathname;

  return (
    <SidebarProvider
      defaultOpen
      style={
        {
          "--sidebar-width": "13.5rem",
          "--sidebar-width-mobile": "16rem",
        } as React.CSSProperties
      }
    >
      <Sidebar
        variant="sidebar"
        collapsible="icon"
        className="border-r border-sidebar-border"
      >
        <SidebarHeader className="border-b border-sidebar-border h-14 px-3 justify-center">
          <Link to="/" className="flex items-center gap-2.5">
            <div className="relative size-8 grid place-items-center rounded-sm bg-primary/10 border border-primary/30 shrink-0">
              <Radio
                className="size-4 text-primary"
                strokeWidth={2}
              />
              <span className="absolute -top-0.5 -right-0.5 size-1.5 rounded-full bg-primary scan-pulse" />
            </div>
            <div className="flex flex-col leading-tight group-data-[collapsible=icon]:hidden">
              <span className="font-mono text-[11px] uppercase tracking-[0.18em] text-muted-foreground">
                MeshCore
              </span>
              <span className="font-mono text-[10px] uppercase tracking-[0.14em] text-primary">
                operator console
              </span>
            </div>
          </Link>
        </SidebarHeader>

        <SidebarContent className="gap-1">
          <NavSection label="Network" items={PRIMARY} pathname={pathname} />
          <Separator className="mx-3 bg-sidebar-border/50" />
          <NavSection
            label="Telemetry"
            items={TELEMETRY}
            pathname={pathname}
          />
          <Separator className="mx-3 bg-sidebar-border/50" />
          <NavSection label="Comms" items={COMMS} pathname={pathname} />
        </SidebarContent>

        <SidebarFooter className="border-t border-sidebar-border px-2 py-2 gap-1 group-data-[collapsible=icon]:hidden">
          <div className="px-2 py-1 flex items-center justify-between font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground/70">
            <span>system</span>
            <span className="inline-flex items-center gap-1.5">
              <Activity className="size-2.5 text-success" />
              <span className="text-success">nominal</span>
            </span>
          </div>
          <ThemeToggle />
        </SidebarFooter>
        <SidebarRail />
      </Sidebar>

      <SidebarInset className="bg-background overflow-hidden flex flex-col">
        <header className="shrink-0 z-30 h-14 flex items-center gap-3 border-b border-border bg-background/80 backdrop-blur supports-[backdrop-filter]:bg-background/60 px-4">
          <SidebarTrigger className="size-8" />
          <Separator orientation="vertical" className="h-5" />
          <div className="flex items-center gap-2 font-mono text-[11px] uppercase tracking-[0.14em] text-muted-foreground">
            <Waves className="size-3.5 text-primary" />
            <span>meshnet</span>
            <span className="text-muted-foreground/40">/</span>
            <span className="text-foreground">{routeLabel(pathname)}</span>
          </div>
          <div className="ml-auto flex items-center gap-3 font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground/70">
            <ClockBadge />
          </div>
        </header>

        <main className="relative flex-1 overflow-y-auto overflow-x-hidden px-4 sm:px-6 py-6 max-w-[100vw]">
          <div className="relative z-10">{children}</div>
        </main>
      </SidebarInset>
    </SidebarProvider>
  );
}

function routeLabel(pathname: string): string {
  if (pathname === "/") return "overview";
  const seg = pathname.split("/").filter(Boolean).map(decodeURIComponent);
  if (seg.length === 0) return "overview";
  if (seg[0] === "companions" && seg[1] && seg[2] === "repeaters")
    return `companions / ${seg[1]} / repeater`;
  if (seg[0] === "companions" && seg[1] && seg[2])
    return `companions / ${seg[1]} / ${seg[2]}`;
  if (seg[0] === "companions" && seg[1]) return `companions / ${seg[1]}`;
  return seg.join(" / ");
}

import { useEffect, useState } from "react";
function ClockBadge() {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const id = setInterval(() => setNow(new Date()), 1000);
    return () => clearInterval(id);
  }, []);
  const time = now.toLocaleTimeString(undefined, {
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hour12: false,
  });
  return (
    <span className="hidden md:inline-flex items-center gap-1.5">
      <span className="size-1.5 rounded-full bg-primary scan-pulse" />
      <span className="text-foreground tabular-nums">{time}</span>
    </span>
  );
}
