import { useState } from "react";
import { SelectField } from "@/components/ConfigFields";
import RADIO_PRESETS from "@/data/radio-presets.json";

export interface RadioPreset {
  name: string;
  freq: number;
  bw: number;
  sf: number;
  cr: number;
  // Shown in the label but not applied: it's per-node routing, not shared RF config.
  pathHashSize?: number;
}

const PRESETS = RADIO_PRESETS as RadioPreset[];

function matches(p: RadioPreset, freq: string, bw: string, sf: string, cr: string): boolean {
  return (
    Math.abs(parseFloat(freq) - p.freq) < 1e-6 &&
    Math.abs(parseFloat(bw) - p.bw) < 1e-6 &&
    parseInt(sf, 10) === p.sf &&
    parseInt(cr, 10) === p.cr
  );
}

// Generated from https://api.meshcore.nz/api/v1/config (suggested_radio_settings.entries); an explicit pick is kept only to disambiguate presets sharing RF values.
export function RadioPresetSelect({
  freq,
  bw,
  sf,
  cr,
  onApply,
  hint = "community presets · fills the RF fields below",
}: {
  freq: string;
  bw: string;
  sf: string;
  cr: string;
  onApply: (p: RadioPreset) => void;
  hint?: React.ReactNode;
}) {
  const [picked, setPicked] = useState("");

  const pickedPreset = PRESETS.find((p) => p.name === picked);
  const value =
    pickedPreset && matches(pickedPreset, freq, bw, sf, cr)
      ? picked
      : (PRESETS.find((p) => matches(p, freq, bw, sf, cr))?.name ?? "custom");

  const apply = (name: string) => {
    setPicked(name);
    const p = PRESETS.find((x) => x.name === name);
    if (p) onApply(p);
  };

  return (
    <SelectField
      label="Preset"
      value={value}
      options={[
        { value: "custom", label: "Custom" },
        ...PRESETS.map((p) => ({
          value: p.name,
          label:
            `${p.name} · ${p.freq} / SF${p.sf} / BW${p.bw} / CR${p.cr}` +
            (p.pathHashSize ? ` / ${p.pathHashSize}B` : ""),
        })),
      ]}
      onChange={apply}
      hint={hint}
    />
  );
}
