// FastMail mail edge: a Cloudflare Email Worker that hands every message Email Routing accepts
// to the FastMail API as a signed delivery. It keeps no state and makes no routing decision; the
// API owns mailboxes and answers whether a recipient exists.
//
// Bindings (see wrangler.jsonc and README.md):
//   MAILBOX_INBOUND_URL     variable  https://<api host>/v1/inbound-mail-messages
//   MAILBOX_INBOUND_SECRET  secret    the same value as the API's MAILBOX_INBOUND_SECRET

// The API accepts raw messages up to 5 MiB; larger ones are refused here before any upload.
export const MAX_RAW_BYTES = 5 * 1024 * 1024;

const encoder = new TextEncoder();

/**
 * Signs one delivery: hex HMAC-SHA256 over "<unix seconds>.<lowercase recipient>.<raw message>".
 * The API recomputes it and rejects a timestamp more than five minutes from its clock.
 */
export async function signDelivery(secret, timestamp, recipient, raw) {
  const key = await crypto.subtle.importKey(
    'raw',
    encoder.encode(secret),
    { name: 'HMAC', hash: 'SHA-256' },
    false,
    ['sign'],
  );
  const prefix = encoder.encode(`${timestamp}.${recipient.toLowerCase()}.`);
  const payload = new Uint8Array(prefix.length + raw.length);
  payload.set(prefix, 0);
  payload.set(raw, prefix.length);
  const signature = new Uint8Array(await crypto.subtle.sign('HMAC', key, payload));
  return Array.from(signature, byte => byte.toString(16).padStart(2, '0')).join('');
}

/**
 * Delivers one message. Permanent answers from the API become SMTP rejections, so the sender gets
 * a bounce; anything else throws, which makes Cloudflare report a temporary failure and the
 * sending server retry. A message is never silently dropped.
 */
export async function deliver(message, env, fetcher = fetch, now = Date.now) {
  if (!env.MAILBOX_INBOUND_URL || !env.MAILBOX_INBOUND_SECRET) {
    throw new Error('MAILBOX_INBOUND_URL and MAILBOX_INBOUND_SECRET must be configured');
  }
  if (message.rawSize > MAX_RAW_BYTES) {
    message.setReject('Message too large');
    return 'rejected';
  }

  const recipient = message.to.toLowerCase();
  const raw = new Uint8Array(await new Response(message.raw).arrayBuffer());
  const timestamp = Math.floor(now() / 1000);
  const signature = await signDelivery(env.MAILBOX_INBOUND_SECRET, timestamp, recipient, raw);

  const response = await fetcher(env.MAILBOX_INBOUND_URL, {
    method: 'POST',
    headers: {
      'Content-Type': 'message/rfc822',
      'X-Mailbox-Recipient': recipient,
      'X-Mailbox-Signature': `t=${timestamp},v1=${signature}`,
    },
    body: raw,
  });
  if (response.ok) {
    return 'delivered';
  }
  switch (response.status) {
    case 404:
      message.setReject('Mailbox does not exist');
      return 'rejected';
    case 413:
      message.setReject('Message too large');
      return 'rejected';
    case 422:
      message.setReject('Message could not be processed');
      return 'rejected';
    default:
      throw new Error(`FastMail API answered HTTP ${response.status}`);
  }
}

export default {
  async email(message, env) {
    await deliver(message, env);
  },
};
