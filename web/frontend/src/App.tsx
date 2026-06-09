import { lazy, Suspense } from "react";
import { Route, Routes } from "react-router-dom";
import { AppShell } from "@/components/AppShell";
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

function PageFallback() {
  return (
    <div className="space-y-6">
      <div className="h-12 w-2/3 bg-muted animate-pulse" />
      <div className="h-32 w-full bg-muted animate-pulse" />
    </div>
  );
}

export function App() {
  return (
    <AppShell>
      <Suspense fallback={<PageFallback />}>
        <Routes>
          <Route path="/" element={<DashboardPage />} />
          <Route path="/peers" element={<PeersPage />} />
          <Route path="/map" element={<MapPage />} />
          <Route path="/packets" element={<PacketsPage />} />
          <Route path="/traces" element={<TracesPage />} />
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
