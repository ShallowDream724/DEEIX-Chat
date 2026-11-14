"use client";

import * as React from "react";
import { getSpeechCapabilities, type SpeechCapabilities } from "@/shared/api/speech";
import { useOptionalAuthSession } from "@/shared/auth/auth-session-context";

export function useSpeechCapabilities() {
  const token = useOptionalAuthSession()?.accessToken ?? "";
  const [capabilities, setCapabilities] = React.useState<SpeechCapabilities>({ asr: [], tts: [] });
  React.useEffect(() => {
    setCapabilities({ asr: [], tts: [] });
    if (!token) return;
    const controller = new AbortController();
    void getSpeechCapabilities(token, controller.signal).then(setCapabilities).catch(() => {});
    return () => controller.abort();
  }, [token]);
  return capabilities;
}
