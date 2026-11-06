/** The connection ended without an authoritative generation result. Never retry the POST automatically. */
export class ConversationStreamDisconnectedError extends Error {
  readonly accepted: boolean;
  constructor(accepted: boolean, options?: ErrorOptions) {
    super("Conversation stream disconnected", options);
    this.name = "ConversationStreamDisconnectedError";
    this.accepted = accepted;
  }
}
