const { test } = require('node:test');
const assert = require('node:assert/strict');
const { readFileSync } = require('node:fs');
const { runInNewContext } = require('node:vm');

function setup(initialResult) {
  const ids = new Map();
  class Element {
    constructor(tag) { this.tagName = tag; this.children = []; this.dataset = {}; this.value = ''; this.classList = { toggle() {} }; }
    set id(id) { this._id = id; ids.set(id, this); }
    get id() { return this._id; }
    append(child) { child.parentElement = this; this.children.push(child); }
    replaceChildren() { this.children = []; }
  }
  const get = id => ids.get(id);
  for (const id of ['tabs', 'editor', 'status', 'table', 'page', 'prev', 'next', 'new', 'refresh']) {
    const e = new Element('div'); e.id = id;
  }
  let message;
  const calls = [];
  const records = new Map();
  const parent = { postMessage(request) {
    if (request.id === undefined) return;
    let result = {};
    if (request.method === 'tools/call') {
      const { name, arguments: args } = request.params;
      calls.push({ name, args });
      if (name.startsWith('save_')) {
        const id = args.id || `generated-${records.size + 1}`;
        const record = { ...args, id };
        records.set(id, record);
        result = { structuredContent: { tool: name, arguments: args, data: { saved: id, record } } };
      } else {
        result = { structuredContent: { data: [...records.values()] } };
      }
    }
    queueMicrotask(() => message({ source: parent, data: { jsonrpc: '2.0', id: request.id, result } }));
  } };
  const window = { parent, addEventListener(name, listener) { if (name === 'message') message = listener; }, openai: { toolOutput: initialResult } };
  const html = readFileSync(require.resolve('../internal/transport/admin.html'), 'utf8');
  const script = html.match(/<script>([\s\S]*?)<\/script>/)[1];
  runInNewContext(script, { window, document: { getElementById: get, createElement: tag => new Element(tag) }, setTimeout, clearTimeout });
  return {
    get, calls, records,
    settle: () => new Promise(resolve => setImmediate(resolve)),
    submit: () => get('editor').onsubmit({ preventDefault() {}, submitter: get('editor').children.at(-1) }),
  };
}

test('new coupon hides its ID, then reuses the returned ID for subsequent edits', async () => {
  const ui = setup({ tool: 'open_crm_admin', arguments: { section: 'privileges' } });
  await ui.settle();
  assert.equal(ui.get('field-id').parentElement.hidden, true);
  assert.equal(ui.get('field-id').required, false);
  ui.get('field-name').value = 'Free coffee';
  ui.get('field-description').value = 'One standard coffee';
  await ui.submit();
  const first = ui.calls.find(c => c.name === 'save_privilege');
  assert.equal(Object.hasOwn(first.args, 'id'), false);
  assert.equal(ui.get('field-id').value, 'generated-1');
  assert.equal(ui.get('field-id').readOnly, true);
  assert.equal(ui.get('field-id').parentElement.hidden, false);
  ui.get('field-stock').value = '20';
  await ui.submit();
  assert.equal(ui.calls.filter(c => c.name === 'save_privilege')[1].args.id, 'generated-1');
  assert.equal(ui.records.size, 1);
  assert.equal(ui.records.get('generated-1').stock, 20);
  ui.get('new').onclick();
  assert.equal(ui.get('field-id').value, '');
  assert.equal(ui.get('field-id').parentElement.hidden, true);
});

test('a tool-created master record opens in the editor with its generated ID', async () => {
  const record = { id: 'server-generated', kind: 'tier', name: 'Gold', active: true, data: {} };
  const ui = setup({ tool: 'save_master_record', arguments: { kind: 'tier', name: 'Gold', active: true, data: {} }, data: { saved: record.id, record } });
  await ui.settle();
  assert.equal(ui.get('field-id').value, record.id);
  assert.equal(ui.get('field-id').readOnly, true);
  await ui.submit();
  assert.equal(ui.calls.find(c => c.name === 'save_master_record').args.id, record.id);
});
