// Decoder for a repeater status `errEvents` value. This is the firmware's
// `_err_flags` bitmask (MeshCore/src/Dispatcher.h), NOT a counter: fatal-event
// bits are OR'd in as they occur and only cleared on reboot. Treat it as a set
// of sticky "what has gone wrong since boot" flags — never sum, average, or
// chart a rate of it.

export interface ErrEventFlag {
  bit: number;
  /** Short UI label. */
  label: string;
  /** Firmware constant name, surfaced in tooltips. */
  name: string;
  /** What the flag means. */
  description: string;
}

// Mirror of the ERR_EVENT_* defines in MeshCore/src/Dispatcher.h. Keep in sync
// if upstream adds bits — unknownErrBits() surfaces any we don't yet know.
export const ERR_EVENT_FLAGS: ErrEventFlag[] = [
  {
    bit: 1 << 0,
    label: "Queue full",
    name: "ERR_EVENT_FULL",
    description: "Packet pool exhausted — a packet could not be allocated.",
  },
  {
    bit: 1 << 1,
    label: "CAD timeout",
    name: "ERR_EVENT_CAD_TIMEOUT",
    description:
      "Channel-activity-detection stayed busy too long; the radio may be stuck.",
  },
  {
    bit: 1 << 2,
    label: "RX-start timeout",
    name: "ERR_EVENT_STARTRX_TIMEOUT",
    description: "Radio failed to (re)enter receive mode for over 8 seconds.",
  },
];

const KNOWN_BITS = ERR_EVENT_FLAGS.reduce((acc, f) => acc | f.bit, 0);

/** The flags active in a mask, in bit order. */
export function decodeErrEvents(mask: number): ErrEventFlag[] {
  return ERR_EVENT_FLAGS.filter((f) => (mask & f.bit) !== 0);
}

/** Bits set beyond the flags we know about (e.g. a newer firmware); 0 if none. */
export function unknownErrBits(mask: number): number {
  return mask & ~KNOWN_BITS;
}
