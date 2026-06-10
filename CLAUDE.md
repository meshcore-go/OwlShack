# CLAUDE.md — project handover

Canonical context for working on this codebase. Read this in full before making changes.

External references that are **not** duplicated here:
- [README.md](./README.md) — public-facing project intro.
- [MESHCORE_APP_FEATURES.md](./MESHCORE_APP_FEATURES.md) — feature reference for the upstream MeshCore Flutter app, used as a parity yardstick.
- [`internal/api/server.go`](./internal/api/server.go) `s.routes()` — canonical list of REST/WS endpoints; the table below summarises but the source is authoritative.
- [`internal/store/store.go`](./internal/store/store.go) — schema migrations.
- `~/Data/wesley/MeshCore/src/helpers/CommonCLI.cpp` — firmware CLI handler. The autocomplete catalogue inside [`web/frontend/src/pages/RepeaterDetailPage.tsx`](./web/frontend/src/pages/RepeaterDetailPage.tsx) (`CLI_TOPLEVEL_COMMANDS`, `CLI_CONFIG_KEYS`) mirrors it; regenerate from there if upstream adds commands.
- `~/.claude/projects/-home-dylan-Data-wesley-meshcore-bot/memory/project_dev_workflow.md` — the build/restart cycle reminder.

---

## What this is

A MeshCore companion + observer + repeater-admin tool, distributed as a single static Go binary. It speaks the MeshCore mesh protocol, optionally bridges to MQTT (LetsMesh, CoreScope), persists state to SQLite, and serves a React SPA at `:4432` for live ops.

Capabilities visible in the UI:

- **Overview** — live peer count, peer-type spectrum, recently-seen list, companions roster.
- **Peers** — searchable/sortable table with type pills, signal bars, last-seen.
- **Map** — Leaflet map of geolocated peers, type-filterable.
- **Packets** — live packet stream grouped by `packetHash`, with a resizable detail Sheet (route, path size, hops, signal, observation timeline, raw hex).
- **Trace** — interactive path-builder + result timeline.
- **Companions → Companion Detail** — chat client (channels + DMs) per companion.
- **Contacts / Channels / Repeaters** — per-companion management pages.
- **Repeater Detail** — login, status telemetry, CLI terminal w/ autocomplete, neighbours list, full settings panel (radio, position w/ map picker, advert intervals, routing, owner info, security).

---

## Tech stack

| Layer | Tech |
|---|---|
| Backend | Go 1.26+ (no CGO), `modernc.org/sqlite`, `embed.FS` for SPA, port `:4432` |
| Frontend | **React 19** + Vite 6 + TypeScript 5.7, Tailwind v4, **shadcn/ui (new-york style)** |
| Routing | `react-router-dom@7` |
| Real-time | WebSocket at `/api/ws`, topics: `peers`, `packets`, `messages`, `traces` |
| Mesh proto | `github.com/meshcore-go/meshcore-go` (locally replaced via `../meshcore-go` in `go.mod`) |
| Map | Leaflet + CARTO tiles (dark/light auto-switch via `<html class="dark">`) |
| Toasts | `sonner` |
| Config | TOML at `config.toml` in CWD (see `config.toml.example`) |

> **Important**: The frontend is React, not Preact. Migrating off Preact was the first large change in the recent UI rewrite — DaisyUI, PicoCSS, preact-router, `@preact/preset-vite`, lucide-preact have been removed and must NOT be reintroduced.

---

## Build / run / test

The Go binary embeds `web/frontend/dist/` via `go:embed` ([`web/embed.go`](./web/embed.go)) and serves the SPA on `:4432`. **Do not run `vite dev`** — there is no API server alongside it and the WS / MeshCore endpoints don't exist outside the Go binary.

```bash
# Standard rebuild + restart
cd /home/dylan/Data/wesley/meshcore-bot/web/frontend && npm run build
cd /home/dylan/Data/wesley/meshcore-bot && go build -o meshcore-bot .
screen -S meshcore -X quit; sleep 1
screen -dmS meshcore bash -c './meshcore-bot -vvv 2>&1 | tee /tmp/meshcore-bot2.log'

# Logs
tail -f /tmp/meshcore-bot2.log

# Inspect the DB read-only while the bot holds it open (verify migrations, SNR, etc.)
python3 -c "import sqlite3; c=sqlite3.connect('file:meshcore.db?mode=ro', uri=True); print(c.execute('PRAGMA user_version').fetchone())"
```

`npm run build` is `tsc -b && vite build`. Type errors fail the build. Bundle is currently ~770 kB minified — Vite emits a >500 kB warning we don't currently action.

> **Gotcha**: `go build` must run from the project root (`/home/dylan/Data/wesley/meshcore-bot`), not from `web/frontend/`. If you chain commands after `npm run build`, use absolute paths — `cd ..` from `web/frontend` only reaches `web/`, not root.

There's a Playwright config at [`web/frontend/playwright.config.ts`](./web/frontend/playwright.config.ts) but it's only used for ad-hoc visual checks driven through the Playwright MCP server against the running binary. No CI for the SPA.

---

## Database writes — strict rule

