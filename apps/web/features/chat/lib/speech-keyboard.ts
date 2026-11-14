import { isImeComposingKeyEvent } from "@/shared/lib/platform-shortcuts";

type SpeechKeyEvent = { key: string; keyCode?: number; ctrlKey: boolean; metaKey: boolean; altKey: boolean; nativeEvent?: { isComposing?: boolean } };

/** Local textarea policy only; callers preserve native editing and system shortcuts. */
export function speechInputKeyAction(event: SpeechKeyEvent, composing: boolean): "preserve" | "stop" | "interrupt" | "default" {
  if (isImeComposingKeyEvent(event, composing) || ["Shift", "Control", "Alt", "Meta", "AltGraph"].includes(event.key)) return "preserve";
  if (event.key === "Enter") return "stop";
  if (["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End", "PageUp", "PageDown", "Tab", "Escape", "Backspace", "Delete"].includes(event.key)) return "interrupt";
  if ((event.ctrlKey || event.metaKey) && ["a", "x", "v"].includes(event.key.toLowerCase())) return "interrupt";
  if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) return "interrupt";
  return "default";
}
