'use client';

import { Bold, Code, Italic, Link2, List, Quote, type LucideIcon } from 'lucide-react';
import { useLocale } from 'next-intl';

import { Button } from '@/components/ui/button';
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip';

export type ComposeFormatAction = 'bold' | 'italic' | 'list' | 'quote' | 'link' | 'code';

export interface TextSelectionTransform {
  value: string;
  selectionStart: number;
  selectionEnd: number;
}

function clampSelection(value: string, start: number, end: number): [number, number] {
  const safeStart = Math.max(0, Math.min(start, value.length));
  const safeEnd = Math.max(safeStart, Math.min(end, value.length));
  return [safeStart, safeEnd];
}

function applyInlineWrap(
  value: string,
  start: number,
  end: number,
  marker: string,
  allowToggle: (before: string, after: string) => boolean = (before, after) =>
    before.endsWith(marker) && after.startsWith(marker)
): TextSelectionTransform {
  const before = value.slice(0, start);
  const selected = value.slice(start, end);
  const after = value.slice(end);
  const len = marker.length;

  if (selected.length > 0 && allowToggle(before, after)) {
    return {
      value: before.slice(0, before.length - len) + selected + after.slice(len),
      selectionStart: start - len,
      selectionEnd: end - len,
    };
  }

  return {
    value: before + marker + selected + marker + after,
    selectionStart: start + len,
    selectionEnd: end + len,
  };
}

function applyLinePrefix(
  value: string,
  start: number,
  end: number,
  prefix: string
): TextSelectionTransform {
  const lineStart = start === 0 ? 0 : value.lastIndexOf('\n', start - 1) + 1;
  const searchEndFrom = end > start && value[end - 1] === '\n' ? end - 1 : end;
  const nextNewline = value.indexOf('\n', searchEndFrom);
  const lineEnd = nextNewline === -1 ? value.length : nextNewline;

  const block = value.slice(lineStart, lineEnd);
  const lines = block.split('\n');
  const nonEmptyLines = lines.filter(line => line.length > 0);
  const allPrefixed =
    nonEmptyLines.length > 0 && nonEmptyLines.every(line => line.startsWith(prefix));

  const transformedLines = lines.map(line => {
    if (allPrefixed) {
      return line.startsWith(prefix) ? line.slice(prefix.length) : line;
    }
    return prefix + line;
  });

  const nextBlock = transformedLines.join('\n');
  const nextValue = value.slice(0, lineStart) + nextBlock + value.slice(lineEnd);
  const firstLineDelta = transformedLines[0].length - lines[0].length;
  const totalDelta = nextBlock.length - block.length;

  return {
    value: nextValue,
    selectionStart: Math.max(lineStart, start + firstLineDelta),
    selectionEnd: Math.max(lineStart, end + totalDelta),
  };
}

function applyCodeFormat(value: string, start: number, end: number): TextSelectionTransform {
  const selected = value.slice(start, end);
  if (selected.includes('\n')) {
    const before = value.slice(0, start);
    const after = value.slice(end);
    const prefix = '```\n';
    const suffix = '\n```';
    return {
      value: before + prefix + selected + suffix + after,
      selectionStart: start + prefix.length,
      selectionEnd: end + prefix.length,
    };
  }
  return applyInlineWrap(value, start, end, '`');
}

function applyLinkFormat(value: string, start: number, end: number): TextSelectionTransform {
  const before = value.slice(0, start);
  const selected = value.slice(start, end);
  const after = value.slice(end);

  if (selected.length === 0) {
    const inserted = '[](https://)';
    return {
      value: before + inserted + after,
      selectionStart: start + 1,
      selectionEnd: start + 1,
    };
  }

  if (/^https?:\/\/\S+$/iu.test(selected.trim())) {
    const url = selected.trim();
    const inserted = `[](${url})`;
    return {
      value: before + inserted + after,
      selectionStart: start + 1,
      selectionEnd: start + 1,
    };
  }

  const placeholderUrl = 'https://';
  const inserted = `[${selected}](${placeholderUrl})`;
  const urlStart = start + selected.length + 3;
  return {
    value: before + inserted + after,
    selectionStart: urlStart,
    selectionEnd: urlStart + placeholderUrl.length,
  };
}

