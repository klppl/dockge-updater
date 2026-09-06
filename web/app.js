const ui = {
  systemPill: document.querySelector('#system-pill'),
  systemLabel: document.querySelector('#system-label'),
  metricStacks: document.querySelector('#metric-stacks'),
  metricUpdates: document.querySelector('#metric-updates'),
  metricCurrent: document.querySelector('#metric-current'),
  metricErrors: document.querySelector('#metric-errors'),
  metricLast: document.querySelector('#metric-last'),
  metricLastExact: document.querySelector('#metric-last-exact'),
  metricNext: document.querySelector('#metric-next'),
  metricNextExact: document.querySelector('#metric-next-exact'),
  stackSummary: document.querySelector('#stack-summary'),
  stackList: document.querySelector('#stack-list'),
  stackSearch: document.querySelector('#stack-search'),
  searchClear: document.querySelector('#search-clear'),
  filterTabs: document.querySelectorAll('.filter-tab'),
  filterErrorsTab: document.querySelector('#filter-errors'),
  activityList: document.querySelector('#activity-list'),
  activityCount: document.querySelector('#activity-count'),
  jobStrip: document.querySelector('#job-strip'),
  jobMessage: document.querySelector('#job-message'),
  jobElapsed: document.querySelector('#job-elapsed'),
  checkAll: document.querySelector('#check-all'),
  checkIcon: document.querySelector('#check-icon'),
  themeToggle: document.querySelector('#theme-toggle'),
  settingsOpen: document.querySelector('#settings-open'),
  settingsDialog: document.querySelector('#settings-dialog'),
  settingsForm: document.querySelector('#settings-form'),
  settingsClose: document.querySelector('#settings-close'),
  settingsCancel: document.querySelector('#settings-cancel'),
  scheduleFields: document.querySelector('#schedule-fields'),
  updateWeekday: document.querySelector('#update-weekday'),
  updateTime: document.querySelector('#update-time'),
  toast: document.querySelector('#toast'),
  footerVersion: document.querySelector('#footer-version'),
};

let snapshot = null;
let pollTimer = null;
let toastTimer = null;
let currentFilter = 'all';
let searchQuery = '';

// --- Theme Management ---
function initTheme() {
  const savedTheme = localStorage.getItem('dockge-updater-theme') ||
    (window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark');
  setTheme(savedTheme);

  ui.themeToggle?.addEventListener('click', () => {
    const isDark = document.documentElement.getAttribute('data-theme') === 'dark';
    const nextTheme = isDark ? 'light' : 'dark';
    setTheme(nextTheme);
    localStorage.setItem('dockge-updater-theme', nextTheme);
  });
}

function setTheme(theme) {
  document.documentElement.setAttribute('data-theme', theme);
}

// --- API Helpers ---
async function request(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    headers: { 'Content-Type': 'application/json', ...(options.headers || {}) },
  });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(body.error || `Request failed (${response.status})`);
  return body;
}

function element(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function externalLink(label, href) {
  const link = element('a', 'service-link', label);
  link.href = href;
  link.target = '_blank';
  link.rel = 'noopener noreferrer';
  link.title = `Open ${label} in new tab`;
  return link;
}

function shortRelative(value) {
  if (!value || value.startsWith('0001-')) return 'Never';
  const seconds = Math.round((new Date(value).getTime() - Date.now()) / 1000);
  const formatter = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });
  const ranges = [[86400, 'day'], [3600, 'hour'], [60, 'minute']];
  for (const [size, unit] of ranges) {
    if (Math.abs(seconds) >= size || unit === 'minute') return formatter.format(Math.round(seconds / size), unit);
  }
  return 'just now';
}

function exactDate(value) {
  if (!value || value.startsWith('0001-')) return '';
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value));
}

function timeOnly(value) {
  if (!value || value.startsWith('0001-')) return '—';
  return new Intl.DateTimeFormat(undefined, { weekday: 'short', hour: '2-digit', minute: '2-digit' }).format(new Date(value));
}

function statusInfo(stack) {
  const values = {
    update_available: [`${stack.updatesAvailable} update${stack.updatesAvailable === 1 ? '' : 's'} available`, 'badge-update'],
    up_to_date: ['Up to date', 'badge-current'],
    error: ['Needs attention', 'badge-error'],
    checking: ['Checking', 'badge-checking'],
    unknown: ['Not checked', 'badge-unknown'],
  };
  return values[stack.status] || values.unknown;
}

