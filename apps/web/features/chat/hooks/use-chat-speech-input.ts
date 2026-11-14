"use client";
import * as React from "react";
import { useUserSettings } from "@/entities/user-settings";
import { useSpeechCapabilities } from "@/shared/hooks/use-speech-capabilities";
import { createSpeechOpusEncoder, pcm16Frame, prepareSpeechCaptureContext, speechWav, startSpeechCapture, supportsSpeechOpus, type SpeechCapture } from "@/features/chat/lib/speech-capture";
import { SpeechDraftSegment } from "@/features/chat/lib/speech-draft";
import { beginRecordingActivity } from "@/shared/lib/media-activity";
import { createSpeechSession, speechResourceURL, transcribeSpeech } from "@/shared/api/speech";
import { useOptionalAuthSession } from "@/shared/auth/auth-session-context";

type Recognition = {
  continuous: boolean; interimResults: boolean; lang: string;
  onend: (() => void) | null; onstart: (() => void) | null;
  onerror: ((event: { error: string }) => void) | null;
  onresult: ((event: { results: { length: number; item: (index: number) => { isFinal: boolean; length: number; item: (index: number) => { transcript: string } } } }) => void) | null;
  start: () => void; stop: () => void; abort?: () => void;
};
type RecognitionWindow = Window & { SpeechRecognition?: new () => Recognition; webkitSpeechRecognition?: new () => Recognition };
export type SpeechInputStatus = "idle" | "starting" | "listening" | "transcribing";
export type SpeechInputErrorCode = "audioUnavailable" | "interrupted" | "languageUnsupported" | "network" | "noSpeech" | "permissionDenied" | "serviceUnavailable" | "startFailed" | "unavailable";
type Session = {
  id: string; scope: string; controller: AbortController; segment: SpeechDraftSegment;
  userID: string; accessToken: string; providerID: string;
  cancelled: boolean; stopping: boolean; failed: boolean; sequence: number; samples: number;
  maxDuration: number; frames: Int16Array<ArrayBuffer>[]; capture?: SpeechCapture;
  captureContext?: AudioContext;
  activity?: ReturnType<typeof beginRecordingActivity>; socket?: WebSocket;
  encoder?: Awaited<ReturnType<typeof createSpeechOpusEncoder>>; recognition?: Recognition;
  finalize?: Promise<boolean>; completed: Promise<boolean>; resolveCompleted: (success: boolean) => void;
};
const browserError: Record<string, SpeechInputErrorCode> = { "audio-capture": "audioUnavailable", "language-not-supported": "languageUnsupported", network: "network", "not-allowed": "permissionDenied", "service-not-allowed": "serviceUnavailable" };
function release(session: Session) {
  session.capture?.cancel(); session.activity?.stop(); session.controller.abort(); session.socket?.close();
  if (session.captureContext?.state !== "closed") void session.captureContext?.close().catch(() => {});
  try { session.encoder?.close(); } catch {}
  if (session.recognition) { session.recognition.onend = null; session.recognition.onresult = null; session.recognition.onerror = null; session.recognition.abort?.(); }
  session.frames = [];
}

