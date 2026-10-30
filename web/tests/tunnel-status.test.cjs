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

function harness({reduced = false} = {}) {
    const animal = animalNode();
    const tasks = new Map();
    let nextTask = 0;
    let now = 0;
    const clock = {
        tasks,
        advance(ms) {
            const until = now + ms;
            for (;;) {
                const task = [...tasks].filter(([, task]) => task.at <= until).sort((a, b) => a[1].at - b[1].at)[0];
                if (!task) break;
                tasks.delete(task[0]);
                now = task[1].at;
                task[1].callback();
            }
            now = until;
        },
    };
    const value = animalNode();
    const proto = {...animalNode(), querySelector() { return value; }};
    const dot = {hidden: false};
    const pillText = {removeAttribute() {}};
    const pill = {...animalNode(), querySelector(selector) {
        return {'.proto': proto, '.dot': dot, '.text': pillText}[selector] || null;
    }, contains(element) { return element === proto; }};
    const network = {fail: true};
    const rows = ['a', 'b'].map(key => ({
        dataset: {key}, classList: {toggle() {}}, animal: animalNode(), protocol: animalNode(),
        querySelector(selector) {
            if (selector === '.health-animal') return this.animal;
            if (selector.includes('runtime')) return this.runtime ||= {};
            if (selector.includes('__protocol')) return this.protocol;
            if (selector.includes('summary')) return {setAttribute() {}};
            return null;
        },
    }));
    const state = {config: {active_tunnel_key: 'a', tunnels: [{key: 'a'}, {key: 'b'}]}, selectedTunnelKey: 'a',
        tunnelStatuses: {}, statusFailCount: 0, runningSigs: {a: 'saved'}, tunnelAlertDismissed: ''};
    const cfui = {state, $: id => id === 'status-pill' ? pill : null, t: (key, params) => params ? key + JSON.stringify(params) : key,
        apiGet: async () => {if (network.fail) throw Error('browser offline'); return {statuses: state.tunnelStatuses};}, addLog() {}};
    const motion = {matches: reduced, addEventListener(type, listener) {this.listener = listener;}};
    const document = {hidden: false, events: {}, addEventListener(type, listener) {this.events[type] = listener;}, querySelectorAll: () => rows, createElement: () => animalNode()};
    const window = {cfui, events: {}, matchMedia: () => motion,
        addEventListener(type, listener) {this.events[type] = listener;},
        setTimeout(callback, delay) { const id = ++nextTask; tasks.set(id, {callback, at: now + delay}); return id; },
        clearTimeout(id) {tasks.delete(id);},
    };
    const context = {window, document, console};
    vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../dist/js/app-tunnel.js'), 'utf8'), context);
    return {cfui, state, rows, pill, pillText, proto, dot, value, clock, motion, document, window, context, network, animal};
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
    assert.match(pill.attributes['aria-label'], /status_stale/);
    assert.equal(pill.querySelector('.health-animal'), null);
    assert.equal(pill.attributes['data-state'], 'offline');
    for (const row of rows) assert.equal(row.animal.dataset.health, 'unknown');
    network.fail = false;
    await cfui.fetchStatus();
    assert.equal(state.statusFailCount, 0);
    for (const row of rows) assert.equal(row.dataset.state, 'running');
    assert.equal(pill.attributes['data-state'], 'ok');
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
    const {cfui, state, pill, network} = harness();
    state.tunnelStatuses = {
        a: {running: true, status: 'running', connectivity: 'connected', last_checked_at: '2026-10-29T10:00:00Z', health_error: 'selected-only detail'},
        b: {running: false, status: 'error', connectivity: 'unknown', error: 'invalid token'},
    };
    network.fail = false;
    await cfui.fetchStatus();
    assert.equal(pill.attributes['data-state'], 'error');
    assert.match(pill.attributes['aria-label'], /status_error.*tunnels_active_ratio/);
    assert.doesNotMatch(pill.attributes['aria-label'], /last_checked|selected-only|2026/);
    network.fail = true;
    await cfui.fetchStatus(); await cfui.fetchStatus(); await cfui.fetchStatus();
    assert.equal(pill.attributes['data-state'], 'offline');
    assert.match(pill.attributes['aria-label'], /status_stale.*tunnels_active_ratio/);
    assert.doesNotMatch(pill.attributes['aria-label'], /last_checked|selected-only|2026/);
});

