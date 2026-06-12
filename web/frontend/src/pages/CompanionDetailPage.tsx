import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ChangeEvent,
  type KeyboardEvent,
  type ReactNode,
} from "react";
import {
  Link,
  useNavigate,
  useParams,
  useSearchParams,
} from "react-router-dom";
import {
  AlertCircle,
  ArrowLeft,
  Ban,
  CheckCheck,
  ChevronLeft,
  Copy,
  CornerUpLeft,
  ExternalLink,
  Hash,
  Loader2,
  LogIn,
  MessageSquare,
  MoreVertical,
  Radar,
  Radio,
  Reply,
  RotateCw,
  Search,
  Send,
  Trash2,
  UserPlus,
  Users,
  X,
} from "lucide-react";
import { toast } from "sonner";
import { useWebSocket } from "@/hooks/useWebSocket";
import { PageHeader } from "@/components/PageHeader";
import { ConnectionPill, PeerTypePill } from "@/components/StatusIndicator";
import { PeerAvatar } from "@/components/PeerAvatar";
import { snrTextClass } from "@/components/SignalStrength";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { ChatHeaderMenu } from "@/components/ChatHeaderMenu";
import { CoordLink } from "@/components/CoordLink";
import { ComposerAttachMenu } from "@/components/ComposerAttachMenu";
import { EmojiButton, EmojiMartPanel, EmojiPicker } from "@/components/EmojiPicker";
import { useEmojiAutocomplete } from "@/hooks/useEmojiAutocomplete";
import { useIsMobile } from "@/hooks/use-mobile";
import {
  AddContactDialog,
  type ContactPrefill,
  type ContactType,
} from "@/components/AddContactDialog";
import { formatDateTime, formatShortTime, timeAgo, truncateMid } from "@/lib/format";
import { cn } from "@/lib/utils";

interface Conversation {
  id: string;
  type: string;
  name: string;
  channel: string;
  lastMessage?: {
    text: string;
    sender: string;
    direction: string;
    timestamp: string;
  };
  unreadCount: number;
  lastActive?: string;
  peerType?: string;
  isRepeater?: boolean;
  pubkey?: string;
}

interface Message {
  id?: number;
  channel: string;
  sender: string;
  text: string;
  direction: string;
  timestamp: string;
  snr?: number | null;
  rssi?: number | null;
  repeatCount?: number | null;
  hops?: number | null;
  pathHashSize?: number | null;
  status?: string | null;
}

// GET /rooms/{pubkey}/session returns {loggedIn:false} or the bare session
// struct — presence of pubkeyHex means logged in, same rule as repeaters.
interface RoomSession {
  loggedIn?: boolean;
  pubkeyHex?: string;
  isAdmin?: boolean;
  role?: string;
}

interface PathHop {
  hash: string;
  peerNames?: string[];
}

interface PathInfo {
  hops: number;
  pathHashSize: number;
  sender: string;
  path: PathHop[];
}

interface EchoEntry {
  id: number;
  receivedAt: string;
  hops: number;
  pathHashSize: number;
  path: PathHop[];
  snr?: number | null;
  rssi?: number | null;
}

type SortMode = "recent" | "name" | "unread";
type ModalKind = "path" | "echoes" | "rxPaths";

interface ModalState {
  kind: ModalKind;
  message: Message;
  backTo?: ModalState;
}

const MENTION_RE = /@\[([^\]]+)\]/g;

// Single-pass tokenizer for rich content in message text. Alternatives are
// tried in order at each position, so tokens never overlap:
//  - contact: a shared-contact embed  <64-hex-pubkey:type(1-4):name>
//  - url:     http/https only (other schemes are never linkified)
//  - coord:   lat,lon with decimals (range-validated at render time). The
//             surrounding (?<![\d.]) / (?![\d.]) guards stop it matching the
//             middle of a longer number.
//  - mention: @[name]
const TOKEN_RE = new RegExp(
  [
    "(?<contact><[0-9a-fA-F]{64}:[1-4]:[^>]*>)",
    "(?<url>https?:\\/\\/[^\\s<>]+)",
    "(?<coord>(?<![\\d.])-?\\d{1,3}\\.\\d+\\s*,\\s*-?\\d{1,3}\\.\\d+(?![\\d.]))",
    "(?<mention>@\\[[^\\]]+\\])",
  ].join("|"),
  "g",
);

const CONTACT_TYPE_BY_INT: Record<string, ContactType> = {
  "1": "CHAT",
  "2": "REPEATER",
  "3": "ROOM",
  "4": "SENSOR",
};

const COORD_RE = /^(-?\d{1,3}\.\d+)\s*,\s*(-?\d{1,3}\.\d+)$/;

// Parse + range-validate a "lat,lon" token. Returns null if out of range so
// the caller leaves it as plain text rather than a bogus map link.
function parseCoord(s: string): { lat: number; lon: number } | null {
  const m = COORD_RE.exec(s.trim());
  if (!m) return null;
  const lat = parseFloat(m[1]);
  const lon = parseFloat(m[2]);
  if (!Number.isFinite(lat) || !Number.isFinite(lon)) return null;
  if (lat < -90 || lat > 90 || lon < -180 || lon > 180) return null;
  return { lat, lon };
}