All SQLite writes MUST go through `store.WriteAsync(fn)` (RX/hot path; never blocks) or `store.WriteSync(fn)` (HTTP handlers; blocks until done so caller sees auto-generated IDs). Direct calls like `c.store.Peers.Upsert(...)` race with the writer goroutine and produce `SQLITE_BUSY` under load. The writer goroutine is the only goroutine that should call `db.Exec` outside of read paths. Don't call `WriteSync` from RX dispatch or from inside another writer closure — both deadlock.

**Driver pragmas use modernc syntax.** `store.Open` sets pragmas via `_pragma=` query params (`?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)`). The driver is `modernc.org/sqlite`, **not** `mattn/go-sqlite3`, so the mattn-style `_journal_mode=WAL&_busy_timeout=...` form is silently ignored — which once left the DB in rollback-journal mode with no busy timeout, so concurrent reads raced the writer and failed immediately with `SQLITE_BUSY`. WAL lets readers run alongside the single writer; keep the `_pragma=` form.

---

## Repo layout

The code models two **independent** axes of the MeshCore world:

- **`internal/node/<type>`** — node personalities the bot *runs / emulates* on the mesh (owns an
  identity, lifecycle, packet handlers; one per `[[companion]]`-style config block). Today: `companion`.
  Future: `repeater`, `sensor`, `roomserver`.
- **`internal/client/<type>`** — protocol *clients* that drive *remote* nodes (request → parse).
  Today: `repeater` (admin: login/CLI/status/path/neighbours/telemetry/ACL). Future: `sensor`
  (telemetry), `roomserver` (join/post), `companion`.

A running node uses clients to talk to others: `node/companion → client/repeater`, never the reverse.
Shared node plumbing (identity/radio/dispatch/advert) can factor into an `internal/node` engine when a
second role lands — not created yet to avoid empty abstractions.

The dependency arrow points inward:
`main → internal/app → {node/companion, client/repeater, modem, echo, mqtt, trigger, config} → {api, store}`.
**`api` never imports the domain** — the seam is the `api.Backend` interface (see below).

### Go

```
main.go                    thin entrypoint (~50 ln): flags, logging.Configure, app.Run
internal/
  app/                     supervisor loop (SIGHUP reload, reconnect+backoff), node
                           lifecycle, peer hydration, packet logger, and the api.Backend impl
  config/                  Config types + load/marshal/validate/defaults (multi-format)
  logging/                 LevelTrace const + Configure(verbosity, level) slog setup
  buildinfo/               Version var (set via -ldflags at release)
  node/                    node personalities we RUN
    companion/             companion runtime: channels, DMs, triggers, templater, identity
  client/                  clients for remote nodes we TALK TO
    repeater/              repeater.Client: login, status, CLI, path, neighbours, telemetry, ACL
  echo/                    echo.Tracker: tx echoes/repeats; emits repeatCount WS updates
  modem/                   modem.Setup/State/MuxOptions + KISS/companion stats providers
  mqtt/                    MQTT observer + packet/status formatting + JWT auth
  trigger/                 Trigger interface, ChannelTrigger, CronTrigger, Templater
  api/                     HTTP+WS server (mux, routes, websocket hub); Backend seam
  store/                   SQLite persistence (peers, contacts, channels, messages,
                           packets, conversations, echoes, blocked senders)
web/embed.go               go:embed of web/frontend/dist  (stays at root — go:embed can't reach ..)
web/frontend/              React SPA
```

> Two big single-type files remain (`internal/client/repeater/client.go`, `internal/node/companion/companion.go`).
> They're cohesive; splitting their parsers/handlers into sibling files within the same package is
> a safe future cleanup (no import changes).

### Go architecture highlights

**Two-tier peer model.**
- `discovered_peers` (SQLite) — every peer ever seen, shared across companions.
- `companion_contacts` (SQLite) — per-companion contact list with metadata (`isRepeater`, `repeaterPassword`, …).

**Database migrations** (in `internal/store/store.go`): squashed to a single `migrateV1` baseline (`user_version=1`) that creates all tables. Add changes as `migrateV2`+ appended to the `migrations` slice — never edit `migrateV1`. `snr` columns are `REAL` (real dB); `rssi` is `INTEGER` (dBm).

**Repeater management (client — we drive remote repeaters; this is NOT repeater emulation).**
- `repeater.Client` ([`internal/client/repeater/client.go`](./internal/client/repeater/client.go)) handles login, status request, CLI, path ops, neighbours, sessions. Owned by a running node and exposed via `Companion.Repeaters()`.
- Login uses `ANON_REQ` (0x07) via flood → `PATH` response (0x08) or direct `RESPONSE` (0x01).
- Status / CLI use the **ephemeral shared secret** from the login session (NOT the node identity).
- CLI flags byte = `TXT_TYPE_CLI_DATA << 2` (= 4).
- Sessions live in memory — lost on server restart; re-login required.
- PATH packet: destination = recipient's ephemeral hash, source = sender's hash.

**API ↔ domain seam — `api.Backend`.** The `api` package must never import the domain (would create an import cycle: domain already imports `api` for `*api.Hub`). So `api.Server` holds a single `api.Backend` interface ([`internal/api/backend.go`](./internal/api/backend.go)) behind one `sync.RWMutex`, swapped atomically via `SetBackend` on startup / reload / reconnect. `internal/app` implements it ([`internal/app/backend.go`](./internal/app/backend.go)) over the live `[]*companion.Companion`. Route handlers reach it through thin accessors on `Server` (`CompanionLookup()`, `ChannelMutator()`, `ConfigPersist()`, `repeaterOps()`, …) that return bound backend methods, or nil when no backend is wired yet (handlers already guard with 503/404). **Do not** reintroduce per-feature `Set*`/mutex pairs — add a method to `Backend` instead.

