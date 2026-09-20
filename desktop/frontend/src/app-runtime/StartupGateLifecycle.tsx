import { useEffect } from "react";
import { app } from "../lib/bridge";
import { dismissOnboarding, shouldOpenOnboarding } from "../lib/onboarding";
import { useOverlayStore } from "../store/overlays";
import { useAppNavigationStore } from "../store/appNavigation";

export async function probeProviderSetupState(): Promise<boolean> {
  const needs = await app.NeedsOnboarding();
  useOverlayStore.getState().setProviderSetupNeeded(needs);
  return needs;
}

/** Reports whether the credential store is encrypted but this process holds no
 *  derived key, which is what the startup unlock dialog exists to resolve. */
export async function probeVaultLockState(): Promise<boolean> {
  const vault = await app.VaultSettings();
  return vault.configured && !vault.unlocked;
}

/** Probes setup once; a later navigation intent owns the current page. */
export function StartupGateLifecycle() {
  useEffect(() => {
    let cancelled = false;
    const navigationGeneration = useAppNavigationStore.getState().generation;
    (async () => {
      try {
        const needs = await app.NeedsOnboarding();
        if (cancelled) return;
        useOverlayStore.getState().setProviderSetupNeeded(needs);
        const navigation = useAppNavigationStore.getState();
        if (shouldOpenOnboarding(needs) && navigation.generation === navigationGeneration) {
          dismissOnboarding();
          navigation.setSettingsFocus({ target: "model-access", onboarding: true });
          navigation.setSettingsTarget("providers");
        }
      } catch {
        // Setup status is advisory; bridge failures must not block startup.
      }
    })();
    return () => { cancelled = true; };
  }, []);

  // A locked credential store leaves provider keys and bot secrets unreadable,
  // so the unlock dialog is offered once the splash yields. Dismissing it falls
  // back to the same flow inside Settings; this probe never blocks startup.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const locked = await probeVaultLockState();
        if (!cancelled && locked) useOverlayStore.getState().setVaultUnlockOpen(true);
      } catch {
        // Vault status is advisory; bridge failures must not block startup.
      }
    })();
    return () => { cancelled = true; };
  }, []);

  return null;
}
