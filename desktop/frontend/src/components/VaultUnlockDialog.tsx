import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";

import { unlockCredentialStore } from "../lib/credentialUnlock";
import { useT } from "../lib/i18n";
import { useOverlayStore } from "../store/overlays";

/** VaultUnlockDialog is the global startup prompt for a master-password
 *  protected credential store. The password is sent straight to Go and never
 *  enters a shared store, a status event, or the rendered tree. It renders only
 *  after the startup splash yields, and dismissing it falls back to
 *  Settings > Credential encryption. */
export function VaultUnlockDialog() {
  const t = useT();
  const requested = useOverlayStore((s) => s.vaultUnlockOpen);
  const splashVisible = useOverlayStore((s) => s.startupSplashVisible);
  const setOpen = useOverlayStore((s) => s.setVaultUnlockOpen);
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const open = requested && !splashVisible;

  useEffect(() => {
    setPassword("");
    setError(null);
    setBusy(false);
    if (open) queueMicrotask(() => inputRef.current?.focus());
  }, [open]);

  if (!open) return null;

  const dismiss = () => setOpen(false);

  const submit = async () => {
    if (busy) return;
    setBusy(true);
    setError(null);
    try {
      await unlockCredentialStore(password);
      setPassword("");
      dismiss();
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };

  return createPortal(
    <div
      className="modal-backdrop vault-unlock-backdrop"
      data-app-overlay=""
      role="presentation"
      onMouseDown={(event) => {
        if (event.target === event.currentTarget && !busy) dismiss();
      }}
    >
      <form
        className="modal vault-unlock-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="vault-unlock-title"
        onSubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <div className="modal__title" id="vault-unlock-title">{t("vault.unlock.title")}</div>
        <p className="vault-unlock-dialog__hint">{t("vault.unlock.body")}</p>
        <input
          ref={inputRef}
          className="mem-input"
          type="password"
          value={password}
          autoComplete="current-password"
          aria-label={t("vault.unlock.label")}
          onChange={(event) => setPassword(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Escape") {
              event.preventDefault();
              dismiss();
            }
          }}
        />
        {error && <div className="banner banner--error" role="alert"><span>{error}</span></div>}
        <div className="modal__actions">
          <button type="button" className="btn btn--small" disabled={busy} onClick={dismiss}>
            {t("vault.unlock.later")}
          </button>
          <button type="submit" className="btn btn--small btn--primary" disabled={busy || password.length === 0}>
            {busy ? t("vault.unlock.working") : t("vault.unlock.submit")}
          </button>
        </div>
      </form>
    </div>,
    document.body,
  );
}
