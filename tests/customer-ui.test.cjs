const { test } = require('node:test');
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { runInNewContext } = require('node:vm');
const { webcrypto } = require('node:crypto');

class Element {
  constructor() {
    this.hidden = false;
    this.disabled = false;
    this.textContent = '';
    this.children = [];
    this.listeners = {};
    this.attributes = {};
    this.classes = new Set();
    this.classList = { add: (v) => this.classes.add(v), remove: (v) => this.classes.delete(v) };
  }
  addEventListener(name, fn) { this.listeners[name] = fn; }
  setAttribute(name, value) { this.attributes[name] = value; }
  removeAttribute(name) { delete this.attributes[name]; }
  replaceChildren() { this.children = []; }
  append(child) { this.children.push(child); }
  focus() {}
  reset() {}
  click() { return this.listeners.click?.(); }
}

function setup({ items = [], points = 0, redeem } = {}) {
  const nodes = new Map();
  const get = (id) => {
    if (!nodes.has(id)) nodes.set(id, new Element());
    return nodes.get(id);
  };
  const form = get('register-form');
  form.action = 'https://example.com/instance/api/v1/register';
  form.elements = { name: { value: 'Test Member' }, email: { value: 'test@example.com' } };
  get('coupon-template').content = {
    cloneNode() {
      const fields = new Map();
      return { querySelector(selector) {
        if (!fields.has(selector)) fields.set(selector, new Element());
        return fields.get(selector);
      } };
    },
  };
  const requests = [];
  const state = { items, points, redeem, pointsFail: false, couponsFail: false };
  const response = (status, body) => ({ ok: status >= 200 && status < 300, status, json: async () => body });
  const fetch = async (input, options) => {
    const url = new URL(input);
    requests.push({ url, options });
    if (url.pathname.endsWith('/register')) {
      return response(201, { customer: { id: 'member-1', name: 'Test Member', email: 'test@example.com', points: 0 }, access_token: 'private-token' });
    }
    assert.equal(options.headers.Authorization, 'Bearer private-token');
    assert.equal(options.credentials, 'omit');
    assert.ok(url.pathname.startsWith('/instance/api/v1/'));
    if (url.pathname.endsWith('/points')) return response(state.pointsFail ? 503 : 200, { points: state.points });
    if (url.pathname.endsWith('/privileges')) {
      const page = Number(url.searchParams.get('page'));
      return response(state.couponsFail ? 503 : 200, state.items.slice((page - 1) * 100, page * 100));
    }
    if (url.pathname.endsWith('/redemptions')) return state.redeem(options, state, response);
    throw new Error(`Unexpected request: ${url}`);
  };
  runInNewContext(readFileSync(require.resolve('../internal/transport/register.js'), 'utf8'), {
    document: { getElementById: get, querySelector: get, title: '' },
    window: { location: { href: 'https://example.com/instance/public/register' } },
    fetch, URL, TextEncoder, Uint8Array, crypto: webcrypto,
  });
  return {
    get, state, requests,
    register: () => form.listeners.submit({ preventDefault() {} }),
    cards: () => get('coupon-list').children,
  };
}

const coupon = (id = 'coffee', changes = {}) => ({ id, name: 'Coffee <special>', description: 'One coffee', cost: 50, stock: 2, ...changes });

test('signup displays real coupons, disables unaffordable and sold-out rewards, and refreshes points', async () => {
  const ui = setup({ items: [coupon(), coupon('sold-out', { stock: 0 })] });
  await ui.register();
  assert.equal(ui.get('registration').hidden, true);
  assert.equal(ui.get('confirmation').hidden, false);
  assert.equal(ui.cards().length, 2);
  assert.equal(ui.cards()[0].querySelector('.coupon-name').textContent, 'Coffee <special>');
  assert.equal(ui.cards()[0].querySelector('.redeem-button').disabled, true);
  assert.match(ui.cards()[0].querySelector('.coupon-help').textContent, /50 more points/);
  assert.equal(ui.cards()[1].querySelector('.redeem-button').textContent, 'Sold out');
  ui.state.points = 100;
  await ui.get('refresh-rewards').click();
  assert.equal(ui.get('member-points').textContent, '100');
  assert.equal(ui.cards()[0].querySelector('.redeem-button').disabled, false);
  assert.equal(ui.cards()[1].querySelector('.redeem-button').disabled, true);
});

