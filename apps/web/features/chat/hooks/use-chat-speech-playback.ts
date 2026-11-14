"use client";
import * as React from "react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { useUserSettings } from "@/entities/user-settings";
import { configureSpeechPlayback, playSpeech, stopSpeechPlayback } from "@/features/chat/lib/speech-playback";
import { subscribeResponseCompletion } from "@/features/chat/lib/response-completion";
import { useOptionalAuthSession } from "@/shared/auth/auth-session-context";
import { useSpeechCapabilities } from "@/shared/hooks/use-speech-capabilities";

export function useChatSpeechPlayback(conversationID: string | null, draftScope: string) {
  const auth = useOptionalAuthSession();
  const { settings } = useUserSettings();
  const capabilities = useSpeechCapabilities();
  const t = useTranslations("chat.speech");
  const scope = conversationID ?? draftScope;
  const latest = React.useRef({ conversationID, settings, scope, t }); latest.current = { conversationID, settings, scope, t };
  React.useLayoutEffect(() => {
    configureSpeechPlayback({ scope, user: String(auth?.user?.publicID ?? auth?.user?.id ?? auth?.accessToken ?? ""), token: auth?.accessToken ?? "", voice: settings["chat.speech_voice"] || "stella", enabled: capabilities.tts.some((item) => item.id === "deepseek") });
  }, [auth?.user?.publicID, auth?.user?.id, auth?.accessToken, capabilities.tts, scope, settings]);
  React.useEffect(() => subscribeResponseCompletion((event) => {
    const value = latest.current;
    if (value.settings["chat.auto_read_replies"] !== "true" || event.conversationID !== (value.conversationID ?? value.scope) || document.hidden) return;
    void playSpeech(event.messageID, event.content, (reason) => toast.error(value.t(reason === "tooLong" ? "tooLong" : "failed")));
  }), []);
  React.useEffect(() => () => stopSpeechPlayback(), []);
}
