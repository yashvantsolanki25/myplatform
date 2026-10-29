// ════════════════════════════════════════════════════
// myplatform — Web UI
// ════════════════════════════════════════════════════

const API = {
  clusters: () => fetch('/api/clusters').then(r => r.json()),
  deploy: (body) => fetch('/api/deploy', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  }).then(r => r.json()),
  job: (id) => fetch(`/api/jobs/${id}`).then(r => r.json()),
  status: (params) => fetch(`/api/status?${params}`).then(r => r.json()),
  getConfig: () => fetch('/api/config').then(r => r.json()),
  saveConfig: (body) => fetch('/api/config', {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  }).then(r => r.json()),
};

const state = {
  clusters: [],
  warnings: [],
  config: { awsRegions: [], gcpProject: '', azureSubscription: '' },
  selectedClusters: new Set(),
  jobPollTimer: null,
  statusPollTimer: null,
};

const PROVIDERS = ['aws', 'gcp', 'azure'];
const PROV_LABELS = { aws: 'AWS', gcp: 'GCP', azure: 'Azure' };

// ─── Helpers ────────────────────────────────────────
function $(sel, root = document) { return root.querySelector(sel); }
function $$(sel, root = document) { return [...root.querySelectorAll(sel)]; }

function toast(msg, type = 'info', ms = 4000) {
  const el = document.createElement('div');
  el.className = `toast ${type}`;
  el.textContent = msg;
  $('#toastContainer').appendChild(el);
  setTimeout(() => {
    el.classList.add('removing');
    setTimeout(() => el.remove(), 250);
  }, ms);
}

function statusClass(status) {
  const s = (status || '').toLowerCase();
  if (['active', 'running'].some(x => s.includes(x))) return 'active';
  if (['provision', 'creating', 'upgrading', 'reconcil'].some(x => s.includes(x))) return 'provisioning';
  if (['fail', 'error', 'delet'].some(x => s.includes(x))) return 'failed';
  return 'unknown';
}

function parseRef(ref) {
  const [prov, ...rest] = ref.split('/');
  return { prov, name: rest.join('/') };
}

// ─── Tab switching ──────────────────────────────────
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

// ─── Cloud badges ───────────────────────────────────
function renderCloudBadges() {
  const el = $('#cloudBadges');
  el.innerHTML = '';
  if (state.config.awsRegions && state.config.awsRegions.length > 0)
    el.innerHTML += `<div class="cloud-badge aws"><span class="dot"></span>AWS</div>`;
  if (state.config.gcpProject)
    el.innerHTML += `<div class="cloud-badge gcp"><span class="dot"></span>GCP</div>`;
  if (state.config.azureSubscription)
    el.innerHTML += `<div class="cloud-badge azure"><span class="dot"></span>Azure</div>`;
}

// ─── Cluster summary ────────────────────────────────
function renderSummary() {
  const el = $('#clusterSummary');
  el.innerHTML = '';
  const counts = {};
  state.clusters.forEach(c => counts[c.provider] = (counts[c.provider] || 0) + 1);

  const total = state.clusters.length;
  const cards = [{ key: 'all', label: 'Total', count: total, icon: '∑' }];
  PROVIDERS.forEach(p => {
    if (counts[p]) cards.push({ key: p, label: PROV_LABELS[p], count: counts[p], icon: PROV_LABELS[p] });
  });

  cards.forEach(c => {
    const div = document.createElement('div');
    div.className = 'summary-card';
    div.innerHTML = `
      <div class="sum-icon ${c.key}">${c.icon}</div>
      <div>
        <div class="sum-count">${c.count}</div>
        <div class="sum-label">${c.label} ${c.key === 'all' ? 'Clusters' : ''}</div>
      </div>`;
    el.appendChild(div);
  });
}