**API layer** ([`internal/api/`](./internal/api/)).
- `server.go` — SPA handler, route registration, the `Backend` accessors.
- `repeater.go` — 8 repeater endpoints (login, status, cli, session GET/DELETE, path GET/DELETE/PUT).

### Frontend (`web/frontend/src/`)

```
main.tsx           ThemeProvider + TooltipProvider + BrowserRouter + <Toaster>
App.tsx            <AppShell> + <Routes>
index.css          Tailwind v4 import, oklch theme tokens, base layer (cursors,
                   body grid), utility classes (label-overline, panel, scan-pulse),
                   Leaflet overrides
vite-env.d.ts      vite/client + *.css module shim

components/
  AppShell.tsx     sidebar + header + clock + theme toggle
  PageHeader.tsx   PageHeader, PageMeta
  PeerAvatar.tsx   hashed-color square avatar w/ first-letter or emoji
  SectionTitle.tsx            shared panel header (eyebrow + title + trailing)
  InlineConfirm.tsx           confirm-to-remove trigger ("Remove? yes/no" in place; iconOnly variant)
  LoadErrorAlert.tsx          destructive alert + retry button for failed page loads
  SignalStrength.tsx          also exports snrTextClass / snrFill
  StatusIndicator.tsx         ConnectionPill, PeerTypePill, PEER_TYPE_HEX
  AddContactDialog.tsx        reusable manual-only add-contact modal (Type/Name/Pubkey), prefillable
  PeerDetailSheet.tsx         peer detail slide-over + share menu (copy key/link, QR via `qrcode`, share-in-message)
  ui/                         shadcn primitives — alert, avatar, badge, button, card,
                              dialog, dropdown-menu, input, label, popover, progress,
                              scroll-area, select, separator, sheet, sidebar, skeleton,
                              sonner, switch, table, tabs, textarea, tooltip

hooks/
  useWebSocket.ts  single global WS, topic-subscription, reconnect with backoff,
                   returns { connected, messages } and accepts an optional
                   per-message callback. Mobile-resume aware: on visibilitychange/
                   focus/online/pageshow it reconnects immediately (skipping the
                   backoff) and probes an OPEN-but-suspect socket with
                   {"action":"ping"} — no message within 5s ⇒ half-open ⇒ force
                   reconnect. A backgrounded mobile tab WILL lose its socket (OS
                   suspension, no web API prevents it); this makes recovery
                   instant rather than backoff-delayed.
  useApiList.ts    shared fetch scaffold for list endpoints — { items, setItems,
                   loading, error, reload }; items is null until first success,
                   then `json || []`; setItems is exposed for WS merging; pass
                   url=null to defer. Use this instead of hand-rolling
                   loading/error/useEffect fetch state on a page.
  use-mobile.ts    shadcn-shipped breakpoint hook

lib/
  utils.ts         cn() (clsx + tailwind-merge)
  format.ts        timeAgo, formatDateTime, formatShortTime, truncate, truncateMid,
                   formatUptime, formatSecsAgo, formatBattery
  leaflet.ts       shared CARTO tile plumbing: tileUrlForTheme, themeTileLayer(),
                   useThemeTiles(mapRef, tileRef) — used by MapPage, TrackMap,
                   RepeaterDetailPage's position picker. Use these for any new map.
  theme.tsx        ThemeProvider; light/dark/system, persists to localStorage,
                   toggles class on <html>

pages/
  DashboardPage.tsx        Reference page — copy patterns from here
  PeersPage.tsx
  MapPage.tsx              Leaflet, type filter pills, theme-aware tiles
  PacketsPage.tsx          live stream grouped by packetHash, resizable detail Sheet
  TracesPage.tsx           companion + path-hash-size pickers, repeater list, vertical timeline
  CompanionsPage.tsx
  CompanionDetailPage.tsx  threads list + chat panel + path/echoes modals (largest page).
                           `MessageText` linkifies shared-contact embeds `<pubkey:type:name>`
                           (type 1=CHAT/2=REPEATER/3=ROOM/4=SENSOR), `lat,lon` coords (→ map menu),
                           http(s) URLs (→ click-confirm). `?compose=` prefills the composer.
  ContactsPage.tsx
  ChannelsPage.tsx
  RepeatersListPage.tsx    per-companion repeater roster
  RepeaterDetailPage.tsx   login + 4 tabs (Status / Terminal / Neighbors / Settings)
```

---

## REST endpoints (summary — see `internal/api/server.go` for the canonical list)

