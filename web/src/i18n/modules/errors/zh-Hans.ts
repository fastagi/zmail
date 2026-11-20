// Errors translations - Simplified Chinese
import type { LocaleMessageShape } from '../../locale-message-shape';
import type { ErrorsMessages } from './en-US';

const messages = {
  notFound: '页面未找到',
  serverError: '服务器错误',
  networkError: '网络错误，请检查网络连接',
  rateLimited: '操作过于频繁，请稍后重试',
  unauthorized: '请登录后继续',
  validationFailed: '请检查标出的字段',
  forbidden: '您没有权限访问此资源',
  authForbiddenDescription: '当前会话无法访问受保护应用。如非预期，请联系管理员。',
  authUnavailable: '暂时无法验证登录状态',
  authUnavailableDescription: '您的登录状态未被更改。请检查网络连接后重试。',
  transactional: {
    disabled: '实例未启用事务邮件。',
    rateLimited: '试发太频繁。',
    recipientRateLimited: '这个模板发往同一地址太频繁。',
    retryIn: '请稍后再试（{seconds} 秒）。',
    retryLater: '请稍后再试。',
    messageNotFound: '这封邮件已不存在。发送日志只保留 30 天。',
    idempotencyConflict: '这个幂等键已用于不同的内容。',
    idempotencyKeyInvalid: '幂等键缺失或格式不正确。',
  },
} as const satisfies LocaleMessageShape<ErrorsMessages>;

export default messages;
