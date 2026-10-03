/* ==========================================================================
 * QZRS-webdav-backup 控制台
 * 原生 JavaScript 单页应用，无外部依赖、无构建步骤。
 * 通过 go:embed 打包进服务端二进制。
 * ========================================================================== */
'use strict';

/* ------------------------------------------------------------------ 状态 */

const S = {
  me: null,
  status: null,
  settings: null,
  profiles: [],
  jobs: [],
  runs: [],
  archives: [],
  route: { name: 'dashboard', id: null },
  booted: false,
  pollTimer: null,
  polling: false,
  logOffset: 0,
  logRunID: null,
};

/* ------------------------------------------------------------------ 图标 */

const ICONS = {
  dash: '<path d="M3 3h7v7H3zM14 3h7v4h-7zM14 10h7v11h-7zM3 14h7v7H3z"/>',
  jobs: '<path d="M4 6h16M4 12h16M4 18h10"/>',
  store: '<path d="M21 8V19a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8M1 4h22v4H1zM10 12h4"/>',
  history: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
  cloud: '<path d="M18 17H7A4 4 0 0 1 6.2 9.1 6 6 0 0 1 18 8.5a4.5 4.5 0 0 1 0 8.5z"/>',
  gear: '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.6 1.6 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.6 1.6 0 0 0-2.7 1.1V21a2 2 0 1 1-4 0v-.1A1.6 1.6 0 0 0 7.5 19.4l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1A1.6 1.6 0 0 0 3.6 14H3a2 2 0 1 1 0-4h.1A1.6 1.6 0 0 0 4.6 7.5l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1A1.6 1.6 0 0 0 10 3.6V3a2 2 0 1 1 4 0v.1a1.6 1.6 0 0 0 2.5 1.5l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.6 1.6 0 0 0 1.1 2.7H21a2 2 0 1 1 0 4h-.1a1.6 1.6 0 0 0-1.5 1z"/>',
  play: '<path d="M7 4l12 8-12 8z"/>',
  stop: '<rect x="6" y="6" width="12" height="12" rx="1.5"/>',
  plus: '<path d="M12 5v14M5 12h14"/>',
  trash: '<path d="M3 6h18M8 6V4h8v2M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>',
  edit: '<path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.1 2.1 0 0 1 3 3L12 15l-4 1 1-4z"/>',
  refresh: '<path d="M21 12a9 9 0 1 1-2.6-6.4M21 3v6h-6"/>',
  check: '<path d="M20 6L9 17l-5-5"/>',
  x: '<path d="M18 6L6 18M6 6l12 12"/>',
  folder: '<path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z"/>',
  file: '<path d="M13 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z"/><path d="M13 2v7h7"/>',
  up: '<path d="M12 19V5M5 12l7-7 7 7"/>',
  download: '<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M7 10l5 5 5-5M12 15V3"/>',
  logout: '<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9"/>',
  search: '<circle cx="11" cy="11" r="8"/><path d="M21 21l-4.3-4.3"/>',
  alert: '<path d="M12 9v4M12 17h.01M10.3 3.9L1.8 18a2 2 0 0 0 1.7 3h17a2 2 0 0 0 1.7-3L13.7 3.9a2 2 0 0 0-3.4 0z"/>',
  info: '<circle cx="12" cy="12" r="10"/><path d="M12 16v-4M12 8h.01"/>',
  shield: '<path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>',
  clock: '<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>',
  hdd: '<path d="M21 12H3M6 16h.01M10 16h.01M5 4h14a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2z"/>',
  key: '<circle cx="8" cy="15" r="4"/><path d="M10.9 12.1L21 2M18 5l3 3M15 8l2 2"/>',
  link: '<path d="M10 13a5 5 0 0 0 7 0l3-3a5 5 0 0 0-7-7l-1 1"/><path d="M14 11a5 5 0 0 0-7 0l-3 3a5 5 0 0 0 7 7l1-1"/>',
};

function icon(name, size = 15) {
  const body = ICONS[name] || '';
  return `<svg width="${size}" height="${size}" viewBox="0 0 24 24" fill="none"
    stroke="currentColor" stroke-width="1.9" stroke-linecap="round" stroke-linejoin="round"
    aria-hidden="true">${body}</svg>`;
}

/* ------------------------------------------------------------------ 工具 */

const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

function esc(s) {
  if (s === null || s === undefined) return '';
  return String(s)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}

function fmtBytes(n) {
  if (n === null || n === undefined || isNaN(n)) return '—';
  if (n < 1024) return n + ' B';
  const units = ['KB', 'MB', 'GB', 'TB', 'PB'];
  let v = n / 1024, i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return v.toFixed(v >= 100 ? 0 : v >= 10 ? 1 : 2) + ' ' + units[i];
}

function fmtDuration(ms) {
  if (!ms && ms !== 0) return '—';
  const s = Math.round(ms / 1000);
  if (s < 60) return s + ' 秒';
  const m = Math.floor(s / 60), rs = s % 60;
  if (m < 60) return `${m} 分 ${rs} 秒`;
  const h = Math.floor(m / 60);
  return `${h} 时 ${m % 60} 分`;
}

