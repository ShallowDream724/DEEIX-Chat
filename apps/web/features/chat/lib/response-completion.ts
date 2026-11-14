export type ResponseCompletion = { conversationID: string; messageID: string; content: string };
const listeners = new Set<(event: ResponseCompletion) => void>();
const completed = new Set<string>();
export function publishResponseCompletion(event: ResponseCompletion) {
  const key = `${event.conversationID}:${event.messageID}`;
  if (completed.has(key)) return;
  completed.add(key);
  if (completed.size > 512) completed.delete(completed.values().next().value ?? "");
  for (const listener of listeners) listener(event);
}
export function subscribeResponseCompletion(listener: (event: ResponseCompletion) => void) {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
}
