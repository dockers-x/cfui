const {test} = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
const {createHash} = require('node:crypto');

function animalNode() {
    return {dataset: {}, attributes: {}, events: {}, face: {},
        setAttribute(key, value) { this.attributes[key] = value; },
        getAttribute(key) { return this.attributes[key]; },
        removeAttribute(key) { delete this.attributes[key]; },
        querySelector() { return this.face; },
        addEventListener(key, handler) { this.events[key] = handler; },
    };
}

function harness() {
    const animal = animalNode();
    const pill = {setAttribute(key, value) { this[key] = value; }, querySelector(selector) {
        return selector === '.health-animal' ? animal : {removeAttribute() {}};
    }};
    const network = {fail: true};
    const rows = ['a', 'b'].map(key => ({
        dataset: {key}, classList: {toggle() {}}, animal: animalNode(),
        querySelector(selector) {
            if (selector === '.health-animal') return this.animal;
            if (selector.includes('runtime')) return this.runtime ||= {};
            if (selector.includes('summary')) return {setAttribute() {}};
            return null;
        },
    }));
    const state = {config: {active_tunnel_key: 'a', tunnels: [{key: 'a'}, {key: 'b'}]}, selectedTunnelKey: 'a',
        tunnelStatuses: {}, statusFailCount: 0, runningSigs: {a: 'saved'}, tunnelAlertDismissed: ''};
    const cfui = {state, $: id => id === 'status-pill' ? pill : null, t: (key, params) => params ? key + JSON.stringify(params) : key,
        apiGet: async () => {if (network.fail) throw Error('browser offline'); return {statuses: state.tunnelStatuses};}, addLog() {}};
    const context = {window: {cfui}, document: {querySelectorAll: () => rows, createElement: () => animalNode()}, console};
    vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../dist/js/app-tunnel.js'), 'utf8'), context);
    return {cfui, state, rows, pill, context, network, animal};
}

test('running instance only gets green with confirmed edge connectivity', () => {
    const {cfui} = harness();
    for (const health of ['unknown', 'degraded', 'disconnected', undefined]) {
        const view = cfui.tunnelPresentation({running: true, status: 'running', connectivity: health});
        assert.notEqual(view.pill, 'ok');
        assert.notEqual(view.state, 'running');
    }
    assert.equal(cfui.tunnelPresentation({running: true, status: 'running', connectivity: 'connected'}).pill, 'ok');
    for (const phase of ['starting', 'stopping', 'reconnecting']) {
        assert.equal(cfui.tunnelPresentation({running: true, status: phase, connectivity: 'connected'}).state, phase);
    }
});

test('overview counts connected edges independently from active instances', () => {
    const {cfui} = harness();
    const view = cfui.tunnelOverview([{key: 'a'}, {key: 'b'}], {
        a: {running: true, status: 'running', connectivity: 'connected'},
        b: {running: true, status: 'reconnecting', connectivity: 'connected'},
    });
    assert.equal(view.active, 2);
    assert.equal(view.connected, 1);
    assert.notEqual(view.pill, 'ok');
});

test('three poll failures mark every row unknown without changing last observation, then recover', async () => {
    const {cfui, state, rows, pill, network, animal} = harness();
    const observed = '2026-10-29T10:00:00Z';
    state.tunnelStatuses = {a: {running: true, status: 'running', connectivity: 'connected', last_checked_at: observed},
        b: {running: true, status: 'running', connectivity: 'connected', last_checked_at: observed}};
    await cfui.fetchStatus(); await cfui.fetchStatus(); await cfui.fetchStatus();
    assert.equal(state.statusFailCount, 3);
    assert.equal(state.tunnelStatuses.a.last_checked_at, observed);
    assert.equal(state.tunnelStatuses.a.connectivity, 'connected');
    for (const row of rows) assert.equal(row.dataset.state, 'unknown');
    assert.match(animal.attributes['aria-label'], /status_stale/);
    assert.equal(animal.title, undefined);
    assert.equal(animal.dataset.health, 'unknown');
    for (const row of rows) assert.equal(row.animal.dataset.health, 'unknown');
    network.fail = false;
    await cfui.fetchStatus();
    assert.equal(state.statusFailCount, 0);
    for (const row of rows) assert.equal(row.dataset.state, 'running');
    assert.equal(pill['data-state'], 'ok');
});


test('official animal health state remains available to assistive technology without visible text', () => {
    const {cfui, animal} = harness();
    for (const health of ['connected', 'degraded', 'disconnected', 'unknown']) {
        cfui.updateHealthAnimal(animal, {status: 'running', running: true, connectivity: health});
        assert.equal(animal.dataset.health, health);
        assert.equal(animal.attributes['aria-label'], 'status_' + health);
        assert.equal(animal.title, undefined);
    }
    cfui.updateHealthAnimal(animal, {status: 'reconnecting', connectivity: 'disconnected'});
    assert.equal(animal.dataset.health, 'recovering');
});

