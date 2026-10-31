// ════════════════════════════════════════════════════
// myplatform — Web UI (auth + credentials + deploy)
// ════════════════════════════════════════════════════

const PROVIDERS = ['aws', 'gcp', 'azure'];
const PROV_LABELS = { aws: 'AWS', gcp: 'GCP', azure: 'Azure' };

const state = {
  clusters: [],
  warnings: [],
  creds: {},
  user: null,
  token: null,
  selectedClusters: new Set(),
  jobPollTimer: null,
  statusPollTimer: null,
};

// ─── Helpers ────────────────────────────────────────
function $(s, r = document) { return r.querySelector(s); }
function $$(s, r = document) { return [...r.querySelectorAll(s)]; }

function getToken() { return localStorage.getItem('mp_token'); }
function setToken(t) { localStorage.setItem('mp_token', t); state.token = t; }
function clearToken() { localStorage.removeItem('mp_token'); state.token = null; }

function authHeaders() {
  const t = getToken();
  return t ? { 'Authorization': 'Bearer ' + t } : {};
}

function toast(msg, type = 'info', ms = 4000) {
  const el = document.createElement('div');
  el.className = `toast ${type}`;
  el.textContent = msg;
  $('#toastContainer').appendChild(el);
  setTimeout(() => { el.classList.add('removing'); setTimeout(() => el.remove(), 250); }, ms);
}

function statusClass(s) {
  s = (s || '').toLowerCase();
  if (['active', 'running'].some(x => s.includes(x))) return 'active';
  if (['provision', 'creating', 'upgrading', 'reconcil'].some(x => s.includes(x))) return 'provisioning';
  if (['fail', 'error', 'delet'].some(x => s.includes(x))) return 'failed';
  return 'unknown';
}

function parseRef(ref) { const [p, ...r] = ref.split('/'); return { prov: p, name: r.join('/') }; }

// ─── API (authenticated) ───────────────────────────
async function api(path, opts = {}) {
  const res = await fetch(path, {
    ...opts,
    headers: { 'Content-Type': 'application/json', ...authHeaders(), ...(opts.headers || {}) },
  });
  if (res.status === 401 && path !== '/api/auth/login') {
    showAuth();
    throw new Error('Session expired');
  }
  const data = await res.json();
  return data;
}

const API = {
  register: (body) => api('/api/auth/register', { method: 'POST', body: JSON.stringify(body) }),
  login: (body) => api('/api/auth/login', { method: 'POST', body: JSON.stringify(body) }),
  me: () => api('/api/auth/me'),
  clusters: () => api('/api/clusters'),
  deploy: (body) => api('/api/deploy', { method: 'POST', body: JSON.stringify(body) }),
  job: (id) => api(`/api/jobs/${id}`),
  status: (params) => api(`/api/status?${params}`),
  getCredentials: () => api('/api/credentials'),
  saveCredentials: (prov, body) => api(`/api/credentials/${prov}`, { method: 'PUT', body: JSON.stringify(body) }),
  deleteCredentials: (prov) => api(`/api/credentials/${prov}`, { method: 'DELETE' }),
};

// ─── Auth ──────────────────────────────────────────
function showAuth() {
  clearToken();
  $('#authView').classList.remove('hidden');
  $('#appShell').classList.add('hidden');
  if (state.jobPollTimer) clearInterval(state.jobPollTimer);
  if (state.statusPollTimer) clearInterval(state.statusPollTimer);
}

function showApp() {
  $('#authView').classList.add('hidden');
  $('#appShell').classList.remove('hidden');
}