```
GET  /api/peers
DELETE /api/peers/{pubkey}
GET  /api/packets?limit=N

GET  /api/companions
GET  /api/companions/{name}/contacts
GET  /api/companions/{name}/contacts/{pubkey}                (single contact; 404 if absent)
POST /api/companions/{name}/contacts                         { pubkey }
DELETE /api/companions/{name}/contacts/{pubkey}
PATCH /api/companions/{name}/contacts/{pubkey}               { isRepeater?, repeaterPassword?, ... }

GET|POST|DELETE /api/companions/{name}/channels[/{channel}]

GET  /api/companions/{name}/conversations
POST /api/companions/{name}/conversations/{id}/read          { lastReadId }
POST /api/companions/{name}/conversations/{id}/block         { sender }
DELETE /api/companions/{name}/conversations/{id}/block/{sender}
GET  /api/companions/{name}/conversations/{id}/block
GET  /api/companions/{name}/conversations/{id}/participants
DELETE /api/companions/{name}/conversations/{id}/messages

GET  /api/companions/{name}/channels/{channel}/key
PATCH /api/companions/{name}/channels/{channel}               { name }

GET  /api/companions/{name}/contacts/{pubkey}/path
DELETE /api/companions/{name}/contacts/{pubkey}/path

GET  /api/companions/{name}/messages?channel=&limit=&afterId=
POST /api/companions/{name}/messages                         { channel, text }
DELETE /api/messages/{id}
GET  /api/messages/{id}/echoes
GET  /api/companions/{name}/messages/{id}/path?channel=

POST /api/companions/{name}/trace                            { path, pathHashSize }

POST /api/companions/{name}/repeaters/{pubkey}/login         { password }
GET  /api/companions/{name}/repeaters/{pubkey}/status
POST /api/companions/{name}/repeaters/{pubkey}/cli           { command }
GET|DELETE /api/companions/{name}/repeaters/{pubkey}/session
GET|DELETE|PUT /api/companions/{name}/repeaters/{pubkey}/path

GET  /api/nodes/monitored
GET  /api/nodes/{pubkey}/metrics
GET  /api/nodes/{pubkey}/history?metric=&from=&to=&bucket=
POST /api/nodes/{pubkey}/poll                                (on-demand poll; 502 on failure)

WS   /api/ws
```

`?afterId=` was added in the recent UI rewrite. `internal/store/message.go` exports `ListAfter(companionID, channel, afterID, limit)` and `routes_messages.go` honours `?afterId=` to return messages with `id > afterID` ordered ASC. The frontend uses this for delta backfill on cache re-open and on WS reconnect.

---

## Node monitoring

A type-agnostic poller (`internal/monitor`) polls monitored contacts on a staggered schedule → `node_metrics` (time-series) + `node_state` (latest snapshot, JSON `metric→value`). Gotchas:

- **Kind resolution is peer-type-first** (`monitorKind` in `internal/app/monitor_collector.go`): the advertised peer type wins when known; `metadata.isRepeater` is only the fallback for repeaters whose advert hasn't been heard. A stray `isRepeater` flag must never route a chat node to the repeater collector — companion firmware has no login handler, so that poll can only time out.
- **Companions are sessionless, telemetry-only.** `companionCollector` sends one `REQ_TYPE_GET_TELEMETRY_DATA` contact request (ECDH with the static identity — no login, no password). What comes back is gated by the *remote* node's telemetry-sharing prefs (base/location/environment, optionally per-contact flags) and requires the bot to be in its contacts. `MonitoringSettings` takes a `kind` prop ("repeater" | "companion") and hides password/probes for companions; its save only writes `isRepeater`/`repeaterPassword`/`monitorProbes` for the repeater kind. Chat contacts enrol via the monitoring panel on `ContactDetailPage`.

- **Metric keys are channel-faithful.** A node's *own* readings keep clean names (`mcu_temperature`, `battery`); *external* sensors carry their LPP channel (`temperature_ch2`, `humidity_ch3`) — the channel is the firmware's stable per-sensor identity on 1.16+. Status fields are fixed names (`last_snr`, `battery_mv`). Don't strip the channel to "unify" sensors — that makes multi-sensor identity order-dependent. Frontend tiles pattern-match by type (`metricKeysOfType` in `NodeStatTiles.tsx`), so they render whatever channel a board uses; `metricDef`/`metricOrderIndex` resolve `_chN` keys back to the base catalogue.
- **Firmware channel split:** ≤1.15 lumps all sensors on the self channel (ch1); 1.16+ gives each its own (ch2+). `parseRepeaterTelemetry` marks only the FIRST self-type reading on the self channel as the node's own (MCU temp/battery/location), the rest external — otherwise 1.15's external temp collides with the MCU temp under `mcu_temperature` and is lost. A 1.15→1.16 upgrade changes a board's channel keys (accepted one-off history discontinuity).
- **Snapshot merges, never blanks:** `UpsertNodeState` merges new readings onto the stored snapshot, so a partial poll (status OK, telemetry failed) keeps last-known values; a fully-failed poll uses `MarkPollFailure` (touches only `last_poll_ts`/`last_error`). Telemetry/neighbours probes retry once in-poll (`monitorProbeAttempts`).
- **Manual poll:** `monitor.Service.PollNow` (behind `POST /api/nodes/{pubkey}/poll`) runs an out-of-band poll, serialized against scheduled polls via `pollMu.TryLock` (returns "a poll is already in progress" rather than racing the radio).
- **Overview staleness dot** derives from each node's configured interval, not a constant: stale when `age > interval + max(25%, 5min)`. The interval is exposed per-node via `intervalSecs` on `/api/nodes/monitored`.
- **List membership is toggle-driven, not data-driven:** `/api/nodes/monitored` lists the poller's current target set (`monitor.Service.Targets()`, i.e. contacts with the monitor flag), merged with `node_state` snapshots where they exist. A freshly enrolled node appears immediately with `lastPollTs: 0` (UI shows a muted dot + "waiting for first poll"); toggling monitoring off hides a node even though its `node_state` row is retained. Don't go back to listing `node_state` rows directly — that hid new nodes until their first poll and showed unmonitored leftovers forever.