test('animal can expand with a touch click and collapse on Escape or blur', () => {
    const {cfui} = harness();
    const animal = cfui.createHealthAnimal();
    let stopped = false;
    animal.events.click({stopPropagation() { stopped = true; }});
    assert.equal(stopped, true);
    assert.equal(animal.getAttribute('aria-expanded'), 'true');
    animal.events.keydown({key: 'Escape'});
    assert.equal(animal.getAttribute('aria-expanded'), 'false');
    animal.events.click({stopPropagation() {}});
    animal.events.blur();
    assert.equal(animal.getAttribute('aria-expanded'), 'false');
});


test('health is represented by official animal state while runtime text remains lifecycle', async () => {
    const {cfui, state, network, rows} = harness();
    state.tunnelStatuses = {a: {running: true, status: 'running', connectivity: 'degraded'},
        b: {running: true, status: 'reconnecting', connectivity: 'disconnected'}};
    network.fail = false;
    await cfui.fetchStatus();
    assert.equal(rows[0].runtime.textContent, 'status_running');
    assert.equal(rows[0].animal.dataset.health, 'degraded');
    assert.match(rows[0].animal.attributes['aria-label'], /status_degraded/);
    assert.equal(rows[1].animal.dataset.health, 'recovering');
});

test('vendored PNGs match the pinned original react-animals artwork', () => {
    const iconDir = path.join(__dirname, '../dist/icons/react-animals');
    const metadata = JSON.parse(fs.readFileSync(path.join(iconDir, 'UPSTREAM-package.json'), 'utf8'));
    assert.equal(metadata.name, 'react-animals');
    assert.equal(metadata.version, '0.0.82');
    const hashes = {
        panda: '847235a238b90011ed8648d248f68a9454c7da848676104570b24fa6383372b3',
        otter: '51caa3a89e23c8c8924027fd78736c70e12b904f608f4e3d1ca9ae9aa0431f83',
        grizzly: '143d4c799e0dbfaf7f107328ee38d0132e6eb8d7de5af7cca21cf69cc175f61d',
        rabbit: '5ba9c64d1b32fd21972e45d106b272aa430dc8dc02dc81feb42909f129997b64',
    };
    for (const [animal, expected] of Object.entries(hashes)) {
        const image = fs.readFileSync(path.join(iconDir, animal + '.png'));
        assert.equal(createHash('sha256').update(image).digest('hex'), expected);
        assert.deepEqual(image.subarray(0, 8), Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]));
    }
    assert.equal(fs.existsSync(path.join(iconDir, 'fox.svg')), false);
});

test('lifecycle error is red and stopped instances never keep stale connected health', () => {
    const {cfui, animal} = harness();
    cfui.updateHealthAnimal(animal, {running: false, status: 'error', connectivity: 'unknown', error: 'invalid token'});
    assert.equal(animal.dataset.health, 'error');
    assert.match(animal.attributes['aria-label'], /status_error.*invalid token/);
    cfui.updateHealthAnimal(animal, {running: false, status: 'stopped', connectivity: 'connected'});
    assert.equal(animal.dataset.health, 'unknown');
    assert.notEqual(cfui.tunnelOverview([{key: 'a'}], {a: {status: 'stopped', connectivity: 'connected'}}).health, 'connected');
});

test('aggregate prioritizes lifecycle errors and never borrows selected tunnel observation details', async () => {
    const {cfui, state, animal, network} = harness();
    state.tunnelStatuses = {
        a: {running: true, status: 'running', connectivity: 'connected', last_checked_at: '2026-10-29T10:00:00Z', health_error: 'selected-only detail'},
        b: {running: false, status: 'error', connectivity: 'unknown', error: 'invalid token'},
    };
    network.fail = false;
    await cfui.fetchStatus();
    assert.equal(animal.dataset.health, 'error');
    assert.match(animal.attributes['aria-label'], /status_error.*tunnels_active_ratio/);
    assert.doesNotMatch(animal.attributes['aria-label'], /last_checked|selected-only|2026/);
    network.fail = true;
    await cfui.fetchStatus(); await cfui.fetchStatus(); await cfui.fetchStatus();
    assert.equal(animal.dataset.health, 'unknown');
    assert.match(animal.attributes['aria-label'], /status_stale.*tunnels_active_ratio/);
    assert.doesNotMatch(animal.attributes['aria-label'], /last_checked|selected-only|2026/);
});
