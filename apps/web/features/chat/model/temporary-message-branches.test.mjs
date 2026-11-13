import assert from "node:assert/strict";
import test from "node:test";
import { TemporaryMessageBranches } from "./temporary-message-branches.ts";

test("saved attachment revision preserves the original reply and branch-scoped history", () => {
  const branches = new TemporaryMessageBranches();
  const ancestor = { id: "ancestor", localAttachments: [{ fileID: "shared" }] };
  const original = { id: "original", parentID: "ancestor", localAttachments: [{ fileID: "removed" }] };
  const oldReply = { id: "reply", parentID: "original" };
  const revision = { id: "revision", parentID: "ancestor", localAttachments: [] };
  branches.archive("ancestor", "original", [ancestor, original, oldReply], ["ancestor context", "old file", "old reply"]);
  branches.archive("ancestor", "revision", [ancestor, revision], ["ancestor context", "edited user"]);
  assert.equal(branches.retainsAttachment("removed"), true);
  assert.equal(branches.retainsAttachment("shared"), true);
  assert.deepEqual(branches.navigator(revision), { parentPublicID: "ancestor", index: 2, total: 2, canPrevious: true, canNext: false });
  const old = branches.cycle("ancestor", "previous", [ancestor, revision], ["ancestor context", "edited user"]);
  assert.deepEqual(old.messages.map((item) => item.id), ["ancestor", "original", "reply"]);
  const next = branches.cycle("ancestor", "next", old.messages, old.history);
  assert.deepEqual(next.messages.map((item) => item.id), ["ancestor", "revision"]);
  assert.deepEqual(next.history, ["ancestor context", "edited user"]);
  assert.equal(branches.cycle("ancestor", "next", next.messages, next.history), undefined);
});

test("cycling archives new descendants and session cleanup drops all branch retention", () => {
  const branches = new TemporaryMessageBranches();
  const original = { id: "first", localAttachments: [{ fileID: "one" }] };
  const revised = { id: "second", localAttachments: [{ fileID: "two" }] };
  branches.archive(null, "first", [original], ["first"]);
  branches.archive(null, "second", [revised], ["second"]);
  const reply = { id: "new-reply", parentID: "second" };
  const previous = branches.cycle(null, "previous", [revised, reply], ["second", "new reply"]);
  const next = branches.cycle(null, "next", previous.messages, previous.history);
  assert.deepEqual(next.messages, [revised, reply]);
  assert.deepEqual(next.history, ["second", "new reply"]);
  branches.clear();
  assert.equal(branches.retainsAttachment("one"), false);
  assert.equal(branches.retainsAttachment("two"), false);
  assert.equal(branches.navigator(revised), undefined);
});
