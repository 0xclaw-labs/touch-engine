// Reuse a request only for the same one-time handoff. Changing h on a mounted
// continuation must never observe the old source's response.
export function createHandoffRequest<T>(issue: (h: string) => Promise<T>) {
    let current: {
        h: string;
        promise: Promise<T>;
    } | null = null;
    return (h: string): Promise<T> => { if (!current || current.h !== h)
        current = { h, promise: issue(h) }; return current.promise; };
}
export function continuationDestination(data: {
    known?: boolean;
    path?: string;
}): string {
    return data.known === false ? "/" : data.path || "/";
}
