import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import {
  detectPersonalProvider,
  PersonalProviderPicker,
} from '@/features/mailbox/components/personal-provider-picker';
import { WorkspaceModeToggle } from '@/features/mailbox/components/workspace-mode-toggle';
import {
  createWorkspaceModeStore,
  DEFAULT_WORKSPACE_MODE,
  isNavSectionVisibleInMode,
  PERSONAL_MODE_HIDDEN_SECTIONS,
  selectIsPersonal,
  useWorkspaceModeStore,
  WORKSPACE_MODE_STORAGE_KEY,
} from '@/features/mailbox/stores/workspace-mode';

describe('workspace-mode store', () => {
  beforeEach(() => {
    window.localStorage.clear();
    useWorkspaceModeStore.setState({
      mode: DEFAULT_WORKSPACE_MODE,
      isPersonal: false,
    });
  });

  it('defaults to work mode and persists transitions to localStorage', () => {
    expect(useWorkspaceModeStore.getState().mode).toBe('work');
    expect(selectIsPersonal(useWorkspaceModeStore.getState())).toBe(false);
    expect(useWorkspaceModeStore.getState().isPersonal).toBe(false);

    useWorkspaceModeStore.getState().setMode('personal');
    expect(useWorkspaceModeStore.getState().mode).toBe('personal');
    expect(selectIsPersonal(useWorkspaceModeStore.getState())).toBe(true);
    expect(useWorkspaceModeStore.getState().isPersonal).toBe(true);
    expect(window.localStorage.getItem(WORKSPACE_MODE_STORAGE_KEY)).toBe('personal');

    useWorkspaceModeStore.getState().toggleMode();
    expect(useWorkspaceModeStore.getState().mode).toBe('work');
    expect(selectIsPersonal(useWorkspaceModeStore.getState())).toBe(false);
    expect(window.localStorage.getItem(WORKSPACE_MODE_STORAGE_KEY)).toBe('work');

    useWorkspaceModeStore.getState().toggleMode();
    expect(useWorkspaceModeStore.getState().mode).toBe('personal');
    expect(window.localStorage.getItem(WORKSPACE_MODE_STORAGE_KEY)).toBe('personal');
  });

  it('restores persisted mode safely and falls back to work on invalid values', () => {
    window.localStorage.setItem(WORKSPACE_MODE_STORAGE_KEY, 'personal');
    const restoredStore = createWorkspaceModeStore();
    expect(restoredStore.getState().mode).toBe('personal');
    expect(restoredStore.getState().isPersonal).toBe(true);

    window.localStorage.setItem(WORKSPACE_MODE_STORAGE_KEY, 'unknown-mode');
    const fallbackStore = createWorkspaceModeStore();
    expect(fallbackStore.getState().mode).toBe('work');
    expect(fallbackStore.getState().isPersonal).toBe(false);
  });

  it('defines B2B-only sections hidden in Personal Mode', () => {
    expect(PERSONAL_MODE_HIDDEN_SECTIONS).toEqual([
      'domains',
      'broadcasts',
      'templates',
      'contacts',
      'developer',
      'members',
      'admin',
    ]);

    for (const section of PERSONAL_MODE_HIDDEN_SECTIONS) {
      expect(isNavSectionVisibleInMode(section, 'personal')).toBe(false);
      expect(isNavSectionVisibleInMode(section, 'work')).toBe(true);
    }

    expect(isNavSectionVisibleInMode('inbox', 'personal')).toBe(true);
    expect(isNavSectionVisibleInMode('external', 'personal')).toBe(true);
  });
});

describe('WorkspaceModeToggle', () => {
  beforeEach(() => {
    window.localStorage.clear();
    useWorkspaceModeStore.setState({
      mode: DEFAULT_WORKSPACE_MODE,
      isPersonal: false,
    });
  });

  it('renders an accessible radiogroup and switches between Work and Personal on click', () => {
    render(<WorkspaceModeToggle />);

    const group = screen.getByRole('radiogroup', { name: 'Workspace mode' });
    expect(group).toHaveAttribute('data-mode', 'work');

    const personalRadio = screen.getByRole('radio', { name: 'Personal' });
    const workRadio = screen.getByRole('radio', { name: 'Work' });

    expect(workRadio).toHaveAttribute('aria-checked', 'true');
    expect(personalRadio).toHaveAttribute('aria-checked', 'false');

    fireEvent.click(personalRadio);

    expect(personalRadio).toHaveAttribute('aria-checked', 'true');
    expect(workRadio).toHaveAttribute('aria-checked', 'false');
    expect(useWorkspaceModeStore.getState().mode).toBe('personal');
    expect(window.localStorage.getItem(WORKSPACE_MODE_STORAGE_KEY)).toBe('personal');

    fireEvent.click(workRadio);

    expect(workRadio).toHaveAttribute('aria-checked', 'true');
    expect(personalRadio).toHaveAttribute('aria-checked', 'false');
    expect(useWorkspaceModeStore.getState().mode).toBe('work');
  });

  it('supports keyboard arrow navigation and controlled mode', () => {
    const onChange = vi.fn();
    render(<WorkspaceModeToggle value="work" onChange={onChange} />);

    const workRadio = screen.getByRole('radio', { name: 'Work' });
    fireEvent.keyDown(workRadio, { key: 'ArrowLeft' });

    expect(onChange).toHaveBeenCalledWith('personal');
    // Controlled mode does not mutate the global store directly.
    expect(useWorkspaceModeStore.getState().mode).toBe('work');
  });
});

