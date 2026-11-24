import { useMemo, useState } from 'react';
import {
  detectProviderFromEmail,
  type MailUiComposePayload,
  type MailUiProviderId,
  type MailUiProviderPreset,
  type WorkspaceMode,
} from '@zmail/mail-ui';

const FEATURED_PROVIDER_IDS: readonly MailUiProviderId[] = [
  'gmail',
  'outlook',
  'netease163',
  'qq',
  'icloud',
];

export interface QuickConnectSubmit {
  email: string;
  secret: string;
  mode: WorkspaceMode;
  preset: MailUiProviderPreset;
}

export function QuickConnectModal({
  presets,
  accountCount,
  maxAccounts,
  onClose,
  onSubmit,
}: {
  presets: readonly MailUiProviderPreset[];
  accountCount: number;
  maxAccounts: number;
  onClose: () => void;
  onSubmit: (payload: QuickConnectSubmit) => void;
}) {
  const [email, setEmail] = useState('');
  const [secret, setSecret] = useState('');
  const [providerId, setProviderId] = useState<MailUiProviderId>('gmail');
  const [workspaceMode, setWorkspaceMode] = useState<WorkspaceMode>('personal');

  const detected = useMemo(() => detectProviderFromEmail(email), [email]);
  const selectedPreset = useMemo(() => {
    if (detected?.matched) return detected.preset;
    return presets.find((p) => p.id === providerId) ?? presets[0]!;
  }, [detected, presets, providerId]);

  const featuredPresets = useMemo(
    () =>
      FEATURED_PROVIDER_IDS.map((id) => presets.find((p) => p.id === id)).filter(
        (p): p is MailUiProviderPreset => Boolean(p)
      ),
    [presets]
  );

  return (
    <div className="modal-backdrop" role="dialog" aria-label="Quick Connect Mailbox">
      <div className="modal-card">
        <div className="modal-header">
          <strong>Quick Connect ({accountCount}/{maxAccounts})</strong>
          <button type="button" className="btn-ghost" onClick={onClose}>Close</button>
        </div>

        <div className="provider-grid">
          {featuredPresets.map((preset) => (
            <button
              key={preset.id}
              type="button"
              className={selectedPreset.id === preset.id ? 'provider-chip active' : 'provider-chip'}
              onClick={() => setProviderId(preset.id)}
            >
              <span className="provider-dot" style={{ backgroundColor: preset.badgeColor }}>
                {preset.shortLabel}
              </span>
              <span>{preset.label}</span>
            </button>
          ))}
        </div>

        <input
          type="email"
          placeholder="you@gmail.com, you@163.com, you@qq.com, you@icloud.com..."
          value={email}
          onChange={(event) => {
            setEmail(event.target.value);
            const hit = detectProviderFromEmail(event.target.value);
            if (hit?.matched) setProviderId(hit.preset.id);
          }}
        />

        {selectedPreset.authStrategy !== 'oauth2' && (
          <input
            type="password"
            placeholder="App password / authorization code (stored in OS Keychain)"
            value={secret}
            onChange={(event) => setSecret(event.target.value)}
          />
        )}

        <div className="provider-meta">
          <div>
            <strong>{selectedPreset.label}</strong> ({selectedPreset.authStrategy}) — IMAP{' '}
            {selectedPreset.imapHost || 'custom'}:{selectedPreset.imapPort} / SMTP{' '}
            {selectedPreset.smtpHost || 'custom'}:{selectedPreset.smtpPort}
          </div>
          <div>{selectedPreset.guidance}</div>
        </div>

        <div className="modal-footer">
          <div className="mode-switch">
            {(['personal', 'work'] as const).map((next) => (
              <button
                key={next}
                type="button"
                className={workspaceMode === next ? 'btn-primary' : 'btn-secondary'}
                onClick={() => setWorkspaceMode(next)}
              >
                {next === 'work' ? 'Work' : 'Personal'}
              </button>
            ))}
          </div>
          <button
            type="button"
            className="btn-primary"
            onClick={() => onSubmit({ email, secret, mode: workspaceMode, preset: selectedPreset })}
          >
            {selectedPreset.authStrategy === 'oauth2' ? 'Continue with OAuth' : 'Connect Mailbox'}
          </button>
        </div>
      </div>
    </div>
  );
}

export function ComposeModal({
  draft,
  toText,
  onToTextChange,
  onChangeDraft,
  onClose,
  onSend,
}: {
  draft: MailUiComposePayload;
  toText: string;
  onToTextChange: (value: string) => void;
  onChangeDraft: (updater: (prev: MailUiComposePayload | null) => MailUiComposePayload | null) => void;
  onClose: () => void;
  onSend: () => void;
}) {
  return (
    <div className="modal-backdrop" role="dialog" aria-label="Compose message">
      <div className="modal-card">
        <div className="modal-header">
          <strong>
            {draft.mode === 'new' ? 'New Message' : draft.mode.toUpperCase()} — From {draft.fromEmail}
          </strong>
          <button type="button" className="btn-ghost" onClick={onClose}>Close</button>
        </div>
        <input
          type="text"
          placeholder="To (comma separated)"
          value={toText}
          onChange={(event) => onToTextChange(event.target.value)}
        />
        <input
          type="text"
          placeholder="Subject"
          value={draft.subject}
          onChange={(event) =>
            onChangeDraft((prev) => (prev ? { ...prev, subject: event.target.value } : prev))
          }
        />
        <textarea
          rows={8}
          placeholder="Write your message..."
          value={draft.bodyText}
          onChange={(event) =>
            onChangeDraft((prev) => (prev ? { ...prev, bodyText: event.target.value } : prev))
          }
        />
        <div className="modal-footer">
          <span className="status-pill">Sending via {draft.fromEmail}</span>
          <button type="button" className="btn-primary" onClick={onSend}>Send via /v1</button>
        </div>
      </div>
    </div>
  );
}
