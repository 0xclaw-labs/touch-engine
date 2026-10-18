// MX-OPS candidate return consumption (display/adoption guard, no adoption).
//
// Cloud output always lands as a candidate. A late or duplicate receipt never
// moves the adopted pointer; only the merchant's explicit adopt action does.
// Same idempotency key + same digest replays; a different digest on the same
// key is an idempotency conflict.

export interface ReturnReceipt {
  idempotencyKey: string;
  creativeRef: string;
  candidateRef: string;
  contentDigest: string;
  receivedAtUnix: number;
  source: string;
}

export interface ReturnState {
  candidates: ReturnReceipt[];
  adoptedRef: string | null;
}

export interface ReturnResult {
  state: ReturnState;
  replayed: boolean;
  conflict: boolean;
  late: boolean;
  rejected: boolean;
  autoAdopted: false;
}

export const EMPTY_RETURN_STATE: ReturnState = { candidates: [], adoptedRef: null };

function normalizeDigest(value: string): string {
  const trimmed = value.trim().toLowerCase();
  return trimmed.startsWith("sha256:") ? trimmed.slice("sha256:".length) : trimmed;
}

export function acceptReturn(state: ReturnState, incoming: ReturnReceipt): ReturnResult {
  const key = incoming.idempotencyKey.trim();
  const digest = normalizeDigest(incoming.contentDigest);
  if (key === "" || digest === "" || incoming.candidateRef.trim() === "") {
    return { state, replayed: false, conflict: false, late: false, rejected: true, autoAdopted: false };
  }
  const existing = state.candidates.find((item) => item.idempotencyKey.trim() === key);
  if (existing) {
    const sameContent = normalizeDigest(existing.contentDigest) === digest;
    return {
      state,
      replayed: sameContent,
      conflict: !sameContent,
      late: false,
      rejected: false,
      autoAdopted: false,
    };
  }
  const late = state.adoptedRef !== null;
  const normalized: ReturnReceipt = {
    ...incoming,
    idempotencyKey: key,
    contentDigest: `sha256:${digest}`,
    candidateRef: incoming.candidateRef.trim(),
  };
  return {
    // adoptedRef is deliberately carried over untouched: a return never adopts.
    state: { candidates: [...state.candidates, normalized], adoptedRef: state.adoptedRef },
    replayed: false,
    conflict: false,
    late,
    rejected: false,
    autoAdopted: false,
  };
}
