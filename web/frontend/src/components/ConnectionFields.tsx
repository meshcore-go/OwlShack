import { useEffect, useState } from "react";
import { SelectField, TextField } from "@/components/ConfigFields";
import {
  boardHint,
  boardOption,
  configApi,
  defaultBoard,
  type SerialPort,
  type SpiBoard,
} from "@/lib/configApi";

// The stored setting is still one URL string; these controls only spare the operator from typing it.
const BACKENDS = [
  { value: "kiss", label: "KISS modem (serial / TCP)" },
  { value: "spi", label: "SPI radio hat" },
];

const TRANSPORTS = [
  { value: "serial", label: "Serial / USB" },
  { value: "tcp", label: "Network (TCP)" },
];

// The buses a Pi header exposes; a hat's own port comes from the board list and is preselected.
const SPI_PORTS = ["SPI0.0", "SPI0.1", "SPI1.0", "SPI1.1", "SPI1.2"];

const DEFAULT_SERIAL = "/dev/ttyACM0";

export function connectionScheme(c: string): "serial" | "tcp" | "spi" {
  if (c.startsWith("tcp://")) return "tcp";
  if (c.startsWith("spi://")) return "spi";
  return "serial";
}

export function connectionTarget(c: string): string {
  return c.replace(/^[a-z0-9]+:\/\//, "");
}

// useSerialPorts lists what the host can see; an empty list is also what a fetch failure looks like,
// and every caller renders that the same way.
export function useSerialPorts(enabled: boolean): SerialPort[] {
  const [ports, setPorts] = useState<SerialPort[]>([]);
  useEffect(() => {
    if (!enabled) return;
    configApi.getSerialPorts().then(setPorts).catch(() => setPorts([]));
  }, [enabled]);
  return ports;
}

export interface ConnectionRow {
  label: string;
  value: string;
  sub?: string;
}

// connectionSummary describes a connection the way an operator would say it out loud. The stored
// value is a URL, but a review screen that prints the URL is asking the reader to parse a scheme.
export function connectionSummary(
  connectionType: string,
  connection: string,
  baudRate: string,
  spiBoard: string,
  boards: SpiBoard[],
  ports: SerialPort[],
): ConnectionRow[] {
  const target = connectionTarget(connection);

  if (connectionType === "spi") {
    const board = boards.find((b) => b.name === spiBoard);
    return [
      { label: "Radio backend", value: "SPI radio hat" },
      { label: "Hat", value: board?.label ?? spiBoard ?? "—" },
      { label: "SPI port", value: target || board?.spiPort || "—" },
    ];
  }

  if (connectionScheme(connection) === "tcp") {
    const at = target.lastIndexOf(":");
    return [
      { label: "Radio backend", value: "KISS modem over the network" },
      { label: "Host", value: at > 0 ? target.slice(0, at) : target || "—" },
      { label: "Port", value: at > 0 ? target.slice(at + 1) : "—" },
    ];
  }

  const port = ports.find((p) => p.path === target);
  return [
    { label: "Radio backend", value: "KISS modem over serial" },
    // The by-id path is what gets stored, but the board's own name is what identifies it to a person.
    { label: "Device", value: port?.label ?? target ?? "—", sub: port?.label ? port.device : undefined },
    { label: "Baud rate", value: baudRate },
  ];
}

function serialHint(ports: SerialPort[], target: string): string {
  const chosen = ports.find((p) => p.path === target);
  if (chosen?.stable) return `stored as ${chosen.path}, which survives a replug`;
  return "serial devices this host can see right now";
}

export function ConnectionFields({
  connectionType,
  setConnectionType,
  connection,
  setConnection,
  baudRate,
  setBaudRate,
  spiBoard,
  setSpiBoard,
  boards,
}: {
  connectionType: string;
  setConnectionType: (v: string) => void;
  connection: string;
  setConnection: (v: string) => void;
  baudRate: string;
  setBaudRate: (v: string) => void;
  spiBoard: string;
  setSpiBoard: (v: string) => void;
  boards: SpiBoard[];
}) {
  const spi = connectionType === "spi";
  const ports = useSerialPorts(!spi);
  const transport = spi ? "spi" : connectionScheme(connection);
  const target = connectionTarget(connection);
  const board = boards.find((b) => b.name === spiBoard);

  // An install from before the picker stores the tty. Adopt the same device's stable name so it is
  // one device on one row, not the tty and its own by-id entry offered as two choices. Settings load
  // after this component mounts, so this reacts to the connection too, not just the port list.
  useEffect(() => {
    if (spi || connectionScheme(connection) !== "serial") return;
    const same = ports.find((p) => p.stable && p.device === connectionTarget(connection));
    if (same) setConnection(`serial://${same.path}`);
  }, [ports, connection, spi, setConnection]);

  const pickBackend = (v: string) => {
    setConnectionType(v);
    if (v === "spi") {
      const pick = board ?? defaultBoard(boards);
      if (pick && !spiBoard) setSpiBoard(pick.name);
      if (connectionScheme(connection) !== "spi") {
        setConnection(`spi://${pick?.spiPort ?? SPI_PORTS[0]}`);
      }
    } else if (connectionScheme(connection) === "spi") {
      setConnection(`serial://${ports[0]?.path ?? DEFAULT_SERIAL}`);
    }
  };

  const pickTransport = (v: string) => {
    if (v === "tcp") setConnection("tcp://");
    else setConnection(`serial://${ports[0]?.path ?? DEFAULT_SERIAL}`);
  };

  const pickBoard = (name: string) => {
    setSpiBoard(name);
    const b = boards.find((x) => x.name === name);
    if (b) setConnection(`spi://${b.spiPort}`);
  };

  return (
    <>
      <SelectField
        label="Radio backend"
        value={connectionType}
        options={BACKENDS}
        onChange={pickBackend}
        hint={
          spi
            ? "A radio wired to this host's SPI bus. No MeshCore firmware involved."
            : "MeshCore firmware driving the radio, over serial or TCP."
        }
      />

      {spi ? (
        <>
          {boards.length > 0 ? (
            <SelectField
              label="Radio hat"
              value={spiBoard}
              options={boards.map(boardOption)}
              onChange={pickBoard}
              hint={boardHint(board)}
            />
          ) : (
            <TextField
              label="Radio hat"
              value={spiBoard}
              onChange={setSpiBoard}
              hint="No board list available from the server."
              placeholder="ultrapeaterzero-e22p"
            />
          )}
          <SelectField
            label="SPI port"
            value={target || board?.spiPort || SPI_PORTS[0]}
            options={SPI_PORTS.map((p) => ({ value: p, label: p }))}
            onChange={(v) => setConnection(`spi://${v}`)}
            hint={
              board && target && target !== board.spiPort
                ? `This hat is wired to ${board.spiPort}; change it only if yours is on another bus.`
                : "The chip-select line this hat's NSS is wired to."
            }
          />
        </>
      ) : (
        <>
          <SelectField
            label="Transport"
            value={transport}
            options={TRANSPORTS}
            onChange={pickTransport}
          />
          {transport === "tcp" ? (
            <TextField
              label="Address"
              value={target}
              onChange={(v) => setConnection(`tcp://${v.trim()}`)}
              placeholder="192.168.1.50:5000"
              hint="host:port of a MeshCore node exposing its KISS interface over the network"
            />
          ) : (
            <>
              {ports.length > 0 ? (
                <SelectField
                  label="Device"
                  value={target}
                  options={ports.map((p) => ({
                    value: p.path,
                    label: p.label ? `${p.label} · ${p.device}` : p.device,
                  }))}
                  onChange={(v) => setConnection(`serial://${v}`)}
                  hint={serialHint(ports, target)}
                />
              ) : (
                <TextField
                  label="Device"
                  value={target}
                  onChange={(v) => setConnection(`serial://${v.trim()}`)}
                  placeholder={DEFAULT_SERIAL}
                  hint="No serial devices detected. Plug the modem in, or type its path."
                />
              )}
              <TextField
                label="Baud rate"
                value={baudRate}
                onChange={setBaudRate}
                placeholder="115200"
              />
            </>
          )}
        </>
      )}
    </>
  );
}
