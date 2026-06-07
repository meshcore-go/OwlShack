import { useEffect, useRef, useState } from "react";

type MessageHandler = (topic: string, data: unknown) => void;

export function useWebSocket(topics: string[], onMessage?: MessageHandler) {
  const [connected, setConnected] = useState(false);
  const wsRef = useRef<WebSocket | null>(null);
  const reconnectRef = useRef<number | null>(null);
  const backoffRef = useRef(1000);
  const onMessageRef = useRef(onMessage);
  onMessageRef.current = onMessage;

  const topicsKey = topics.join(",");

  useEffect(() => {
    let mounted = true;

    const connect = () => {
      if (wsRef.current?.readyState === WebSocket.OPEN) return;

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

    connect();

    return () => {
      mounted = false;
      if (reconnectRef.current) clearTimeout(reconnectRef.current);
      wsRef.current?.close();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [topicsKey]);

  return { connected };
}