test('header has a dot and deduplicated protocols, without animals', async () => {
    const {cfui, state, network, pill, dot, value, clock, rows} = harness();
    state.config.tunnels[0].protocol = 'auto';
    state.config.tunnels[1].protocol = 'auto';
    state.tunnelStatuses = {a: {running: true, status: 'running', connectivity: 'connected', protocol: 'auto', active_protocols: ['quic']},
        b: {running: true, status: 'running', connectivity: 'connected', protocol: 'auto', active_protocols: ['quic']}};
    network.fail = false;
    await cfui.fetchStatus();
    assert.equal(pill.querySelector('.health-animal'), null);
    assert.equal(dot.hidden, false);
    assert.equal(value.textContent, 'QUIC');
    assert.equal(clock.tasks.size, 0);
    assert.equal(rows[0].protocol.textContent, 'QUIC');
    assert.equal(rows[1].protocol.textContent, 'QUIC');
    assert.match(pill.attributes['aria-label'], /protocol_display_set.*QUIC/);
});

test('mixed protocol set rotates without polling resets or live announcements', () => {
    const {cfui, value, clock, proto} = harness();
    const profiles = [{key: 'a', protocol: 'http2'}, {key: 'b', protocol: 'quic'}, {key: 'c', protocol: 'quic'}];
    assert.deepEqual(Array.from(cfui.updateHeaderProtocols(profiles, {})), ['HTTP2', 'QUIC']);
    assert.equal(value.textContent, 'HTTP2');
    const label = proto.attributes['aria-label'];
    clock.advance(2000);
    cfui.updateHeaderProtocols(profiles, {});
    clock.advance(3000);
    assert.equal(proto.attributes['data-fading'], 'true');
    clock.advance(160);
    assert.equal(value.textContent, 'QUIC');
    assert.equal(proto.attributes['aria-label'], label);
    cfui.updateHeaderProtocols(profiles, {});
    assert.equal(value.textContent, 'QUIC');
    assert.equal(clock.tasks.size, 1);
    cfui.updateHeaderProtocols([{key: 'a', protocol: 'quic'}], {});
    assert.equal(value.textContent, 'QUIC');
    assert.equal(clock.tasks.size, 0);
});

test('rotation supports manual pause, hover, focus, page visibility and cleanup', () => {
    const {cfui, value, clock, proto, pill, document, window} = harness();
    cfui.updateHeaderProtocols([{key: 'a', protocol: 'auto'}, {key: 'b', protocol: 'quic'}], {});
    proto.events.click();
    assert.equal(proto.attributes['aria-pressed'], 'true');
    assert.equal(clock.tasks.size, 0);
    clock.advance(10000);
    assert.equal(value.textContent, 'AUTO');
    proto.events.click();
    assert.equal(clock.tasks.size, 1);
    pill.events.pointerenter();
    assert.equal(clock.tasks.size, 0);
    pill.events.pointerleave();
    assert.equal(clock.tasks.size, 1);
    pill.events.focusin();
    assert.equal(clock.tasks.size, 0);
    pill.events.focusout({relatedTarget: null});
    assert.equal(clock.tasks.size, 1);
    document.hidden = true;
    document.events.visibilitychange();
    assert.equal(clock.tasks.size, 0);
    document.hidden = false;
    document.events.visibilitychange();
    assert.equal(clock.tasks.size, 1);
    window.events.pagehide();
    assert.equal(clock.tasks.size, 0);
});

test('reduced motion keeps one protocol and changes instantly without a fade timer', () => {
    const {cfui, clock, value, motion, proto} = harness({reduced: true});
    cfui.updateHeaderProtocols([{key: 'a', protocol: 'auto'}, {key: 'b', protocol: 'quic'}, {key: 'c', protocol: 'http2'}], {});
    assert.equal(value.textContent, 'AUTO');
    assert.equal(clock.tasks.size, 1);
    clock.advance(5000);
    assert.equal(value.textContent, 'HTTP2');
    assert.equal(proto.attributes['data-fading'], undefined);
    assert.equal(clock.tasks.size, 1);
    motion.matches = false;
    motion.listener();
    clock.advance(5000);
    assert.equal(proto.attributes['data-fading'], 'true');
    assert.equal(clock.tasks.size, 1);
    motion.matches = true;
    motion.listener();
    assert.equal(value.textContent, 'HTTP2');
    assert.equal(proto.attributes['data-fading'], undefined);
    assert.equal(clock.tasks.size, 1);
    clock.advance(5000);
    assert.equal(value.textContent, 'QUIC');
    assert.equal(clock.tasks.size, 1);
});

