// Zmail mail edge: a Cloudflare Email Worker that hands every message Email Routing accepts
// to the Zmail API as a signed delivery, and forwards the provider's delivery events for sent
// mail the same way. It keeps no state and makes no routing decision; the API owns mailboxes
// and answers whether a recipient exists.
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
      throw new Error(`Zmail API answered HTTP ${response.status}`);
  }
}

// The value signed in place of a recipient for delivery events, so a signed inbound message
// can never be replayed as an event.
export const DELIVERY_EVENT_SCOPE = 'delivery-event';

/** The API route for delivery events, next to the inbound route unless set explicitly. */
export function deliveryEventsUrl(env) {
  if (env.MAILBOX_EVENTS_URL) {
    return env.MAILBOX_EVENTS_URL;
  }
  return env.MAILBOX_INBOUND_URL.replace(/\/inbound-mail-messages\/?$/, '/mail-delivery-events');
}

/**
 * Forwards one Cloudflare Email Sending event to the Zmail API. Returns 'ack' when the event is
 * finished with, including events the API cannot use or that name a message Zmail did not send,
 * and 'retry' when the API should see it again.
 */
export async function forwardEvent(event, env, fetcher = fetch, now = Date.now) {
  if (!env.MAILBOX_INBOUND_URL || !env.MAILBOX_INBOUND_SECRET) {
    throw new Error('MAILBOX_INBOUND_URL and MAILBOX_INBOUND_SECRET must be configured');
  }
  const body = encoder.encode(JSON.stringify(event));
  const timestamp = Math.floor(now() / 1000);
  const signature = await signDelivery(
    env.MAILBOX_INBOUND_SECRET,
    timestamp,
    DELIVERY_EVENT_SCOPE,
    body,
  );
  let response;
  try {
    response = await fetcher(deliveryEventsUrl(env), {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        'X-Mailbox-Signature': `t=${timestamp},v1=${signature}`,
      },
      body,
    });
  } catch {
    return 'retry';
  }
  if (response.ok) {
    return 'ack';
  }
  // 404: the message was not sent through Zmail. 413 and 422: the API will never accept it.
  return [404, 413, 422].includes(response.status) ? 'ack' : 'retry';
}

export default {
  async email(message, env) {
    await deliver(message, env);
  },

  // Consumes the queue that a Cloudflare Email Sending event subscription writes to.
  async queue(batch, env) {
    for (const message of batch.messages) {
      const outcome = await forwardEvent(message.body, env);
      if (outcome === 'ack') {
        message.ack();
      } else {
        message.retry();
      }
    }
  },
};
