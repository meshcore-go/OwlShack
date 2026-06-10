import { useEffect, useRef, useState } from "react";

type MessageHandler = (topic: string, data: unknown) => void;

// How long after an app-level ping to wait for any inbound message before
// declaring the socket half-open and forcing a reconnect.
const PROBE_TIMEOUT_MS = 5000;

export function useWebSocket(topics: string[], onMessage?: MessageHandler) {
  const [connected, setConnected] = useState(false);
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectRef = useRef<number | null>(null);
  const probeRef = useRef<number | null>(null);
  const backoffRef = useRef(1000);
  const lastMsgRef = useRef(0);
  const onMessageRef = useRef(onMessage);
  onMessageRef.current = onMessage;

  const topicsKey = topics.join(",");

  useEffect(() => {
    let mounted = true;

    const connect = () => {
      const current = wsRef.current;
      if (
        current &&
        (current.readyState === WebSocket.OPEN ||
          current.readyState === WebSocket.CONNECTING)
      )
        return;

      const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
      const wsUrl = import.meta.env.DEV
        ? "ws://localhost:8080/api/ws"
        : `${protocol}//${window.location.host}/api/ws`;

      const ws = new WebSocket(wsUrl);
      wsRef.current = ws;

      ws.onopen = () => {
        if (!mounted) return;
        setConnected(true);
        backoffRef.current = 1000;
        for (const topic of topics) {
          ws.send(JSON.stringify({ action: "subscribe", topic }));
        }
      };

      ws.onmessage = (event) => {
        if (!mounted) return;
        lastMsgRef.current = Date.now();
        try {
          const data = JSON.parse(event.data);
          const topic = data.topic;
          if (!topic) return;
          if (onMessageRef.current) onMessageRef.current(topic, data.data);
        } catch (err) {
          console.error("ws parse error", err);
        }
      };

      ws.onclose = () => {
        if (!mounted) return;
        setConnected(false);
        wsRef.current = null;
        reconnectRef.current = window.setTimeout(() => {
          backoffRef.current = Math.min(backoffRef.current * 1.5, 30000);
          connect();
        }, backoffRef.current);
      };

      ws.onerror = () => {
        ws.close();
      };
    };

    // Mobile browsers suspend background tabs: the socket gets killed outright,
    // or survives half-open after a network switch (still reads OPEN but
    // receives nothing). On any "we're back" signal, reconnect immediately —
    // skipping whatever backoff is pending — and probe a socket that still
    // claims to be open with an app-level ping. Any inbound message (the
    // server's "pong" or a regular broadcast) counts as proof of life.
    const resume = () => {
      if (!mounted || document.visibilityState === "hidden") return;
      const ws = wsRef.current;
      if (!ws || ws.readyState >= WebSocket.CLOSING) {
        if (reconnectRef.current) {
          clearTimeout(reconnectRef.current);
          reconnectRef.current = null;
        }
        backoffRef.current = 1000;
        connect();
        return;
      }
      if (ws.readyState === WebSocket.OPEN && probeRef.current == null) {
        const sentAt = Date.now();
        try {
          ws.send(JSON.stringify({ action: "ping" }));
        } catch {
          ws.close();
          return;
        }
        probeRef.current = window.setTimeout(() => {
          probeRef.current = null;
          if (wsRef.current === ws && lastMsgRef.current < sentAt) {
            // Nothing heard since the probe — half-open. Force the reconnect
            // path (onclose schedules it on the freshly reset backoff).
            backoffRef.current = 1000;
            ws.close();
          }
        }, PROBE_TIMEOUT_MS);
      }
    };

    connect();
    document.addEventListener("visibilitychange", resume);
    window.addEventListener("focus", resume);
    window.addEventListener("online", resume);
    window.addEventListener("pageshow", resume);

    return () => {
      mounted = false;
      document.removeEventListener("visibilitychange", resume);
      window.removeEventListener("focus", resume);
      window.removeEventListener("online", resume);
      window.removeEventListener("pageshow", resume);
      if (reconnectRef.current) clearTimeout(reconnectRef.current);
      if (probeRef.current) clearTimeout(probeRef.current);
      wsRef.current?.close();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [topicsKey]);

  return { connected };
}