// Trailing sentence punctuation usually isn't part of a URL ("see https://x.")
// so peel it off and render it as plain text.
function splitTrailingPunct(url: string): { url: string; trailing: string } {
  const m = /[.,;:!?'")\]}]+$/.exec(url);
  if (!m) return { url, trailing: "" };
  return { url: url.slice(0, m.index), trailing: url.slice(m.index) };
}
const SORT_KEY = "companion-sort";

export function CompanionDetailPage() {
  const { name } = useParams<{ name: string }>();
  const decodedName = useMemo(
    () => (name ? decodeURIComponent(name) : ""),
    [name],
  );
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const activeChannel = searchParams.get("channel");

  const [conversations, setConversations] = useState<Conversation[]>([]);
  const [messages, setMessages] = useState<Message[]>([]);
  const [loadingList, setLoadingList] = useState(true);
  const [loadingMsgs, setLoadingMsgs] = useState(false);
  const [listError, setListError] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [sort, setSort] = useState<SortMode>(() => {
    if (typeof window === "undefined") return "recent";
    const stored = window.localStorage.getItem(SORT_KEY) as SortMode | null;
    return stored ?? "recent";
  });
  const [composer, setComposer] = useState("");
  const [sending, setSending] = useState(false);
  const [msgSearch, setMsgSearch] = useState("");
  const [msgSearchOpen, setMsgSearchOpen] = useState(false);
  const [contextMsg, setContextMsg] = useState<Message | null>(null);
  const [contextPos, setContextPos] = useState<{ x: number; y: number } | null>(
    null,
  );
  const [modal, setModal] = useState<ModalState | null>(null);
  const [modalLoading, setModalLoading] = useState(false);
  const [modalPath, setModalPath] = useState<PathInfo | null>(null);
  const [modalEchoes, setModalEchoes] = useState<EchoEntry[] | null>(null);
  const [ownPubkey, setOwnPubkey] = useState<string | null>(null);
  const [ownPos, setOwnPos] = useState<{ lat: number; lon: number } | null>(null);
  const isMobile = useIsMobile();
  const [emojiOpen, setEmojiOpen] = useState(false);
  const [addContactOpen, setAddContactOpen] = useState(false);
  const [addContactPrefill, setAddContactPrefill] =
    useState<ContactPrefill | null>(null);

  const messagesEndRef = useRef<HTMLDivElement>(null);
  const composerRef = useRef<HTMLTextAreaElement>(null);
  const messageCacheRef = useRef<Map<string, Message[]>>(new Map());
  const wasConnectedRef = useRef(false);

  const activeConversation = useMemo(
    () => conversations.find((c) => c.channel === activeChannel) ?? null,
    [conversations, activeChannel],
  );

  // Channels that already have a conversation row. The WS handler reads this
  // to decide between an in-place row update and a full roster reload — the
  // decision must stay outside the setConversations updater (updaters must be
  // pure; StrictMode invokes them twice).
  const knownChannelsRef = useRef<Set<string>>(new Set());
  knownChannelsRef.current = useMemo(
    () => new Set(conversations.map((c) => c.channel)),
    [conversations],
  );

  const isContact = activeConversation?.type === "contact";
  const isRoom = activeConversation?.peerType === "ROOM";
  // Rooms silently truncate posts at 151 chars (firmware MAX_POST_TEXT_LEN).
  const charLimit = isRoom
    ? 151
    : isContact
      ? 155
      : Math.max(0, 153 - decodedName.length);

  const roomPubkey = isRoom ? (activeConversation?.pubkey ?? null) : null;
  const [roomSession, setRoomSession] = useState<RoomSession | null>(null);
  const [roomSessionLoading, setRoomSessionLoading] = useState(false);

  useEffect(() => {
    setRoomSession(null);
    if (!roomPubkey || !decodedName) return;
    let cancelled = false;
    setRoomSessionLoading(true);
    fetch(
      `/api/companions/${encodeURIComponent(decodedName)}/rooms/${roomPubkey}/session`,
    )
      .then((r) => (r.ok ? r.json() : null))
      .then((s: RoomSession | null) => {
        if (!cancelled) setRoomSession(s);
      })
      .catch(() => {})
      .finally(() => {
        if (!cancelled) setRoomSessionLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [decodedName, roomPubkey]);

  const roomLoggedIn =
    !!roomSession && roomSession.loggedIn !== false && !!roomSession.pubkeyHex;
  const roomReadOnly = roomLoggedIn && roomSession?.role === "read-only";

  useEffect(() => {
    if (typeof window !== "undefined") {
      window.localStorage.setItem(SORT_KEY, sort);
    }
  }, [sort]);

  const loadConversations = useCallback(() => {
    if (!decodedName) return;
    setLoadingList(true);
    setListError(null);
    fetch(`/api/companions/${encodeURIComponent(decodedName)}/conversations`)
      .then((r) => {
        if (!r.ok) throw new Error("conversations");
        return r.json();
      })
      .then((data: Conversation[]) => setConversations(data || []))
      .catch(() => setListError("Failed to load conversations"))
      .finally(() => setLoadingList(false));
  }, [decodedName]);

  useEffect(() => {
    loadConversations();
  }, [loadConversations]);

  // This companion's own public key, used to flag self-add attempts on
  // shared-contact cards in chat.
  useEffect(() => {
    if (!decodedName) return;
    let cancelled = false;
    fetch("/api/companions")
      .then((r) => (r.ok ? r.json() : []))
      .then(
        (list: { name: string; pubkey?: string; lat?: number; lon?: number }[]) => {
          if (cancelled) return;
          const me = (list || []).find((c) => c.name === decodedName);
          setOwnPubkey(me?.pubkey ?? null);
          setOwnPos(
            me && me.lat != null && me.lon != null
              ? { lat: me.lat, lon: me.lon }
              : null,
          );
        },
      )
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [decodedName]);

  const onAddContact = useCallback((prefill: ContactPrefill) => {
    setAddContactPrefill(prefill);
    setAddContactOpen(true);
  }, []);

  // Pre-fill the composer from a ?compose= param (e.g. "Share in a message"
  // from the Peers screen), then strip the param so it isn't re-applied.
  useEffect(() => {
    const text = searchParams.get("compose");
    if (!text) return;
    setComposer(text);
    const next = new URLSearchParams(searchParams);
    next.delete("compose");
    setSearchParams(next, { replace: true });
    window.setTimeout(() => composerRef.current?.focus(), 0);
  }, [searchParams, setSearchParams]);

  const lastIdOf = useCallback((arr: Message[]): number => {
    let max = 0;
    for (const m of arr) {
      if (typeof m.id === "number" && m.id > max) max = m.id;
    }
    return max;
  }, []);

  const initialLoadMessages = useCallback(
    async (channel: string) => {
      if (!decodedName) return;
      setLoadingMsgs(true);
      try {
        const r = await fetch(
          `/api/companions/${encodeURIComponent(decodedName)}/messages?channel=${encodeURIComponent(channel)}&limit=100`,
        );
        if (!r.ok) throw new Error("messages");
        const data: Message[] = await r.json();
        const ordered = (data || []).slice().reverse();
        messageCacheRef.current.set(channel, ordered);
        setMessages(ordered);
      } catch {
        toast.error("Failed to load messages");
      } finally {
        setLoadingMsgs(false);
      }
    },
    [decodedName],
  );

  const backfillMessages = useCallback(
    async (channel: string) => {
      if (!decodedName) return;
      const existing = messageCacheRef.current.get(channel) || [];
      const afterId = lastIdOf(existing);
      if (afterId <= 0) {
        await initialLoadMessages(channel);
        return;
      }
      try {
        const r = await fetch(
          `/api/companions/${encodeURIComponent(decodedName)}/messages?channel=${encodeURIComponent(channel)}&afterId=${afterId}&limit=500`,
        );
        if (!r.ok) return;
        const delta: Message[] = await r.json();
        if (!delta || delta.length === 0) return;
        const merged = mergeMessages(existing, delta);
        messageCacheRef.current.set(channel, merged);
        if (channel === activeChannel) setMessages(merged);
      } catch {
        // backfill is best-effort
      }
    },
    [decodedName, lastIdOf, initialLoadMessages, activeChannel],
  );

  useEffect(() => {
    if (!activeChannel) {
      setMessages([]);
      return;
    }
    const cached = messageCacheRef.current.get(activeChannel);
    if (cached) {
      setMessages(cached);
      setLoadingMsgs(false);
      backfillMessages(activeChannel);
    } else {
      initialLoadMessages(activeChannel);
    }
  }, [activeChannel, initialLoadMessages, backfillMessages]);

  useEffect(() => {
    if (!activeChannel || !activeConversation) return;
    if (activeConversation.unreadCount === 0) return;
    const last = messages[messages.length - 1];
    if (!last?.id) return;
    fetch(
      `/api/companions/${encodeURIComponent(decodedName)}/conversations/${encodeURIComponent(activeConversation.id)}/read`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ lastReadId: last.id }),
      },
    ).catch(() => {});
    setConversations((prev) =>
      prev.map((c) =>
        c.id === activeConversation.id ? { ...c, unreadCount: 0 } : c,
      ),
    );
  }, [activeChannel, activeConversation, messages, decodedName]);

  const handleWsMessage = useCallback(
    (topic: string, data: unknown) => {
      if (topic !== "messages" || !data || typeof data !== "object") return;
      const payload = data as Message & {
        companion?: string;
        action?: string;
      };
      if (payload.companion !== decodedName) return;

      if (payload.action === "repeatCount" && payload.id != null) {
        const updateRepeat = (m: Message): Message =>
          m.id === payload.id
            ? { ...m, repeatCount: payload.repeatCount ?? m.repeatCount }
            : m;
        if (payload.channel) {
          const cached = messageCacheRef.current.get(payload.channel);
          if (cached) {
            messageCacheRef.current.set(payload.channel, cached.map(updateRepeat));
          }
        }
        setMessages((prev) => prev.map(updateRepeat));
        return;
      }

      if (payload.action === "status" && payload.id != null) {
        const updateStatus = (m: Message): Message =>
          m.id === payload.id
            ? { ...m, status: (payload as { status?: string }).status ?? m.status }
            : m;
        if (payload.channel) {
          const cached = messageCacheRef.current.get(payload.channel);
          if (cached) {
            messageCacheRef.current.set(payload.channel, cached.map(updateStatus));
          }
        }
        setMessages((prev) => prev.map(updateStatus));
        return;
      }

      const incoming: Message = {
        id: payload.id,
        channel: payload.channel,
        sender: payload.sender,
        text: payload.text,
        direction: payload.direction,
        timestamp: payload.timestamp,
        snr: payload.snr,
        rssi: payload.rssi,
        repeatCount: payload.repeatCount,
        hops: payload.hops,
        pathHashSize: payload.pathHashSize,
        status: (payload as { status?: string }).status,
      };
      if (!incoming.channel || !incoming.timestamp) return;

      const cached = messageCacheRef.current.get(incoming.channel);
      if (cached) {
        messageCacheRef.current.set(
          incoming.channel,
          mergeMessages(cached, [incoming]),
        );
      }

      if (incoming.channel === activeChannel) {
        setMessages((prev) => mergeMessages(prev, [incoming]));
      }

      if (!knownChannelsRef.current.has(incoming.channel)) {
        loadConversations();
        return;
      }

      setConversations((prev) => {
        const idx = prev.findIndex((c) => c.channel === incoming.channel);
        if (idx === -1) return prev;
        const updated = [...prev];
        const target = { ...updated[idx] };
        target.lastMessage = {
          text: incoming.text,
          sender: incoming.sender,
          direction: incoming.direction,
          timestamp: incoming.timestamp,
        };
        target.lastActive = incoming.timestamp;
        if (
          incoming.direction === "rx" &&
          incoming.channel !== activeChannel
        ) {
          target.unreadCount = (target.unreadCount || 0) + 1;
        }
        updated[idx] = target;
        return updated;
      });
    },
    [decodedName, activeChannel, loadConversations],
  );

  const { connected } = useWebSocket(["messages"], handleWsMessage);

  useEffect(() => {
    if (connected && !wasConnectedRef.current) {
      // first connect or reconnect — backfill any messages we may have missed.
      // only the active channel needs immediate refresh; others are refreshed
      // on re-open.
      if (activeChannel) backfillMessages(activeChannel);
    }
    wasConnectedRef.current = connected;
  }, [connected, activeChannel, backfillMessages]);

  const initialScrollDoneRef = useRef(false);
  useEffect(() => {
    initialScrollDoneRef.current = false;
    setMsgSearch("");
    setMsgSearchOpen(false);
  }, [activeChannel]);

  useEffect(() => {
    if (loadingMsgs || messages.length === 0) return;
    if (!initialScrollDoneRef.current) {
      messagesEndRef.current?.scrollIntoView({ behavior: "instant" as ScrollBehavior });
      initialScrollDoneRef.current = true;
    } else {
      messagesEndRef.current?.scrollIntoView({ behavior: "smooth" });
    }
  }, [messages, loadingMsgs]);

  useEffect(() => {
    if (!contextMsg) return;
    const dismiss = (e: MouseEvent | TouchEvent) => {
      const target = (
        "touches" in e ? document.elementFromPoint((e as TouchEvent).touches[0].clientX, (e as TouchEvent).touches[0].clientY) : e.target
      ) as HTMLElement | null;
      if (target?.closest("[data-context-menu]")) return;
      setContextMsg(null);
      setContextPos(null);
    };
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.key === "Escape") {
        setContextMsg(null);
        setContextPos(null);
      }
    };
    window.addEventListener("mousedown", dismiss);
    window.addEventListener("touchstart", dismiss);
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("mousedown", dismiss);
      window.removeEventListener("touchstart", dismiss);
      window.removeEventListener("keydown", onKey);
    };
  }, [contextMsg]);

  const filteredConversations = useMemo(() => {
    const q = search.trim().toLowerCase();
    let arr = conversations.filter((c) => !c.isRepeater);
    if (q) {
      arr = arr.filter(
        (c) =>
          c.name.toLowerCase().includes(q) ||
          c.lastMessage?.text.toLowerCase().includes(q),
      );
    }
    const isPublic = (c: Conversation) =>
      c.name.toLowerCase() === "public" || c.channel.toLowerCase() === "public";
    const pinPublic = (a: Conversation, b: Conversation) => {
      const ap = isPublic(a) ? 0 : 1;
      const bp = isPublic(b) ? 0 : 1;
      return ap - bp;
    };
    if (sort === "name") {
      arr.sort((a, b) => pinPublic(a, b) || a.name.localeCompare(b.name));
    } else if (sort === "unread") {
      arr.sort((a, b) => {
        const p = pinPublic(a, b);
        if (p !== 0) return p;
        if (a.unreadCount !== b.unreadCount)
          return b.unreadCount - a.unreadCount;
        return tsValue(b.lastActive) - tsValue(a.lastActive);
      });
    } else {
      arr.sort(
        (a, b) => pinPublic(a, b) || tsValue(b.lastActive) - tsValue(a.lastActive),
      );
    }
    return arr;
  }, [conversations, search, sort]);

  const filteredMessages = useMemo(() => {
    if (!msgSearch.trim()) return messages;
    const q = msgSearch.toLowerCase();
    return messages.filter(
      (m) =>
        m.text.toLowerCase().includes(q) ||
        m.sender.toLowerCase().includes(q),
    );
  }, [messages, msgSearch]);

  const groupedMessages = useMemo(() => groupMessages(filteredMessages), [filteredMessages]);

  const openConversation = useCallback(
    (c: Conversation) => {
      if (c.isRepeater && c.pubkey) {
        navigate(
          `/companions/${encodeURIComponent(decodedName)}/repeaters/${encodeURIComponent(c.pubkey)}`,
        );
        return;
      }
      setSearchParams({ channel: c.channel });
    },
    [decodedName, navigate, setSearchParams],
  );

  const closeChat = useCallback(() => {
    const params = new URLSearchParams(searchParams);
    params.delete("channel");
    setSearchParams(params);
  }, [searchParams, setSearchParams]);

  const emojiAC = useEmojiAutocomplete({
    textareaRef: composerRef,
    setText: setComposer,
  });

  const send = useCallback(async () => {
    if (!activeConversation || !composer.trim() || sending) return;
    const text = composer.trim();
    if (text.length > charLimit) {
      toast.error(`Message too long (${text.length}/${charLimit})`);
      return;
    }
    setSending(true);
    try {
      const r = await fetch(
        `/api/companions/${encodeURIComponent(decodedName)}/messages`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            channel: activeConversation.channel,
            text,
          }),
        },
      );
      if (!r.ok) throw new Error("send");
      setComposer("");
      setEmojiOpen(false);
    } catch {
      toast.error("Send failed");
    } finally {
      setSending(false);
    }
  }, [activeConversation, composer, sending, charLimit, decodedName]);

  const onComposerKey = useCallback(
    (e: KeyboardEvent<HTMLTextAreaElement>) => {
      if (emojiAC.handleKeyDown(e)) return;
      if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        send();
      }
    },
    [emojiAC.handleKeyDown, send],
  );

  // focus=false keeps the caret without summoning the mobile keyboard (used by
  // the mobile emoji panel, which would otherwise be covered by the keyboard).
  const insertAtCursor = useCallback((text: string, focus = true) => {
    const el = composerRef.current;
    if (!el) {
      setComposer((c) => c + text);
      return;
    }
    const start = el.selectionStart ?? el.value.length;
    const end = el.selectionEnd ?? el.value.length;
    setComposer(el.value.slice(0, start) + text + el.value.slice(end));
    requestAnimationFrame(() => {
      if (focus) el.focus();
      const pos = start + text.length;
      el.setSelectionRange(pos, pos);
    });
  }, []);

  const onMessageContext = useCallback(
    (e: React.MouseEvent, m: Message) => {
      e.preventDefault();
      setContextMsg(m);
      setContextPos({ x: e.clientX, y: e.clientY });
    },
    [],
  );

  const handleCopy = useCallback(async (m: Message) => {
    try {
      await navigator.clipboard.writeText(m.text);
      toast.success("Copied");
    } catch {
      toast.error("Copy failed");
    }
  }, []);

  const handleReply = useCallback(
    (m: Message) => {
      const isOwn = m.direction === "tx" || m.sender === decodedName;
      const next = isOwn
        ? `${m.text
            .split("\n")
            .map((line) => `> ${line}`)
            .join("\n")}\n`
        : `@[${m.sender}] `;
      setComposer(next);
      requestAnimationFrame(() => {
        const el = composerRef.current;
        if (!el) return;
        el.focus();
        const end = el.value.length;
        el.setSelectionRange(end, end);
      });
    },
    [decodedName],
  );

  const handleDelete = useCallback(
    async (m: Message) => {
      if (!m.id) return;
      try {
        const r = await fetch(`/api/messages/${m.id}`, { method: "DELETE" });
        if (!r.ok) throw new Error("delete");
        setMessages((prev) => prev.filter((x) => x.id !== m.id));
        const cached = messageCacheRef.current.get(m.channel);
        if (cached) {
          messageCacheRef.current.set(
            m.channel,
            cached.filter((x) => x.id !== m.id),
          );
        }
        toast.success("Deleted");
      } catch {
        toast.error("Delete failed");
      }
    },
    [],
  );

  const handleRetry = useCallback(
    async (m: Message) => {
      if (!m.id) return;
      try {
        const r = await fetch(
          `/api/companions/${encodeURIComponent(decodedName)}/messages/${m.id}/retry`,
          { method: "POST" },
        );
        if (!r.ok) throw new Error("retry");
        setMessages((prev) => prev.filter((x) => x.id !== m.id));
        const cached = messageCacheRef.current.get(m.channel);
        if (cached) {
          messageCacheRef.current.set(
            m.channel,
            cached.filter((x) => x.id !== m.id),
          );
        }
      } catch {
        toast.error("Retry failed");
      }
    },
    [decodedName],
  );

  const handleBlock = useCallback(
    async (m: Message) => {
      if (!activeConversation) return;
      try {
        const r = await fetch(
          `/api/companions/${encodeURIComponent(decodedName)}/conversations/${encodeURIComponent(activeConversation.id)}/block`,
          {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ sender: m.sender }),
          },
        );
        if (!r.ok) throw new Error("block");
        toast.success(`Blocked ${m.sender}`);
      } catch {
        toast.error("Block failed");
      }
    },
    [activeConversation, decodedName],
  );

  const openModal = useCallback(
    async (kind: ModalKind, m: Message, backTo?: ModalState) => {
      if (!m.id) {
        toast.error("Message missing id");
        return;
      }
      setModal({ kind, message: m, backTo });
      setModalPath(null);
      setModalEchoes(null);
      setModalLoading(true);
      try {
        if (kind === "path") {
          const r = await fetch(
            `/api/companions/${encodeURIComponent(decodedName)}/messages/${m.id}/path?channel=${encodeURIComponent(m.channel)}`,
          );
          if (!r.ok) throw new Error("path");
          setModalPath(await r.json());
        } else if (kind === "echoes") {
          const r = await fetch(`/api/messages/${m.id}/echoes`);
          if (!r.ok) throw new Error("echoes");
          setModalEchoes(await r.json());
        } else if (kind === "rxPaths") {
          const [pr, er] = await Promise.all([
            fetch(
              `/api/companions/${encodeURIComponent(decodedName)}/messages/${m.id}/path?channel=${encodeURIComponent(m.channel)}`,
            ),
            fetch(`/api/messages/${m.id}/echoes`),
          ]);
          if (pr.ok) setModalPath(await pr.json());
          if (er.ok) setModalEchoes(await er.json());
        }
      } catch {
        toast.error("Failed to load");
      } finally {
        setModalLoading(false);
      }
    },
    [decodedName],
  );

  const closeModal = useCallback(() => {
    setModal(null);
    setModalPath(null);
    setModalEchoes(null);
  }, []);

  const composerLen = composer.length;
  const overLimit = composerLen > charLimit;

  return (
    <div className={cn("h-[calc(100dvh-3.5rem)] -my-6 -mx-4 sm:-mx-6 flex flex-col overflow-hidden", activeChannel && "max-lg:[&>:first-child]:hidden")}>
      <div className="shrink-0 px-4 sm:px-6 pt-6">
        <PageHeader
          title="Messages"
          meta={
            <span className="font-mono text-sm text-muted-foreground tabular-nums">
              {conversations.length} thread
              {conversations.length === 1 ? "" : "s"}
            </span>
          }
          trailing={<ConnectionPill connected={connected} />}
          actions={
            <>
              <Link
                to={`/companions/${encodeURIComponent(decodedName)}/contacts`}
                className="inline-flex items-center font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground hover:text-primary px-2.5 py-1.5 sm:py-0.5 border border-border"
              >
                <Users className="size-3 mr-1.5" /> contacts
              </Link>
              <Link
                to={`/companions/${encodeURIComponent(decodedName)}/channels`}
                className="inline-flex items-center font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground hover:text-primary px-2.5 py-1.5 sm:py-0.5 border border-border"
              >
                <Hash className="size-3 mr-1.5" /> channels
              </Link>
              <Link
                to={`/companions/${encodeURIComponent(decodedName)}/repeaters`}
                className="inline-flex items-center font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground hover:text-primary px-2.5 py-1.5 sm:py-0.5 border border-border"
              >
                <Radio className="size-3 mr-1.5" /> repeaters
              </Link>
            </>
          }
        />
      </div>

      {listError && (
        <div className="shrink-0 px-4 sm:px-6">
          <Alert variant="destructive">
            <AlertTitle className="font-mono uppercase tracking-[0.1em]">
              Error
            </AlertTitle>
            <AlertDescription>
              {listError}
              <Button
                variant="ghost"
                size="sm"
                onClick={loadConversations}
                className="ml-2 h-7 text-xs uppercase tracking-[0.1em]"
              >
                retry
              </Button>
            </AlertDescription>
          </Alert>
        </div>
      )}

      <section className={cn(
        "grid grid-cols-1 lg:grid-cols-[20rem_minmax(0,1fr)] gap-px bg-border border border-border flex-1 min-h-0 overflow-hidden mx-4 sm:mx-6 mb-6",
        activeChannel && "max-lg:border-0 max-lg:mx-0 max-lg:mb-0",
      )}>
        {/* Conversation list */}
        <div
          className={cn(
            "bg-card flex flex-col min-h-0",
            activeChannel && "hidden lg:flex",
          )}
        >
          <div className="px-4 py-3 border-b border-border flex items-center justify-between gap-2">
            <span className="label-overline">Threads</span>
            <Select
              value={sort}
              onValueChange={(v) => setSort(v as SortMode)}
            >
              <SelectTrigger
                size="sm"
                className="font-mono text-[10px] uppercase tracking-[0.1em] h-7 px-2 rounded-none border-border"
              >
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem
                  value="recent"
                  className="font-mono text-xs uppercase tracking-[0.08em]"
                >
                  Recent
                </SelectItem>
                <SelectItem
                  value="name"
                  className="font-mono text-xs uppercase tracking-[0.08em]"
                >
                  Name
                </SelectItem>
                <SelectItem
                  value="unread"
                  className="font-mono text-xs uppercase tracking-[0.08em]"
                >
                  Unread
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="px-3 py-3 border-b border-border">
            <div className="relative">
              <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 size-3.5 text-muted-foreground/50" />
              <Input
                value={search}
                onChange={(e: ChangeEvent<HTMLInputElement>) =>
                  setSearch(e.target.value)
                }
                placeholder="search"
                className="pl-8 h-8 font-mono text-xs rounded-none border-border bg-background"
              />
            </div>
          </div>
          <div className="flex-1 overflow-y-auto divide-y divide-border/60">
            {loadingList ? (
              <ListSkeleton />
            ) : filteredConversations.length === 0 ? (
              <div className="p-6 text-center font-mono text-xs text-muted-foreground/60 uppercase tracking-[0.1em]">
                No threads
              </div>
            ) : (
              filteredConversations.map((c) => (
                <ConversationRow
                  key={c.id}
                  convo={c}
                  active={c.channel === activeChannel}
                  onClick={() => openConversation(c)}
                />
              ))
            )}
          </div>
        </div>

        {/* Chat panel */}
        <div
          className={cn(
            "bg-card flex flex-col min-h-0",
            !activeChannel && "hidden lg:flex",
          )}
        >
          {activeConversation ? (
            <>
              <div className="px-4 py-3 border-b border-border flex items-center gap-3">
                <button
                  type="button"
                  onClick={closeChat}
                  className="lg:hidden p-1 -ml-1 text-muted-foreground hover:text-foreground"
                >
                  <ChevronLeft className="size-4" />
                </button>
                <PeerAvatar name={activeConversation.name} size="md" />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="font-mono text-sm font-semibold truncate">
                      {activeConversation.name}
                    </span>
                    {activeConversation.peerType && (
                      <PeerTypePill type={activeConversation.peerType} />
                    )}
                  </div>
                  <div className="text-mono-xs text-muted-foreground">
                    {activeConversation.type === "channel"
                      ? "broadcast channel"
                      : isRoom
                        ? roomLoggedIn
                          ? `room server · ${roomSession?.role ?? "joined"}`
                          : "room server · not joined"
                        : "direct message"}
                    {activeConversation.lastActive && (
                      <>
                        {" "}
                        · last {timeAgo(activeConversation.lastActive)}
                      </>
                    )}
                  </div>
                </div>
                <ChatHeaderMenu
                  companion={decodedName}
                  conversation={activeConversation}
                  onSearchToggle={() => {
                    setMsgSearchOpen((v) => !v);
                    if (msgSearchOpen) setMsgSearch("");
                  }}
                  onMessagesCleared={() => {
                    setMessages([]);
                    messageCacheRef.current.delete(activeConversation.channel);
                  }}
                />
              </div>

              {msgSearchOpen && (
                <div className="px-3 py-2 border-b border-border flex items-center gap-2 bg-card">
                  <Search className="size-3.5 text-muted-foreground/50 shrink-0" />
                  <Input
                    value={msgSearch}
                    onChange={(e) => setMsgSearch(e.target.value)}
                    placeholder="Search messages..."
                    className="h-7 font-mono text-xs rounded-none border-border bg-background"
                    autoFocus
                  />
                  <button
                    type="button"
                    onClick={() => { setMsgSearchOpen(false); setMsgSearch(""); }}
                    className="text-muted-foreground hover:text-foreground shrink-0"
                  >
                    <X className="size-3.5" />
                  </button>
                  {msgSearch && (
                    <span className="font-mono text-[10px] tabular-nums text-muted-foreground/60 shrink-0">
                      {filteredMessages.length}
                    </span>
                  )}
                </div>
              )}

              <div className="flex-1 overflow-y-auto overflow-x-hidden px-4 py-4 space-y-4 bg-background/30">
                {loadingMsgs ? (
                  <MessagesSkeleton />
                ) : groupedMessages.length === 0 ? (
                  <div className="h-full flex flex-col items-center justify-center text-center text-muted-foreground/50 gap-3">
                    <MessageSquare className="size-8" strokeWidth={1.2} />
                    <span className="font-mono text-xs uppercase tracking-[0.12em]">
                      No transmissions on record
                    </span>
                  </div>
                ) : (
                  groupedMessages.map((group, gi) => (
                    <MessageGroup
                      key={`${group.sender}-${gi}-${group.messages[0].timestamp}`}
                      group={group}
                      ownName={decodedName}
                      ownPubkey={ownPubkey}
                      onContext={onMessageContext}
                      onReply={handleReply}
                      onRetry={handleRetry}
                      onAddContact={onAddContact}
                    />
                  ))
                )}
                <div ref={messagesEndRef} />
              </div>

              {isRoom && !roomLoggedIn ? (
                <RoomJoinBar
                  companion={decodedName}
                  pubkey={roomPubkey ?? ""}
                  checking={roomSessionLoading}
                  onJoined={setRoomSession}
                />
              ) : (
              <div className="relative border-t border-border bg-card">
                {emojiAC.dropdown}
                <div className="px-3 py-2 flex items-end gap-2">
                  <ComposerAttachMenu
                    companion={decodedName}
                    ownPubkey={ownPubkey}
                    ownName={decodedName}
                    ownLat={ownPos?.lat}
                    ownLon={ownPos?.lon}
                    onInsert={insertAtCursor}
                  />
                  {isMobile ? (
                    <EmojiButton
                      active={emojiOpen}
                      onClick={() => setEmojiOpen((o) => !o)}
                    />
                  ) : (
                    <EmojiPicker onSelect={insertAtCursor} />
                  )}
                  <Textarea
                    ref={composerRef}
                    rows={1}
                    value={composer}
                    onChange={(e: ChangeEvent<HTMLTextAreaElement>) => {
                      setComposer(e.target.value);
                      emojiAC.handleChange(
                        e.target.value,
                        e.target.selectionStart ?? e.target.value.length,
                      );
                    }}
                    onKeyDown={onComposerKey}
                    onBlur={() => emojiAC.close()}
                    placeholder={
                      roomReadOnly ? "read-only access" : "transmit…"
                    }
                    disabled={roomReadOnly}
                    className="resize-none rounded-none border-border font-mono text-sm min-h-[2.25rem] max-h-[100px] bg-background"
                    style={{ height: "auto" }}
                  />
                  <Button
                    onClick={send}
                    disabled={
                      sending || !composer.trim() || overLimit || roomReadOnly
                    }
                    size="sm"
                    className="rounded-none h-9 font-mono text-[11px] uppercase tracking-[0.12em]"
                  >
                    <Send className="size-3.5" />
                    send
                  </Button>
                </div>
                <div className="px-3 pb-2 flex items-center justify-between">
                  <span className="text-mono-xs text-muted-foreground/60 hidden sm:inline">
                    {roomReadOnly
                      ? "read-only access — posting disabled"
                      : "Enter sends · Shift+Enter newline"}
                  </span>
                  <span
                    className={cn(
                      "font-mono text-[10px] tabular-nums",
                      overLimit
                        ? "text-destructive"
                        : composerLen > charLimit * 0.85
                          ? "text-warning"
                          : "text-muted-foreground/60",
                    )}
                  >
                    {composerLen}/{charLimit}
                  </span>
                </div>
                {isMobile && emojiOpen && (
                  <div className="border-t border-border">
                    <EmojiMartPanel
                      fullWidth
                      onSelect={(e) => insertAtCursor(e, false)}
                    />
                  </div>
                )}
              </div>
              )}
            </>
          ) : (
            <div className="h-full grid place-items-center text-center p-8">
              <div className="space-y-3">
                <Radar
                  className="size-10 mx-auto text-muted-foreground/40"
                  strokeWidth={1.2}
                />
                <div className="font-mono text-sm uppercase tracking-[0.12em] text-muted-foreground">
                  Select a thread to begin
                </div>
                <div className="text-mono-xs text-muted-foreground/60">
                  Choose a conversation from the panel.
                </div>
              </div>
            </div>
          )}
        </div>
      </section>

      {contextMsg && contextPos && (
        <ContextMenu
          msg={contextMsg}
          pos={contextPos}
          ownName={decodedName}
          onCopy={() => {
            handleCopy(contextMsg);
            setContextMsg(null);
          }}
          onReply={() => {
            handleReply(contextMsg);
            setContextMsg(null);
          }}
          onViewPaths={() => {
            openModal("rxPaths", contextMsg);
            setContextMsg(null);
          }}
          onEchoes={() => {
            openModal("echoes", contextMsg);
            setContextMsg(null);
          }}
          onBlock={() => {
            handleBlock(contextMsg);
            setContextMsg(null);
          }}
          onDelete={() => {
            handleDelete(contextMsg);
            setContextMsg(null);
          }}
        />
      )}

      <AddContactDialog
        companion={decodedName}
        open={addContactOpen}
        onOpenChange={setAddContactOpen}
        initial={addContactPrefill ?? undefined}
        ownPubkey={ownPubkey ?? undefined}
      />

      <Dialog open={!!modal} onOpenChange={(o) => !o && closeModal()}>
        <DialogContent className="rounded-none border-border bg-card max-w-xl">
          <DialogHeader>
            <div className="flex items-center gap-2">
              {modal?.backTo && (
                <button
                  type="button"
                  onClick={() => {
                    const back = modal.backTo!;
                    setModal(back);
                    if (back.kind === "path") openModal("path", back.message);
                    else if (back.kind === "echoes")
                      openModal("echoes", back.message);
                    else openModal("rxPaths", back.message);
                  }}
                  className="text-muted-foreground hover:text-foreground"
                >
                  <ArrowLeft className="size-4" />
                </button>
              )}
              <DialogTitle className="font-mono text-sm uppercase tracking-[0.12em]">
                {modal?.kind === "path" && "Receive path"}
                {modal?.kind === "echoes" && "Echo observations"}
                {modal?.kind === "rxPaths" && "Path & echoes"}
              </DialogTitle>
            </div>
            <DialogDescription className="font-mono text-xs text-muted-foreground">
              {modal?.message.sender}
              {modal?.message.timestamp && (
                <>
                  {" "}
                  · {formatDateTime(modal.message.timestamp)}
                </>
              )}
            </DialogDescription>
          </DialogHeader>
          <div className="max-h-[60vh] overflow-y-auto">
            {modalLoading ? (
              <div className="space-y-2">
                <Skeleton className="h-8 w-full" />
                <Skeleton className="h-8 w-3/4" />
                <Skeleton className="h-8 w-2/3" />
              </div>
            ) : (
              <>
                {(modal?.kind === "path" || modal?.kind === "rxPaths") &&
                  modalPath && <PathTimeline path={modalPath} />}
                {(modal?.kind === "echoes" || modal?.kind === "rxPaths") &&
                  modalEchoes && (
                    <EchoList echoes={modalEchoes} />
                  )}
                {modal?.kind === "rxPaths" && !modalPath && !modalEchoes && (
                  <p className="font-mono text-xs text-muted-foreground/60">
                    No path data.
                  </p>
                )}
              </>
            )}
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function tsValue(iso?: string): number {
  if (!iso) return 0;
  return new Date(iso).getTime() || 0;
}

function mergeMessages(existing: Message[], incoming: Message[]): Message[] {
  if (incoming.length === 0) return existing;
  const byId = new Map<number, number>();
  const byKey = new Map<string, number>();
  existing.forEach((m, i) => {
    if (typeof m.id === "number" && m.id > 0) byId.set(m.id, i);
    else byKey.set(`${m.timestamp}:${m.sender}:${m.text}`, i);
  });
  const out = existing.slice();
  let appended = false;
  for (const m of incoming) {
    const idMatch = typeof m.id === "number" && m.id > 0 ? byId.get(m.id) : undefined;
    if (idMatch != null) {
      out[idMatch] = { ...out[idMatch], ...m };
      continue;
    }
    const k = `${m.timestamp}:${m.sender}:${m.text}`;
    const keyMatch = byKey.get(k);
    if (keyMatch != null) {
      out[keyMatch] = { ...out[keyMatch], ...m };
      continue;
    }
    out.push(m);
    appended = true;
    if (typeof m.id === "number" && m.id > 0) byId.set(m.id, out.length - 1);
    else byKey.set(k, out.length - 1);
  }
  if (appended) {
    out.sort((a, b) => {
      const ta = tsValue(a.timestamp);
      const tb = tsValue(b.timestamp);
      if (ta !== tb) return ta - tb;
      const ia = typeof a.id === "number" ? a.id : 0;
      const ib = typeof b.id === "number" ? b.id : 0;
      return ia - ib;
    });
  }
  return out;
}

interface MessageGroupShape {
  sender: string;
  direction: string;
  messages: Message[];
}

function groupMessages(messages: Message[]): MessageGroupShape[] {
  const out: MessageGroupShape[] = [];
  for (const m of messages) {
    const last = out[out.length - 1];
    if (
      last &&
      last.sender === m.sender &&
      last.direction === m.direction &&
      Math.abs(
        new Date(m.timestamp).getTime() -
          new Date(last.messages[last.messages.length - 1].timestamp).getTime(),
      ) <
        5 * 60 * 1000
    ) {
      last.messages.push(m);
    } else {
      out.push({ sender: m.sender, direction: m.direction, messages: [m] });
    }
  }
  return out;
}

function ConversationRow({
  convo,
  active,
  onClick,
}: {
  convo: Conversation;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "w-full px-3 py-2.5 flex items-start gap-3 text-left transition-colors group",
        active
          ? "bg-primary/10 border-l-2 border-primary"
          : "hover:bg-muted/40 border-l-2 border-transparent",
      )}
    >
      <PeerAvatar name={convo.name} size="md" />
      <div className="min-w-0 flex-1 space-y-0.5">
        <div className="flex items-center justify-between gap-2">
          <span
            className={cn(
              "truncate font-medium text-sm",
              convo.unreadCount > 0 && "text-foreground",
            )}
          >
            {convo.name}
          </span>
          <span className="font-mono text-[10px] tabular-nums text-muted-foreground/70 shrink-0">
            {convo.lastActive ? formatShortTime(convo.lastActive) : ""}
          </span>
        </div>
        <div className="flex items-center justify-between gap-2">
          <span className="truncate text-xs text-muted-foreground">
            {convo.lastMessage ? (
              <>
                {convo.lastMessage.direction === "tx" && (
                  <span className="text-primary/80 font-mono uppercase tracking-[0.1em] mr-1">
                    me ›
                  </span>
                )}
                {convo.lastMessage.text}
              </>
            ) : (
              <span className="italic text-muted-foreground/50">
                no messages
              </span>
            )}
          </span>
          {convo.unreadCount > 0 && (
            <span className="shrink-0 font-mono text-[10px] tabular-nums px-1.5 py-0.5 bg-primary/15 text-primary border border-primary/30">
              {convo.unreadCount}
            </span>
          )}
        </div>
        <div className="flex items-center gap-1.5 pt-0.5">
          {convo.peerType && <PeerTypePill type={convo.peerType} />}
          {convo.isRepeater && (
            <span className="font-mono text-[9px] uppercase tracking-[0.1em] px-1 py-0.5 border border-warning/40 text-warning bg-warning/5">
              repeater
            </span>
          )}
          {convo.type === "channel" && (
            <span className="font-mono text-[9px] uppercase tracking-[0.1em] text-muted-foreground/60">
              # channel
            </span>
          )}
        </div>
      </div>
    </button>
  );
}

