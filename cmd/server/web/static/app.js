(() => {
  'use strict';

  const $ = (s, r = document) => r.querySelector(s);
  const $$ = (s, r = document) => [...r.querySelectorAll(s)];
  const reduceMotion = matchMedia('(prefers-reduced-motion: reduce)').matches;
  const fmt = new Intl.NumberFormat('en');
  const bare = (u) => u.replace(/^https?:\/\//, '');
  const wait = (ms) => new Promise((r) => setTimeout(r, ms));

  // Builds DOM with textContent only, so user-controlled strings (URLs,
  // emails, referrers) are never parsed as HTML.
  function h(tag, props = {}, ...kids) {
    const n = document.createElement(tag);
    for (const [k, v] of Object.entries(props)) {
      if (v == null || v === false) continue;
      if (k === 'class') n.className = v;
      else if (k.startsWith('on')) n.addEventListener(k.slice(2), v);
      else n.setAttribute(k, v === true ? '' : v);
    }
    for (const kid of kids.flat()) if (kid != null && kid !== false) n.append(kid.nodeType ? kid : document.createTextNode(kid));
    return n;
  }
  const sep = () => h('span', { class: 'sep' }, '|');

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
      const e = new Error(data.error || 'something went wrong');
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
    toastTimer = setTimeout(() => t.classList.remove('show'), 2200);
  }
  async function copy(text) {
    try { await navigator.clipboard.writeText(text); toast('copied ' + bare(text)); }
    catch { toast('copy failed, select it manually'); }
  }

  /* ---------- hover preview (same card as sonofnos.com) ---------- */
  const preview = $('#link-preview');
  function showPreview(el, url) {
    let u;
    try { u = new URL(url); } catch { return; }
    $('#preview-favicon').src = 'https://www.google.com/s2/favicons?sz=32&domain=' + encodeURIComponent(u.hostname);
    $('#preview-title').textContent = u.hostname.replace(/^www\./, '');
    $('#preview-domain').textContent = bare(url);
    const r = el.getBoundingClientRect();
    const left = Math.max(12, Math.min(r.left + r.width / 2 - 150, innerWidth - 312));
    preview.style.left = left + 'px';
    preview.style.top = r.top - preview.offsetHeight - 8 + 'px';
    const caret = preview.querySelector('.preview-caret');
    caret.style.transform = `translateX(${Math.max(-130, Math.min(130, r.left + r.width / 2 - (left + 150)))}px)`;
    preview.classList.add('visible');
  }
  const hidePreview = () => preview.classList.remove('visible');
  function withPreview(el, url) {
    if (matchMedia('(hover: hover)').matches) {
      el.addEventListener('mouseenter', () => showPreview(el, url));
      el.addEventListener('mouseleave', hidePreview);
    }
    return el;
  }
  addEventListener('scroll', hidePreview, { passive: true });

  /* ---------- auth ---------- */
  let user = null;
  const dlg = $('#auth-dialog');
  let mode = 'login';
  let afterAuth = null;

  function setMode(m) {
    mode = m;
    const signup = m === 'signup';
    $('#tab-login').setAttribute('aria-pressed', String(!signup));
    $('#tab-signup').setAttribute('aria-pressed', String(signup));
    $('#auth-title').textContent = signup ? 'create account' : 'sign in';
    $('#auth-submit').textContent = signup ? 'create account' : 'sign in';
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
  const toDashboard = () => { location.hash = '#/dashboard'; };
  $('#tab-login').addEventListener('click', () => setMode('login'));
  $('#tab-signup').addEventListener('click', () => setMode('signup'));
  $('#auth-close').addEventListener('click', () => dlg.close());
  dlg.addEventListener('click', (e) => { if (e.target === dlg) dlg.close(); });
  $$('[data-open-auth]').forEach((b) => b.addEventListener('click', () => (user ? toDashboard() : openAuth(b.dataset.openAuth, toDashboard))));

  $('#auth-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const err = $('#auth-error'), btn = $('#auth-submit');
    err.hidden = true;
    btn.disabled = true;
    try {
      user = await api('/auth/' + mode, { method: 'POST', body: { email: $('#a-email').value, password: $('#a-pass').value } });
      $('#a-pass').value = '';
      dlg.close();
      renderAuth();
      toast(mode === 'signup' ? 'account created' : 'signed in');
      const next = afterAuth; afterAuth = null;
      next ? next() : route();
    } catch (ex) {
      err.textContent = ex.message;
      err.hidden = false;
    } finally { btn.disabled = false; }
  });

  async function signOut() {
    try { await api('/auth/logout', { method: 'POST' }); } catch {}
    user = null; renderAuth(); toast('signed out'); location.hash = '#/';
  }
  $('#signout').addEventListener('click', signOut);

  function renderAuth() {
    const slot = $('#auth-slot');
    slot.replaceChildren(user
      ? h('span', {}, h('a', { href: '#/dashboard' }, 'dashboard'), sep(), h('button', { type: 'button', class: 'link', onclick: signOut }, 'sign out'))
      : h('button', { type: 'button', class: 'link', onclick: () => openAuth('login', toDashboard) }, 'sign in'));
    $('#account-note').hidden = !!user;
    $('#who').textContent = user ? user.email : '';
  }

  /* ---------- landing: shorten ---------- */
  $('#shorten-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const err = $('#shorten-error'), btn = $('#shorten-btn');
    err.hidden = true;
    btn.disabled = true;
    try {
      const r = await api('/links', { method: 'POST', body: { url: $('#url').value } });
      const a = $('#result-link');
      a.textContent = bare(r.short_url);
      a.href = r.short_url;
      $('#result').hidden = false;
      $('#copy-result').onclick = () => copy(r.short_url);
    } catch (ex) {
      $('#result').hidden = true;
      err.textContent = ex.message;
      err.hidden = false;
    } finally { btn.disabled = false; }
  });

  /* ---------- landing: motion ---------- */
  function onVisible(el, fn, threshold = 0.35) {
    if (reduceMotion || !('IntersectionObserver' in window)) return fn(true);
    new IntersectionObserver((en, io) => { if (en[0].isIntersecting) { io.disconnect(); fn(false); } }, { threshold }).observe(el);
  }

  // A request typed out like a terminal session, cycling through the three
  // places a redirect can be answered from.
  const scenes = [
    [['$ curl -I snaplink.sonofnos.com/aB3xY', ''], ['  lru      ', 'dim'], ['hit      0.004 ms\n', ''], ['HTTP/2 302  location: https://example.com/launch', '']],
    [['$ curl -I snaplink.sonofnos.com/q7Kp2', ''], ['  lru      ', 'dim'], ['miss\n', 'dim'], ['  redis    ', 'dim'], ['hit      0.31 ms   (lru warmed)\n', ''], ['HTTP/2 302  location: https://example.com/docs', '']],
    [['$ curl -I snaplink.sonofnos.com/Zr90d', ''], ['  lru      ', 'dim'], ['miss\n', 'dim'], ['  redis    ', 'dim'], ['miss\n', 'dim'], ['  postgres ', 'dim'], ['found    1.8 ms    (both caches warmed)\n', ''], ['HTTP/2 302  location: https://example.com/archive/2019', '']],
  ];
  function initTrace() {
    const pre = $('#trace');
    const paint = (parts, typed, cursor) => {
      pre.replaceChildren();
      let left = typed;
      for (const [text, cls] of parts) {
        if (left <= 0) break;
        const t = text.slice(0, left);
        left -= text.length;
        pre.append(cls ? h('span', { class: cls }, t) : t);
        if (text.startsWith('$') && left >= 0 && t === text) pre.append('\n');
      }
      if (cursor) pre.append(h('span', { class: 'cursor' }, ' '));
    };
    if (reduceMotion) { const s = scenes[1]; paint(s, 1e6, false); return; }
    onVisible(pre, async () => {
      for (let k = 0; ; k = (k + 1) % scenes.length) {
        const s = scenes[k];
        const total = s.reduce((n, [t]) => n + t.length, 0);
        const cmdLen = s[0][0].length;
        for (let i = 0; i <= cmdLen; i += 2) { paint(s, i, true); await wait(22); }
        await wait(350);
        for (let i = cmdLen; i <= total; i += 4) { paint(s, i, true); await wait(16); }
        paint(s, total, true);
        await wait(2800);
      }
    });
  }

  function initNumbers() {
    const nums = $$('[data-count]');
    const bars = $$('#bars .bt');
    const W = 24;
    const fillBar = (el, n) => { el.replaceChildren('█'.repeat(n), h('s', {}, '░'.repeat(W - n))); };
    onVisible($('#numbers'), (instant) => {
      if (instant) { bars.forEach((b) => fillBar(b, +b.dataset.fill)); return; }
      const t0 = performance.now(), dur = 1200;
      const tick = (now) => {
        const p = Math.min((now - t0) / dur, 1), e = 1 - Math.pow(1 - p, 3);
        nums.forEach((el) => { const v = +el.dataset.count * e; el.textContent = el.dataset.dec ? v.toFixed(1) : fmt.format(Math.round(v)); });
        bars.forEach((b) => fillBar(b, Math.round(+b.dataset.fill * e) || (p > 0.05 ? Math.min(1, +b.dataset.fill) : 0)));
        if (p < 1) requestAnimationFrame(tick);
      };
      requestAnimationFrame(tick);
    });
  }

  /* ---------- dashboard ---------- */
  let links = [], selected = null;

  const ago = (iso) => {
    const d = (Date.now() - new Date(iso)) / 1000;
    if (d < 60) return 'just now';
    if (d < 3600) return Math.floor(d / 60) + 'm ago';
    if (d < 86400) return Math.floor(d / 3600) + 'h ago';
    return Math.floor(d / 86400) + 'd ago';
  };
  function expiry(iso) {
    if (!iso) return null;
    const d = (new Date(iso) - Date.now()) / 1000;
    if (d <= 0) return { text: 'expired', expired: true };
    if (d < 3600) return { text: 'expires in ' + Math.ceil(d / 60) + 'm' };
    if (d < 86400) return { text: 'expires in ' + Math.floor(d / 3600) + 'h' };
    return { text: 'expires in ' + Math.floor(d / 86400) + 'd' };
  }

  function renderDashboard() {
    const total = links.reduce((n, l) => n + l.clicks, 0);
    const live = links.filter((l) => !(l.expires_at && new Date(l.expires_at) <= Date.now())).length;
    const plural = (n, w) => fmt.format(n) + ' ' + w + (n === 1 ? '' : 's');
    $('#dash-summary').textContent = [plural(links.length, 'link'), plural(total, 'click'), fmt.format(live) + ' active'].join(' · ');

    const list = $('#links-list');
    list.replaceChildren();
    if (!links.length) { list.append(h('p', { class: 'empty' }, 'no links yet. create one above.')); return; }
    for (const l of links) {
      const exp = expiry(l.expires_at);
      let armed = false;
      const del = h('button', { type: 'button', class: 'link' }, 'delete');
      del.addEventListener('click', async () => {
        if (!armed) {
          armed = true; del.textContent = 'confirm delete?'; del.classList.add('danger');
          setTimeout(() => { armed = false; del.textContent = 'delete'; del.classList.remove('danger'); }, 3000);
          return;
        }
        try {
          await api('/me/links/' + encodeURIComponent(l.code), { method: 'DELETE' });
          links = links.filter((x) => x.code !== l.code);
          if (selected === l.code) closeAnalytics();
          renderDashboard(); toast('deleted /' + l.code);
        } catch (ex) { toast(ex.message); }
      });
      list.append(h('div', { class: 'item' + (selected === l.code ? ' active' : '') },
        h('div', { class: 'item-top' },
          h('a', { class: 'short', href: l.short_url, target: '_blank', rel: 'noopener' }, bare(l.short_url)),
          h('span', { class: 'clicks' }, fmt.format(l.clicks) + (l.clicks === 1 ? ' click' : ' clicks'))),
        withPreview(h('a', { class: 'dest', href: l.long_url, target: '_blank', rel: 'noopener noreferrer' }, '→ ' + l.long_url), l.long_url),
        h('div', { class: 'meta' },
          h('span', {}, ago(l.created_at)), h('span', { class: 'sep' }, '·'),
          h('span', { class: exp && exp.expired ? 'expired' : '' }, exp ? exp.text : 'no expiry'), sep(),
          h('button', { type: 'button', class: 'link', onclick: () => openAnalytics(l.code) }, 'analytics'), sep(),
          h('button', { type: 'button', class: 'link', onclick: () => copy(l.short_url) }, 'copy'), sep(),
          del),
      ));
    }
  }

  async function loadLinks() {
    try { links = await api('/me/links'); renderDashboard(); }
    catch (ex) { if (ex.status === 401) { user = null; renderAuth(); location.hash = '#/'; } else toast(ex.message); }
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
      toast('created ' + bare(r.short_url));
      await loadLinks();
    } catch (ex) {
      if (ex.status === 401) { user = null; renderAuth(); openAuth('login'); return; }
      err.textContent = ex.message; err.hidden = false;
    }
  });

  function closeAnalytics() { selected = null; $('#analytics-panel').hidden = true; renderDashboard(); }
  $('#an-close').addEventListener('click', closeAnalytics);

  const day = (iso) => new Date(iso + 'T00:00:00Z').toLocaleDateString('en', { month: 'short', day: 'numeric', timeZone: 'UTC' }).toLowerCase();

  function drawChart(daily) {
    const max = Math.max(1, ...daily.map((d) => d.clicks));
    const readout = h('p', { class: 'readout' }, 'hover a day');
    const cols = h('div', { class: 'cols', role: 'img', 'aria-label': `clicks per day, last ${daily.length} days, ${daily.reduce((n, d) => n + d.clicks, 0)} total` });
    daily.forEach((d, i) => {
      const bar = h('i', { class: d.clicks ? '' : 'zero', style: `height:${d.clicks ? Math.max(3, (d.clicks / max) * 100) : 1}%;animation-delay:${i * 18}ms` });
      bar.addEventListener('mouseenter', () => { readout.textContent = `${day(d.date)} — ${fmt.format(d.clicks)} click${d.clicks === 1 ? '' : 's'}`; });
      cols.append(bar);
    });
    cols.addEventListener('mouseleave', () => { readout.textContent = 'hover a day'; });
    const axis = h('div', { class: 'axis' }, h('span', {}, day(daily[0].date)), h('span', {}, day(daily[daily.length - 1].date)));
    $('#chart').replaceChildren(cols, axis, readout);
  }

  function breakdown(title, rows) {
    const max = Math.max(1, ...rows.map((r) => r.count));
    const width = 16;
    return h('div', { class: 'breakdown' }, h('h3', {}, title),
      rows.length ? rows.map((r) => {
        const n = Math.max(1, Math.round((r.count / max) * width));
        return h('div', { class: 'hb' }, h('span', { class: 'n', title: r.label }, r.label.toLowerCase()),
          h('span', { class: 'b' }, '█'.repeat(n), h('s', {}, '░'.repeat(width - n))),
          h('span', { class: 'c' }, fmt.format(r.count)));
      }) : h('p', { class: 'muted small' }, 'no data yet'));
  }

  async function openAnalytics(code) {
    selected = code; renderDashboard();
    const panel = $('#analytics-panel'); panel.hidden = false;
    $('#an-h').textContent = 'analytics /' + code;
    $('#an-sub').textContent = 'loading…';
    $('#chart').replaceChildren(); $('#an-cols').replaceChildren();
    try {
      const a = await api('/me/links/' + encodeURIComponent(code) + '/analytics');
      $('#an-sub').textContent = `${fmt.format(a.total)} click${a.total === 1 ? '' : 's'} · ${fmt.format(a.uniques)} unique visitor${a.uniques === 1 ? '' : 's'} · last ${a.days} days`;
      drawChart(a.daily);
      $('#an-cols').replaceChildren(breakdown('browsers', a.browsers), breakdown('systems', a.systems), breakdown('referrers', a.referrers));
      panel.scrollIntoView({ behavior: reduceMotion ? 'auto' : 'smooth', block: 'start' });
    } catch (ex) { toast(ex.message); closeAnalytics(); }
  }

  /* ---------- routing ---------- */
  function route() {
    const dash = location.hash.startsWith('#/dashboard');
    if (dash && !user) { location.hash = '#/'; openAuth('login', toDashboard); return; }
    hidePreview();
    $('#view-landing').hidden = dash;
    $('#view-dashboard').hidden = !dash;
    if (dash) { scrollTo(0, 0); loadLinks(); }
    document.title = dash ? 'your links · Snaplink' : 'Snaplink';
  }
  addEventListener('hashchange', route);

  /* ---------- boot ---------- */
  (async () => {
    const landing = $('#view-landing');
    if (!reduceMotion) {
      landing.classList.add('boot');
      [...landing.children].forEach((el, i) => { el.style.animationDelay = Math.min(i, 8) * 60 + 'ms'; });
      // Drop the class once the entrance has played, otherwise elements that
      // are unhidden later (the result line) would fade in again.
      setTimeout(() => landing.classList.remove('boot'), 1100);
    }
    initTrace(); initNumbers();
    try { const me = await api('/auth/me'); user = me.email ? me : null; } catch { user = null; }
    renderAuth();
    route();
  })();
})();
