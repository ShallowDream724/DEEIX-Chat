import { authedRequest } from "@/shared/api/authed-client";
import { resolveApiBaseURL } from "@/shared/api/http-client";

export type SpeechASRCapability = { id: string; name: string; streaming: boolean; formats: string[]; sample_rate: number; max_duration_ms: number; max_bytes: number };
export type SpeechTTSCapability = { id: string; name: string; voices: { id: string; name: string }[]; formats: string[]; max_weighted_characters: number; max_text_bytes: number };
export type SpeechCapabilities = { selected_input_provider?: string; max_recording_duration_ms?: number; asr: SpeechASRCapability[]; tts: SpeechTTSCapability[] };
export type SpeechTranscript = { session_id: string; sequence: number; text: string; final: boolean; duration_ms?: number };
export type SpeechSession = { id: string; ws_url?: string; provider?: string; session_token?: string; max_duration_ms: number; max_bytes: number };
export type SpeechSynthesis = { id: string; status: string; media_url: string; cache_hit: boolean };

export function getSpeechCapabilities(accessToken: string, signal?: AbortSignal) {
  return authedRequest<SpeechCapabilities>("/api/v1/speech/capabilities", { accessToken, signal });
}
export function transcribeSpeech(accessToken: string, provider: string, draftID: string, requestID: string, audio: Blob, signal: AbortSignal, session?: SpeechSession) {
  const body = new FormData();
  body.set("file", audio, "speech.wav");
  body.set("provider", provider);
  body.set("draft_id", draftID);
  body.set("request_id", requestID);
  if (session) { body.set("session_id", session.id); if (session.session_token) body.set("session_token", session.session_token); }
  return authedRequest<SpeechTranscript>("/api/v1/speech/asr/transcriptions", { accessToken, method: "POST", body, signal });
}
export function createSpeechSession(accessToken: string, provider: string, draftID: string, signal: AbortSignal, format: "opus" | "wav" = "opus") {
  return authedRequest<SpeechSession>("/api/v1/speech/asr/sessions", { accessToken, method: "POST", body: { provider, draft_id: draftID, format, sample_rate: 16000 }, signal });
}
export function cancelSpeechSession(accessToken: string, id: string) {
  return authedRequest<unknown>(`/api/v1/speech/asr/sessions/${encodeURIComponent(id)}`, { accessToken, method: "DELETE", keepalive: true }, false);
}
export function synthesizeSpeech(accessToken: string, text: string, voice: string, requestID: string, retry = false) {
  return authedRequest<SpeechSynthesis>("/api/v1/speech/tts", { accessToken, method: "POST", body: { provider: "deepseek", text, voice, format: "wav", request_id: requestID, retry } }, false);
}
export function getSpeechSynthesis(accessToken: string, id: string) {
  return authedRequest<SpeechSynthesis>(`/api/v1/speech/tts/${encodeURIComponent(id)}`, { accessToken });
}
export function speechResourceURL(path: string) {
  return new URL(path, `${resolveApiBaseURL() || window.location.origin}/`).href;
}