// Replaces the composer for a room thread until a login session exists.
function RoomJoinBar({
  companion,
  pubkey,
  checking,
  onJoined,
}: {
  companion: string;
  pubkey: string;
  checking: boolean;
  onJoined: (s: RoomSession) => void;
}) {
  const [password, setPassword] = useState("");
  const [savePw, setSavePw] = useState(false);
  const [savedPw, setSavedPw] = useState("");
  const [metadata, setMetadata] = useState<Record<string, unknown> | null>(
    null,
  );
  const [joining, setJoining] = useState(false);

  useEffect(() => {
    let cancelled = false;
    setPassword("");
    setSavedPw("");
    setSavePw(false);
    setMetadata(null);
    fetch(
      `/api/companions/${encodeURIComponent(companion)}/contacts/${pubkey}`,
    )
      .then((r) => (r.ok ? r.json() : null))
      .then((c: { metadata?: Record<string, unknown> } | null) => {
        if (cancelled || !c) return;
        const meta = c.metadata ?? {};
        setMetadata(meta);
        const pw =
          typeof meta.roomPassword === "string" ? meta.roomPassword : "";
        setSavedPw(pw);
        if (pw) setSavePw(true);
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [companion, pubkey]);

  const join = useCallback(async () => {
    setJoining(true);
    try {
      const pw = password || savedPw;
      const r = await fetch(
        `/api/companions/${encodeURIComponent(companion)}/rooms/${pubkey}/login`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ password: pw }),
        },
      );
      if (!r.ok) {
        const err = (await r.json().catch(() => null)) as {
          error?: string;
        } | null;
        throw new Error(err?.error || "Join failed");
      }
      const result = (await r.json()) as { role?: string };

      // PATCH replaces the whole metadata blob — merge with what's stored.
      const desired = savePw ? pw : "";
      if (metadata && (metadata.roomPassword ?? "") !== desired) {
        await fetch(
          `/api/companions/${encodeURIComponent(companion)}/contacts/${pubkey}`,
          {
            method: "PATCH",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ ...metadata, roomPassword: desired }),
          },
        ).catch(() => {});
      }

      const sr = await fetch(
        `/api/companions/${encodeURIComponent(companion)}/rooms/${pubkey}/session`,
      );
      const sess = sr.ok ? ((await sr.json()) as RoomSession) : null;
      onJoined(
        sess && sess.pubkeyHex
          ? sess
          : { pubkeyHex: pubkey, role: result.role },
      );
      toast.success(`Joined room${result.role ? ` · ${result.role}` : ""}`);
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Failed to join room");
    } finally {
      setJoining(false);
    }
  }, [companion, pubkey, password, savedPw, savePw, metadata, onJoined]);

  return (
    <div className="border-t border-border bg-card px-3 py-3 space-y-2">
      <div className="flex items-center gap-2 font-mono text-[11px] uppercase tracking-[0.12em] text-muted-foreground">
        <LogIn className="size-3.5" />
        Join room to post &amp; sync messages
      </div>
      <div className="flex items-center gap-2">
        <Input
          type="password"
          value={password}
          onChange={(e) => setPassword(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") void join();
          }}
          placeholder={
            savedPw ? "•••••• (saved)" : "room password (blank to re-auth)"
          }
          disabled={joining}
          className="h-9 font-mono text-sm rounded-none border-border bg-background"
        />
        <Button
          onClick={join}
          disabled={joining || checking}
          size="sm"
          className="rounded-none h-9 font-mono text-[11px] uppercase tracking-[0.12em]"
        >
          {joining ? (
            <Loader2 className="size-3.5 animate-spin" />
          ) : (
            <LogIn className="size-3.5" />
          )}
          join
        </Button>
      </div>
      <label className="flex items-center gap-2 cursor-pointer text-mono-xs text-muted-foreground w-fit">
        <Switch checked={savePw} onCheckedChange={setSavePw} />
        save password to contact
      </label>
    </div>
  );
}