---

## WebSocket payload shapes

Topics emitted from the Go side:

- `peers` — `{ pubkey, name, type, lat, lon, lastSeen, lastAdvertTs, snr, rssi, outPath, outPathHashSize }`. `outPath` is the advert path; hops = `len(outPath bytes)/outPathHashSize` (empty ⇒ direct/0 hops).
- `packets` — `{ id?, receivedAt, direction, raw, payloadType, route, pathHashSize, hops, packetHash, summary, snr, rssi }`
- `messages` — **flat** payload `{ companion, channel, sender, text, direction, timestamp, id, snr?, rssi?, hops?, pathHashSize?, repeatCount? }`. There is **no `message` wrapper.** A previous bug had the React handler reading `payload.message` and silently dropping every WS message — re-introducing that wrapper would re-create the bug. The action variant `{ action: "repeatCount", companion, channel, id, repeatCount }` updates a tx echo count.
- `traces` — `{ companion, tag, hops, path, hopSNRs, snr }`

**Keepalive**: the hub pings every 50 s with a 75 s read deadline (zombie clients are reaped; browsers answer protocol pings automatically). Clients may send `{"action":"ping"}` and get `{"topic":"pong"}` back — used by `useWebSocket` to detect half-open sockets on mobile tab resume. Page handlers must filter by topic anyway, so the `pong` topic reaching them is harmless.

> **SNR vs RSSI.** SNR is **real dB** end-to-end (`snr REAL` / `*float64` / JSON number, rounded to 1 dp in UI). The wire/firmware value is quarter-dB (×4); meshcore-go converts it at ingest. RSSI is raw `int8` dBm. `<SignalStrength>` thresholds are real dB. Trace per-hop SNRs come from raw `pkt.Path` bytes (still ×4 — decoded `/4` in the bot); `meshcore.PathSNRdB(b)` helps.

---

## UI / styling system

Aesthetic: **operator console / radio shack utility**. Phosphor-green primary on warm-charcoal dark, JetBrains Mono for everything diagnostic, sharp corners. **Always read [`pages/DashboardPage.tsx`](./web/frontend/src/pages/DashboardPage.tsx) once before adding a new page** — every other page mirrors its eyebrow → title → `space-y-8` → panel-with-section-titles → divided-row pattern.

### Theme tokens (full list in [`index.css`](./web/frontend/src/index.css))

- Standard shadcn token set + `*-foreground` variants.
- Status: `success`, `warning`, `info`.
- Signal: `signal`, `signal-strong`, `signal-weak`, `signal-dead` (used by `<SignalStrength>`).
- Charts: `chart-1`–`chart-5`.
- Sidebar tokens.

`--primary` is `oklch(0.78 0.18 155)` in dark mode. **Don't introduce a different accent.** Blue/violet are reserved for chart and type-pill tones.

### Typography

- Body: **Inter**. Mono: **JetBrains Mono**. Both preconnected in [`index.html`](./web/frontend/index.html).
- All labels, eyebrows, badges, buttons: mono uppercase, `tracking-[0.08em]`–`tracking-[0.14em]`.
- Numerics get `tabular-nums`.

### Layout primitives

- Panels: `bg-card border border-border` (or use the `panel` utility).
- Sharp corners: `rounded-none` by default, `rounded-sm` (2 px) for chips/tiles. Never `rounded-lg`/`xl`/`full`.
- Eyebrows: `<span className="label-overline">…</span>`.
- Stat grids: 1 px border separators via `gap-px bg-border border border-border` (see `DashboardPage.StatCell` and `RepeaterDetailPage.StatTile`).
- Page wrapper: `<div className="space-y-8">` then `<PageHeader>` then sections.
- Empty states: centered `<CircleDashed>` + small uppercase mono caption.
- Background grid texture is painted by `body::before` in `index.css` — keep it.

### Reusable components — use these, don't reinvent

- `<PageHeader />`, `<PageMeta />`
- `<ConnectionPill connected />`
- `<PeerTypePill type />`
- `<SignalStrength snr size="sm|md" showLabel />`
- `<PeerAvatar name size="xs|sm|md|lg" />`
- shadcn primitives at `@/components/ui/*`
- Toasts: `import { toast } from "sonner"`

### Cursors

`index.css` has a global rule giving `cursor: pointer` to non-disabled `<button>`, `[role="button"|"menuitem"|"option"|"tab"]`, `<summary>`, and `<label[for]>`. Don't add per-component cursor classes. The shadcn `dropdown-menu.tsx` was patched to use `cursor-pointer` instead of its default `cursor-default` to match this convention.

### Maps

