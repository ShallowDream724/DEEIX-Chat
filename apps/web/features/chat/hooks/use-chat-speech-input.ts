"use client";
import * as React from "react";
import { refreshSpeechCapabilities, useSpeechCapabilities } from "@/shared/hooks/use-speech-capabilities";
import { createSpeechOpusEncoder, pcm16Frame, prepareSpeechCaptureContext, speechWav, startSpeechCapture, supportsBrowserRecognitionTrack, supportsSpeechOpus, watchMicrophonePermission, type SpeechCapture } from "@/features/chat/lib/speech-capture";
import { SpeechDraftSegment } from "@/features/chat/lib/speech-draft";
import { beginRecordingActivity } from "@/shared/lib/media-activity";
import { cancelSpeechSession, createSpeechSession, speechResourceURL, transcribeSpeech, type SpeechSession } from "@/shared/api/speech";
import { useOptionalAuthSession } from "@/shared/auth/auth-session-context";

type Recognition = {
  continuous: boolean; interimResults: boolean; lang: string;
  unspokenPunctuation?: boolean;
  onend: (() => void) | null; onstart: (() => void) | null;
  onerror: ((event: { error: string }) => void) | null;
  onresult: ((event: { results: { length: number; item: (index: number) => { isFinal: boolean; length: number; item: (index: number) => { transcript: string } } } }) => void) | null;
  start: (audioTrack?: MediaStreamTrack) => void; stop: () => void; abort?: () => void;
};
type RecognitionWindow = Window & { SpeechRecognition?: new () => Recognition; webkitSpeechRecognition?: new () => Recognition };
export type SpeechInputStatus = "idle" | "starting" | "listening" | "transcribing";
export type SpeechInputErrorCode = "audioUnavailable" | "durationLimit" | "interrupted" | "languageUnsupported" | "network" | "noSpeech" | "permissionDenied" | "processingFailed" | "serviceUnavailable" | "startFailed" | "unavailable";
type Session = {
  id: string; scope: string; controller: AbortController; segment: SpeechDraftSegment;
  userID: string; accessToken: string; providerID: string;
  cancelled: boolean; stopping: boolean; failed: boolean; sequence: number; samples: number;
  maxDuration: number; frames: Int16Array<ArrayBuffer>[]; capture?: SpeechCapture;
  captureContext?: AudioContext;
  activity?: ReturnType<typeof beginRecordingActivity>; socket?: WebSocket;
  encoder?: Awaited<ReturnType<typeof createSpeechOpusEncoder>>; recognition?: Recognition;
  finalize?: Promise<boolean>; completed: Promise<boolean>; resolveCompleted: (success: boolean) => void;
  draft: string; userEdited: boolean; persistDraft?: (draft: string) => void; disposers: (() => void)[];
  remoteSession?: SpeechSession; remoteFinished?: boolean;
};
const browserError: Record<string, SpeechInputErrorCode> = { "audio-capture": "audioUnavailable", "language-not-supported": "languageUnsupported", network: "network", "not-allowed": "permissionDenied", "service-not-allowed": "serviceUnavailable" };
function serviceError(code?: string): SpeechInputErrorCode { return code === "speech.duration_limit" ? "durationLimit" : code === "speech.unavailable" ? "serviceUnavailable" : "processingFailed"; }
function release(session: Session) {
  session.activity?.stop();
  for (const dispose of session.disposers.splice(0)) dispose();
  session.controller.abort();
  if (session.remoteSession && !session.remoteFinished) { session.remoteFinished = true; void cancelSpeechSession(session.accessToken, session.remoteSession.id).catch(() => {}); }
  try { session.capture?.cancel(); } catch {}
  try { session.socket?.close(); } catch {}
  if (session.captureContext?.state !== "closed") void session.captureContext?.close().catch(() => {});
  try { session.encoder?.close(); } catch {}
  if (session.recognition) { session.recognition.onstart = null; session.recognition.onend = null; session.recognition.onresult = null; session.recognition.onerror = null; try { session.recognition.abort?.(); } catch {} }
  session.frames = [];
}

