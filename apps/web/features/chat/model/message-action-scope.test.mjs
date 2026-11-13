import assert from "node:assert/strict";
import test from "node:test";
import { captureMessageActionScope } from "./message-action-scope.ts";

test("a completed save only reconciles the same conversation and login session", async () => {
  let conversationID = "original";
  let sessionID = "first-session";
  const current = captureMessageActionScope(() => conversationID, () => sessionID);
  assert.equal(current(), true);
  let complete;
  const saved = new Promise((resolve) => { complete = resolve; });
  let reconciliations = 0;
  const result = saved.then(() => { if (current()) reconciliations++; return true; });
  conversationID = "new-conversation";
  complete();
  assert.equal(await result, true);
  assert.equal(reconciliations, 0);
  conversationID = "original";
  sessionID = "second-session";
  assert.equal(current(), false);
});