export function useChatSpeechInput(params: {
  draft: string; scopeKey: string; language: string; listeningPlaceholder: string;
  onDraftChange: (value: string) => void; onError: (error: SpeechInputErrorCode) => void;
  placeholder: string; startingPlaceholder: string;
}) {
  const latest = React.useRef(params); latest.current = params;
  const auth = useOptionalAuthSession();
  const token = auth?.accessToken ?? "";
  const userID = auth?.user?.publicID ?? "";
  const userIDRef = React.useRef(userID); userIDRef.current = userID;
  const { settings } = useUserSettings();
  const capabilities = useSpeechCapabilities();
  const providerID = settings["chat.speech_input_provider"] || "browser";
  const capability = capabilities.asr.find((item) => item.id === providerID);
  const [browserSupported, setBrowserSupported] = React.useState(false);
  const [captureSupported, setCaptureSupported] = React.useState(false);
  const [opusSupported, setOpusSupported] = React.useState(false);
  const [status, setStatus] = React.useState<SpeechInputStatus>("idle");
  const [elapsedMS, setElapsedMS] = React.useState(0);
  const [maxDurationMS, setMaxDurationMS] = React.useState(120000);
  const sessionRef = React.useRef<Session | null>(null);
  const sendPendingRef = React.useRef(false);
  const current = (session: Session) => sessionRef.current === session && !session.cancelled && latest.current.scopeKey === session.scope && userIDRef.current === session.userID;
  const update = (session: Session, text: string) => {
    if (!current(session)) return;
    session.segment.reconcile(latest.current.draft);
    const next = session.segment.update(text);
    latest.current = { ...latest.current, draft: next };
    latest.current.onDraftChange(next);
  };
  const complete = (session: Session, success: boolean) => {
    const visible = current(session);
    if (visible) sessionRef.current = null;
    session.failed ||= !success;
    session.resolveCompleted(success && !session.failed);
    release(session);
    if (visible) setStatus("idle");
  };
  const fail = (session: Session, error: SpeechInputErrorCode) => {
    if (!current(session)) return;
    latest.current.onError(error); complete(session, false);
  };
  const cancel = React.useCallback((restore = true) => {
    const session = sessionRef.current;
    if (!session) return;
    if (restore && latest.current.scopeKey === session.scope) {
      session.segment.reconcile(latest.current.draft);
      latest.current.onDraftChange(session.segment.cancel());
    }
    session.cancelled = true;
    try { session.socket?.send(JSON.stringify({ type: "cancel" })); } catch {}
    session.resolveCompleted(false); release(session); sessionRef.current = null; sendPendingRef.current = false; setStatus("idle");
  }, []);

  const stop = async (): Promise<boolean> => {
    const session = sessionRef.current;
    if (!session) return true;
    if (session.finalize) return session.finalize;
    session.stopping = true; setStatus("transcribing");
    session.finalize = (async () => {
      try {
        if (session.recognition) session.recognition.stop();
        else {
          await session.capture?.stop(); session.activity?.stop();
          if (!current(session)) return false;
          if (session.socket) { await session.encoder?.flush(); session.socket.send(JSON.stringify({ type: "finish" })); }
          else {
            if (session.samples === 0) { fail(session, "noSpeech"); return false; }
            const timeout = setTimeout(() => { if (current(session)) fail(session, "network"); }, 60000);
            let result: Awaited<ReturnType<typeof transcribeSpeech>>;
            try { result = await transcribeSpeech(session.accessToken, session.providerID, session.scope, session.id, speechWav(session.frames, session.samples), session.controller.signal); }
            finally { clearTimeout(timeout); }
            session.frames = [];
            if (!current(session)) return false;
            if (!result.text.trim()) { fail(session, "noSpeech"); return false; }
            update(session, result.text); complete(session, true);
          }
        }
        let timer: ReturnType<typeof setTimeout> | undefined;
        try { return await Promise.race([session.completed, new Promise<boolean>((resolve) => { timer = setTimeout(() => { fail(session, "network"); resolve(false); }, 20000); })]); }
        finally { clearTimeout(timer); }
      } catch { if (current(session)) fail(session, "network"); return false; }
    })();
    return session.finalize;
  };
  const stopRef = React.useRef(stop); stopRef.current = stop;
  React.useEffect(() => {
    // Type assertion: lib.dom omits the browser's optional Web Speech constructors.
    const browser = window as RecognitionWindow;
    setBrowserSupported(window.isSecureContext && Boolean(browser.SpeechRecognition ?? browser.webkitSpeechRecognition));
    setCaptureSupported(window.isSecureContext && Boolean(navigator.mediaDevices && window.AudioWorkletNode));
    setOpusSupported(supportsSpeechOpus());
    return () => cancel(false);
  }, [cancel]);
  React.useEffect(() => { if (sessionRef.current?.scope !== params.scopeKey) cancel(false); }, [params.scopeKey, cancel]);
  React.useEffect(() => { if (sessionRef.current?.userID !== userID) cancel(false); }, [userID, cancel]);
  React.useEffect(() => { sessionRef.current?.segment.reconcile(params.draft); }, [params.draft]);

  const supported = providerID === "browser" ? browserSupported : Boolean(capability && captureSupported && (!capability.streaming || opusSupported));
  const start = async () => {
    if (sessionRef.current || sendPendingRef.current) return;
    if (!supported) { latest.current.onError("unavailable"); return; }
    let resolveCompleted: (success: boolean) => void = () => {};
    const completed = new Promise<boolean>((resolve) => { resolveCompleted = resolve; });
    const duration = Math.min(capabilities.max_recording_duration_ms || 120000, capability?.max_duration_ms || Infinity);
    const maxSamples = Math.max(1, Math.floor(Math.min(duration * 16, capability?.max_bytes && !capability.streaming ? (capability.max_bytes - 44) / 2 : Infinity)));
    const session: Session = { id: crypto.randomUUID(), scope: latest.current.scopeKey, userID, accessToken: token, providerID, controller: new AbortController(), segment: new SpeechDraftSegment(latest.current.draft), cancelled: false, stopping: false, failed: false, sequence: -1, samples: 0, maxDuration: maxSamples / 16, frames: [], completed, resolveCompleted };
    sessionRef.current = session; setElapsedMS(0); setMaxDurationMS(session.maxDuration); setStatus("starting");
    session.activity = beginRecordingActivity(session.id);
    try {
      if (providerID === "browser") {
        // Type assertion: lib.dom omits the browser's optional Web Speech constructors.
        const browser = window as RecognitionWindow;
        const Constructor = browser.SpeechRecognition ?? browser.webkitSpeechRecognition;
        if (!Constructor) throw new Error("unavailable");
        let started: number | null = null;
        let prefix = ""; let lastText = ""; let emptyRestarts = 0;
        const startRecognition = () => {
          if (!current(session) || session.stopping) return;
          const recognition = new Constructor(); session.recognition = recognition;
          recognition.continuous = true; recognition.interimResults = true; recognition.lang = latest.current.language;
          recognition.onstart = () => { if (current(session) && !session.stopping) { started ??= performance.now(); setStatus("listening"); } };
          recognition.onresult = (event) => {
            if (!current(session) || session.recognition !== recognition) return;
            const pieces: string[] = [];
            for (let i = 0; i < event.results.length; i++) { const result = event.results.item(i); if (result.length) pieces.push(result.item(0).transcript); }
            lastText = pieces.join(" ").trim(); update(session, [prefix, lastText].filter(Boolean).join(" "));
          };
          recognition.onerror = ({ error }) => { if (error !== "no-speech" && error !== "aborted") fail(session, browserError[error] ?? "unavailable"); };
          recognition.onend = () => {
            if (!current(session) || session.recognition !== recognition) return;
            if (session.stopping) { if ((prefix + lastText).trim()) complete(session, true); else fail(session, "noSpeech"); return; }
            if (lastText) { prefix = [prefix, lastText].filter(Boolean).join(" "); lastText = ""; emptyRestarts = 0; }
            else if (++emptyRestarts > 2) { fail(session, "noSpeech"); return; }
            startRecognition();
          };
          recognition.start();
        };
        startRecognition();
        const timer = setInterval(() => {
          if (!current(session)) { clearInterval(timer); return; }
          const elapsed = started === null ? 0 : Math.min(session.maxDuration, performance.now() - started); setElapsedMS(elapsed);
          if (elapsed >= session.maxDuration) { clearInterval(timer); void stopRef.current(); }
        }, 200);
      } else {
        session.captureContext = prepareSpeechCaptureContext();
        if (capability?.streaming) {
          const remote = await createSpeechSession(token, providerID, session.scope, session.controller.signal);
          if (!current(session)) return;
          session.maxDuration = Math.min(session.maxDuration, remote.max_duration_ms || Infinity); setMaxDurationMS(session.maxDuration);
          const url = new URL(speechResourceURL(remote.ws_url)); url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
          const socket = new WebSocket(url); session.socket = socket;
          await new Promise<void>((resolve, reject) => {
            const timer = setTimeout(() => reject(new Error("handshake timeout")), 15000);
            const abort = () => { clearTimeout(timer); reject(new DOMException("Aborted", "AbortError")); };
            session.controller.signal.addEventListener("abort", abort, { once: true });
            socket.onmessage = ({ data }) => {
              if (!current(session) || typeof data !== "string") return;
              let event: { type: string; session_id: string; sequence: number; text?: string };
              try { event = JSON.parse(data); } catch { fail(session, "network"); return; }
              if (event.session_id && event.session_id !== remote.id) return;
              if (event.type === "ready") { clearTimeout(timer); session.controller.signal.removeEventListener("abort", abort); resolve(); }
              if (event.type === "error") { clearTimeout(timer); reject(new Error("ASR failed")); fail(session, "serviceUnavailable"); }
              if ((event.type === "transcript" || event.type === "final") && event.sequence > session.sequence) { session.sequence = event.sequence; update(session, event.text ?? ""); }
              if (event.type === "final" && event.sequence >= session.sequence) { if (event.text?.trim()) complete(session, true); else fail(session, "noSpeech"); }
            };
            socket.onerror = () => { clearTimeout(timer); reject(new Error("websocket error")); fail(session, "network"); };
            socket.onclose = () => { if (current(session)) { clearTimeout(timer); reject(new Error("websocket closed")); fail(session, "interrupted"); } };
          });
          let sentBytes = 0;
          session.encoder = await createSpeechOpusEncoder((packet) => {
            if (!current(session)) return;
            if (socket.bufferedAmount > 256 * 1024) { fail(session, "network"); return; }
            sentBytes += packet.byteLength;
            if (sentBytes > Math.min(remote.max_bytes || Infinity, capability.max_bytes || Infinity)) { fail(session, "audioUnavailable"); return; }
            socket.send(packet.slice().buffer);
          }, () => fail(session, "audioUnavailable"));
          if (!current(session)) { session.encoder.close(); return; }
        }
        if (!current(session)) return;
        session.capture = await startSpeechCapture({
          context: session.captureContext,
          maxSamples: Math.floor(session.maxDuration * 16),
          onFrame: (frame) => {
            if (!current(session)) return;
            session.samples += frame.length;
            let squared = 0; for (const value of frame) squared += value * value;
            session.activity?.publishLevel(Math.min(1, Math.sqrt(squared / frame.length) * 4));
            if (session.samples % 3200 < frame.length) setElapsedMS(session.samples / 16);
            if (session.encoder) { try { session.encoder.encode(frame); } catch { fail(session, "audioUnavailable"); } }
            else session.frames.push(pcm16Frame(frame));
          },
          onLimit: () => { setElapsedMS(session.maxDuration); void stopRef.current(); },
          onError: () => fail(session, "audioUnavailable"),
        });
        if (!current(session) || session.stopping) { session.capture.cancel(); return; }
        setStatus("listening");
      }
    } catch (error) { if (current(session)) fail(session, error instanceof DOMException && error.name === "NotAllowedError" ? "permissionDenied" : "startFailed"); }
  };
  const finishAndSend = async (send: (finalDraft: string) => void | Promise<void>) => {
    if (sendPendingRef.current) return;
    sendPendingRef.current = true;
    const scope = latest.current.scopeKey;
    try {
      const success = await stopRef.current();
      if (!success || scope !== latest.current.scopeKey) return;
      await new Promise<void>((resolve) => requestAnimationFrame(() => resolve()));
      if (scope === latest.current.scopeKey) await send(latest.current.draft);
    } finally { sendPendingRef.current = false; }
  };
  return { supported, status, active: status !== "idle", elapsedMS, maxDurationMS, remainingMS: Math.max(0, maxDurationMS - elapsedMS), start, stop, cancel: () => cancel(), finishAndSend,
    toggle: () => { if (sessionRef.current) void stopRef.current(); else void start(); },
    placeholder: status === "starting" ? params.startingPlaceholder : status === "listening" ? params.listeningPlaceholder : params.placeholder };
}