Leaflet popup / zoom-control / attribution / container backgrounds are themed in `index.css`. Default marker icons are bundler-incompatible; the rebind lives at the top of [`RepeaterDetailPage.tsx`](./web/frontend/src/pages/RepeaterDetailPage.tsx) — copy that pattern if you add another Leaflet usage.

---

## Patterns established in the recent rewrite

**Tab persistence + fetch-once.** For tabs that hit the mesh (Status, Neighbors), use Radix `forceMount` on the `<TabsContent>` plus `data-[state=inactive]:hidden`, and pass an `active={tab === "x"}` prop into the tab component. Inside, a `fetchedRef` guards the auto-fetch so it only fires the first time the tab becomes active. Manual refresh button always works. This deliberately avoids spamming the radio just because a user toggles tabs. Implementation in [`RepeaterDetailPage.tsx`](./web/frontend/src/pages/RepeaterDetailPage.tsx).

**Per-action busy state.** Any pair of buttons that share a busy flag (load/save, refresh/discover) should use a discriminated union (`"load" | "save" | null`). Only the spinner on the actually running button spins; both stay disabled. See `SectionFooter` and the Neighbors tab.

**Dropdowns over free text** for any setting with a finite valid set on the firmware side. The `SelectField` component in `RepeaterDetailPage.tsx` gracefully handles unknown device values by appending them as `"<value> (custom)"`. Currently used for:
- Bandwidth: 10 LoRa widths (7.8, 10.4, 15.6, 20.8, 31.25, 41.7, 62.5, 125, 250, 500 kHz).
- Spreading factor: SF5–SF12.
- Coding rate: 4/5–4/8.
- Repeat: on/off.
- Path hash mode: `0=1B / 1=2B / 3=4B` (firmware validator is `< 3` and these values are field-width selectors — do not "fix" to 0/1/2).
- Loop detect: off / minimal / moderate / strict.

**Message cache + delta backfill** (in [`CompanionDetailPage.tsx`](./web/frontend/src/pages/CompanionDetailPage.tsx)):
- `messageCacheRef: Map<channel, Message[]>`
- First open: full fetch via `?limit=100`. Cache populated.
- Re-open: render cache instantly, then fire `?afterId=lastId` and merge.
- WS messages always update both cache and active state via `mergeMessages` (dedup by `id`, fallback `timestamp+sender+text`).
- WS reconnect (`connected` false → true) triggers the same `afterId` backfill on the active channel.

**Threads list filters out repeaters.** `isRepeater` conversations (set by `routes_conversations.go` from contact metadata or peer type) are managed on the dedicated `RepeatersListPage` instead.

**Public is always pinned to the top** of the threads list regardless of sort mode (`recent` / `name` / `unread`).

**Initial scroll instant, subsequent smooth.** Conversations open with `behavior: "instant"` for the first scroll-to-bottom; later message arrivals use `smooth`.

**Reply asymmetry.**
- Replying to a **received** message: composer pre-fills with `@[sender] ` only.
- Replying to your **own** message: composer pre-fills with the message quoted using `> ` line prefixes.

**Message context menu** uses a `data-context-menu` attribute + `mousedown`-outside listener (NOT `click`/`scroll`) so the menu isn't killed by auto-scroll.

**Hover icons on messages.** Each bubble row reveals a Reply icon and a 3-dot context menu icon on hover, opposite the bubble. The 3-dot opens the same menu as right-click / long-press.

**Save-credentials toggle on login.** `RepeaterDetailPage` LoginCard has a Switch that, when checked, persists the password to contact metadata via `PATCH /api/companions/{name}/contacts/{pubkey}`. Toggling off clears any saved password. Pre-checked when a saved password is present. Blank passwords are allowed (some repeaters have no admin password set).

**`loggedIn` derivation.** `/session` returns either `{loggedIn:false}` or the bare session struct (no `loggedIn` field). The page treats presence of `pubkeyHex` as logged in. **Don't change this without changing `api/repeater.go` simultaneously** — the asymmetry is in the firmware response.

**Position picker.** The Settings → Position section embeds an interactive Leaflet map below the lat/lon inputs:
- Click anywhere → fills lat/lon (rounded to 6 decimals).
- Pin is draggable; release updates inputs.
- Manual entry into the inputs re-centers the map and moves the pin.

