'use client';

import { useId, useRef, useState, type ReactNode } from 'react';
import {
  AlertCircle,
  Bold,
  ChevronDown,
  ChevronUp,
  Copy,
  Heading,
  Image as ImageIcon,
  Italic,
  Link as LinkIcon,
  Minus,
  MousePointerClick,
  MoveVertical,
  PanelBottom,
  Plus,
  Trash2,
  Type,
  type LucideIcon,
} from 'lucide-react';

import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select';
import { Textarea } from '@/components/ui/textarea';
import {
  blockAligns,
  blockIssues,
  designBlockTypes,
  imageWidths,
  newBlock,
  newBlockId,
  spacerHeights,
  type BlockAlign,
  type DesignBlock,
  type DesignBlockType,
  type DesignSettings,
  type TemplateDesign,
} from '@/features/mailbox/utils/template-design';
import { useT } from '@/i18n';
import { cn } from '@/utils';

/** Fields every broadcast fills from the contact, offered in the field picker. */
const contactFields = ['first_name', 'last_name', 'email', 'unsubscribe_url'] as const;

/** Example forms shown next to link fields; template syntax, not interface copy. */
const fieldLinkExample = '{{action_url}}';

const blockIcon = {
  heading: Heading,
  text: Type,
  button: MousePointerClick,
  image: ImageIcon,
  divider: Minus,
  spacer: MoveVertical,
  footer: PanelBottom,
} as const satisfies Record<DesignBlockType, LucideIcon>;

const blockKey = {
  heading: 'templates.visual.blockHeading',
  text: 'templates.visual.blockText',
  button: 'templates.visual.blockButton',
  image: 'templates.visual.blockImage',
  divider: 'templates.visual.blockDivider',
  spacer: 'templates.visual.blockSpacer',
  footer: 'templates.visual.blockFooter',
} as const satisfies Record<DesignBlockType, string>;

const alignKey = {
  left: 'templates.visual.alignLeft',
  center: 'templates.visual.alignCenter',
  right: 'templates.visual.alignRight',
} as const satisfies Record<BlockAlign, string>;

const issueKey = {
  buttonLabel: 'templates.visual.issueButtonLabel',
  buttonUrl: 'templates.visual.issueButtonUrl',
  imageSource: 'templates.visual.issueImageSource',
  imageLink: 'templates.visual.issueImageLink',
} as const;

const settingKey = {
  background: 'templates.visual.colorBackground',
  surface: 'templates.visual.colorSurface',
  text: 'templates.visual.colorText',
  accent: 'templates.visual.colorAccent',
} as const satisfies Record<keyof DesignSettings, string>;

/** A one-line summary of a block for the collapsed list. */
function summaryOf(block: DesignBlock): string {
  switch (block.type) {
    case 'heading':
    case 'text':
    case 'footer':
      return block.text.split('\n')[0] ?? '';
    case 'button':
      return block.label;
    case 'image':
      return block.alt || block.src;
    case 'spacer':
      return `${block.height}px`;
    case 'divider':
      return '';
  }
}

/**
 * The block editor: email colors, then the blocks in order. A block opens for editing when it is
 * selected; new blocks go after the selected one. Every change is passed up as a whole design.
 */
