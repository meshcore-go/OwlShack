import { useState } from "react";
import { SelectField } from "@/components/ConfigFields";
import RADIO_PRESETS from "@/data/radio-presets.json";

export interface RadioPreset {
  name: string;
  freq: number;
  bw: number;
  sf: number;
  cr: number;
  // Path hash width the region runs, in bytes. Shown in the label but not
  // applied: it's a per-node routing setting (the repeater's Path hash mode),
  // not part of the shared RF config these fields write.
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

// Community radio presets, generated from the official app's config feed
// (https://api.meshcore.nz/api/v1/config → suggested_radio_settings.entries).
// The selection is derived from the live RF fields, so manually typing values
// that match a preset shows that preset; anything else shows "Custom". An
// explicit pick is remembered only to disambiguate presets that share the same
// RF values (e.g. Brazil vs Australia: SA, WA), so picking one doesn't visibly
// snap to whichever appears first in the list. Connection/TX are untouched.
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