function initAuth() {
  // Login form
  $('#loginForm').addEventListener('submit', async e => {
    e.preventDefault();
    const btn = $('#loginSubmit');
    btn.disabled = true; btn.textContent = 'Signing in…';
    $('#loginError').classList.add('hidden');
    try {
      const res = await API.login({
        email: $('#loginEmail').value.trim(),
        password: $('#loginPassword').value,
      });
      if (res.error) { $('#loginError').textContent = res.error; $('#loginError').classList.remove('hidden'); return; }
      setToken(res.token);
      state.user = res.user;
      showApp();
      initApp();
    } catch (err) { toast(err.message, 'error'); }
    finally { btn.disabled = false; btn.textContent = 'Sign In'; }
  });

  // Register form
  $('#registerForm').addEventListener('submit', async e => {
    e.preventDefault();
    if ($('#regPassword').value !== $('#regPasswordConfirm').value) {
      $('#registerError').textContent = 'Passwords do not match';
      $('#registerError').classList.remove('hidden');
      return;
    }
    const btn = $('#registerSubmit');
    btn.disabled = true; btn.textContent = 'Creating…';
    $('#registerError').classList.add('hidden');
    try {
      const res = await API.register({
        name: $('#regName').value.trim(),
        email: $('#regEmail').value.trim(),
        password: $('#regPassword').value,
      });
      if (res.error) { $('#registerError').textContent = res.error; $('#registerError').classList.remove('hidden'); return; }
      setToken(res.token);
      state.user = res.user;
      showApp();
      initApp();
    } catch (err) { toast(err.message, 'error'); }
    finally { btn.disabled = false; btn.textContent = 'Create Account'; }
  });

  // Toggle between login/register
  $('#showRegister').addEventListener('click', e => {
    e.preventDefault();
    $('#loginForm').classList.add('hidden');
    $('#registerForm').classList.remove('hidden');
  });
  $('#showLogin').addEventListener('click', e => {
    e.preventDefault();
    $('#registerForm').classList.add('hidden');
    $('#loginForm').classList.remove('hidden');
  });

  // Logout
  $('#logoutBtn').addEventListener('click', () => {
    showAuth();
    toast('Signed out', 'info');
  });
}

// ─── App init (after auth) ─────────────────────────
let appInited = false;
function initApp() {
  updateUserChip();
  if (!appInited) {
    initTabs();
    initEnvVars();
    initDeploy();
    initStatus();
    initSettings();
    $('#refreshBtn').addEventListener('click', loadClusters);
    $('#emptyConfigBtn').addEventListener('click', openSettings);
    appInited = true;
  }
  loadCredentials();
  loadClusters();
}

function updateUserChip() {
  const chip = $('#userChip');
  const initials = (state.user?.name || state.user?.email || '?').charAt(0).toUpperCase();
  const avatar = document.createElement('span');
  avatar.className = 'user-avatar';
  avatar.textContent = initials;
  const label = document.createElement('span');
  label.textContent = state.user?.name || state.user?.email || '';
  chip.replaceChildren(avatar, label);
}

// ─── Tabs ──────────────────────────────────────────
function initTabs() {
  $$('.tab').forEach(tab => {
    tab.addEventListener('click', () => {
      $$('.tab').forEach(t => t.classList.remove('active'));
      $$('.view').forEach(v => v.classList.remove('active'));
      tab.classList.add('active');
      $(`#${tab.dataset.tab}View`).classList.add('active');
    });
  });
}

// ─── Cloud badges (from credentials) ───────────────
function renderCloudBadges() {
  const el = $('#cloudBadges');
  el.innerHTML = '';
  PROVIDERS.forEach(p => {
    if (state.creds[p]?.configured)
      el.innerHTML += `<div class="cloud-badge ${p}"><span class="dot"></span>${PROV_LABELS[p]}</div>`;
  });
}

// ─── Cluster summary ───────────────────────────────
function renderSummary() {
  const el = $('#clusterSummary');
  el.innerHTML = '';
  const counts = {};
  state.clusters.forEach(c => counts[c.provider] = (counts[c.provider] || 0) + 1);
  const total = state.clusters.length;
  const cards = [{ key: 'all', label: 'Total', count: total, icon: '∑' }];
  PROVIDERS.forEach(p => { if (counts[p]) cards.push({ key: p, label: PROV_LABELS[p], count: counts[p], icon: PROV_LABELS[p] }); });
  cards.forEach(c => {
    el.innerHTML += `<div class="summary-card"><div class="sum-icon ${c.key}">${c.icon}</div><div><div class="sum-count">${c.count}</div><div class="sum-label">${c.label}${c.key === 'all' ? ' Clusters' : ''}</div></div></div>`;
  });
}

