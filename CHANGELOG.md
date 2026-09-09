# Changelog

Notable changes per release. Dates are the tag date; unreleased work sits at the
top until tagged.

## v1.3.2

Corrects two fields on the MQTT status schema that carried a different measurement from the one
their name promises. The schema is shared: `meshcoretomqtt` forwards real firmware nodes to the
same brokers under the same topic, and it is the client LetsMesh recommends, so the names were
already defined by the firmware and OwlShack was the one publishing something else into them.

Baseline `v1.3.1` · no schema change

### Upgrading

- **`recv_errors` changes meaning.** It now carries radio-driver receive failures, matching the
  firmware's own field (`driver.getPacketsRecvErrors()`, which `RadioLibWrapper::recvRaw`
  increments when `readData` fails after the interrupt). It was carrying packet parse failures.
  On a KISS modem it is now **0**: the TNC does not expose its driver counters over telemetry
  frames, only through the CLI stats reply.
- **`packet_parse_errors` is new** and carries what `recv_errors` used to: bytes that arrived
  intact and did not decode as a MeshCore packet. The radio did its job; something else sent
  malformed bytes.
- **A dashboard keyed on `recv_errors` will see OwlShack nodes change**, in most cases dropping to
  0 on KISS. Nothing errors and no key disappears, so the change is invisible on the wire. Read
  `client_version` if you need to tell the versions apart.
- No database migration; `user_version` stays 11.

### Fixed

- **`recv_errors` published a parse failure under a name the firmware had already defined** as a
  radio-driver counter. A consumer aggregating across firmware nodes and OwlShack nodes was
  summing two unrelated measurements, with no way to tell them apart.
- **`hw_decode_errors` published an SPI data-packet readout failure** on the SPI path.
  `PacketsRecvErrors` was mapped into it, but that field is a malformed KISS **SETHARDWARE** frame,
  which is the battery, MCU-temperature and noise-floor channel rather than a mesh packet. It is
  now KISS-only, publishing 0 on SPI like the other four KISS counters, and `PacketsRecvErrors`
  goes to `recv_errors` where it belongs.
- **A driver error incremented two counters.** On the SPI path the modem error handler fed both
  `driver_errors` and the parse count, so `driver_errors` was a strict subset of `recv_errors` and
  the same event was counted twice. `recv_errors` also meant different things on the two
  transports: parse failures on KISS, parse plus driver failures on SPI.
- `GET /api/radio/status` omits `hwDecodeErrors` on SPI rather than reporting a 0 it never
  measured, and gains `recvErrors` on SPI. The MQTT payload keeps publishing both keys on both
  transports, because omitting a key there is a coordinated change and dropping to 0 is not.

### Known limitations

- The rename is invisible to a consumer that does not parse `client_version`. Whoever runs
  CoreScope or LetsMesh is better told than left to notice.
- `packet_parse_errors` has not been observed non-zero on the bench. Read a 0 there as
  "nothing malformed arrived, or the path is not exercised", not as proof the counter works.

## v1.3.1

**Security fix.** A repeater created through OwlShack had no admin password, and a blank one
compared equal to the blank a login request sends, so any node in radio range could log in as
admin: change settings, set a password and lock the operator out, read the access list, run CLI
commands. Upgrade if you run a repeater.

Baseline `v1.3.0` · schema `user_version` 10 → 11

### Upgrading

- **The database migrates itself** on first start. A blank repeater admin password becomes
  `password`, the same default the firmware ships (`ADMIN_PASSWORD` in `simple_repeater`), rather
  than an invented value an operator could not guess.
- **Change it.** `password` is the well-known firmware default, so it closes the "no password at
  all" hole without being a secret. Set your own on the Repeater page, or over CLI with
  `password <new>`.
- **A repeater with a password already set is untouched** by the migration.
- **Creating a repeater now requires an admin password.** `POST /api/config/repeater` rejects a
  missing or blank one, and the setup form will not submit without it.

### Fixed

- **A blank repeater admin password granted admin to any node in range.** `CreateRepeater` never
  set one and the column defaults to `''`, so `authLogin` matched the blank password a login
  carries against the blank stored one and returned `permAdmin`. The auth logic itself mirrors the
  firmware faithfully, including the fall-through when a blank-password sender is not in the ACL;
  the firmware is not exposed because it never stores a blank password. Three paths could leave one
  blank and all three now refuse: create (required field), `PUT /api/config/repeater/admin`
  (sending `""` cleared it), and the `password` CLI command. The guest password is deliberately
  still clearable — a blank guest password grants `PERM_ACL_GUEST`, which is 0, and is what the
  firmware does.
