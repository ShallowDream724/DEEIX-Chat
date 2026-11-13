import assert from "node:assert/strict";
import test from "node:test";
import { canSaveMessageDraft, createMessageEditAttachments, messageAttachmentIDsEqual } from "./message-edit-draft.ts";

const file = (fileID) => ({ fileID, fileName: `${fileID}.png`, mimeType: "image/png", sizeBytes: 1, fileCategory: "image" });

test("attachment-only drafts are valid; deleting every attachment requires text", () => {
  assert.equal(canSaveMessageDraft("", [file("one")]), true);
  assert.equal(canSaveMessageDraft("  ", []), false);
  assert.equal(canSaveMessageDraft(" keep text ", []), true);
});

test("add, remove, replace and clearing all attachments are changes", () => {
  const initial = [file("one"), file("two")];
  assert.equal(messageAttachmentIDsEqual(initial, initial.map((item) => ({ ...item, processingReady: true }))), true);
  for (const changed of [[file("one")], [...initial, file("three")], [file("one"), file("three")], []]) assert.equal(messageAttachmentIDsEqual(initial, changed), false);
});

test("editing owns temporary previews and preserves the source and stable asset identity", () => {
  const localFile = new File(["pixels"], "image.png", { type: "image/png" });
  const original = { ...file("one"), localFile, previewURL: URL.createObjectURL(localFile), assetID: "asset-one", contentVersion: "hash-one" };
  const [draft] = createMessageEditAttachments([original]);
  assert.notEqual(draft.previewURL, original.previewURL);
  assert.equal(draft.assetID, original.assetID);
  assert.equal(draft.contentVersion, original.contentVersion);
  URL.revokeObjectURL(draft.previewURL);
  assert.equal(original.localFile, localFile);
  assert.equal(original.previewURL.startsWith("blob:"), true);
  URL.revokeObjectURL(original.previewURL);
});

test("persisted message blob previews are not transferred into edit ownership", () => {
  const original = { ...file("one"), previewURL: "blob:source-owned" };
  const [draft] = createMessageEditAttachments([original]);
  assert.equal(draft.previewURL, undefined);
  assert.equal(original.previewURL, "blob:source-owned");
});