// ─── Cluster grid ──────────────────────────────────
function renderClusters() {
  const grid = $('#clusterGrid');
  const empty = $('#clusterEmpty');
  const skel = $('#clusterSkeleton');
  if (state.clusters.length === 0) { skel.classList.add('hidden'); empty.classList.remove('hidden'); grid.innerHTML = ''; return; }
  skel.classList.add('hidden'); empty.classList.add('hidden'); grid.innerHTML = '';
  state.clusters.forEach(c => {
    const card = document.createElement('div');
    card.className = `cluster-card ${c.provider}`;
    card.innerHTML = `
      <div class="card-top"><span class="prov-badge">${PROV_LABELS[c.provider]}</span><span class="status-badge ${statusClass(c.status)}">${c.status || '—'}</span></div>
      <div class="card-name">${c.name}</div>
      <div class="card-meta">
        <span><svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M8 1.5 C5 1.5 3 3.5 3 6 c0 3 5 8 5 8 s5-5 5-8 c0-2.5-2-4.5-5-4.5z"/><circle cx="8" cy="6" r="1.5"/></svg> ${c.region || '—'}</span>
        <span><svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5"><rect x="2" y="3" width="12" height="10" rx="1.5"/><path d="M5 6.5 L8 9 L11 6.5"/></svg> v${c.version || '—'}</span>
      </div>`;
    card.addEventListener('click', () => {
      state.selectedClusters.has(c.ref) ? state.selectedClusters.delete(c.ref) : state.selectedClusters.add(c.ref);
      updatePickerChecks(); updateDeployHint();
      $$('.tab').forEach(t => t.classList.remove('active')); $$('.view').forEach(v => v.classList.remove('active'));
      $('[data-tab="deploy"]').classList.add('active'); $('#deployView').classList.add('active');
    });
    grid.appendChild(card);
  });
}

// ─── Load clusters ─────────────────────────────────
async function loadClusters() {
  const btn = $('#refreshBtn'); btn.classList.add('spinning');
  try {
    const data = await API.clusters();
    if (data.error) {
      $('#clusterSkeleton').classList.add('hidden'); $('#clusterGrid').innerHTML = '';
      const err = $('#clusterConfigError');
      err.classList.remove('hidden');
      err.innerHTML = `<h3>Cloud Not Configured</h3><p>${data.error}</p><button class="btn btn-primary" id="errConfigBtn" style="margin-top:18px">Configure Clouds</button>`;
      $('#errConfigBtn').addEventListener('click', openSettings);
      state.clusters = []; renderClusterPicker(); return;
    }
    $('#clusterConfigError').classList.add('hidden');
    state.clusters = data.clusters || []; state.warnings = data.warnings || [];
    renderCloudBadges(); renderSummary(); renderClusters(); renderClusterPicker();
    if (state.warnings.length) state.warnings.forEach(w => toast(w, 'info', 5000));
    if (state.clusters.length) toast(`Found ${state.clusters.length} cluster${state.clusters.length > 1 ? 's' : ''}`, 'success');
  } catch (err) { if (state.token) toast('Failed to fetch clusters: ' + err.message, 'error'); }
  finally { btn.classList.remove('spinning'); }
}

// ─── Cluster picker ─────────────────────────────────
function renderClusterPicker() {
  const el = $('#deployClusterPicker');
  if (state.clusters.length === 0) { el.innerHTML = '<p class="muted">No clusters available. Configure credentials and refresh.</p>'; return; }
  el.innerHTML = '';
  const grouped = {};
  state.clusters.forEach(c => (grouped[c.provider] = grouped[c.provider] || []).push(c));
  PROVIDERS.forEach(p => {
    if (!grouped[p]) return;
    const g = document.createElement('div');
    g.className = 'cluster-group';
    g.innerHTML = `<div class="cluster-group-header ${p}"><span class="group-dot"></span>${PROV_LABELS[p]} (${grouped[p].length})<button type="button" class="btn-link select-all" data-prov="${p}">Select all</button></div>`;
    grouped[p].forEach(c => {
      const row = document.createElement('label');
      row.className = 'cluster-check';
      row.innerHTML = `<input type="checkbox" value="${c.ref}" ${state.selectedClusters.has(c.ref) ? 'checked' : ''} /><span class="check-name">${c.name}</span><span class="check-region">${c.region}</span>`;
      row.querySelector('input').addEventListener('change', e => {
        e.target.checked ? state.selectedClusters.add(c.ref) : state.selectedClusters.delete(c.ref);
        updateDeployHint();
      });
      g.appendChild(row);
    });
    el.appendChild(g);
  });
  $$('.select-all').forEach(b => b.addEventListener('click', () => {
    const refs = grouped[b.dataset.prov].map(c => c.ref);
    refs.every(r => state.selectedClusters.has(r)) ? refs.forEach(r => state.selectedClusters.delete(r)) : refs.forEach(r => state.selectedClusters.add(r));
    updatePickerChecks(); updateDeployHint();
  }));
  updateDeployHint();
}
function updatePickerChecks() { $$('#deployClusterPicker input[type="checkbox"]').forEach(cb => cb.checked = state.selectedClusters.has(cb.value)); }
function updateDeployHint() { const n = state.selectedClusters.size; $('#deployHint').textContent = n ? `${n} cluster${n > 1 ? 's' : ''} selected` : ''; }