// --- Render Functions ---
function renderStack(stack) {
  const card = element('article', `stack-card ${stack.updatesAvailable > 0 ? 'has-update' : ''}`);

  // Card Header / Summary Row
  const header = element('div', 'stack-header');

  const left = element('div', 'stack-info');

  // Status Indicator
  const [statusLabel, badgeClass] = statusInfo(stack);
  const badge = element('span', `status-badge ${badgeClass}`);
  const dot = element('span', 'badge-dot', '');
  dot.setAttribute('aria-hidden', 'true');
  const labelText = element('span', 'badge-text', statusLabel);
  badge.append(dot, labelText);

  const titleGroup = element('div', 'stack-title-group');
  const name = element('h3', 'stack-name', stack.name);
  const meta = element('div', 'stack-meta');

  const fileTag = element('span', 'meta-tag file-tag', stack.composeFile.split('/').pop() || 'compose.yaml');
  const serviceCount = stack.services?.length || 0;
  const serviceTag = element('span', 'meta-tag count-tag', `${serviceCount} ${serviceCount === 1 ? 'service' : 'services'}`);

  meta.append(fileTag, serviceTag);

  if (stack.lastChecked && !stack.lastChecked.startsWith('0001-')) {
    const checkedTag = element('span', 'meta-tag time-tag', `checked ${shortRelative(stack.lastChecked)}`);
    checkedTag.title = exactDate(stack.lastChecked);
    meta.append(checkedTag);
  }

  titleGroup.append(name, meta);
  left.append(badge, titleGroup);

  // Actions on right
  const right = element('div', 'stack-actions');

  const isUpdating = snapshot?.job?.running;
  const hasUpdates = stack.updatesAvailable > 0;

  const updateBtn = element('button', `btn btn-sm ${hasUpdates ? 'btn-update' : 'btn-ghost'}`);
  updateBtn.type = 'button';
  updateBtn.disabled = isUpdating || !hasUpdates;
  updateBtn.dataset.stackId = stack.id;
  updateBtn.innerHTML = `
    <svg viewBox="0 0 24 24" width="13" height="13" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"></path>
      <polyline points="7 10 12 15 17 10"></polyline>
      <line x1="12" y1="15" x2="12" y2="3"></line>
    </svg>
    <span>${hasUpdates ? `Update (${stack.updatesAvailable})` : 'Update'}</span>
  `;
  updateBtn.title = isUpdating
    ? 'Another Docker job is running'
    : (!hasUpdates ? 'All services are already up to date' : `Apply ${stack.updatesAvailable} service update${stack.updatesAvailable === 1 ? '' : 's'}`);

  updateBtn.addEventListener('click', (e) => {
    e.stopPropagation();
    updateStack(stack);
  });

  const toggleBtn = element('button', 'btn-expand', '');
  toggleBtn.type = 'button';
  toggleBtn.setAttribute('aria-label', `Toggle ${stack.name} service details`);
  toggleBtn.innerHTML = `
    <svg class="chevron-icon" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
      <polyline points="6 9 12 15 18 9"></polyline>
    </svg>
  `;

  right.append(updateBtn, toggleBtn);
  header.append(left, right);
  card.append(header);

  // Details Container
  const details = element('div', 'stack-details');
  details.hidden = true; // collapsed by default

  const toggleDetails = () => {
    const isHidden = details.hidden;
    details.hidden = !isHidden;
    card.classList.toggle('is-expanded', !isHidden);
    toggleBtn.setAttribute('aria-expanded', String(!isHidden));
  };

  header.addEventListener('click', (e) => {
    if (e.target.closest('button, a')) return;
    toggleDetails();
  });
  toggleBtn.addEventListener('click', toggleDetails);

  if (stack.error) {
    const errorBlock = element('div', 'stack-error-banner');
    errorBlock.innerHTML = `
      <svg viewBox="0 0 24 24" width="15" height="15" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <circle cx="12" cy="12" r="10"></circle>
        <line x1="12" y1="8" x2="12" y2="12"></line>
        <line x1="12" y1="16" x2="12.01" y2="16"></line>
      </svg>
      <span>${stack.error}</span>
    `;
    details.append(errorBlock);
  }

  const tableWrap = element('div', 'service-table-wrap');
  if (serviceCount === 0) {
    tableWrap.append(element('div', 'service-empty', stack.error || 'No services detected or awaiting first check.'));
  } else {
    const table = element('table', 'service-table');
    table.innerHTML = `
      <thead>
        <tr>
          <th>Service</th>
          <th>Image</th>
          <th>State</th>
          <th>Status</th>
          <th>Links</th>
        </tr>
      </thead>
      <tbody></tbody>
    `;
    const tbody = table.querySelector('tbody');

    stack.services.forEach((service) => {
      const row = element('tr', `service-tr ${service.updateAvailable ? 'is-update' : ''}`);

      // Service Name
      const tdName = element('td', 'td-name');
      tdName.innerHTML = `<span class="service-tag-name">${service.name}</span>`;

      // Image
      const tdImage = element('td', 'td-image');
      const imgCode = element('code', 'image-badge', service.image);
      tdImage.append(imgCode);

      // Container State
      const tdState = element('td', 'td-state');
      const stateText = service.containerState || 'not running';
      const isRunning = stateText.toLowerCase().includes('running');
      const statePill = element('span', `state-pill ${isRunning ? 'state-running' : 'state-idle'}`, stateText);
      tdState.append(statePill);

      // Status / Update badge
      const tdStatus = element('td', 'td-status');
      if (service.updateAvailable) {
        tdStatus.innerHTML = `
          <span class="update-chip">
            <svg viewBox="0 0 24 24" width="11" height="11" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <line x1="12" y1="19" x2="12" y2="5"></line>
              <polyline points="5 12 12 5 19 12"></polyline>
            </svg>
            New image available
          </span>
        `;
      } else {
        tdStatus.innerHTML = `<span class="current-chip">Current</span>`;
      }

      // Links
      const tdLinks = element('td', 'td-links');
      const linksGroup = element('div', 'links-group');
      if (service.sourceUrl) linksGroup.append(externalLink('Repository', service.sourceUrl));
      if (service.changelogUrl) linksGroup.append(externalLink('Releases', service.changelogUrl));
      if (!service.sourceUrl && !service.changelogUrl) {
        linksGroup.innerHTML = `<span class="links-none">—</span>`;
      }
      tdLinks.append(linksGroup);

      row.append(tdName, tdImage, tdState, tdStatus, tdLinks);
      tbody.append(row);
    });
    tableWrap.append(table);
  }

  details.append(tableWrap);
  card.append(details);
  return card;
}

