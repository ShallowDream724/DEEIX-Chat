import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { stripTypeScriptTypes } from "node:module";
import test from "node:test";
let harnessSequence = 0;

// Exercise the hook's async state transitions without adding a DOM test runtime.
async function harness(t) {
  const slots = [];
  const requests = [];
  let cursor = 0;
  let pendingEffects = [];
  const changed = (previous, next) => !previous || previous.length !== next.length || next.some((value, index) => !Object.is(value, previous[index]));
  const React = {
    useState(initial) {
      const index = cursor++;
      slots[index] ??= { value: typeof initial === "function" ? initial() : initial };
      return [slots[index].value, next => { slots[index].value = typeof next === "function" ? next(slots[index].value) : next; }];
    },
    useRef(value) { const index = cursor++; slots[index] ??= { current: value }; return slots[index]; },
    useCallback(callback, deps) {
      const index = cursor++;
      if (changed(slots[index]?.deps, deps)) slots[index] = { value: callback, deps };
      return slots[index].value;
    },
    useEffect(effect, deps) {
      const index = cursor++;
      if (changed(slots[index]?.deps, deps)) {
        pendingEffects.push(() => { slots[index]?.cleanup?.(); slots[index] = { deps, cleanup: effect() }; });
      }
    },
  };
  const oldWindow = globalThis.window;
  globalThis.window = new EventTarget();
  globalThis.__authUserHarness = { React, getMe: token => new Promise((resolve, reject) => { requests.push({ token, resolve, reject }); }) };
  t.after(() => { globalThis.window = oldWindow; delete globalThis.__authUserHarness; });
  const source = stripTypeScriptTypes(await readFile(new URL("./use-auth-user-loader.ts", import.meta.url), "utf8"))
    .replace(/^import .*;\r?$/gm, "");
  const bindings = "const {React,getMe}=globalThis.__authUserHarness; const USER_PROFILE_UPDATED_EVENT='profile';\n";
  const { useAuthUserLoader } = await import(`data:text/javascript;base64,${Buffer.from(`${bindings}${source}\n// harness ${++harnessSequence}`).toString("base64")}`);
  return {
    requests,
    // biome-ignore lint/correctness/useHookAtTopLevel: this runner supplies the hook lifecycle for the test.
    render(token) { cursor = 0; return useAuthUserLoader(token); },
    effects() { const effects = pendingEffects; pendingEffects = []; for (const effect of effects) effect(); },
    profile(user) { const event = new Event("profile"); event.detail = user; window.dispatchEvent(event); },
  };
}

test("token switch suppresses previous identity before effects and a late prior-account refresh cannot win", async t => {
  const h = await harness(t);
  let result = h.render("token-a"); h.effects();
  h.requests[0].resolve({ id: 1, displayName: "A" }); await Promise.resolve();
  result = h.render("token-a");
  assert.equal(result.user.id, 1);
  const previousRefresh = result.refreshUser();
  result = h.render("token-b");
  assert.equal(result.user, null);
  assert.equal(result.userStatus, "loading");
  h.effects();
  h.requests[2].resolve({ id: 2, displayName: "B" }); await Promise.resolve();
  h.requests[1].resolve({ id: 1, displayName: "Old A" });
  assert.equal(await previousRefresh, null);
  result = h.render("token-b");
  assert.equal(result.user.id, 2);
  assert.equal(result.userStatus, "ready");
  h.profile({ id: 1, displayName: "Wrong account" });
  assert.equal(h.render("token-b").user.id, 2);
  h.profile({ id: 2, displayName: "Updated B" });
  assert.equal(h.render("token-b").user.displayName, "Updated B");
});

test("same-account profile refresh preserves identity while loading and the latest request wins", async t => {
  const h = await harness(t);
  h.render("token"); h.effects();
  h.requests[0].resolve({ id: 3, displayName: "Initial" }); await Promise.resolve();
  let result = h.render("token");
  const old = result.refreshUser();
  const latest = result.refreshUser();
  result = h.render("token");
  assert.equal(result.user.id, 3);
  assert.equal(result.userStatus, "loading");
  h.requests[2].resolve({ id: 3, displayName: "Latest" }); await latest;
  h.requests[1].reject(new Error("Late failure")); await old;
  result = h.render("token");
  assert.equal(result.user.displayName, "Latest");
  assert.equal(result.userStatus, "ready");
});
