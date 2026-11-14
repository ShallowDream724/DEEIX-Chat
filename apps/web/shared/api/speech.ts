import { authedRequest } from "@/shared/api/authed-client";
import { resolveApiBaseURL } from "@/shared/api/http-client";

export type SpeechASRCapability = { id: string; name: string; streaming: boolean; formats: string[]; sample_rate: number; max_duration_ms: number; max_bytes: number };
export type SpeechTTSCapability = { id: string; name: string; voices: { id: string; name: string }[]; formats: string[]; max_weighted_characters: number; max_text_bytes: number };
export type SpeechCapabilities = { max_recording_duration_ms?: number; asr: SpeechASRCapability[]; tts: SpeechTTSCapability[] };
export type SpeechTranscript = { session_id: string; sequence: number; text: string; final: boolean; duration_ms?: number };
export type SpeechSession = { id: string; ws_url: string; max_duration_ms: number; max_bytes: number };
export type SpeechSynthesis = { id: string; status: string; media_url: string; cache_hit: boolean };

export function getSpeechCapabilities(accessToken: string, signal?: AbortSignal) {
  return authedRequest<SpeechCapabilities>("/api/v1/speech/capabilities", { accessToken, signal });
}
export function transcribeSpeech(accessToken: string, provider: string, draftID: string, requestID: string, audio: Blob, signal: AbortSignal) {
  const body = new FormData();
  body.set("file", audio, "speech.wav");
  body.set("provider", provider);
  body.set("draft_id", draftID);
  body.set("request_id", requestID);
  return authedRequest<SpeechTranscript>("/api/v1/speech/asr/transcriptions", { accessToken, method: "POST", body, signal });
}
export function createSpeechSession(accessToken: string, provider: string, draftID: string, signal: AbortSignal) {
  return authedRequest<SpeechSession>("/api/v1/speech/asr/sessions", { accessToken, method: "POST", body: { provider, draft_id: draftID, format: "opus", sample_rate: 16000 }, signal });
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
