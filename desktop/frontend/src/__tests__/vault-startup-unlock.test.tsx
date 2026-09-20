// Run: tsx src/__tests__/vault-startup-unlock.test.tsx
//
// The startup probe must request the master-password dialog only for an
// encrypted store this process has not unlocked.

import assert from "node:assert/strict";
import React, { act, StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { JSDOM } from "jsdom";

import { probeVaultLockState, StartupGateLifecycle } from "../app-runtime/StartupGateLifecycle";
import { useOverlayStore } from "../store/overlays";
import { installDesktopHostStub } from "./desktopHostStub";

const dom = new JSDOM("<div id='root'></div>", { url: "http://localhost" });
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  localStorage: dom.window.localStorage,
  sessionStorage: dom.window.sessionStorage,
  IS_REACT_ACT_ENVIRONMENT: true,
});

let vault = { configured: true, unlocked: false, path: "/home/.reasonix/.env", minLength: 8 };
const probes: Array<() => void> = [];
installDesktopHostStub({
  NeedsOnboarding: async () => false,
  VaultSettings: () => new Promise((resolve) => { probes.push(() => resolve({ ...vault })); }),
});

async function mount() {
  useOverlayStore.getState().setVaultUnlockOpen(false);
  const root = createRoot(document.getElementById("root")!);
  await act(async () => root.render(<StrictMode><StartupGateLifecycle /></StrictMode>));
  await act(async () => { probes.splice(0).forEach((resolve) => resolve()); });
  return root;
}

try {
  let root = await mount();
  assert.equal(useOverlayStore.getState().vaultUnlockOpen, true, "a locked credential store requests the unlock prompt");
  await act(async () => root.unmount());

  vault = { ...vault, unlocked: true };
  root = await mount();
  assert.equal(useOverlayStore.getState().vaultUnlockOpen, false, "an already-unlocked store never prompts");
  await act(async () => root.unmount());

  vault = { ...vault, configured: false, unlocked: false };
  root = await mount();
  assert.equal(useOverlayStore.getState().vaultUnlockOpen, false, "a plaintext store never prompts");
  await act(async () => root.unmount());

  const pendingProbe = probeVaultLockState();
  await act(async () => { probes.splice(0).forEach((resolve) => resolve()); });
  assert.equal(await pendingProbe, false, "the probe mirrors the backend lock state");

  console.log("vault startup probe: locked, unlocked and plaintext stores all resolve correctly");
} finally {
  dom.window.close();
}
