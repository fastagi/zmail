import { act } from 'react';
import { hydrateRoot, type Root } from 'react-dom/client';
import { renderToString } from 'react-dom/server';
import { fireEvent, render, screen } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { ThemeToggle } from '@/components/theme-toggle';
import commonMessages from '@/i18n/modules/common/en-US';

const themeState = vi.hoisted(() => ({
  selected: 'system',
  setTheme: vi.fn(),
}));

vi.mock('next-themes', () => ({
  useTheme: () => ({
    theme: themeState.selected,
    setTheme: themeState.setTheme,
  }),
}));

function renderThemeToggle() {
  return (
    <NextIntlClientProvider
      locale="en-US"
      messages={{ common: commonMessages } as unknown as AbstractIntlMessages}
    >
      <ThemeToggle />
    </NextIntlClientProvider>
  );
}

describe('ThemeToggle', () => {
  beforeEach(() => {
    themeState.selected = 'system';
    themeState.setTheme.mockClear();
  });

  it('uses a shadcn three-state selector with light, dark, and system options', () => {
    render(renderThemeToggle());

    const selector = screen.getByRole('combobox', { name: 'Toggle theme' });
    expect(selector).toHaveTextContent('System');

    fireEvent.keyDown(selector, { key: 'ArrowDown' });
    expect(screen.getAllByRole('option').map(option => option.textContent)).toEqual([
      'Light',
      'Dark',
      'System',
    ]);

    fireEvent.click(screen.getByRole('option', { name: 'Dark' }));
    expect(themeState.setTheme).toHaveBeenCalledWith('dark');
  });

  it('hydrates a persisted theme without changing the server first frame', async () => {
    themeState.selected = 'dark';
    const container = document.createElement('div');
    const serverMarkup = renderToString(renderThemeToggle());
    expect(serverMarkup).toContain('aria-label="Toggle theme"');
    expect(serverMarkup).toContain('lucide-monitor');
    container.innerHTML = serverMarkup;
    document.body.appendChild(container);

    const recoverableError = vi.fn();
    let root: Root | undefined;
    await act(async () => {
      root = hydrateRoot(container, renderThemeToggle(), { onRecoverableError: recoverableError });
    });

    expect(recoverableError).not.toHaveBeenCalled();
    expect(container.querySelector('[role="combobox"]')).toHaveTextContent('Dark');

    await act(async () => root?.unmount());
    container.remove();
  });
});
