const ui = {
  systemLabel: document.querySelector('#system-label'),
  metricUpdates: document.querySelector('#metric-updates'),
  metricUpdateNote: document.querySelector('#metric-update-note'),
  metricStacks: document.querySelector('#metric-stacks'),
  metricLast: document.querySelector('#metric-last'),
  metricLastExact: document.querySelector('#metric-last-exact'),
  metricNext: document.querySelector('#metric-next'),
  metricNextExact: document.querySelector('#metric-next-exact'),
  stackSummary: document.querySelector('#stack-summary'),
  stackList: document.querySelector('#stack-list'),
  activityList: document.querySelector('#activity-list'),
  activityCount: document.querySelector('#activity-count'),
  jobStrip: document.querySelector('#job-strip'),
  jobMessage: document.querySelector('#job-message'),
  jobElapsed: document.querySelector('#job-elapsed'),
  checkAll: document.querySelector('#check-all'),
  settingsDialog: document.querySelector('#settings-dialog'),
  settingsForm: document.querySelector('#settings-form'),
  scheduleFields: document.querySelector('#schedule-fields'),
  updateWeekday: document.querySelector('#update-weekday'),
  updateTime: document.querySelector('#update-time'),
  toast: document.querySelector('#toast'),
  footerVersion: document.querySelector('#footer-version'),
};

let snapshot = null;
let pollTimer = null;
let toastTimer = null;

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
    update_available: [`${stack.updatesAvailable} update${stack.updatesAvailable === 1 ? '' : 's'}`, 'status-update'],
    up_to_date: ['Current', 'status-current'],
    error: ['Needs attention', 'status-error'],
    checking: ['Checking', 'status-checking'],
    unknown: ['Not checked', 'status-unknown'],
  };
  return values[stack.status] || values.unknown;
}

function renderStack(stack) {
  const card = element('article', 'stack-card');
  const main = element('div', 'stack-main');
  const identity = element('div', 'stack-identity');
  const glyph = element('span', 'stack-glyph', 'DC');
  glyph.setAttribute('aria-hidden', 'true');
  const names = element('div');
  names.append(element('div', 'stack-name', stack.name));
  names.append(element('div', 'stack-meta', stack.composeFile.split('/').pop()));
  identity.append(glyph, names);

  const actions = element('div', 'stack-actions');
  const [label, className] = statusInfo(stack);
  actions.append(element('span', `status-badge ${className}`, label));
  const update = element('button', 'button button-quiet', 'Update stack');
  update.type = 'button';
  update.dataset.stackId = stack.id;
  update.disabled = snapshot.job.running || stack.updatesAvailable === 0;
  update.title = snapshot.job.running ? 'Another Docker job is running' : (stack.updatesAvailable === 0 ? 'No newer image detected' : `Apply ${stack.updatesAvailable} service update${stack.updatesAvailable === 1 ? '' : 's'}`);
  update.addEventListener('click', () => updateStack(stack));
  actions.append(update);
  main.append(identity, actions);
  card.append(main);

  const details = element('details', 'service-details');
  const serviceCount = stack.services?.length || 0;
  const summaryText = stack.error ? stack.error : `${serviceCount} service${serviceCount === 1 ? '' : 's'} · ${stack.lastChecked && !stack.lastChecked.startsWith('0001-') ? `checked ${shortRelative(stack.lastChecked)}` : 'awaiting check'}`;
  details.append(element('summary', '', summaryText));
  const list = element('div', 'service-list');
  if (serviceCount === 0) {
    list.append(element('div', 'service-row', stack.error || 'Services appear after the first successful check.'));
  } else {
    stack.services.forEach((service) => {
      const row = element('div', 'service-row');
      row.append(element('span', 'service-name', service.name));
      row.append(element('span', 'service-image', service.image));
      const state = service.updateAvailable ? 'Update available' : (service.containerState || 'not deployed');
      row.append(element('span', `service-state${service.updateAvailable ? ' is-update' : ''}`, state));
      list.append(row);
    });
  }
  details.append(list);
  card.append(details);
  return card;
}

function renderStacks(stacks) {
  ui.stackList.replaceChildren();
  if (!stacks.length) {
    const empty = element('div', 'empty-state');
    empty.append(element('strong', '', 'No Compose stacks found'));
    empty.append(element('p', '', 'Check that STACKS_DIR points to the directory Dockge uses for its stacks.'));
    ui.stackList.append(empty);
    return;
  }
  stacks.forEach((stack) => ui.stackList.append(renderStack(stack)));
}