// ─── Cluster grid ────────────────────────────────────
function renderClusters() {
  const grid = $('#clusterGrid');
  const empty = $('#clusterEmpty');
  const skeleton = $('#clusterSkeleton');

  if (state.clusters.length === 0) {
    skeleton.classList.add('hidden');
    empty.classList.remove('hidden');
    grid.innerHTML = '';
    return;
  }

  skeleton.classList.add('hidden');
  empty.classList.add('hidden');
  grid.innerHTML = '';

  state.clusters.forEach(c => {
    const card = document.createElement('div');
    card.className = `cluster-card ${c.provider}`;
    card.innerHTML = `
      <div class="card-top">
        <span class="prov-badge">${PROV_LABELS[c.provider]}</span>
        <span class="status-badge ${statusClass(c.status)}">${c.status || '—'}</span>
      </div>
      <div class="card-name">${c.name}</div>
      <div class="card-meta">
        <span><svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M8 1.5 C5 1.5 3 3.5 3 6 c0 3 5 8 5 8 s5-5 5-8 c0-2.5-2-4.5-5-4.5z"/><circle cx="8" cy="6" r="1.5"/></svg> ${c.region || '—'}</span>
        <span><svg width="12" height="12" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5"><rect x="2" y="3" width="12" height="10" rx="1.5"/><path d="M5 6.5 L8 9 L11 6.5"/></svg> v${c.version || '—'}</span>
      </div>`;
    card.addEventListener('click', () => {
      state.selectedClusters.has(c.ref)
        ? state.selectedClusters.delete(c.ref)
        : state.selectedClusters.add(c.ref);
      updatePickerChecks();
      updateDeployHint();
      $$('.tab').forEach(t => t.classList.remove('active'));
      $$('.view').forEach(v => v.classList.remove('active'));
      $('[data-tab="deploy"]').classList.add('active');
      $('#deployView').classList.add('active');
    });
    grid.appendChild(card);
  });
}

// ─── Load clusters ──────────────────────────────────
async function loadClusters() {
  const btn = $('#refreshBtn');
  btn.classList.add('spinning');

  try {
    const data = await API.clusters();
    if (data.error) {
      $('#clusterSkeleton').classList.add('hidden');
      $('#clusterGrid').innerHTML = '';
      const err = $('#clusterConfigError');
      err.classList.remove('hidden');
      err.innerHTML = `<h3>No Cloud Configured</h3>
        <p>${data.error}</p>
        <button class="btn btn-primary" id="configErrorBtn" style="margin-top:18px">Configure Clouds</button>`;
      $('#configErrorBtn').addEventListener('click', openSettings);
      state.clusters = [];
      renderClusterPicker();
      return;
    }

    $('#clusterConfigError').classList.add('hidden');
    state.clusters = data.clusters || [];
    state.warnings = data.warnings || [];

    renderCloudBadges();
    renderSummary();
    renderClusters();
    renderClusterPicker();

    if (state.warnings.length > 0) {
      state.warnings.forEach(w => toast(w, 'info', 5000));
    }
    if (state.clusters.length > 0) {
      toast(`Found ${state.clusters.length} cluster${state.clusters.length > 1 ? 's' : ''}`, 'success');
    }
  } catch (err) {
    toast('Failed to fetch clusters: ' + err.message, 'error');
  } finally {
    btn.classList.remove('spinning');
  }
}

// ─── Cluster picker (deploy view) ───────────────────
function renderClusterPicker() {
  const el = $('#deployClusterPicker');

  if (state.clusters.length === 0) {
    el.innerHTML = '<p class="muted">No clusters available. Refresh the clusters list first.</p>';
    return;
  }

  el.innerHTML = '';
  const grouped = {};
  state.clusters.forEach(c => {
    (grouped[c.provider] = grouped[c.provider] || []).push(c);
  });

  PROVIDERS.forEach(p => {
    if (!grouped[p]) return;
    const group = document.createElement('div');
    group.className = 'cluster-group';
    group.innerHTML = `
      <div class="cluster-group-header ${p}">
        <span class="group-dot"></span>${PROV_LABELS[p]} (${grouped[p].length})
        <button type="button" class="btn-link select-all" data-prov="${p}">Select all</button>
      </div>`;
    grouped[p].forEach(c => {
      const row = document.createElement('label');
      row.className = 'cluster-check';
      row.innerHTML = `
        <input type="checkbox" value="${c.ref}" ${state.selectedClusters.has(c.ref) ? 'checked' : ''} />
        <span class="check-name">${c.name}</span>
        <span class="check-region">${c.region}</span>`;
      row.querySelector('input').addEventListener('change', e => {
        e.target.checked ? state.selectedClusters.add(c.ref) : state.selectedClusters.delete(c.ref);
        updateDeployHint();
      });
      group.appendChild(row);
    });
    el.appendChild(group);
  });

  $$('.select-all').forEach(btn => {
    btn.addEventListener('click', () => {
      const prov = btn.dataset.prov;
      const refs = grouped[prov].map(c => c.ref);
      const allSelected = refs.every(r => state.selectedClusters.has(r));
      if (allSelected) refs.forEach(r => state.selectedClusters.delete(r));
      else refs.forEach(r => state.selectedClusters.add(r));
      updatePickerChecks();
      updateDeployHint();
    });
  });

  updateDeployHint();
}