function filterStacks(stacks) {
  return stacks.filter((stack) => {
    // Status tab filter
    if (currentFilter === 'updates' && stack.updatesAvailable === 0) return false;
    if (currentFilter === 'current' && (stack.updatesAvailable > 0 || stack.status === 'error')) return false;
    if (currentFilter === 'error' && stack.status !== 'error') return false;

    // Search query filter
    if (searchQuery) {
      const q = searchQuery.toLowerCase();
      const matchName = stack.name?.toLowerCase().includes(q);
      const matchCompose = stack.composeFile?.toLowerCase().includes(q);
      const matchServices = stack.services?.some((s) =>
        s.name?.toLowerCase().includes(q) || s.image?.toLowerCase().includes(q)
      );
      if (!matchName && !matchCompose && !matchServices) return false;
    }
    return true;
  });
}

function renderStacks(stacks) {
  ui.stackList.replaceChildren();

  const filtered = filterStacks(stacks);

  if (!stacks.length) {
    const empty = element('div', 'empty-state');
    empty.innerHTML = `
      <div class="empty-icon">📁</div>
      <h4>No Compose stacks found</h4>
      <p>Ensure <code>STACKS_DIR</code> points to your Dockge stacks folder.</p>
    `;
    ui.stackList.append(empty);
    return;
  }

  if (!filtered.length) {
    const empty = element('div', 'empty-state');
    empty.innerHTML = `
      <div class="empty-icon">🔍</div>
      <h4>No matching stacks found</h4>
      <p>Try clearing your search or status filter.</p>
      <button class="btn btn-ghost btn-sm" id="btn-reset-filters" type="button">Reset filters</button>
    `;
    empty.querySelector('#btn-reset-filters')?.addEventListener('click', () => {
      searchQuery = '';
      ui.stackSearch.value = '';
      ui.searchClear.hidden = true;
      setFilter('all');
    });
    ui.stackList.append(empty);
    return;
  }

  filtered.forEach((stack) => ui.stackList.append(renderStack(stack)));
}

