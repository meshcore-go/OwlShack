import catalog from "@/data/cli-catalog.json";

export interface CliCommand {
  command: string;
  hint?: string;
}

export interface CliConfigKey {
  key: string;
  hint?: string;
}

// Curated mirror of the firmware CLI handler
// (~/Data/wesley/MeshCore/src/helpers/CommonCLI.cpp) used by the repeater
// terminal autocomplete. The data lives in data/cli-catalog.json — edit that
// (regenerating from CommonCLI.cpp when upstream adds commands) rather than
// inlining a copy at a call site. Commands ending in a space (e.g. "time ",
// "set name ") intentionally pre-fill the argument position. configKeys are
// ordered by area in the JSON: identity, radio, location, advert, routing,
// duty/airtime, security, diagnostics, bridge, power/ADC.
export const CLI_TOPLEVEL_COMMANDS = catalog.topLevelCommands as CliCommand[];
export const CLI_CONFIG_KEYS = catalog.configKeys as CliConfigKey[];
