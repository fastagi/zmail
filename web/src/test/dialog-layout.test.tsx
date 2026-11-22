import { render, screen } from '@testing-library/react';
import { NextIntlClientProvider, type AbstractIntlMessages } from 'next-intl';
import { describe, expect, it } from 'vitest';

import { Dialog, DialogContent, DialogTitle } from '@/components/ui/dialog';
import { messages } from '@/i18n/modules';

describe('dialog layout', () => {
  it.each(['sm', 'default', 'lg', 'xl', 'full'] as const)(
    'keeps a margin and rounded corners at every width (%s)',
    size => {
      render(
        <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
          <Dialog open>
            <DialogContent size={size} aria-describedby={undefined}>
              <DialogTitle>Title</DialogTitle>
            </DialogContent>
          </Dialog>
        </NextIntlClientProvider>
      );
      const classes = screen.getByRole('dialog').className.split(/\s+/);
      expect(classes).toContain('w-[calc(100%-2rem)]');
      expect(classes).not.toContain('w-full');
      expect(classes).toContain('max-h-[calc(100dvh-2rem)]');
      expect(classes).toContain('rounded-xl');
    }
  );

  it.each([
    ['sm', 'max-w-[28rem]'],
    ['default', 'max-w-[36rem]'],
    ['lg', 'max-w-[48rem]'],
    ['xl', 'max-w-[64rem]'],
  ] as const)('caps the %s dialog at a dialog-sized width', (size, maxWidth) => {
    render(
      <NextIntlClientProvider locale="en-US" messages={messages as unknown as AbstractIntlMessages}>
        <Dialog open>
          <DialogContent size={size} aria-describedby={undefined}>
            <DialogTitle>Title</DialogTitle>
          </DialogContent>
        </Dialog>
      </NextIntlClientProvider>
    );
    expect(screen.getByRole('dialog').className.split(/\s+/)).toContain(maxWidth);
  });
});