test('every card shows observed protocol or unknown while stopped cards retain configured values', async () => {
    const {cfui, state, network, rows} = harness();
    state.config.tunnels[0].protocol = 'http2';
    state.config.tunnels[1].protocol = 'auto';
    state.tunnelStatuses = {a: {status: 'stopped'}, b: {running: true, status: 'running', connectivity: 'connected', protocol: 'auto'}};
    network.fail = false;
    await cfui.fetchStatus();
    assert.equal(rows[0].protocol.textContent, 'HTTP2');
    assert.match(rows[1].protocol.innerHTML, /<svg class="protocol-loader"/);
    assert.equal(rows[1].protocol.attributes['aria-label'], 'protocol_pending');
    assert.equal(rows[0].protocol.hidden, false);
    assert.equal(rows[1].protocol.hidden, false);
});


test('automatic tunnels display observed transports and follow fallback', async () => {
    const {cfui, state, network, rows, value, clock} = harness();
    state.config.tunnels.forEach(profile => { profile.protocol = 'auto'; });
    state.tunnelStatuses = {
        a: {running: true, status: 'running', protocol: 'auto', active_protocols: ['quic']},
        b: {running: true, status: 'running', protocol: 'auto', active_protocols: ['http2']},
    };
    network.fail = false;
    await cfui.fetchStatus();
    assert.equal(rows[0].protocol.textContent, 'QUIC');
    assert.equal(rows[1].protocol.textContent, 'HTTP2');
    assert.equal(value.textContent, 'HTTP2');
    clock.advance(5160);
    assert.equal(value.textContent, 'QUIC');
    state.tunnelStatuses.a.active_protocols = ['http2'];
    await cfui.fetchStatus();
    assert.equal(value.textContent, 'HTTP2');
    assert.equal(clock.tasks.size, 0);
    assert.equal(rows[0].protocol.textContent, 'HTTP2');
});

test('active protocols exclude stopped configuration and unknown never claims AUTO', () => {
    const {cfui} = harness();
    const profiles = [{key: 'a', protocol: 'auto'}, {key: 'b', protocol: 'auto'}];
    assert.deepEqual(Array.from(cfui.protocolLabels(profiles, {a: {running: true, active_protocols: ['quic', 'http2', 'quic']}})), ['HTTP2', 'QUIC']);
    for (const status of [{running: true}, {status: 'reconnecting'}, {running: true, active_protocols: ['auto', 'invalid']}]) {
        assert.deepEqual(Array.from(cfui.protocolLabels(profiles, {a: status})), ['—']);
    }
});

test('one tunnel can show mixed active protocols on its card', async () => {
    const {cfui, state, network, rows} = harness();
    state.tunnelStatuses = {a: {running: true, active_protocols: ['quic', 'http2', 'quic']}};
    network.fail = false;
    await cfui.fetchStatus();
    assert.equal(rows[0].protocol.textContent, 'HTTP2 / QUIC');
});


test('pending protocol uses an accessible SVG in both slots without resetting on polling', async () => {
    const {cfui, state, network, rows, value, proto, pill} = harness();
    state.tunnelStatuses = {a: {running: true, status: 'starting'}, b: {running: true, status: 'reconnecting'}};
    network.fail = false;
    await cfui.fetchStatus();
    const svg = value.innerHTML;
    assert.match(svg, /<svg class="protocol-loader"/);
    assert.match(svg, /aria-hidden="true"/);
    assert.equal(value.attributes['aria-label'], 'protocol_pending');
    assert.match(proto.attributes['aria-label'], /protocol_pending/);
    assert.match(pill.attributes['aria-label'], /protocol_pending/);
    assert.doesNotMatch(proto.attributes['aria-label'], /—/);
    assert.equal(rows[0].protocol.innerHTML, svg);
    assert.equal(rows[1].protocol.innerHTML, svg);
    let writes = 0;
    Object.defineProperty(value, 'innerHTML', {get: () => svg, set: () => {writes++;}});
    await cfui.fetchStatus();
    assert.equal(writes, 0);
    state.tunnelStatuses.a.active_protocols = ['quic'];
    state.tunnelStatuses.b.active_protocols = ['quic'];
    await cfui.fetchStatus();
    assert.equal(value.textContent, 'QUIC');
    assert.equal(rows[0].protocol.textContent, 'QUIC');
    assert.equal(rows[1].protocol.textContent, 'QUIC');
});