function updatePickerChecks() {
  $$('#deployClusterPicker input[type="checkbox"]').forEach(cb => {
    cb.checked = state.selectedClusters.has(cb.value);
  });
}

function updateDeployHint() {
  const n = state.selectedClusters.size;
  $('#deployHint').textContent = n === 0 ? '' : `${n} cluster${n > 1 ? 's' : ''} selected`;
}

// ─── Env vars ───────────────────────────────────────
function initEnvVars() {
  $('#addEnvBtn').addEventListener('click', () => {
    const row = document.createElement('div');
    row.className = 'env-row';
    row.innerHTML = `
      <input type="text" class="env-key" placeholder="KEY" autocomplete="off" />
      <input type="text" class="env-val" placeholder="VALUE" autocomplete="off" />
      <button type="button" class="env-remove" title="Remove">&times;</button>`;
    row.querySelector('.env-remove').addEventListener('click', () => row.remove());
    $('#envVars').appendChild(row);
  });

  $('#envVars').addEventListener('click', e => {
    if (e.target.classList.contains('env-remove')) e.target.closest('.env-row').remove();
  });
}

function collectEnv() {
  const env = {};
  $$('.env-row').forEach(row => {
    const k = row.querySelector('.env-key').value.trim();
    const v = row.querySelector('.env-val').value.trim();
    if (k) env[k] = v;
  });
  return Object.keys(env).length > 0 ? env : null;
}

// ─── Deploy ─────────────────────────────────────────
function initDeploy() {
  $('#deployForm').addEventListener('submit', async e => {
    e.preventDefault();
    const name = $('#dName').value.trim();
    const image = $('#dImage').value.trim();

    if (!name || !image) { toast('App name and image are required', 'error'); return; }
    if (state.selectedClusters.size === 0) { toast('Select at least one cluster', 'error'); return; }

    const btn = $('#deployBtn');
    btn.disabled = true;
    $('.btn-label', btn).textContent = 'Deploying…';
    $('.btn-spinner', btn).classList.remove('hidden');

    try {
      const res = await API.deploy({
        name,
        image,
        namespace: $('#dNamespace').value.trim() || 'default',
        port: parseInt($('#dPort').value) || 8080,
        replicas: parseInt($('#dReplicas').value) || 2,
        env: collectEnv(),
        clusters: [...state.selectedClusters],
      });

      if (res.error) {
        toast(res.error, 'error');
        return;
      }

      toast(`Deployment started: ${name} → ${state.selectedClusters.size} clusters`, 'info');
      pollJob(res.jobId, name, image);
    } catch (err) {
      toast('Deploy failed: ' + err.message, 'error');
    } finally {
      btn.disabled = false;
      $('.btn-label', btn).textContent = 'Deploy';
      $('.btn-spinner', btn).classList.add('hidden');
    }
  });
}

