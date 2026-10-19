const $ = (s) => document.querySelector(s);
const el = (tag, attrs = {}, ...kids) => { const e = document.createElement(tag); for (const [k, v] of Object.entries(attrs)) k.startsWith('on') ? e.addEventListener(k.slice(2), v) : e.setAttribute(k, v); for (const k of kids) e.append(k); return e; };
const api = async (path, opts) => { const r = await fetch(path, opts); const b = await r.json().catch(() => ({})); if (!r.ok) throw new Error(b.error || r.statusText); return b; };
let current = null;

function watch(jobId, onEvent) {
  return new Promise((resolve, reject) => {
    const es = new EventSource(`/api/jobs/${jobId}/events`);
    const handle = (type) => (m) => { const e = JSON.parse(m.data); onEvent && onEvent(type, e); if (type === 'done') { es.close(); resolve(e.data); } if (type === 'error') { es.close(); reject(new Error(e.error)); } };
    ['stage', 'data', 'done', 'error'].forEach((t) => es.addEventListener(t, handle(t)));
  });
}

async function upload(file) {
  const st = $('#upstatus'); st.textContent = `Uploading ${file.name}…`;
  const fd = new FormData(); fd.append('file', file);
  try {
    const t0 = performance.now();
    const r = await api('/api/uploads', { method: 'POST', body: fd });
    const res = await watch(r.job.id, (t, e) => { if (t === 'stage') st.textContent = `Extracting: ${e.stage}`; });
    st.textContent = `Extracted in ${Math.round(performance.now() - t0)} ms${res.reused ? ' (cached)' : ''}`;
    await loadProfiles(); openProfile(res.profileId);
  } catch (e) { st.textContent = 'Error: ' + e.message; }
}

async function loadProfiles() {
  const ps = await api('/api/profiles'); const ul = $('#profiles'); ul.innerHTML = '';
  for (const p of ps) ul.append(el('li', { 'data-id': p.id, onclick: () => openProfile(p.id), class: p.id === current ? 'active' : '' }, p.name, el('small', {}, `${p.sourceType} · v${p.version}`)));
  if (!ps.length) ul.append(el('li', { class: 'muted' }, 'No profiles yet'));
}

async function openProfile(id) {
  current = id; loadProfiles();
  const { profile: p, version } = await api(`/api/profiles/${id}`);
  const m = $('#main'); m.innerHTML = '';
  m.append(el('div', { class: 'row' }, el('h2', { style: 'margin:0;font-size:20px;color:#1d1f27;text-transform:none' }, p.name), el('span', { class: 'pill' }, `v${version}`), el('span', { class: 'pill' }, `${p.aspect} · ${p.sourceType}`),
    el('a', { class: 'btn sec', href: `/api/profiles/${id}/components.xml`, target: '_blank' }, 'components.xml')));
  m.append(el('h3', {}, 'Colors'));
  const sw = el('div', { class: 'row' }); for (const c of p.tokens.colors) sw.append(el('div', { class: 'sw', title: c.value }, el('div', { style: `background:${c.value}` }), c.name)); m.append(sw);
  m.append(el('h3', {}, 'Typography'));
  m.append(el('div', {}, ...p.tokens.fonts.map((f) => el('div', {}, el('b', {}, f.role + ': '), `${f.family}`, el('span', { class: 'muted' }, f.fallback ? ` (fallback ${f.fallback})` : '')))));
  m.append(el('div', { class: 'muted' }, p.tokens.typeScale.steps.map((s) => `${s.name} ${s.size}pt`).join(' · ')));
  m.append(el('h3', {}, `Layouts (${p.layouts.length})`));
  const lg = el('div', { class: 'grid' });
  for (const l of p.layouts) lg.append(el('div', { class: 'card' }, el('img', { src: `/api/profiles/${id}/layouts/${l.id}/preview`, alt: l.name }), el('div', { class: 'cap' }, `${l.id} · ${l.name} · ${l.kind}`)));
  m.append(lg);
  m.append(el('h3', {}, 'Generate a deck in this style'));
  const prompt = el('textarea', { id: 'prompt', placeholder: 'e.g. Quarterly business review for a B2B SaaS company: growth, churn, pricing changes, roadmap' });
  const n = el('input', { type: 'number', id: 'nslides', value: '6', min: '1', max: '30' });
  const go = el('button', { id: 'generate' }, 'Generate');
  const out = el('div', { id: 'deck' });
  go.onclick = () => generate(id, prompt.value, +n.value, out, go);
  m.append(prompt, el('div', { class: 'row', style: 'margin-top:8px' }, el('label', {}, 'Slides '), n, go), out);
}

