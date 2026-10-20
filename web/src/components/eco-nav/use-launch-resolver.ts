"use client";

import { useEffect, useState } from "react";
import type { LaunchAvailability } from "@/lib/eco-nav/model";

/**
 * HUI-2628: the browser only needs to know whether a target is registered.
 * It does not receive or construct launch URLs; the switch BFF mints them.
 */
export function useLaunchTargets(): LaunchAvailability {
  const [targets, setTargets] = useState<Record<string, boolean>>({});
  useEffect(() => {
    let alive = true;
    fetch("/api/eco-nav/launches")
      .then(async (res) => {
        if (!res.ok) throw new Error("launches");
        return (await res.json()) as { targets?: unknown };
      })
      .then((data) => {
        if (!alive) return;
        const list = Array.isArray(data.targets) ? data.targets.filter((item): item is string => typeof item === "string") : [];
        setTargets(Object.fromEntries(list.map((id) => [id, true])));
      })
      .catch(() => {
        if (alive) setTargets({});
      });
    return () => {
      alive = false;
    };
  }, []);
  return (targetId) => targets[targetId] === true;
}