function pollJob(jobId, name, image) {
  const container = $('#deployResults');
  if (state.jobPollTimer) clearInterval(state.jobPollTimer);

  const render = (job) => {
    const total = job.total;
    const done = job.completed;
    const pct = total > 0 ? Math.round((done / total) * 100) : 0;
    const allDone = job.status === 'completed';
    const hasFailures = job.failed > 0;

    let progressClass = '';
    if (allDone && hasFailures && job.failed === total) progressClass = 'all-failed';
    else if (hasFailures) progressClass = 'has-failures';

    let html = `
      <div class="result-card">
        <div class="result-header">
          <span class="result-title">${name}</span>
          <span class="result-sub">${image}</span>
          <span class="status-badge ${allDone ? (hasFailures ? 'failed' : 'active') : 'provisioning'}">
            ${allDone ? (hasFailures ? 'Completed with errors' : 'Success') : 'Deploying'}
          </span>
        </div>
        <div class="progress-bar"><div class="progress-fill ${progressClass}" style="width:${pct}%"></div></div>`;

    // Known results
    job.results.forEach(r => {
      const { prov, name: cname } = parseRef(r.ref);
      html += `
        <div class="result-row">
          <div class="rr-ref">${cname}<span class="rr-prov"> · ${PROV_LABELS[prov]}</span></div>
          <div class="rr-status">
            ${r.ready ? `<span class="rr-ready">${r.ready}</span>` : ''}
            <span class="rr-badge ${r.ok ? 'ok' : 'fail'}">${r.ok ? 'OK' : 'Failed'}</span>
          </div>
        </div>`;
      if (r.error) html += `<div class="rr-error">${r.error}</div>`;
    });

    // Pending clusters (total - completed results)
    const pending = total - job.results.length;
    if (pending > 0) {
      for (let i = 0; i < pending; i++) {
        html += `
          <div class="result-row">
            <div class="rr-ref" style="color:var(--text-muted)">Waiting…</div>
            <div class="rr-status"><span class="deploying-dot"></span><span class="rr-badge pending">Pending</span></div>
          </div>`;
      }
    }

    html += `</div>`;
    container.innerHTML = html;
  };

  const poll = async () => {
    try {
      const job = await API.job(jobId);
      render(job);
      if (job.status === 'completed') {
        if (state.jobPollTimer) { clearInterval(state.jobPollTimer); state.jobPollTimer = null; }
        if (job.failed === 0) toast(`Deployment complete: ${name}`, 'success');
        else if (job.failed === job.total) toast(`All ${job.total} deployments failed`, 'error');
        else toast(`${job.total - job.failed}/${job.total} succeeded, ${job.failed} failed`, 'error');
      }
    } catch (err) {
      console.error(err);
    }
  };

  poll();
  state.jobPollTimer = setInterval(poll, 2000);
}

// ─── Status ─────────────────────────────────────────
function initStatus() {
  $('#statusBtn').addEventListener('click', checkStatus);

  $('#autoRefresh').addEventListener('change', e => {
    if (e.target.checked) {
      checkStatus();
      state.statusPollTimer = setInterval(checkStatus, 5000);
    } else if (state.statusPollTimer) {
      clearInterval(state.statusPollTimer);
      state.statusPollTimer = null;
    }
  });
}

async function checkStatus() {
  const name = $('#sName').value.trim();
  if (!name) { toast('App name is required', 'error'); return; }

  const ns = $('#sNamespace').value.trim() || 'default';
  const params = new URLSearchParams({ name, namespace: ns });
  state.clusters.forEach(c => params.append('cluster', c.ref));

  const btn = $('#statusBtn');
  btn.disabled = true;
  btn.textContent = 'Checking…';

  try {
    const data = await API.status(params);
    if (data.error) { toast(data.error, 'error'); return; }
    renderStatus(data.statuses || []);
  } catch (err) {
    toast('Status check failed: ' + err.message, 'error');
  } finally {
    btn.disabled = false;
    btn.textContent = 'Check Status';
  }
}