function MessageGroup({
  group,
  ownName,
  ownPubkey,
  onContext,
  onReply,
  onRetry,
  onAddContact,
}: {
  group: MessageGroupShape;
  ownName: string;
  ownPubkey: string | null;
  onContext: (e: React.MouseEvent, m: Message) => void;
  onReply: (m: Message) => void;
  onRetry: (m: Message) => void;
  onAddContact: (prefill: ContactPrefill) => void;
}) {
  const isTx = group.direction === "tx";
  return (
    <div
      className={cn(
        "flex gap-2",
        isTx ? "flex-row-reverse" : "flex-row",
      )}
    >
      <div className="shrink-0 pt-0.5">
        <PeerAvatar name={group.sender} size="sm" />
      </div>
      <div
        className={cn(
          "max-w-[90%] sm:max-w-[68%] flex flex-col gap-1",
          isTx ? "items-end" : "items-start",
        )}
      >
        <div className="flex items-baseline gap-2 px-1">
          <span className="font-mono text-[10px] uppercase tracking-[0.08em] text-muted-foreground">
            {isTx ? "me" : group.sender}
          </span>
          <span className="font-mono text-[10px] tabular-nums text-muted-foreground/60">
            {formatShortTime(group.messages[0].timestamp)}
          </span>
        </div>
        {group.messages.map((m, i) => (
          <MessageBubble
            key={m.id ?? `${m.timestamp}-${i}`}
            msg={m}
            isTx={isTx}
            ownName={ownName}
            ownPubkey={ownPubkey}
            onContext={onContext}
            onReply={onReply}
            onRetry={onRetry}
            onAddContact={onAddContact}
          />
        ))}
      </div>
    </div>
  );
}