// ─── Env vars ──────────────────────────────────────
function initEnvVars() {
  $('#addEnvBtn').addEventListener('click', () => {
    const row = document.createElement('div');
    row.className = 'env-row';
    row.innerHTML = `<input type="text" class="env-key" placeholder="KEY" autocomplete="off" /><input type="text" class="env-val" placeholder="VALUE" autocomplete="off" /><button type="button" class="env-remove" title="Remove">&times;</button>`;
    row.querySelector('.env-remove').addEventListener('click', () => row.remove());
    $('#envVars').appendChild(row);
  });
}
function collectEnv() {
  const env = {};
  $$('.env-row').forEach(r => { const k = r.querySelector('.env-key').value.trim(), v = r.querySelector('.env-val').value.trim(); if (k) env[k] = v; });
  return Object.keys(env).length > 0 ? env : null;
}

// ─── Deploy ────────────────────────────────────────
function initDeploy() {
  $('#deployForm').addEventListener('submit', async e => {
    e.preventDefault();
    const name = $('#dName').value.trim(), image = $('#dImage').value.trim();
    if (!name || !image) { toast('App name and image are required', 'error'); return; }
    if (state.selectedClusters.size === 0) { toast('Select at least one cluster', 'error'); return; }
    const btn = $('#deployBtn');
    btn.disabled = true; $('.btn-label', btn).textContent = 'Deploying…'; $('.btn-spinner', btn).classList.remove('hidden');
    try {
      const res = await API.deploy({
        name, image, namespace: $('#dNamespace').value.trim() || 'default',
        port: parseInt($('#dPort').value) || 8080, replicas: parseInt($('#dReplicas').value) || 2,
        env: collectEnv(), clusters: [...state.selectedClusters],
      });
      if (res.error) { toast(res.error, 'error'); return; }
      toast(`Deployment started: ${name} → ${state.selectedClusters.size} clusters`, 'info');
      pollJob(res.jobId, name, image);
    } catch (err) { toast('Deploy failed: ' + err.message, 'error'); }
    finally { btn.disabled = false; $('.btn-label', btn).textContent = 'Deploy'; $('.btn-spinner', btn).classList.add('hidden'); }
  });
}