export function TemplateVisualEditor({
  design,
  variables,
  disabled,
  onChange,
}: {
  design: TemplateDesign;
  /** Variables the template already uses, offered in the field picker. */
  variables: readonly string[];
  disabled: boolean;
  onChange: (design: TemplateDesign) => void;
}) {
  const t = useT('mailbox');
  const [selectedId, setSelectedId] = useState<string | null>(design.blocks[0]?.id ?? null);
  const fields = [...new Set([...contactFields, ...variables])];

  const setBlocks = (blocks: DesignBlock[]) => onChange({ ...design, blocks });
  const replace = (block: DesignBlock) =>
    setBlocks(design.blocks.map(existing => (existing.id === block.id ? block : existing)));

  const insert = (type: DesignBlockType) => {
    const block = newBlock(type);
    const at = design.blocks.findIndex(existing => existing.id === selectedId);
    const blocks = [...design.blocks];
    blocks.splice(at < 0 ? blocks.length : at + 1, 0, block);
    setBlocks(blocks);
    setSelectedId(block.id);
  };

  const move = (index: number, by: -1 | 1) => {
    const target = index + by;
    if (target < 0 || target >= design.blocks.length) return;
    const blocks = [...design.blocks];
    const [block] = blocks.splice(index, 1);
    if (block) blocks.splice(target, 0, block);
    setBlocks(blocks);
  };

  const duplicate = (index: number) => {
    const source = design.blocks[index];
    if (!source) return;
    const copy = { ...source, id: newBlockId() } as DesignBlock;
    const blocks = [...design.blocks];
    blocks.splice(index + 1, 0, copy);
    setBlocks(blocks);
    setSelectedId(copy.id);
  };

  const remove = (index: number) => {
    const blocks = design.blocks.filter((_, at) => at !== index);
    setBlocks(blocks);
    setSelectedId(blocks[Math.min(index, blocks.length - 1)]?.id ?? null);
  };

  return (
    <div className="space-y-4">
      <fieldset className="space-y-2 rounded-md border p-3" disabled={disabled}>
        <legend className="px-1 text-sm font-medium">{t('templates.visual.style')}</legend>
        <div className="grid grid-cols-2 gap-3 sm:grid-cols-4">
          {(Object.keys(settingKey) as (keyof DesignSettings)[]).map(key => (
            <ColorField
              key={key}
              label={t(settingKey[key])}
              value={design.settings[key]}
              onChange={value =>
                onChange({ ...design, settings: { ...design.settings, [key]: value } })
              }
            />
          ))}
        </div>
      </fieldset>

      <section aria-label={t('templates.visual.blocks')} className="space-y-2">
        <h3 className="text-sm font-medium">{t('templates.visual.blocks')}</h3>
        {design.blocks.length === 0 ? (
          <p className="rounded-md border border-dashed p-4 text-center text-sm text-muted-foreground">
            {t('templates.visual.empty')}
          </p>
        ) : (
          <ol className="space-y-2">
            {design.blocks.map((block, index) => {
              const Icon = blockIcon[block.type];
              const selected = block.id === selectedId;
              const issues = blockIssues(block);
              const name = t(blockKey[block.type]);
              const summary = summaryOf(block).trim();
              return (
                <li
                  key={block.id}
                  data-block-type={block.type}
                  className={cn(
                    'rounded-md border bg-background',
                    selected && 'border-primary ring-1 ring-primary/40'
                  )}
                >
                  <div className="flex items-center gap-1 p-1.5">
                    <button
                      type="button"
                      className="flex min-w-0 flex-1 items-center gap-2 rounded px-1.5 py-1 text-left text-sm hover:bg-muted"
                      aria-expanded={selected}
                      aria-label={[
                        summary ? `${name}: ${summary}` : name,
                        issues.length > 0 ? t('templates.visual.hasIssues') : '',
                      ]
                        .filter(Boolean)
                        .join('. ')}
                      onClick={() => setSelectedId(selected ? null : block.id)}
                    >
                      <Icon className="size-4 shrink-0 text-muted-foreground" aria-hidden="true" />
                      <span className="shrink-0 font-medium">{name}</span>
                      <span className="truncate text-muted-foreground">{summaryOf(block)}</span>
                      {issues.length > 0 ? (
                        <AlertCircle
                          className="ml-auto size-4 shrink-0 text-warning"
                          aria-label={t('templates.visual.hasIssues')}
                        />
                      ) : null}
                    </button>
                    <IconButton
                      label={t('templates.visual.moveUp', { block: name })}
                      icon={ChevronUp}
                      disabled={disabled || index === 0}
                      onClick={() => move(index, -1)}
                    />
                    <IconButton
                      label={t('templates.visual.moveDown', { block: name })}
                      icon={ChevronDown}
                      disabled={disabled || index === design.blocks.length - 1}
                      onClick={() => move(index, 1)}
                    />
                    <IconButton
                      label={t('templates.visual.duplicate', { block: name })}
                      icon={Copy}
                      disabled={disabled}
                      onClick={() => duplicate(index)}
                    />
                    <IconButton
                      label={t('templates.visual.remove', { block: name })}
                      icon={Trash2}
                      disabled={disabled}
                      onClick={() => remove(index)}
                    />
                  </div>
                  {selected ? (
                    <div className="space-y-3 border-t p-3">
                      <BlockFields
                        block={block}
                        fields={fields}
                        disabled={disabled}
                        onChange={replace}
                      />
                      {issues.map(issue => (
                        <p key={issue} className="flex items-start gap-1.5 text-xs text-warning">
                          <AlertCircle className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
                          {t(issueKey[issue])}
                        </p>
                      ))}
                    </div>
                  ) : null}
                </li>
              );
            })}
          </ol>
        )}
        <div
          role="group"
          aria-label={t('templates.visual.add')}
          className="flex flex-wrap items-center gap-1.5 pt-1"
        >
          <span className="mr-1 flex items-center gap-1 text-sm text-muted-foreground">
            <Plus className="size-4" aria-hidden="true" />
            {t('templates.visual.add')}
          </span>
          {designBlockTypes.map(type => {
            const Icon = blockIcon[type];
            return (
              <Button
                key={type}
                type="button"
                size="xs"
                variant="outline"
                disabled={disabled}
                icon={<Icon className="size-3.5" />}
                onClick={() => insert(type)}
              >
                {t(blockKey[type])}
              </Button>
            );
          })}
        </div>
      </section>
    </div>
  );
}

