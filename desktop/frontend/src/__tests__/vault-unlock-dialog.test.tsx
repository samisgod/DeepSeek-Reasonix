// Run: tsx src/__tests__/vault-unlock-dialog.test.tsx

import React from "react";
import { JSDOM } from "jsdom";
import { act } from "react";

import type { AppBindings } from "../lib/bridge";
import { installDesktopHostStub } from "./desktopHostStub";

let passed = 0;
let failed = 0;
function ok(value: boolean, label: string) {
  if (value) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

console.log("\nmaster-password startup unlock prompt");
const dom = new JSDOM("<!doctype html><html><body><div id=\"root\"></div></body></html>", {
  pretendToBeVisual: true,
  url: "http://localhost/",
});
(globalThis as typeof globalThis & { IS_REACT_ACT_ENVIRONMENT: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
globalThis.window = dom.window as unknown as Window & typeof globalThis;
globalThis.document = dom.window.document;
Object.defineProperty(globalThis, "navigator", { configurable: true, value: dom.window.navigator });
globalThis.HTMLElement = dom.window.HTMLElement;
globalThis.Event = dom.window.Event;
globalThis.KeyboardEvent = dom.window.KeyboardEvent;
Object.defineProperty(dom.window.HTMLElement.prototype, "attachEvent", { configurable: true, value: () => {} });
Object.defineProperty(dom.window.HTMLElement.prototype, "detachEvent", { configurable: true, value: () => {} });

const calls: string[] = [];
let reloads = 0;
let rejectWith: string | null = null;
installDesktopHostStub(({ main: { App: {
  async UnlockVault(password: string) {
    calls.push(password);
    if (rejectWith) throw new Error(rejectWith);
    return { configured: true, unlocked: true, path: "/mock/.reasonix/.env", minLength: 8 };
  },
  async ReloadSettings() { reloads += 1; },
} as Partial<AppBindings> as AppBindings } }).main.App);

const [{ createRoot }, { VaultUnlockDialog }, { LocaleProvider }, { useOverlayStore }] = await Promise.all([
  import("react-dom/client"),
  import("../components/VaultUnlockDialog"),
  import("../lib/i18n"),
  import("../store/overlays"),
]);

const rootElement = document.getElementById("root");
if (!rootElement) throw new Error("missing root");
const root = createRoot(rootElement);
await act(async () => root.render(<LocaleProvider><VaultUnlockDialog /></LocaleProvider>));

// The prompt waits for the startup splash, matching the shell's boot sequence.
await act(async () => {
  useOverlayStore.getState().setStartupSplashVisible(true);
  useOverlayStore.getState().setVaultUnlockOpen(true);
});
ok(document.querySelector('input[type="password"]') === null, "the prompt holds until the startup splash yields");

await act(async () => { useOverlayStore.getState().setStartupSplashVisible(false); });
const input = document.querySelector<HTMLInputElement>('input[type="password"]');
ok(Boolean(input), "a locked store opens a masked master-password prompt");
ok(document.body.textContent?.includes("Unlock the credential store") === true, "the prompt explains the locked store");

const typePassword = async (value: string) => {
  await act(async () => {
    const field = document.querySelector<HTMLInputElement>('input[type="password"]');
    if (!field) return;
    const setter = Object.getOwnPropertyDescriptor(dom.window.HTMLInputElement.prototype, "value")?.set;
    setter?.call(field, value);
    field.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
    await Promise.resolve();
  });
};

await typePassword("correct horse battery");
await act(async () => {
  document.querySelector("form")?.dispatchEvent(new dom.window.Event("submit", { bubbles: true, cancelable: true }));
  await Promise.resolve();
});
ok(calls.length === 1 && calls[0] === "correct horse battery", "submit sends the password once to the native bridge");
ok(reloads === 1, "a successful unlock rebuilds the runtime so the keys load");
ok(useOverlayStore.getState().vaultUnlockOpen === false, "a successful unlock closes the prompt");
ok(document.body.textContent?.includes("correct horse battery") === false, "the password is never rendered into the page");

rejectWith = "invalid master password";
await act(async () => { useOverlayStore.getState().setVaultUnlockOpen(true); });
await typePassword("wrong password");
await act(async () => {
  document.querySelector("form")?.dispatchEvent(new dom.window.Event("submit", { bubbles: true, cancelable: true }));
  await Promise.resolve();
});
ok(calls.length === 2 && calls[1] === "wrong password", "a retry sends the new password");
ok(reloads === 1, "a rejected password never rebuilds the runtime");
ok(document.body.textContent?.includes("invalid master password") === true, "a rejected password surfaces the backend error");
ok(useOverlayStore.getState().vaultUnlockOpen === true, "a rejected password keeps the prompt open");

rejectWith = null;
const later = [...document.querySelectorAll("button")].find((button) => button.textContent === "Not now");
await act(async () => { later?.click(); await Promise.resolve(); });
ok(useOverlayStore.getState().vaultUnlockOpen === false, "dismissing defers unlocking to the settings page");

await act(async () => root.unmount());
dom.window.close();
process.stdout.write(`\n${passed} passed, ${failed} failed\n`);
if (failed > 0) process.exit(1);