function pollJob(jobId, name, image) {
  const container = $('#deployResults');
  if (state.jobPollTimer) clearInterval(state.jobPollTimer);
  const render = job => {
    const total = job.total, done = job.completed, pct = total > 0 ? Math.round(done / total * 100) : 0;
    const allDone = job.status === 'completed', hasFail = job.failed > 0;
    let pc = allDone && hasFail && job.failed === total ? 'all-failed' : hasFail ? 'has-failures' : '';
    let h = `<div class="result-card"><div class="result-header"><span class="result-title">${name}</span><span class="result-sub">${image}</span><span class="status-badge ${allDone ? (hasFail ? 'failed' : 'active') : 'provisioning'}">${allDone ? (hasFail ? 'Completed with errors' : 'Success') : 'Deploying'}</span></div><div class="progress-bar"><div class="progress-fill ${pc}" style="width:${pct}%"></div></div>`;
    job.results.forEach(r => {
      const { prov, name: cn } = parseRef(r.ref);
      h += `<div class="result-row"><div class="rr-ref">${cn}<span class="rr-prov"> · ${PROV_LABELS[prov]}</span></div><div class="rr-status">${r.ready ? `<span class="rr-ready">${r.ready}</span>` : ''}<span class="rr-badge ${r.ok ? 'ok' : 'fail'}">${r.ok ? 'OK' : 'Failed'}</span></div></div>`;
      if (r.error) h += `<div class="rr-error">${r.error}</div>`;
    });
    for (let i = job.results.length; i < total; i++)
      h += `<div class="result-row"><div class="rr-ref" style="color:var(--text-muted)">Waiting…</div><div class="rr-status"><span class="deploying-dot"></span><span class="rr-badge pending">Pending</span></div></div>`;
    container.innerHTML = h + '</div>';
  };
  const poll = async () => {
    try {
      const job = await API.job(jobId);
      render(job);
      if (job.status === 'completed') {
        if (state.jobPollTimer) { clearInterval(state.jobPollTimer); state.jobPollTimer = null; }
        if (job.failed === 0) toast(`Deployment complete: ${name}`, 'success');
        else toast(`${job.total - job.failed}/${job.total} succeeded`, 'error');
      }
    } catch (e) {}
  };
  poll(); state.jobPollTimer = setInterval(poll, 2000);
}

// ─── Status ────────────────────────────────────────
function initStatus() {
  $('#statusBtn').addEventListener('click', checkStatus);
  $('#autoRefresh').addEventListener('change', e => {
    if (e.target.checked) { checkStatus(); state.statusPollTimer = setInterval(checkStatus, 5000); }
    else if (state.statusPollTimer) { clearInterval(state.statusPollTimer); state.statusPollTimer = null; }
  });
}
async function checkStatus() {
  const name = $('#sName').value.trim();
  if (!name) { toast('App name is required', 'error'); return; }
  const ns = $('#sNamespace').value.trim() || 'default';
  const params = new URLSearchParams({ name, namespace: ns });
  state.clusters.forEach(c => params.append('cluster', c.ref));
  const btn = $('#statusBtn'); btn.disabled = true; btn.textContent = 'Checking…';
  try {
    const data = await API.status(params);
    if (data.error) { toast(data.error, 'error'); return; }
    renderStatus(data.statuses || []);
  } catch (err) { toast('Status check failed: ' + err.message, 'error'); }
  finally { btn.disabled = false; btn.textContent = 'Check Status'; }
}
function renderStatus(statuses) {
  const el = $('#statusResults');
  if (!statuses.length) { el.innerHTML = '<div class="empty-state"><h3>No results</h3></div>'; return; }
  let h = '<div class="card" style="padding:0;overflow:hidden"><table class="status-table"><thead><tr><th>Cluster</th><th>Ready</th><th>Up-to-date</th><th>Detail</th></tr></thead><tbody>';
  statuses.forEach(s => {
    const { prov, name } = parseRef(s.ref);
    const rc = s.error ? 'var(--error)' : s.updated === s.desired && s.desired > 0 ? 'var(--success)' : 'var(--warning)';
    h += `<tr><td class="st-cluster">${name}<span class="st-prov"> · ${PROV_LABELS[prov]}</span></td><td class="st-ready" style="color:${rc}">${s.ready || '—'}</td><td class="st-ready">${s.error ? '—' : `${s.updated}/${s.desired}`}</td><td class="st-detail">${s.error || ''}</td></tr>`;
  });
  el.innerHTML = h + '</tbody></table></div>';
}

// ─── Credentials ───────────────────────────────────
function initSettings() {
  $('#settingsBtn').addEventListener('click', openSettings);
  $('#settingsClose').addEventListener('click', closeSettings);
  $('#settingsClose2').addEventListener('click', closeSettings);
  $('#settingsModal').addEventListener('click', e => { if (e.target === $('#settingsModal')) closeSettings(); });
  document.addEventListener('keydown', e => { if (e.key === 'Escape') closeSettings(); });

  $('#saveAwsBtn').addEventListener('click', () => saveCred('aws'));
  $('#removeAwsBtn').addEventListener('click', () => removeCred('aws'));
  $('#saveGcpBtn').addEventListener('click', () => saveCred('gcp'));
  $('#removeGcpBtn').addEventListener('click', () => removeCred('gcp'));
  $('#saveAzureBtn').addEventListener('click', () => saveCred('azure'));
  $('#removeAzureBtn').addEventListener('click', () => removeCred('azure'));
}

