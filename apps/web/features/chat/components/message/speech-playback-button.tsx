"use client";
import * as React from "react";
import { LoaderCircle, Pause, Play, Volume2 } from "lucide-react";
import { useTranslations } from "next-intl";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { getServerSpeechPlaybackState, getSpeechPlaybackState, isSpeechPlaybackAvailable, playSpeech, reconcileSpeechPlaybackMessage, subscribeSpeechPlayback } from "@/features/chat/lib/speech-playback";

export function SpeechPlaybackButton({ messageID, text, disabled }: { messageID: string; text: string; disabled: boolean }) {
  const t = useTranslations("chat.speech");
  const state = React.useSyncExternalStore(subscribeSpeechPlayback, getSpeechPlaybackState, getServerSpeechPlaybackState);
  React.useLayoutEffect(() => { reconcileSpeechPlaybackMessage(messageID, text, disabled); }, [messageID, text, disabled]);
  const status = state.messageID === messageID ? state.status : "idle";
  if (!isSpeechPlaybackAvailable()) return null;
  const label = status === "playing" ? t("pause") : status === "paused" ? t("resume") : status === "error" ? t("retry") : t("play");
  return <Tooltip><TooltipTrigger asChild>
    <Button type="button" variant="ghost" size="icon-sm" className="text-muted-foreground [&_svg]:size-3.5" disabled={disabled || !text.trim() || status === "loading"} aria-label={label} onClick={() => void playSpeech(messageID, text, (reason) => toast.error(t(reason === "tooLong" ? "tooLong" : "failed")), { manual: true })}>
      {status === "loading" ? <LoaderCircle className="animate-spin" /> : status === "playing" ? <Pause /> : status === "paused" ? <Play /> : <Volume2 />}
    </Button>
  </TooltipTrigger><TooltipContent>{status === "loading" ? t("loading") : label}</TooltipContent></Tooltip>;
}
