import { useEffect, useState } from "react";
import { AlertTriangle, Loader2, Save } from "lucide-react";
import { PageHeader } from "@/components/PageHeader";
import { LoadErrorAlert } from "@/components/LoadErrorAlert";
import { SectionTitle } from "@/components/SectionTitle";
import { SelectField, TextField } from "@/components/ConfigFields";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { useConfig } from "@/hooks/useConfig";

const BANDWIDTHS = [7.8, 10.4, 15.6, 20.8, 31.25, 41.7, 62.5, 125, 250, 500];
const LOG_LEVELS = ["trace", "debug", "info", "warn", "error"];

export function RadioPage() {
  const { config, loading, error, saving, save, reload } = useConfig();

  const [connection, setConnection] = useState("");
  const [baudRate, setBaudRate] = useState("115200");
  const [freq, setFreq] = useState("");
  const [bw, setBw] = useState("");
  const [sf, setSf] = useState("");
  const [cr, setCr] = useState("");
  const [tx, setTx] = useState("");
  const [listenAddr, setListenAddr] = useState("");
  const [logLevel, setLogLevel] = useState("info");

  useEffect(() => {
    if (!config) return;
    setConnection(config.connection ?? "");
    setBaudRate(String(config.baudRate ?? 115200));
    setFreq(config.freq != null ? String(config.freq) : "");
    setBw(config.bw != null ? String(config.bw) : "");
    setSf(config.sf != null ? String(config.sf) : "");
    setCr(config.cr != null ? String(config.cr) : "");
    setTx(config.tx != null ? String(config.tx) : "");
    setListenAddr(config.listenAddr ?? "");
    setLogLevel(config.logLevel ?? "info");
  }, [config]);

  const onSave = async () => {
    if (!config) return;
    const next = {
      ...config,
      connection,
      baudRate: parseInt(baudRate, 10) || 115200,
      freq: parseFloat(freq) || null,
      bw: parseFloat(bw) || null,
      sf: parseInt(sf, 10) || null,
      cr: parseInt(cr, 10) || null,
      tx: tx === "" ? null : parseInt(tx, 10),
      listenAddr: listenAddr || null,
      logLevel: logLevel || null,
    };
    await save(next);
  };

  return (
    <div className="space-y-8">
      <PageHeader
        eyebrow="system"
        title="Radio"
        actions={
          <Button
            size="sm"
            onClick={onSave}
            disabled={saving || loading || !config}
            className="rounded-none font-mono text-[11px] uppercase tracking-[0.12em]"
          >
            {saving ? (
              <Loader2 className="size-3.5 animate-spin" />
            ) : (
              <Save className="size-3.5" />
            )}
            save
          </Button>
        }
      />

      {error && <LoadErrorAlert message={error} onRetry={reload} />}

      <div className="flex items-start gap-2 border border-warning/40 bg-warning/5 px-3 py-2 font-mono text-[11px] text-warning">
        <AlertTriangle className="size-3.5 shrink-0 mt-0.5" />
        <span>
          Changing connection or radio parameters reconnects the modem and
          restarts all companions (repeater/room sessions drop). Log level
          applies instantly; the web address needs a process restart.
        </span>
      </div>

      {loading ? (
        <Skeleton className="h-64 w-full rounded-none" />
      ) : config ? (
        <>
          <section className="panel">
            <SectionTitle eyebrow="kiss modem" title="Connection" />
            <div className="p-4 grid grid-cols-1 sm:grid-cols-2 gap-4">
              <TextField
                label="Connection"
                value={connection}
                onChange={setConnection}
                placeholder="serial:///dev/ttyACM0 or tcp://host:port"
              />
              <TextField
                label="Baud rate"
                value={baudRate}
                onChange={setBaudRate}
                placeholder="115200"
              />
            </div>
          </section>

          <section className="panel">
            <SectionTitle eyebrow="rf parameters" title="LoRa Radio" />
            <div className="p-4 grid grid-cols-1 sm:grid-cols-3 lg:grid-cols-5 gap-4">
              <TextField
                label="Frequency (MHz)"
                value={freq}
                onChange={setFreq}
                placeholder="917.375"
              />
              <SelectField
                label="Bandwidth (kHz)"
                value={bw}
                options={BANDWIDTHS.map((b) => ({
                  value: String(b),
                  label: `${b} kHz`,
                }))}
                onChange={setBw}
              />
              <SelectField
                label="Spreading factor"
                value={sf}
                options={Array.from({ length: 8 }, (_, i) => ({
                  value: String(i + 5),
                  label: `SF${i + 5}`,
                }))}
                onChange={setSf}
              />
              <SelectField
                label="Coding rate"
                value={cr}
                options={Array.from({ length: 4 }, (_, i) => ({
                  value: String(i + 5),
                  label: `4/${i + 5}`,
                }))}
                onChange={setCr}
              />
              <TextField
                label="TX power (dBm)"
                value={tx}
                onChange={setTx}
                placeholder="0-22"
              />
            </div>
          </section>

          <section className="panel">
            <SectionTitle eyebrow="process" title="Service" />
            <div className="p-4 grid grid-cols-1 sm:grid-cols-2 gap-4">
              <TextField
                label="Web listen address"
                value={listenAddr}
                onChange={setListenAddr}
                placeholder=":4432"
                hint="applies on next process restart"
              />
              <SelectField
                label="Log level"
                value={logLevel}
                options={LOG_LEVELS.map((l) => ({ value: l, label: l }))}
                onChange={setLogLevel}
                hint="-v / -vv flags override this"
              />
            </div>
          </section>
        </>
      ) : null}
    </div>
  );
}
