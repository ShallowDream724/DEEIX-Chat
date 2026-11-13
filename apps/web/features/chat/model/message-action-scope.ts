// Async actions may finish after navigation or account/session changes. The write
// still belongs to its original message; local reconciliation belongs to that view.
export function captureMessageActionScope(getConversationID: () => string | null, getSessionID: () => string) {
  const conversationID = getConversationID();
  const sessionID = getSessionID();
  return () => getConversationID() === conversationID && getSessionID() === sessionID;
}
