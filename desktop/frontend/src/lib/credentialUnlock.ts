import { app } from "./bridge";

// unlockCredentialStore verifies the master password and then asks the host to
// rebuild the active runtime. Provider keys, bot secrets, and remote-SSH
// passwords that were unreadable while the store was locked are resolved from
// the credential file during a rebuild, so unlocking without one would leave
// the app running on an empty-key view. The rebuild is best-effort: a failed or
// queued reload never turns a successful unlock into an error.
export async function unlockCredentialStore(password: string): Promise<void> {
  await app.UnlockVault(password);
  try {
    await app.ReloadSettings();
  } catch {
    /* credentials are already unlocked; the runtime reload is advisory */
  }
}
