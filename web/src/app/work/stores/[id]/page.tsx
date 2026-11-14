"use client";
import { Suspense } from "react";
import { useParams } from "next/navigation";
import StoreWorkspace from "@/components/work/store-workspace";
export default function StoreWorkspacePage() { const {id}=useParams<{id:string}>(); return <Suspense fallback={<p className="tk-state">正在读取门店工作区</p>}><StoreWorkspace key={id} /></Suspense>; }
