const {test} = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
const source = name => fs.readFileSync(path.join(__dirname, '../dist/js', name), 'utf8');

function curtain({storageBlocked = false} = {}) {
    const tasks = new Map();
    let next = 0;
    const nodes = Object.fromEntries(['boot-screen', 'boot-message', 'boot-retry'].map(id => [id, {
        dataset: {}, hidden: id === 'boot-retry', attributes: {}, events: {},
        setAttribute(k, v) { this.attributes[k] = v; },
        addEventListener(k, v) { this.events[k] = v; },
        remove() { this.removed = true; },
    }]));
    const walkers = ['connected', 'warning', 'error', 'unknown'].map(name => {
        const image = {src: name, getAttribute() { return name; }};
        return {image, style: {setProperty() {}}, events: {},
            querySelector() { return image; }, addEventListener(name, fn) { this.events[name] = fn; }};
    });
    nodes['boot-screen'].querySelectorAll = () => walkers;
    const root = {dataset: {}, classList: {remove(name) { this.removed = name; }}};
    const window = {};
    const location = {reload() { this.reloaded = true; }};
    vm.runInNewContext(source('app-boot.js'), {
        window, document: {getElementById: id => nodes[id], documentElement: root},
        navigator: {language: 'zh-CN'}, localStorage: {getItem() { if (storageBlocked) throw Error('blocked'); return null; }}, location,
        setTimeout(fn, ms) { tasks.set(++next, {fn, ms}); return next; }, clearTimeout(id) { tasks.delete(id); },
    });
    return {boot: window.cfuiBoot, nodes, root, tasks, location, walkers};
}

test('curtain stays until ready, then releases page and removes itself', () => {
    const {boot, nodes, root, tasks} = curtain();
    assert.equal(root.classList.removed, undefined);
    boot.ready();
    assert.equal(root.classList.removed, 'booting');
    assert.equal(nodes['boot-screen'].dataset.phase, 'ready');
    assert.equal(nodes['boot-screen'].attributes['aria-busy'], 'false');
    assert.deepEqual([...tasks.values()].map(t => t.ms), [250]);
    [...tasks.values()][0].fn();
    assert.equal(nodes['boot-screen'].removed, true);
    boot.fail();
    assert.equal(nodes['boot-screen'].dataset.phase, 'ready');
});

test('watchdog offers reload without exposing incomplete content and can recover', () => {
    const {boot, tasks, nodes, root, location} = curtain();
    [...tasks.values()][0].fn();
    assert.equal(nodes['boot-retry'].hidden, false);
    assert.equal(nodes['boot-screen'].dataset.phase, 'error');
    assert.equal(root.classList.removed, undefined);
    nodes['boot-retry'].events.click();
    assert.equal(location.reloaded, true);
    boot.ready();
    assert.equal(root.classList.removed, 'booting');
});

test('authentication can wait indefinitely with the curtain suspended', () => {
    const {boot, tasks, nodes} = curtain();
    boot.waitForLogin(true);
    assert.equal(tasks.size, 0);
    assert.equal(nodes['boot-screen'].hidden, true);
    boot.waitForLogin(false);
    assert.equal(nodes['boot-screen'].hidden, false);
    assert.deepEqual([...tasks.values()].map(t => t.ms), [20000]);
});

test('blocked local storage still shows a localized curtain and watchdog', () => {
    const {nodes, tasks} = curtain({storageBlocked: true});
    assert.equal(nodes['boot-message'].textContent, '小伙伴们正在准备…');
    assert.equal(tasks.size, 1);
});

async function bootstrap({configFailure = false} = {}) {
    const calls = [];
    let resolveConfig;
    const configWait = new Promise(resolve => { resolveConfig = resolve; });
    const cfui = {state: {features: {tunnel_manager: true}, currentLang: 'zh'},
        $: () => ({hidden: true}), currentWorkspace: () => 'local',
        fetchConfig: async options => { calls.push(['config', options.required]); await configWait; if (configFailure) throw Error('offline'); },
        fetchStatus: async options => { calls.push(['status', options.required]); },
        // This optional service intentionally never finishes.
        fetchTunnelManagerSettings: () => new Promise(() => {}),
        toast: {err() {}},
    };
    for (const name of ['loadLanguage', 'initTheme', 'updateMetricsVisibility', 'addLog', 'fetchVersion', 'fetchFeatures',
        'syncWorkspaceFromRoute', 'wireUI', 'wireLocalAuth', 'initializeLocalAuth', 'wireTunnel', 'wireLogs',
        'wireServices', 'wireBackup', 'wireS3', 'wireOAuth', 'disconnectLogStream']) {
        cfui[name] = async () => {};
    }
    const boot = {ready() { calls.push(['ready']); }, fail() { calls.push(['fail']); }};
    vm.runInNewContext(source('app-init.js'), {window: {cfui, cfuiBoot: boot, addEventListener() {}},
        document: {addEventListener() {}}, console: {error() {}}, setInterval() {return 1;}, clearInterval() {},
    });
    const flush = async () => { for (let i = 0; i < 20; i++) await Promise.resolve(); };
    await flush();
    assert.deepEqual(calls, [['config', true]]);
    resolveConfig();
    await flush();
    return calls;
}

test('bootstrap reveals only after core data, without waiting for optional remote services', async () => {
    assert.deepEqual(await bootstrap(), [['config', true], ['status', true], ['ready']]);
});

test('core config failure keeps the curtain in error instead of revealing the shell', async () => {
    assert.deepEqual(await bootstrap({configFailure: true}), [['config', true], ['fail']]);
});


test('each parade round uses all four animals and swaps only on the walker loop', () => {
    const {walkers} = curtain();
    const expected = ['connected', 'error', 'unknown', 'warning'];
    assert.deepEqual(walkers.map(w => w.image.src).sort(), expected);
    for (let round = 0; round < 3; round++) {
        const chosen = [];
        for (const walker of walkers) {
            const before = walker.image.src;
            walker.events.animationiteration({target: walker.image, animationName: 'boot-peek'});
            assert.equal(walker.image.src, before);
            walker.events.animationiteration({target: walker, animationName: 'boot-walk'});
            chosen.push(walker.image.src);
        }
        assert.deepEqual(chosen.sort(), expected);
    }
});