function renderStatus(statuses) {
  const el = $('#statusResults');
  if (statuses.length === 0) {
    el.innerHTML = '<div class="empty-state"><h3>No results</h3><p>No clusters configured or found.</p></div>';
    return;
  }

  let html = `
    <div class="card" style="padding:0;overflow:hidden">
      <table class="status-table">
        <thead><tr><th>Cluster</th><th>Ready</th><th>Up-to-date</th><th>Detail</th></tr></thead>
        <tbody>`;
  statuses.forEach(s => {
    const { prov, name } = parseRef(s.ref);
    const readyColor = s.error ? 'var(--error)' : (s.updated === s.desired && s.desired > 0 ? 'var(--success)' : 'var(--warning)');
    html += `
      <tr>
        <td class="st-cluster">${name}<span class="st-prov"> · ${PROV_LABELS[prov]}</span></td>
        <td class="st-ready" style="color:${readyColor}">${s.ready || '—'}</td>
        <td class="st-ready">${s.error ? '—' : `${s.updated}/${s.desired}`}</td>
        <td class="st-detail">${s.error || ''}</td>
      </tr>`;
  });
  html += `</tbody></table></div>`;
  el.innerHTML = html;
}

// ─── Settings / Config ─────────────────────────────
async function loadConfig() {
  try {
    const cfg = await API.getConfig();
    state.config = {
      awsRegions: cfg.awsRegions || [],
      gcpProject: cfg.gcpProject || '',
      azureSubscription: cfg.azureSubscription || '',
    };
    renderCloudBadges();
    updateSettingsForm();
  } catch (err) {
    console.error('Failed to load config:', err);
  }
}

function updateSettingsForm() {
  $('#cfgAwsRegions').value = state.config.awsRegions.join(', ');
  $('#cfgGcpProject').value = state.config.gcpProject;
  $('#cfgAzureSub').value = state.config.azureSubscription;
  updateCredStatus('aws', state.config.awsRegions.length > 0);
  updateCredStatus('gcp', !!state.config.gcpProject);
  updateCredStatus('azure', !!state.config.azureSubscription);
}

function updateCredStatus(cloud, configured) {
  const el = $(`#${cloud}Status`);
  if (!el) return;
  el.textContent = configured ? 'Configured' : 'Not set';
  el.className = `cred-status ${configured ? 'configured' : 'unconfigured'}`;
}

function initSettings() {
  $('#settingsBtn').addEventListener('click', openSettings);
  $('#settingsClose').addEventListener('click', closeSettings);
  $('#settingsCancel').addEventListener('click', closeSettings);
  $('#settingsModal').addEventListener('click', e => {
    if (e.target === $('#settingsModal')) closeSettings();
  });
  $('#settingsSave').addEventListener('click', saveConfig);
  document.addEventListener('keydown', e => {
    if (e.key === 'Escape' && !$('#settingsModal').classList.contains('hidden'))
      closeSettings();
  });
}

function openSettings() { $('#settingsModal').classList.remove('hidden'); }
function closeSettings() { $('#settingsModal').classList.add('hidden'); }

async function saveConfig() {
  const awsRaw = $('#cfgAwsRegions').value.trim();
  const awsRegions = awsRaw ? awsRaw.split(',').map(s => s.trim()).filter(Boolean) : [];
  const gcpProject = $('#cfgGcpProject').value.trim();
  const azureSubscription = $('#cfgAzureSub').value.trim();

  const btn = $('#settingsSave');
  const label = $('.btn-label', btn);
  const spinner = $('.btn-spinner', btn);
  btn.disabled = true;
  label.textContent = 'Saving…';
  spinner.classList.remove('hidden');

  try {
    const res = await API.saveConfig({ awsRegions, gcpProject, azureSubscription });
    if (res.error) { toast(res.error, 'error'); return; }

    state.config = { awsRegions, gcpProject, azureSubscription };
    renderCloudBadges();
    updateCredStatus('aws', awsRegions.length > 0);
    updateCredStatus('gcp', !!gcpProject);
    updateCredStatus('azure', !!azureSubscription);
    closeSettings();
    toast('Cloud settings updated', 'success');
    state.selectedClusters.clear();
    loadClusters();
  } catch (err) {
    toast('Failed to save settings: ' + err.message, 'error');
  } finally {
    btn.disabled = false;
    label.textContent = 'Save & Connect';
    spinner.classList.add('hidden');
  }
}

// ─── Init ───────────────────────────────────────────
function init() {
  initTabs();
  initEnvVars();
  initDeploy();
  initStatus();
  initSettings();
  $('#refreshBtn').addEventListener('click', loadClusters);
  loadConfig().then(() => loadClusters());
}

document.addEventListener('DOMContentLoaded', init);
