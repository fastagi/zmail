import type { AuditEntry } from '@/features/audit/types';

export type AuditStatusTone = 'success' | 'warning' | 'danger' | 'neutral';

const isZh = (locale: string) => locale.toLowerCase().startsWith('zh');

/** Converts snake_case, kebab-case, or dot.case identifiers into readable words. */
export function humanizeToken(raw: string): string {
  const cleaned = raw.trim().replace(/[._/-]+/g, ' ').replace(/\s+/g, ' ');
  return cleaned ? cleaned[0]!.toUpperCase() + cleaned.slice(1) : '';
}

// Tuple: [regex tested against `${action} ${resource} ${path}`, zh-Hans title, en-US title]
const ACTION_RULES: readonly (readonly [RegExp, string, string])[] = [
  [/auth\/login|\blogin\b/, '用户登录', 'User login'],
  [/auth\/logout|\blogout\b/, '退出登录', 'User logout'],
  [/auth\/register|\bregister\b/, '用户注册', 'User registration'],
  [/auth\/refresh|\brefresh\b/, '刷新登录会话', 'Refresh auth session'],
  [/password/, '修改账号密码', 'Change password'],
  [/grant.*operator|operator.*grant/, '授予平台管理员权限', 'Grant platform operator access'],
  [/revoke.*operator|operator.*revoke/, '撤销平台管理员权限', 'Revoke platform operator access'],
  [/disable.*user|user.*disable/, '停用用户账号', 'Disable user account'],
  [/enable.*user|user.*enable/, '启用用户账号', 'Enable user account'],
  [/invit/, '邀请组织成员', 'Invite organization member'],
  [/suspend.*org|org.*suspend/, '暂停组织服务', 'Suspend organization'],
  [/resume.*org|org.*resume/, '恢复组织服务', 'Resume organization'],
  [/quota.*org|org.*quota/, '调整组织发送配额', 'Update organization quota'],
  [/external[-_]mail/, '外部邮箱同步配置', 'External mail sync configuration'],
  [/mailbox/, '邮箱配置变更', 'Mailbox configuration'],
  [/broadcast/, '邮件群发', 'Email broadcast'],
  [/transactional|\bemails?\b|email\.send/, '发送事务邮件', 'Send transactional email'],
  [/domain/, '域名与 DNS 配置', 'Domain & DNS configuration'],
  [/api[-_]key/, 'API 密钥管理', 'API key management'],
  [/contact/, '联系人管理', 'Contacts management'],
  [/replay.*webhook|webhook.*replay/, '重放 Webhook 消息', 'Replay webhook delivery'],
  [/webhook/, 'Webhook 配置变更', 'Webhook configuration'],
  [/template/, '邮件模板管理', 'Email template management'],
  [/suppression/, '抑制名单变更', 'Suppression list update'],
  [/setting/, '应用设置变更', 'App setting update'],
  [/organization/, '组织配置变更', 'Organization update'],
  [/\busers?\b/, '用户账号变更', 'User account update'],
];

export function formatAuditTitle(
  entry: Pick<AuditEntry, 'action' | 'resource' | 'method' | 'path'>,
  locale: string,
): string {
  const haystack = `${entry.action} ${entry.resource} ${entry.path}`.toLowerCase();
  const zh = isZh(locale);
  for (const [pattern, zhTitle, enTitle] of ACTION_RULES) {
    if (pattern.test(haystack)) {
      return zh ? zhTitle : enTitle;
    }
  }
  const action = humanizeToken(entry.action);
  const resource = humanizeToken(entry.resource);
  if (action && resource && action.toLowerCase() !== resource.toLowerCase()) {
    return `${action} · ${resource}`;
  }
  return action || resource || entry.action;
}