- **Migration slot 10 was not covered by the frozen-slots test.** `TestMigrations_ShippedSlotsFrozen`
  pinned v1.1.0 and v1.2.0 only, so the slot v1.3.0 shipped could have been edited or renumbered
  without failing anything. Pinned now, along with slot 11.

### Known limitations

- Setting an admin password longer than 15 characters is accepted but cannot be typed by a firmware
  client, which truncates to that length. Validate or truncate at the boundary in a later release.
- No authentication on the REST API or web UI, so anyone who can reach the port can still read and
  change the repeater's password directly.

## v1.3.0

Drive a bare SX12xx LoRa chip directly on the host SPI bus: no companion MCU, no
KISS firmware in between. Plus zero-hop node discovery that no longer needs a
repeater personality, a radio-health endpoint, a dead-receiver watchdog, and an
MQTT layer that no longer blocks startup on brokers that are not there.

Baseline `v1.2.0` · schema `user_version` 9 → 10 · `meshcore-go` v1.4.0 · Go 1.26+

### Upgrading

- **The database migrates itself** on first start, adding `settings.spi_board`.
  Existing installs get `NULL`, which means "KISS modem" — the previous
  behaviour. No manual SQL.
- **Absent is not zero.** Counters a transport cannot measure are now *omitted*
  from `GET /api/radio/status` instead of published as `0`. A KISS modem has no
  chip-level CRC count; an SPI radio has no KISS framing errors. Read a missing
  key as "not measurable" and `0` as "measured none" — a consumer that treats
  absent as zero will report a healthy radio for one that cannot answer. On the
  SPI path `inboundDroppedOldest`, `rxMetaTimeouts`, `rxMetaMisattributed`,
  `hwErrors` and `txOutcomeLost` are the newly omitted keys, alongside
  `batteryMv` and `mcuTempC`.
- **The MQTT status schema is unchanged.** Those same counters still publish `0`
  there, because that payload is shared with meshcore-bot and CoreScope; making
  it omit them is a coordinated change, not a local one.
- **The first MQTT `online` status arrives slightly later**, once the broker
  connection completes, rather than during startup. Same message and topic; no
  longer ordered before the node starts serving.

### Added

- **SPI radio support.** A `spi://` connection scheme drives the LoRa chip itself
  — SX1262 and SX127x — over the host's SPI bus and GPIO lines.
- **Board registry** of 21 definitions, as JSON rather than buried in Go,
  selectable in Settings and the setup wizard and served at
  `GET /api/spi/boards`. Two are verified against hardware; the other 19 are
  transcribed from vendor documentation and marked as such, because a wrong
  `RESET` pin looks exactly like a dead radio.
- **`GET /api/radio/status`** reports the modem's link counters: frames dropped,
  decode and hardware errors, slow-handler stalls, and on the SPI path the
  chip's own packet totals, CRC errors, driver faults and receiver recoveries.
  On a hat with no battery and no MCU temperature sensor these are the only
  health signal the radio has.
- **Dead-receiver watchdog.** An SX126x can be left deaf by a failed
  `ResumeReceive` while every other indicator still reads healthy. The driver
  now reports a stuck receiver, OwlShack reconnects the modem, and each recovery
  is counted so a flapping radio is visible rather than merely quiet.
- **`meshcore-go` v1.4.0**, with `hardware/transport` and the new
  `hardware/sx12xx` at the same tag. Activity-LED blinking moved into the
  library's chip drivers, where every consumer gets it.
- **Date and time in bot templates.** `now` gives the current time and `date`
  formats one in any Go layout, with an optional IANA zone:
  `{{date now "Mon 3:04PM" "Pacific/Auckland"}}`. `date` also reads the raw
  unix seconds a group trigger's `{{.Timestamp}}` arrives as, which previously
  rendered as a bare number and had no way to be formatted. The zone database
  is compiled in, so a named zone resolves identically on every release target
  rather than only where the host ships zoneinfo. Omitting the zone uses the
  process's local time, which in a container is UTC until `TZ` is set: the
  README now documents that, and a mistyped `TZ` falls back to UTC silently.