function IconButton({
  label,
  icon: Icon,
  disabled,
  onClick,
}: {
  label: string;
  icon: LucideIcon;
  disabled: boolean;
  onClick: () => void;
}) {
  return (
    <Button
      type="button"
      size="xs"
      isIcon
      variant="ghost"
      className="shrink-0"
      aria-label={label}
      title={label}
      disabled={disabled}
      onClick={onClick}
    >
      <Icon className="size-4" aria-hidden="true" />
    </Button>
  );
}

function ColorField({
  label,
  value,
  onChange,
}: {
  label: string;
  value: string;
  onChange: (value: string) => void;
}) {
  const id = useId();
  return (
    <div className="space-y-1">
      <Label htmlFor={id} className="text-xs">
        {label}
      </Label>
      <div className="flex items-center gap-2">
        <input
          id={id}
          type="color"
          value={value}
          className="h-8 w-10 cursor-pointer rounded border bg-transparent p-0.5"
          onChange={event => onChange(event.target.value)}
        />
        <span className="font-mono text-xs text-muted-foreground">{value}</span>
      </div>
    </div>
  );
}

function Field({
  label,
  children,
  hint,
}: {
  label: string;
  children: (id: string) => ReactNode;
  hint?: ReactNode;
}) {
  const id = useId();
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children(id)}
      {hint ? <p className="text-xs text-muted-foreground">{hint}</p> : null}
    </div>
  );
}

