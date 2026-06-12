import { lazy, Suspense } from "react";
import { Route, Routes } from "react-router-dom";
import { AppShell } from "@/components/AppShell";
import { useConfig } from "@/hooks/useConfig";
import { DashboardPage } from "@/pages/DashboardPage";
import { PeersPage } from "@/pages/PeersPage";
import { CompanionsPage } from "@/pages/CompanionsPage";

const MapPage = lazy(() =>
  import("@/pages/MapPage").then((m) => ({ default: m.MapPage })),
);
const PacketsPage = lazy(() =>
  import("@/pages/PacketsPage").then((m) => ({ default: m.PacketsPage })),
);
const TracesPage = lazy(() =>
  import("@/pages/TracesPage").then((m) => ({ default: m.TracesPage })),
);
const CompanionDetailPage = lazy(() =>
  import("@/pages/CompanionDetailPage").then((m) => ({
    default: m.CompanionDetailPage,
  })),
);
const ContactsPage = lazy(() =>
  import("@/pages/ContactsPage").then((m) => ({ default: m.ContactsPage })),
);
const ContactDetailPage = lazy(() =>
  import("@/pages/ContactDetailPage").then((m) => ({
    default: m.ContactDetailPage,
  })),
);
const ChannelsPage = lazy(() =>
  import("@/pages/ChannelsPage").then((m) => ({ default: m.ChannelsPage })),
);
const RepeaterDetailPage = lazy(() =>
  import("@/pages/RepeaterDetailPage").then((m) => ({
    default: m.RepeaterDetailPage,
  })),
);
const RepeatersListPage = lazy(() =>
  import("@/pages/RepeatersListPage").then((m) => ({
    default: m.RepeatersListPage,
  })),
);
const MonitoringPage = lazy(() =>
  import("@/pages/MonitoringPage").then((m) => ({
    default: m.MonitoringPage,
  })),
);
const MonitoringDetailPage = lazy(() =>
  import("@/pages/MonitoringDetailPage").then((m) => ({
    default: m.MonitoringDetailPage,
  })),
);
const BotsPage = lazy(() =>
  import("@/pages/BotsPage").then((m) => ({ default: m.BotsPage })),
);
const MqttPage = lazy(() =>
  import("@/pages/MqttPage").then((m) => ({ default: m.MqttPage })),
);
const RadioPage = lazy(() =>
  import("@/pages/RadioPage").then((m) => ({ default: m.RadioPage })),
);
// Lazy so Leaflet (pulled in by the position picker) stays out of the main
// bundle — the wizard only renders on first run.
const SetupWizard = lazy(() =>
  import("@/components/SetupWizard").then((m) => ({ default: m.SetupWizard })),
);

function PageFallback() {
  return (
    <div className="space-y-6">
      <div className="h-12 w-2/3 bg-muted animate-pulse" />
      <div className="h-32 w-full bg-muted animate-pulse" />
    </div>
  );
}

export function App() {
  const { config, save, reload } = useConfig();
  // Fresh install: never configured (flag unset) and no companions yet. An
  // observer-only setup that skipped the wizard has setupComplete=true, and
  // existing installs always have >=1 companion, so neither re-triggers it.
  const needsSetup =
    config != null &&
    config.setupComplete !== true &&
    (config.companions?.length ?? 0) === 0;

  return (
    <AppShell>
      {needsSetup && (
        <Suspense fallback={null}>
          <SetupWizard config={config} save={save} reload={reload} />
        </Suspense>
      )}
      <Suspense fallback={<PageFallback />}>
        <Routes>
          <Route path="/" element={<DashboardPage />} />
          <Route path="/peers" element={<PeersPage />} />
          <Route path="/map" element={<MapPage />} />
          <Route path="/packets" element={<PacketsPage />} />
          <Route path="/traces" element={<TracesPage />} />
          <Route path="/monitoring" element={<MonitoringPage />} />
          <Route path="/monitoring/:pubkey" element={<MonitoringDetailPage />} />
          <Route path="/bots" element={<BotsPage />} />
          <Route path="/mqtt" element={<MqttPage />} />
          <Route path="/radio" element={<RadioPage />} />
          <Route path="/companions" element={<CompanionsPage />} />
          <Route path="/companions/:name" element={<CompanionDetailPage />} />
          <Route
            path="/companions/:name/contacts"
            element={<ContactsPage />}
          />
          <Route
            path="/companions/:name/contacts/:pubkey"
            element={<ContactDetailPage />}
          />
          <Route
            path="/companions/:name/channels"
            element={<ChannelsPage />}
          />
          <Route
            path="/companions/:name/repeaters"
            element={<RepeatersListPage />}
          />
          <Route
            path="/companions/:name/repeaters/:pubkey"
            element={<RepeaterDetailPage />}
          />
        </Routes>
      </Suspense>
    </AppShell>
  );
}
