import { useEffect, useRef, useState } from "react";
import L from "leaflet";
import markerIcon from "leaflet/dist/images/marker-icon.png";
import markerIcon2x from "leaflet/dist/images/marker-icon-2x.png";
import markerShadow from "leaflet/dist/images/marker-shadow.png";
import {
  Radio,
  Loader2,
  ChevronLeft,
  ChevronRight,
  Check,
  Hash,
  CircleDashed,
} from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { TextField, SelectField } from "@/components/ConfigFields";
import { RadioPresetSelect } from "@/components/RadioPresetSelect";
import { themeTileLayer, useThemeTiles } from "@/lib/leaflet";
import { cn } from "@/lib/utils";
import type { AppConfig, CompanionConfig } from "@/hooks/useConfig";

// Leaflet's default marker icon URLs break under bundlers; rebind once at load.
type MarkerProto = L.Icon.Default & { _getIconUrl?: () => string };
delete (L.Icon.Default.prototype as MarkerProto)._getIconUrl;
L.Icon.Default.mergeOptions({
  iconUrl: markerIcon,
  iconRetinaUrl: markerIcon2x,
  shadowUrl: markerShadow,
});

const BANDWIDTHS = [7.8, 10.4, 15.6, 20.8, 31.25, 41.7, 62.5, 125, 250, 500];

type Step = "welcome" | "radio" | "companion" | "review";
const STEPS: { id: Step; label: string }[] = [
  { id: "welcome", label: "Welcome" },
  { id: "radio", label: "Radio" },
  { id: "companion", label: "Companion" },
  { id: "review", label: "Review" },
];