function renderEvents(events) {
  ui.activityList.replaceChildren();
  if (!events.length) {
    ui.activityList.append(element('li', 'activity-empty', 'No activity yet. Your first image check will appear here.'));
    return;
  }
  events.slice(0, 15).forEach((event) => {
    const item = element('li', 'activity-item');

    const mark = element('span', `activity-mark ${event.type || 'info'}`);
    mark.setAttribute('aria-hidden', 'true');

    const copy = element('div', 'activity-copy');
    const titleRow = element('div', 'activity-top');
    titleRow.append(element('span', 'activity-title', event.title));

    const time = element('time', 'activity-time', shortRelative(event.time));
    time.dateTime = event.time;
    time.title = exactDate(event.time);
    titleRow.append(time);

    copy.append(titleRow);

    if (event.detail) {
      copy.append(element('p', 'activity-detail', event.detail));
    }

    item.append(mark, copy);
    ui.activityList.append(item);
  });
}

function setFilter(filter) {
  currentFilter = filter;
  ui.filterTabs.forEach((tab) => {
    const isActive = tab.dataset.filter === filter;
    tab.classList.toggle('is-active', isActive);
    tab.setAttribute('aria-selected', String(isActive));
  });
  if (snapshot?.stacks) {
    renderStacks(snapshot.stacks);
  }
}

function render(data) {
  snapshot = data;
  const stacks = data.stacks || [];
  const updates = stacks.reduce((sum, stack) => sum + stack.updatesAvailable, 0);
  const errors = stacks.filter((stack) => stack.status === 'error').length;
  const currentCount = stacks.filter((stack) => stack.updatesAvailable === 0 && stack.status !== 'error').length;
  const hasChecked = data.lastCheck && !data.lastCheck.startsWith('0001-');

  // Overview metrics & filter counts
  ui.metricStacks.textContent = String(stacks.length);
  ui.metricUpdates.textContent = String(updates);
  ui.metricCurrent.textContent = String(currentCount);
  ui.metricErrors.textContent = String(errors);

  if (ui.filterErrorsTab) {
    ui.filterErrorsTab.hidden = errors === 0;
  }

  ui.metricLast.textContent = shortRelative(data.lastCheck);
  ui.metricLastExact.textContent = exactDate(data.lastCheck) || 'No check recorded';
  ui.metricNext.textContent = timeOnly(data.nextCheck);
  ui.metricNextExact.textContent = data.settings.autoUpdatePolicy === 'off'
    ? '(Manual approval)'
    : `(Auto: ${data.settings.autoUpdatePolicy})`;

  ui.stackSummary.textContent = errors
    ? `${errors} stack${errors === 1 ? '' : 's'} require attention`
    : (!hasChecked
      ? `${stacks.length} stack${stacks.length === 1 ? '' : 's'} ready for first check`
      : `${updates} pending update${updates === 1 ? '' : 's'} across ${stacks.length} stack${stacks.length === 1 ? '' : 's'}`);

  ui.activityCount.textContent = String((data.events || []).length);
  ui.footerVersion.textContent = `v${data.version || '1.0'} · single binary`;

  // System & Job State
  const isRunning = Boolean(data.job?.running);
  ui.jobStrip.hidden = !isRunning;
  ui.checkAll.disabled = isRunning;
  ui.checkAll.classList.toggle('is-loading', isRunning);

  if (isRunning) {
    ui.systemLabel.textContent = data.job.kind === 'update' ? 'Updating stack…' : 'Checking images…';
    ui.systemPill.className = 'system-pill pill-working';
    ui.jobMessage.textContent = data.job.message || 'Processing Docker task…';
    const elapsed = Math.max(0, Math.round((Date.now() - new Date(data.job.startedAt).getTime()) / 1000));
    ui.jobElapsed.textContent = `${elapsed}s`;
  } else {
    ui.systemLabel.textContent = 'System ready';
    ui.systemPill.className = 'system-pill pill-ready';
  }

  renderStacks(stacks);
  renderEvents(data.events || []);
  schedulePoll(isRunning ? 1500 : 15000);
}

async function refresh(showErrors = true) {
  try {
    render(await request('/api/status'));
  } catch (error) {
    ui.systemLabel.textContent = 'Disconnected';
    ui.systemPill.className = 'system-pill pill-error';
    if (showErrors) showToast(error.message, true);
    schedulePoll(5000);
  }
}