describe('PersonalProviderPicker', () => {
  it('auto-detects Gmail OAuth, QQ App Password, Outlook OAuth, and Custom IMAP from email input', () => {
    const onSelectProvider = vi.fn();
    render(<PersonalProviderPicker onSelectProvider={onSelectProvider} />);

    const input = screen.getByRole('textbox', { name: 'Email address' });

    // Gmail -> OAuth
    fireEvent.change(input, { target: { value: 'alice@gmail.com' } });
    expect(screen.getByRole('status')).toHaveTextContent('Gmail');
    expect(screen.getByRole('status')).toHaveTextContent('OAuth 2.0');
    fireEvent.click(screen.getByRole('button', { name: 'Connect Gmail' }));
    expect(onSelectProvider).toHaveBeenLastCalledWith(
      expect.objectContaining({
        id: 'gmail',
        authMethod: 'oauth',
        imapHost: 'imap.gmail.com',
        email: 'alice@gmail.com',
      })
    );

    // QQ -> App Password with imap.qq.com
    fireEvent.change(input, { target: { value: 'bob@qq.com' } });
    expect(screen.getByRole('status')).toHaveTextContent('QQ Mail');
    expect(screen.getByRole('status')).toHaveTextContent('App Password');
    expect(screen.getByRole('status')).toHaveTextContent('imap.qq.com:993');
    fireEvent.click(screen.getByRole('button', { name: 'Connect QQ Mail' }));
    expect(onSelectProvider).toHaveBeenLastCalledWith(
      expect.objectContaining({
        id: 'qq',
        authMethod: 'app_password',
        imapHost: 'imap.qq.com',
        smtpHost: 'smtp.qq.com',
        email: 'bob@qq.com',
      })
    );

    // Outlook -> OAuth
    fireEvent.change(input, { target: { value: 'carol@outlook.com' } });
    expect(screen.getByRole('status')).toHaveTextContent('Outlook');
    expect(screen.getByRole('status')).toHaveTextContent('OAuth 2.0');
    fireEvent.click(screen.getByRole('button', { name: 'Connect Outlook' }));
    expect(onSelectProvider).toHaveBeenLastCalledWith(
      expect.objectContaining({
        id: 'outlook',
        authMethod: 'oauth',
        imapHost: 'outlook.office365.com',
        email: 'carol@outlook.com',
      })
    );

    // Custom domain -> Custom IMAP
    fireEvent.change(input, { target: { value: 'dan@custom.io' } });
    expect(screen.getByRole('status')).toHaveTextContent('Custom IMAP');
    fireEvent.click(screen.getByRole('button', { name: 'Connect Custom IMAP' }));
    expect(onSelectProvider).toHaveBeenLastCalledWith(
      expect.objectContaining({
        id: 'custom',
        authMethod: 'app_password',
        imapHost: '',
        imapPort: 993,
        email: 'dan@custom.io',
      })
    );
  });

  it('auto-detects additional personal providers via detectPersonalProvider', () => {
    expect(detectPersonalProvider('me@icloud.com')).toMatchObject({
      id: 'icloud',
      authMethod: 'app_password',
      imapHost: 'imap.mail.me.com',
    });
    expect(detectPersonalProvider('dev@163.com')).toMatchObject({
      id: 'netease163',
      imapHost: 'imap.163.com',
    });
    expect(detectPersonalProvider('dev@126.com')).toMatchObject({
      id: 'netease126',
      imapHost: 'imap.126.com',
    });
    expect(detectPersonalProvider('user@fastmail.com')).toMatchObject({
      id: 'fastmail',
      imapHost: 'imap.fastmail.com',
    });
    expect(detectPersonalProvider('user@yahoo.com')).toMatchObject({
      id: 'yahoo',
      imapHost: 'imap.mail.yahoo.com',
    });
    expect(detectPersonalProvider('incomplete@')).toBeNull();
  });

  it('invokes onSelectProvider with pre-filled server hints when clicking provider tiles directly', () => {
    const onSelectProvider = vi.fn();
    render(<PersonalProviderPicker onSelectProvider={onSelectProvider} />);

    fireEvent.click(screen.getByRole('button', { name: /QQ Mail/i }));
    expect(onSelectProvider).toHaveBeenCalledWith(
      expect.objectContaining({
        id: 'qq',
        authMethod: 'app_password',
        imapHost: 'imap.qq.com',
        imapPort: 993,
        smtpHost: 'smtp.qq.com',
        smtpPort: 465,
      })
    );

    fireEvent.click(screen.getByRole('button', { name: /Gmail/i }));
    expect(onSelectProvider).toHaveBeenLastCalledWith(
      expect.objectContaining({
        id: 'gmail',
        authMethod: 'oauth',
        imapHost: 'imap.gmail.com',
      })
    );

    fireEvent.click(screen.getByRole('button', { name: /Custom IMAP/i }));
    expect(onSelectProvider).toHaveBeenLastCalledWith(
      expect.objectContaining({
        id: 'custom',
        authMethod: 'app_password',
        imapPort: 993,
      })
    );
  });
});