function MessageBubble({
  msg,
  isTx,
  ownName,
  ownPubkey,
  onContext,
  onReply,
  onRetry,
  onAddContact,
}: {
  msg: Message;
  isTx: boolean;
  ownName: string;
  ownPubkey: string | null;
  onContext: (e: React.MouseEvent, m: Message) => void;
  onReply: (m: Message) => void;
  onRetry: (m: Message) => void;
  onAddContact: (prefill: ContactPrefill) => void;
}) {
  const longPressTimer = useRef<number | null>(null);
  const longPressTriggered = useRef(false);
  const [tapped, setTapped] = useState(false);

  const onTouchStart = (e: React.TouchEvent) => {
    longPressTriggered.current = false;
    const t = e.touches[0];
    longPressTimer.current = window.setTimeout(() => {
      longPressTriggered.current = true;
      const fakeEvt = {
        preventDefault: () => {},
        clientX: t.clientX,
        clientY: t.clientY,
      } as unknown as React.MouseEvent;
      onContext(fakeEvt, msg);
    }, 500);
  };
  const onTouchEnd = () => {
    if (longPressTimer.current) {
      window.clearTimeout(longPressTimer.current);
      longPressTimer.current = null;
    }
  };

  const onBubbleClick = () => {
    if (longPressTriggered.current) return;
    setTapped((v) => !v);
  };

  return (
    <div
      className={cn(
        "group/msg flex items-center gap-1.5",
        isTx ? "flex-row-reverse" : "flex-row",
      )}
    >
      <div
        onContextMenu={(e) => onContext(e, msg)}
        onTouchStart={onTouchStart}
        onTouchEnd={onTouchEnd}
        onTouchMove={onTouchEnd}
        onClick={onBubbleClick}
        className={cn(
          "px-3 py-1.5 rounded-sm border text-sm leading-snug whitespace-pre-wrap break-words select-none sm:select-auto",
          isTx
            ? "bg-primary/15 border-primary/30 text-foreground"
            : "bg-card border-border",
        )}
      >
        <MessageText
          text={msg.text}
          ownName={ownName}
          ownPubkey={ownPubkey}
          onAddContact={onAddContact}
        />
        {(msg.snr != null ||
          (msg.repeatCount != null && msg.repeatCount > 0) ||
          (isTx && msg.status)) && (
          <div
            className={cn(
              "mt-1 flex items-center gap-2 font-mono text-[10px] tabular-nums",
              isTx ? "justify-end" : "justify-start",
            )}
          >
            {!isTx && msg.snr != null && (
              <span className={snrTextClass(msg.snr)}>{msg.snr.toFixed(1)}dB</span>
            )}
            {msg.hops != null && msg.hops > 0 && (
              <span className="text-muted-foreground/60">
                {msg.hops} hop{msg.hops > 1 ? "s" : ""}
              </span>
            )}
            {msg.repeatCount != null && msg.repeatCount > 0 && (
              <span className="text-success/80 inline-flex items-center gap-1">
                <Reply className="size-2.5" />×{msg.repeatCount}
              </span>
            )}
            {isTx && msg.status === "sending" && (
              <span className="text-muted-foreground/60 inline-flex items-center gap-1">
                <Loader2 className="size-2.5 animate-spin" />sending
              </span>
            )}
            {isTx && msg.status === "delivered" && (
              <span className="text-success/80 inline-flex items-center gap-1">
                <CheckCheck className="size-2.5" />delivered
              </span>
            )}
            {isTx && msg.status === "failed" && (
              <span className="text-destructive inline-flex items-center gap-1">
                <AlertCircle className="size-2.5" />failed
                <button
                  type="button"
                  onClick={() => onRetry(msg)}
                  className="ml-1 inline-flex items-center gap-0.5 text-primary hover:text-primary/80"
                >
                  <RotateCw className="size-2.5" />retry
                </button>
              </span>
            )}
          </div>
        )}
      </div>
      <div
        className={cn(
          "flex items-center gap-0.5 transition-opacity",
          tapped ? "opacity-100" : "opacity-0 group-hover/msg:opacity-100",
          isTx ? "flex-row-reverse" : "flex-row",
        )}
      >
        <button
          type="button"
          aria-label="Reply"
          onClick={() => onReply(msg)}
          className="size-7 grid place-items-center text-muted-foreground/70 hover:text-primary hover:bg-muted/60 border border-transparent hover:border-border rounded-sm"
        >
          <CornerUpLeft className="size-3.5" strokeWidth={1.6} />
        </button>
        <button
          type="button"
          aria-label="More"
          onClick={(e) => {
            const rect = (e.currentTarget as HTMLButtonElement).getBoundingClientRect();
            const fake = {
              preventDefault: () => {},
              clientX: rect.left,
              clientY: rect.bottom + 4,
            } as unknown as React.MouseEvent;
            onContext(fake, msg);
          }}
          className="size-7 grid place-items-center text-muted-foreground/70 hover:text-foreground hover:bg-muted/60 border border-transparent hover:border-border rounded-sm"
        >
          <MoreVertical className="size-3.5" strokeWidth={1.6} />
        </button>
      </div>
    </div>
  );
}

