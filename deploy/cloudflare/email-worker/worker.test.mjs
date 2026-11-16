// Run with: node --test deploy/cloudflare/email-worker/
import assert from 'node:assert/strict';
import { test } from 'node:test';

import { MAX_RAW_BYTES, deliver, signDelivery } from './worker.js';

const secret = '0123456789abcdef0123456789abcdef';
const raw = new TextEncoder().encode('From: a@b.c\r\n\r\nbody');

function message(overrides = {}) {
  const rejections = [];
  return {
    to: 'Hello@Mail.Example',
    rawSize: raw.length,
    raw: new Blob([raw]).stream(),
    setReject: reason => rejections.push(reason),
    rejections,
    ...overrides,
  };
}

const env = {
  MAILBOX_INBOUND_URL: 'https://api.example/v1/inbound-mail-messages',
  MAILBOX_INBOUND_SECRET: secret,
};

test('signature matches the vector pinned in the API test suite', async () => {
  // The same vector is asserted by TestSignInboundMatchesTheEdgeVector in the Go mailbox module.
  assert.equal(
    await signDelivery(secret, 1800000000, 'hello@mail.example', raw),
    'e7c3e27b6f2a8c68f085c52ef15055793f8bba92e6845f9201c98e3ca3e4ef1c',
  );
  assert.equal(
    await signDelivery(secret, 1800000000, 'HELLO@mail.example', raw),
    await signDelivery(secret, 1800000000, 'hello@mail.example', raw),
    'the recipient is signed in lowercase',
  );
});

test('delivers the raw message with a signed, lowercased recipient', async () => {
  const calls = [];
  const fetcher = async (url, init) => {
    calls.push({ url, init });
    return new Response('{}', { status: 201 });
  };
  const mail = message();

  assert.equal(await deliver(mail, env, fetcher, () => 1800000000_000), 'delivered');
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, env.MAILBOX_INBOUND_URL);
  assert.equal(calls[0].init.method, 'POST');
  assert.equal(calls[0].init.headers['X-Mailbox-Recipient'], 'hello@mail.example');
  assert.equal(
    calls[0].init.headers['X-Mailbox-Signature'],
    't=1800000000,v1=e7c3e27b6f2a8c68f085c52ef15055793f8bba92e6845f9201c98e3ca3e4ef1c',
  );
  assert.deepEqual(calls[0].init.body, raw);
  assert.deepEqual(mail.rejections, []);
});

test('a repeated delivery the API already stored still counts as delivered', async () => {
  const fetcher = async () => new Response('{}', { status: 200 });
  assert.equal(await deliver(message(), env, fetcher), 'delivered');
});

for (const [status, reason] of [
  [404, 'Mailbox does not exist'],
  [413, 'Message too large'],
  [422, 'Message could not be processed'],
]) {
  test(`HTTP ${status} from the API bounces the message`, async () => {
    const mail = message();
    assert.equal(await deliver(mail, env, async () => new Response('{}', { status })), 'rejected');
    assert.deepEqual(mail.rejections, [reason]);
  });
}

for (const status of [401, 429, 500, 503]) {
  test(`HTTP ${status} from the API fails the delivery so the sender retries`, async () => {
    const mail = message();
    await assert.rejects(deliver(mail, env, async () => new Response('{}', { status })), /HTTP/);
    assert.deepEqual(mail.rejections, [], 'a temporary failure must not bounce the message');
  });
}

test('an oversized message is refused without contacting the API', async () => {
  let called = false;
  const mail = message({ rawSize: MAX_RAW_BYTES + 1 });
  assert.equal(await deliver(mail, env, async () => { called = true; }), 'rejected');
  assert.equal(called, false);
  assert.deepEqual(mail.rejections, ['Message too large']);
});

test('a network failure propagates so the sender retries', async () => {
  await assert.rejects(deliver(message(), env, async () => { throw new Error('offline'); }), /offline/);
});

test('missing configuration fails loudly instead of dropping mail', async () => {
  await assert.rejects(deliver(message(), {}, async () => new Response('{}')), /must be configured/);
});