function SelectField<T extends string | number>({
  label,
  value,
  options,
  disabled,
  onChange,
}: {
  label: string;
  value: T;
  options: readonly { value: T; label: string }[];
  disabled: boolean;
  onChange: (value: T) => void;
}) {
  return (
    <Field label={label}>
      {id => (
        <Select
          value={String(value)}
          disabled={disabled}
          onValueChange={next => {
            const picked = options.find(option => String(option.value) === next);
            if (picked) onChange(picked.value);
          }}
        >
          <SelectTrigger id={id} className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {options.map(option => (
              <SelectItem key={String(option.value)} value={String(option.value)}>
                {option.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      )}
    </Field>
  );
}

function AlignField({
  value,
  disabled,
  onChange,
}: {
  value: BlockAlign;
  disabled: boolean;
  onChange: (value: BlockAlign) => void;
}) {
  const t = useT('mailbox');
  return (
    <SelectField
      label={t('templates.visual.align')}
      value={value}
      disabled={disabled}
      options={blockAligns.map(align => ({ value: align, label: t(alignKey[align]) }))}
      onChange={onChange}
    />
  );
}

/** Wraps the selection in `before` and `after`, or inserts `fill` between them when nothing is selected. */
function wrapSelection(
  element: HTMLTextAreaElement | HTMLInputElement | null,
  value: string,
  before: string,
  after: string,
  fill: string
): { value: string; caret: number } {
  const start = element?.selectionStart ?? value.length;
  const end = element?.selectionEnd ?? value.length;
  const selected = value.slice(start, end) || fill;
  const next = value.slice(0, start) + before + selected + after + value.slice(end);
  return { value: next, caret: start + before.length + selected.length + after.length };
}

/**
 * A text area with buttons for bold, italic, links, and fields. Markup is plain text that the
 * renderer turns into HTML, so the text stays readable and safe.
 */
function MarkupTextarea({
  id,
  value,
  rows,
  fields,
  disabled,
  markup,
  onChange,
}: {
  id: string;
  value: string;
  rows: number;
  fields: readonly string[];
  disabled: boolean;
  markup: boolean;
  onChange: (value: string) => void;
}) {
  const t = useT('mailbox');
  const ref = useRef<HTMLTextAreaElement>(null);

  const apply = (before: string, after: string, fill: string) => {
    const next = wrapSelection(ref.current, value, before, after, fill);
    onChange(next.value);
    requestAnimationFrame(() => {
      ref.current?.focus();
      ref.current?.setSelectionRange(next.caret, next.caret);
    });
  };

  return (
    <div className="space-y-1.5">
      <div className="flex flex-wrap items-center gap-1">
        {markup ? (
          <>
            <IconButton
              label={t('templates.visual.bold')}
              icon={Bold}
              disabled={disabled}
              onClick={() => apply('**', '**', t('templates.visual.boldFill'))}
            />
            <IconButton
              label={t('templates.visual.italic')}
              icon={Italic}
              disabled={disabled}
              onClick={() => apply('*', '*', t('templates.visual.italicFill'))}
            />
          </>
        ) : null}
        <IconButton
          label={t('templates.visual.link')}
          icon={LinkIcon}
          disabled={disabled}
          onClick={() => apply('[', '](https://)', t('templates.visual.linkFill'))}
        />
        <FieldPicker
          fields={fields}
          disabled={disabled}
          onPick={name => apply(`{{${name}}}`, '', '')}
        />
      </div>
      <Textarea
        ref={ref}
        id={id}
        value={value}
        rows={rows}
        disabled={disabled}
        onChange={event => onChange(event.target.value)}
      />
    </div>
  );
}

function FieldPicker({
  fields,
  disabled,
  onPick,
}: {
  fields: readonly string[];
  disabled: boolean;
  onPick: (name: string) => void;
}) {
  const t = useT('mailbox');
  return (
    <Select
      value=""
      disabled={disabled}
      onValueChange={value => {
        if (value) onPick(value);
      }}
    >
      <SelectTrigger
        size="sm"
        aria-label={t('templates.visual.insertField')}
        className="h-7 text-xs"
      >
        <SelectValue placeholder={t('templates.visual.insertField')} />
      </SelectTrigger>
      <SelectContent>
        {fields.map(name => (
          <SelectItem key={name} value={name}>
            {name}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

/** An input with a field picker that inserts a placeholder at the cursor. */
function FieldInput({
  id,
  value,
  fields,
  disabled,
  placeholder,
  onChange,
}: {
  id: string;
  value: string;
  fields: readonly string[];
  disabled: boolean;
  placeholder?: string;
  onChange: (value: string) => void;
}) {
  const ref = useRef<HTMLInputElement>(null);
  return (
    <div className="flex gap-2">
      <Input
        ref={ref}
        id={id}
        value={value}
        spellCheck={false}
        placeholder={placeholder}
        disabled={disabled}
        className="min-w-0 flex-1"
        onChange={event => onChange(event.target.value)}
      />
      <FieldPicker
        fields={fields}
        disabled={disabled}
        onPick={name => onChange(wrapSelection(ref.current, value, `{{${name}}}`, '', '').value)}
      />
    </div>
  );
}

function BlockFields({
  block,
  fields,
  disabled,
  onChange,
}: {
  block: DesignBlock;
  fields: readonly string[];
  disabled: boolean;
  onChange: (block: DesignBlock) => void;
}) {
  const t = useT('mailbox');
  switch (block.type) {
    case 'heading':
      return (
        <>
          <Field label={t('templates.visual.headingText')}>
            {id => (
              <MarkupTextarea
                id={id}
                value={block.text}
                rows={2}
                fields={fields}
                disabled={disabled}
                markup={false}
                onChange={text => onChange({ ...block, text })}
              />
            )}
          </Field>
          <div className="grid grid-cols-2 gap-3">
            <SelectField
              label={t('templates.visual.size')}
              value={block.size}
              disabled={disabled}
              options={[
                { value: 'large', label: t('templates.visual.sizeLarge') },
                { value: 'medium', label: t('templates.visual.sizeMedium') },
              ]}
              onChange={size => onChange({ ...block, size })}
            />
            <AlignField
              value={block.align}
              disabled={disabled}
              onChange={align => onChange({ ...block, align })}
            />
          </div>
        </>
      );
    case 'text':
    case 'footer':
      return (
        <>
          <Field
            label={t(
              block.type === 'text' ? 'templates.visual.bodyText' : 'templates.visual.footerText'
            )}
            hint={t('templates.visual.markupHint')}
          >
            {id => (
              <MarkupTextarea
                id={id}
                value={block.text}
                rows={block.type === 'text' ? 6 : 3}
                fields={fields}
                disabled={disabled}
                markup
                onChange={text => onChange({ ...block, text })}
              />
            )}
          </Field>
          <AlignField
            value={block.align}
            disabled={disabled}
            onChange={align => onChange({ ...block, align })}
          />
        </>
      );
    case 'button':
      return (
        <>
          <Field label={t('templates.visual.buttonLabel')}>
            {id => (
              <Input
                id={id}
                value={block.label}
                disabled={disabled}
                onChange={event => onChange({ ...block, label: event.target.value })}
              />
            )}
          </Field>
          <Field
            label={t('templates.visual.buttonUrl')}
            hint={
              <>
                {t('templates.visual.urlHint')}{' '}
                <code className="font-mono">{fieldLinkExample}</code>
              </>
            }
          >
            {id => (
              <FieldInput
                id={id}
                value={block.url}
                fields={fields}
                disabled={disabled}
                onChange={url => onChange({ ...block, url })}
              />
            )}
          </Field>
          <AlignField
            value={block.align}
            disabled={disabled}
            onChange={align => onChange({ ...block, align })}
          />
        </>
      );
    case 'image':
      return (
        <>
          <Field
            label={t('templates.visual.imageSource')}
            hint={t('templates.visual.imageSourceHint')}
          >
            {id => (
              <FieldInput
                id={id}
                value={block.src}
                fields={fields}
                disabled={disabled}
                onChange={src => onChange({ ...block, src })}
              />
            )}
          </Field>
          <Field label={t('templates.visual.imageAlt')}>
            {id => (
              <Input
                id={id}
                value={block.alt}
                disabled={disabled}
                onChange={event => onChange({ ...block, alt: event.target.value })}
              />
            )}
          </Field>
          <Field label={t('templates.visual.imageLink')}>
            {id => (
              <FieldInput
                id={id}
                value={block.href}
                fields={fields}
                disabled={disabled}
                onChange={href => onChange({ ...block, href })}
              />
            )}
          </Field>
          <div className="grid grid-cols-2 gap-3">
            <SelectField
              label={t('templates.visual.imageWidth')}
              value={block.width}
              disabled={disabled}
              options={imageWidths.map(width => ({ value: width, label: `${width}%` }))}
              onChange={width => onChange({ ...block, width })}
            />
            <AlignField
              value={block.align}
              disabled={disabled}
              onChange={align => onChange({ ...block, align })}
            />
          </div>
        </>
      );
    case 'spacer':
      return (
        <SelectField
          label={t('templates.visual.spacerHeight')}
          value={block.height}
          disabled={disabled}
          options={spacerHeights.map(height => ({ value: height, label: `${height}px` }))}
          onChange={height => onChange({ ...block, height })}
        />
      );
    case 'divider':
      return <p className="text-sm text-muted-foreground">{t('templates.visual.dividerHint')}</p>;
  }
}