// Interactive position picker (mirrors RepeaterDetailPage): click to set,
// drag the pin, manual input re-centres.
function PositionMap({
  lat,
  lon,
  onPick,
}: {
  lat: number;
  lon: number;
  onPick: (lat: number, lon: number) => void;
}) {
  const containerRef = useRef<HTMLDivElement>(null);
  const mapRef = useRef<L.Map | null>(null);
  const markerRef = useRef<L.Marker | null>(null);
  const tileRef = useRef<L.TileLayer | null>(null);
  const onPickRef = useRef(onPick);
  onPickRef.current = onPick;

  const validLat = Number.isFinite(lat);
  const validLon = Number.isFinite(lon);
  const initialLat = validLat ? lat : 0;
  const initialLon = validLon ? lon : 0;

  useEffect(() => {
    if (!containerRef.current || mapRef.current) return;
    const map = L.map(containerRef.current, {
      zoomControl: true,
      attributionControl: true,
    }).setView([initialLat, initialLon], validLat && validLon ? 13 : 2);
    tileRef.current = themeTileLayer().addTo(map);

    map.on("click", (e) => {
      onPickRef.current(e.latlng.lat, e.latlng.lng);
    });

    mapRef.current = map;

    return () => {
      map.remove();
      mapRef.current = null;
      tileRef.current = null;
      markerRef.current = null;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useThemeTiles(mapRef, tileRef);

  useEffect(() => {
    const map = mapRef.current;
    if (!map) return;
    if (!validLat || !validLon) {
      if (markerRef.current) {
        markerRef.current.remove();
        markerRef.current = null;
      }
      return;
    }
    if (!markerRef.current) {
      markerRef.current = L.marker([lat, lon], { draggable: true })
        .addTo(map)
        .on("dragend", (e) => {
          const m = e.target as L.Marker;
          const { lat: la, lng: ln } = m.getLatLng();
          onPickRef.current(la, ln);
        });
      map.setView([lat, lon], Math.max(map.getZoom(), 12));
    } else {
      markerRef.current.setLatLng([lat, lon]);
    }
  }, [lat, lon, validLat, validLon]);

  return (
    <div className="space-y-1">
      <Label className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">
        Pick from map · click or drag pin
      </Label>
      <div ref={containerRef} className="h-56 border border-border bg-muted" />
    </div>
  );
}

function StepDots({ current }: { current: Step }) {
  const activeIdx = STEPS.findIndex((s) => s.id === current);
  return (
    <div className="flex items-center gap-2 font-mono text-[10px] uppercase tracking-[0.12em]">
      {STEPS.map((s, i) => (
        <div key={s.id} className="flex items-center gap-2">
          <span
            className={cn(
              "inline-flex items-center gap-1.5",
              i === activeIdx
                ? "text-primary"
                : i < activeIdx
                  ? "text-muted-foreground"
                  : "text-muted-foreground/40",
            )}
          >
            <span
              className={cn(
                "grid size-4 place-items-center border text-[9px]",
                i === activeIdx
                  ? "border-primary text-primary"
                  : i < activeIdx
                    ? "border-muted-foreground/50 text-muted-foreground"
                    : "border-muted-foreground/30 text-muted-foreground/40",
              )}
            >
              {i < activeIdx ? <Check className="size-2.5" /> : i + 1}
            </span>
            <span className="hidden sm:inline">{s.label}</span>
          </span>
          {i < STEPS.length - 1 && (
            <span className="text-muted-foreground/30">/</span>
          )}
        </div>
      ))}
    </div>
  );
}

export function SetupWizard({
  config,
  save,
  reload,
}: {
  config: AppConfig;
  save: (next: AppConfig) => Promise<boolean>;
  reload: () => void;
}) {
  const [step, setStep] = useState<Step>("welcome");
  const [busy, setBusy] = useState(false);

  // Radio (pre-filled from the bootstrapped defaults).
  const [connection, setConnection] = useState(
    config.connection ?? "serial:///dev/ttyACM0",
  );
  const [baudRate, setBaudRate] = useState(String(config.baudRate ?? 115200));
  const [freq, setFreq] = useState(
    config.freq != null ? String(config.freq) : "917.375",
  );
  const [bw, setBw] = useState(config.bw != null ? String(config.bw) : "62.5");
  const [sf, setSf] = useState(config.sf != null ? String(config.sf) : "7");
  const [cr, setCr] = useState(config.cr != null ? String(config.cr) : "8");
  const [tx, setTx] = useState(config.tx != null ? String(config.tx) : "22");

  // Companion.
  const [skipCompanion, setSkipCompanion] = useState(false);
  const [name, setName] = useState("");
  const [lat, setLat] = useState("");
  const [lon, setLon] = useState("");
  const [showAdvanced, setShowAdvanced] = useState(false);
  const [privateKey, setPrivateKey] = useState("");
  const [advertInterval, setAdvertInterval] = useState("");

  const radioValid =
    connection.trim() !== "" &&
    Number.isFinite(parseFloat(freq)) &&
    bw !== "" &&
    sf !== "" &&
    cr !== "" &&
    Number.isFinite(parseInt(tx, 10));

  const finish = async () => {
    setBusy(true);
    const next = structuredClone(config) as AppConfig;
    next.connection = connection.trim();
    next.baudRate = parseInt(baudRate, 10) || 115200;
    next.freq = parseFloat(freq) || null;
    next.bw = parseFloat(bw) || null;
    next.sf = parseInt(sf, 10) || null;
    next.cr = parseInt(cr, 10) || null;
    next.tx = tx === "" ? null : parseInt(tx, 10);
    next.setupComplete = true;

    if (!skipCompanion && name.trim()) {
      const comp: CompanionConfig = {
        name: name.trim(),
        latitude: lat === "" ? null : parseFloat(lat) || 0,
        longitude: lon === "" ? null : parseFloat(lon) || 0,
        channels: [{ name: "Public" }],
      };
      if (privateKey.trim()) comp.privateKey = privateKey.trim();
      if (advertInterval.trim())
        comp.advertInterval = parseInt(advertInterval, 10) || 0;
      next.companions = [...(next.companions ?? []), comp];
    }

    const ok = await save(next);
    setBusy(false);
    // Once the reload settles, the re-fetched config carries setupComplete and
    // the gate in App.tsx unmounts this wizard.
    if (ok) window.setTimeout(reload, 1600);
  };

  return (
    <Dialog open onOpenChange={() => {}}>
      <DialogContent
        showCloseButton={false}
        className="rounded-none border-border sm:max-w-2xl max-h-[88vh] overflow-y-auto gap-5"
      >
        <DialogHeader className="gap-3">
          <div className="flex items-center gap-2.5">
            <div className="relative grid size-8 shrink-0 place-items-center rounded-sm border border-primary/30 bg-primary/10">
              <Radio className="size-4 text-primary" strokeWidth={2} />
            </div>
            <div className="flex flex-col leading-tight">
              <span className="label-overline">OwlShack · first-run setup</span>
              <DialogTitle className="font-mono text-sm uppercase tracking-widest">
                Get on the air
              </DialogTitle>
            </div>
          </div>
          <StepDots current={step} />
        </DialogHeader>

        {step === "welcome" && (
          <div className="space-y-4">
            <p className="font-mono text-sm leading-relaxed text-muted-foreground">
              Welcome. Two quick steps: configure the radio, then create your
              first companion (the identity you run on the mesh).
            </p>
            <p className="font-mono text-xs leading-relaxed text-muted-foreground/70">
              Nothing is broadcast until you create a companion. You can skip
              the companion step to just watch live mesh traffic in this
              console; the radio still needs to be configured to connect.
            </p>
          </div>
        )}

        {step === "radio" && (
          <div className="space-y-5">
            <div className="space-y-3">
              <span className="label-overline">kiss modem · connection</span>
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
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
            </div>
            <div className="space-y-3">
              <span className="label-overline">rf parameters · lora radio</span>
              <RadioPresetSelect
                freq={freq}
                bw={bw}
                sf={sf}
                cr={cr}
                onApply={(p) => {
                  setFreq(String(p.freq));
                  setBw(String(p.bw));
                  setSf(String(p.sf));
                  setCr(String(p.cr));
                }}
              />
              <div className="grid grid-cols-1 sm:grid-cols-3 gap-4">
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
            </div>
            {!radioValid && (
              <p className="font-mono text-[10px] text-warning">
                Fill in the connection and all RF parameters to continue.
              </p>
            )}
          </div>
        )}

        {step === "companion" && (
          <div className="space-y-5">
            <TextField
              label="Companion name"
              value={name}
              onChange={setName}
              placeholder="e.g. KO6XYZ-1 or Base Station"
              hint="shown on the mesh and in adverts · choose something recognisable"
            />
            <div className="grid grid-cols-2 gap-4">
              <TextField
                label="Latitude"
                value={lat}
                onChange={setLat}
                placeholder="blank = no position"
              />
              <TextField
                label="Longitude"
                value={lon}
                onChange={setLon}
                placeholder="blank = no position"
              />
            </div>
            <PositionMap
              lat={parseFloat(lat)}
              lon={parseFloat(lon)}
              onPick={(la, lo) => {
                setLat(la.toFixed(6));
                setLon(lo.toFixed(6));
              }}
            />
            <div className="flex items-center gap-1.5 font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground/70">
              <Hash className="size-3 text-primary" />
              joins the <span className="text-foreground">Public</span> channel
              automatically
            </div>

            {showAdvanced ? (
              <div className="space-y-4 border-t border-border pt-4">
                <span className="label-overline">advanced · optional</span>
                <TextField
                  label="Private key"
                  type="password"
                  value={privateKey}
                  onChange={setPrivateKey}
                  placeholder="blank = generate a new identity"
                  hint="64-hex ed25519 seed · leave blank to auto-generate"
                />
                <TextField
                  label="Advert interval (s)"
                  value={advertInterval}
                  onChange={setAdvertInterval}
                  placeholder="blank = 86400 (daily)"
                  hint="0 = never advertise"
                />
              </div>
            ) : (
              <button
                type="button"
                onClick={() => setShowAdvanced(true)}
                className="font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground hover:text-foreground"
              >
                + advanced (identity key, advert interval)
              </button>
            )}
          </div>
        )}

        {step === "review" && (
          <div className="space-y-4">
            <div className="border border-border bg-card divide-y divide-border font-mono text-xs">
              <div className="flex justify-between gap-4 px-3 py-2">
                <span className="text-muted-foreground uppercase tracking-[0.08em]">
                  Connection
                </span>
                <span className="truncate text-right">{connection}</span>
              </div>
              <div className="flex justify-between gap-4 px-3 py-2">
                <span className="text-muted-foreground uppercase tracking-[0.08em]">
                  Radio
                </span>
                <span className="tabular-nums text-right">
                  {freq} MHz · {bw} kHz · SF{sf} · 4/{cr} · {tx} dBm
                </span>
              </div>
              <div className="flex justify-between gap-4 px-3 py-2">
                <span className="text-muted-foreground uppercase tracking-[0.08em]">
                  Companion
                </span>
                <span className="text-right">
                  {skipCompanion || !name.trim() ? (
                    <span className="inline-flex items-center gap-1.5 text-muted-foreground">
                      <CircleDashed className="size-3" /> view-only
                    </span>
                  ) : (
                    <span>
                      {name.trim()}
                      <span className="text-muted-foreground">
                        {" "}
                        · #Public
                        {lat && lon ? ` · ${lat}, ${lon}` : ""}
                      </span>
                    </span>
                  )}
                </span>
              </div>
            </div>
            <p className="font-mono text-[10px] text-muted-foreground/70">
              {skipCompanion || !name.trim()
                ? "No companion is created. You can watch live mesh traffic in this console, but you won't appear on the mesh, and MQTT feeds (LetsMesh, CoreScope) need a companion. Add one any time from the Companions page."
                : "The companion starts immediately and begins advertising on the mesh."}
            </p>
          </div>
        )}

        {/* Footer / navigation */}
        <div className="flex items-center justify-between gap-2 border-t border-border pt-4">
          <div>
            {step !== "welcome" && (
              <Button
                variant="ghost"
                size="sm"
                disabled={busy}
                onClick={() =>
                  setStep(
                    step === "radio"
                      ? "welcome"
                      : step === "companion"
                        ? "radio"
                        : "companion",
                  )
                }
                className="rounded-none font-mono text-[11px] uppercase tracking-[0.12em]"
              >
                <ChevronLeft className="size-3.5" /> back
              </Button>
            )}
          </div>

          <div className="flex items-center gap-2">
            {step === "companion" && (
              <Button
                variant="ghost"
                size="sm"
                disabled={busy}
                onClick={() => {
                  setSkipCompanion(true);
                  setStep("review");
                }}
                className="rounded-none font-mono text-[11px] uppercase tracking-[0.12em] text-muted-foreground"
              >
                skip · view-only
              </Button>
            )}

            {step === "welcome" && (
              <Button
                size="sm"
                onClick={() => setStep("radio")}
                className="rounded-none font-mono text-[11px] uppercase tracking-[0.12em]"
              >
                begin <ChevronRight className="size-3.5" />
              </Button>
            )}
            {step === "radio" && (
              <Button
                size="sm"
                disabled={!radioValid}
                onClick={() => setStep("companion")}
                className="rounded-none font-mono text-[11px] uppercase tracking-[0.12em]"
              >
                next <ChevronRight className="size-3.5" />
              </Button>
            )}
            {step === "companion" && (
              <Button
                size="sm"
                disabled={!name.trim()}
                onClick={() => {
                  setSkipCompanion(false);
                  setStep("review");
                }}
                className="rounded-none font-mono text-[11px] uppercase tracking-[0.12em]"
              >
                review <ChevronRight className="size-3.5" />
              </Button>
            )}
            {step === "review" && (
              <Button
                size="sm"
                disabled={busy}
                onClick={finish}
                className="rounded-none font-mono text-[11px] uppercase tracking-[0.12em]"
              >
                {busy ? (
                  <Loader2 className="size-3.5 animate-spin" />
                ) : (
                  <Check className="size-3.5" />
                )}
                finish
              </Button>
            )}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