**CLI terminal autocomplete.** `<Tab>` opens / applies highlighted suggestion. `↑/↓` cycles suggestions when open (otherwise still cycles command history). `Esc` closes. `Enter` always submits. Suggestions only render when input is non-empty (otherwise they'd reappear after every submit). Catalogue is curated and split into top-level commands and config keys; both are ranked prefix-first then substring-match. No result cap. When the input starts with `get ` or `set `, the menu switches to the config keys list.

---

## Repeater Settings — real CLI command names

Settings sections each have their own load+save and use the **real** firmware command names (not invented combined ones):

| Section | Get | Set |
|---|---|---|
| Identity (name) | n/a | `set name <new>` |
| Radio | `get radio` (returns `freq,bw,sf,cr` comma-separated) + `get tx` | `set radio freq,bw,sf,cr` + `set tx N` |
| Position | `get lat` + `get lon` | `set lat <x>` + `set lon <x>` (also map picker) |
| Advert intervals | `get advert.interval` (mins) + `get flood.advert.interval` (hours) | matching `set …` |
| Routing | `get repeat` / `get path.hash.mode` / `get loop.detect` / `get flood.max` | matching `set …` (separate calls — semicolon chaining is NOT supported) |
| Owner Info | `get owner.info` (returns `|`-separated; convert to `\n` for display) | `set owner.info <encoded>` (convert `\n` back to `|`) |
| Security | `get guest.password` | `password <admin>` + `set guest.password <guest>` |

The `>` prompt prefix is stripped from CLI responses by `stripPromptPrefix`.

---

## QR Code format (official spec: docs.meshcore.io/qr_codes/)

- Channel: `meshcore://channel/add?name={name}&secret={32-hex-secret}`
- Contact: `meshcore://contact/add?name={name}&public_key={64-hex-pubkey}&type={1=companion|2=repeater|3=room|4=sensor}`

---

## Notes on the device / firmware

- The status payload has two distinct error counters — don't conflate them:
  - `errEvents` (uint16, bytes 40-42) — `_err_flags` bitmask of fatal events (queue-full, etc).
  - `recvErrors` (uint32, bytes 52-56) — `radio_driver.getPacketsRecvErrors()`, the actual receive-errors counter.
  Both are surfaced as separate StatTiles in the Status tab.
- Settings tab is **admin-only**. Logging in as guest hides it.
- Repeater sessions live in `RepeaterManager` memory; a binary restart drops them all and login is required again.
- Firmware does not support semicolon-chained commands. Sequential `set …` calls only.
- `freq` (set) and `prv.key` are serial-only commands — sending them via `/cli` yields an error.
- `radio.rxgain` is SX126x/LR1110 only. `bridge.*` are gated by compile-time flags. `pwrmgt.*` and `bootloader.ver` are NRF52-only.
- Advert intervals: direct is `3-240 minutes`, flood is `3-168 hours`. `flood.max` ≤ 64. `dutycycle` is `1-100` and writes `airtime_factor` indirectly.
- **Wrong password / unreachable repeater**: client sees a silent ~10 s timeout — the firmware drops the packet either way. The current UX doesn't distinguish the two cases.
- **Logout is local-only**: deletes the session from server memory, no radio traffic.
- `meshcore.PathHashSize = 1` (1 byte per hop hash). Direct neighbour = 0 hops.
- Firmware `get` responses are prefixed with `> ` (e.g., `> 917.375`).
- **Repeater login uses the companion's static identity**, not an ephemeral keypair, despite the protocol field being named `AnonReq.EphemeralPubKey`. This lets the repeater's ACL recognise the companion across sessions (blank-password reauth) and avoids orphan contact entries on the repeater. Internal field names in `RepeaterSession` are `localPubKey`/`sharedSecret` to reflect this.
- **Repeater ACL surface** (admin-only):
  - `REQ_TYPE_GET_ACCESS_LIST` (0x05) returns `(6-byte pubkey prefix, 1-byte permissions)` entries — *prefix only*, not full pubkey.
  - `setperm <full-hex-pubkey> <perms-int>` CLI command writes; perms=0 deletes.
  - Permission lower-2-bits: 0=GUEST (not persisted), 1=READ_ONLY, 2=READ_WRITE, 3=ADMIN.
  - MAX_CLIENTS = 20.
  - Resolve prefix→name in UI by cross-referencing both `discovered_peers` AND `companions` (the companion's own pubkey won't appear in its own peers list).
- **Telemetry permissions** are firmware-side ACL-gated:
  - Guest: base only (battery voltage + MCU temperature)
  - Admin: base + location + environment sensors (humidity, external temp, etc)
  The `inverse_perm_mask` byte in the request is set to `0x00` to ask for everything we're allowed; the firmware filters per the requester's role.

### Protocol constants

```
PayloadTypes: Req(0), Response(1), TxtMsg(2), Ack(3), Advert(4), GrpTxt(5),
              GrpData(6), AnonReq(7), Path(8), Trace(9), MultiPart(A),
              RawCustom(B), Control(C)

reqTypeGetStatus     = 0x01
reqTypeGetNeighbors  = 0x06
txtTypeCliData       = 1
cliPrefixLen         = 3
CLI flags byte       = TXT_TYPE_CLI_DATA << 2  (= 4)
PathHashSize         = 1 byte
```

---

## Path routing — data model

**OutPath semantics on `node.Peer`:**
- `nil` = path unknown (will send as flood)
- `[]byte{}` (non-nil, zero-length) = direct neighbor (0 hops, no routing needed)
- `[]byte{...}` = multi-hop path (send as direct-routed)

**OutPathHashSize** must be stored per-peer (1, 2, or 4 bytes per hop). Don't hardcode `meshcore.PathHashSize` for outbound routing — use `peer.OutPathHashSize` (0 = default/1).

**PathReturn plaintext format** (decrypted from a `PayloadTypePath` packet):
```
[pathLenByte:1][path_data:N][extra_type:1][extra_data:M]
pathLenByte: upper 2 bits = (hashSize - 1), lower 6 bits = hopCount
pathDataLen = hopCount * hashSize
extra_type: PayloadTypeResponse (0x01), PayloadTypeAck (0x03), or 0xFF (dummy/padding)
```

**Firmware sends PathReturn for ALL flood requests** (login, status, CLI, neighbors) — not just login. Path re-learning is automatic whenever a flood request reaches the repeater.

**Persistence:** `SetOutPath` only updates memory. Must explicitly call `store.Peers.UpdateOutPath(pubkey, path, hashSize)` via `WriteAsync` to persist across restarts.

---

## DM plaintext format

```
[timestamp:4][flags_byte:1][text:N][padding...]
flags_byte = (type << 2) | (attempt & 3)
```
- Use `plaintext[4] >> 2` to get type (0=plain, 1=CLI). Don't compare raw byte.
- `CalcAckHash` takes unpadded plaintext (trim trailing `\x00` from text) + sender's pubkey.
- `attemptByte` for BuildAckPayload is `plaintext[5+textLen+1]` (padding byte after text).

---

## Message delivery status

Messages have a `status` column: `NULL` (rx/legacy), `"sending"`, `"delivered"`, `"failed"`.
- WS action: `{ action: "status", companion, channel, id, status }`
- Retry endpoint: `POST /api/companions/{name}/messages/{messageId}/retry` — deletes failed msg, re-sends.

---

## Conventions / don'ts

- Don't reintroduce DaisyUI, PicoCSS, preact-router, `@preact/preset-vite`, lucide-preact.
- Don't run `vite dev`. Use the build+restart cycle.
- Don't add per-component `cursor` classes; rely on the global rule in `index.css`.
- Don't write `.message` wrapper into WS message handling.
- Don't change the `loggedIn` derivation in `RepeaterDetailPage.tsx` without coordinated firmware/API changes.
- Don't blanket-fix the `tracking-[0.1em]` → `tracking-widest` style hints; existing files use bracket notation throughout for consistency.
- Don't auto-refresh on tab switch — that spams the mesh radio. Use the fetch-once pattern.
- Add-contact modal (`AddContactDialog`) is manual-only (Type/Name/Pubkey) — add a *discovered* peer to contacts from the Peers screen (`PeerDetailSheet`), not via a peer-picker in the modal.

---

## Where to look first when debugging

| Symptom | Look at |
|---|---|
| WS message not appearing in UI | `useWebSocket.ts` parsing + the page's `handleWsMessage`. Note flat shape — no `.message` wrapper. |
| Repeater login appears successful but stays on login screen | `loggedIn` derivation in `RepeaterDetailPage.tsx`. |
| Settings load shows blank value | Real CLI command name — check it against `MeshCore/src/helpers/CommonCLI.cpp`. Don't assume the firmware has combined commands like `get coords`. |
| Threads list shows repeaters | Confirm `isRepeater` is set on the conversation row — set by `routes_conversations.go` from contact metadata or peer type. |
| Map marker icon broken | Default-icon `mergeOptions` at top of `RepeaterDetailPage.tsx`; Vite needs explicit imports of leaflet's marker images. |
| "Works on Owly 1 but not Owly 2" or vice versa | They have different admin passwords. Settings tab is admin-only; logging in as guest hides Settings. |
| Bundle warning > 500 kB | Known. Code-splitting routes via `React.lazy` would help; not done yet. |
| Style hint warnings (`tracking-[0.1em]` etc) | Non-blocking. Don't auto-fix wholesale. |

---

## Test repeater

- Name: Owly 2
- Pubkey: `b8b950572f212cba178ab74a5b6f217460e5d34835361ee2b342c1a86628e492`
- Password: *(redacted — ask the maintainer)*
- Companion name: `🐶Wes Test - DELETE`
- Server port: 4432
- Direct neighbour (0 hops)

---

## Known limitations / tech debt

- Routes are code-split (per-page chunks) but the main `index` chunk is still >500 kB minified — Vite's warning remains unactioned.
- Status / Neighbors auto-fetch is **once per page mount**. After a long absence the user must hit Refresh.
- Repeater login state is in-memory on the Go side — restart drops sessions.
- Settings-section CLI `get`s run sequentially on purpose (half-duplex radio; parallel requests compete for airtime). The Go client *could* correlate concurrent commands — it matches responses by a random per-command prefix — so this is a deliberate airtime trade-off, not a limitation of the protocol layer.
- No automated Go tests yet — the package boundaries now make the domain unit-testable (e.g. `internal/config` validation/round-trip, `internal/repeater` parsers); the compiler + manual testing on Owly 2 are the only safety net today.
- No automated tests for the frontend; Playwright is ad-hoc against the running binary.
- Login timeout is identical for "wrong password" vs "unreachable" — would be nice to differentiate.
- **MQTT packet SNR format**: [`internal/mqtt/format.go`](./internal/mqtt/format.go) — `pkt.SNR` (real-dB float32) was formatted with `%d`, emitting literal `%!d(float32=…)` to the feed and failing `go vet`/CI. Now `%.2f` (a sane default that unbreaks CI); **confirm the exact representation the LetsMesh/CoreScope schema expects** (integer? quarter-dB?) and adjust if needed.

---

## Feature ideas not yet started

- Telemetry display: CayenneLPP sensor data (`meshcore-go` has full decoder for 25+ sensor types).
- Repeater firmware update: OTA via CLI (`start ota` exists).
- Multi-repeater management: batch operations across multiple repeaters.
- Message retry indicator: send/retry/fail status on chat messages.