function MessageText({
  text,
  ownName,
  ownPubkey,
  onAddContact,
}: {
  text: string;
  ownName: string;
  ownPubkey: string | null;
  onAddContact: (prefill: ContactPrefill) => void;
}) {
  const parts: ReactNode[] = [];
  let last = 0;
  let key = 0;
  for (const match of text.matchAll(TOKEN_RE)) {
    const idx = match.index ?? 0;
    const token = match[0];
    const g = match.groups ?? {};

    // A coordinate-looking token that fails range validation is left as plain
    // text; skip it without consuming so it falls into the next text slice.
    const coord = g.coord ? parseCoord(token) : null;
    if (g.coord && !coord) continue;

    if (idx > last) parts.push(text.slice(last, idx));

    if (g.contact) {
      const m = /^<([0-9a-fA-F]{64}):([1-4]):([^>]*)>$/.exec(token);
      if (m) {
        parts.push(
          <ContactCard
            key={`c-${key++}`}
            pubkey={m[1].toLowerCase()}
            type={CONTACT_TYPE_BY_INT[m[2]]}
            name={m[3]}
            ownPubkey={ownPubkey}
            onAdd={onAddContact}
          />,
        );
      } else {
        parts.push(token);
      }
    } else if (g.url) {
      const { url, trailing } = splitTrailingPunct(token);
      parts.push(<UrlLink key={`u-${key++}`} url={url} />);
      if (trailing) parts.push(trailing);
    } else if (g.coord && coord) {
      parts.push(
        <CoordLink key={`g-${key++}`} lat={coord.lat} lon={coord.lon} raw={token} />,
      );
    } else if (g.mention) {
      const target = token.slice(2, -1);
      const isSelf = target === ownName;
      parts.push(
        <span
          key={`m-${key++}`}
          className={cn(
            "px-1 -mx-0.5 rounded-sm font-medium",
            isSelf
              ? "bg-warning/20 text-warning"
              : "text-primary font-semibold",
          )}
        >
          @{target}
        </span>,
      );
    }
    last = idx + token.length;
  }
  if (last < text.length) parts.push(text.slice(last));
  return <>{parts}</>;
}