export function useChatSpeechInput(params: {
  draft: string; scopeKey: string; language: string;
  onDraftChange: (value: string, selectionEnd?: number | null) => void; onError: (error: SpeechInputErrorCode) => void;
  placeholder: string;
  onDraftPersist?: (draft: string) => void;
  onResultCommitted?: (result: { scopeKey: string; sessionID: string; draft: string; selectionEnd: number }) => void;
}) {
  const latest = React.useRef(params); latest.current = params;
  const auth = useOptionalAuthSession();
  const token = auth?.accessToken ?? "";
  const userID = auth?.user?.publicID ?? "";
  const userIDRef = React.useRef(userID); userIDRef.current = userID;
  const capabilities = useSpeechCapabilities();
  const providerID = capabilities.selected_input_provider ?? "";
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
    session.userEdited = session.segment.reconcile(latest.current.draft) || session.userEdited;
    const next = session.segment.update(text);
    session.draft = next;
    latest.current = { ...latest.current, draft: next };
    latest.current.onDraftChange(next, session.segment.selectionEnd);
  };
  const complete = (session: Session, success: boolean) => {
    const visible = current(session);
    if (visible) {
      session.userEdited = session.segment.reconcile(latest.current.draft) || session.userEdited;
      session.draft = latest.current.draft;
    }
    session.persistDraft?.(session.draft);
    if (visible) sessionRef.current = null;
    session.failed ||= !success;
    session.resolveCompleted(success && !session.failed);
    release(session);
    if (visible) {
      setStatus("idle");
      const selectionEnd = session.segment.selectionEnd;
      if (success && !session.userEdited && selectionEnd !== null) latest.current.onResultCommitted?.({ scopeKey: session.scope, sessionID: session.id, draft: session.draft, selectionEnd });
    }
  };
  const fail = (session: Session, error: SpeechInputErrorCode) => {
    if (!current(session)) return;
    latest.current.onError(error); complete(session, false);
  };
  const cancel = React.useCallback((restore = true) => {
    const session = sessionRef.current;
    if (!session) return latest.current.draft;
    const visible = latest.current.scopeKey === session.scope && userIDRef.current === session.userID;
    if (visible) {
      session.segment.reconcile(latest.current.draft);
      session.draft = restore ? session.segment.cancel() : latest.current.draft;
    }
    session.cancelled = true;
    sessionRef.current = null;
    session.activity?.stop();
    try { session.socket?.send(JSON.stringify({ type: "cancel" })); } catch {}
    session.resolveCompleted(false); release(session); sendPendingRef.current = false; setStatus("idle");
    if (visible && restore) { latest.current = { ...latest.current, draft: session.draft }; latest.current.onDraftChange(session.draft); }
    session.persistDraft?.(session.draft);
    return session.draft;
  }, []);

  const stop = async (): Promise<boolean> => {
    const session = sessionRef.current;
    if (!session) return true;
    if (!current(session)) { cancel(false); return false; }
    if (session.finalize) return session.finalize;
    session.persistDraft?.(latest.current.draft);
    session.stopping = true; setStatus("transcribing");
    session.finalize = (async () => {
      try {
        if (session.recognition) { session.recognition.stop(); await session.capture?.stop(); session.activity?.stop(); }
        else {
          await session.capture?.stop(); session.activity?.stop();
          if (!current(session)) return session.completed;
          if (session.socket) { await session.encoder?.flush(); session.socket.send(JSON.stringify({ type: "finish" })); }
          else {
            if (session.samples === 0) { fail(session, "noSpeech"); return false; }
            const timeout = setTimeout(() => { if (current(session)) fail(session, "network"); }, 60000);
            let result: Awaited<ReturnType<typeof transcribeSpeech>>;
            try {
              const audio = speechWav(session.frames, session.samples);
              // Blob owns its bytes; the PCM chunks are no longer needed while the upload awaits a result.
              session.frames.length = 0;
              result = await transcribeSpeech(session.accessToken, session.providerID, session.scope, session.id, audio, session.controller.signal, session.remoteSession);
            }
            finally { clearTimeout(timeout); }
            session.frames = [];
            if (!current(session)) return false;
            session.remoteFinished = true;
            if (!result.text.trim()) { fail(session, "noSpeech"); return false; }
            update(session, result.text); complete(session, true);
          }
        }
        let timer: ReturnType<typeof setTimeout> | undefined;
        try { return await Promise.race([session.completed, new Promise<boolean>((resolve) => { timer = setTimeout(() => { fail(session, "network"); resolve(false); }, 20000); })]); }
        finally { clearTimeout(timer); }
      } catch (error) {
        const code = error && typeof error === "object" && "errorCode" in error && typeof error.errorCode === "string" ? error.errorCode : undefined;
        if (current(session)) fail(session, code?.startsWith("speech.") ? serviceError(code) : "network"); return session.completed;
      }
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
    return () => { cancel(false); };
  }, [cancel]);
  React.useEffect(() => { if (sessionRef.current?.scope !== params.scopeKey) cancel(false); }, [params.scopeKey, cancel]);
  React.useEffect(() => { if (sessionRef.current?.userID !== userID) cancel(false); }, [userID, cancel]);
  React.useEffect(() => {
    const session = sessionRef.current;
    if (session && session.scope === params.scopeKey) { session.userEdited = session.segment.reconcile(params.draft) || session.userEdited; session.draft = params.draft; }
  }, [params.draft, params.scopeKey]);
  React.useEffect(() => {
    const leaving = () => cancel(false);
    window.addEventListener("pagehide", leaving);
    window.addEventListener("beforeunload", leaving);
    return () => { window.removeEventListener("pagehide", leaving); window.removeEventListener("beforeunload", leaving); };
  }, [cancel]);

  const supported = providerID === "browser" ? browserSupported : Boolean(capability && captureSupported && (!capability.streaming || opusSupported));
  const start = async (insertionIndex = latest.current.draft.length) => {
    if (sessionRef.current || sendPendingRef.current) return;
    if (!browserSupported && !captureSupported) { latest.current.onError("unavailable"); return; }
    let resolveCompleted: (success: boolean) => void = () => {};
    const completed = new Promise<boolean>((resolve) => { resolveCompleted = resolve; });
    const duration = Math.min(capabilities.max_recording_duration_ms || 120000, capability?.max_duration_ms || Infinity);
    const maxSamples = Math.max(1, Math.floor(Math.min(duration * 16, capability?.max_bytes && !capability.streaming ? (capability.max_bytes - 44) / 2 : Infinity)));
    const session: Session = { id: crypto.randomUUID(), scope: latest.current.scopeKey, userID, accessToken: token, providerID, controller: new AbortController(), segment: new SpeechDraftSegment(latest.current.draft, insertionIndex), draft: latest.current.draft, userEdited: false, persistDraft: latest.current.onDraftPersist, disposers: [], cancelled: false, stopping: false, failed: false, sequence: -1, samples: 0, maxDuration: maxSamples / 16, frames: [], completed, resolveCompleted };
    sessionRef.current = session; setElapsedMS(0); setMaxDurationMS(session.maxDuration); setStatus("starting");
    session.activity = beginRecordingActivity(session.id);
    try {
      // Unlock the audio clock in the click gesture before the fresh configuration request.
      if (captureSupported) session.captureContext = prepareSpeechCaptureContext();
      const fresh = await refreshSpeechCapabilities(token, userID || token);
      if (!current(session)) return;
      const providerID = fresh.selected_input_provider ?? "";
      const capability = fresh.asr.find((item) => item.id === providerID);
      if (!providerID || (providerID === "browser" ? !browserSupported : !capability || !captureSupported || (capability.streaming && !opusSupported))) { fail(session, "unavailable"); return; }
      session.providerID = providerID;
      session.maxDuration = Math.min(fresh.max_recording_duration_ms || 120000, capability?.max_duration_ms || Infinity,
        capability?.max_bytes && !capability.streaming ? Math.max(1, capability.max_bytes - 44) / 32 : Infinity);
      setMaxDurationMS(session.maxDuration);
      const capture = () => startSpeechCapture({
        context: session.captureContext, signal: session.controller.signal, maxSamples: Math.floor(session.maxDuration * 16), keepTrackUntilCancel: providerID === "browser",
        onFrame: (frame, bands) => {
          if (!current(session)) return;
          session.samples += frame.length;
          let squared = 0; for (const value of frame) squared += value * value;
          session.activity?.publishLevel(Math.min(1, Math.sqrt(squared / frame.length) * 4), bands);
          if (session.samples % 3200 < frame.length) setElapsedMS(session.samples / 16);
          if (providerID === "browser") return;
          if (session.encoder) { try { session.encoder.encode(frame); } catch { fail(session, "audioUnavailable"); } }
          else session.frames.push(pcm16Frame(frame));
        },
        onLimit: () => { setElapsedMS(session.maxDuration); void stopRef.current(); }, onError: (error) => fail(session, error),
      });
      if (providerID === "browser") {
        let browserTrack: MediaStreamTrack | undefined;
        if (captureSupported && supportsBrowserRecognitionTrack()) {
          session.capture = await capture();
          if (!current(session)) { session.capture.cancel(); return; }
          browserTrack = session.capture.track;
        } else {
          if (session.captureContext?.state !== "closed") void session.captureContext?.close().catch(() => {});
          session.captureContext = undefined;
          void watchMicrophonePermission(session.controller.signal, () => fail(session, "permissionDenied"));
        }
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
          if ("unspokenPunctuation" in recognition) recognition.unspokenPunctuation = true;
          recognition.onstart = () => { if (current(session) && !session.stopping) { started ??= performance.now(); setStatus("listening"); } };
          recognition.onresult = (event) => {
            if (!current(session) || session.recognition !== recognition) return;
            const pieces: string[] = [];
            for (let i = 0; i < event.results.length; i++) { const result = event.results.item(i); if (result.length) pieces.push(result.item(0).transcript); }
            lastText = pieces.join(" ").trim(); update(session, [prefix, lastText].filter(Boolean).join(" "));
          };
          recognition.onerror = ({ error }) => {
            if (!current(session) || session.recognition !== recognition) return;
            if (error === "aborted" && !session.stopping) fail(session, "interrupted");
            else if (error !== "no-speech" && error !== "aborted") fail(session, browserError[error] ?? "processingFailed");
          };
          recognition.onend = () => {
            if (!current(session) || session.recognition !== recognition) return;
            if (session.stopping) { if ((prefix + lastText).trim()) complete(session, true); else fail(session, "noSpeech"); return; }
            if (lastText) { prefix = [prefix, lastText].filter(Boolean).join(" "); lastText = ""; emptyRestarts = 0; }
            else if (++emptyRestarts > 2) { fail(session, "noSpeech"); return; }
            startRecognition();
          };
          try { if (browserTrack) recognition.start(browserTrack); else recognition.start(); }
          catch (error) {
            if (!browserTrack || !(error instanceof TypeError || (error instanceof DOMException && error.name === "NotSupportedError"))) throw error;
            // A known unsupported overload must release its only track before asking the native service to capture.
            session.capture?.cancel(); session.capture = undefined; session.captureContext = undefined; browserTrack = undefined; session.samples = 0;
            session.activity?.stop(); session.activity = beginRecordingActivity(session.id); setElapsedMS(0);
            void watchMicrophonePermission(session.controller.signal, () => fail(session, "permissionDenied"));
            recognition.start();
          }
        };
        startRecognition();
        const timer = setInterval(() => {
          if (!current(session)) { clearInterval(timer); return; }
          const elapsed = browserTrack ? session.samples / 16 : started === null ? 0 : Math.min(session.maxDuration, performance.now() - started); setElapsedMS(elapsed);
          if (elapsed >= session.maxDuration) { clearInterval(timer); void stopRef.current(); }
        }, 200);
        session.disposers.push(() => clearInterval(timer));
      } else {
        const remote = await createSpeechSession(token, providerID, session.scope, session.controller.signal, capability?.streaming ? "opus" : "wav");
        if (!current(session)) { void cancelSpeechSession(token, remote.id).catch(() => {}); return; }
        session.remoteSession = remote;
        session.maxDuration = Math.min(session.maxDuration, remote.max_duration_ms || Infinity);
        if (!capability?.streaming && remote.max_bytes) session.maxDuration = Math.min(session.maxDuration, Math.max(1, remote.max_bytes - 44) / 32);
        setMaxDurationMS(session.maxDuration);
        if (capability?.streaming) {
          if (!remote.ws_url) { fail(session, "serviceUnavailable"); return; }
          const url = new URL(speechResourceURL(remote.ws_url)); url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
          const socket = new WebSocket(url); session.socket = socket;
          await new Promise<void>((resolve, reject) => {
            const timer = setTimeout(() => reject(new Error("handshake timeout")), 15000);
            const abort = () => { clearTimeout(timer); reject(new DOMException("Aborted", "AbortError")); };
            session.controller.signal.addEventListener("abort", abort, { once: true });
            socket.onmessage = ({ data }) => {
              if (!current(session) || typeof data !== "string") return;
              let event: { type: string; session_id: string; sequence: number; text?: string; code?: string };
              try { event = JSON.parse(data); } catch { fail(session, "network"); return; }
              if (event.session_id && event.session_id !== remote.id) return;
              if (event.type === "ready") { clearTimeout(timer); session.controller.signal.removeEventListener("abort", abort); resolve(); }
              if (event.type === "error") { clearTimeout(timer); reject(new Error("ASR failed")); fail(session, serviceError(event.code)); }
              if ((event.type === "transcript" || event.type === "final") && event.sequence > session.sequence) { session.sequence = event.sequence; update(session, event.text ?? ""); }
              if (event.type === "final" && event.sequence >= session.sequence) { session.remoteFinished = true; if (event.text?.trim()) complete(session, true); else fail(session, "noSpeech"); }
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
        session.capture = await capture();
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
  const interruptForEditing = (draft = latest.current.draft) => {
    latest.current = { ...latest.current, draft };
    cancel(false);
  };
  return { supported, status, active: status !== "idle", elapsedMS, maxDurationMS, remainingMS: Math.max(0, maxDurationMS - elapsedMS), start, stop, cancel: () => cancel(), finishAndSend, interruptForEditing,
    toggle: () => { if (sessionRef.current) void stopRef.current(); else void start(); },
    placeholder: params.placeholder };
}