function renderEvents(events) {
  ui.activityList.replaceChildren();
  if (!events.length) {
    ui.activityList.append(element('li', 'activity-empty', 'No activity yet. Your first image check will appear here.'));
    return;
  }
  events.slice(0, 10).forEach((event) => {
    const item = element('li', 'activity-item');
    item.append(element('span', `activity-mark ${event.type}`));
    const copy = element('div', 'activity-copy');
    copy.append(element('strong', '', event.title));
    if (event.detail) copy.append(element('p', '', event.detail));
    const timestamp = element('time', '', shortRelative(event.time));
    timestamp.dateTime = event.time;
    timestamp.title = exactDate(event.time);
    copy.append(timestamp);
    item.append(copy);
    ui.activityList.append(item);
  });
}

function render(data) {
  snapshot = data;
  const stacks = data.stacks || [];
  const updates = stacks.reduce((sum, stack) => sum + stack.updatesAvailable, 0);
  const errors = stacks.filter((stack) => stack.status === 'error').length;
  const hasChecked = data.lastCheck && !data.lastCheck.startsWith('0001-');
  ui.metricUpdates.textContent = hasChecked ? String(updates) : '—';
  ui.metricUpdateNote.textContent = !hasChecked ? 'Waiting for first check' : (updates ? `${updates} service${updates === 1 ? '' : 's'} ready to apply` : 'All checked services are current');
  ui.metricStacks.textContent = String(stacks.length);
  ui.metricLast.textContent = shortRelative(data.lastCheck);
  ui.metricLastExact.textContent = exactDate(data.lastCheck) || 'No check recorded';
  ui.metricNext.textContent = timeOnly(data.nextCheck);
  ui.metricNextExact.textContent = data.settings.autoUpdatePolicy === 'off' ? 'Updates require approval' : `Auto-update: ${data.settings.autoUpdatePolicy}`;
  ui.stackSummary.textContent = errors ? `${errors} stack${errors === 1 ? '' : 's'} need attention` : (!hasChecked ? `${stacks.length} stack${stacks.length === 1 ? '' : 's'} ready for first check` : `${updates} pending across ${stacks.length} stacks`);
  ui.activityCount.textContent = String((data.events || []).length);
  ui.footerVersion.textContent = `v${data.version} · single binary · local state`;

  ui.jobStrip.hidden = !data.job.running;
  ui.checkAll.disabled = data.job.running;
  ui.checkAll.dataset.state = data.job.running ? 'loading' : '';
  ui.systemLabel.textContent = data.job.running ? 'Working' : 'System ready';
  if (data.job.running) {
    ui.jobMessage.textContent = data.job.message || 'Working';
    const elapsed = Math.max(0, Math.round((Date.now() - new Date(data.job.startedAt).getTime()) / 1000));
    ui.jobElapsed.textContent = `${elapsed}s`;
  }
  renderStacks(stacks);
  renderEvents(data.events || []);
  schedulePoll(data.job.running ? 1500 : 15000);
}

async function refresh(showErrors = true) {
  try {
    render(await request('/api/status'));
  } catch (error) {
    ui.systemLabel.textContent = 'Disconnected';
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
  ui.toast.classList.toggle('is-error', isError);
  ui.toast.hidden = false;
  toastTimer = window.setTimeout(() => { ui.toast.hidden = true; }, 4200);
}

async function beginCheck() {
  try {
    await request('/api/check', { method: 'POST' });
    await refresh();
  } catch (error) {
    showToast(error.message, true);
  }
}

async function updateStack(stack) {
  if (!window.confirm(`Update ${stack.name}? Docker Compose will recreate containers that use newer images.`)) return;
  try {
    await request(`/api/stacks/${encodeURIComponent(stack.id)}/update`, { method: 'POST' });
    await refresh();
  } catch (error) {
    showToast(error.message, true);
  }
}

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
    await refresh();
  } catch (error) {
    showToast(error.message, true);
  }
}

ui.checkAll.addEventListener('click', beginCheck);
document.querySelector('#settings-open').addEventListener('click', openSettings);
document.querySelector('#settings-close').addEventListener('click', () => ui.settingsDialog.close());
document.querySelector('#settings-cancel').addEventListener('click', () => ui.settingsDialog.close());
ui.settingsForm.addEventListener('change', (event) => { if (event.target.name === 'autoUpdatePolicy') syncPolicyFields(); });
ui.settingsForm.addEventListener('submit', saveSettings);
ui.settingsDialog.addEventListener('click', (event) => {
  const bounds = ui.settingsDialog.getBoundingClientRect();
  const inside = event.clientX >= bounds.left && event.clientX <= bounds.right && event.clientY >= bounds.top && event.clientY <= bounds.bottom;
  if (!inside) ui.settingsDialog.close();
});

refresh();