// A shared-contact embed rendered as a tappable card. Adding routes through the
// manual Add Contact modal (pre-filled) so the user reviews before committing;
// the user's own key is shown but cannot be added.
function ContactCard({
  pubkey,
  type,
  name,
  ownPubkey,
  onAdd,
}: {
  pubkey: string;
  type?: ContactType;
  name: string;
  ownPubkey: string | null;
  onAdd: (prefill: ContactPrefill) => void;
}) {
  const isSelf = !!ownPubkey && pubkey.toLowerCase() === ownPubkey.toLowerCase();
  const displayName = name.trim() || "unknown";
  return (
    <div className="my-1 max-w-[18rem] select-text rounded-sm border border-border bg-background/60 p-2.5">
      <div className="flex items-center gap-2">
        <PeerAvatar name={displayName} size="sm" />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <span className="truncate text-sm font-medium">{displayName}</span>
            {type && <PeerTypePill type={type} />}
          </div>
          <code className="mt-0.5 block truncate font-mono text-[10px] text-muted-foreground">
            {truncateMid(pubkey, 6, 6)}
          </code>
        </div>
      </div>
      {isSelf ? (
        <div className="mt-2 border border-dashed border-border py-1.5 text-center font-mono text-[10px] uppercase tracking-[0.1em] text-muted-foreground/70">
          This is you
        </div>
      ) : (
        <button
          type="button"
          onClick={(e) => {
            e.stopPropagation();
            onAdd({ pubkey, name: name.trim(), type });
          }}
          className="mt-2 inline-flex w-full items-center justify-center gap-1.5 border border-primary/40 bg-primary/10 py-1.5 font-mono text-[11px] uppercase tracking-[0.1em] text-primary transition-colors hover:bg-primary/20"
        >
          <UserPlus className="size-3.5" /> Add Contact
        </button>
      )}
    </div>
  );
}