const HTTP_STATUS_MEANINGS: Record<number, readonly [string, string]> = {
  400: ['参数错误', 'Bad Request'],
  401: ['未授权', 'Unauthorized'],
  403: ['无权限', 'Forbidden'],
  404: ['未找到', 'Not Found'],
  409: ['冲突', 'Conflict'],
  413: ['请求过大', 'Payload Too Large'],
  422: ['校验失败', 'Unprocessable'],
  429: ['限流', 'Rate Limited'],
  500: ['异常', 'Server Error'],
  502: ['网关错误', 'Bad Gateway'],
  503: ['服务不可用', 'Unavailable'],
  504: ['网关超时', 'Timeout'],
};

export function formatHttpStatus(
  statusCode: number,
  locale: string,
): { label: string; tone: AuditStatusTone } {
  const tone: AuditStatusTone =
    statusCode >= 500
      ? 'danger'
      : statusCode >= 400
        ? 'warning'
        : statusCode >= 200 && statusCode < 300
          ? 'success'
          : 'neutral';
  const zh = isZh(locale);
  const meaning = HTTP_STATUS_MEANINGS[statusCode];
  if (meaning) {
    return { label: `${statusCode} ${zh ? meaning[0] : meaning[1]}`, tone };
  }
  if (statusCode >= 500) {
    return { label: `${statusCode} ${zh ? '服务异常' : 'Server Error'}`, tone };
  }
  if (statusCode >= 400) {
    return { label: `${statusCode} ${zh ? '请求异常' : 'Client Error'}`, tone };
  }
  return { label: String(statusCode), tone };
}

export function isAuditServerError(entry: Pick<AuditEntry, 'status_code'>): boolean {
  return entry.status_code >= 500;
}

export function isAuditError(entry: Pick<AuditEntry, 'status_code' | 'result'>): boolean {
  return entry.status_code >= 400 || entry.result === 'failure' || entry.result === 'error';
}

const METHOD_CLASSES: Record<string, string> = {
  POST: 'border-blue-500/25 bg-blue-500/10 text-blue-700 dark:text-blue-300',
  PUT: 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300',
  PATCH: 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300',
  DELETE: 'border-red-500/25 bg-red-500/10 text-red-700 dark:text-red-300',
  CLI: 'border-purple-500/25 bg-purple-500/10 text-purple-700 dark:text-purple-300',
};

export function methodBadgeClass(method: string): string {
  return METHOD_CLASSES[method.toUpperCase()] ?? 'border-border bg-muted text-muted-foreground';
}

export function formatRelativeTime(
  isoString: string,
  locale: string,
  now: Date = new Date(),
): string {
  const date = new Date(isoString);
  if (Number.isNaN(date.getTime())) return isoString;
  const diff = Math.floor((now.getTime() - date.getTime()) / 1000);
  const zh = isZh(locale);
  if (diff < 60 && diff >= -30) return zh ? '刚刚' : 'Just now';
  if (diff < 0) return date.toISOString();
  const units: readonly [number, string, string][] = [
    [31536000, '年前', 'y ago'],
    [2592000, '个月前', 'mo ago'],
    [86400, '天前', 'd ago'],
    [3600, '小时前', 'h ago'],
    [60, '分钟前', 'm ago'],
  ];
  for (const [secs, zhUnit, enUnit] of units) {
    if (diff >= secs) {
      const value = Math.floor(diff / secs);
      return zh ? `${value} ${zhUnit}` : `${value}${enUnit}`;
    }
  }
  return zh ? '刚刚' : 'Just now';
}

export function buildDebugSummary({
  entry,
  title,
  timestamp,
  actor,
  statusLabel,
}: {
  entry: AuditEntry;
  title: string;
  timestamp: string;
  actor: string;
  statusLabel: string;
}): string {
  const target = [entry.target_type, entry.target_id].filter(Boolean).join(' · ') || '-';
  return `Title: ${title}\nTime: ${timestamp} (${entry.created_at})\nStatus: ${statusLabel}\nRequest: ${entry.method} ${entry.path}\nActor: ${actor}\nAction / Resource: ${entry.action} · ${entry.resource}\nTarget: ${target}\nResult: ${entry.result || '-'}\nRequest ID: ${entry.request_id || '-'}`;
}