- **A new owl mark** replaces the radio glyph in the sidebar, the favicon and
  every PWA icon. An installed app picks up the new icon on its next launch.
  The maskable and iOS icons are opaque on purpose: Android crops the maskable
  to a circle, and iOS renders transparency in an `apple-touch-icon` as black.
- **Zero-hop node discovery**, on a Discover page and at `POST /api/discover` /
  `GET /api/discover`, with each answer also broadcast on the new `discovered`
  websocket topic. It asks every repeater and sensor in direct radio range to
  answer and waits 30 s, because responders stagger their replies by a widened
  random delay — an empty list before the window closes means "not yet", not
  "nothing there". Results show as a list or on a map with a link line per
  responder, and each known node opens the standard peer detail panel.
  Two properties are worth stating plainly. It works from *whichever* node is
  running: the firmware's request carries no sender identity, so finding what a
  radio can hear no longer requires running a repeater. And every link is
  reported in both directions — the SNR we measured on the reply beside the SNR
  the responder reported for our request — because a link limited by our receive
  looks identical to a healthy one if you only print a single number.
  Only repeaters and sensors are offered. `simple_room_server` has no
  `onControlDataRecv` override and `companion_radio` forwards the frame to its
  phone app without ever replying, so listing those types would return an empty
  result that reads as "none in range".

### Fixed

- **A radio that will not start no longer takes the web UI down with it.** `modem.Setup` ran before
  the HTTP server and its failure was fatal, so a node whose modem had moved, been unplugged or been
  swapped for a different board exited before serving the page that would have fixed it — and the
  log said "complete setup in the web UI" while guaranteeing there was none. Recovery meant a shell,
  `sqlite3`, or deleting the database. The server now starts first; a radio that cannot open is
  reported, retried with capped backoff, and corrected from Settings, which reconnects it in place.
  `GET /api/radio/status` answers `503` while there is no modem rather than a page of zeroes. This
  also covers a first run on an SPI host, where the bootstrapped default is a KISS modem on
  `/dev/ttyACM0` that does not exist — previously the setup wizard could not be reached at all.
- **MQTT startup no longer waits for absent brokers.** Connections were dialled
  in line, serially, with a 10 s timeout each — on boot and again on every
  `SIGHUP` reload. Three unreachable brokers cost 30 s before the mesh node
  started. The dial now runs in the background; the first connect failure is
  still logged at error level so an unreachable broker stays visible.
- **Token refresh no longer drops publishes.** Refreshing a broker token
  disconnected the old client before dialling the new one, leaving nothing to
  publish through for the whole dial — up to 10 s, every 8 minutes, per token
  broker. The new client is established first and the old one dropped
  immediately after the swap.
- **A failed token refresh now retries.** When the reconnect failed, the retry
  loop found the stale client still reporting connected and returned without
  dialling; the broker stayed dead until the next refresh tick, by which point
  its token had expired. The stale client is dropped on the failure path, which
  is what lets the retry work.
- **A KISS modem that goes quiet without erroring is now detected.** A serial
  read timeout returns `(0, nil)` rather than an error, so a device that stays
  enumerated but stops talking — a USB autosuspend that never resumes, wedged
  firmware, a stalled hypervisor passthrough — left the read loop spinning every
  100 ms with nothing to report. No error, no reconnect, no log line: the node
  looked healthy and simply never heard another packet. A liveness probe now
  asks the modem for its status every 30 s and reconnects after three
  consecutive unanswered probes. Inbound silence is deliberately *not* the
  trigger — a quiet mesh is normal — and the probe stays disarmed until the
  modem has answered at least once, so firmware that does not implement the
  status queries is never reconnected in a loop.
- **Board readings are no longer published after the modem stops answering.**
  The reply flags were sticky, so once the serial port went away the status
  payload kept carrying the last battery voltage and MCU temperature — a user
  log showed `battery_mv=4148` and `mcu_temp_c=20.5` being published while every
  query was returning `write frame: input/output error` and the port was closed.
  A reading now drops out of the payload if the modem has not answered for 45 s.