// An http/https URL. Clicking shows a confirmation with the full destination
// before opening, since message text is untrusted. Only http/https reach here
// (enforced by the tokenizer), so no javascript:/data: schemes are possible.
function UrlLink({ url }: { url: string }) {
  const [open, setOpen] = useState(false);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          onClick={(e) => e.stopPropagation()}
          className="break-all text-info underline decoration-dotted underline-offset-2 hover:text-info/80"
        >
          {url}
        </button>
      </PopoverTrigger>
      <PopoverContent
        align="start"
        onClick={(e) => e.stopPropagation()}
        className="w-72 space-y-2 rounded-none p-3"
      >
        <p className="label-overline">Open external link?</p>
        <p className="break-all font-mono text-[11px] text-muted-foreground">
          {url}
        </p>
        <div className="flex justify-end gap-2 pt-1">
          <Button
            variant="ghost"
            size="xs"
            onClick={() => setOpen(false)}
            className="font-mono uppercase tracking-[0.1em]"
          >
            Cancel
          </Button>
          <Button
            size="xs"
            onClick={() => {
              window.open(url, "_blank", "noopener,noreferrer");
              setOpen(false);
            }}
            className="font-mono uppercase tracking-[0.1em]"
          >
            <ExternalLink className="size-3" /> Open
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  );
}

function ContextMenu({
  msg,
  pos,
  ownName,
  onCopy,
  onReply,
  onViewPaths,
  onEchoes,
  onBlock,
  onDelete,
}: {
  msg: Message;
  pos: { x: number; y: number };
  ownName: string;
  onCopy: () => void;
  onReply: () => void;
  onViewPaths: () => void;
  onEchoes: () => void;
  onBlock: () => void;
  onDelete: () => void;
}) {
  const isRx = msg.direction === "rx";
  const showEchoes =
    !isRx && msg.repeatCount != null && msg.repeatCount > 0;
  return (
    <div
      role="menu"
      data-context-menu
      className="fixed z-[60] min-w-[10rem] bg-popover border border-border rounded-sm shadow-md py-1 text-sm"
      style={{
        top: Math.min(pos.y, window.innerHeight - 240),
        left: Math.min(pos.x, window.innerWidth - 200),
      }}
      onClick={(e) => e.stopPropagation()}
    >
      <CtxItem icon={<Copy className="size-3.5" />} onClick={onCopy}>
        Copy
      </CtxItem>
      <CtxItem icon={<CornerUpLeft className="size-3.5" />} onClick={onReply}>
        Reply
      </CtxItem>
      {isRx && (
        <CtxItem icon={<Radar className="size-3.5" />} onClick={onViewPaths}>
          View paths
        </CtxItem>
      )}
      {showEchoes && (
        <CtxItem icon={<Reply className="size-3.5" />} onClick={onEchoes}>
          Echoes
        </CtxItem>
      )}
      {isRx && msg.sender !== ownName && (
        <CtxItem
          icon={<Ban className="size-3.5" />}
          onClick={onBlock}
          variant="destructive"
        >
          Block sender
        </CtxItem>
      )}
      <CtxItem
        icon={<Trash2 className="size-3.5" />}
        onClick={onDelete}
        variant="destructive"
      >
        Delete
      </CtxItem>
    </div>
  );
}

function CtxItem({
  icon,
  onClick,
  children,
  variant,
}: {
  icon: ReactNode;
  onClick: () => void;
  children: ReactNode;
  variant?: "destructive";
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "w-full text-left px-3 py-1.5 flex items-center gap-2 font-mono text-xs uppercase tracking-[0.08em] hover:bg-muted/60",
        variant === "destructive" && "text-destructive",
      )}
    >
      <span className="text-muted-foreground/70">{icon}</span>
      {children}
    </button>
  );
}

function PathTimeline({ path }: { path: PathInfo }) {
  if (!path.path || path.path.length === 0) {
    return (
      <p className="font-mono text-xs text-muted-foreground/60 px-1">
        No path hops recorded.
      </p>
    );
  }
  return (
    <div className="space-y-2 px-1">
      <div className="flex items-center gap-3 text-mono-xs text-muted-foreground">
        <span>hops · {path.hops}</span>
        <span>hashSize · {path.pathHashSize}</span>
        <span>sender · {path.sender}</span>
      </div>
      <ol className="relative pl-6 border-l border-border">
        {path.path.map((hop, i) => (
          <li key={`${hop.hash}-${i}`} className="relative pb-3">
            <span className="absolute -left-[1.04rem] top-0.5 size-3.5 grid place-items-center bg-card border border-border font-mono text-[9px] tabular-nums">
              {i + 1}
            </span>
            <div className="flex items-baseline justify-between gap-2">
              <span className="font-mono text-xs">
                {hop.peerNames && hop.peerNames.length > 0
                  ? hop.peerNames.join(" / ")
                  : (
                      <span className="text-muted-foreground italic">
                        unknown
                      </span>
                    )}
              </span>
              <code className="font-mono text-[10px] text-muted-foreground/70">
                {hop.hash}
              </code>
            </div>
          </li>
        ))}
      </ol>
    </div>
  );
}

function EchoList({ echoes }: { echoes: EchoEntry[] }) {
  if (echoes.length === 0) {
    return (
      <p className="font-mono text-xs text-muted-foreground/60 px-1 mt-3">
        No echo observations.
      </p>
    );
  }
  return (
    <div className="mt-3 space-y-2 px-1">
      <div className="text-mono-xs text-muted-foreground">
        {echoes.length} echo{echoes.length === 1 ? "" : "es"}
      </div>
      {echoes.map((e) => (
        <div
          key={e.id}
          className="border border-border bg-card/50 p-2.5 space-y-1.5"
        >
          <div className="flex items-baseline justify-between font-mono text-[10px] tabular-nums uppercase tracking-[0.08em]">
            <span className="text-muted-foreground">
              {formatDateTime(e.receivedAt)}
            </span>
            <span className="flex items-center gap-2">
              <span>{e.hops} hop{e.hops === 1 ? "" : "s"}</span>
              {e.snr != null && (
                <span className={snrTextClass(e.snr)}>{e.snr}dB</span>
              )}
            </span>
          </div>
          {e.path.length > 0 && (
            <ol className="pl-3 border-l border-border space-y-1">
              {e.path.map((hop, i) => (
                <li
                  key={`${hop.hash}-${i}`}
                  className="font-mono text-[11px] flex items-baseline justify-between gap-2"
                >
                  <span>
                    {hop.peerNames?.[0] || (
                      <span className="text-muted-foreground/60 italic">
                        unknown
                      </span>
                    )}
                  </span>
                  <code className="text-muted-foreground/60 text-[10px]">
                    {hop.hash}
                  </code>
                </li>
              ))}
            </ol>
          )}
        </div>
      ))}
    </div>
  );
}

function ListSkeleton() {
  return (
    <div className="p-3 space-y-3">
      {[...Array(5)].map((_, i) => (
        <div key={i} className="flex items-start gap-3">
          <Skeleton className="size-9 rounded-sm" />
          <div className="flex-1 space-y-1.5">
            <Skeleton className="h-3 w-24" />
            <Skeleton className="h-2.5 w-40" />
          </div>
        </div>
      ))}
    </div>
  );
}

function MessagesSkeleton() {
  return (
    <div className="space-y-3">
      {[...Array(4)].map((_, i) => (
        <div
          key={i}
          className={cn(
            "flex gap-2",
            i % 2 === 0 ? "flex-row" : "flex-row-reverse",
          )}
        >
          <Skeleton className="size-7 rounded-sm" />
          <Skeleton className="h-10 w-48 rounded-sm" />
        </div>
      ))}
    </div>
  );
}

