import { useCallback, useEffect, useState, type ReactNode } from "react";
import { KeyRound, Lock, LockOpen, ShieldCheck, ShieldOff } from "lucide-react";
import { app } from "../lib/bridge";
import { unlockCredentialStore } from "../lib/credentialUnlock";
import { useT } from "../lib/i18n";
import { SettingsField, SettingsSection } from "./SettingsForm";
import { InlineConfirmButton } from "./InlineConfirmButton";

// VaultSettingsPage manages the master password that encrypts Reasonix's
// credential store (<Reasonix home>/.env: provider API keys, bot secrets,
// remote-SSH passwords). The GUI mirrors `reasonix secrets set/change/unlock/
// disable`, while status and the encrypted file path stay read-only.
type VaultView = Awaited<ReturnType<typeof app.VaultSettings>>;

export function VaultSettingsPage() {
  const t = useT();
  const [view, setView] = useState<VaultView | null>(null);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [pending, setPending] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setFailed(false);
    try {
      setView(await app.VaultSettings());
    } catch {
      setView(null);
      setFailed(true);
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // run serializes one vault mutation. It surfaces the typed backend error
  // (wrong password, too short) verbatim, which is clearer than a generic banner.
  const run = useCallback(async (label: string, action: () => Promise<VaultView>, success: string): Promise<boolean> => {
    setPending(label);
    setError(null);
    setNotice(null);
    try {
      setView(await action());
      setNotice(success);
      return true;
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      return false;
    } finally {
      setPending(null);
    }
  }, []);

  if (loading) return <div className="empty">{t("settings.loading")}</div>;
  if (failed || !view) {
    return (
      <div className="banner banner--error settings-load-error" role="alert">
        <span>{t("settings.loadFailed")}</span>
        <button className="btn btn--small" type="button" onClick={() => void load()}>{t("common.retry")}</button>
      </div>
    );
  }

  const minLength = view.minLength > 0 ? view.minLength : 8;
  const busy = pending !== null;
  const statusLabel = view.configured
    ? t(view.unlocked ? "settings.vault.statusUnlocked" : "settings.vault.statusLocked")
    : t("settings.vault.statusDisabled");

  return (<>
    {error && <div className="banner banner--error" role="alert"><span>{error}</span></div>}
    {notice && <div className="banner banner--success" role="status"><span>{notice}</span></div>}
    <SettingsSection title={t("settings.vault.title")} description={t("settings.vault.hint")}>
      <SettingsField label={t("settings.vault.status")} icon={<ShieldCheck size={18} />}>
        <span>{statusLabel}</span>
      </SettingsField>
      <SettingsField label={t("settings.vault.store")} hint={t("settings.vault.storeHint")}>
        <input className="mem-input" value={view.path} placeholder={t("common.none")} aria-label={t("settings.vault.store")} readOnly />
      </SettingsField>
    </SettingsSection>

    {!view.configured && (
      <NewPasswordSection
        id="enable"
        pending={pending}
        minLength={minLength}
        title={t("settings.vault.enableTitle")}
        description={t("settings.vault.enableHint", { n: minLength })}
        actionLabel={t("settings.vault.enableAction")}
        workingLabel={t("settings.vault.working")}
        icon={<ShieldCheck size={18} />}
        onSubmit={(next) => run("enable", () => app.SetVaultPassword(next), t("settings.vault.enableSuccess"))}
      />
    )}

    {view.configured && (
      <>
        <SettingsSection title={t("settings.vault.stateTitle")} description={view.unlocked ? t("settings.vault.stateUnlockedHint") : t("settings.vault.stateLockedHint")}>
          {view.unlocked ? (
            <SettingsField label={t("settings.vault.lock")} hint={t("settings.vault.lockHint")} icon={<Lock size={18} />}>
              <button className="btn btn--small" type="button" disabled={busy} onClick={() => void run("lock", async () => app.LockVault(), t("settings.vault.lockSuccess"))}>
                {pending === "lock" ? t("settings.vault.working") : t("settings.vault.lockAction")}
              </button>
            </SettingsField>
          ) : (
            <LockAction
              pending={pending}
              onUnlock={(password) => run("unlock", async () => {
                await unlockCredentialStore(password);
                return app.VaultSettings();
              }, t("settings.vault.unlockSuccess"))}
            />
          )}
        </SettingsSection>

        <NewPasswordSection
          id="change"
          pending={pending}
          minLength={minLength}
          title={t("settings.vault.changeTitle")}
          description={t("settings.vault.changeHint")}
          actionLabel={t("settings.vault.changeAction")}
          workingLabel={t("settings.vault.working")}
          icon={<KeyRound size={18} />}
          currentField={view.unlocked ? t("settings.vault.currentOptional") : t("settings.vault.currentPassword")}
          currentHint={view.unlocked ? t("settings.vault.currentHint") : undefined}
          onSubmit={(next, current) => run("change", () => app.ChangeVaultPassword(current, next), t("settings.vault.changeSuccess"))}
        />

        <DisableSection
          pending={pending}
          onDisable={(password) => run("disable", () => app.DisableVault(password), t("settings.vault.disableSuccess"))}
        />
      </>
    )}
  </>);
}

// NewPasswordSection collects a freshly chosen master password (enable or
// change). The current-password field appears only when re-keying; leaving it
// empty reuses the key the running host already holds.
function NewPasswordSection({
  id,
  pending,
  minLength,
  title,
  description,
  actionLabel,
  workingLabel,
  icon,
  currentField,
  currentHint,
  onSubmit,
}: {
  id: string;
  pending: string | null;
  minLength: number;
  title: string;
  description: string;
  actionLabel: string;
  workingLabel: string;
  icon: ReactNode;
  currentField?: string;
  currentHint?: string;
  onSubmit: (next: string, current: string) => Promise<boolean>;
}) {
  const t = useT();
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [localError, setLocalError] = useState<string | null>(null);
  const busy = pending !== null;

  const submit = async () => {
    setLocalError(null);
    if (next.length < minLength) {
      setLocalError(t("settings.vault.tooShort", { n: minLength }));
      return;
    }
    if (next !== confirm) {
      setLocalError(t("settings.vault.mismatch"));
      return;
    }
    if (await onSubmit(next, current)) {
      setCurrent("");
      setNext("");
      setConfirm("");
    }
  };

  return (
    <SettingsSection title={title} description={description}>
      {currentField && (
        <SettingsField label={currentField} hint={currentHint} icon={icon}>
          <input className="mem-input" type="password" autoComplete="current-password" value={current} disabled={busy} aria-label={currentField} onChange={(event) => setCurrent(event.currentTarget.value)} />
        </SettingsField>
      )}
      <SettingsField label={t("settings.vault.newPassword")} icon={icon}>
        <input className="mem-input" type="password" autoComplete="new-password" value={next} disabled={busy} aria-label={t("settings.vault.newPassword")} onChange={(event) => setNext(event.currentTarget.value)} />
      </SettingsField>
      <SettingsField label={t("settings.vault.confirmPassword")}>
        <input className="mem-input" type="password" autoComplete="new-password" value={confirm} disabled={busy} aria-label={t("settings.vault.confirmPassword")} onChange={(event) => setConfirm(event.currentTarget.value)} />
      </SettingsField>
      {localError && <div className="banner banner--error" role="alert"><span>{localError}</span></div>}
      <div className="settings-inline-controls">
        <button className="btn btn--small" type="button" disabled={busy} onClick={() => void submit()}>
          {pending === id ? workingLabel : actionLabel}
        </button>
      </div>
    </SettingsSection>
  );
}

// LockAction verifies a master password to re-open a locked store in this
// process. Credential reads stay unavailable until it succeeds.
function LockAction({ pending, onUnlock }: { pending: string | null; onUnlock: (password: string) => Promise<boolean> }) {
  const t = useT();
  const [password, setPassword] = useState("");
  const busy = pending !== null;

  const submit = async () => {
    if (await onUnlock(password)) setPassword("");
  };

  return (
    <SettingsField label={t("settings.vault.unlock")} hint={t("settings.vault.unlockHint")} icon={<LockOpen size={18} />}>
      <div className="settings-inline-controls">
        <input className="mem-input" type="password" autoComplete="current-password" value={password} disabled={busy} aria-label={t("settings.vault.unlock")} onChange={(event) => setPassword(event.currentTarget.value)} />
        <button className="btn btn--small" type="button" disabled={busy} onClick={() => void submit()}>
          {pending === "unlock" ? t("settings.vault.working") : t("settings.vault.unlockAction")}
        </button>
      </div>
    </SettingsField>
  );
}

// DisableSection decrypts the store back to a plaintext .env. It is destructive
// (the at-rest guarantee ends), so the action confirms in place.
function DisableSection({ pending, onDisable }: { pending: string | null; onDisable: (password: string) => Promise<boolean> }) {
  const t = useT();
  const [password, setPassword] = useState("");
  const busy = pending !== null;

  const submit = async () => {
    if (await onDisable(password)) setPassword("");
  };

  return (
    <SettingsSection title={t("settings.vault.disableTitle")} description={t("settings.vault.disableHint")}>
      <SettingsField label={t("settings.vault.disable")} hint={t("settings.vault.disablePasswordHint")} icon={<ShieldOff size={18} />}>
        <div className="settings-inline-controls">
          <input className="mem-input" type="password" autoComplete="current-password" value={password} disabled={busy} aria-label={t("settings.vault.disable")} onChange={(event) => setPassword(event.currentTarget.value)} />
          <InlineConfirmButton
            label={t("settings.vault.disableAction")}
            confirmLabel={t("settings.vault.disableConfirm")}
            cancelLabel={t("common.cancel")}
            danger
            disabled={busy}
            onConfirm={() => void submit()}
          />
        </div>
      </SettingsField>
    </SettingsSection>
  );
}
