"use client";

import * as React from "react";
import type { ChatAreaMessage } from "@/features/chat/types/messages";
import type { PendingAttachment } from "@/features/chat/types/chat-runtime";

export type MessageEditAttachmentContextValue = {
  conversationID?: string | null;
  temporary?: boolean;
  selectedToolIDs?: number[];
  resolveAttachments?: (message: ChatAreaMessage) => PendingAttachment[];
};

const Context = React.createContext<MessageEditAttachmentContextValue>({});
export const MessageEditAttachmentProvider = Context.Provider;
export function useMessageEditAttachmentContext() { return React.useContext(Context); }
