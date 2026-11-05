import assert from "node:assert/strict";
import test from "node:test";
import { nativeBadgeFileIDs, nativeFileIdentity } from "./native-readiness.ts";

const files = Array.from({ length: 5 }, (_, index) => ({
  fileID: `file-${index}`, fileName: `report-${index}.pdf`, sizeBytes: 1024,
  mimeType: "application/pdf", fileCategory: "pdf", processingStatus: "failed", extractStatus: "failed",
}));
const snapshot = {
  scopeKey: "model-a/branch-a/tools-a",
  eligibleFileIDs: new Set(files.map((file) => file.fileID)),
  fileIdentities: new Map(files.map((file) => [file.fileID, nativeFileIdentity(file)])),
};
const ids = (items, source = snapshot, scope = snapshot.scopeKey) => [...nativeBadgeFileIDs(source, scope, items)];

test("removing files retains native badges through repeated pending requests", () => {
  assert.deepEqual(ids(files.slice(1)), ["file-1", "file-2", "file-3", "file-4"]);
  assert.deepEqual(ids(files.slice(3)), ["file-3", "file-4"]);
  assert.deepEqual(ids([]), []);
});

test("new uploads are unconfirmed without reverting previously confirmed native files", () => {
  assert.deepEqual(ids([...files, { ...files[0], fileID: "new-file" }]), files.map((file) => file.fileID));
  assert.deepEqual(ids([{ ...files[0], extractStatus: "extracting", processingReady: false, ragReady: false }]), ["file-0"]);
});

test("model, branch and tool scope changes never reuse native badges", () => {
  for (const scope of ["model-b/branch-a/tools-a", "model-a/branch-b/tools-a", "model-a/branch-a/tools-b"]) {
    assert.deepEqual(ids(files, snapshot, scope), []);
  }
});

test("missing files, changed originals and authoritative negative results remove badges", () => {
  assert.deepEqual(ids([{ ...files[0], processingErrorCode: "file.not_found" }]), []);
  assert.deepEqual(ids([{ ...files[0], sizeBytes: 9999 }]), []);
  assert.deepEqual(ids([{ ...files[0], mimeType: "audio/wav" }]), []);
  assert.deepEqual(ids(files, { ...snapshot, eligibleFileIDs: new Set() }), []);
  assert.deepEqual(ids(files, null), []);
});
