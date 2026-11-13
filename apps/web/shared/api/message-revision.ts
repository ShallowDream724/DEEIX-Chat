import { authedRequest } from "@/shared/api/authed-client";
import { pathParam } from "@/shared/api/http-client";
import type { MessageDTO } from "@/shared/api/conversation-types";

export type SaveMessageRevisionRequest = {
  content: string;
  fileIDs: string[];
  expectedUpdatedAt: string;
  revisionID: string;
};

export function saveMessageRevision(accessToken: string, messageID: string, payload: SaveMessageRevisionRequest) {
  return authedRequest<{ message: MessageDTO; selectedLeafMessagePublicID: string }>(
    `/api/v1/messages/${pathParam(messageID)}/revisions`,
    { method: "POST", accessToken, body: payload }, true,
  );
}