function schedulePoll(delay) {
  window.clearTimeout(pollTimer);
  pollTimer = window.setTimeout(() => refresh(false), delay);
}

function showToast(message, isError = false) {
  window.clearTimeout(toastTimer);
  ui.toast.textContent = message;
  ui.toast.className = `toast ${isError ? 'is-error' : 'is-success'}`;
  ui.toast.hidden = false;
  toastTimer = window.setTimeout(() => { ui.toast.hidden = true; }, 4500);
}

async function beginCheck() {
  try {
    await request('/api/check', { method: 'POST' });
    showToast('Image check initiated');
    await refresh();
  } catch (error) {
    showToast(error.message, true);
  }
}

async function updateStack(stack) {
  const msg = `Apply updates to "${stack.name}"?\nDocker Compose will pull latest images and recreate containers.`;
  if (!window.confirm(msg)) return;
  try {
    await request(`/api/stacks/${encodeURIComponent(stack.id)}/update`, { method: 'POST' });
    showToast(`Updating stack "${stack.name}"…`);
    await refresh();
  } catch (error) {
    showToast(error.message, true);
  }
}

// --- Settings Dialog ---
function syncPolicyFields() {
  const policy = new FormData(ui.settingsForm).get('autoUpdatePolicy');
  const off = policy === 'off';
  ui.scheduleFields.classList.toggle('is-off', off);
  ui.updateTime.disabled = off;
  ui.updateWeekday.disabled = off || policy !== 'weekly';
}

function openSettings() {
  if (!snapshot) return;
  const settings = snapshot.settings;
  ui.settingsForm.elements.checkTime.value = settings.checkTime;
  ui.settingsForm.elements.updateTime.value = settings.updateTime;
  ui.settingsForm.elements.updateWeekday.value = settings.updateWeekday;
  const policy = ui.settingsForm.querySelector(`[name="autoUpdatePolicy"][value="${settings.autoUpdatePolicy}"]`);
  if (policy) policy.checked = true;
  syncPolicyFields();
  ui.settingsDialog.showModal();
  window.requestAnimationFrame(() => ui.settingsForm.elements.checkTime.focus());
}

async function saveSettings(event) {
  event.preventDefault();
  const form = new FormData(ui.settingsForm);
  const payload = {
    checkTime: form.get('checkTime'),
    autoUpdatePolicy: form.get('autoUpdatePolicy'),
    updateTime: form.get('updateTime') || snapshot.settings.updateTime,
    updateWeekday: form.get('updateWeekday') || snapshot.settings.updateWeekday,
  };
  try {
    await request('/api/settings', { method: 'PUT', body: JSON.stringify(payload) });
    ui.settingsDialog.close();
    showToast('Schedule settings saved');
    await refresh();
  } catch (error) {
    showToast(error.message, true);
  }
}

// --- Event Bindings ---
ui.checkAll.addEventListener('click', beginCheck);
ui.settingsOpen.addEventListener('click', openSettings);
ui.settingsClose.addEventListener('click', () => ui.settingsDialog.close());
ui.settingsCancel.addEventListener('click', () => ui.settingsDialog.close());
ui.settingsForm.addEventListener('change', (event) => {
  if (event.target.name === 'autoUpdatePolicy') syncPolicyFields();
});
ui.settingsForm.addEventListener('submit', saveSettings);

ui.settingsDialog.addEventListener('click', (event) => {
  const bounds = ui.settingsDialog.getBoundingClientRect();
  const inside = event.clientX >= bounds.left && event.clientX <= bounds.right &&
                 event.clientY >= bounds.top && event.clientY <= bounds.bottom;
  if (!inside) ui.settingsDialog.close();
});

// Search & Filter Bindings
ui.filterTabs.forEach((tab) => {
  tab.addEventListener('click', () => setFilter(tab.dataset.filter));
});

ui.stackSearch.addEventListener('input', (e) => {
  searchQuery = e.target.value.trim();
  ui.searchClear.hidden = !searchQuery;
  if (snapshot?.stacks) renderStacks(snapshot.stacks);
});

ui.searchClear.addEventListener('click', () => {
  searchQuery = '';
  ui.stackSearch.value = '';
  ui.searchClear.hidden = true;
  ui.stackSearch.focus();
  if (snapshot?.stacks) renderStacks(snapshot.stacks);
});

// Initialize
initTheme();
refresh();

