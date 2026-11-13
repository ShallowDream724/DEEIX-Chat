type BranchMessage = { id: string; parentID?: string; localAttachments?: readonly { fileID: string }[] };
type Snapshot<Message, History> = { rootID: string; messages: Message[]; history: History[] };

// Temporary conversations keep branch snapshots only for this browser session.
// Shared File objects stay retained until every branch is discarded with the session.
export class TemporaryMessageBranches<Message extends BranchMessage, History> {
  private readonly branches = new Map<string, Snapshot<Message, History>[]>();
  private readonly attachmentIDs = new Set<string>();

  private key(parentID: string | null | undefined) { return parentID ?? "root"; }

  archive(parentID: string | null | undefined, rootID: string, messages: readonly Message[], history: readonly History[]) {
    const key = this.key(parentID);
    const entries = this.branches.get(key) ?? [];
    const snapshot = { rootID, messages: [...messages], history: [...history] };
    const index = entries.findIndex((entry) => entry.rootID === rootID);
    if (index >= 0) entries[index] = snapshot;
    else entries.push(snapshot);
    for (const message of messages) for (const attachment of message.localAttachments ?? []) this.attachmentIDs.add(attachment.fileID);
    this.branches.set(key, entries);
  }

  retainsAttachment(fileID: string) { return this.attachmentIDs.has(fileID); }

  navigator(message: Message) {
    const branches = this.branches.get(this.key(message.parentID));
    const index = branches?.findIndex((entry) => entry.rootID === message.id) ?? -1;
    return branches && index >= 0 ? { parentPublicID: message.parentID ?? null, index: index + 1, total: branches.length, canPrevious: index > 0, canNext: index < branches.length - 1 } : undefined;
  }

  cycle(parentID: string | null, direction: "previous" | "next", messages: readonly Message[], history: readonly History[]) {
    const entries = this.branches.get(this.key(parentID));
    if (!entries || entries.length < 2) return undefined;
    const currentIndex = entries.findIndex((entry) => messages.some((message) => message.id === entry.rootID));
    if (currentIndex < 0) return undefined;
    const next = entries[currentIndex + (direction === "previous" ? -1 : 1)];
    if (!next) return undefined;
    this.archive(parentID, entries[currentIndex].rootID, messages, history);
    return { messages: [...next.messages], history: [...next.history] };
  }

  clear() { this.branches.clear(); this.attachmentIDs.clear(); }
}