function fmtTime(iso) {
  if (!iso) return '—';
  const d = new Date(iso);
  if (isNaN(d.getTime())) return '—';
  const now = new Date();
  const sameDay = d.toDateString() === now.toDateString();
  const pad = (x) => String(x).padStart(2, '0');
  const hm = `${pad(d.getHours())}:${pad(d.getMinutes())}`;
  if (sameDay) return '今天 ' + hm;
  const y = new Date(now.getTime() - 86400000);
  if (d.toDateString() === y.toDateString()) return '昨天 ' + hm;
  if (d.getFullYear() === now.getFullYear()) return `${d.getMonth() + 1}月${d.getDate()}日 ${hm}`;
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${hm}`;
}

function fmtTimeFull(iso) {
  if (!iso) return '—';
  const d = new Date(iso);
  if (isNaN(d.getTime())) return '—';
  const pad = (x) => String(x).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ` +
         `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

function relTime(iso) {
  if (!iso) return '—';
  const d = new Date(iso);
  if (isNaN(d.getTime())) return '—';
  const diff = Date.now() - d.getTime();
  const abs = Math.abs(diff);
  const suffix = diff >= 0 ? '前' : '后';
  if (abs < 60000) return '刚刚';
  if (abs < 3600000) return Math.floor(abs / 60000) + ' 分钟' + suffix;
  if (abs < 86400000) return Math.floor(abs / 3600000) + ' 小时' + suffix;
  return Math.floor(abs / 86400000) + ' 天' + suffix;
}

function fmtUptime(sec) {
  if (sec === null || sec === undefined) return '—';
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  if (d) return `${d} 天 ${h} 小时`;
  if (h) return `${h} 小时 ${m} 分`;
  return `${m} 分钟`;
}

/* ------------------------------------------------------------------ API */

async function api(path, { method = 'GET', body = null, raw = false } = {}) {
  const opts = {
    method,
    headers: { 'X-Requested-With': 'qzrs-webdav-backup' },
    credentials: 'same-origin',
  };
  if (body !== null) {
    opts.headers['Content-Type'] = 'application/json';
    opts.body = JSON.stringify(body);
  }

  let res;
  try {
    res = await fetch(path, opts);
  } catch (err) {
    throw new Error('无法连接服务：' + err.message);
  }

  if (res.status === 401) {
    if (!raw) { S.me = null; renderLogin(); }
    throw Object.assign(new Error('未登录或登录已过期'), { code: 401 });
  }

  let data = null;
  const text = await res.text();
  if (text) {
    try { data = JSON.parse(text); } catch { data = { message: text }; }
  }

  if (!res.ok) {
    const msg = (data && (data.message || data.error)) || `HTTP ${res.status}`;
    throw Object.assign(new Error(msg), { code: res.status, data });
  }
  return data;
}

/* ------------------------------------------------------------------ Toast */

function toast(message, kind = 'info', title = '') {
  const root = $('#toasts');
  const el = document.createElement('div');
  el.className = `toast toast-${kind}`;
  const ic = kind === 'error' ? 'alert' : kind === 'success' ? 'check'
           : kind === 'warning' ? 'alert' : 'info';
  el.innerHTML =
    `<span style="margin-top:1px;color:var(--${kind === 'info' ? 'primary' : kind})">${icon(ic, 16)}</span>` +
    `<div style="min-width:0">${title ? `<div class="toast-title">${esc(title)}</div>` : ''}` +
    `<div class="toast-msg">${esc(message)}</div></div>`;
  root.appendChild(el);

  const kill = () => {
    el.classList.add('leaving');
    setTimeout(() => el.remove(), 170);
  };
  el.addEventListener('click', kill);
  setTimeout(kill, kind === 'error' ? 9000 : 4200);
}

/* ------------------------------------------------------------------ 对话框 */

let modalStack = [];

function openModal({ title, subtitle = '', body, footer = '', width = '' , onMount }) {
  const root = $('#modal-root');
  const backdrop = document.createElement('div');
  backdrop.className = 'modal-backdrop';
  backdrop.innerHTML =
    `<div class="modal ${width}" role="dialog" aria-modal="true">
       <div class="modal-head">
         <div style="min-width:0">
           <h3>${esc(title)}</h3>
           ${subtitle ? `<p class="sub">${esc(subtitle)}</p>` : ''}
         </div>
         <button class="btn btn-ghost btn-icon modal-close" aria-label="关闭">${icon('x', 16)}</button>
       </div>
       <div class="modal-body">${body}</div>
       ${footer ? `<div class="modal-foot">${footer}</div>` : ''}
     </div>`;

  const close = () => {
    backdrop.remove();
    modalStack = modalStack.filter((m) => m !== close);
    if (!modalStack.length) document.body.style.overflow = '';
  };

  backdrop.addEventListener('mousedown', (e) => {
    if (e.target === backdrop) close();
  });
  $('.modal-close', backdrop).addEventListener('click', close);

  root.appendChild(backdrop);
  document.body.style.overflow = 'hidden';
  modalStack.push(close);

  const api = { el: backdrop, close, $: (s) => $(s, backdrop) };
  if (onMount) onMount(api);

  const firstInput = $('input:not([type=hidden]), select, textarea', backdrop);
  if (firstInput) setTimeout(() => firstInput.focus(), 40);

  return api;
}

document.addEventListener('keydown', (e) => {
  if (e.key === 'Escape' && modalStack.length) {
    modalStack[modalStack.length - 1]();
  }
});

async function confirmModal({ title, message, confirmText = '确认', danger = false, detail = '' }) {
  return new Promise((resolve) => {
    let settled = false;
    const done = (v) => { if (!settled) { settled = true; resolve(v); } };

    const m = openModal({
      title,
      width: 'narrow',
      body: `${detail ? `<div class="banner ${danger ? 'banner-danger' : 'banner-info'}" style="margin-bottom:14px">${icon(danger ? 'alert' : 'info', 16)}<div>${detail}</div></div>` : ''}
             <p style="margin:0;line-height:1.6">${esc(message)}</p>`,
      footer: `<button class="btn" data-act="cancel">取消</button>
               <button class="btn ${danger ? 'btn-danger' : 'btn-primary'}" data-act="ok">${esc(confirmText)}</button>`,
      onMount({ el, close }) {
        $('[data-act=cancel]', el).addEventListener('click', () => { done(false); close(); });
        $('[data-act=ok]', el).addEventListener('click', () => { done(true); close(); });
        el.addEventListener('mousedown', (e) => { if (e.target === el) done(false); });
      },
    });
    const origClose = m.close;
    m.close = () => { done(false); origClose(); };
  });
}

/* ------------------------------------------------------------------ 路由 */

const ROUTES = [
  { hash: '#/', name: 'dashboard', title: '概览' },
  { hash: '#/jobs', name: 'jobs', title: '备份任务' },
  { hash: '#/archives', name: 'archives', title: '备份仓库' },
  { hash: '#/runs', name: 'runs', title: '执行历史' },
  { hash: '#/profiles', name: 'profiles', title: 'WebDAV 配置' },
  { hash: '#/settings', name: 'settings', title: '设置' },
];

function parseHash() {
  const h = location.hash || '#/';
  const parts = h.replace(/^#\/?/, '').split('/').filter(Boolean);
  if (!parts.length) return { name: 'dashboard', id: null };
  const [head, tail] = parts;
  // 两段式路由：#/jobs/new → 新建编辑器，#/jobs/<id> → 任务编辑，
  // #/runs/<id> → 运行详情。此前这里原样返回 name='jobs'/'runs'，
  // 路由表里的 job-new / job-edit / run-detail 三个分支永远命中不了，
  // 点「新建任务」「编辑」「运行详情」都只是把列表页重画一遍。
  if (head === 'jobs') {
    if (tail === 'new') return { name: 'job-new', id: null };
    if (tail) return { name: 'job-edit', id: tail };
  }
  if (head === 'runs' && tail) return { name: 'run-detail', id: tail };
  return { name: head, id: tail || null };
}

function navTo(hash) {
  if (location.hash === hash) { route(); return; }
  location.hash = hash;
}

window.addEventListener('hashchange', route);

/* ------------------------------------------------------------------ 启动 */

async function boot() {
  // 通知 index.html 里的 ES5 兜底脚本：主脚本已成功解析并执行到这里。
  window.__wdbBooted = true;
  try {
    S.me = await api('/api/me', { raw: true });
  } catch {
    S.me = { authenticated: false };
  }

  S.booted = true;
  // 直接移除而不是 hidden：.boot 的 display:flex 是作者样式，会压过
  // 浏览器对 [hidden] 的默认 display:none，结果启动提示和界面上下两截
  // 同时显示（真实发生过）。remove 之后不存在被任何样式复活的可能。
  const bootEl = document.getElementById('boot');
  if (bootEl) bootEl.remove();
  $('#app').hidden = false;

  if (!S.me.authenticated) {
    renderLogin();
    return;
  }

  try {
    S.settings = await api('/api/settings');
  } catch {
    S.settings = null;
  }
  try {
    await route();
  } catch (err) {
    renderFatal(err);
  }
}

// renderFatal 把启动阶段的致命错误显示出来，而不是留一个空白控制台。
// 配合 index.html 里的 ES5 兜底脚本：那个负责「脚本根本没执行」，这个负责
// 「脚本执行了但初始化失败」。
function renderFatal(err) {
  const el = $('#app');
  if (!el) return;
  const msg = err && err.message ? err.message : String(err);
  el.innerHTML = `<div class="login-wrap"><div class="login-card">
      <div class="login-title">控制台加载失败</div>
      <p class="hint" style="margin:12px 0 16px;color:#b42318">${esc(msg)}</p>
      <button class="btn btn-primary" id="fatal-retry" style="width:100%">重新加载</button>
    </div></div>`;
  const btn = $('#fatal-retry');
  if (btn) btn.addEventListener('click', () => location.reload());
}

async function refreshCore() {
  const [status, profiles, jobs] = await Promise.all([
    api('/api/status'),
    api('/api/profiles'),
    api('/api/jobs'),
  ]);
  S.status = status;
  S.profiles = profiles.profiles || [];
  S.jobs = jobs.jobs || [];
}

function startPolling() {
  stopPolling();
  S.pollTimer = setInterval(async () => {
    if (S.polling) return;
    S.polling = true;
    try {
      const hasRunning = (S.status?.running_jobs || []).length > 0;
      await refreshCore();
      const nowRunning = (S.status?.running_jobs || []).length > 0;
      // Re-render only when something visibly changed, to avoid clobbering
      // in-progress form input.
      if (hasRunning || nowRunning) route({ fromPoll: true });
    } catch { /* transient network errors are ignored during polling */ }
    finally { S.polling = false; }
  }, 2500);
}

function stopPolling() {
  if (S.pollTimer) { clearInterval(S.pollTimer); S.pollTimer = null; }
}

/* ------------------------------------------------------------------ 登录 */

function renderLogin() {
  stopPolling();
  const app = $('#app');
  app.innerHTML = `
    <div class="login-wrap">
      <form class="login-card" id="login-form" autocomplete="off">
        <div class="login-brand">
          <div class="brand-mark">W</div>
          <div>
            <div class="login-title">QZRS-webdav-backup</div>
            <div class="login-sub">路由器文件备份控制台</div>
          </div>
        </div>

        <div class="field">
          <label class="label" for="lg-user">用户名</label>
          <!-- autocomplete 关掉：浏览器密码管理器会把其它网站的账号（例如
               同名 GitHub 用户名）自动填进来，覆盖掉预置的 admin，接着就是
               连续 401 直到触发限速锁定。 -->
          <input class="input" id="lg-user" name="username" autocomplete="off"
                 value="admin" required>
        </div>

        <div class="field">
          <label class="label" for="lg-pass">密码</label>
          <input class="input" id="lg-pass" name="password" type="password"
                 autocomplete="off" required>
        </div>

        <div id="login-error" class="hidden" style="margin-bottom:14px"></div>

        <button class="btn btn-primary" type="submit" style="width:100%"
                id="login-submit">登录</button>

        <p class="hint" style="margin-top:16px;text-align:center">
          用户名固定为 <code>admin</code>。初始密码只在安装时显示一次，<br>
          也可以在设备上执行下列命令找回：<br>
          <code>grep -A3 已创建默认管理账号 /etc/qzrs-webdav-backup/service.log</code>
        </p>
      </form>
    </div>`;

  $('#login-form').addEventListener('submit', async (e) => {
    e.preventDefault();
    const btn = $('#login-submit');
    const errBox = $('#login-error');
    btn.disabled = true;
    btn.textContent = '登录中…';
    errBox.classList.add('hidden');

    try {
      await api('/api/login', {
        method: 'POST',
        body: { username: $('#lg-user').value.trim(), password: $('#lg-pass').value },
      });
      S.me = { authenticated: true, username: $('#lg-user').value.trim() };
      await refreshCore();
      startPolling();
      navTo('#/');
      route();
    } catch (err) {
      errBox.className = 'banner banner-danger';
      errBox.innerHTML = `${icon('alert', 16)}<div>${esc(err.message)}</div>`;
      btn.disabled = false;
      btn.textContent = '登录';
      $('#lg-pass').select();
    }
  });

  setTimeout(() => $('#lg-user').focus(), 50);
}

/* ------------------------------------------------------------------ 布局 */

function renderShell(title, bodyHTML, actionsHTML = '') {
  const running = S.status?.running_jobs?.length || 0;

  const navItems = ROUTES.map((r) => {
    const active = S.route.name === r.name ||
      (r.name === 'jobs' && ['job-new', 'job-edit'].includes(S.route.name)) ||
      (r.name === 'runs' && S.route.name === 'run-detail');
    let badge = '';
    if (r.name === 'jobs' && S.jobs.length) badge = `<span class="nav-badge">${S.jobs.length}</span>`;
    if (r.name === 'profiles' && S.profiles.length) badge = `<span class="nav-badge">${S.profiles.length}</span>`;
    if (r.name === 'archives' && running) badge = `<span class="nav-badge">${running}</span>`;
    return `<a class="nav-item ${active ? 'active' : ''}" href="${r.hash}">
              ${icon(navIcon(r.name), 16)}<span>${r.title}</span>${badge}
            </a>`;
  }).join('');

  const version = S.status?.version || 'dev';
  const disk = S.status?.disk;

  $('#app').innerHTML = `
    <div class="app-shell">
      <aside class="sidebar">
        <div class="brand">
          <div class="brand-mark">W</div>
          <div class="brand-text">
            <div class="brand-title">QZRS-webdav-backup</div>
            <div class="brand-sub">${esc(S.status?.platform || '')} · v${esc(version)}</div>
          </div>
        </div>
        <nav class="nav">${navItems}</nav>
        <div class="sidebar-foot">
          ${disk && disk.supported ? `
            <div>
              <div style="display:flex;justify-content:space-between;margin-bottom:4px">
                <span>存储空间</span><span class="tabular">${disk.used_percent.toFixed(0)}%</span>
              </div>
              <div class="progress">
                <div class="progress-bar ${disk.used_percent > 90 ? 'danger' : disk.used_percent > 75 ? 'warn' : ''}"
                     style="width:${Math.min(100, disk.used_percent).toFixed(1)}%"></div>
              </div>
              <div style="margin-top:4px">剩余 ${fmtBytes(disk.free)}</div>
            </div>` : ''}
          <div>运行 ${esc(fmtUptime(S.status?.uptime_seconds))}</div>
        </div>
      </aside>

      <div class="main">
        <header class="topbar">
          <h1>${esc(title)}</h1>
          <div class="topbar-spacer"></div>
          <div class="toolbar">
            ${actionsHTML}
            <button class="btn btn-ghost btn-icon" id="btn-logout" title="退出登录">
              ${icon('logout', 16)}
            </button>
          </div>
        </header>
        <div class="content">${bodyHTML}</div>
      </div>
    </div>`;

  $('#btn-logout').addEventListener('click', async () => {
    if (!await confirmModal({ title: '退出登录', message: '确定要退出当前会话吗？', confirmText: '退出' })) return;
    try { await api('/api/logout', { method: 'POST' }); } catch { /* ignore */ }
    S.me = { authenticated: false };
    stopPolling();
    renderLogin();
  });
}

function navIcon(name) {
  return { dashboard: 'dash', jobs: 'jobs', archives: 'store', runs: 'history',
           profiles: 'cloud', settings: 'gear' }[name] || 'dash';
}

/* ------------------------------------------------------------------ 路由分发 */

async function route(opts) {
  if (!S.booted) return;
  if (!S.me || !S.me.authenticated) { renderLogin(); return; }

  // 轮询触发的重绘不能落在表单页上：整页 innerHTML 重绘会把用户填了一半的
  // 表单、以及目录选择器写回路径的目标 DOM 全部换掉（选择器回调持有旧元素
  // 引用，写入静默丢失——「选完目录路径没回来」就是这么来的）。
  if (opts && opts.fromPoll &&
      (S.route.name === 'job-new' || S.route.name === 'job-edit' ||
       S.route.name === 'settings')) {
    return;
  }

  S.route = parseHash();

  try {
    await refreshCore();
  } catch (err) {
    if (err.code === 401) return;
    renderShell('出错了',
      `<div class="banner banner-danger">${icon('alert', 16)}
        <div><strong>无法获取服务状态</strong><br>${esc(err.message)}</div></div>`);
    return;
  }

  if (!S.pollTimer && S.status) startPolling();

  switch (S.route.name) {
    case 'dashboard': renderDashboard(); break;
    case 'jobs': renderJobs(); break;
    case 'job-new': renderJobEditor(null); break;
    case 'job-edit': renderJobEditor(S.route.id); break;
    case 'archives': renderArchives(); break;
    case 'runs': await renderRuns(); break;
    case 'run-detail': await renderRunDetail(S.route.id); break;
    case 'profiles': renderProfiles(); break;
    case 'settings': await renderSettings(); break;
    default: renderDashboard();
  }
}

/* ------------------------------------------------------------------ 概览 */

function statusBadge(run) {
  const status = run && run.status;
  if (!status) return '<span class="badge">未执行</span>';
  const map = {
    success: ['badge-success', '成功'],
    failed: ['badge-danger', '失败'],
    partial: ['badge-warning', '部分成功'],
    running: ['badge-primary badge-running', '执行中'],
  };
  const entry = map[status] || ['badge', status];
  return `<span class="badge ${entry[0]}"><span class="badge-dot"></span>${entry[1]}</span>`;
}

function renderDashboard() {
  const st = S.status || {};
  const jobs = S.jobs || [];
  const runs = st.recent_runs || [];
  const enabled = jobs.filter((j) => j.enabled).length;
  const running = st.running_jobs || [];
  const disk = st.disk;

  const schedules = st.schedules || [];
  const upcoming = schedules
    .filter((s) => s.next_run)
    .sort((a, b) => new Date(a.next_run) - new Date(b.next_run))
    .slice(0, 4);

  const neverRun = jobs.filter((j) => !j.last_run_at);
  const failing = jobs.filter((j) => j.last_status === 'failed');

  let alertsHTML = '';
  if (failing.length) {
    alertsHTML += `<div class="banner banner-danger">${icon('alert', 16)}
      <div><strong>${failing.length} 个任务上次执行失败：</strong>
      ${failing.map((j) => esc(j.name)).join('、')}
      — 前往「执行历史」查看日志。</div></div>`;
  }
  if (disk && disk.supported && disk.used_percent > 90) {
    alertsHTML += `<div class="banner banner-danger">${icon('hdd', 16)}
      <div><strong>存储空间告急：</strong>已使用 ${disk.used_percent.toFixed(0)}%，
      仅剩 ${fmtBytes(disk.free)}。备份暂存可能失败。</div></div>`;
  } else if (disk && disk.supported && disk.used_percent > 80) {
    alertsHTML += `<div class="banner banner-warning">${icon('hdd', 16)}
      <div><strong>存储空间偏紧：</strong>已使用 ${disk.used_percent.toFixed(0)}%，
      剩余 ${fmtBytes(disk.free)}。</div></div>`;
  }
  if (!S.profiles.length) {
    alertsHTML += `<div class="banner banner-info">${icon('info', 16)}
      <div><strong>还没有 WebDAV 配置</strong>，先到「WebDAV 配置」添加一个远端存储。</div></div>`;
  } else if (!jobs.length) {
    alertsHTML += `<div class="banner banner-info">${icon('info', 16)}
      <div><strong>还没有备份任务</strong>，点击「新建任务」开始配置。</div></div>`;
  }

  const body = `
    ${alertsHTML ? `<div style="display:flex;flex-direction:column;gap:10px">${alertsHTML}</div>` : ''}

    <div class="stat-grid">
      <div class="stat">
        <div class="stat-label">备份任务</div>
        <div class="stat-value">${jobs.length}</div>
        <div class="stat-note">${enabled} 个已启用${running.length ? ` · ${running.length} 个执行中` : ''}</div>
      </div>
      <div class="stat">
        <div class="stat-label">WebDAV 配置</div>
        <div class="stat-value">${S.profiles.length}</div>
        <div class="stat-note">${S.profiles.filter((p) => p.has_password).length} 个已保存密码</div>
      </div>
      <div class="stat">
        <div class="stat-label">下次执行</div>
        <div class="stat-value sm">${upcoming.length ? esc(fmtTime(upcoming[0].next_run)) : '无计划'}</div>
        <div class="stat-note">${upcoming.length ? esc(upcoming[0].spec || '') : '所有任务均为手动触发'}</div>
      </div>
      <div class="stat">
        <div class="stat-label">存储剩余</div>
        <div class="stat-value sm">${disk && disk.supported ? fmtBytes(disk.free) : '未知'}</div>
        <div class="stat-note">${disk && disk.supported ? `共 ${fmtBytes(disk.total)}` : esc(disk?.error || '')}</div>
      </div>
    </div>

    <div class="card">
      <div class="card-head">
        <h2>备份任务</h2>
        <p class="sub">共 ${jobs.length} 个</p>
        <div class="card-head-actions">
          <button class="btn btn-sm btn-primary" data-act="new-job">${icon('plus', 14)}新建任务</button>
        </div>
      </div>
      <div class="card-body tight">
        ${jobs.length ? `
        <div class="table-wrap"><table class="table">
          <thead><tr>
            <th>任务</th><th>状态</th><th>上次执行</th><th>下次执行</th><th class="actions">操作</th>
          </tr></thead>
          <tbody>${jobs.map(jobRow).join('')}</tbody>
        </table></div>` : `
        <div class="empty">
          <div class="empty-icon">${icon('jobs', 20)}</div>
          <div class="empty-title">还没有备份任务</div>
          <div class="empty-desc">创建一个任务，把路由器上的配置或指定目录自动备份到 WebDAV 远端。</div>
          <button class="btn btn-primary" data-act="new-job">${icon('plus', 14)}新建任务</button>
        </div>`}
      </div>
    </div>

    <div class="card">
      <div class="card-head">
        <h2>最近执行</h2>
        <div class="card-head-actions">
          <button class="btn btn-sm btn-ghost" data-act="go-runs">查看全部</button>
        </div>
      </div>
      <div class="card-body tight">
        ${runs.length ? `
        <div class="table-wrap"><table class="table">
          <thead><tr><th>任务</th><th>类型</th><th>状态</th><th>开始时间</th><th class="num">体积</th><th class="actions"></th></tr></thead>
          <tbody>${runs.slice(0, 8).map(runRow).join('')}</tbody>
        </table></div>` : `
        <div class="empty"><div class="empty-title">暂无执行记录</div>
        <div class="empty-desc">任务执行后这里会显示每次备份与恢复的结果。</div></div>`}
      </div>
    </div>

    ${upcoming.length ? `
    <div class="card">
      <div class="card-head"><h2>调度计划</h2></div>
      <div class="card-body">
        <div class="kv">
          ${upcoming.map((s) => {
            const job = jobs.find((j) => j.id === s.job_id);
            return `<div class="kv-row">
              <span class="kv-key">${esc(job ? job.name : s.job_id)}</span>
              <span class="kv-val">${esc(fmtTimeFull(s.next_run))}
                <span class="text-muted mono" style="margin-left:8px">${esc(s.spec || '')}</span></span>
            </div>`;
          }).join('')}
        </div>
      </div>
    </div>` : ''}
  `;

  renderShell('概览', body, `
    <button class="btn btn-sm" data-act="refresh">${icon('refresh', 14)}刷新</button>`);

  bindDashboard();
}

function jobRow(j) {
  const running = (S.status?.running_jobs || []).includes(j.id);
  const sched = (S.status?.schedules || []).find((s) => s.job_id === j.id) || {};
  return `<tr>
    <td class="name-cell">
      <div class="row-title">${esc(j.name)}${j.enabled ? '' : ' <span class="badge">已停用</span>'}</div>
      <div class="row-sub">${esc((j.source.paths || []).join('  '))}</div>
    </td>
    <td>${statusBadge({ status: running ? 'running' : (j.last_status || '') })}</td>
    <td class="nowrap text-muted">${j.last_run_at ? esc(relTime(j.last_run_at)) : '—'}</td>
    <td class="nowrap text-muted">${sched.next_run ? esc(fmtTime(sched.next_run)) : '手动'}</td>
    <td class="actions">
      <button class="btn btn-sm ${running ? '' : 'btn-primary'}" data-run="${esc(j.id)}"
        ${running ? 'disabled' : ''} title="${running ? '执行中' : '立即备份'}">
        ${icon(running ? 'stop' : 'play', 13)}</button>
      <button class="btn btn-sm" data-edit="${esc(j.id)}" title="编辑">${icon('edit', 13)}</button>
      <button class="btn btn-sm btn-danger" data-del="${esc(j.id)}" title="删除">${icon('trash', 13)}</button>
    </td>
  </tr>`;
}

function runRow(r) {
  const typeLabel = r.type === 'restore' ? '恢复' : '备份';
  const detail = r.type === 'restore' ? (r.restore_from || '') : (r.remote_path || '');
  return `<tr>
    <td class="name-cell">
      <div class="row-title">${esc(r.job_name || '—')}</div>
      <div class="row-sub">${esc(detail)}</div>
    </td>
    <td><span class="badge ${r.type === 'restore' ? 'badge-primary' : ''}">${typeLabel}</span></td>
    <td>${statusBadge(r)}</td>
    <td class="nowrap text-muted">${esc(fmtTimeFull(r.started_at))}</td>
    <td class="num">${r.archive_size ? fmtBytes(r.archive_size) : '—'}</td>
    <td class="actions">
      <button class="btn btn-sm btn-ghost" data-log="${esc(r.id)}" title="查看日志">${icon('file', 13)}</button>
    </td>
  </tr>`;
}

function bindDashboard() {
  const app = $('#app');

  $('[data-act=refresh]', app)?.addEventListener('click', async () => {
    toast('正在刷新…', 'info');
    await route();
  });
  $$('[data-act=new-job]', app).forEach((b) =>
    b.addEventListener('click', () => navTo('#/jobs/new')));
  $('[data-act=go-runs]', app)?.addEventListener('click', () => navTo('#/runs'));

  bindJobAndRunActions(app);
}

function bindJobAndRunActions(root) {
  $$('[data-run]', root).forEach((b) => b.addEventListener('click', async () => {
    const id = b.dataset.run;
    b.disabled = true;
    try {
      await api(`/api/jobs/${encodeURIComponent(id)}/run`, { method: 'POST' });
      toast('备份已启动，可在执行历史中查看进度', 'success');
      await route();
    } catch (err) {
      toast(err.message, 'error', '无法启动');
      b.disabled = false;
    }
  }));

  $$('[data-edit]', root).forEach((b) =>
    b.addEventListener('click', () => navTo('#/jobs/' + encodeURIComponent(b.dataset.edit))));

  $$('[data-del]', root).forEach((b) => b.addEventListener('click', async () => {
    const job = S.jobs.find((j) => j.id === b.dataset.del);
    if (!job) return;
    const ok = await confirmModal({
      title: '删除备份任务',
      message: `确定要删除任务「${job.name}」吗？已经上传到远端的备份文件不会被删除。`,
      confirmText: '删除',
      danger: true,
    });
    if (!ok) return;
    try {
      await api(`/api/jobs/${encodeURIComponent(job.id)}`, { method: 'DELETE' });
      toast('任务已删除', 'success');
      await route();
    } catch (err) { toast(err.message, 'error', '删除失败'); }
  }));

  $$('[data-log]', root).forEach((b) =>
    b.addEventListener('click', () => navTo('#/runs/' + encodeURIComponent(b.dataset.log))));
}

/* ------------------------------------------------------------------ 任务列表页 */

function renderJobs() {
  const jobs = S.jobs;
  const body = `
    <div class="card">
      <div class="card-head">
        <h2>全部任务</h2>
        <p class="sub">${jobs.filter((j) => j.enabled).length} / ${jobs.length} 已启用</p>
        <div class="card-head-actions">
          <button class="btn btn-sm btn-primary" data-act="new-job">${icon('plus', 14)}新建任务</button>
        </div>
      </div>
      <div class="card-body tight">
        ${jobs.length ? `
        <div class="table-wrap"><table class="table">
          <thead><tr>
            <th>任务</th><th>目标</th><th>状态</th><th>上次执行</th><th>下次执行</th><th class="actions">操作</th>
          </tr></thead>
          <tbody>${jobs.map((j) => {
            const prof = S.profiles.find((p) => p.id === j.target.profile_id);
            const sched = (S.status?.schedules || []).find((s) => s.job_id === j.id) || {};
            const running = (S.status?.running_jobs || []).includes(j.id);
            const mode = j.schedule.mode === 'cron' ? `cron ${j.schedule.cron}`
                       : j.schedule.mode === 'interval' ? `每 ${j.schedule.interval}`
                       : '手动';
            return `<tr>
              <td class="name-cell">
                <div class="row-title">${esc(j.name)}${j.enabled ? '' : ' <span class="badge">已停用</span>'}</div>
                <div class="row-sub">${(j.source.paths || []).map(esc).join('  ')}</div>
              </td>
              <td>
                <div>${esc(prof ? prof.name : '配置已删除')}</div>
                <div class="row-sub mono">${esc('/' + (j.target.dir || ''))}</div>
              </td>
              <td>${statusBadge({ status: running ? 'running' : (j.last_status || '') })}</td>
              <td class="nowrap text-muted">${j.last_run_at ? esc(relTime(j.last_run_at)) : '—'}</td>
              <td class="nowrap text-muted">${sched.next_run ? esc(fmtTime(sched.next_run)) : esc(mode)}</td>
              <td class="actions">
                <button class="btn btn-sm ${running ? '' : 'btn-primary'}" data-run="${esc(j.id)}" ${running ? 'disabled' : ''}>
                  ${icon(running ? 'stop' : 'play', 13)}</button>
                <button class="btn btn-sm" data-edit="${esc(j.id)}">${icon('edit', 13)}</button>
                <button class="btn btn-sm btn-danger" data-del="${esc(j.id)}">${icon('trash', 13)}</button>
              </td>
            </tr>`;
          }).join('')}</tbody>
        </table></div>` : `
        <div class="empty">
          <div class="empty-icon">${icon('jobs', 20)}</div>
          <div class="empty-title">还没有备份任务</div>
          <div class="empty-desc">任务定义了「备份什么」和「备份到哪里」，可以手动执行或按计划自动执行。</div>
          <button class="btn btn-primary" data-act="new-job">${icon('plus', 14)}新建任务</button>
        </div>`}
      </div>
    </div>`;

  renderShell('备份任务', body);
  const app = $('#app');
  $$('[data-act=new-job]', app).forEach((b) =>
    b.addEventListener('click', () => navTo('#/jobs/new')));
  bindJobAndRunActions(app);
}

/* ------------------------------------------------------------------ 任务编辑器 */

const PRESET_PATHS = [
  { label: 'OpenWrt 系统配置', desc: '/etc/config、/etc/passwd、防火墙等', paths: ['/etc/config', '/etc/passwd', '/etc/shadow', '/etc/dropbear', '/etc/rc.local'] },
  { label: '已安装软件包清单', desc: '/etc/opkg 与版本信息', paths: ['/etc/opkg', '/etc/openwrt_release', '/etc/banner'] },
  { label: 'Docker 容器数据', desc: '/opt/docker 下的卷与容器数据', paths: ['/opt/docker'] },
];

function renderJobEditor(jobId) {
  const isNew = !jobId || jobId === 'new';
  const job = isNew ? null : S.jobs.find((j) => j.id === jobId);

  if (!isNew && !job) {
    renderShell('任务不存在',
      `<div class="banner banner-danger">${icon('alert', 16)}<div>找不到该任务，可能已被删除。</div></div>
       <button class="btn" data-act="back">返回任务列表</button>`);
    $('[data-act=back]').addEventListener('click', () => navTo('#/jobs'));
    return;
  }

  if (!S.profiles.length) {
    renderShell('新建任务',
      `<div class="banner banner-warning">${icon('alert', 16)}
        <div><strong>还没有 WebDAV 配置</strong><br>
        备份需要一个远端存储。请先添加一个 WebDAV 配置。</div></div>
       <button class="btn btn-primary" id="go-profiles">去添加配置</button>`);
    $('#go-profiles').addEventListener('click', () => navTo('#/profiles'));
    return;
  }

  const j = job || {
    name: '', comment: '', enabled: true,
    source: { paths: [], include: [], exclude: [], max_file_size_mb: 0, follow_symlinks: false, one_file_system: true },
    target: { profile_id: S.profiles[0].id, dir: 'backups' },
    schedule: { mode: 'manual', cron: '0 3 * * *', interval: '24h' },
    retention: { keep: 7 },
    options: { compression: 'gzip', gzip_level: 6, temp_dir: '', exclude_caches: true, stream: false },
  };

  const body = `
    <div class="card">
      <div class="card-head"><h2>基本信息</h2></div>
      <div class="card-body">
        <div class="form-grid">
          <div class="field">
            <label class="label" for="f-name">任务名称 <span class="req">*</span></label>
            <input class="input" id="f-name" value="${esc(j.name)}" placeholder="例如 OpenWrt 配置备份" maxlength="60">
            <p class="hint">用于生成远端文件名，建议使用简短的中文或英文。</p>
          </div>
          <div class="field">
            <label class="label" for="f-enabled">启用状态</label>
            <label class="check" style="padding-top:6px">
              <input type="checkbox" id="f-enabled" ${j.enabled ? 'checked' : ''}>
              <span class="check-text"><span class="check-title">启用此任务</span>
              <span class="check-desc">停用后不会按计划执行，但仍可手动运行</span></span>
            </label>
          </div>
          <div class="field span-2">
            <label class="label" for="f-comment">备注</label>
            <input class="input" id="f-comment" value="${esc(j.comment || '')}" placeholder="可选">
          </div>
        </div>
      </div>
    </div>

    <div class="card">
      <div class="card-head">
        <h2>备份内容</h2>
        <p class="sub">要打包哪些路径</p>
      </div>
      <div class="card-body">
        <div class="field">
          <label class="label">源路径 <span class="req">*</span></label>
          <div style="display:flex;gap:8px;flex-wrap:wrap;margin-bottom:9px">
            ${PRESET_PATHS.map((p, i) =>
              `<button class="btn btn-sm" data-preset="${i}" title="${esc(p.desc)}">+ ${esc(p.label)}</button>`
            ).join('')}
          </div>
          <div class="token-list" id="paths-list"></div>
          <div class="token-add" style="margin-top:8px">
            <input class="input mono" id="path-input" placeholder="/etc/config" autocomplete="off">
            <button class="btn" id="path-browse">${icon('folder', 14)}浏览</button>
            <button class="btn" id="path-add">${icon('plus', 14)}添加</button>
          </div>
          <p class="hint">归档内会保留完整路径结构（去掉开头的斜杠），恢复到 / 即可原地还原。</p>
        </div>

        <div class="divider"></div>

        <div class="form-grid">
          <div class="field">
            <label class="label">仅包含（通配符，留空为全部）</label>
            <div class="token-list" id="include-list"></div>
            <div class="token-add" style="margin-top:8px">
              <input class="input mono" id="include-input" placeholder="*.conf" autocomplete="off">
              <button class="btn" id="include-add">${icon('plus', 14)}</button>
            </div>
          </div>
          <div class="field">
            <label class="label">排除（通配符）</label>
            <div class="token-list" id="exclude-list"></div>
            <div class="token-add" style="margin-top:8px">
              <input class="input mono" id="exclude-input" placeholder="*.log" autocomplete="off">
              <button class="btn" id="exclude-add">${icon('plus', 14)}</button>
            </div>
          </div>
        </div>
        <p class="hint" style="margin-top:6px">
          不含斜杠的模式匹配任意层级的名字（如 <code>*.log</code>）；
          含斜杠则从根开始匹配（如 <code>etc/config/**</code>）；<code>**</code> 匹配任意多层。
        </p>

        <div class="divider"></div>

        <div class="form-grid">
          <div class="field">
            <label class="label" for="f-maxsize">单个文件大小上限（MB，0 为不限）</label>
            <input class="input" id="f-maxsize" type="number" min="0" value="${j.source.max_file_size_mb || 0}">
          </div>
          <div class="field">
            <label class="label">选项</label>
            <label class="check">
              <input type="checkbox" id="f-onefs" ${j.source.one_file_system ? 'checked' : ''}>
              <span class="check-text"><span class="check-title">不跨文件系统</span>
              <span class="check-desc">遇到挂载点就停止，避免把整块硬盘拖进来</span></span>
            </label>
            <label class="check">
              <input type="checkbox" id="f-follow" ${j.source.follow_symlinks ? 'checked' : ''}>
              <span class="check-text"><span class="check-title">跟随符号链接</span>
              <span class="check-desc">默认保存为链接本身，开启后会读取目标内容</span></span>
            </label>
          </div>
        </div>
      </div>
    </div>

    <div class="card">
      <div class="card-head"><h2>备份目标</h2></div>
      <div class="card-body">
        <div class="form-grid">
          <div class="field">
            <label class="label" for="f-profile">WebDAV 配置 <span class="req">*</span></label>
            <select class="select" id="f-profile">
              ${S.profiles.map((p) =>
                `<option value="${esc(p.id)}" ${p.id === j.target.profile_id ? 'selected' : ''}>
                  ${esc(p.name)} — ${esc(p.url)}
                </option>`).join('')}
            </select>
          </div>
          <div class="field">
            <label class="label" for="f-dir">远端目录</label>
            <input class="input mono" id="f-dir" value="${esc(j.target.dir || '')}" placeholder="backups">
            <p class="hint">相对于配置的根地址，不存在时会自动创建。</p>
          </div>
        </div>
      </div>
    </div>

    <div class="card">
      <div class="card-head"><h2>执行计划</h2></div>
      <div class="card-body">
        <div class="field">
          <label class="label">触发方式</label>
          <div class="segmented" id="sched-seg">
            <button data-mode="manual" class="${j.schedule.mode === 'manual' ? 'on' : ''}">仅手动</button>
            <button data-mode="interval" class="${j.schedule.mode === 'interval' ? 'on' : ''}">固定间隔</button>
            <button data-mode="cron" class="${j.schedule.mode === 'cron' ? 'on' : ''}">cron 表达式</button>
          </div>
        </div>

        <div id="sched-interval" class="${j.schedule.mode === 'interval' ? '' : 'hidden'}">
          <div class="field">
            <label class="label" for="f-interval">间隔</label>
            <select class="select" id="f-interval">
              ${['1h', '2h', '6h', '12h', '24h', '168h'].map((v) =>
                `<option value="${v}" ${j.schedule.interval === v ? 'selected' : ''}>${
                  { '1h': '每小时', '2h': '每 2 小时', '6h': '每 6 小时', '12h': '每 12 小时',
                    '24h': '每天', '168h': '每周' }[v]}</option>`).join('')}
            </select>
          </div>
        </div>

        <div id="sched-cron" class="${j.schedule.mode === 'cron' ? '' : 'hidden'}">
          <div class="field">
            <label class="label" for="f-cron">cron 表达式（分 时 日 月 周）</label>
            <input class="input mono" id="f-cron" value="${esc(j.schedule.cron || '')}" placeholder="0 3 * * *">
            <div style="display:flex;gap:6px;flex-wrap:wrap;margin-top:8px">
              ${[['0 3 * * *', '每天 03:00'], ['0 */6 * * *', '每 6 小时'],
                 ['30 4 * * 0', '每周日 04:30'], ['0 3 1 * *', '每月 1 日 03:00']]
                .map(([e, l]) => `<button class="btn btn-sm" data-cron="${esc(e)}">${esc(l)}</button>`).join('')}
            </div>
          </div>
        </div>

        <div id="sched-preview" class="hidden">
          <div class="banner banner-info" style="align-items:flex-start">
            ${icon('clock', 16)}
            <div><strong>接下来将在这个时间执行：</strong>
            <div id="sched-preview-list" class="mono" style="font-size:12px;margin-top:5px"></div></div>
          </div>
        </div>
      </div>
    </div>

    <div class="card">
      <div class="card-head"><h2>压缩与保留</h2></div>
      <div class="card-body">
        <div class="form-grid">
          <div class="field">
            <label class="label" for="f-compression">压缩方式</label>
            <select class="select" id="f-compression">
              <option value="gzip" ${j.options.compression === 'gzip' ? 'selected' : ''}>gzip（推荐）</option>
              <option value="none" ${j.options.compression === 'none' ? 'selected' : ''}>不压缩（.tar）</option>
            </select>
          </div>
          <div class="field">
            <label class="label" for="f-level">压缩级别（1 最快 ~ 9 最小）</label>
            <input class="input" id="f-level" type="number" min="1" max="9" value="${j.options.gzip_level || 6}">
          </div>
          <div class="field">
            <label class="label" for="f-keep">保留版本数（0 为不限）</label>
            <input class="input" id="f-keep" type="number" min="0" value="${j.retention.keep}">
            <p class="hint">超出数量时自动删除远端最旧的归档。</p>
          </div>
          <div class="field">
            <label class="label" for="f-tempdir">本地暂存目录</label>
            <input class="input mono" id="f-tempdir" value="${esc(j.options.temp_dir || '')}" placeholder="留空使用默认">
            <p class="hint">打包时的临时文件位置，空间紧张时指向大容量分区。</p>
          </div>
        </div>

        <div class="divider"></div>

        <label class="check">
          <input type="checkbox" id="f-caches" ${j.options.exclude_caches ? 'checked' : ''}>
          <span class="check-text"><span class="check-title">排除常见缓存与临时文件</span>
          <span class="check-desc">.DS_Store、Thumbs.db、*.swp、*~、node_modules、__pycache__ 等</span></span>
        </label>
        <label class="check">
          <input type="checkbox" id="f-stream" ${j.options.stream ? 'checked' : ''}>
          <span class="check-text"><span class="check-title">流式上传（不占用本地临时空间）</span>
          <span class="check-desc">直接边打包边上传。需要远端支持无长度 PUT，部分服务端会拒绝；失败时请关闭此项</span></span>
        </label>
      </div>
    </div>

    <div class="toolbar" style="justify-content:flex-end">
      <button class="btn" data-act="cancel">取消</button>
      ${!isNew ? `<button class="btn" data-act="test-run">${icon('play', 14)}保存并立即备份</button>` : ''}
      <button class="btn btn-primary" data-act="save">${icon('check', 14)}保存任务</button>
    </div>`;

  renderShell(isNew ? '新建备份任务' : '编辑：' + job.name, body);

  mountJobEditor(j, isNew, PRESET_PATHS);
}

function mountJobEditor(j, isNew, presets) {
  const app = $('#app');
  const lists = {
    paths: { el: $('#paths-list', app), add: $('#path-add', app), input: $('#path-input', app), value: [...(j.source.paths || [])] },
    include: { el: $('#include-list', app), add: $('#include-add', app), input: $('#include-input', app), value: [...(j.source.include || [])] },
    exclude: { el: $('#exclude-list', app), add: $('#exclude-add', app), input: $('#exclude-input', app), value: [...(j.source.exclude || [])] },
  };

  function drawList(key) {
    const L = lists[key];
    if (!L.value.length) {
      L.el.innerHTML = `<p class="hint" style="padding:2px 0">${
        key === 'paths' ? '尚未添加任何路径' : '未设置'}</p>`;
      return;
    }
    L.el.innerHTML = L.value.map((v, i) =>
      `<div class="token-row">
         <span class="token-text">${esc(v)}</span>
         <span class="token-actions">
           <button class="btn btn-ghost btn-icon btn-sm" data-rm="${key}:${i}" title="移除">${icon('x', 13)}</button>
         </span>
       </div>`).join('');
  }

  function refresh() { drawList('paths'); drawList('include'); drawList('exclude'); }

  app.addEventListener('click', (e) => {
    const rm = e.target.closest('[data-rm]');
    if (rm) {
      const [key, idx] = rm.dataset.rm.split(':');
      lists[key].value.splice(Number(idx), 1);
      drawList(key);
    }
  });

  function addFrom(key) {
    const L = lists[key];
    const v = L.input.value.trim();
    if (!v) return;
    if (L.value.includes(v)) { toast('该条目已存在', 'warning'); return; }
    L.value.push(v);
    L.input.value = '';
    drawList(key);
    L.input.focus();
  }

  ['paths', 'include', 'exclude'].forEach((key) => {
    lists[key].add.addEventListener('click', () => addFrom(key));
    lists[key].input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') { e.preventDefault(); addFrom(key); }
    });
  });

  // Presets append without duplicating.
  $$('[data-preset]', app).forEach((b) => b.addEventListener('click', () => {
    const p = presets[Number(b.dataset.preset)];
    let added = 0;
    p.paths.forEach((path) => {
      if (!lists.paths.value.includes(path)) { lists.paths.value.push(path); added++; }
    });
    drawList('paths');
    toast(added ? `已添加 ${added} 个路径` : '这些路径都已经在列表里了', added ? 'success' : 'info');
  }));

  $('#path-browse', app).addEventListener('click', () => {
    openPathPicker({
      multiple: true,
      onPick: (paths) => {
        let added = 0;
        paths.forEach((p) => {
          if (!lists.paths.value.includes(p)) { lists.paths.value.push(p); added++; }
        });
        drawList('paths');
        if (added) toast(`已添加 ${added} 个路径`, 'success');
      },
    });
  });

  /* 调度 */
  let mode = j.schedule.mode || 'manual';
  const seg = $('#sched-seg', app);
  const intervalBox = $('#sched-interval', app);
  const cronBox = $('#sched-cron', app);

  async function updateSchedulePreview() {
    const box = $('#sched-preview', app);
    const list = $('#sched-preview-list', app);
    if (mode === 'manual') { box.classList.add('hidden'); return; }

    const payload = mode === 'cron'
      ? { mode: 'cron', cron: $('#f-cron', app).value.trim() }
      : { mode: 'interval', interval: $('#f-interval', app).value };

    try {
      const r = await api('/api/schedule/validate', { method: 'POST', body: payload });
      if (!r.valid) {
        box.classList.remove('hidden');
        box.innerHTML = `<div class="banner banner-danger">${icon('alert', 16)}
          <div><strong>表达式无效</strong><br>${esc(r.error || '')}</div></div>`;
        return;
      }
      const runs = r.next_runs || [];
      box.classList.remove('hidden');
      box.innerHTML = `<div class="banner banner-info" style="align-items:flex-start">${icon('clock', 16)}
        <div><strong>接下来将在这个时间执行：</strong>
        <div style="font-family:var(--mono);font-size:12px;margin-top:5px;line-height:1.7">
          ${runs.map(esc).join('<br>')}</div></div></div>`;
    } catch {
      box.classList.add('hidden');
    }
  }

  seg.addEventListener('click', (e) => {
    const btn = e.target.closest('button[data-mode]');
    if (!btn) return;
    mode = btn.dataset.mode;
    $$('button', seg).forEach((b) => b.classList.toggle('on', b === btn));
    intervalBox.classList.toggle('hidden', mode !== 'interval');
    cronBox.classList.toggle('hidden', mode !== 'cron');
    updateSchedulePreview();
  });

  $('#f-interval', app).addEventListener('change', updateSchedulePreview);
  $('#f-cron', app).addEventListener('input', debounce(updateSchedulePreview, 400));
  $$('[data-cron]', app).forEach((b) => b.addEventListener('click', () => {
    $('#f-cron', app).value = b.dataset.cron;
    updateSchedulePreview();
  }));
  if (mode !== 'manual') updateSchedulePreview();

  /* 保存 */
  async function collect() {
    const name = $('#f-name', app).value.trim();
    if (!name) { toast('请填写任务名称', 'warning'); $('#f-name', app).focus(); return null; }
    if (!lists.paths.value.length) { toast('至少添加一个源路径', 'warning'); return null; }

    const keep = parseInt($('#f-keep', app).value, 10);
    const level = parseInt($('#f-level', app).value, 10);
    const maxSize = parseInt($('#f-maxsize', app).value, 10);

    return {
      name,
      comment: $('#f-comment', app).value.trim(),
      enabled: $('#f-enabled', app).checked,
      source: {
        paths: lists.paths.value,
        include: lists.include.value.length ? lists.include.value : undefined,
        exclude: lists.exclude.value.length ? lists.exclude.value : undefined,
        max_file_size_mb: isNaN(maxSize) ? 0 : maxSize,
        follow_symlinks: $('#f-follow', app).checked,
        one_file_system: $('#f-onefs', app).checked,
      },
      target: {
        profile_id: $('#f-profile', app).value,
        dir: $('#f-dir', app).value.trim(),
      },
      schedule: {
        mode,
        cron: mode === 'cron' ? $('#f-cron', app).value.trim() : '',
        interval: mode === 'interval' ? $('#f-interval', app).value : '',
      },
      retention: { keep: isNaN(keep) ? 0 : keep },
      options: {
        compression: $('#f-compression', app).value,
        gzip_level: isNaN(level) ? 6 : level,
        temp_dir: $('#f-tempdir', app).value.trim() || undefined,
        exclude_caches: $('#f-caches', app).checked,
        stream: $('#f-stream', app).checked,
      },
    };
  }

  $('[data-act=cancel]', app).addEventListener('click', () => navTo('#/jobs'));

  $('[data-act=save]', app).addEventListener('click', async (e) => {
    const payload = await collect();
    if (!payload) return;
    const btn = e.currentTarget;
    btn.disabled = true;
    try {
      if (isNew) {
        const created = await api('/api/jobs', { method: 'POST', body: payload });
        toast('任务已创建', 'success');
        navTo('#/jobs');
        await route();
        return created;
      }
      await api(`/api/jobs/${encodeURIComponent(j.id)}`, { method: 'PUT', body: payload });
      toast('任务已保存', 'success');
      navTo('#/jobs');
      await route();
    } catch (err) {
      toast(err.message, 'error', '保存失败');
      btn.disabled = false;
    }
  });

  $('[data-act=test-run]', app)?.addEventListener('click', async (e) => {
    const payload = await collect();
    if (!payload) return;
    const btn = e.currentTarget;
    btn.disabled = true;
    try {
      await api(`/api/jobs/${encodeURIComponent(j.id)}`, { method: 'PUT', body: payload });
      await api(`/api/jobs/${encodeURIComponent(j.id)}/run`, { method: 'POST' });
      toast('已保存并开始备份，可在执行历史中查看', 'success');
      navTo('#/runs');
      await route();
    } catch (err) {
      toast(err.message, 'error', '操作失败');
      btn.disabled = false;
    }
  });

  // 初始渲染已有条目（源路径/包含/排除）。此前 refresh 定义了却从未被调用，
  // 编辑已有任务时这些列表一片空白，看起来像配置丢了。
  refresh();
}

function debounce(fn, ms) {
  let t;
  return (...args) => { clearTimeout(t); t = setTimeout(() => fn(...args), ms); };
}

/* ------------------------------------------------------------------ 目录选择器 */

function openPathPicker({ multiple = false, initial = '/', onPick }) {
  let cwd = initial;
  let showHidden = false;
  const picked = new Set();

  const m = openModal({
    title: '选择本地路径',
    subtitle: multiple ? '可以多选，选中后点击「添加」' : '点击目录进入，双击可选中',
    width: 'wide',
    body: `
      <div class="picker-path">
        <span class="p mono" id="pk-cwd">/</span>
        <button class="btn btn-sm" id="pk-up" title="上一级">${icon('up', 13)}上级</button>
        <button class="btn btn-sm" id="pk-manual" title="手动输入">${icon('edit', 13)}手输</button>
      </div>
      <div class="toolbar" style="margin-bottom:9px">
        <label class="check" style="padding:0">
          <input type="checkbox" id="pk-hidden"><span class="check-text">显示隐藏文件</span>
        </label>
        <div class="spacer"></div>
        <span class="hint" id="pk-count"></span>
      </div>
      <div class="picker-list" id="pk-list"></div>
      <div id="pk-manual-box" class="hidden" style="margin-top:10px">
        <div class="token-add">
          <input class="input mono" id="pk-manual-input" placeholder="/etc/config">
          <button class="btn" id="pk-manual-ok">添加</button>
        </div>
      </div>`,
    footer: `
      <button class="btn left" id="pk-here">${icon('check', 14)}选择当前目录</button>
      <button class="btn" id="pk-cancel">取消</button>
      ${multiple ? `<button class="btn btn-primary" id="pk-confirm">添加所选</button>` : ''}`,
    onMount({ el, close }) {
      const listEl = $('#pk-list', el);
      const cwdEl = $('#pk-cwd', el);
      const countEl = $('#pk-count', el);
      const upBtn = $('#pk-up', el);

      function updateCount() {
        countEl.textContent = picked.size ? `已选 ${picked.size} 项` : '';
      }

      async function load(path) {
        listEl.innerHTML = '<div class="empty" style="padding:22px"><div class="boot-spinner"></div></div>';
        try {
          const r = await api(`/api/fs/list?path=${encodeURIComponent(path)}&hidden=${showHidden ? 1 : 0}`);
          cwd = r.path;
          cwdEl.textContent = r.path;
          upBtn.disabled = !r.parent;

          const entries = r.entries || [];
          if (!entries.length) {
            listEl.innerHTML = '<div class="empty" style="padding:22px"><div class="empty-title">这个目录是空的</div></div>';
            return;
          }
          listEl.innerHTML = entries.map((e) => {
            if (e.error) {
              return `<div class="picker-row err"><span>${icon('alert', 14)}</span>
                <span class="picker-name">${esc(e.name)}</span>
                <span class="picker-meta">无法读取</span></div>`;
            }
            return `<div class="picker-row ${e.is_dir ? 'is-dir' : ''} ${e.is_symlink ? 'is-symlink' : ''}"
                        data-path="${esc(e.path)}" data-dir="${e.is_dir ? 1 : 0}">
              <span style="color:var(--text-3)">${icon(e.is_dir ? 'folder' : 'file', 14)}</span>
              <span class="picker-name">${esc(e.name)}</span>
              <span class="picker-meta">${e.is_dir ? '' : fmtBytes(e.size)}</span>
              ${multiple && e.is_dir ? `<input type="checkbox" data-check="${esc(e.path)}"
                 ${picked.has(e.path) ? 'checked' : ''} style="margin:0;accent-color:var(--primary)">` : ''}
            </div>`;
          }).join('');
        } catch (err) {
          listEl.innerHTML = `<div class="empty" style="padding:22px">
            <div class="empty-title text-danger">无法读取目录</div>
            <div class="empty-desc">${esc(err.message)}</div></div>`;
        }
      }

      listEl.addEventListener('click', (e) => {
        const chk = e.target.closest('[data-check]');
        if (chk) {
          e.stopPropagation();
          if (chk.checked) picked.add(chk.dataset.check); else picked.delete(chk.dataset.check);
          updateCount();
          return;
        }
        const row = e.target.closest('.picker-row');
        if (!row || row.classList.contains('err')) return;
        const p = row.dataset.path;
        if (row.dataset.dir === '1') {
          load(p);
        } else if (!multiple) {
          onPick([p]); close();
        }
      });

      listEl.addEventListener('dblclick', (e) => {
        const row = e.target.closest('.picker-row');
        if (!row || row.dataset.dir === '1') return;
        onPick([row.dataset.path]);
        close();
      });

      upBtn.addEventListener('click', () => {
        const parts = cwd.replace(/\/+$/, '').split('/').filter(Boolean);
        parts.pop();
        load('/' + parts.join('/'));
      });

      $('#pk-hidden', el).addEventListener('change', (e) => {
        showHidden = e.target.checked;
        load(cwd);
      });

      $('#pk-manual', el).addEventListener('click', () => {
        $('#pk-manual-box', el).classList.toggle('hidden');
        $('#pk-manual-input', el).focus();
      });

      const addManual = () => {
        const v = $('#pk-manual-input', el).value.trim();
        if (!v) return;
        onPick([v]);
        close();
      };
      $('#pk-manual-ok', el).addEventListener('click', addManual);
      $('#pk-manual-input', el).addEventListener('keydown', (e) => {
        if (e.key === 'Enter') { e.preventDefault(); addManual(); }
      });

      $('#pk-here', el).addEventListener('click', () => { onPick([cwd]); close(); });
      $('#pk-cancel', el).addEventListener('click', close);
      $('#pk-confirm', el)?.addEventListener('click', () => {
        if (!picked.size) { toast('请至少勾选一个目录', 'warning'); return; }
        onPick(Array.from(picked));
        close();
      });

      load(initial);
    },
  });
  return m;
}

/* ------------------------------------------------------------------ 归档与恢复 */

function renderArchives() {
  const profileOptions = S.profiles.map((p) =>
    `<option value="${esc(p.id)}">${esc(p.name)}</option>`).join('');

  const body = `
    <div class="card">
      <div class="card-head">
        <h2>远端备份仓库</h2>
        <p class="sub">浏览 WebDAV 上的归档文件，并从中恢复</p>
        <div class="card-head-actions">
          <button class="btn btn-sm" id="ar-load">${icon('refresh', 14)}刷新</button>
        </div>
      </div>
      <div class="card-body">
        ${S.profiles.length ? `
        <div class="toolbar" style="margin-bottom:14px">
          <div style="min-width:200px;flex:1">
            <select class="select" id="ar-profile">${profileOptions}</select>
          </div>
          <div style="min-width:160px;flex:1">
            <input class="input mono" id="ar-dir" placeholder="目录（留空为该配置的根目录）">
          </div>
          <button class="btn btn-primary" id="ar-load2">${icon('search', 14)}列出归档</button>
        </div>` : `
        <div class="banner banner-warning">${icon('alert', 16)}
          <div>还没有 WebDAV 配置，请先到「WebDAV 配置」添加。</div></div>`}
      </div>
      <div class="card-body tight" id="ar-results">
        <div class="empty">
          <div class="empty-icon">${icon('store', 20)}</div>
          <div class="empty-title">尚未加载</div>
          <div class="empty-desc">选择一个 WebDAV 配置后点击「列出归档」，即可查看远端已有的备份文件。</div>
        </div>
      </div>
    </div>

    <div class="card">
      <div class="card-head">
        <h2>恢复向导</h2>
        <p class="sub">把远端归档解包回本机</p>
      </div>
      <div class="card-body">
        <div class="banner banner-warning">${icon('alert', 16)}
          <div><strong>恢复会覆盖目标目录中的同名文件。</strong>
          建议先用「试运行」确认将要写入的内容，再正式执行。</div></div>
        <div style="margin-top:14px;display:flex;gap:9px;flex-wrap:wrap">
          <button class="btn btn-primary" id="rw-start">${icon('download', 14)}从归档恢复</button>
          <button class="btn" id="rw-own">${icon('refresh', 14)}从某个任务的最新备份恢复</button>
        </div>
      </div>
    </div>`;

  renderShell('备份仓库', body);

  if (S.profiles.length) {
    const load = doListArchives;
    $('#ar-load', $('#app')).addEventListener('click', load);
    $('#ar-load2', $('#app')).addEventListener('click', load);
  }
  $('#rw-start', $('#app')).addEventListener('click', () => openRestoreWizard());
  $('#rw-own', $('#app')).addEventListener('click', () => openRestoreFromJob());
}

async function doListArchives() {
  const app = $('#app');
  const profileId = $('#ar-profile', app).value;
  const dir = $('#ar-dir', app).value.trim();
  const box = $('#ar-results', app);

  box.innerHTML = '<div class="empty" style="padding:30px"><div class="boot-spinner"></div><div class="empty-desc">正在读取远端目录…</div></div>';

  try {
    const r = await api(`/api/archives?profile_id=${encodeURIComponent(profileId)}&dir=${encodeURIComponent(dir)}`);
    const items = r.archives || [];
    S.archives = items;

    if (!items.length) {
      box.innerHTML = `<div class="empty"><div class="empty-icon">${icon('store', 20)}</div>
        <div class="empty-title">这个位置没有备份文件</div>
        <div class="empty-desc">确认目录是否正确，或者先运行一次备份任务。</div></div>`;
      return;
    }

    const total = items.reduce((a, b) => a + (b.size || 0), 0);
    box.innerHTML = `
      <div class="table-wrap"><table class="table">
        <thead><tr>
          <th>归档文件</th><th>所属任务</th><th class="num">大小</th><th>修改时间</th><th class="actions">操作</th>
        </tr></thead>
        <tbody>${items.map((a) => `
          <tr>
            <td class="name-cell">
              <div class="row-title mono" style="font-size:12.5px">${esc(a.name)}</div>
              <div class="row-sub">${esc('/' + a.path)}</div>
            </td>
            <td>${a.job_name ? esc(a.job_name) : '<span class="text-muted">—</span>'}</td>
            <td class="num">${fmtBytes(a.size)}</td>
            <td class="nowrap text-muted">${esc(fmtTimeFull(a.modified))}</td>
            <td class="actions">
              <button class="btn btn-sm" data-preview="${esc(a.path)}" data-profile="${esc(a.profile_id)}"
                title="预览内容">${icon('search', 13)}预览</button>
              <button class="btn btn-sm btn-primary" data-restore="${esc(a.path)}" data-profile="${esc(a.profile_id)}"
                title="恢复">${icon('download', 13)}恢复</button>
            </td>
          </tr>`).join('')}</tbody>
      </table></div>
      <div style="padding:11px 14px;border-top:1px solid var(--border)" class="hint">
        共 ${items.length} 个归档，合计 ${fmtBytes(total)}
      </div>`;

    $$('[data-preview]', box).forEach((b) => b.addEventListener('click', () =>
      openPreview(b.dataset.profile, b.dataset.preview)));
    $$('[data-restore]', box).forEach((b) => b.addEventListener('click', () =>
      openRestoreWizard({ profileId: b.dataset.profile, remotePath: b.dataset.restore })));
  } catch (err) {
    box.innerHTML = `<div class="empty"><div class="empty-icon text-danger">${icon('alert', 20)}</div>
      <div class="empty-title">读取失败</div>
      <div class="empty-desc">${esc(err.message)}</div></div>`;
  }
}

async function openPreview(profileId, remotePath) {
  const m = openModal({
    title: '归档预览',
    subtitle: remotePath,
    width: 'wide',
    body: '<div class="empty" style="padding:30px"><div class="boot-spinner"></div><div class="empty-desc">正在下载并解析归档，请稍候…</div></div>',
    footer: `<button class="btn" id="pv-close">关闭</button>
             <button class="btn btn-primary" id="pv-restore" disabled>${icon('download', 14)}恢复此归档</button>`,
  });

  $('#pv-close', m.el).addEventListener('click', m.close);

  try {
    const p = await api('/api/preview', {
      method: 'POST',
      body: { profile_id: profileId, remote_path: remotePath },
    });
    const mf = p.manifest;
    $('.modal-body', m.el).innerHTML = `
      <div class="kv" style="margin-bottom:16px">
        <div class="kv-row"><span class="kv-key">归档大小</span><span class="kv-val">${fmtBytes(p.total_size)}</span></div>
        ${mf ? `
          <div class="kv-row"><span class="kv-key">创建时间</span><span class="kv-val">${esc(fmtTimeFull(mf.created_at))}</span></div>
          <div class="kv-row"><span class="kv-key">来源主机</span><span class="kv-val">${esc(mf.hostname || '未知')}</span></div>
          <div class="kv-row"><span class="kv-key">原始路径</span><span class="kv-val mono" style="font-size:12px">${(mf.roots || []).map(esc).join('<br>')}</span></div>
          <div class="kv-row"><span class="kv-key">文件数量</span><span class="kv-val">${mf.file_count}</span></div>
          <div class="kv-row"><span class="kv-key">原始体积</span><span class="kv-val">${fmtBytes(mf.total_bytes)}</span></div>
          ${mf.errors && mf.errors.length ? `<div class="kv-row"><span class="kv-key">备份时告警</span>
            <span class="kv-val text-warning">${mf.errors.length} 条</span></div>` : ''}
        ` : `<div class="kv-row"><span class="kv-key">清单</span>
          <span class="kv-val text-warning">未找到内置清单，可能是其他工具生成的归档</span></div>`}
      </div>

      <div class="label" style="margin-bottom:7px">内容（前 ${(p.entries || []).length} 项${p.entries_truncated ? '，已截断' : ''}）</div>
      <div class="log-view" style="max-height:260px">${(p.entries || []).map(esc).join('\n') || '（空）'}</div>`;

    const btn = $('#pv-restore', m.el);
    btn.disabled = false;
    btn.addEventListener('click', () => {
      m.close();
      openRestoreWizard({ profileId, remotePath });
    });
  } catch (err) {
    $('.modal-body', m.el).innerHTML =
      `<div class="banner banner-danger">${icon('alert', 16)}<div>${esc(err.message)}</div></div>`;
  }
}

function openRestoreWizard(preset = {}) {
  if (!S.profiles.length) { toast('请先添加 WebDAV 配置', 'warning'); return; }

  let pickedProfile = preset.profileId || (S.profiles[0] && S.profiles[0].id);

  const m = openModal({
    title: '恢复归档',
    subtitle: '从 WebDAV 远端解包到本机目录',
    width: 'wide',
    body: `
      <div class="banner banner-warning" style="margin-bottom:18px">${icon('alert', 16)}
        <div><strong>恢复操作会写入本机文件系统。</strong>
        强烈建议先用「试运行」确认将要写入的文件，再执行正式恢复。</div></div>

      <div class="field">
        <label class="label">WebDAV 配置</label>
        <select class="select" id="rs-profile">
          ${S.profiles.map((p) => `<option value="${esc(p.id)}" ${p.id === pickedProfile ? 'selected' : ''}>
            ${esc(p.name)} — ${esc(p.url)}</option>`).join('')}
        </select>
      </div>

      <div class="field">
        <label class="label">远端归档路径 <span class="req">*</span></label>
        <div class="token-add">
          <input class="input mono" id="rs-path" value="${esc(preset.remotePath || '')}"
                 placeholder="backups/nightly-20261002-030000000.tar.gz">
          <button class="btn" id="rs-browse">${icon('search', 14)}选择</button>
        </div>
        <p class="hint">相对所选配置根地址的路径。点击「选择」可从远端已有归档中挑一个。</p>
      </div>

      <div class="field">
        <label class="label">恢复到本机目录 <span class="req">*</span></label>
        <div class="token-add">
          <input class="input mono" id="rs-dest" placeholder="/tmp/restore-test">
          <button class="btn" id="rs-browse-local">${icon('folder', 14)}浏览</button>
        </div>
        <p class="hint warn">
          归档内保留了完整路径（如 <code>etc/config/network</code>）。
          恢复到 <code>/</code> 会原地还原系统配置，请务必先用试运行确认。
        </p>
      </div>

      <div class="form-grid">
        <div class="field">
          <label class="label" for="rs-strip">去掉前 N 层路径</label>
          <input class="input" id="rs-strip" type="number" min="0" max="10" value="0">
        </div>
        <div class="field">
          <label class="label">选项</label>
          <label class="check">
            <input type="checkbox" id="rs-overwrite" checked>
            <span class="check-text"><span class="check-title">覆盖已存在的文件</span>
            <span class="check-desc">关闭时同名文件会被跳过</span></span>
          </label>
          <label class="check">
            <input type="checkbox" id="rs-dry">
            <span class="check-text"><span class="check-title">试运行</span>
            <span class="check-desc">只列出将要写入的内容，不修改任何文件</span></span>
          </label>
        </div>
      </div>

      <div id="rs-result"></div>`,

    footer: `
      <button class="btn left" id="rs-cancel">取消</button>
      <button class="btn" id="rs-verify">${icon('shield', 14)}仅校验归档</button>
      <button class="btn btn-primary" id="rs-go">${icon('play', 14)}开始恢复</button>`,
  });

  const el = m.el;
  $('#rs-cancel', el).addEventListener('click', m.close);

  $('#rs-browse', el).addEventListener('click', () => {
    const pid = $('#rs-profile', el).value;
    const list = S.archives.filter((a) => a.profile_id === pid);
    if (!list.length) {
      toast('请先在「备份仓库」中列出该配置的归档', 'info');
      return;
    }
    openModal({
      title: '选择远端归档',
      width: 'wide',
      body: `<div class="picker-list">${list.map((a) => `
        <div class="picker-row" data-p="${esc(a.path)}">
          <span style="color:var(--text-3)">${icon('file', 14)}</span>
          <span class="picker-name mono" style="font-size:12px">${esc(a.name)}</span>
          <span class="picker-meta">${fmtBytes(a.size)} · ${esc(fmtTime(a.modified))}</span>
        </div>`).join('')}</div>`,
      onMount({ el: inner, close }) {
        inner.addEventListener('click', (e) => {
          const row = e.target.closest('[data-p]');
          if (!row) return;
          $('#rs-path', el).value = row.dataset.p;
          close();
        });
      },
    });
  });

  $('#rs-browse-local', el).addEventListener('click', () => {
    openPathPicker({
      initial: $('#rs-dest', el).value || '/',
      onPick: (paths) => { $('#rs-dest', el).value = paths[0]; },
    });
  });

  async function submit(verifyOnly) {
    const body = {
      profile_id: $('#rs-profile', el).value,
      remote_path: $('#rs-path', el).value.trim(),
      dest_dir: $('#rs-dest', el).value.trim(),
      overwrite: $('#rs-overwrite', el).checked,
      dry_run: $('#rs-dry', el).checked,
      strip_components: parseInt($('#rs-strip', el).value, 10) || 0,
    };
    if (!body.remote_path) { toast('请填写远端归档路径', 'warning'); return; }
    if (!body.dest_dir) { toast('请选择恢复目标目录', 'warning'); return; }
    if (verifyOnly) { body.dry_run = true; }

    const box = $('#rs-result', el);
    box.innerHTML = `<div class="banner banner-info" style="margin-top:14px">${icon('info', 16)}
      <div>正在下载归档并执行…大文件可能需要几分钟，请勿关闭页面。</div></div>`;

    try {
      const r = await api('/api/restore', { method: 'POST', body });
      const run = r.run;
      toast('恢复任务已启动', 'success');
      m.close();
      navTo('#/runs/' + encodeURIComponent(run.id));
      route();
    } catch (err) {
      box.innerHTML = `<div class="banner banner-danger" style="margin-top:14px">${icon('alert', 16)}
        <div><strong>无法启动恢复</strong><br>${esc(err.message)}</div></div>`;
    }
  }

  $('#rs-go', el).addEventListener('click', () => submit(false));
  $('#rs-verify', el).addEventListener('click', () => submit(true));
}

function openRestoreFromJob() {
  const jobs = S.jobs.filter((j) => j.enabled || j.last_run_at);
  if (!jobs.length) { toast('还没有可用的备份任务', 'info'); return; }

  openModal({
    title: '从任务的最新备份恢复',
    subtitle: '自动定位该任务最近一次上传的归档',
    width: 'wide',
    body: `<div class="picker-list">${jobs.map((j) => `
      <div class="picker-row" data-job="${esc(j.id)}">
        <span style="color:var(--text-3)">${icon('jobs', 14)}</span>
        <span class="picker-name">
          <strong>${esc(j.name)}</strong>
          <div class="row-sub">${esc((j.source.paths || []).join('  '))}</div>
        </span>
        <span class="picker-meta">${j.last_run_at ? esc(relTime(j.last_run_at)) : '未执行'}</span>
      </div>`).join('')}</div>`,
    onMount({ el, close }) {
      el.addEventListener('click', async (e) => {
        const row = e.target.closest('[data-job]');
        if (!row) return;
        const jobId = row.dataset.job;
        close();
        try {
          const r = await api(`/api/archives?job_id=${encodeURIComponent(jobId)}`);
          const list = r.archives || [];
          if (!list.length) {
            toast('该任务的远端目录中还没有归档文件', 'warning');
            return;
          }
          list.sort((a, b) => (a.name < b.name ? 1 : -1));
          const newest = list[0];
          openRestoreWizard({ profileId: newest.profile_id, remotePath: newest.path });
          setTimeout(() => toast(`已选中最新归档 ${newest.name}`, 'info'), 250);
        } catch (err) {
          toast(err.message, 'error', '读取失败');
        }
      });
    },
  });
}

/* ------------------------------------------------------------------ 执行历史 */

async function renderRuns() {
  let runs = [];
  let loadError = null;
  try {
    const r = await api('/api/runs?limit=200');
    runs = r.runs || [];
  } catch (err) {
    loadError = err;
  }

  if (loadError) {
    renderShell('执行历史',
      `<div class="banner banner-danger">${icon('alert', 16)}
        <div><strong>无法加载执行历史</strong><br>${esc(loadError.message)}</div></div>`);
    return;
  }

  const filter = S.runFilter || 'all';
  const shown = filter === 'all' ? runs : runs.filter((r) => r.type === filter);

  const body = `
    <div class="card">
      <div class="card-head">
        <h2>执行历史</h2>
        <p class="sub">共 ${runs.length} 条记录${filter !== 'all' ? `，显示 ${shown.length} 条` : ''}</p>
        <div class="card-head-actions">
          <div class="segmented" id="rn-filter">
            <button data-f="all" class="${filter === 'all' ? 'on' : ''}">全部</button>
            <button data-f="backup" class="${filter === 'backup' ? 'on' : ''}">备份</button>
            <button data-f="restore" class="${filter === 'restore' ? 'on' : ''}">恢复</button>
          </div>
          <button class="btn btn-sm btn-danger" id="rn-clearall" ${runs.length ? '' : 'disabled'}
            title="删除全部执行记录与日志">${icon('trash', 14)}清除全部</button>
          <button class="btn btn-sm" id="rn-refresh">${icon('refresh', 14)}刷新</button>
        </div>
      </div>
      <div class="card-body tight">
        ${shown.length ? `
        <div class="table-wrap"><table class="table">
          <thead><tr>
            <th>任务</th><th>类型</th><th>状态</th><th>开始</th><th class="num">文件数</th>
            <th class="num">体积</th><th class="num">用时</th><th class="actions"></th>
          </tr></thead>
          <tbody>${shown.map((r) => `
            <tr>
              <td class="name-cell">
                <div class="row-title">${esc(r.job_name || '—')}</div>
                <div class="row-sub">${esc(r.message || r.remote_path || r.restore_from || '')}</div>
              </td>
              <td><span class="badge ${r.type === 'restore' ? 'badge-primary' : ''}">
                ${r.type === 'restore' ? '恢复' : '备份'}</span></td>
              <td>${statusBadge(r)}</td>
              <td class="nowrap text-muted">${esc(fmtTimeFull(r.started_at))}</td>
              <td class="num">${r.file_count || '—'}</td>
              <td class="num">${r.archive_size ? fmtBytes(r.archive_size) : '—'}</td>
              <td class="num">${r.duration_ms ? esc(fmtDuration(r.duration_ms)) : '—'}</td>
              <td class="actions">
                <button class="btn btn-sm" data-log="${esc(r.id)}">${icon('file', 13)}日志</button>
                <button class="btn btn-sm btn-danger" data-delrun="${esc(r.id)}"
                  ${r.status === 'running' ? 'disabled' : ''}>${icon('trash', 13)}</button>
              </td>
            </tr>`).join('')}</tbody>
        </table></div>` : `
        <div class="empty"><div class="empty-icon">${icon('history', 20)}</div>
          <div class="empty-title">${filter === 'all' ? '还没有执行记录' : '没有这类记录'}</div>
          <div class="empty-desc">运行任意备份或恢复任务后，这里会记录每一次的结果与日志。</div></div>`}
      </div>
    </div>`;

  renderShell('执行历史', body);

  const app = $('#app');
  $('#rn-refresh', app).addEventListener('click', async () => { await route(); toast('已刷新', 'info'); });

  $('#rn-filter', app).addEventListener('click', (e) => {
    const btn = e.target.closest('button[data-f]');
    if (!btn) return;
    S.runFilter = btn.dataset.f;
    route();
  });

  $('#rn-clearall', app)?.addEventListener('click', async () => {
    const ok = await confirmModal({
      title: '清除全部执行历史',
      message: `将删除全部 ${runs.length} 条（含未显示的）执行记录及其日志，此操作不可恢复。`,
      detail: '任务配置不受影响，仅清理执行历史。',
      confirmText: '全部清除', danger: true,
    });
    if (!ok) return;
    try {
      const r = await api('/api/runs', { method: 'DELETE' });
      toast(`已清除 ${r.deleted ?? runs.length} 条记录`, 'success');
      await route();
    } catch (err) { toast(err.message, 'error', '清除失败'); }
  });

  $$('[data-log]', app).forEach((b) =>
    b.addEventListener('click', () => navTo('#/runs/' + encodeURIComponent(b.dataset.log))));

  $$('[data-delrun]', app).forEach((b) => b.addEventListener('click', async () => {
    const ok = await confirmModal({
      title: '删除执行记录', message: '确定要删除这条记录及其日志吗？',
      confirmText: '删除', danger: true,
    });
    if (!ok) return;
    try {
      await api(`/api/runs/${encodeURIComponent(b.dataset.delrun)}`, { method: 'DELETE' });
      toast('记录已删除', 'success');
      await route();
    } catch (err) { toast(err.message, 'error', '删除失败'); }
  }));
}

let runDetailTimer = null;

const STAGE_LABEL = {
  starting: '准备中', connecting: '连接远端', preparing: '准备远端目录',
  archiving: '打包中', uploading: '上传中', pruning: '清理旧版本',
  downloading: '下载中', verifying: '校验中', extracting: '解包中', done: '完成',
};

async function renderRunDetail(runId) {
  if (runDetailTimer) { clearInterval(runDetailTimer); runDetailTimer = null; }

  S.logOffset = 0;
  S.logRunID = runId;
  let logText = '';

  renderShell('执行详情', `
    <div id="rd-meta"></div>
    <div class="card">
      <div class="card-head">
        <h2>运行日志</h2>
        <span id="rd-live"></span>
        <div class="card-head-actions">
          <button class="btn btn-sm" id="rd-back">返回历史</button>
          <button class="btn btn-sm btn-ghost" id="rd-log-end">滚动到底部</button>
        </div>
      </div>
      <div class="card-body">
        <pre class="log-view" id="rd-log">正在读取日志…</pre>
      </div>
    </div>`);

  const app = $('#app');
  const stop = () => {
    if (runDetailTimer) { clearInterval(runDetailTimer); runDetailTimer = null; }
  };

  $('#rd-back', app).addEventListener('click', () => { stop(); navTo('#/runs'); });
  $('#rd-log-end', app).addEventListener('click', () => {
    const el = $('#rd-log');
    if (el) el.scrollTop = el.scrollHeight;
  });

  // paintMeta() rebuilds the meta card via innerHTML on every 1.5s poll tick,
  // so any listener bound to #rd-cancel dies with the element it belonged to.
  // Re-bind on every paint, and remember a pending cancel so the button turns
  // into a disabled "终止中…" instead of letting the user fire it repeatedly.
  let cancelRequested = false;

  function paintMeta(run, isRunning) {
    const meta = $('#rd-meta');
    if (!meta) return;
    meta.innerHTML = `
      <div class="card">
        <div class="card-head">
          <h2>${esc(run.job_name || '执行记录')}</h2>
          ${statusBadge(run)}
          <div class="card-head-actions">
            ${isRunning ? `<button class="btn btn-sm btn-danger" id="rd-cancel"${cancelRequested ? ' disabled' : ''}>
              ${icon('stop', 13)}${cancelRequested ? '终止中…' : '终止任务'}</button>` : ''}
          </div>
        </div>
        <div class="card-body">
          <div class="kv">
            <div class="kv-row"><span class="kv-key">类型</span>
              <span class="kv-val">${run.type === 'restore' ? '恢复' : '备份'}
                · ${run.trigger === 'schedule' ? '定时触发' : '手动触发'}</span></div>
            <div class="kv-row"><span class="kv-key">开始时间</span>
              <span class="kv-val">${esc(fmtTimeFull(run.started_at))}</span></div>
            ${run.finished_at ? `<div class="kv-row"><span class="kv-key">结束时间</span>
              <span class="kv-val">${esc(fmtTimeFull(run.finished_at))}</span></div>` : ''}
            <div class="kv-row"><span class="kv-key">用时</span>
              <span class="kv-val">${run.duration_ms ? esc(fmtDuration(run.duration_ms))
                : (isRunning ? '进行中…' : '—')}</span></div>
            ${isRunning && run.stage ? `<div class="kv-row"><span class="kv-key">当前阶段</span>
              <span class="kv-val">${esc(STAGE_LABEL[run.stage] || run.stage)}</span></div>` : ''}
            ${run.remote_path ? `<div class="kv-row"><span class="kv-key">远端文件</span>
              <span class="kv-val mono" style="font-size:12px">/${esc(String(run.remote_path).replace(/^\//, ''))}</span></div>` : ''}
            ${run.restore_from ? `<div class="kv-row"><span class="kv-key">恢复来源</span>
              <span class="kv-val mono" style="font-size:12px">${esc(run.restore_from)}</span></div>` : ''}
            ${run.restore_dest ? `<div class="kv-row"><span class="kv-key">恢复目标</span>
              <span class="kv-val mono" style="font-size:12px">${esc(run.restore_dest)}${run.dry_run ? '（试运行）' : ''}</span></div>` : ''}
            ${run.file_count ? `<div class="kv-row"><span class="kv-key">文件数量</span>
              <span class="kv-val">${run.file_count} 个文件${run.dir_count ? ` / ${run.dir_count} 个目录` : ''}${run.skipped ? ` · 跳过 ${run.skipped}` : ''}</span></div>` : ''}
            ${run.raw_size ? `<div class="kv-row"><span class="kv-key">原始体积</span>
              <span class="kv-val">${fmtBytes(run.raw_size)}</span></div>` : ''}
            ${run.archive_size ? `<div class="kv-row"><span class="kv-key">归档体积</span>
              <span class="kv-val">${fmtBytes(run.archive_size)}${run.raw_size
                ? `（压缩至 ${(run.archive_size / run.raw_size * 100).toFixed(0)}%）` : ''}</span></div>` : ''}
            ${run.message ? `<div class="kv-row"><span class="kv-key">结果说明</span>
              <span class="kv-val ${run.status === 'failed' ? 'text-danger'
                : run.status === 'partial' ? 'text-warning' : ''}">${esc(run.message)}</span></div>` : ''}
          </div>

          ${run.errors && run.errors.length ? `
            <div class="divider"></div>
            <div class="label" style="margin-bottom:7px">读取告警（共 ${run.error_count || run.errors.length} 条）</div>
            <pre class="log-view" style="max-height:170px;margin:0">${esc(run.errors.join('\n'))}</pre>` : ''}
        </div>
      </div>`;

    if (isRunning && !cancelRequested) {
      $('#rd-cancel', $('#app'))?.addEventListener('click', async (e) => {
        const btn = e.currentTarget;
        if (btn) btn.disabled = true;
        try {
          await api(`/api/jobs/${encodeURIComponent(run.job_id)}/cancel`, { method: 'POST' });
          cancelRequested = true;
          toast('已发出终止请求，等待任务退出…', 'info');
          paintMeta(run, true);
        } catch (err) {
          toast(err.message, 'error', '终止失败');
          if (btn) btn.disabled = false;
        }
      });
    }
  }

  function paintLive(run, isRunning) {
    const el = $('#rd-live');
    if (!el) return;
    el.outerHTML = isRunning
      ? `<span id="rd-live" class="badge badge-primary badge-running"><span class="badge-dot"></span>实时更新</span>`
      : `<span id="rd-live" class="badge badge-success">${icon('check', 12)}已完成</span>`;
  }

  function paintLog(text) {
    const el = $('#rd-log');
    if (!el) return;
    const atBottom = el.scrollTop + el.clientHeight >= el.scrollHeight - 40;
    // textContent keeps the log inert: archive contents are attacker-influenced
    // data and must never be interpreted as markup.
    el.textContent = text || '（暂无日志输出）';
    if (atBottom) el.scrollTop = el.scrollHeight;
    else $('#rd-log-end', $('#app'))?.classList.remove('hidden');
  }

  async function poll() {
    let run;
    try {
      run = await api(`/api/runs/${encodeURIComponent(runId)}`);
    } catch (err) {
      stop();
      if (err.code === 404) {
        renderShell('记录不存在',
          `<div class="banner banner-danger">${icon('alert', 16)}
            <div>找不到该执行记录，可能已被清理。</div></div>
           <button class="btn" id="rd-back2">返回历史</button>`);
        $('#rd-back2').addEventListener('click', () => navTo('#/runs'));
      } else {
        toast(err.message, 'error', '无法读取执行记录');
      }
      return;
    }

    try {
      const log = await api(`/api/runs/${encodeURIComponent(runId)}/log?offset=${S.logOffset}`);
      if (log.text) {
        logText += log.text;
        S.logOffset = log.offset + new Blob([log.text]).size;
      }
    } catch { /* log tail failures are non-fatal */ }

    const isRunning = run.status === 'running';
    paintMeta(run, isRunning);
    paintLive(run, isRunning);
    paintLog(logText);

    if (!isRunning) stop();
    return isRunning;
  }

  // Keep refreshing only while the run is in flight.
  if (await poll()) {
    runDetailTimer = setInterval(poll, 1500);
  }
}

/* ------------------------------------------------------------------ WebDAV 配置 */

function renderProfiles() {
  const body = `
    <div class="card">
      <div class="card-head">
        <h2>WebDAV 配置</h2>
        <p class="sub">远端存储的连接信息，可被多个任务共用</p>
        <div class="card-head-actions">
          <button class="btn btn-sm btn-primary" id="pf-new">${icon('plus', 14)}添加配置</button>
        </div>
      </div>
      <div class="card-body tight">
        ${S.profiles.length ? `
        <div class="table-wrap"><table class="table">
          <thead><tr>
            <th>名称</th><th>地址</th><th>账号</th><th class="num">关联任务</th><th class="actions">操作</th>
          </tr></thead>
          <tbody>${S.profiles.map((p) => `
            <tr>
              <td class="name-cell"><div class="row-title">${esc(p.name)}</div>
                <div class="row-sub">${p.insecure_tls ? '已跳过 TLS 校验' : `超时 ${p.timeout_sec || 120} 秒`}</div></td>
              <td class="wrap mono" style="font-size:12px">${esc(p.url)}</td>
              <td>${esc(p.username || '—')}
                ${p.has_password ? '' : '<span class="badge badge-warning">无密码</span>'}</td>
              <td class="num">${p.used_by_jobs}</td>
              <td class="actions">
                <button class="btn btn-sm" data-test="${esc(p.id)}">${icon('link', 13)}测试</button>
                <button class="btn btn-sm" data-pedit="${esc(p.id)}">${icon('edit', 13)}</button>
                <button class="btn btn-sm btn-danger" data-pdel="${esc(p.id)}">${icon('trash', 13)}</button>
              </td>
            </tr>`).join('')}</tbody>
        </table></div>` : `
        <div class="empty">
          <div class="empty-icon">${icon('cloud', 20)}</div>
          <div class="empty-title">还没有 WebDAV 配置</div>
          <div class="empty-desc">填写远端 WebDAV 服务的地址与账号，例如坚果云、Nextcloud、Alist 或自建的 WebDAV 服务。</div>
          <button class="btn btn-primary" id="pf-new2">${icon('plus', 14)}添加配置</button>
        </div>`}
      </div>
    </div>

    <div class="card">
      <div class="card-head"><h2>常见服务地址示例</h2></div>
      <div class="card-body">
        <div class="kv">
          <div class="kv-row"><span class="kv-key">坚果云</span>
            <span class="kv-val mono" style="font-size:12px">https://dav.jianguoyun.com/dav/</span></div>
          <div class="kv-row"><span class="kv-key">Nextcloud</span>
            <span class="kv-val mono" style="font-size:12px">https://cloud.example.com/remote.php/dav/files/&lt;用户&gt;/</span></div>
          <div class="kv-row"><span class="kv-key">Alist</span>
            <span class="kv-val mono" style="font-size:12px">http://192.168.1.10:5244/dav</span></div>
          <div class="kv-row"><span class="kv-key">群晖 WebDAV</span>
            <span class="kv-val mono" style="font-size:12px">http://192.168.1.20:5005/</span></div>
          <div class="kv-row"><span class="kv-key">本地测试</span>
            <span class="kv-val mono" style="font-size:12px">http://127.0.0.1:8080/dav</span></div>
        </div>
        <p class="hint" style="margin-top:12px">
          提示：坚果云等服务需要使用「应用密码」而非登录密码。地址请带上完整路径前缀，
          工具会在其下创建目录。
        </p>
      </div>
    </div>`;

  renderShell('WebDAV 配置', body);

  const app = $('#app');
  $('#pf-new', app)?.addEventListener('click', () => openProfileEditor(null));
  $('#pf-new2', app)?.addEventListener('click', () => openProfileEditor(null));

  $$('[data-pedit]', app).forEach((b) => b.addEventListener('click', () => {
    const p = S.profiles.find((x) => x.id === b.dataset.pedit);
    if (p) openProfileEditor(p);
  }));

  $$('[data-pdel]', app).forEach((b) => b.addEventListener('click', async () => {
    const p = S.profiles.find((x) => x.id === b.dataset.pdel);
    if (!p) return;
    const ok = await confirmModal({
      title: '删除 WebDAV 配置',
      message: `确定要删除「${p.name}」吗？`,
      detail: p.used_by_jobs ? `有 ${p.used_by_jobs} 个任务正在使用这个配置，需要先修改或删除这些任务。` : '',
      confirmText: '删除', danger: true,
    });
    if (!ok) return;
    try {
      await api(`/api/profiles/${encodeURIComponent(p.id)}`, { method: 'DELETE' });
      toast('配置已删除', 'success');
      await route();
    } catch (err) { toast(err.message, 'error', '删除失败'); }
  }));

  $$('[data-test]', app).forEach((b) => b.addEventListener('click', () => {
    const p = S.profiles.find((x) => x.id === b.dataset.test);
    if (p) testProfile(p.id, null);
  }));
}

function openProfileEditor(profile) {
  const isNew = !profile;
  const p = profile || { name: '', url: '', username: '', insecure_tls: false, timeout_sec: 120 };

  const m = openModal({
    title: isNew ? '添加 WebDAV 配置' : '编辑：' + p.name,
    width: 'wide',
    body: `
      <div class="field">
        <label class="label" for="p-name">名称 <span class="req">*</span></label>
        <input class="input" id="p-name" value="${esc(p.name)}" placeholder="例如 坚果云备份">
      </div>

      <div class="field">
        <label class="label" for="p-url">WebDAV 地址 <span class="req">*</span></label>
        <input class="input mono" id="p-url" value="${esc(p.url)}" placeholder="https://dav.jianguoyun.com/dav/">
        <p class="hint">必须包含 http:// 或 https:// 前缀，建议以 / 结尾。</p>
      </div>

      <div class="form-grid">
        <div class="field">
          <label class="label" for="p-user">用户名</label>
          <input class="input" id="p-user" value="${esc(p.username || '')}" autocomplete="off">
        </div>
        <div class="field">
          <label class="label" for="p-pass">密码</label>
          <input class="input" id="p-pass" type="password" autocomplete="new-password"
                 placeholder="${p.has_password ? '已保存，留空表示不修改' : '请输入密码'}">
          ${p.has_password ? `<label class="check" style="margin-top:2px">
            <input type="checkbox" id="p-clear"><span class="check-text">
            <span class="check-desc">清除已保存的密码</span></span></label>` : ''}
        </div>
      </div>

      <div class="form-grid">
        <div class="field">
          <label class="label" for="p-timeout">超时（秒）</label>
          <input class="input" id="p-timeout" type="number" min="5" max="3600" value="${p.timeout_sec || 120}">
          <p class="hint">连接与响应头超时，不影响大文件传输总时长。</p>
        </div>
        <div class="field">
          <label class="label">安全</label>
          <label class="check">
            <input type="checkbox" id="p-insecure" ${p.insecure_tls ? 'checked' : ''}>
            <span class="check-text"><span class="check-title">跳过 TLS 证书校验</span>
            <span class="check-desc">仅用于自签名证书的内网服务</span></span>
          </label>
        </div>
      </div>

      <div id="p-test-result"></div>`,
    footer: `
      <button class="btn left" id="p-test">${icon('link', 14)}测试连接</button>
      <button class="btn" id="p-cancel">取消</button>
      <button class="btn btn-primary" id="p-save">${icon('check', 14)}保存</button>`,
  });

  const el = m.el;
  $('#p-cancel', el).addEventListener('click', m.close);

  function collect() {
    const body = {
      name: $('#p-name', el).value.trim(),
      url: $('#p-url', el).value.trim(),
      username: $('#p-user', el).value.trim(),
      insecure_tls: $('#p-insecure', el).checked,
      timeout_sec: parseInt($('#p-timeout', el).value, 10) || 120,
    };
    const pass = $('#p-pass', el).value;
    if (pass) body.password = pass;
    if ($('#p-clear', el)?.checked) body.clear_password = true;
    return body;
  }

  $('#p-test', el).addEventListener('click', async () => {
    const box = $('#p-test-result', el);
    const body = collect();
    box.innerHTML = `<div class="banner banner-info" style="margin-top:14px">${icon('info', 16)}
      <div>正在连接…</div></div>`;
    try {
      const r = await api(`/api/profiles/${isNew ? 'new' : encodeURIComponent(p.id)}/test`, {
        method: 'POST', body,
      });
      if (r.ok) {
        box.innerHTML = `<div class="banner banner-info" style="margin-top:14px;background:var(--success-soft)">
          ${icon('check', 16)}<div><strong>连接成功</strong><br>
          ${r.archives !== undefined ? `该目录已有 ${r.archives} 个归档，合计 ${fmtBytes(r.archive_bytes || 0)}` : ''}
          ${r.list_warning ? `<br><span class="text-warning">${esc(r.list_warning)}</span>` : ''}
          </div></div>`;
      } else {
        box.innerHTML = `<div class="banner banner-danger" style="margin-top:14px">${icon('alert', 16)}
          <div><strong>连接失败</strong><br>${esc(r.error || '未知错误')}</div></div>`;
      }
    } catch (err) {
      box.innerHTML = `<div class="banner banner-danger" style="margin-top:14px">${icon('alert', 16)}
        <div>${esc(err.message)}</div></div>`;
    }
  });

  $('#p-save', el).addEventListener('click', async () => {
    const body = collect();
    if (!body.name) { toast('请填写名称', 'warning'); return; }
    if (!body.url) { toast('请填写 WebDAV 地址', 'warning'); return; }
    try {
      if (isNew) await api('/api/profiles', { method: 'POST', body });
      else await api(`/api/profiles/${encodeURIComponent(p.id)}`, { method: 'PUT', body });
      toast('配置已保存', 'success');
      m.close();
      await route();
    } catch (err) { toast(err.message, 'error', '保存失败'); }
  });
}

async function testProfile(profileId, btn) {
  if (btn) btn.disabled = true;
  toast('正在测试连接…', 'info');
  try {
    const r = await api(`/api/profiles/${encodeURIComponent(profileId)}/test`, {
      method: 'POST', body: {},
    });
    if (r.ok) {
      // "归档"指的是 .tar.gz 备份包，不是目录。档案里不存远端目录（那是任务
      // 的字段），所以这里看到的通常是 WebDAV 根目录 —— 归档数为 0 是正常的，
      // 说明还没跑过第一次备份。措辞必须把这层意思说清，否则会被理解成
      // "没检测到目录"。
      const where = r.base_dir ? `远端目录 /${r.base_dir}` : 'WebDAV 根目录';
      let msg = '连接成功，认证通过';
      if (r.list_warning) {
        msg += `；${r.list_warning}`;
      } else if (r.entries !== undefined) {
        msg += `；${where}共 ${r.entries} 个条目`;
        if (r.archives !== undefined) {
          msg += `，其中备份归档 ${r.archives} 个${r.archives === 0 ? '（还没跑过备份）' : ''}`;
        }
        // 列出前几个条目（目录会标注出来），用户就不用再去网盘里核对
        // 到底有没有建成、目录是不是真的在预期位置。
        if (Array.isArray(r.names) && r.names.length > 0) {
          const shown = r.names.slice(0, 6).join('、');
          const more = r.names.length > 6 ? ` 等 ${r.names.length} 项` : '';
          msg += `：${shown}${more}`;
        }
      }
      toast(msg, 'success', '测试通过');
    } else {
      toast(r.error || '未知错误', 'error', '连接失败');
    }
  } catch (err) {
    toast(err.message, 'error', '测试失败');
  } finally {
    if (btn) btn.disabled = false;
  }
}

/* ------------------------------------------------------------------ 设置 */

async function renderSettings() {
  if (!S.settings) {
    try { S.settings = await api('/api/settings'); } catch { S.settings = {}; }
  }
  const st = S.status || {};
  const settings = S.settings || {};
  const body = `
    <div class="card">
      <div class="card-head"><h2>系统信息</h2></div>
      <div class="card-body">
        <div class="kv">
          <div class="kv-row"><span class="kv-key">版本</span><span class="kv-val">v${esc(st.version || 'dev')}</span></div>
          <div class="kv-row"><span class="kv-key">运行时长</span><span class="kv-val">${esc(fmtUptime(st.uptime_seconds))}</span></div>
          <div class="kv-row"><span class="kv-key">监听地址</span><span class="kv-val mono">${esc(st.listen || '—')}</span></div>
          <div class="kv-row"><span class="kv-key">数据目录</span><span class="kv-val mono">${esc(st.data_dir || '—')}</span></div>
          <div class="kv-row"><span class="kv-key">暂存目录</span><span class="kv-val mono">${esc(st.temp_dir || (st.data_dir || '') + '/tmp')}</span></div>
          <div class="kv-row"><span class="kv-key">运行平台</span><span class="kv-val mono">${esc(st.platform || '—')} · ${esc(st.go_version || '')}</span></div>
          ${st.disk && st.disk.supported ? `
          <div class="kv-row"><span class="kv-key">存储使用</span>
            <span class="kv-val">${fmtBytes(st.disk.used)} / ${fmtBytes(st.disk.total)}
              （${st.disk.used_percent.toFixed(1)}%，剩余 ${fmtBytes(st.disk.free)}）</span></div>` : ''}
        </div>
      </div>
    </div>

    <div class="card">
      <div class="card-head"><h2>账号安全</h2></div>
      <div class="card-body">
        <div class="form-grid">
          <div class="field">
            <label class="label" for="s-user">管理员用户名</label>
            <input class="input" id="s-user" value="${esc(S.me.username || 'admin')}">
          </div>
          <div class="field">
            <label class="label">&nbsp;</label>
            <button class="btn" id="s-saveuser">${icon('check', 14)}保存用户名</button>
          </div>
        </div>

        <div class="divider"></div>

        <div class="banner banner-info" style="margin-bottom:16px">${icon('shield', 16)}
          <div><strong>修改密码</strong><br>
          修改成功后，除当前浏览器外的其他登录会话都会失效。</div></div>

        <div class="form-grid">
          <div class="field">
            <label class="label" for="s-cur">当前密码</label>
            <input class="input" id="s-cur" type="password" autocomplete="current-password">
          </div>
          <div class="field">
            <label class="label" for="s-new">新密码（至少 5 位）</label>
            <input class="input" id="s-new" type="password" autocomplete="new-password">
          </div>
          <div class="field">
            <label class="label" for="s-new2">确认新密码</label>
            <input class="input" id="s-new2" type="password" autocomplete="new-password">
          </div>
        </div>
        <div class="toolbar" style="margin-top:12px">
          <button class="btn btn-primary" id="s-pass">${icon('key', 14)}修改密码</button>
        </div>
      </div>
    </div>

    <div class="card">
      <div class="card-head"><h2>任务通知</h2></div>
      <div class="card-body">
        <div class="form-grid">
          <div class="field">
            <label class="label" for="s-notify-url">Webhook 地址</label>
            <input class="input mono" id="s-notify-url" value="${esc((settings.notify && settings.notify.url) || '')}"
              placeholder="https://…（留空关闭通知）">
            <p class="hint">支持 <b>bark</b>（填个人推送地址 https://api.day.app/你的key）、
              <b>telegram</b>（填 https://api.telegram.org/bot&lt;token&gt;/sendMessage）、
              <b>wecom</b>（企业微信机器人地址）、<b>json</b>（任意接收 JSON POST 的地址）。</p>
          </div>
          <div class="field">
            <label class="label" for="s-notify-format">推送格式</label>
            <select class="input" id="s-notify-format">
              ${['json','bark','wecom','telegram'].map((f) =>
                `<option value="${f}" ${((settings.notify && settings.notify.format) || 'json') === f ? 'selected' : ''}>${f}</option>`).join('')}
            </select>
          </div>
          <div class="field">
            <label class="label" for="s-notify-chatid">Telegram Chat ID</label>
            <input class="input mono" id="s-notify-chatid" value="${esc((settings.notify && settings.notify.chat_id) || '')}"
              placeholder="仅 telegram 格式需要">
          </div>
          <div class="field">
            <label class="label">触发时机</label>
            <label class="check">
              <input type="checkbox" id="s-notify-fail" ${(!settings.notify || settings.notify.on_failure !== false) ? 'checked' : ''}>
              <span class="check-text"><span class="check-title">失败时通知</span>
              <span class="check-desc">备份或恢复失败时推送（建议开启）</span></span>
            </label>
            <label class="check">
              <input type="checkbox" id="s-notify-ok" ${(settings.notify && settings.notify.on_success) ? 'checked' : ''}>
              <span class="check-text"><span class="check-title">成功时通知</span>
              <span class="check-desc">每次成功也推送，任务多时可能扰民</span></span>
            </label>
          </div>
        </div>
        <div class="toolbar" style="margin-top:12px">
          <button class="btn btn-primary" id="s-notify-save">${icon('check', 14)}保存通知设置</button>
        </div>
      </div>
    </div>

    <div class="card">
      <div class="card-head"><h2>运行参数</h2></div>
      <div class="card-body">
        <div class="form-grid">
          <div class="field">
            <label class="label" for="s-temp">全局暂存目录</label>
            <input class="input mono" id="s-temp" value="${esc(settings.temp_dir || '')}" placeholder="留空使用数据目录下的 tmp">
            <p class="hint">打包时临时文件的位置。OpenWrt 上根分区通常很小，建议指向大容量挂载点，如 <code>/opt/qzrs-webdav-backup/tmp</code>。</p>
          </div>
          <div class="field">
            <label class="label" for="s-keep">执行历史保留条数</label>
            <input class="input" id="s-keep" type="number" min="10" max="5000" value="${settings.history_keep || 200}">
            <p class="hint">超出后自动清理最旧的记录与日志。</p>
          </div>
          <div class="field">
            <label class="label" for="s-ttl">登录有效期（小时）</label>
            <input class="input" id="s-ttl" type="number" min="1" max="720" value="${settings.session_ttl_hours || 72}">
          </div>
          <div class="field">
            <label class="label">反向代理</label>
            <label class="check">
              <input type="checkbox" id="s-proxy" ${settings.trusted_proxy ? 'checked' : ''}>
              <span class="check-text"><span class="check-title">信任 X-Forwarded-For</span>
              <span class="check-desc">仅在确认本服务位于你自己控制的反向代理之后时才启用</span></span>
            </label>
          </div>
        </div>
        <div class="toolbar" style="margin-top:14px">
          <button class="btn btn-primary" id="s-save">${icon('check', 14)}保存设置</button>
        </div>
      </div>
    </div>

    <details class="disclosure">
      <summary>配置文件与数据位置</summary>
      <div class="disclosure-body">
        <div class="kv">
          <div class="kv-row"><span class="kv-key">配置文件</span>
            <span class="kv-val mono" style="font-size:12px">${esc(settings.config_path || '')}</span></div>
          <div class="kv-row"><span class="kv-key">执行历史</span>
            <span class="kv-val mono" style="font-size:12px">${esc((st.data_dir || '') + '/state/runs')}</span></div>
          <div class="kv-row"><span class="kv-key">运行日志</span>
            <span class="kv-val mono" style="font-size:12px">${esc((st.data_dir || '') + '/state/logs')}</span></div>
        </div>
        <p class="hint" style="margin-top:10px">
          配置文件包含加密后的 WebDAV 凭据，权限为 0600，请勿公开分享。
          修改配置文件后需要重启服务才能完全生效。
        </p>
      </div>
    </details>`;

  renderShell('设置', body);

  const app = $('#app');

  $('#s-saveuser', app).addEventListener('click', async () => {
    const username = $('#s-user', app).value.trim();
    if (!username) { toast('用户名不能为空', 'warning'); return; }
    try {
      await api('/api/settings', { method: 'PUT', body: { username } });
      toast('用户名已更新', 'success');
      S.me.username = username;
      await route();
    } catch (err) { toast(err.message, 'error', '保存失败'); }
  });

  $('#s-pass', app).addEventListener('click', async () => {
    const cur = $('#s-cur', app).value;
    const nw = $('#s-new', app).value;
    const nw2 = $('#s-new2', app).value;
    if (!cur) { toast('请输入当前密码', 'warning'); return; }
    if (nw.length < 5) { toast('新密码至少 5 位', 'warning'); return; }
    if (nw !== nw2) { toast('两次输入的新密码不一致', 'warning'); return; }
    try {
      await api('/api/password', { method: 'POST', body: { current: cur, new: nw } });
      toast('密码已修改', 'success');
      $('#s-cur', app).value = ''; $('#s-new', app).value = ''; $('#s-new2', app).value = '';
    } catch (err) { toast(err.message, 'error', '修改失败'); }
  });

  $('#s-notify-save', app).addEventListener('click', async () => {
    const body = {
      notify: {
        url: $('#s-notify-url', app).value.trim(),
        format: $('#s-notify-format', app).value,
        chat_id: $('#s-notify-chatid', app).value.trim(),
        on_failure: $('#s-notify-fail', app).checked,
        on_success: $('#s-notify-ok', app).checked,
      },
    };
    try {
      await api('/api/settings', { method: 'PUT', body });
      toast('通知设置已保存', 'success');
      await route();
    } catch (err) { toast(err.message, 'error', '保存失败'); }
  });

  $('#s-save', app).addEventListener('click', async () => {
    const body = {
      temp_dir: $('#s-temp', app).value.trim(),
      history_keep: parseInt($('#s-keep', app).value, 10) || 200,
      session_ttl_hours: parseInt($('#s-ttl', app).value, 10) || 72,
      trusted_proxy: $('#s-proxy', app).checked,
    };
    try {
      S.settings = await api('/api/settings', { method: 'PUT', body });
      toast('设置已保存', 'success');
      await route();
    } catch (err) { toast(err.message, 'error', '保存失败'); }
  });
}

/* ------------------------------------------------------------------ 启动 */

(async function init() {
  try {
    await boot();
  } catch (err) {
    renderFatal(err);
  }
})();

window.addEventListener('beforeunload', stopPolling);
