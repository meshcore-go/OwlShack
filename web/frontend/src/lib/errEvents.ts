// `errEvents` is the firmware's sticky `_err_flags` bitmask (Dispatcher.h), cleared only on reboot — never a count or a rate.

export interface ErrEventFlag {
  bit: number;
  label: string;
  name: string;
  description: string;
}

// Mirror of the ERR_EVENT_* defines in MeshCore/src/Dispatcher.h.
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

export function decodeErrEvents(mask: number): ErrEventFlag[] {
  return ERR_EVENT_FLAGS.filter((f) => (mask & f.bit) !== 0);
}

export function unknownErrBits(mask: number): number {
  return mask & ~KNOWN_BITS;
}