async function generate(profileId, prompt, slides, out, btn) {
  if (!prompt.trim()) { alert('Enter a prompt'); return; }
  btn.disabled = true; out.innerHTML = '';
  const status = el('p', { class: 'muted' }, 'Planning…'); const grid = el('div', { class: 'grid' }); out.append(status, grid);
  const t0 = performance.now(); let first = 0;
  try {
    const r = await api('/api/decks', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ profileId, prompt, slides }) });
    const res = await watch(r.job.id, (t, e) => {
      if (t !== 'data') return; const d = e.data;
      if (d.type === 'outline') { status.textContent = `“${d.title}” – writing ${d.total} slides…`; grid.innerHTML = ''; for (let i = 1; i <= d.total; i++) grid.append(el('div', { class: 'card slide pending', id: `s${i}` }, `Slide ${i}`)); }
      if (d.type === 'slide') { if (!first) first = performance.now() - t0; const c = $(`#s${d.index}`); c.className = 'card slide'; c.innerHTML = d.svg; }
    });
    // Fast (e.g. offline) jobs can finish before the event stream attaches: fill any missed slides.
    for (let i = 1; i <= res.slides; i++) {
      let c = grid.querySelector(`#s${i}`);
      if (!c) { c = el('div', { class: 'card slide pending', id: `s${i}` }); grid.append(c); }
      if (c.classList.contains('pending')) { c.innerHTML = await (await fetch(`/api/decks/${res.deckId}/slides/${i}`)).text(); c.className = 'card slide'; }
    }
    for (const c of grid.children) c.append(regenButton(res.deckId, +c.id.slice(1), c));
    const u = res.usage || {};
    status.innerHTML = '';
    status.append(`${res.slides} slides in ${Math.round(performance.now() - t0)} ms (first slide ${Math.round(first)} ms) · tokens in ${u.inputTokens || 0} / out ${u.outputTokens || 0} `,
      el('a', { class: 'btn', href: `/api/decks/${res.deckId}/export.pptx` }, 'Download PPTX'));
  } catch (e) { status.textContent = 'Error: ' + e.message; }
  btn.disabled = false;
}

function regenButton(deckId, n, card) {
  const b = el('button', { class: 'sec', style: 'margin-top:6px;padding:4px 10px;font-size:12px' }, '↻ Regenerate');
  b.onclick = async () => {
    const hint = prompt('Optional: what should change on this slide?', '') ?? null; if (hint === null) return;
    b.disabled = true; b.textContent = 'Regenerating…';
    try { const r = await api(`/api/decks/${deckId}/slides/${n}/regenerate`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ hint }) });
      card.querySelector('svg').outerHTML = r.svg; b.textContent = '↻ Regenerate'; } catch (e) { b.textContent = 'Error: ' + e.message; }
    b.disabled = false;
  };
  return b;
}

const drop = $('#drop');
$('#file').onchange = (e) => e.target.files[0] && upload(e.target.files[0]);
['dragover', 'dragenter'].forEach((t) => drop.addEventListener(t, (e) => { e.preventDefault(); drop.classList.add('over'); }));
['dragleave', 'drop'].forEach((t) => drop.addEventListener(t, (e) => { e.preventDefault(); drop.classList.remove('over'); }));
drop.addEventListener('drop', (e) => e.dataTransfer.files[0] && upload(e.dataTransfer.files[0]));
function showLogin(msg) {
  $('aside').classList.add('locked');
  $('#upstatus').textContent = ''; $('#profiles').innerHTML = '';
  const email = el('input', { type: 'email', id: 'email', placeholder: 'Email', autocomplete: 'username' });
  const pw = el('input', { type: 'password', id: 'password', placeholder: 'Password (8+ characters)', autocomplete: 'current-password' });
  const err = el('p', { class: 'muted', id: 'autherr' }, msg || '');
  const send = (path) => async (e) => {
    e.preventDefault();
    try { await api(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ email: email.value, password: pw.value }) }); start(); }
    catch (x) { err.textContent = x.message; }
  };
  const m = $('#main'); m.innerHTML = '';
  m.append(el('form', { class: 'auth', id: 'authform', onsubmit: send('/api/auth/login') }, el('h2', {}, 'Sign in'), email, pw,
    el('div', { class: 'row' }, el('button', { type: 'submit', id: 'login' }, 'Sign in'), el('button', { type: 'button', class: 'sec', id: 'signup', onclick: send('/api/auth/signup') }, 'Create account')), err));
}

async function start() {
  try {
    const { user } = await api('/api/auth/me');
    $('aside').classList.remove('locked');
    $('#account').innerHTML = '';
    $('#account').append(el('span', { class: 'muted' }, user.email), ' ', el('a', { href: '#', id: 'logout', onclick: async (e) => { e.preventDefault(); await api('/api/auth/logout', { method: 'POST' }); current = null; showLogin(); } }, 'Sign out'));
    $('#main').innerHTML = '<p class="muted">Upload a deck to extract its style, or pick a profile.</p>';
    loadProfiles();
  } catch { showLogin(); }
}
start();
