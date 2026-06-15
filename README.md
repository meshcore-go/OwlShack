# OwlShack

An operator console for [MeshCore](https://github.com/meshcore-dev/MeshCore) mesh networks, packaged as a single static Go binary. It runs companion nodes on the mesh, observes and archives traffic, administers remote repeaters, talks to room servers, polls nodes for telemetry, and serves a live web UI for all of it. Built on the pure Go [meshcore-go](https://github.com/meshcore-go/meshcore-go) library (no CGO).

> **Note:** OwlShack is the project's new name. The repository, release binaries and Docker image are still published as `meshcore-bot` for now, so the commands below use that name.

## What it is

Plug a MeshCore radio into any Linux, macOS or Windows machine (a Raspberry Pi is plenty), point a browser at `http://localhost:8080`, and you get a full operator console for the mesh:

- **Companion chat**: run one or more companion identities that send and receive on public and private channels, hold DM conversations, and auto-respond via triggers.
- **Network observer**: every packet, peer and advert seen on the air is decoded, streamed live, mapped, and persisted to SQLite.
- **Repeater administration**: log in to remote repeaters and drive them (status telemetry, a CLI terminal, neighbours, full settings, access control).
- **Room servers**: join server-hosted chat rooms, post, and receive backlog.
- **Node monitoring**: poll repeaters and companions on a schedule and chart their telemetry over time.
- **MQTT bridge**: forward observed traffic to aggregators like LetsMesh and CoreScope.

Configuration lives in the database and is managed from the web UI. A config file is only ever a one-time import.

## Features

**Web operator console** (served on `:8080` by default, single binary, SPA embedded)
- Overview dashboard, searchable peers table, live Leaflet map, live packet stream, interactive path tracer.
- Per-companion chat client (channels and DMs), contacts and channel management.
- Repeater detail: login, status tiles, CLI terminal with autocomplete, neighbours, and a full settings panel (radio, position with map picker, advert intervals, routing, owner info, security).
- Node monitoring views with telemetry history charts.
- In-app config editors for companions/bots, MQTT, and radio, all backed by the same stored config.

**Companion nodes**
- Group and cron triggers with Go `text/template` responses and regex matching.
- Public and private channels, DM conversations, message delivery status and retry.
- Inline ed25519 identity per companion (generated automatically if you omit it).

**Repeater client** (drive remote repeaters over the air)
- Login, status request, CLI passthrough, path learning, neighbours, telemetry, and ACL (`setperm`, access list).

**Room server client**
- Login, post, ACK-gated backlog sync, and author resolution.

**Operations**
- SQLite persistence (peers, contacts, channels, messages, packets, conversations, telemetry).
- Database is the source of truth for config; `SIGHUP` reloads it with minimal disruption (only changed companions restart).
- Multi-format config import (TOML, YAML, JSON).
- Static binary, no CGO, runs anywhere Go cross-compiles.

## The web console

Once running, open `http://localhost:8080`.

| Section | What it does |
|---------|--------------|
| Overview | Live peer count, peer-type spectrum, recently seen list, companions roster |
| Peers | Searchable, sortable table with type pills, signal bars, last-seen |
| Map | Leaflet map of geolocated peers, filterable by type |
| Packets | Live packet stream grouped by hash, with a detail panel (route, hops, signal, raw hex) |
| Trace | Interactive path builder and result timeline |
| Monitoring | Polled nodes with status and telemetry history charts |
| Bots | Trigger CRUD across companions (group and cron) |
| MQTT | Top-level MQTT node selector and broker management |
| Radio | Connection, baud, RF parameters, listen address, log level |
| Companions | Companion roster, plus per-companion chat, contacts, channels, repeaters |

## Installation

### Download a release binary (recommended)

Pre-built binaries for Linux, macOS, and Windows are on the [Releases](https://github.com/meshcore-go/meshcore-bot/releases) page.

1. Grab the [latest release](https://github.com/meshcore-go/meshcore-bot/releases/latest).
2. Download the binary for your platform (for example `meshcore-bot-linux-arm64` for a Raspberry Pi).
3. Make it executable and move it onto your `PATH`:

```bash
chmod +x meshcore-bot-linux-arm64
sudo mv meshcore-bot-linux-arm64 /usr/local/bin/meshcore-bot
```

### Docker

Images are published to `ghcr.io/meshcore-go/meshcore-bot` for `linux/386`, `linux/amd64`, `linux/arm/v6`, `linux/arm/v7`, `linux/arm64/v8`, `linux/ppc64le`, `linux/riscv64`, and `linux/s390x`.

```bash
docker pull ghcr.io/meshcore-go/meshcore-bot:latest
```

### Build from source

Requires Go 1.26+. The frontend is built separately and embedded into the binary.

```bash
git clone https://github.com/meshcore-go/meshcore-bot.git
cd meshcore-bot
(cd web/frontend && npm install && npm run build)   # builds the SPA into web/frontend/dist
go build -o meshcore-bot .
```

## Quick start

### 1. Connect your radio

Plug a MeshCore radio into your machine via USB. On Linux it usually appears as `/dev/ttyACM0`; on macOS as `/dev/cu.usbmodem*`.

On Linux, give your user access to serial devices, then log out and back in:

```bash
sudo usermod -a -G dialout $USER
```

### 2. Run it

```bash
./meshcore-bot -vvv
```

On first run with no stored config, OwlShack starts quietly with no companion — it observes the mesh but advertises nothing — and the web console presents a first-run setup wizard that walks you through radio settings and creating your first companion. If a `config.toml` (or `.yaml` / `.json`) is present in the working directory, it is imported automatically instead (and the wizard is skipped). To import an explicit file and overwrite the stored config:

```bash
./meshcore-bot -config myconfig.toml
```

### 3. Open the console

Visit `http://localhost:8080`. From here you can configure radio settings, add companions and channels, set up triggers, manage repeaters, and watch the mesh live. After the first run you rarely need the config file again: the web UI writes everything back to the database.

### Using Docker

Mount a config file for the initial import and pass through the serial device:

```bash
docker run -d \
  --device /dev/ttyACM0 \
  -p 8080:8080 \
  -v ./config.toml:/data/config.toml \
  ghcr.io/meshcore-go/meshcore-bot
```

For a TCP radio connection (a serial-to-TCP bridge), drop `--device`:

```bash
docker run -d \
  -p 8080:8080 \
  -v ./config.toml:/data/config.toml \
  ghcr.io/meshcore-go/meshcore-bot
```

## Configuration

**The database is the source of truth.** Config is stored relationally in `meshcore.db`. A config file is read only to seed or overwrite that stored config:

- First run with an empty database imports a `config.toml` / `.yaml` / `.json` from the working directory, or bootstraps a quiet default (no companions) and shows the first-run wizard.
- `-config <path>` imports the given file and overwrites the stored config.
- After that, the web UI is the way to change settings, and `SIGHUP` reloads from the database.

Config files from older releases import automatically: the legacy `nodeType` key, `[[bot]]` blocks, and `[[observer]]` MQTT sections are mapped onto the current `connectionType` / `[[companion]]` / `[mqtt]` layout.

A minimal import file:

```toml
# Connection (KISS modem)
connection = "serial:///dev/ttyACM0"   # or "tcp://host:port"
baudRate = 115200

# Radio
freq = 917.375
bw = 62.50
sf = 7
cr = 8
tx = 22

[[companion]]
name = "Ping Bot"
# privateKey = "<64 hex chars>"   # ed25519 seed; omit to generate one
latitude = 0.0
longitude = 0.0
advertInterval = 86400            # seconds; 0 = never advertise
channels = ["#testing", "Public"]

[[companion.trigger]]
type = "group"
template = "Pong! Hello {{.Sender}}"
channels = ["#testing"]
match = ["(?i)^ping"]
```

### Connection and radio

| Field | Description | Default |
|-------|-------------|---------|
| `connection` | `serial:///dev/ttyACM0` or `tcp://host:port` | `serial:///dev/ttyACM0` |
| `baudRate` | Serial baud rate | `115200` |
| `freq` | Frequency in MHz | `917.375` |
| `bw` | Bandwidth in kHz | `62.50` |
| `sf` | Spreading factor | `7` |
| `cr` | Coding rate | `8` |
| `tx` | TX power | `22` |
| `logLevel` | `debug`, `info`, `warn`, `error`, `trace` (overridden by `-v` flags) | `info` |

### Companions

Each `[[companion]]` is a node identity that OwlShack runs on the mesh.

| Field | Description |
|-------|-------------|
| `name` | Display name shown on the mesh and in adverts (also the storage key for this companion's history) |
| `privateKey` | 64-hex ed25519 seed; omit to have one generated and stored |
| `latitude` / `longitude` | Advertised position (decimal degrees) |
| `advertInterval` | Seconds between adverts; `0` = never, omit for the default |
| `channels` | Channels this companion joins (see below) |
| `trigger` | Array of triggers (see below) |

### Triggers

Each `[[companion.trigger]]` has a `type` and a `template`.

| Field | Description |
|-------|-------------|
| `type` | `group` (channel messages; `channel` is accepted as an alias) or `cron` |
| `template` | Go `text/template` for the response or message |
| `channels` | Channels to listen on / send to |
| `match` | Array of [Go regular expressions](https://pkg.go.dev/regexp/syntax) matched against incoming messages (group triggers) |
| `schedule` | Cron expression, for example `"*/5 * * * *"` (cron triggers) |
| `contacts` | Contact names to listen to for DMs |
| `retryTimeout` | Seconds to wait for a repeater echo before retrying (default `5`) |
| `maxRetries` | Maximum send retries (default `3`) |
| `charLimitBehaviour` | `truncate` or `split` when a message exceeds the character limit |
| `pathHashSize` | `0` = copy sender's setting, `1`/`2`/`3` = bytes per hash (default `1`) |

After sending, the companion listens for the message to be echoed back by a repeater. If no echo arrives within `retryTimeout`, it re-sends, up to `maxRetries` times. This applies to both group and cron triggers.

### Channels

`channels` accepts plain names for public/hashtag channels, or objects with a `privateKey` for private channels:

```toml
# Public channels: just the name
channels = ["#general", "#testing"]

# Private channels: use the table syntax
[[companion.trigger.channels]]
name = "Secret Ops"
privateKey = "a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6"

[[companion.trigger.channels]]
name = "#general"
```

### Template variables

**Group / channel triggers:**

| Variable | Description |
|----------|-------------|
| `{{.Sender}}` | Sender's node name |
| `{{.Channel}}` | Channel name |
| `{{.Message}}` | Original message text |
| `{{.Match}}` | Map of named regex capture groups |
| `{{.Timestamp}}` | Message timestamp |
| `{{.SNR}}` | Signal-to-noise ratio (dB) |
| `{{.RSSI}}` | Received signal strength (dBm) |
| `{{.Hops}}` | Number of hops |
| `{{.PathHashes}}` | Raw path hashes |
| `{{.PathHashSize}}` | Bytes per path hash |

**Cron triggers:** `{{.Time}}` (current time) and `{{.Schedule}}` (the schedule string). `{{.BotName}}` is available in every template.

**Built-in functions:** `formatPathBytes` renders raw path hashes into a readable string (returns `Direct` when there is no path).

## MQTT integration

OwlShack can publish observed mesh traffic to MQTT brokers, used by services like [LetsMesh](https://letsmesh.net) and [CoreScope](https://github.com/Kpa-clawbot/CoreScope) to aggregate network data.

MQTT is configured at the top level. Exactly one companion feeds the bridge, selected by `node` (empty = the first companion).

```toml
[mqtt]
node = "Ping Bot"
iataCode = "AKL"
# statusInterval = 300
# owner = "callsign"
# email = "you@example.com"

[[mqtt.broker]]
name = "letsmesh-us"
enabled = true
transport = "websockets"          # "websockets" or "tcp"
host = "mqtt-us-v1.letsmesh.net"
port = 443
tlsEnabled = true
authType = "token"                # "token", "basic", or "none"
audience = "mqtt-us-v1.letsmesh.net"
packetTopic = "meshcore/{IATA}/{PUBLIC_KEY}/packets"
statusTopic = "meshcore/{IATA}/{PUBLIC_KEY}/status"
```

| MQTT field | Description |
|------------|-------------|
| `node` | Companion that feeds the bridge (empty = first) |
| `enabled` | Enable the bridge (omit = enabled) |
| `iataCode` | Location identifier (for example an airport code) |
| `statusInterval` | Seconds between status publishes (default `300`) |
| `owner` / `email` | Included in MQTT token claims (optional) |

| Broker field | Description |
|--------------|-------------|
| `name` | Display name |
| `enabled` | Enable this broker |
| `dedup` | Per-broker packet deduplication |
| `transport` | `websockets` or `tcp` |
| `host` / `port` | Broker endpoint |
| `path` | WebSocket path (default `/`) |
| `packetTopic` / `statusTopic` | Topic templates; placeholders `{iata}` `{pubkey}` `{name}` (uppercase `{IATA}` `{PUBLIC_KEY}` also resolve). Empty = the default `meshcore/{iata}/{pubkey}/<kind>` |
| `disallowedPacketTypes` | Packet types to exclude (for example `["ack", "advert"]`) |
| `retainStatus` | Retain status messages on the broker |
| `tlsEnabled` / `tlsInsecure` | Enable TLS / skip certificate verification |
| `authType` | `token` (Ed25519 JWT from the node identity), `basic`, or `none` |
| `username` / `password` | Credentials for `basic` auth |
| `audience` | Token audience (for `token` auth) |

## CLI flags

| Flag | Description |
|------|-------------|
| `-c, --config PATH` | Import a config file (TOML, YAML, or JSON) into the database and run with it |
| `-V, --version` | Print version and exit |
| `-v, --verbose` | Increase log verbosity (`-v` debug, `-vv` / `-vvv` trace) |

## Environment variables

The web UI binds to `:8080` by default. Two environment variables override the stored listen address at startup (precedence: env > stored config > default), which is handy for Docker and PaaS deployments:

| Variable | Description |
|----------|-------------|
| `HOST` | Bind host (e.g. `0.0.0.0` or `127.0.0.1`). Unset = all interfaces. |
| `PORT` | Bind port (e.g. `4432`). Unset = the stored/default port. |

Either may be set alone, for example `PORT=4432 ./meshcore-bot`.

## Hot reload

Send `SIGHUP` to reload configuration from the database without a full restart:

```bash
kill -SIGHUP $(pgrep meshcore-bot)
```

Reloads are diff-based: only companions whose effective config changed are restarted, so unchanged ones keep their sessions. A radio or connection change reconnects the modem; changing the listen address needs a process restart.

## License

See [LICENSE](LICENSE).
