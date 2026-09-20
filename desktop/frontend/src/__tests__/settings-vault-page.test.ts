// Run: tsx src/__tests__/settings-vault-page.test.ts

import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const testDir = dirname(fileURLToPath(import.meta.url));
const settings = readFileSync(resolve(testDir, "../components/SettingsPanel.tsx"), "utf8");
const navigation = readFileSync(resolve(testDir, "../components/SettingsNavigation.tsx"), "utf8");
const page = readFileSync(resolve(testDir, "../components/VaultSettingsPage.tsx"), "utf8");
const bridge = readFileSync(resolve(testDir, "../lib/bridge.ts"), "utf8");
const types = readFileSync(resolve(testDir, "../lib/types.ts"), "utf8");
const backend = readFileSync(resolve(testDir, "../../../vault_settings_app.go"), "utf8");
const overlayHost = readFileSync(resolve(testDir, "../app-shell/AppOverlayHost.tsx"), "utf8");
const startupGate = readFileSync(resolve(testDir, "../app-runtime/StartupGateLifecycle.tsx"), "utf8");
const overlays = readFileSync(resolve(testDir, "../store/overlays.ts"), "utf8");
const unlockDialog = readFileSync(resolve(testDir, "../components/VaultUnlockDialog.tsx"), "utf8");
const credentialUnlock = readFileSync(resolve(testDir, "../lib/credentialUnlock.ts"), "utf8");
const locales = ["en.ts", "zh.ts", "zh-TW.ts"].map((name) => readFileSync(resolve(testDir, `../locales/${name}`), "utf8"));
const [en] = locales;

let passed = 0;
let failed = 0;

function ok(condition: boolean, label: string) {
  if (condition) {
    process.stdout.write(`  PASS  ${label}\n`);
    passed += 1;
  } else {
    process.stdout.write(`  FAIL  ${label}\n`);
    failed += 1;
  }
}

console.log("\nsettings vault page contract");

ok(/"vault"/.test(navigation.match(/SETTINGS_NAV_TABS: SettingsTab\[\] = \[([^\]]+)\]/)?.[1] ?? ""), "The vault page is a first-class settings navigation tab");
ok(navigation.includes('["permissions", "sandbox", "network", "browser", "vault"]'), "The vault page sits in the security navigation group");
ok(types.includes('| "browser" | "vault" |'), "SettingsTab exposes the vault route");
ok(settings.includes('tab === "vault"') && settings.includes("<VaultSettingsPage />"), "Settings renders the vault page");
ok(settings.includes('import("./VaultSettingsPage")'), "Vault UI loads only when its page bundle renders");

for (const method of ["VaultSettings", "SetVaultPassword", "ChangeVaultPassword", "LockVault", "DisableVault"]) {
  ok(page.includes(`app.${method}`), `Vault page calls app.${method}`);
  ok(bridge.includes(`${method}(`), `Bridge binds ${method}`);
  ok(new RegExp(`func \\(a \\*App\\) ${method}\\(`).test(backend), `Backend defines App.${method}`);
}
// Unlocking is shared by the startup dialog and the settings page, and always
// refreshes the runtime so newly readable credentials take effect.
ok(credentialUnlock.includes("app.UnlockVault"), "The unlock helper verifies the master password");
ok(credentialUnlock.includes("app.ReloadSettings"), "The unlock helper rebuilds the runtime for the unlocked store");
ok(page.includes("unlockCredentialStore"), "The settings page unlocks through the shared helper");
ok(unlockDialog.includes("unlockCredentialStore"), "The startup dialog unlocks through the shared helper");
ok(bridge.includes("UnlockVault("), "Bridge binds UnlockVault");
ok(/func \(a \*App\) UnlockVault\(/.test(backend), "Backend defines App.UnlockVault");

ok(page.includes("readOnly"), "The encrypted credential path is read-only");
ok(page.includes('type="password"'), "Password fields never render their value");

const usedKeys = [...new Set([...page.matchAll(/"(settings\.vault\.[A-Za-z]+)"/g)].map((match) => match[1]))];
ok(usedKeys.length >= 20, "The vault page uses its own localized keys");
ok(usedKeys.every((key) => en.includes(`"${key}":`)), "Every settings.vault key has an English string");
for (const locale of locales) {
  ok(locale.includes('"settings.tab.vault":'), "Every locale names the vault tab");
  ok(usedKeys.every((key) => locale.includes(`"${key}":`)), "Every locale covers every settings.vault key");
}

ok(overlays.includes("vaultUnlockOpen"), "The overlay store owns the startup unlock dialog state");
ok(startupGate.includes("app.VaultSettings()"), "The startup gate probes credential-store lock state");
ok(overlayHost.includes("<VaultUnlockDialog />"), "The shell overlay host mounts the startup unlock dialog");
ok(unlockDialog.includes("unlockCredentialStore"), "The startup dialog unlocks through the shared helper");

const unlockKeys = [...new Set([...unlockDialog.matchAll(/"(vault\.unlock\.[A-Za-z]+)"/g)].map((match) => match[1]))];
ok(unlockKeys.length >= 4, "The startup dialog uses its own localized keys");
for (const locale of locales) {
  ok(unlockKeys.every((key) => locale.includes(`"${key}":`)), "Every locale covers every vault.unlock key");
}

console.log(`\n${passed} passed, ${failed} failed, ${passed + failed} total`);
if (failed > 0) process.exit(1);
