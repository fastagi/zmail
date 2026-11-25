import {
  buildDebugSummary,
  formatAuditTitle,
  formatHttpStatus,
  formatRelativeTime,
  humanizeToken,
  isAuditError,
  isAuditServerError,
  methodBadgeClass,
} from '@/features/audit/utils/format-audit';

describe('format-audit utilities', () => {
  it('translates known actions and resources into zh-Hans and en-US titles', () => {
    expect(
      formatAuditTitle(
        { action: 'login', resource: 'auth', method: 'POST', path: '/v1/auth/login' },
        'zh-Hans',
      ),
    ).toBe('用户登录');
    expect(
      formatAuditTitle(
        { action: 'login', resource: 'auth', method: 'POST', path: '/v1/auth/login' },
        'en-US',
      ),
    ).toBe('User login');
    expect(
      formatAuditTitle(
        {
          action: 'grant',
          resource: 'platform_operators',
          method: 'CLI',
          path: 'operator:grant',
        },
        'zh-Hans',
      ),
    ).toBe('授予平台管理员权限');
    expect(
      formatAuditTitle(
        {
          action: 'create',
          resource: 'external-mail',
          method: 'POST',
          path: '/v1/external-mail/accounts',
        },
        'zh-Hans',
      ),
    ).toBe('外部邮箱同步配置');
    expect(
      formatAuditTitle(
        {
          action: 'send',
          resource: 'emails',
          method: 'POST',
          path: '/v1/transactional/emails',
        },
        'zh-Hans',
      ),
    ).toBe('发送事务邮件');
  });

  it('humanizes unknown snake_case and kebab-case tokens cleanly', () => {
    expect(humanizeToken('custom_webhook_rule')).toBe('Custom webhook rule');
    expect(
      formatAuditTitle(
        {
          action: 'rotate_secret',
          resource: 'billing-plans',
          method: 'POST',
          path: '/v1/billing-plans/1/rotate',
        },
        'en-US',
      ),
    ).toBe('Rotate secret · Billing plans');
  });

  it('formats HTTP status codes with localized meaning and tone', () => {
    expect(formatHttpStatus(200, 'zh-Hans')).toEqual({ label: '200', tone: 'success' });
    expect(formatHttpStatus(401, 'zh-Hans')).toEqual({ label: '401 未授权', tone: 'warning' });
    expect(formatHttpStatus(413, 'zh-Hans')).toEqual({ label: '413 请求过大', tone: 'warning' });
    expect(formatHttpStatus(429, 'en-US')).toEqual({ label: '429 Rate Limited', tone: 'warning' });
    expect(formatHttpStatus(500, 'zh-Hans')).toEqual({ label: '500 异常', tone: 'danger' });
  });

  it('identifies errors and server errors accurately', () => {
    expect(isAuditError({ status_code: 200, result: 'success' })).toBe(false);
    expect(isAuditError({ status_code: 200, result: 'failure' })).toBe(true);
    expect(isAuditError({ status_code: 403 })).toBe(true);
    expect(isAuditServerError({ status_code: 429 })).toBe(false);
    expect(isAuditServerError({ status_code: 502 })).toBe(true);
  });

  it('formats relative time in zh-Hans and en-US', () => {
    const now = new Date('2026-11-25T15:00:00Z');
    expect(formatRelativeTime('2026-11-25T14:59:45Z', 'zh-Hans', now)).toBe('刚刚');
    expect(formatRelativeTime('2026-11-25T14:55:00Z', 'zh-Hans', now)).toBe('5 分钟前');
    expect(formatRelativeTime('2026-11-25T13:00:00Z', 'en-US', now)).toBe('2h ago');
  });

  it('returns distinct badge classes per HTTP method', () => {
    expect(methodBadgeClass('POST')).toContain('blue');
    expect(methodBadgeClass('PATCH')).toContain('amber');
    expect(methodBadgeClass('DELETE')).toContain('red');
    expect(methodBadgeClass('CLI')).toContain('purple');
    expect(methodBadgeClass('GET')).toContain('muted');
  });

  it('builds a clean multi-line debug summary', () => {
    const summary = buildDebugSummary({
      entry: {
        id: 10,
        user_id: 7,
        actor_type: 'user',
        action: 'user.disabled',
        resource: 'users',
        target_type: 'user',
        target_id: '1042',
        result: 'failure',
        method: 'POST',
        path: '/v1/operator/users/1042/disable',
        status_code: 500,
        request_id: 'req-99',
        created_at: '2026-09-30T04:40:00Z',
      },
      title: 'Disable user account',
      timestamp: 'Sep 30, 2026, 4:40:00 AM',
      actor: 'User #7',
      statusLabel: '500 Server Error',
    });

    expect(summary).toContain('Title: Disable user account');
    expect(summary).toContain('Status: 500 Server Error');
    expect(summary).toContain('Request: POST /v1/operator/users/1042/disable');
    expect(summary).toContain('Actor: User #7');
    expect(summary).toContain('Action / Resource: user.disabled · users');
    expect(summary).toContain('Target: user · 1042');
    expect(summary).toContain('Result: failure');
    expect(summary).toContain('Request ID: req-99');
  });
});
