/* Own the initial curtain independently of the application bundle. */
(() => {
    'use strict';
    const screen = document.getElementById('boot-screen');
    const message = document.getElementById('boot-message');
    const retry = document.getElementById('boot-retry');
    let savedLang;
    try { savedLang = localStorage.getItem('lang'); } catch { /* use browser locale */ }
    const lang = (savedLang || navigator.language || 'en').slice(0, 2);
    const copy = {
        zh: ['小伙伴们正在准备…', '加载遇到了问题，请重试', '重新加载'],
        ja: ['みんなで準備中…', '読み込めませんでした。もう一度お試しください', '再読み込み'],
        en: ['Getting things ready…', 'Unable to finish loading. Please try again.', 'Reload'],
    }[lang] || ['Getting things ready…', 'Unable to finish loading. Please try again.', 'Reload'];
    message.textContent = copy[0];
    retry.textContent = copy[2];
    try {
        const theme = localStorage.getItem('theme');
        if (theme === 'light' || theme === 'dark') document.documentElement.dataset.theme = theme;
    } catch { /* system theme remains available */ }
    const walkers = Array.from(screen.querySelectorAll('.boot-walker'));
    const sources = walkers.map(walker => walker.querySelector('img').getAttribute('src'));
    let queue = [];
    function nextAnimal() {
        if (!queue.length) {
            queue = [...sources];
            for (let i = queue.length - 1; i > 0; i--) {
                const j = Math.floor(Math.random() * (i + 1));
                [queue[i], queue[j]] = [queue[j], queue[i]];
            }
        }
        return queue.shift();
    }
    walkers.forEach((walker, index) => {
        const image = walker.querySelector('img');
        image.src = nextAnimal();
        walker.style.setProperty('--boot-slot', index);
        walker.addEventListener('animationiteration', event => {
            // Swap only while this walker is outside the lane; never replace a visible pet.
            if (event.target === walker && event.animationName === 'boot-walk') image.src = nextAnimal();
        });
    });
    let finished = false;
    let timer;
    function fail() {
        if (finished) return;
        clearTimeout(timer);
        screen.dataset.phase = 'error';
        screen.setAttribute('aria-busy', 'false');
        message.textContent = copy[1];
        retry.hidden = false;
    }
    function arm() { timer = setTimeout(fail, 20000); }
    retry.addEventListener('click', () => location.reload());
    arm();
    window.cfuiBoot = {
        fail,
        // Authentication dialogs sit above the curtain and may wait indefinitely.
        waitForLogin(waiting) {
            clearTimeout(timer);
            screen.hidden = waiting;
            if (!waiting) arm();
        },
        ready() {
            if (finished) return;
            finished = true;
            clearTimeout(timer);
            document.documentElement.classList.remove('booting');
            screen.hidden = false;
            screen.dataset.phase = 'ready';
            screen.setAttribute('aria-busy', 'false');
            const remove = () => screen.remove();
            screen.addEventListener('transitionend', (event) => {
                if (event.target === screen && event.propertyName === 'opacity') remove();
            });
            setTimeout(remove, 250);
        },
    };
})();
