import { create } from 'zustand';

export type WorkspaceMode = 'personal' | 'work';

export const WORKSPACE_MODE_STORAGE_KEY = 'zmail.workspace_mode';

export const DEFAULT_WORKSPACE_MODE: WorkspaceMode = 'work';

/**
 * B2B-only navigation sections hidden when the user switches to Personal Mode.
 * Work Mode keeps every organization, domain, campaign, and admin surface visible.
 */
export const PERSONAL_MODE_HIDDEN_SECTIONS = [
  'domains',
  'broadcasts',
  'templates',
  'contacts',
  'developer',
  'members',
  'admin',
] as const;

export type PersonalModeHiddenSection = (typeof PERSONAL_MODE_HIDDEN_SECTIONS)[number];

const PERSONAL_MODE_HIDDEN_SET: ReadonlySet<string> = new Set(PERSONAL_MODE_HIDDEN_SECTIONS);

export function isWorkspaceMode(value: unknown): value is WorkspaceMode {
  return value === 'personal' || value === 'work';
}

export function isNavSectionVisibleInMode(section: string, mode: WorkspaceMode): boolean {
  if (mode === 'work') {
    return true;
  }
  return !PERSONAL_MODE_HIDDEN_SET.has(section);
}

export function readPersistedWorkspaceMode(): WorkspaceMode {
  if (typeof window === 'undefined') {
    return DEFAULT_WORKSPACE_MODE;
  }
  try {
    const raw = window.localStorage.getItem(WORKSPACE_MODE_STORAGE_KEY);
    return isWorkspaceMode(raw) ? raw : DEFAULT_WORKSPACE_MODE;
  } catch {
    return DEFAULT_WORKSPACE_MODE;
  }
}

export function writePersistedWorkspaceMode(mode: WorkspaceMode): void {
  if (typeof window === 'undefined') {
    return;
  }
  try {
    window.localStorage.setItem(WORKSPACE_MODE_STORAGE_KEY, mode);
  } catch {
    // Ignore storage quota or private-browsing security errors.
  }
}

export interface WorkspaceModeState {
  mode: WorkspaceMode;
  isPersonal: boolean;
  setMode: (mode: WorkspaceMode) => void;
  toggleMode: () => void;
  hydrate: () => void;
}

export function createWorkspaceModeStore(initialMode?: WorkspaceMode) {
  const startMode = initialMode ?? readPersistedWorkspaceMode();

  return create<WorkspaceModeState>()((set, get) => ({
    mode: startMode,
    isPersonal: startMode === 'personal',

    setMode: (mode: WorkspaceMode) => {
      writePersistedWorkspaceMode(mode);
      set({
        mode,
        isPersonal: mode === 'personal',
      });
    },

    toggleMode: () => {
      const nextMode: WorkspaceMode = get().mode === 'personal' ? 'work' : 'personal';
      writePersistedWorkspaceMode(nextMode);
      set({
        mode: nextMode,
        isPersonal: nextMode === 'personal',
      });
    },

    hydrate: () => {
      const persisted = readPersistedWorkspaceMode();
      if (persisted !== get().mode) {
        set({
          mode: persisted,
          isPersonal: persisted === 'personal',
        });
      }
    },
  }));
}

export const useWorkspaceModeStore = createWorkspaceModeStore(DEFAULT_WORKSPACE_MODE);

export const selectWorkspaceMode = (state: WorkspaceModeState): WorkspaceMode => state.mode;
export const selectIsPersonal = (state: WorkspaceModeState): boolean => state.mode === 'personal';

export function useIsPersonalMode(): boolean {
  return useWorkspaceModeStore(selectIsPersonal);
}
