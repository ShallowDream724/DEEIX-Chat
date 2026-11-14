"use client";

import * as React from "react";
import { getSpeechCapabilities, type SpeechCapabilities } from "@/shared/api/speech";
import { useOptionalAuthSession } from "@/shared/auth/auth-session-context";

const EMPTY_CAPABILITIES: SpeechCapabilities = { asr: [], tts: [] };
const CHANGE_EVENT = "deeix:speech-capabilities-changed";
const BROADCAST_KEY = "deeix:speech-capabilities-revision";
type Entry = { value: SpeechCapabilities; token: string; listeners: Set<() => void>; generation: number; completedGeneration: number; pending?: Promise<SpeechCapabilities> };
const entries = new Map<string, Entry>();
function entryFor(token: string, user = token) {
  let entry = entries.get(user);
  if (!entry) { entry = { value: EMPTY_CAPABILITIES, token, listeners: new Set(), generation: 0, completedGeneration: -1 }; entries.set(user, entry); }
  entry.token = token;
  return entry;
}
function refresh(entry: Entry, force = false): Promise<SpeechCapabilities> {
  if (force) entry.generation++;
  if (entry.pending) {
    const targetGeneration = entry.generation;
    return entry.pending.then((value) => entry.completedGeneration < targetGeneration ? refresh(entry) : value,
      (error: unknown) => { if (entry.completedGeneration < targetGeneration) return refresh(entry); throw error; });
  }
  entry.pending = (async () => {
    while (true) {
      const generation = entry.generation;
      let value: SpeechCapabilities;
      try { value = await getSpeechCapabilities(entry.token); }
      catch (error) { if (generation !== entry.generation) continue; entry.completedGeneration = generation; throw error; }
      if (generation !== entry.generation) continue;
      entry.value = value;
      for (const listener of entry.listeners) listener();
      if (generation !== entry.generation) continue;
      entry.completedGeneration = generation;
      return value;
    }
  })().finally(() => { entry.pending = undefined; });
  return entry.pending;
}
/** Recording must use a fresh server-selected source; a failed refresh never falls back to stale configuration. */
export function refreshSpeechCapabilities(accessToken: string, userID = accessToken) { return refresh(entryFor(accessToken, userID), true); }
/** Refresh after an administrator saves speech configuration, including other tabs. */
export function invalidateSpeechCapabilities() {
  if (typeof window === "undefined") return;
  window.dispatchEvent(new Event(CHANGE_EVENT));
  try { window.localStorage.setItem(BROADCAST_KEY, String(Date.now())); } catch {}
}

export function useSpeechCapabilities() {
  const auth = useOptionalAuthSession();
  const token = auth?.accessToken ?? "";
  const user = auth?.user?.publicID || token;
  const entry = React.useMemo(() => {
    if (!user || !token) return null;
    return entryFor(token, user);
  }, [token, user]);
  const subscribe = React.useCallback((listener: () => void) => { entry?.listeners.add(listener); return () => { entry?.listeners.delete(listener); }; }, [entry]);
  const snapshot = React.useCallback(() => entry?.value ?? EMPTY_CAPABILITIES, [entry]);
  const capabilities = React.useSyncExternalStore(subscribe, snapshot, () => EMPTY_CAPABILITIES);
  React.useEffect(() => {
    if (!entry) return;
    const update = () => { if (!document.hidden) void refresh(entry).catch(() => {}); };
    const invalidate = () => { if (!document.hidden) void refresh(entry, true).catch(() => {}); };
    const storage = (event: StorageEvent) => { if (event.key === BROADCAST_KEY) invalidate(); };
    update();
    const timer = window.setInterval(update, 15000);
    window.addEventListener("focus", invalidate);
    window.addEventListener(CHANGE_EVENT, invalidate);
    window.addEventListener("storage", storage);
    document.addEventListener("visibilitychange", update);
    return () => {
      window.clearInterval(timer); window.removeEventListener("focus", invalidate); window.removeEventListener(CHANGE_EVENT, invalidate);
      window.removeEventListener("storage", storage); document.removeEventListener("visibilitychange", update);
      if (!entry.listeners.size) entries.delete(user);
    };
  }, [entry, user]);
  return capabilities;
}
