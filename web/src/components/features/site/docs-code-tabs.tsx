'use client';

import { useState } from 'react';
import { Check, Copy } from 'lucide-react';
import { cn } from '@/utils';

export interface DocsCodeTab {
  id: string;
  label: string;
  code: string;
}

interface DocsCodeTabsProps {
  tabs: readonly DocsCodeTab[];
  copyLabel: string;
  copiedLabel: string;
  ariaLabel: string;
}

/**
 * Lightweight tabbed code block with one-click copy for public documentation pages.
 * Keeps client JS minimal so /docs stays well within route bundle budgets.
 */
export function DocsCodeTabs({ tabs, copyLabel, copiedLabel, ariaLabel }: DocsCodeTabsProps) {
  const [activeId, setActiveId] = useState(tabs[0]?.id ?? '');
  const [copied, setCopied] = useState(false);

  const activeTab = tabs.find(tab => tab.id === activeId) ?? tabs[0];
  if (!activeTab) return null;

  async function handleCopy() {
    if (!activeTab) return;
    try {
      await navigator.clipboard.writeText(activeTab.code);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1800);
    } catch {
      // Ignore clipboard failures in restricted contexts.
    }
  }

  return (
    <div className="overflow-hidden rounded-xl border border-border/80 bg-bg-subtle">
      <div className="flex items-center justify-between gap-2 border-b border-border/70 bg-bg-surface/70 px-3 py-2">
        {tabs.length > 1 ? (
          <div
            role="tablist"
            aria-label={ariaLabel}
            className="flex items-center gap-1 overflow-x-auto"
          >
            {tabs.map(tab => {
              const selected = tab.id === activeTab.id;
              return (
                <button
                  key={tab.id}
                  type="button"
                  role="tab"
                  aria-selected={selected}
                  onClick={() => {
                    setActiveId(tab.id);
                    setCopied(false);
                  }}
                  className={cn(
                    'shrink-0 rounded-md px-2.5 py-1 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary',
                    selected
                      ? 'bg-primary/10 text-primary'
                      : 'text-text-muted hover:bg-muted/60 hover:text-foreground'
                  )}
                >
                  {tab.label}
                </button>
              );
            })}
          </div>
        ) : (
          <span className="px-1 font-mono text-xs font-medium text-text-muted">
            {activeTab.label}
          </span>
        )}

        <button
          type="button"
          onClick={handleCopy}
          aria-label={copied ? copiedLabel : copyLabel}
          className="inline-flex shrink-0 items-center gap-1.5 rounded-md border border-border/60 bg-background/70 px-2.5 py-1 text-xs font-medium text-text-subtle transition-colors hover:border-border hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary"
        >
          {copied ? (
            <Check className="size-3.5 text-emerald-600 dark:text-emerald-400" aria-hidden="true" />
          ) : (
            <Copy className="size-3.5" aria-hidden="true" />
          )}
          <span>{copied ? copiedLabel : copyLabel}</span>
        </button>
      </div>

      <pre
        tabIndex={0}
        aria-label={ariaLabel}
        className="overflow-x-auto p-4 font-mono text-xs leading-relaxed text-foreground sm:p-5 sm:text-[13px]"
      >
        <code>{activeTab.code}</code>
      </pre>
    </div>
  );
}