function openSettings() { $('#settingsModal').classList.remove('hidden'); loadCredentials(); }
function closeSettings() { $('#settingsModal').classList.add('hidden'); }

async function loadCredentials() {
  try {
    const data = await API.getCredentials();
    if (data.error) throw new Error(data.error);
    state.creds = data;
    renderCloudBadges();
    // Update status badges
    PROVIDERS.forEach(p => {
      const el = $(`#${p}CredStatus`);
      if (el) {
        const configured = data[p]?.configured;
        el.textContent = configured ? 'Configured' : 'Not set';
        el.className = `cred-status ${configured ? 'configured' : 'unconfigured'}`;
      }
    });
    $('#awsRegions').value = data.aws?.regions?.join(', ') || '';
    $('#awsAccessKey').placeholder = data.aws?.accessKeyId || 'AKIA...';
    $('#gcpProjectId').value = data.gcp?.projectId || '';
    $('#azureTenantId').value = data.azure?.tenantId || '';
    $('#azureSubscriptionId').value = data.azure?.subscriptionId || '';
  } catch (e) {
    if (state.token) toast('Could not load cloud credentials: ' + e.message, 'error');
  }
}

async function saveCred(prov) {
  let body;
  const btn = $(`#save${prov[0].toUpperCase()}${prov.slice(1)}Btn`);
  const label = btn.textContent;
  if (prov === 'aws') {
    body = {
      accessKeyId: $('#awsAccessKey').value.trim(),
      secretAccessKey: $('#awsSecretKey').value.trim(),
      sessionToken: $('#awsSessionToken').value.trim(),
      regions: $('#awsRegions').value.split(',').map(s => s.trim()).filter(Boolean),
    };
  } else if (prov === 'gcp') {
    body = { serviceAccountKey: $('#gcpServiceAccount').value.trim(), projectId: $('#gcpProjectId').value.trim() };
  } else if (prov === 'azure') {
    body = {
      clientId: $('#azureClientId').value.trim(), clientSecret: $('#azureClientSecret').value.trim(),
      tenantId: $('#azureTenantId').value.trim(), subscriptionId: $('#azureSubscriptionId').value.trim(),
    };
  }
  btn.disabled = true;
  btn.textContent = 'Verifying & saving…';
  try {
    const res = await API.saveCredentials(prov, body);
    if (res.error) { toast(res.error, 'error'); return; }
    if (prov === 'aws') {
      $('#awsSecretKey').value = '';
      $('#awsSessionToken').value = '';
    } else if (prov === 'gcp') {
      $('#gcpServiceAccount').value = '';
    } else if (prov === 'azure') {
      $('#azureClientSecret').value = '';
    }
    toast(`${PROV_LABELS[prov]} credentials saved`, 'success');
    loadCredentials();
    state.selectedClusters.clear();
    loadClusters();
  } catch (err) { toast('Failed: ' + err.message, 'error'); }
  finally {
    btn.disabled = false;
    btn.textContent = label;
  }
}

async function removeCred(prov) {
  try {
    const res = await API.deleteCredentials(prov);
    if (res.error) { toast(res.error, 'error'); return; }
    toast(`${PROV_LABELS[prov]} credentials removed`, 'info');
    loadCredentials();
    state.selectedClusters.clear();
    loadClusters();
  } catch (err) { toast('Failed: ' + err.message, 'error'); }
}

// ─── Bootstrap ─────────────────────────────────────
async function bootstrap() {
  initAuth();
  const token = getToken();
  if (!token) { showAuth(); return; }
  state.token = token;
  try {
    const user = await API.me();
    if (user.error) { showAuth(); return; }
    state.user = user;
    showApp();
    initApp();
  } catch { showAuth(); }
}

document.addEventListener('DOMContentLoaded', bootstrap);
