/**
 * Velox Web Dashboard — Single Page Reactive Client
 */

const app = {
  state: {
    activeTab: 'dashboard',
    status: null,
    configs: [],
    configsPage: 0,
    configsLimit: 50,
    configsProto: '',
    configsSearch: '',
    configsSortBy: 'updated',
    profiles: [],
    eventSource: null,

    // Application Logs & Telemetry
    appLogs: [],
    appLogsTotal: 0,
    appLogsPage: 0,
    appLogsLimit: 100,
    appLogsLevel: 'all',
    appLogsSource: 'all',
    appLogsSearch: '',
    liveLogsActive: true,
    expandedLogIds: new Set(),
  },

  init() {
    this.bindEvents();
    this.setupEventSource();
    this.showLoginResult();
    this.refreshStatus();
    this.loadLogStats();
    
    // Poll status periodically every 3 seconds
    setInterval(() => {
      this.refreshStatus();
      if (this.state.activeTab === 'profiles') this.loadProfiles();
      if (this.state.activeTab === 'sources') this.loadSources();
    }, 3000);
  },

  bindEvents() {
    // Nav tabs
    document.querySelectorAll('.nav-btn').forEach(btn => {
      btn.addEventListener('click', () => {
        this.switchTab(btn.dataset.tab);
      });
    });

    // Refresh all
    document.getElementById('btn-refresh-all')?.addEventListener('click', () => {
      this.refreshStatus();
      this.showToast('Data refreshed', 'info');
    });

    // Protocol filter pills in Configs view
    document.querySelectorAll('#configs-proto-filters .filter-pill').forEach(pill => {
      pill.addEventListener('click', () => {
        document.querySelectorAll('#configs-proto-filters .filter-pill').forEach(p => p.classList.remove('active'));
        pill.classList.add('active');
        this.state.configsProto = pill.dataset.proto;
        this.state.configsPage = 0;
        this.loadConfigs();
      });
    });

    // Sort By dropdown
    document.getElementById('configs-sort-select')?.addEventListener('change', (e) => {
      this.state.configsSortBy = e.target.value;
      this.state.configsPage = 0;
      this.loadConfigs();
    });

    // Search input
    let searchTimeout = null;
    document.getElementById('configs-search-input')?.addEventListener('input', (e) => {
      clearTimeout(searchTimeout);
      searchTimeout = setTimeout(() => {
        this.state.configsSearch = e.target.value.trim();
        this.state.configsPage = 0;
        this.loadConfigs();
      }, 300);
    });

    // Pagination
    document.getElementById('btn-prev-page')?.addEventListener('click', () => {
      if (this.state.configsPage > 0) {
        this.state.configsPage--;
        this.loadConfigs();
      }
    });
    document.getElementById('btn-next-page')?.addEventListener('click', () => {
      this.state.configsPage++;
      this.loadConfigs();
    });

    // Sources
    document.getElementById('btn-add-source')?.addEventListener('click', () => this.addSource());
    document.getElementById('btn-paste-source')?.addEventListener('click', () => this.pasteSource());
    document.getElementById('btn-run-refresh')?.addEventListener('click', () => this.runOptimizer('refresh'));

    // General Options (subscriptions) schedule
    document.getElementById('btn-save-schedule')?.addEventListener('click', () => this.saveSchedule());

    // General Options modal: open from the Profiles button or the always-on status pill; close on ×, Close, or backdrop click.
    const optDialog = document.getElementById('optimizer-dialog');
    document.getElementById('btn-optimizer')?.addEventListener('click', () => optDialog?.showModal());
    document.getElementById('optimizer-status-pill')?.addEventListener('click', () => optDialog?.showModal());
    optDialog?.querySelectorAll('.js-opt-close').forEach(b => b.addEventListener('click', () => optDialog.close()));
    optDialog?.addEventListener('click', (e) => { if (e.target === optDialog) optDialog.close(); });

    // Row actions: rows carry data-action + data-id, never untrusted data inside inline JS.
    document.addEventListener('click', (e) => {
      const el = e.target.closest('[data-action]');
      if (!el || el.tagName === 'INPUT') return;
      const id = el.dataset.id;
      switch (el.dataset.action) {
        case 'profile-settings': this.toggleSettings(Number(id)); break;
        case 'profile-save': this.saveProfile(Number(id)); break;
        case 'profile-genpw': {
          const pw = this.cardPanel(Number(id), '.pc-settings').querySelector('[data-field="password"]');
          pw.value = Array.from(crypto.getRandomValues(new Uint8Array(12)), b => b.toString(16).padStart(2, '0')).join('');
          break;
        }
        case 'profile-results': this.toggleResults(Number(id)); break;
        case 'profile-results-refresh': this.toggleResults(Number(id), true); break;
        case 'profile-results-filter':
          this.cardPanel(Number(id), '.pc-results').dataset.all = el.dataset.all;
          this.renderResults(Number(id));
          break;
        case 'profile-copy': this.copyProfileURL(Number(id), el.dataset.scheme); break;
        case 'profile-set-active': this.setActiveNode(Number(id), el.dataset.config); break;
        case 'profile-toggle': this.toggleProfileCard(Number(id)); break;
        case 'profile-test': this.testProfile(Number(id)); break;
        case 'profile-login': this.loginProfile(Number(id)); break;
        case 'profile-logout': this.logoutProfile(Number(id)); break;
        case 'source-delete': this.deleteSource(Number(id)); break;
        case 'config-copy': this.copyURI((this.state.configs[Number(id)] || {}).raw_uri); break;
        case 'log-copy': this.copyLogJSON(Number(id)); break;
      }
    });
    // Profile cards open from the keyboard too (Enter or Space on the focused card).
    document.addEventListener('keydown', (e) => {
      if ((e.key === 'Enter' || e.key === ' ') && e.target.dataset && e.target.dataset.action === 'profile-toggle') {
        e.preventDefault();
        this.toggleProfileCard(Number(e.target.dataset.id));
      }
    });
    document.addEventListener('change', (e) => {
      if (e.target.dataset.action === 'source-toggle') {
        this.toggleSource(Number(e.target.dataset.id), e.target.checked);
      }
      // Enable the interval only when auto-test is turned on, in the same settings panel.
      if (e.target.dataset.field === 'auto_test') {
        const sel = e.target.closest('.pc-settings')?.querySelector('[data-field="test_interval"]');
        if (sel) sel.disabled = !e.target.checked;
      }
    });

    // Application Logs & Telemetry Events
    document.getElementById('btn-refresh-logs')?.addEventListener('click', () => {
      this.loadLogs();
      this.loadLogStats();
      this.showToast('Application logs refreshed', 'info');
    });

    document.getElementById('btn-export-logs')?.addEventListener('click', () => {
      const url = `/api/logs/export?format=json&level=${encodeURIComponent(this.state.appLogsLevel)}&source=${encodeURIComponent(this.state.appLogsSource)}&search=${encodeURIComponent(this.state.appLogsSearch)}`;
      window.location.href = url;
    });

    document.getElementById('btn-prune-logs')?.addEventListener('click', () => {
      if (confirm('Prune logs older than the current retention policy now?')) {
        this.pruneLogsNow();
      }
    });

    document.getElementById('btn-clear-logs')?.addEventListener('click', () => {
      if (confirm('Are you sure you want to permanently clear all stored application logs and reclaim database space?')) {
        this.clearLogsAll();
      }
    });

    document.getElementById('btn-save-retention')?.addEventListener('click', () => {
      const select = document.getElementById('log-retention-select');
      if (select) {
        this.saveRetention(select.value);
      }
    });

    let logSearchTimeout = null;
    document.getElementById('logs-search-input')?.addEventListener('input', (e) => {
      clearTimeout(logSearchTimeout);
      logSearchTimeout = setTimeout(() => {
        this.state.appLogsSearch = e.target.value.trim();
        this.state.appLogsPage = 0;
        this.loadLogs();
      }, 300);
    });

    document.querySelectorAll('.log-filter-chip').forEach(chip => {
      chip.addEventListener('click', () => {
        document.querySelectorAll('.log-filter-chip').forEach(c => c.classList.remove('active'));
        chip.classList.add('active');
        this.state.appLogsLevel = chip.dataset.level;
        this.state.appLogsPage = 0;
        this.loadLogs();
      });
    });

    document.getElementById('logs-source-select')?.addEventListener('change', (e) => {
      this.state.appLogsSource = e.target.value;
      this.state.appLogsPage = 0;
      this.loadLogs();
    });

    document.getElementById('toggle-live-logs')?.addEventListener('change', (e) => {
      this.state.liveLogsActive = e.target.checked;
      const statusEl = document.getElementById('live-logs-status');
      if (statusEl) {
        statusEl.textContent = e.target.checked ? '● Live Stream' : '○ Paused';
        statusEl.className = e.target.checked ? 'text-emerald' : 'text-muted';
      }
    });

    document.getElementById('btn-logs-prev')?.addEventListener('click', () => {
      if (this.state.appLogsPage > 0) {
        this.state.appLogsPage--;
        this.loadLogs();
      }
    });

    document.getElementById('btn-logs-next')?.addEventListener('click', () => {
      this.state.appLogsPage++;
      this.loadLogs();
    });
  },

  switchTab(tabId) {
    this.state.activeTab = tabId;
    document.querySelectorAll('.nav-btn').forEach(b => {
      b.classList.toggle('active', b.dataset.tab === tabId);
    });
    document.querySelectorAll('.tab-pane').forEach(p => {
      p.classList.toggle('active', p.id === `tab-${tabId}`);
    });

    if (tabId === 'configs') {
      this.loadConfigs();
    } else if (tabId === 'profiles') {
      this.loadProfiles();
    } else if (tabId === 'sources') {
      this.loadSources();
    } else if (tabId === 'logs') {
      this.loadLogs();
      this.loadLogStats();
    }
  },

  async refreshStatus() {
    try {
      const res = await fetch('/api/status');
      if (!res.ok) return;
      const data = await res.json();
      this.state.status = data;
      this.renderStatus(data);
    } catch (e) {
      console.error('Failed to fetch status', e);
    }
  },

  renderStatus(data) {
    // Total configs
    const totalElem = document.getElementById('stat-total-configs');
    if (totalElem) totalElem.textContent = (data.total_configs || 0).toLocaleString();

    // Profiles & optimizer
    const profiles = data.profiles || [];
    const running = profiles.filter(p => p.running).length;
    const profElem = document.getElementById('stat-profiles');
    if (profElem) profElem.textContent = `${running} / ${profiles.length} running`;
    const srcElem = document.getElementById('stat-sources');
    if (srcElem) srcElem.textContent = `${(data.sources || []).length} sources`;
    this.state.progress = (data.optimizer && data.optimizer.progress) || {};
    this.renderOptimizer(data.optimizer);

    // Protocols Breakdown
    this.renderProtocolDistribution(data.protocol_counts || {}, data.total_configs || 0);
  },

  renderProtocolDistribution(counts, total) {
    const totalBadge = document.getElementById('proto-total-badge');
    if (totalBadge) totalBadge.textContent = `${total} nodes`;

    const barContainer = document.getElementById('protocol-bars');
    const chipContainer = document.getElementById('protocol-badges');
    if (!barContainer || !chipContainer) return;

    barContainer.innerHTML = '';
    chipContainer.innerHTML = '';

    const colors = {
      vless: 'var(--proto-vless)',
      hysteria2: 'var(--proto-hy2)',
      trojan: 'var(--proto-trojan)',
      shadowsocks: 'var(--proto-shadowsocks)',
      vmess: 'var(--proto-vmess)',
    };

    for (const [proto, count] of Object.entries(counts)) {
      const pct = total > 0 ? ((count / total) * 100).toFixed(1) : 0;
      const color = colors[proto.toLowerCase()] || '#64748b';

      // Bar segment
      const segment = document.createElement('div');
      segment.className = 'proto-bar';
      segment.style.width = `${pct}%`;
      segment.style.backgroundColor = color;
      segment.title = `${proto}: ${count} (${pct}%)`;
      barContainer.appendChild(segment);

      // Chip
      const chip = document.createElement('div');
      chip.className = 'proto-chip';
      chip.innerHTML = `
        <span class="proto-dot" style="background-color: ${color};"></span>
        <span style="text-transform: capitalize; font-weight: 600;">${proto}</span>
        <span class="text-muted text-xs">(${count})</span>
      `;
      chipContainer.appendChild(chip);
    }
  },

  async loadConfigs() {
    try {
      const offset = this.state.configsPage * this.state.configsLimit;
      const url = new URL('/api/configs', window.location.origin);
      url.searchParams.set('limit', this.state.configsLimit);
      url.searchParams.set('offset', offset);
      if (this.state.configsProto) url.searchParams.set('protocol', this.state.configsProto);
      if (this.state.configsSearch) url.searchParams.set('search', this.state.configsSearch);
      if (this.state.configsSortBy) url.searchParams.set('sort_by', this.state.configsSortBy);

      const res = await fetch(url);
      if (!res.ok) return;
      const data = await res.json();
      const configs = data.configs || [];
      const total = data.total || 0;
      this.state.configs = configs;

      const tbody = document.getElementById('all-configs-tbody');
      if (!tbody) return;

      if (configs.length === 0) {
        tbody.innerHTML = `<tr><td colspan="7" class="text-muted text-center py-4">No configs match your filter.</td></tr>`;
      } else {
        tbody.innerHTML = configs.map((cfg, idx) => {
          const rank = offset + idx + 1;
          const proto = this.escapeHtml(cfg.protocol);
          const transportInfo = this.escapeHtml(`${cfg.network || 'tcp'} / ${cfg.security || 'none'}`);

          return `
            <tr>
              <td class="text-muted font-bold">${rank}</td>
              <td><span class="badge badge-${proto}">${proto}</span></td>
              <td class="font-medium">${this.escapeHtml(cfg.name || 'Unnamed Node')}</td>
              <td class="text-mono text-sm">${this.escapeHtml(cfg.address)}:${Number(cfg.port)}</td>
              <td class="text-xs text-muted">${transportInfo}</td>
              <td class="text-xs text-muted" title="${this.escapeHtml(cfg.source)}">${this.fmtTime(cfg.updated_at)}</td>
              <td>
                <button class="btn btn-secondary btn-sm" data-action="config-copy" data-id="${idx}" title="Copy URI">📋 Copy</button>
              </td>
            </tr>
          `;
        }).join('');
      }

      // Pagination controls
      const countInfo = document.getElementById('configs-count-info');
      const prevBtn = document.getElementById('btn-prev-page');
      const nextBtn = document.getElementById('btn-next-page');

      if (countInfo) {
        const start = total > 0 ? offset + 1 : 0;
        const end = Math.min(offset + configs.length, total);
        countInfo.textContent = `Showing ${start}-${end} of ${total} configs`;
      }
      if (prevBtn) prevBtn.disabled = this.state.configsPage === 0;
      if (nextBtn) nextBtn.disabled = offset + configs.length >= total;

    } catch (e) {
      console.error('Failed to load configs', e);
    }
  },

  setupEventSource() {
    if (this.state.eventSource) {
      this.state.eventSource.close();
    }

    const es = new EventSource('/api/events');
    this.state.eventSource = es;
    es.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data);
        this.handleStreamMessage(msg);
      } catch (err) {
        console.error('Failed to parse SSE message', err);
      }
    };
  },

  handleStreamMessage(msg) {
    switch (msg.type) {
      case 'app_log':
        if (msg.log) this.handleLiveAppLog(msg.log);
        break;
      case 'optimizer_status':
        this.state.progress = msg.status.progress || {};
        this.renderOptimizer(msg.status);
        if (this.state.activeTab === 'profiles') this.loadProfiles();
        break;
    }
  },

  copyURI(uri) {
    if (!uri) return;
    this.copyToClipboard(uri);
  },

  copyToClipboard(text) {
    if (!text) return;
    navigator.clipboard.writeText(text).then(() => {
      this.showToast('Copied to clipboard!', 'success');
    }).catch(() => {
      this.showToast('Failed to copy to clipboard', 'error');
    });
  },

  showToast(message, type = 'info') {
    const container = document.getElementById('toast-container');
    if (!container) return;

    const toast = document.createElement('div');
    toast.className = `toast ${type}`;
    toast.innerHTML = `
      <span>${type === 'success' ? '✅' : type === 'error' ? '⚠️' : 'ℹ️'}</span>
      <span>${this.escapeHtml(message)}</span>
    `;

    container.appendChild(toast);
    setTimeout(() => {
      toast.style.opacity = '0';
      toast.style.transform = 'translateX(100%)';
      toast.style.transition = 'all 0.3s ease';
      setTimeout(() => toast.remove(), 300);
    }, 4000);
  },

  escapeHtml(str) {
    if (!str) return '';
    return String(str).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  },

  // ==========================================
  // Application Logs & Telemetry Methods
  // ==========================================

  async loadLogs() {
    try {
      const offset = this.state.appLogsPage * this.state.appLogsLimit;
      const url = new URL('/api/logs', window.location.origin);
      url.searchParams.set('limit', this.state.appLogsLimit);
      url.searchParams.set('offset', offset);
      if (this.state.appLogsLevel && this.state.appLogsLevel !== 'all') {
        url.searchParams.set('level', this.state.appLogsLevel);
      }
      if (this.state.appLogsSource && this.state.appLogsSource !== 'all') {
        url.searchParams.set('source', this.state.appLogsSource);
      }
      if (this.state.appLogsSearch) {
        url.searchParams.set('search', this.state.appLogsSearch);
      }

      const res = await fetch(url);
      if (!res.ok) return;
      const data = await res.json();
      this.state.appLogs = data.logs || [];
      this.state.appLogsTotal = data.total || 0;

      this.renderLogsTable();
      this.updateLogsPagination();
    } catch (e) {
      console.error('Failed to load application logs', e);
    }
  },

  async loadLogStats() {
    try {
      const res = await fetch('/api/logs/stats');
      if (!res.ok) return;
      const data = await res.json();

      const totalEl = document.getElementById('log-stat-total');
      const errorsEl = document.getElementById('log-stat-errors');
      const errorRateEl = document.getElementById('log-stat-error-rate');
      const warnsEl = document.getElementById('log-stat-warns');
      const dbSizeEl = document.getElementById('log-stat-db-size');
      const retentionSelect = document.getElementById('log-retention-select');
      const retentionBadge = document.getElementById('retention-badge');

      const total = data.total_count || 0;
      const errors = (data.level_counts && data.level_counts['ERROR']) || 0;
      const warns = (data.level_counts && data.level_counts['WARN']) || 0;

      if (totalEl) totalEl.textContent = total.toLocaleString();
      if (errorsEl) errorsEl.textContent = errors.toLocaleString();
      if (errorRateEl) {
        const rate = total > 0 ? ((errors / total) * 100).toFixed(1) : 0;
        errorRateEl.textContent = `${rate}% Error Rate`;
      }
      if (warnsEl) warnsEl.textContent = warns.toLocaleString();
      if (dbSizeEl && data.db_size_bytes) {
        const mb = (data.db_size_bytes / (1024 * 1024)).toFixed(2);
        dbSizeEl.textContent = `DB Size: ${mb} MB`;
      }
      if (data.retention) {
        if (retentionSelect) retentionSelect.value = data.retention;
        if (retentionBadge) retentionBadge.textContent = this.formatRetentionName(data.retention);
      }
    } catch (e) {
      console.error('Failed to load log stats', e);
    }
  },

  formatRetentionName(r) {
    switch (r) {
      case '24h': return '24 Hours';
      case '3d': return '3 Days';
      case '7d': return '7 Days';
      case '30d': return '30 Days';
      case '90d': return '90 Days';
      case '365d': return '1 Year';
      default: return r;
    }
  },

  renderLogsTable() {
    const tbody = document.getElementById('logs-table-tbody');
    if (!tbody) return;

    if (this.state.appLogs.length === 0) {
      tbody.innerHTML = `<tr><td colspan="5" class="text-center py-4 text-muted">No application logs found matching query filters.</td></tr>`;
      return;
    }

    tbody.innerHTML = this.state.appLogs.map(log => this.createLogRowHtml(log)).join('');
  },

  createLogRowHtml(log, isLive = false) {
    const lvl = (log.level || 'INFO').toUpperCase();
    const lvlClass = lvl.toLowerCase();
    const ts = log.timestamp ? log.timestamp.replace('T', ' ').replace('Z', '').split('.')[0] : '—';
    const hasAttrs = log.attrs && Object.keys(log.attrs).length > 0;
    const isExpanded = this.state.expandedLogIds.has(log.id);

    let attrsBtn = '<span class="text-muted text-xs">—</span>';
    if (hasAttrs) {
      const cnt = Object.keys(log.attrs).length;
      attrsBtn = `<button class="btn btn-secondary btn-xs" onclick="event.stopPropagation(); app.toggleLogExpand(${log.id});">${isExpanded ? 'Hide' : 'Inspect (' + cnt + ')'}</button>`;
    }

    const mainRow = `
      <tr class="log-entry-row ${isExpanded ? 'expanded' : ''} ${isLive ? 'live-new' : ''}" id="log-row-${log.id}" onclick="app.toggleLogExpand(${log.id})">
        <td class="text-mono text-xs text-muted">${this.escapeHtml(ts)}</td>
        <td><span class="log-badge ${lvlClass}">${lvl}</span></td>
        <td><span class="subsystem-badge">${this.escapeHtml(log.source || 'system')}</span></td>
        <td class="log-message-cell font-medium">${this.escapeHtml(log.message || '')}</td>
        <td>${attrsBtn}</td>
      </tr>
    `;

    if (!isExpanded || !hasAttrs) {
      return mainRow;
    }

    const prettyJSON = JSON.stringify(log.attrs, null, 2);
    const detailRow = `
      ${mainRow}
      <tr class="log-details-row" id="log-detail-${log.id}">
        <td colspan="5">
          <div class="log-drawer">
            <div class="log-drawer-header">
              <span class="text-xs text-muted font-bold">Log Record Attributes & Context</span>
              <button class="btn btn-secondary btn-xs" data-action="log-copy" data-id="${Number(log.id)}" onclick="event.stopPropagation()">Copy JSON</button>
            </div>
            <pre class="log-json-block">${this.escapeHtml(prettyJSON)}</pre>
          </div>
        </td>
      </tr>
    `;

    return detailRow;
  },

  toggleLogExpand(id) {
    if (this.state.expandedLogIds.has(id)) {
      this.state.expandedLogIds.delete(id);
    } else {
      this.state.expandedLogIds.add(id);
    }
    this.renderLogsTable();
  },

  updateLogsPagination() {
    const offset = this.state.appLogsPage * this.state.appLogsLimit;
    const total = this.state.appLogsTotal;
    const countInfo = document.getElementById('logs-pagination-info');
    const prevBtn = document.getElementById('btn-logs-prev');
    const nextBtn = document.getElementById('btn-logs-next');

    if (countInfo) {
      const start = total > 0 ? offset + 1 : 0;
      const end = Math.min(offset + this.state.appLogs.length, total);
      countInfo.textContent = `Showing ${start}-${end} of ${total} logs`;
    }
    if (prevBtn) prevBtn.disabled = this.state.appLogsPage === 0;
    if (nextBtn) nextBtn.disabled = offset + this.state.appLogs.length >= total;
  },

  handleLiveAppLog(rec) {
    if (!this.state.liveLogsActive) return;

    // Filter checks
    if (this.state.appLogsLevel !== 'all' && rec.level !== this.state.appLogsLevel) {
      return;
    }
    if (this.state.appLogsSource !== 'all' && rec.source !== this.state.appLogsSource) {
      return;
    }
    if (this.state.appLogsSearch) {
      const term = this.state.appLogsSearch.toLowerCase();
      const inMsg = (rec.message || '').toLowerCase().includes(term);
      const inAttrs = (rec.attrs_json || '').toLowerCase().includes(term);
      if (!inMsg && !inAttrs) return;
    }

    // Prepend if on page 0
    if (this.state.appLogsPage === 0) {
      this.state.appLogs.unshift(rec);
      if (this.state.appLogs.length > this.state.appLogsLimit) {
        this.state.appLogs.pop();
      }
      this.state.appLogsTotal++;

      const tbody = document.getElementById('logs-table-tbody');
      if (tbody) {
        if (tbody.children.length === 1 && tbody.children[0].querySelector('td[colspan]')) {
          tbody.innerHTML = '';
        }
        const rowHtml = this.createLogRowHtml(rec, true);
        tbody.insertAdjacentHTML('afterbegin', rowHtml);
        if (tbody.children.length > this.state.appLogsLimit) {
          tbody.lastElementChild.remove();
        }
      }
      this.updateLogsPagination();
    }
  },

  async saveRetention(val) {
    try {
      const res = await fetch('/api/logs/retention', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ retention: val })
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Failed to update retention policy');
      this.showToast(`Log retention updated to ${this.formatRetentionName(val)}`, 'success');
      this.loadLogStats();
      this.loadLogs();
    } catch (e) {
      this.showToast(e.message, 'error');
    }
  },

  async pruneLogsNow() {
    try {
      const res = await fetch('/api/logs/prune', { method: 'POST' });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Failed to prune logs');
      this.showToast(`Pruned ${data.pruned} expired log records`, 'success');
      this.loadLogStats();
      this.loadLogs();
    } catch (e) {
      this.showToast(e.message, 'error');
    }
  },

  async clearLogsAll() {
    try {
      const res = await fetch('/api/logs/clear', { method: 'POST' });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Failed to clear logs');
      this.showToast('All application logs cleared and database vacuumed', 'success');
      this.state.appLogs = [];
      this.state.appLogsTotal = 0;
      this.state.appLogsPage = 0;
      this.loadLogStats();
      this.loadLogs();
    } catch (e) {
      this.showToast(e.message, 'error');
    }
  },

  // ==========================================================================
  // SOURCES, PROFILES & OPTIMIZER
  // ==========================================================================

  async api(method, path, body) {
    const res = await fetch(path, {
      method,
      headers: body ? { 'Content-Type': 'application/json' } : undefined,
      body: body ? JSON.stringify(body) : undefined,
    });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) throw new Error(data.error || `${method} ${path} failed (${res.status})`);
    return data;
  },

  fmtTime(t) {
    return t ? new Date(t).toLocaleString() : '—';
  },

  copyLogJSON(id) {
    const log = (this.state.appLogs || []).find(l => l.id === id);
    if (log) this.copyToClipboard(JSON.stringify(log.attrs, null, 2));
  },

  async loadSources() {
    try {
      this.renderSources(await this.api('GET', '/api/sources'));
    } catch (e) {
      console.error('Failed to load sources', e);
    }
  },

  renderSources(sources) {
    const tbody = document.getElementById('sources-tbody');
    if (!tbody) return;
    if (!sources.length) {
      tbody.innerHTML = `<tr><td colspan="6" class="text-muted text-center py-4">No subscriptions yet. Add one above.</td></tr>`;
      return;
    }
    tbody.innerHTML = sources.map(src => `
      <tr>
        <td class="text-mono text-xs" style="word-break: break-all;">${this.escapeHtml(src.url)}</td>
        <td>
          <label class="toggle-switch">
            <input type="checkbox" data-action="source-toggle" data-id="${Number(src.id)}" ${src.enabled ? 'checked' : ''}>
            <span class="slider"></span>
          </label>
        </td>
        <td class="text-xs text-muted">${this.fmtTime(src.last_fetched_at)}</td>
        <td class="text-mono">${Number(src.last_count)}</td>
        <td class="text-xs">${src.last_error
          ? `<span class="text-danger" title="${this.escapeHtml(src.last_error)}">⚠ ${this.escapeHtml(src.last_error.slice(0, 60))}</span>`
          : (src.last_fetched_at ? '<span class="text-success">OK</span>' : '<span class="text-muted">Pending</span>')}</td>
        <td><button class="btn btn-danger btn-sm" data-action="source-delete" data-id="${Number(src.id)}">Delete</button></td>
      </tr>
    `).join('');
  },

  async addSource() {
    const input = document.getElementById('source-url-input');
    const url = input?.value.trim();
    if (!url) return this.showToast('Enter a subscription URL', 'error');
    try {
      await this.api('POST', '/api/sources', { url });
      input.value = '';
      this.showToast('Subscription added. Fetching now...', 'success');
      this.loadSources();
    } catch (e) {
      this.showToast(e.message, 'error');
    }
  },

  async pasteSource() {
    const input = document.getElementById('source-paste-input');
    const btn = document.getElementById('btn-paste-source');
    const content = input?.value.trim();
    if (!content) return this.showToast('Paste some configs first', 'error');
    if (btn) btn.disabled = true;
    try {
      const res = await this.api('POST', '/api/sources/paste', { content });
      input.value = '';
      this.showToast(`Imported ${res.configs} configs (${res.new} new). Testing auto profiles...`, 'success');
      this.refreshStatus();
    } catch (e) {
      this.showToast(e.message, 'error');
    } finally {
      if (btn) btn.disabled = false;
    }
  },

  async toggleSource(id, enabled) {
    try {
      await this.api('POST', '/api/sources/toggle', { id, enabled });
    } catch (e) {
      this.showToast(e.message, 'error');
      this.loadSources();
    }
  },

  async deleteSource(id) {
    if (!confirm('Delete this subscription? Its configs are removed once they age out.')) return;
    try {
      await this.api('POST', '/api/sources/delete', { id });
      this.loadSources();
    } catch (e) {
      this.showToast(e.message, 'error');
    }
  },

  async loadProfiles() {
    try {
      this.state.profiles = await this.api('GET', '/api/profiles');
      this.renderProfiles();
    } catch (e) {
      console.error('Failed to load profiles', e);
    }
  },

  // Profiles are a stack of cards, one per built-in profile type. Each card keeps a
  // stable skeleton: its live part is redrawn on every poll, while an open settings
  // form or results list is left alone so typing and scrolling are not lost.
  renderProfiles() {
    const box = document.getElementById('profile-cards');
    if (!box) return;
    const profiles = this.state.profiles || [];
    box.querySelectorAll(':scope > :not(.profile-card)').forEach(el => el.remove());
    if (!profiles.length) {
      box.insertAdjacentHTML('beforeend', '<div class="glass-card text-muted">No profile types.</div>');
      return;
    }
    profiles.forEach(p => {
      let card = document.getElementById(`profile-card-${Number(p.id)}`);
      if (!card) {
        card = document.createElement('div');
        card.id = `profile-card-${Number(p.id)}`;
        card.className = 'profile-card';
        card.innerHTML = '<div class="pc-live"></div><div class="pc-settings" hidden></div><div class="pc-results" hidden></div>';
        box.appendChild(card);
      }
      card.querySelector('.pc-live').innerHTML = this.profileCardHTML(p);
    });
  },

  profileCardHTML(p) {
    const id = Number(p.id);
    const type = p.type || {};
    let state, statusClass;
    if (!p.enabled) { state = '<span class="badge">Disabled</span>'; statusClass = 'off'; }
    else if (p.error) { state = `<span class="badge badge-danger" title="${this.escapeHtml(p.error)}">Port error</span>`; statusClass = 'error'; }
    else if (!p.active_node) { state = '<span class="badge badge-warning">No candidate yet</span>'; statusClass = 'warn'; }
    else { state = '<span class="badge badge-success">Serving</span>'; statusClass = 'serving'; }

    const st = p.stats;
    const box = (label, value, hint) => `<div class="pc-metric" title="${this.escapeHtml(hint)}"><span class="pc-metric-value">${value}</span><span class="pc-metric-label">${this.escapeHtml(label)}</span></div>`;
    const funnel = st ? `<div class="pc-funnel">
        ${box('configs', Number(st.configs), 'Stored configs')}
        ${box('reachable', Number(st.reachable), 'Accepted a TCP connection')}
        ${box(`reach ${type.name || 'service'}`, Number(st.reached), 'Step 1: passed the quick filter (no account used)')}
        ${box('checked', Number(st.checked), 'Step 2: got the real check')}
        ${box('candidates', Number(st.passed), 'Passed the real check')}
        ${box('best', st.best_ms ? Math.round(st.best_ms) + ' ms' : '—', 'Response time of the best candidate')}
      </div>` : '<div class="text-xs text-muted mb-2">Not tested yet.</div>';
    const tally = (m, cls) => Object.entries(m || {}).sort((a, b) => b[1] - a[1])
      .map(([k, n]) => `<span class="badge ${cls}">${this.escapeHtml(k || 'no answer')} ${Number(n)}</span>`).join(' ');
    const labels = st && (Object.keys(st.passing || {}).length || Object.keys(st.failing || {}).length)
      ? `<div class="pc-labels">${tally(st.passing, 'badge-success')} ${tally(st.failing, 'badge-danger')}</div>` : '';
    const progress = (this.state.progress || {})[id];
    const outcome = progress
      ? `<div class="text-xs text-amber mb-2">⟳ ${this.escapeHtml(progress)}</div>`
      : (st ? `<div class="text-xs text-muted mb-2">Last test ${this.fmtTime(st.at)}: ${this.escapeHtml(st.outcome)}</div>` : '');
    const best = p.nodes && p.nodes.length ? p.nodes[0] : null;
    const active = p.active_node
      ? `${this.escapeHtml(p.active_node.name || `${p.active_node.address}:${p.active_node.port}`)}${best ? ` <span class="text-muted">· ${this.escapeHtml(best.label || '')} ${Math.round(best.latency_ms)} ms</span>` : ''}`
      : '—';

    const micro = [];
    if (st) {
      micro.push(`${Number(st.passed)} candidates`);
      if (st.best_ms) micro.push(`best ${Math.round(st.best_ms)} ms`);
    } else {
      micro.push('Not tested yet');
    }
    if (p.auto_test) micro.push(`auto ${this.escapeHtml(p.test_interval || '30m')}`);
    micro.push(`:${Number(p.port)}`);
    const open = !!document.getElementById(`profile-card-${id}`)?.classList.contains('expanded');

    return `
      <div class="pc-summary is-${statusClass}" data-action="profile-toggle" data-id="${id}" role="button" tabindex="0" aria-expanded="${open}">
        <div>
          <div class="pc-summary-title"><span class="card-title">${this.escapeHtml(p.name)}</span> ${state}</div>
          <div class="pc-summary-sub">${this.escapeHtml(micro.join(' · '))}</div>
        </div>
        <span class="pc-chevron" aria-hidden="true">›</span>
      </div>
      <div class="pc-detail">
      ${this.accountLine(p)}
      <div class="text-xs text-muted mb-3">${this.escapeHtml(type.description || '')}</div>
      <div class="pc-port mb-3">
        <span class="text-mono">:${Number(p.port)}</span>
        <button class="btn btn-secondary btn-sm" data-action="profile-copy" data-scheme="socks5" data-id="${id}" title="Copy SOCKS5 URL">SOCKS5</button>
        <button class="btn btn-secondary btn-sm" data-action="profile-copy" data-scheme="http" data-id="${id}" title="Copy HTTP proxy URL">HTTP</button>
      </div>
      ${funnel}${labels}${outcome}
      <div class="text-xs mb-3"><span class="text-muted">Active node:</span> ${active} <span class="text-muted">· ${(p.nodes || []).length} candidates</span></div>
      <div class="pc-actions">
        <button class="btn btn-primary btn-sm" data-action="profile-test" data-id="${id}" title="${this.state.optimizerBusy ? 'A benchmark is already running — wait for it to finish' : "Run this profile's full test now"}" ${this.state.optimizerBusy ? 'disabled' : ''}>Test now</button>
        <button class="btn btn-secondary btn-sm" data-action="profile-results" data-id="${id}">Checked configs</button>
        <button class="btn btn-secondary btn-sm" data-action="profile-settings" data-id="${id}">Settings</button>
      </div>
      </div>`;
  },

  // toggleProfileCard opens or closes a profile card. The open state is a class on the
  // card element, which survives the poll that redraws the card's contents.
  toggleProfileCard(id) {
    const card = document.getElementById(`profile-card-${id}`);
    if (!card) return;
    const open = card.classList.toggle('expanded');
    card.querySelector('.pc-summary')?.setAttribute('aria-expanded', String(open));
  },

  serverHost() {
    const addr = this.state.status && this.state.status.listen_addr;
    return addr && addr !== '0.0.0.0' && addr !== '::' ? addr : 'SERVER_IP';
  },

  copyProfileURL(id, scheme) {
    const p = (this.state.profiles || []).find(x => x.id === id);
    if (!p) return;
    const creds = `${encodeURIComponent(p.username)}:${encodeURIComponent(p.password)}`;
    this.copyToClipboard(`${scheme}://${creds}@${this.serverHost()}:${p.port}`);
  },

  cardPanel(id, cls) {
    const card = document.getElementById(`profile-card-${Number(id)}`);
    return card ? card.querySelector(cls) : null;
  },

  // toggleSettings opens the profile's settings form inside its card.
  toggleSettings(id) {
    const panel = this.cardPanel(id, '.pc-settings');
    const p = (this.state.profiles || []).find(x => x.id === id);
    if (!panel || !p) return;
    if (!panel.hidden) { panel.hidden = true; panel.innerHTML = ''; return; }
    const set = p.secrets_set || [];
    const login = (p.type || {}).login;
    const account = login ? `<div class="form-group mb-3"><label class="form-label">${this.escapeHtml(login.provider)} account</label>
      ${this.accountLine(p)}
      <div class="pc-actions" style="margin-top: 0.4rem;">
        <button class="btn btn-primary btn-sm" type="button" data-action="profile-login" data-id="${Number(p.id)}">Sign in with ${this.escapeHtml(login.provider)}</button>
        ${set.includes(login.token_field) ? `<button class="btn btn-secondary btn-sm" type="button" data-action="profile-logout" data-id="${Number(p.id)}">Sign out</button>` : ''}
      </div>
      <span class="text-muted text-xs mt-1">Opens ${this.escapeHtml(login.provider)}'s sign-in page and comes back here. Needs the client secret below.</span></div>` : '';
    // The type's own settings. Secrets are never sent to the browser: empty keeps the saved one.
    const fields = ((p.type || {}).fields || []).map(f => {
      const ph = f.secret ? (set.includes(f.key) ? 'Saved. Leave empty to keep it.' : 'Not set') : '';
      const input = f.secret
        ? `<textarea data-setting="${this.escapeHtml(f.key)}" class="form-input text-mono text-xs" rows="2" autocomplete="off" placeholder="${ph}"></textarea>`
        : `<input type="text" data-setting="${this.escapeHtml(f.key)}" class="form-input" value="${this.escapeHtml((p.settings || {})[f.key] || '')}">`;
      return `<div class="form-group mb-3"><label class="form-label">${this.escapeHtml(f.label)}</label>${input}
        <span class="text-muted text-xs mt-1">${this.escapeHtml(f.help)}</span></div>`;
    }).join('');
    // Per-profile automated testing. This is the only place a profile's testing is scheduled.
    const ivs = { '5m': '5 minutes', '15m': '15 minutes', '30m': '30 minutes', '1h': '1 hour', '3h': '3 hours', '6h': '6 hours', '12h': '12 hours', '24h': '24 hours' };
    const curIv = p.test_interval || '30m';
    const ivOptions = Object.entries(ivs).map(([v, l]) => `<option value="${v}" ${v === curIv ? 'selected' : ''}>${l}</option>`).join('');
    const autoBlock = `<div class="pc-autotest">
      <div class="modal-section-title">Automated testing</div>
      <div class="form-group mb-3">
        <label class="form-label"><input type="checkbox" data-field="auto_test" ${p.auto_test ? 'checked' : ''}> Re-test this profile automatically</label>
        <span class="text-muted text-xs">When on, Velox re-benchmarks this profile on its own interval below and may switch the live node. When off, it runs only when you press Test now. This is the only place this profile's testing is scheduled.</span>
      </div>
      <div class="form-group">
        <label class="form-label" for="pc-iv-${id}">Re-test every</label>
        <select id="pc-iv-${id}" data-field="test_interval" class="form-select" ${p.auto_test ? '' : 'disabled'}>${ivOptions}</select>
      </div>
    </div>`;
    panel.innerHTML = `<div class="pc-panel">
      <div class="two-col-grid">
        <div>
          <div class="form-group mb-3"><label class="form-label">Port on this server</label>
            <input type="number" data-field="port" class="form-input" min="1" max="65535" value="${Number(p.port)}"></div>
          <div class="form-group mb-3"><label class="form-label">Username</label>
            <input type="text" data-field="username" class="form-input" autocomplete="off" value="${this.escapeHtml(p.username)}"></div>
          <div class="form-group mb-3"><label class="form-label">Password</label>
            <div class="input-group"><input type="text" data-field="password" class="form-input" autocomplete="off" value="${this.escapeHtml(p.password)}">
            <button class="btn btn-secondary" type="button" data-action="profile-genpw" data-id="${Number(p.id)}">Generate</button></div></div>
          <div class="form-group mb-3">
            <label class="form-label"><input type="checkbox" data-field="enabled" ${p.enabled ? 'checked' : ''}> Enabled</label>
            <span class="text-muted text-xs">Every profile port is reachable from the internet; keep the password strong.</span>
          </div>
        </div>
        <div>${account}${fields}</div>
      </div>
      ${autoBlock}
      <div class="pc-actions">
        <button class="btn btn-primary" data-action="profile-save" data-id="${Number(p.id)}">Save</button>
        <button class="btn btn-secondary" data-action="profile-settings" data-id="${Number(p.id)}">Close</button>
      </div></div>`;
    panel.hidden = false;
  },

  async saveProfile(id) {
    const panel = this.cardPanel(id, '.pc-settings');
    const p = (this.state.profiles || []).find(x => x.id === id);
    if (!panel || !p) return;
    const field = (k) => panel.querySelector(`[data-field="${k}"]`);
    const settings = {};
    panel.querySelectorAll('[data-setting]').forEach(el => { settings[el.dataset.setting] = el.value; });
    const body = {
      id,
      port: Number(field('port').value),
      username: field('username').value,
      password: field('password').value,
      enabled: field('enabled').checked,
      auto_test: field('auto_test') ? field('auto_test').checked : p.auto_test,
      test_interval: field('test_interval') ? field('test_interval').value : p.test_interval,
      settings,
    };
    try {
      await this.api('POST', '/api/profiles', body);
      this.showToast('Profile saved.' + (body.enabled && body.auto_test ? ' Testing it now...' : ''), 'success');
      panel.hidden = true;
      panel.innerHTML = '';
      this.loadProfiles();
    } catch (e) {
      this.showToast(e.message, 'error');
    }
  },

  // toggleResults lists every config the profile's real check looked at, labeled.
  async toggleResults(id, refresh) {
    const panel = this.cardPanel(id, '.pc-results');
    if (!panel) return;
    if (!panel.hidden && !refresh) { panel.hidden = true; panel.innerHTML = ''; return; }
    try {
      this.state.profileResults = this.state.profileResults || {};
      this.state.profileResults[id] = await this.api('GET', `/api/profiles/results?id=${Number(id)}`);
      panel.dataset.all = panel.dataset.all || 'false';
      this.renderResults(id);
      panel.hidden = false;
    } catch (e) {
      this.showToast(e.message, 'error');
    }
  },

  renderResults(id) {
    const panel = this.cardPanel(id, '.pc-results');
    const all = (this.state.profileResults || {})[id] || [];
    const prof = (this.state.profiles || []).find(x => Number(x.id) === Number(id));
    const activeId = prof && prof.active_node ? prof.active_node.id : null;
    const serving = !!(prof && prof.active_node);
    const showAll = panel.dataset.all === 'true';
    const rows = showAll ? all : all.filter(r => r.ok);
    const activeCell = (r) => {
      if (!r.ok) return '';
      if (r.config_id === activeId) return '<span class="badge badge-success">● active</span>';
      if (!serving) return '';
      return `<button class="btn btn-secondary btn-sm" data-action="profile-set-active" data-id="${Number(id)}" data-config="${this.escapeHtml(r.config_id)}" title="Serve this node on the profile port">Use</button>`;
    };
    const body = rows.length ? rows.map((r, i) => `
      <tr${r.config_id === activeId ? ' class="pc-row-active"' : ''}>
        <td class="text-muted">${r.ok ? Number(r.rank) : i + 1}</td>
        <td>${r.ok ? '<span class="badge badge-success">candidate</span>' : `<span class="badge badge-danger" title="${this.escapeHtml(r.error || '')}">rejected</span>`}</td>
        <td class="font-medium">${this.escapeHtml(r.label || '—')}</td>
        <td>${r.ok ? '<span class="badge badge-success">eligible</span>' : (r.label ? '<span class="badge badge-danger">not eligible</span>' : '<span class="text-xs text-muted">no answer</span>')}</td>
        <td class="text-mono">${r.latency_ms ? Math.round(r.latency_ms) + ' ms' : '—'}</td>
        <td>${activeCell(r)}</td>
        <td class="text-xs">${this.escapeHtml(r.name)}</td>
        <td><span class="badge badge-${this.escapeHtml(r.protocol)}">${this.escapeHtml(r.protocol)}</span></td>
        <td class="text-mono text-xs">${this.escapeHtml(r.address)}:${Number(r.port)}</td>
        <td class="text-xs">${r.response ? `<details><summary class="pc-resp-sum">view</summary><pre class="pc-resp">${this.escapeHtml(this.fmtJson(r.response))}</pre></details>` : '—'}</td>
        <td class="text-xs text-muted">${this.escapeHtml(r.ok ? '' : (r.error || ''))}</td>
      </tr>`).join('')
      : `<tr><td colspan="11" class="text-muted text-center py-4">${all.length ? 'No candidates in the last test.' : 'No test results yet.'}</td></tr>`;
    const candidates = all.filter(r => r.ok).length;
    panel.innerHTML = `<div class="pc-panel">
      <div class="pc-actions mb-2">
        <button class="filter-pill ${showAll ? '' : 'active'}" data-action="profile-results-filter" data-all="false" data-id="${Number(id)}">Candidates (${candidates})</button>
        <button class="filter-pill ${showAll ? 'active' : ''}" data-action="profile-results-filter" data-all="true" data-id="${Number(id)}">All checked (${all.length})</button>
        <button class="btn btn-secondary btn-sm" data-action="profile-results-refresh" data-id="${Number(id)}">Refresh</button>
        <span class="text-xs text-muted">From the last test's real check (step 2). Candidates are ranked by response time; press Use to serve a different one until the next test.</span>
      </div>
      <div class="table-responsive"><table class="data-table">
        <thead><tr><th style="width: 40px;">#</th><th style="width: 100px;">Result</th><th style="width: 70px;">Country</th><th style="width: 90px;">Eligible</th><th style="width: 80px;">Delay</th><th style="width: 80px;">Active</th><th>Config</th><th style="width: 100px;">Protocol</th><th>Server</th><th style="width: 110px;">Response</th><th>Why rejected</th></tr></thead>
        <tbody>${body}</tbody>
      </table></div></div>`;
  },

  // setActiveNode serves a chosen candidate on the profile port until the next test.
  async setActiveNode(id, configId) {
    try {
      await this.api('POST', '/api/profiles/active', { id, config_id: configId });
      this.showToast('Active node switched', 'success');
      await this.loadProfiles();   // refresh active_node
      this.renderResults(id);      // move the "active" marker
    } catch (e) {
      this.showToast(e.message, 'error');
    }
  },

  // fmtJson pretty-prints a stored response body, or returns it unchanged if it is not JSON.
  fmtJson(raw) {
    try { return JSON.stringify(JSON.parse(raw), null, 2); } catch { return raw; }
  },

  // accountLine says who a profile with account sign-in is signed in as.
  accountLine(p) {
    const login = (p.type || {}).login;
    if (!login) return '';
    if (p.account) return `<div class="text-xs text-success">Signed in as ${this.escapeHtml(p.account)}</div>`;
    if ((p.secrets_set || []).includes(login.token_field)) return '<div class="text-xs text-muted">Pasted session</div>';
    return '<div class="text-xs text-amber">Not signed in</div>';
  },

  async loginProfile(id) {
    const panel = this.cardPanel(id, '.pc-settings');
    const el = panel && panel.querySelector('[data-setting="client_secret"]');
    try {
      const res = await this.api('POST', '/api/profiles/login', { id, client_secret: el ? el.value : '' });
      window.location.href = res.url; // the provider sends the browser back to this dashboard
    } catch (e) {
      this.showToast(e.message, 'error');
    }
  },

  async logoutProfile(id) {
    if (!confirm('Sign this profile out? Velox forgets its session; candidates already found keep serving.')) return;
    try {
      await this.api('POST', '/api/profiles/logout', { id });
      this.showToast('Signed out.', 'success');
      const panel = this.cardPanel(id, '.pc-settings');
      if (panel) { panel.hidden = true; panel.innerHTML = ''; }
      this.loadProfiles();
    } catch (e) {
      this.showToast(e.message, 'error');
    }
  },

  // showLoginResult reports a sign-in the provider just redirected back from.
  showLoginResult() {
    const q = new URLSearchParams(window.location.search);
    if (!q.has('login') && !q.has('login_error')) return;
    history.replaceState(null, '', '/');
    this.switchTab('profiles');
    if (q.has('login')) this.showToast(`Signed in${q.get('login') ? ' as ' + q.get('login') : ''}. Press Test now to find candidates.`, 'success');
    else this.showToast('Sign-in failed: ' + q.get('login_error'), 'error');
  },

  async testProfile(id) {
    const p = (this.state.profiles || []).find(x => x.id === id);
    try {
      await this.api('POST', '/api/profiles/test', { id });
      this.showToast(`Testing nodes for "${p ? p.name : id}"...`, 'info');
    } catch (e) {
      this.showToast(e.message, 'error');
    }
  },

  async runOptimizer(kind) {
    try {
      await this.api('POST', '/api/optimizer/run', { kind });
      this.showToast('Fetching all subscriptions...', 'info');
    } catch (e) {
      this.showToast(e.message, 'error');
    }
  },

  async saveSchedule() {
    const body = {
      refresh_interval: document.getElementById('opt-refresh-interval').value,
      config_max_age: document.getElementById('opt-max-age').value,
    };
    try {
      this.renderOptimizer(await this.api('POST', '/api/optimizer', body));
      this.showToast('Schedule saved', 'success');
      document.getElementById('optimizer-dialog')?.close();
    } catch (e) {
      this.showToast(e.message, 'error');
    }
  },

  renderOptimizer(st) {
    if (!st) return;
    const phaseText = { idle: 'Idle', refreshing: 'Fetching subscriptions', testing: 'Testing nodes' }[st.phase] || st.phase;
    const busy = st.phase !== 'idle';
    this.state.optimizerBusy = busy;
    // Reflect the run guard in the UI: while a benchmark runs, no new run can be started.
    const reason = 'A benchmark is already running — wait for it to finish';
    const refreshBtn = document.getElementById('btn-run-refresh');
    if (refreshBtn) { refreshBtn.disabled = busy; refreshBtn.title = busy ? reason : 'Fetch all subscriptions now'; }
    document.querySelectorAll('[data-action="profile-test"]').forEach(b => {
      b.disabled = busy; b.title = busy ? reason : "Run this profile's full test now";
    });
    const pill = document.getElementById('optimizer-status-pill');
    if (pill) pill.className = `status-pill ${st.phase === 'idle' ? 'idle' : 'active'}`;
    const set = (id, text) => { const el = document.getElementById(id); if (el) el.textContent = text; };
    set('optimizer-status-text', phaseText === 'Idle' ? 'Velox: Idle' : `Velox: ${phaseText}`);
    set('stat-optimizer-phase', phaseText);
    set('opt-phase', phaseText);
    set('opt-last-refresh', this.fmtTime(st.last_refresh_at));
    set('opt-next-refresh', this.fmtTime(st.next_refresh_at));
    set('opt-summary', st.last_summary || '—');
    // Fill the schedule form once, so polling does not overwrite the admin's edits.
    const form = document.getElementById('opt-refresh-interval');
    if (form && !form.dataset.loaded) {
      form.value = st.refresh_interval;
      document.getElementById('opt-max-age').value = st.config_max_age;
      form.dataset.loaded = '1';
    }
  },
};

document.addEventListener('DOMContentLoaded', () => {
  app.init();
});

