(() => {
  'use strict';

  const $ = (s, r = document) => r.querySelector(s);
  const $$ = (s, r = document) => [...r.querySelectorAll(s)];
  const reduceMotion = matchMedia('(prefers-reduced-motion: reduce)').matches;

  // Builds DOM with textContent only, so user-controlled strings (URLs, emails,
  // referrers) can never be interpreted as HTML.
  function h(tag, props = {}, ...kids) {
    const n = document.createElement(tag);
    for (const [k, v] of Object.entries(props)) {
      if (v == null || v === false) continue;
      if (k === 'class') n.className = v;
      else if (k.startsWith('on')) n.addEventListener(k.slice(2), v);
      else n.setAttribute(k, v === true ? '' : v);
    }
    for (const kid of kids.flat()) if (kid != null) n.append(kid.nodeType ? kid : document.createTextNode(kid));
    return n;
  }
  const svgNS = 'http://www.w3.org/2000/svg';
  function s(tag, attrs = {}) {
    const n = document.createElementNS(svgNS, tag);
    for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, v);
    return n;
  }

  async function api(path, { method = 'GET', body } = {}) {
    const res = await fetch('/api/v1' + path, {
      method,
      credentials: 'same-origin',
      headers: method === 'GET' ? {} : { 'Content-Type': 'application/json' },
      body: body ? JSON.stringify(body) : undefined,
    });
    if (res.status === 204) return null;
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      const e = new Error(data.error || 'Something went wrong');
      e.status = res.status;
      throw e;
    }
    return data;
  }

  let toastTimer;
  function toast(msg) {
    const t = $('#toast');
    t.textContent = msg;
    t.classList.add('show');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => t.classList.remove('show'), 2400);
  }
  async function copy(text) {
    try { await navigator.clipboard.writeText(text); toast('Copied to clipboard'); }
    catch { toast('Copy failed — select and copy manually'); }
  }
  const fmt = new Intl.NumberFormat();

  /* ---------------- theme ---------------- */
  const icons = {
    system: '<circle cx="12" cy="12" r="8.5"/><path d="M12 3.5v17"/><path d="M12 3.5a8.5 8.5 0 0 1 0 17z" fill="currentColor" stroke="none"/>',
    light: '<circle cx="12" cy="12" r="4.5"/><path d="M12 2.5v2.5m0 14v2.5M4.6 4.6l1.8 1.8m11.2 11.2 1.8 1.8M2.5 12H5m14 0h2.5M4.6 19.4l1.8-1.8m11.2-11.2 1.8-1.8"/>',
    dark: '<path d="M20.5 14.5A8.5 8.5 0 0 1 9.5 3.5a8.5 8.5 0 1 0 11 11Z"/>',
  };
  function applyTheme(t) {
    document.documentElement.setAttribute('data-theme', t);
    try { localStorage.setItem('snaplink-theme', t); } catch {}
    const svg = $('#theme-icon');
    svg.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true">' + icons[t] + '</svg>'; // static markup, no user data
    $('#theme-toggle').setAttribute('aria-label', 'Theme: ' + t + '. Click to change');
    const dark = t === 'dark' || (t === 'system' && !matchMedia('(prefers-color-scheme: light)').matches);
    $('#theme-color-meta').setAttribute('content', dark ? '#0f0d0b' : '#f5f1e8');
  }
  $('#theme-toggle').addEventListener('click', () => {
    const order = ['system', 'light', 'dark'];
    applyTheme(order[(order.indexOf(document.documentElement.getAttribute('data-theme')) + 1) % 3]);
  });
  applyTheme(document.documentElement.getAttribute('data-theme') || 'system');

  /* ---------------- auth ---------------- */
  let user = null;
  const dlg = $('#auth-dialog');
  let mode = 'login';
  let afterAuth = null;

  function setMode(m) {
    mode = m;
    const signup = m === 'signup';
    $('#tab-login').setAttribute('aria-selected', String(!signup));
    $('#tab-signup').setAttribute('aria-selected', String(signup));
    $('#auth-title').textContent = signup ? 'Create account' : 'Sign in';
    $('#auth-submit').textContent = signup ? 'Create account' : 'Sign in';
    $('#a-pass-hint').hidden = !signup;
    $('#a-pass').setAttribute('autocomplete', signup ? 'new-password' : 'current-password');
    $('#auth-error').hidden = true;
  }
  function openAuth(m, then) {
    setMode(m);
    afterAuth = then || null;
    if (!dlg.open) dlg.showModal();
    $('#a-email').focus();
  }
  $('#tab-login').addEventListener('click', () => setMode('login'));
  $('#tab-signup').addEventListener('click', () => setMode('signup'));
  $('#auth-close').addEventListener('click', () => dlg.close());
  dlg.addEventListener('click', (e) => { if (e.target === dlg) dlg.close(); });
  $$('[data-open-auth]').forEach((b) => b.addEventListener('click', () => {
    if (user) location.hash = '#/dashboard';
    else openAuth(b.dataset.openAuth, () => { location.hash = '#/dashboard'; });
  }));

  $('#auth-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const err = $('#auth-error');
    err.hidden = true;
    const btn = $('#auth-submit');
    btn.disabled = true;
    try {
      const res = await api('/auth/' + mode, { method: 'POST', body: { email: $('#a-email').value, password: $('#a-pass').value } });
      user = res;
      $('#a-pass').value = '';
      dlg.close();
      renderAuthSlot();
      toast(mode === 'signup' ? 'Welcome to Snaplink' : 'Signed in');
      const next = afterAuth; afterAuth = null;
      if (next) next(); else route();
    } catch (ex) {
      err.textContent = ex.message;
      err.hidden = false;
    } finally { btn.disabled = false; }
  });

  function renderAuthSlot() {
    const slot = $('#auth-slot');
    slot.replaceChildren();
    if (user) {
      slot.append(
        h('a', { class: 'btn ghost sm', href: '#/dashboard' }, 'Dashboard'),
        h('button', { class: 'btn ghost sm', type: 'button', onclick: async () => {
          try { await api('/auth/logout', { method: 'POST' }); } catch {}
          user = null; renderAuthSlot(); toast('Signed out'); location.hash = '#/';
        } }, 'Sign out'),
      );
    } else {
      slot.append(h('button', { class: 'btn solid sm', type: 'button', onclick: () => openAuth('login', () => { location.hash = '#/dashboard'; }) }, 'Sign in'));
    }
    $('#locked-row').hidden = !!user;
    $('#cta').hidden = !!user;
  }

  /* ---------------- landing: shorten ---------------- */
  $('#shorten-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const err = $('#shorten-error'), btn = $('#shorten-btn');
    err.hidden = true;
    btn.disabled = true;
    try {
      const r = await api('/links', { method: 'POST', body: { url: $('#url').value } });
      const a = $('#result-link');
      a.textContent = r.short_url.replace(/^https?:\/\//, '');
      a.href = r.short_url;
      $('#result').hidden = false;
      $('#copy-result').onclick = () => copy(r.short_url);
    } catch (ex) {
      $('#result').hidden = true;
      err.textContent = ex.message;
      err.hidden = false;
    } finally { btn.disabled = false; }
  });

  /* ---------------- landing: motion ---------------- */
  function initReveal() {
    const items = $$('.reveal');
    items.forEach((el, i) => el.style.setProperty('--d', (i % 4) * 0.08 + 's'));
    if (!('IntersectionObserver' in window)) { items.forEach((el) => el.classList.add('in')); return; }
    const io = new IntersectionObserver((entries) => entries.forEach((en) => {
      if (en.isIntersecting) { en.target.classList.add('in'); io.unobserve(en.target); if (en.target.id === 'bars') return; }
    }), { threshold: 0.15 });
    items.forEach((el) => io.observe(el));
  }

  function countUp() {
    $$('[data-count]').forEach((el) => {
      const target = parseFloat(el.dataset.count), dec = el.dataset.format === 'dec1';
      const show = (v) => { el.textContent = dec ? v.toFixed(1) : Math.round(v).toLocaleString(); };
      if (reduceMotion) return;
      show(0);
      const t0 = performance.now() + 250, dur = 1400;
      const tick = (now) => {
        const p = Math.min(Math.max((now - t0) / dur, 0), 1);
        show(target * (1 - Math.pow(1 - p, 4)));
        if (p < 1) requestAnimationFrame(tick);
      };
      requestAnimationFrame(tick);
    });
  }

  function initTicker() {
    const words = ['In-process LRU', 'Redis cache', 'Postgres sequence', 'Block ID allocation', 'Async click analytics', 'Distributed rate limiting', 'Prometheus metrics', 'Testcontainers', 'k6 load tested'];
    const track = $('#ticker-track');
    for (let i = 0; i < 2; i++) words.forEach((w) => track.append(h('span', {}, w)));
  }

  // Animated redirect path: a packet walks down the cache layers and stops
  // at the first one that has the link.
  function initFlow() {
    const nodes = $$('#flow-row .node'), packet = $('#packet'), cap = $('#flow-caption');
    const centers = [12.5, 37.5, 62.5, 87.5];
    const scenes = [
      { stop: 1, label: 'LRU hit', text: 'Served from process memory: microseconds, no network hop.' },
      { stop: 2, label: 'Redis hit', text: 'One sub-millisecond hop, and the local cache is warmed for next time.' },
      { stop: 3, label: 'Cold link', text: 'Postgres answers once, then both caches remember it.' },
    ];
    const say = (sc) => { cap.replaceChildren(h('b', {}, sc.label), sc.text); };
    const reset = () => nodes.forEach((n) => n.classList.remove('seen', 'miss', 'hit'));
    const wait = (ms) => new Promise((r) => setTimeout(r, ms));
    const mark = (sc, upTo) => nodes.forEach((n, i) => {
      n.classList.toggle('seen', i <= upTo);
      n.classList.toggle('miss', i > 0 && i < sc.stop && i <= upTo && i < sc.stop);
      n.classList.toggle('hit', i === sc.stop && i <= upTo);
    });
    if (reduceMotion) { const sc = scenes[1]; mark(sc, sc.stop); packet.style.left = centers[sc.stop] + '%'; say(sc); return; }
    let running = false;
    async function loop() {
      if (running) return; running = true;
      for (let k = 0; ; k = (k + 1) % scenes.length) {
        const sc = scenes[k];
        reset(); packet.style.left = centers[0] + '%'; say({ label: 'Request', text: 'GET /aB3xY arrives.' });
        await wait(700);
        for (let i = 0; i <= sc.stop; i++) { packet.style.left = centers[i] + '%'; mark(sc, i); if (i) await wait(650); }
        say(sc);
        await wait(2600);
      }
    }
    new IntersectionObserver((en, io) => { if (en[0].isIntersecting) { loop(); io.disconnect(); } }, { threshold: 0.4 }).observe($('#flow'));
  }

  function initBars() {
    const bars = $('#bars');
    if (reduceMotion || !('IntersectionObserver' in window)) { bars.classList.add('in'); return; }
    new IntersectionObserver((en, io) => { if (en[0].isIntersecting) { bars.classList.add('in'); io.disconnect(); } }, { threshold: 0.4 }).observe(bars);
  }

  /* ---------------- dashboard ---------------- */
  let links = [], selected = null;

  const ago = (iso) => {
    const d = (Date.now() - new Date(iso)) / 1000;
    if (d < 60) return 'just now';
    if (d < 3600) return Math.floor(d / 60) + 'm ago';
    if (d < 86400) return Math.floor(d / 3600) + 'h ago';
    return Math.floor(d / 86400) + 'd ago';
  };
  function expiryText(iso) {
    if (!iso) return null;
    const d = (new Date(iso) - Date.now()) / 1000;
    if (d <= 0) return { text: 'Expired', expired: true };
    if (d < 3600) return { text: 'Expires in ' + Math.ceil(d / 60) + 'm' };
    if (d < 86400) return { text: 'Expires in ' + Math.floor(d / 3600) + 'h' };
    return { text: 'Expires in ' + Math.floor(d / 86400) + 'd' };
  }

  function tile(value, label) {
    return h('div', { class: 'metric' }, h('strong', {}, value), h('span', {}, label));
  }

  function renderDashboard() {
    const total = links.reduce((n, l) => n + l.clicks, 0);
    const live = links.filter((l) => !(l.expires_at && new Date(l.expires_at) <= Date.now())).length;
    $('#dash-stats').replaceChildren(tile(fmt.format(links.length), 'links'), tile(fmt.format(total), 'total clicks'), tile(fmt.format(live), 'active'));
    const list = $('#links-list');
    list.replaceChildren();
    if (!links.length) { list.append(h('div', { class: 'empty' }, 'No links yet. Create your first one above.')); return; }
    for (const l of links) {
      const exp = expiryText(l.expires_at);
      let armed = false;
      const del = h('button', { class: 'btn ghost sm', type: 'button' }, 'Delete');
      del.addEventListener('click', async () => {
        if (!armed) { armed = true; del.textContent = 'Confirm?'; del.classList.add('danger'); setTimeout(() => { armed = false; del.textContent = 'Delete'; del.classList.remove('danger'); }, 3000); return; }
        try {
          await api('/me/links/' + encodeURIComponent(l.code), { method: 'DELETE' });
          links = links.filter((x) => x.code !== l.code);
          if (selected === l.code) closeAnalytics();
          renderDashboard(); toast('Link deleted');
        } catch (ex) { toast(ex.message); }
      });
      list.append(h('div', { class: 'row' + (selected === l.code ? ' active' : '') },
        h('a', { class: 'short', href: l.short_url, target: '_blank', rel: 'noopener' }, l.short_url.replace(/^https?:\/\//, '')),
        h('div', { class: 'long', title: l.long_url }, l.long_url),
        h('div', { class: 'clicks' }, fmt.format(l.clicks), h('small', {}, 'clicks')),
        h('div', { class: 'when' }, h('b', {}, ago(l.created_at)), exp ? h('span', { class: exp.expired ? 'expired' : '' }, exp.text) : 'No expiry'),
        h('div', { class: 'row-actions' },
          h('button', { class: 'btn ghost sm', type: 'button', onclick: () => openAnalytics(l.code) }, 'Analytics'),
          h('button', { class: 'btn ghost sm', type: 'button', onclick: () => copy(l.short_url) }, 'Copy'),
          del),
      ));
    }
  }

  async function loadLinks() {
    try { links = await api('/me/links'); renderDashboard(); }
    catch (ex) { if (ex.status === 401) { user = null; renderAuthSlot(); location.hash = '#/'; } else toast(ex.message); }
  }

  $('#dash-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const err = $('#dash-error');
    err.hidden = true;
    const body = { url: $('#d-url').value };
    const alias = $('#d-alias').value.trim();
    if (alias) body.custom_code = alias;
    const exp = parseInt($('#d-exp').value, 10);
    if (exp) body.expires_in_seconds = exp;
    try {
      const r = await api('/links', { method: 'POST', body });
      e.target.reset();
      toast('Created ' + r.short_url.replace(/^https?:\/\//, ''));
      await loadLinks();
    } catch (ex) {
      if (ex.status === 401) { user = null; renderAuthSlot(); openAuth('login'); return; }
      err.textContent = ex.message; err.hidden = false;
    }
  });

  function closeAnalytics() { selected = null; $('#analytics-panel').hidden = true; renderDashboard(); }
  $('#an-close').addEventListener('click', closeAnalytics);

  function hbars(title, rows) {
    const max = Math.max(1, ...rows.map((r) => r.count));
    return h('div', {}, h('h3', {}, title),
      rows.length ? rows.map((r) => h('div', { class: 'hbar' },
        h('div', { class: 'hbar-top' }, h('span', {}, r.label), h('span', {}, fmt.format(r.count))),
        h('div', { class: 'hbar-track' }, h('i', { style: 'width:' + Math.max(4, (r.count / max) * 100) + '%' })),
      )) : h('p', { class: 'none' }, 'No data yet'));
  }

  function drawChart(daily) {
    const W = 720, H = 240, pl = 40, pr = 12, pt = 14, pb = 28;
    const max = Math.max(4, ...daily.map((d) => d.clicks));
    const niceMax = Math.ceil(max / 4) * 4;
    const x = (i) => pl + (i * (W - pl - pr)) / Math.max(1, daily.length - 1);
    const y = (v) => pt + (1 - v / niceMax) * (H - pt - pb);
    const svg = s('svg', { viewBox: `0 0 ${W} ${H}`, role: 'img', 'aria-label': `Clicks per day over the last ${daily.length} days, ${daily.reduce((n, d) => n + d.clicks, 0)} in total` });
    const defs = s('defs'); const grad = s('linearGradient', { id: 'areaGrad', x1: 0, y1: 0, x2: 0, y2: 1 });
    grad.append(s('stop', { offset: '0%', 'stop-color': 'var(--accent)', 'stop-opacity': '.32' }), s('stop', { offset: '100%', 'stop-color': 'var(--accent)', 'stop-opacity': '0' }));
    defs.append(grad); svg.append(defs);
    for (let g = 0; g <= 4; g++) {
      const v = (niceMax / 4) * g;
      svg.append(s('line', { class: 'grid-line', x1: pl, x2: W - pr, y1: y(v), y2: y(v) }));
      const t = s('text', { class: 'axis', x: pl - 8, y: y(v) + 4, 'text-anchor': 'end' }); t.textContent = Math.round(v); svg.append(t);
    }
    [0, Math.floor(daily.length / 2), daily.length - 1].forEach((i) => {
      const t = s('text', { class: 'axis', x: x(i), y: H - 6, 'text-anchor': i === 0 ? 'start' : i === daily.length - 1 ? 'end' : 'middle' });
      t.textContent = new Date(daily[i].date + 'T00:00:00Z').toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: 'UTC' });
      svg.append(t);
    });
    const pts = daily.map((d, i) => [x(i), y(d.clicks)]);
    const line = pts.map((p, i) => (i ? 'L' : 'M') + p[0].toFixed(1) + ' ' + p[1].toFixed(1)).join(' ');
    svg.append(s('path', { class: 'area', d: line + ` L${x(daily.length - 1)} ${y(0)} L${x(0)} ${y(0)} Z` }), s('path', { class: 'line', d: line }));
    const dot = s('circle', { class: 'dot', r: 6, cx: -20, cy: -20 }); svg.append(dot);
    const box = $('#chart'); box.replaceChildren(svg);
    const tip = h('div', { class: 'tip', hidden: true }); box.append(tip);
    svg.addEventListener('pointermove', (ev) => {
      const r = svg.getBoundingClientRect();
      const vx = ((ev.clientX - r.left) / r.width) * W;
      const i = Math.min(daily.length - 1, Math.max(0, Math.round(((vx - pl) / (W - pl - pr)) * (daily.length - 1))));
      dot.setAttribute('cx', pts[i][0]); dot.setAttribute('cy', pts[i][1]);
      tip.replaceChildren(h('b', {}, fmt.format(daily[i].clicks)), ' clicks · ', new Date(daily[i].date + 'T00:00:00Z').toLocaleDateString(undefined, { month: 'short', day: 'numeric', timeZone: 'UTC' }));
      tip.style.left = (pts[i][0] / W) * r.width + 'px'; tip.style.top = (pts[i][1] / H) * r.height + 'px'; tip.hidden = false;
    });
    svg.addEventListener('pointerleave', () => { tip.hidden = true; dot.setAttribute('cx', -20); });
  }

  async function openAnalytics(code) {
    selected = code; renderDashboard();
    const panel = $('#analytics-panel'); panel.hidden = false;
    $('#an-tiles').replaceChildren(); $('#chart').replaceChildren(h('p', { class: 'none' }, 'Loading…')); $('#an-cols').replaceChildren();
    try {
      const a = await api('/me/links/' + encodeURIComponent(code) + '/analytics');
      $('#an-sub').textContent = a.link.short_url.replace(/^https?:\/\//, '') + ' → ' + a.link.long_url + ' · last ' + a.days + ' days';
      $('#an-tiles').replaceChildren(tile(fmt.format(a.total), 'clicks'), tile(fmt.format(a.uniques), 'unique visitors'));
      drawChart(a.daily);
      $('#an-cols').replaceChildren(hbars('Browsers', a.browsers), hbars('Systems', a.systems), hbars('Referrers', a.referrers));
      panel.scrollIntoView({ behavior: reduceMotion ? 'auto' : 'smooth', block: 'nearest' });
    } catch (ex) { toast(ex.message); closeAnalytics(); }
  }

  /* ---------------- routing ---------------- */
  function route() {
    const dash = location.hash.startsWith('#/dashboard');
    if (dash && !user) { location.hash = '#/'; openAuth('login', () => { location.hash = '#/dashboard'; }); return; }
    $('#view-landing').hidden = dash;
    $('#view-dashboard').hidden = !dash;
    $('#nav-links').hidden = dash;
    if (dash) { window.scrollTo(0, 0); loadLinks(); }
  }
  addEventListener('hashchange', route);

  /* ---------------- boot ---------------- */
  (async () => {
    initTicker(); initReveal(); initFlow(); initBars(); countUp();
    try { const me = await api('/auth/me'); user = me.email ? me : null; } catch { user = null; }
    renderAuthSlot();
    route();
  })();
})();