/**
 * Pure markdown-style formatting transform for the plain-text compose body textarea.
 * Preserves selection/cursor bounds so React can restore them synchronously after state updates.
 */
export function applyComposeFormat(
  value: string,
  selectionStart: number,
  selectionEnd: number,
  action: ComposeFormatAction
): TextSelectionTransform {
  const [start, end] = clampSelection(value, selectionStart, selectionEnd);
  switch (action) {
    case 'bold':
      return applyInlineWrap(value, start, end, '**');
    case 'italic':
      return applyInlineWrap(
        value,
        start,
        end,
        '*',
        (before, after) =>
          before.endsWith('*') &&
          after.startsWith('*') &&
          !(before.endsWith('**') && after.startsWith('**') && !before.endsWith('***'))
      );
    case 'list':
      return applyLinePrefix(value, start, end, '- ');
    case 'quote':
      return applyLinePrefix(value, start, end, '> ');
    case 'link':
      return applyLinkFormat(value, start, end);
    case 'code':
      return applyCodeFormat(value, start, end);
  }
}

interface ToolbarItem {
  action: ComposeFormatAction;
  icon: LucideIcon;
  label: string;
  tooltip: string;
  dividerBefore?: boolean;
}

export function ComposeFormatToolbar({
  disabled,
  onFormat,
}: {
  disabled: boolean;
  onFormat: (action: ComposeFormatAction) => void;
}) {
  const locale = useLocale();
  const isZh = locale.toLowerCase().startsWith('zh');

  const items: readonly ToolbarItem[] = [
    {
      action: 'bold',
      icon: Bold,
      label: isZh ? '加粗' : 'Bold',
      tooltip: isZh ? '加粗 (⌘B)' : 'Bold (⌘B)',
    },
    {
      action: 'italic',
      icon: Italic,
      label: isZh ? '斜体' : 'Italic',
      tooltip: isZh ? '斜体 (⌘I)' : 'Italic (⌘I)',
    },
    {
      action: 'list',
      icon: List,
      label: isZh ? '无序列表' : 'Bulleted list',
      tooltip: isZh ? '无序列表' : 'Bulleted list',
      dividerBefore: true,
    },
    {
      action: 'quote',
      icon: Quote,
      label: isZh ? '引用' : 'Quote',
      tooltip: isZh ? '引用' : 'Quote',
    },
    {
      action: 'link',
      icon: Link2,
      label: isZh ? '链接' : 'Link',
      tooltip: isZh ? '插入链接' : 'Insert link',
      dividerBefore: true,
    },
    {
      action: 'code',
      icon: Code,
      label: isZh ? '代码' : 'Code',
      tooltip: isZh ? '行内代码' : 'Code',
    },
  ];

  return (
    <div
      role="toolbar"
      aria-label={isZh ? '文本格式' : 'Formatting'}
      data-compose-format-toolbar="true"
      className="flex flex-wrap items-center gap-0.5 rounded-md border border-border/70 bg-muted/30 px-1 py-0.5"
    >
      {items.map(item => {
        const Icon = item.icon;
        return (
          <span key={item.action} className="inline-flex items-center">
            {item.dividerBefore ? (
              <span aria-hidden="true" className="mx-1 h-3.5 w-px bg-border/70" />
            ) : null}
            <Tooltip>
              <TooltipTrigger asChild>
                <Button
                  type="button"
                  size="xs"
                  variant="ghost"
                  isIcon
                  disabled={disabled}
                  aria-label={item.label}
                  icon={<Icon className="size-3.5" aria-hidden="true" />}
                  onMouseDown={event => event.preventDefault()}
                  onClick={() => onFormat(item.action)}
                />
              </TooltipTrigger>
              <TooltipContent side="top" sideOffset={6}>
                {item.tooltip}
              </TooltipContent>
            </Tooltip>
          </span>
        );
      })}
    </div>
  );
}
