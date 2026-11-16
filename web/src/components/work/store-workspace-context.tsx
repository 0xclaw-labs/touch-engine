"use client";
import { createContext, useContext } from "react";
import type { WorkspacePanel } from "@/lib/work/store-workspace";
export interface StoreWorkspaceState {
  storeId: string;
  refresh: number;
  saved: () => void;
  open: (panel: WorkspacePanel) => void;
}
export const StoreWorkspaceContext = createContext<StoreWorkspaceState | null>(null);
export const useStoreWorkspace = () => useContext(StoreWorkspaceContext);
