# MeshCore Official App — Feature Reference

Documented from the official MeshCore web app (Flutter) at https://app.meshcore.nz/

---

## Navigation Structure

- **Bottom tabs**: Contacts, Channels, Map
- **Top bar**: Battery %, device name, broadcast icon, settings gear, three-dot menu

---

## Contacts Tab

### Contact List
- Shows all discovered peers (contacts)
- Each entry: icon (repeater/initials avatar), name, truncated pubkey, last seen time, route (Flood / N hops)
- Search bar: "Search N Contacts..."
- Filter button (right side) opens panel

### Filter/Sort Panel
**Order:**
- A-Z
- Heard Recently (default)
- Latest Messages

**Filter:**
- All (default)
- Favourites
- Users
- Repeaters
- Room Servers
- Sensors

### Contact Three-Dot Menu
- Details
- Share
- Set Path
- Reset Path
- Ping (Zero Hop)
- Remove Contact
- Favourite (checkbox)

### Contact Detail Page
**Header:** Avatar, name, truncated pubkey, quick action buttons (Manage, Favourite, Telemetry, Share)

**Info Section:**
- Name (editable, pencil icon)
- Public Key (full, copy button)
- Position: lat/lng (with map pin button)
- Distance Away: km / miles
- Contact Type: Repeater/Chat/Room/Sensor

**Last Advert Heard:**
- Seconds ago + absolute datetime

**Path Section:**
- Hops Away: Flood (with X to reset)
- Out Path (editable, pencil icon)
- Out Path Hash Size (info icon)

**Extra Tools:**
- Favourite (checkbox)
- Ping (Zero Hop)
- Permissions
- Remote Management
- View on Map
- View Telemetry
- Share
- Remove Contact

### Tapping a Contact
- If type is User/Chat: opens DM chat view
- If type is Repeater: opens Repeater Login page

---

## Channels Tab

### Channel List
- Shows all channels (16/40 max)
- Each entry: icon (broadcast icon or # icon), channel name, last message preview
- Search bar: "Search N Channels..."
- Filter button (same Order/Filter panel as Contacts)
- Three-dot menu per channel

### Channel List Three-Dot Menu
- Share
- Rename
- Remove Channel
- Notifications (All Messages / current setting shown)

### Channel Chat View
**Header:** Back arrow, channel icon, channel name, "Channel Messages" subtitle, three-dot menu

**Chat Area:**
- Messages displayed with sender name, message text, timestamp
- Empty state: "No Messages — Be the first to say hello!"

**Compose Bar:**
- "+" button (left side, for attachments/actions)
- "Send a message..." text input
- Send arrow button (right side)
- Character counter: 0/137 (bottom-right)

### Channel Chat Three-Dot Menu
- Search
- Share
- Rename
- Participants
- Blocked Senders
- Notifications (All Messages)
- Set Region Scope
- Delete Message History

---

## Map Tab

- Full-screen map with OSM tiles
- Peer markers with labels (name + time since seen)
- Cluster markers (green circle with count)
- Search bar: "Search N Nodes..."
- Bottom-right controls: Filter button, Layers button, Location/recenter button

---

## DM (Direct Message) Chat View

**Header:** Back arrow, contact avatar, contact name, "Direct Messages" subtitle, three-dot menu

**Chat Area:**
- Messages with sender indicator (own messages right-aligned, blue)
- Timestamps, delivery status

**Compose Bar:**
- Same as channel: "+" button, text input, send arrow, character counter 0/155

---

## Repeater Login Page

**Header:** "Repeater Login", repeater name, three-dot menu

**Login Form:**
- Lock icon + "Authentication Required — Please log in to manage this repeater."
- Password field (with show/hide toggle)
- "Remember Password?" checkbox
- "Log In · Flood" button
- Helper text: "Some repeater owners may allow guests to view metrics without a password."

**Post-Login (implied from official app UX):**
- Status view (battery, uptime, noise floor, packet counts)
- CLI terminal
- Neighbor discovery
- Settings (radio, network, security, advert)
- Actions (reboot, advert, clock sync)

---

## Settings Page (Device Configuration)

**Header:** Back arrow, "Settings", checkmark (save) button

**Profile:**
- Avatar (large, centered)
- Device name + truncated pubkey

**Public Info:**
- Name (editable)
- Public Key (full, copy button)
- Latitude / Longitude (with map pin button)
- "Share Position in Advert" checkbox (with info icon)

**Radio Settings:**
- Selected Preset (e.g. "New Zealand (Narrow)") + "Choose Preset" button
- Frequency (MHz)
- Bandwidth (dropdown: 62.5 kHz, 125 kHz, 250 kHz, 500 kHz)
- Spreading Factor (dropdown: 5-12)
- Coding Rate (dropdown: 5-8)
- Transmit Power (dBm)
- "Enable Repeat Mode" checkbox (with info icon)

**Other Settings (expandable sections):**
- Manage Identity Key
- Bluetooth Settings
- Contact Settings
- Message Settings
- Notification Settings
- Position Settings
- Telemetry Settings
- Experimental Settings
- Theme (Auto / Light / Dark)
- Language (Auto)

**Extra Tools:**
- Import Config
- Export Config
- Purge Data
- Bug Reporting
- Debug Logs
- Factory Reset
- Reboot

**Device Info:**
- Channels: N/40
- Contacts: N/200
- Storage: N% used · Nkb / 2048kb
- View Telemetry
- Device Model
- Firmware Date
- Firmware Version

---

## Top-Level App Menu (Three-Dot)

- Disconnect
- Add Contact
- Add Channel
- Discover Contacts
- My Contact Code (QR code)
- Internet Map
- Tools
- About MeshCore

---

## Broadcast Icon Menu (Top-Right Signal Icon)

- Advert · Zero Hop
- Advert · Flood Routed
- Advert · To Clipboard

---

## Tools Page

- **Trace Path · Manual** — Trace a path by entering it, or selecting repeaters from a list
- **Trace Path · Using Map** — Trace a path by selecting repeaters on a map
- **Antenna Coverage** — Check coverage on a map
- **Line of Sight** — Check line of sight on a map
- **Rx Log** — Watch live log of received packets
- **Discover Nearby Nodes** — Scan the network for nearby nodes
- **Discover Regions** — Scan the network for nearby regions
- **Noise Floor** — Watch noise floor in realtime

---

## Key UX Patterns

1. **Three-dot menus** are used everywhere for secondary actions
2. **Search bars** include the count (e.g. "Search 197 Contacts...")
3. **Filter/sort** is accessed via a funnel icon on the right of search
4. **Type indicators**: Repeater (broadcast icon), Chat/User (person icon/initials), Room (people icon), Sensor (chip icon)
5. **Character counter** shown on all message inputs (0/137 for channels, 0/155 for DMs)
6. **Inline confirmations** — no browser dialogs
7. **Route display**: "Flood" or "N hops" shown per contact
8. **Distance calculation** shown on contact detail
9. **Favourite system** for pinning important contacts
10. **Remember password** option for repeater logins
11. **Tapping a contact** goes directly to chat (users) or repeater login (repeaters)

---

## Protocol Constants

- Max channel message text: 153 - len(sender_name) bytes
- Max DM text: 155 bytes
- Max channels per device: 40
- Max contacts per device: 200
- Max storage: 2048kb
- Path hash size: 1-4 bytes per hop
- AES-128 block size: 16 bytes
- Cipher MAC: 2 bytes