- **Five counters no longer publish `0` on an SPI node.** Queue-full-oldest,
  the two signal-metadata pairing counts, hardware errors and lost TX outcomes
  are KISS framing concepts with no analogue on the SPI path, so they read as a
  radio measuring them and finding nothing wrong. They are now absent there and
  render as `—`. Decode errors, queue-full-new and handler-slow *are* measured
  on both paths and keep their numbers.
- **The SPI radio's slow-handler count** was read from the wrong source and
  always reported `0`, which looks like a modem keeping up comfortably.
- **Battery and MCU temperature no longer publish `0`** on hats that have
  neither, which reported a flat cell and a freezing board.
- **Signal-strength bars now line up down a column.** The dB label had no fixed
  width, so the bar icon beside it shifted with the digit count — `0.0dB` is a
  character shorter than `-6.3dB`. The rows knocked out of alignment were the
  ones reading near zero, which is where the eye goes anyway.
- **The repeater's Discover button now waits 30 s between requests.** Firing
  again while the first request is still being answered only adds contention,
  and the firmware allows each repeater four discovery responses every two
  minutes (`discover_limiter(4, 120)`); past that it stays silent, which looks
  exactly like it dropping out of radio range.

### Verified on hardware

| Board | Host | Build | Radio |
|---|---|---|---|
| Zindello UltraPeaterZero (E22P, 1 W) | Raspberry Pi 4 | `linux/arm64` | SF7 · 62.5 kHz · 22 dBm |
| Zindello UltraPeaterZero (E22, 1 W) | Raspberry Pi Zero W | `linux/arm` v6 | SF7 · 62.5 kHz · 22 dBm |

Both nodes transmit and receive each other's traffic, which is the only real
proof the external PA radiates — `TX_DONE` fires whether or not anything leaves
the antenna. Reset recovery was exercised by pulling the chip's `NRST` line and
watching the receiver re-arm.

Discovery was exercised on air against four repeaters. The scan was run with the
repeater personality deliberately out of the path, so the answers prove the
feature does what it claims: three to four repeaters replied to a companion-only
scan, within about three seconds each on SF7. One link came back asymmetric by
roughly 15 dB, which is the case the two SNR columns exist for. A sensors-only
scan returned nothing while those same repeaters were still answering repeater
scans, which is the type filter proven over RF rather than only in a test.

### Internal

- Comments cut to one line or none across 174 files, a net reduction of ~2,400
  lines. A comment that restates its code is a second thing to keep true.
- New tests for the non-blocking startup, the refresh failure path, the
  dead-radio watcher and one-instant status sampling, each mutation-checked by
  reintroducing the bug and confirming the test fails.
- The suite runs clean under `-race -shuffle=on`, which is what exposed retry
  goroutines outliving their tests and racing across test boundaries.
- README rewritten around what the app now does, with screenshots and a
  supported-hardware section whose figures come from `GET /api/spi/boards`
  rather than a hand count.
- Documentation corrections: the MQTT bridge exists as two forks with different
  field sets, so references now name which one; and one reference doc claimed a
  shipped feature was still missing.

### Known limitations

- **No authentication on the REST API or web UI** — the largest real gap for a
  tool that can reconfigure a repeater. Do not expose it to an untrusted
  network.
- Repeater and room sessions live in memory, so a restart drops every login.
- Of 21 SPI board definitions, two are verified on hardware and ten are
  untested presets. The other nine cannot be selected at all: seven need a
  non-default `gpiochip` the GPIO library cannot address, and two have no
  confirmed RF-switch control.
- No autostart unit ships with the binary; a host that reboots does not bring
  its node back.
- A companion or repeater that fails to *restart* on a config reload is still
  fatal; only the radio's own failure is now survivable.
- **Sensor discovery is unproven on air.** The type filter is verified in both
  directions, but no sensor was within radio range of the test bench, so a
  sensor actually answering has only been read in the firmware source.
- A discovery response carries no position, so the Discover map can only place
  nodes whose advert is already on record. The page reports how many it could
  not place rather than quietly showing fewer nodes than the list.
- A node that answers discovery but has never been heard advertising has no
  peer record, so it cannot be opened or mapped. Those rows read `never` and are
  not clickable. This case has not been seen on the bench.

## v1.2.0

Repeater, sensor and room parity with firmware 1.17.1; backup and restore; MQTT
observability. CI extended to run checks on pull requests into `dev` as well as
`main`, and `CLAUDE.md` split into a lean map plus `docs/` reference.

Schema `user_version` 9.