test('redeeming refreshes balance and stock and shows the server receipt', async () => {
  const ui = setup({ points: 100, items: [coupon('coffee', { stock: 1 })], redeem(options, state, response) {
    assert.equal(JSON.parse(options.body).privilege_id, 'coffee');
    assert.ok(options.headers['Idempotency-Key']);
    state.points = 50;
    state.items[0].stock = 0;
    return response(200, { id: 'receipt-1', name: 'Coffee', cost: 50 });
  } });
  await ui.register();
  await ui.cards()[0].querySelector('.redeem-button').click();
  assert.equal(ui.get('member-points').textContent, '50');
  assert.equal(ui.cards()[0].querySelector('.redeem-button').textContent, 'Sold out');
  assert.match(ui.get('redemption-status').textContent, /Coffee redeemed!.*receipt-1/);
});

test('an uncertain redemption retains its key across refresh and safely retries even when stock disappears', async () => {
  const keys = [];
  const ui = setup({ points: 50, items: [coupon('coffee', { stock: 1 })], redeem(options, state, response) {
    keys.push(options.headers['Idempotency-Key']);
    if (keys.length === 1) {
      state.points = 0;
      state.items = [];
      throw new Error('Connection lost after commit');
    }
    return response(200, { id: 'original-receipt', name: 'Coffee', cost: 50 });
  } });
  await ui.register();
  await ui.cards()[0].querySelector('.redeem-button').click();
  await ui.get('refresh-rewards').click();
  assert.equal(ui.cards().length, 1);
  assert.equal(ui.cards()[0].querySelector('.redeem-button').disabled, false);
  await ui.cards()[0].querySelector('.redeem-button').click();
  assert.equal(keys.length, 2);
  assert.equal(keys[0], keys[1]);
  assert.equal(ui.get('member-points').textContent, '0');
  assert.match(ui.get('redemption-status').textContent, /original-receipt/);
});

test('empty and failed catalogs are distinct and failed refresh blocks spending until recovery', async () => {
  const ui = setup();
  await ui.register();
  assert.equal(ui.get('rewards-empty').hidden, false);
  ui.state.items = [coupon()];
  ui.state.points = 100;
  await ui.get('refresh-rewards').click();
  ui.state.pointsFail = true;
  await ui.get('refresh-rewards').click();
  assert.equal(ui.get('rewards-error').hidden, false);
  assert.equal(ui.get('rewards-empty').hidden, true);
  assert.equal(ui.cards()[0].querySelector('.redeem-button').disabled, true);
  ui.state.pointsFail = false;
  await ui.get('refresh-rewards').click();
  assert.equal(ui.cards()[0].querySelector('.redeem-button').disabled, false);
});

test('coupon pagination appends further rewards without losing the initial page', async () => {
  const ui = setup({ items: Array.from({ length: 101 }, (_, i) => coupon(String(i))) });
  await ui.register();
  assert.equal(ui.cards().length, 100);
  assert.equal(ui.get('load-more').hidden, false);
  await ui.get('load-more').click();
  assert.equal(ui.cards().length, 101);
  assert.equal(ui.get('load-more').hidden, true);
});

test('a rapid second click cannot start another redemption while the first is in flight', async () => {
  let finish;
  let count = 0;
  const ui = setup({ points: 100, items: [coupon()], redeem(options, state, response) {
    count++;
    return new Promise((resolve) => { finish = () => {
      state.points = 50;
      resolve(response(200, { id: 'one-receipt', name: 'Coffee', cost: 50 }));
    }; });
  } });
  await ui.register();
  const button = ui.cards()[0].querySelector('.redeem-button');
  const first = button.click();
  await button.click();
  assert.equal(count, 1);
  finish();
  await first;
  assert.equal(ui.get('member-points').textContent, '50');
});

test('a rejected redemption refreshes changed stock and points without claiming success', async () => {
  const ui = setup({ points: 100, items: [coupon()], redeem(options, state, response) {
    state.points = 25;
    state.items[0].stock = 0;
    return response(409, { error: 'insufficient points' });
  } });
  await ui.register();
  await ui.cards()[0].querySelector('.redeem-button').click();
  assert.equal(ui.get('member-points').textContent, '25');
  assert.equal(ui.cards()[0].querySelector('.redeem-button').textContent, 'Sold out');
  assert.equal(ui.get('redemption-status').classes.has('error'), true);
  assert.match(ui.get('redemption-status').textContent, /couldn’t be redeemed/);
});
