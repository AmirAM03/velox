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
    configsWorkingOnly: false,
    configsSortBy: 'score',
    configsSelectedIds: new Set(),
    rotationStatus: null,
    rotationCountdownTimer: null,
    isConnecting: false,
    eventSource: null,
    benchmarkRunning: false,
    benchmarkTotal: 0,
    benchmarkTested: 0,
    benchmarkPassed: 0,
    benchmarkFailed: 0,
    benchmarkFastest: 0,
    logs: [],
    activeLogLevel: 'all',
    autoScrollLogs: true,

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
    this.refreshStatus();
    this.loadTopNodes();
    this.loadLogStats();
    this.loadRotationStatus();
    this.startRotationTicker();
    this.loadMethodology();
    
    // Poll status periodically every 3 seconds
    setInterval(() => {
      this.refreshStatus();
      if (this.state.activeTab === 'rotation') {
        this.loadRotationStatus();
      }
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
      this.loadTopNodes();
      this.showToast('Data refreshed', 'info');
    });

    // Quick Connect toggle in header
    document.getElementById('quick-toggle-btn')?.addEventListener('click', () => {
      if (this.state.status && this.state.status.active_config) {
        this.disconnectProxy();
      } else {
        this.connectProxy('');
      }
    });

    // System Proxy toggle
    document.getElementById('toggle-sys-proxy')?.addEventListener('change', (e) => {
      this.toggleSystemProxy(e.target.checked);
    });

    // Protocol filter pills in Configs view
    document.querySelectorAll('#configs-proto-filters .filter-pill').forEach(pill => {
      if (pill.id === 'btn-toggle-working-filter') return;
      pill.addEventListener('click', () => {
        document.querySelectorAll('#configs-proto-filters .filter-pill').forEach(p => {
          if (p.id !== 'btn-toggle-working-filter') p.classList.remove('active');
        });
        pill.classList.add('active');
        this.state.configsProto = pill.dataset.proto;
        this.state.configsPage = 0;
        this.loadConfigs();
      });
    });

    // Working Only filter pill toggle
    const workingPill = document.getElementById('btn-toggle-working-filter');
    workingPill?.addEventListener('click', () => {
      this.state.configsWorkingOnly = !this.state.configsWorkingOnly;
      workingPill.classList.toggle('active', this.state.configsWorkingOnly);
      this.state.configsPage = 0;
      this.loadConfigs();
    });

    // Sort By dropdown
    document.getElementById('configs-sort-select')?.addEventListener('change', (e) => {
      this.state.configsSortBy = e.target.value;
      this.state.configsPage = 0;
      this.loadConfigs();
    });

    // Select all visible configs checkbox
    document.getElementById('configs-check-all')?.addEventListener('change', (e) => {
      const checked = e.target.checked;
      document.querySelectorAll('.config-row-check').forEach(cb => {
        cb.checked = checked;
        const id = cb.dataset.id;
        if (checked) {
          this.state.configsSelectedIds.add(id);
        } else {
          this.state.configsSelectedIds.delete(id);
        }
      });
      this.updateBulkToolbar();
    });

    // Bulk Add Selected to Pool
    document.getElementById('btn-bulk-add-pool')?.addEventListener('click', async () => {
      const ids = Array.from(this.state.configsSelectedIds);
      if (ids.length === 0) return;
      await this.addConfigsToPool(ids);
    });

    // Bulk Add Working to Pool
    const addWorkingAction = async () => {
      await this.addWorkingToPool();
    };
    document.getElementById('btn-bulk-add-working')?.addEventListener('click', addWorkingAction);
    document.getElementById('btn-pool-add-working')?.addEventListener('click', addWorkingAction);

    // Clear Pool
    document.getElementById('btn-pool-clear')?.addEventListener('click', async () => {
      if (!confirm('Clear all configurations from the auto-rotation pool?')) return;
      await this.clearRotationPool();
    });

    // Master Rotation Toggle
    document.getElementById('toggle-rotation-enabled')?.addEventListener('change', (e) => {
      this.saveRotationConfig({ enabled: e.target.checked });
    });

    // Save Rotation Parameters
    document.getElementById('btn-save-rotation-cfg')?.addEventListener('click', () => {
      const interval = document.getElementById('rot-interval-select')?.value || '15m';
      const target = document.getElementById('rot-target-input')?.value.trim() || 'https://www.google.com/generate_204';
      const threads = parseInt(document.getElementById('rot-threads-select')?.value, 10) || 50;
      const enabled = document.getElementById('toggle-rotation-enabled')?.checked || false;
      this.saveRotationConfig({ enabled, interval, target, threads });
    });

    // Trigger Rotation Now
    document.getElementById('btn-rotation-trigger')?.addEventListener('click', async () => {
      await this.triggerRotationNow();
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

    // Ingest: Fetch Subscription
    document.getElementById('btn-fetch-sub')?.addEventListener('click', () => {
      const url = document.getElementById('sub-url-input')?.value.trim();
      if (!url) {
        this.showToast('Please enter a subscription URL', 'error');
        return;
      }
      this.ingestConfigs({ urls: [url] });
    });

    // Ingest: Paste Configs
    document.getElementById('btn-parse-paste')?.addEventListener('click', () => {
      const raw = document.getElementById('paste-input')?.value.trim();
      if (!raw) {
        this.showToast('Please paste one or more proxy URIs', 'error');
        return;
      }
      this.ingestConfigs({ raw: raw });
    });

    // Benchmark Target Presets
    document.querySelectorAll('.preset-btn').forEach(btn => {
      btn.addEventListener('click', () => {
        document.querySelectorAll('.preset-btn').forEach(b => b.classList.remove('active'));
        btn.classList.add('active');
        const input = document.getElementById('benchmark-target-input');
        if (input) input.value = btn.dataset.url;
      });
    });

    // Benchmark Parallel Threads Chips
    document.querySelectorAll('.thread-chips-group .chip-btn').forEach(btn => {
      btn.addEventListener('click', () => {
        document.querySelectorAll('.thread-chips-group .chip-btn').forEach(b => b.classList.remove('active'));
        btn.classList.add('active');
        const threadsInput = document.getElementById('benchmark-threads-input');
        if (threadsInput) {
          threadsInput.value = btn.dataset.threads;
        }
        const label = document.getElementById('threads-indicator-label');
        if (label) label.textContent = `${btn.dataset.threads} Threads`;
      });
    });

    // Benchmark Threads Numeric Input
    document.getElementById('benchmark-threads-input')?.addEventListener('input', (e) => {
      const val = parseInt(e.target.value, 10) || 100;
      const label = document.getElementById('threads-indicator-label');
      if (label) label.textContent = `${val} Threads`;
      document.querySelectorAll('.thread-chips-group .chip-btn').forEach(btn => {
        btn.classList.toggle('active', btn.dataset.threads === String(val));
      });
    });

    // Start Benchmark
    document.getElementById('btn-start-benchmark')?.addEventListener('click', () => {
      this.startBenchmark();
    });

    // Cancel Benchmark
    document.getElementById('btn-cancel-benchmark')?.addEventListener('click', () => {
      this.cancelBenchmark();
    });

    // Live Console Filters & Controls
    document.getElementById('btn-clear-console')?.addEventListener('click', () => {
      this.clearLogs();
    });
    document.getElementById('console-autoscroll')?.addEventListener('change', (e) => {
      this.state.autoScrollLogs = e.target.checked;
    });
    document.querySelectorAll('.console-filter-btn').forEach(btn => {
      btn.addEventListener('click', () => {
        document.querySelectorAll('.console-filter-btn').forEach(b => b.classList.remove('active'));
        btn.classList.add('active');
        this.state.activeLogLevel = btn.dataset.level;
        this.renderLogs();
      });
    });

    // Deduplication tool
    document.getElementById('btn-run-dedup')?.addEventListener('click', () => {
      this.runDeduplication();
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
    } else if (tabId === 'rotation') {
      this.loadRotationStatus();
      this.loadRotationPool();
    } else if (tabId === 'logs') {
      this.loadLogs();
      this.loadLogStats();
    } else if (tabId === 'benchmark') {
      this.loadMethodology();
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

    // Active Proxy
    const pill = document.getElementById('proxy-status-pill');
    const statusText = document.getElementById('proxy-status-text');
    const activeNode = document.getElementById('stat-active-node');
    const toggleBtn = document.getElementById('quick-toggle-btn');
    const activePort = document.getElementById('stat-active-port');

    if (data.active_config) {
      if (pill) { pill.className = 'status-pill active'; }
      if (statusText) statusText.textContent = `Connected (${data.active_config.protocol})`;
      if (activeNode) activeNode.textContent = data.active_config.name || `${data.active_config.address}:${data.active_config.port}`;
      if (toggleBtn) {
        toggleBtn.textContent = 'Disconnect';
        toggleBtn.className = 'btn btn-danger btn-sm';
      }
      if (activePort) activePort.textContent = `Endpoint: 127.0.0.1:${data.mixed_port || 1080}`;
    } else {
      if (pill) { pill.className = 'status-pill idle'; }
      if (statusText) statusText.textContent = 'Proxy: Disconnected';
      if (activeNode) activeNode.textContent = 'None';
      if (toggleBtn) {
        toggleBtn.textContent = 'Connect Best';
        toggleBtn.className = 'btn btn-primary btn-sm';
      }
      if (activePort) activePort.textContent = 'Endpoint: 127.0.0.1:1080 (Idle)';
    }

    // System Proxy Toggle
    const sysToggle = document.getElementById('toggle-sys-proxy');
    const sysStatus = document.getElementById('stat-sys-proxy');
    if (sysToggle) sysToggle.checked = !!data.system_proxy_enabled;
    if (sysStatus) sysStatus.textContent = data.system_proxy_enabled ? 'Enabled (127.0.0.1:1080)' : 'Disabled';

    // Benchmark stats
    const testedElem = document.getElementById('stat-tested-count');
    const avgLatencyElem = document.getElementById('stat-avg-latency');
    if (testedElem) testedElem.textContent = `${data.tested_configs || 0} Tested`;
    if (avgLatencyElem && data.avg_latency_ms) {
      avgLatencyElem.textContent = `Avg: ${Math.round(data.avg_latency_ms)} ms`;
    }

    // DB Path
    const dbPathElem = document.getElementById('db-path-display');
    if (dbPathElem && data.db_path) {
      dbPathElem.textContent = data.db_path;
    }

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

  async loadTopNodes() {
    try {
      const res = await fetch('/api/configs?limit=5');
      if (!res.ok) return;
      const data = await res.json();
      const configs = data.configs || [];
      const tbody = document.getElementById('top-nodes-tbody');
      if (!tbody) return;

      if (!configs || configs.length === 0) {
        tbody.innerHTML = `<tr><td colspan="7" class="text-muted text-center py-4">No configurations in database. Go to Ingest & Parse tab.</td></tr>`;
        return;
      }

      tbody.innerHTML = configs.map((cfg, idx) => {
        const scoreStr = (cfg.score !== undefined && cfg.score !== null) ? cfg.score.toFixed(3) : '—';
        const latStr = (cfg.latency_ms !== undefined && cfg.latency_ms !== null) ? `${Math.round(cfg.latency_ms)} ms` : '—';
        const badgeClass = `badge-${cfg.protocol}`;

        return `
          <tr>
            <td class="text-muted font-bold">${idx + 1}</td>
            <td><span class="badge ${badgeClass}">${cfg.protocol}</span></td>
            <td class="font-medium">${this.escapeHtml(cfg.name || `${cfg.address}:${cfg.port}`)}</td>
            <td class="text-mono text-sm">${cfg.address}:${cfg.port}</td>
            <td><span class="text-mono">${scoreStr}</span></td>
            <td><span class="text-mono ${cfg.latency_ms ? 'text-success' : ''}">${latStr}</span></td>
            <td>
              <button class="btn btn-secondary btn-sm" onclick="app.connectProxy('${cfg.id}')">Connect</button>
            </td>
          </tr>
        `;
      }).join('');
    } catch (e) {
      console.error('Failed to load top nodes', e);
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
      if (this.state.configsWorkingOnly) url.searchParams.set('working_only', 'true');
      if (this.state.configsSortBy) url.searchParams.set('sort_by', this.state.configsSortBy);

      const res = await fetch(url);
      if (!res.ok) return;
      const data = await res.json();
      const configs = data.configs || [];
      const total = data.total || 0;

      const tbody = document.getElementById('all-configs-tbody');
      if (!tbody) return;

      if (configs.length === 0) {
        tbody.innerHTML = `<tr><td colspan="9" class="text-muted text-center py-4">No configs match your filter.</td></tr>`;
      } else {
        tbody.innerHTML = configs.map((cfg, idx) => {
          const rank = offset + idx + 1;
          const scoreStr = (cfg.score !== undefined && cfg.score !== null) ? cfg.score.toFixed(3) : '—';
          const latStr = (cfg.latency_ms !== undefined && cfg.latency_ms !== null) ? `${Math.round(cfg.latency_ms)} ms` : '—';
          const latColor = cfg.latency_ms ? (cfg.latency_ms < 300 ? 'text-success' : 'text-amber') : 'text-muted';
          const badgeClass = `badge-${cfg.protocol}`;
          const transportInfo = `${cfg.network || 'tcp'} / ${cfg.security || 'none'}`;
          const isSelected = this.state.configsSelectedIds.has(cfg.id);
          const poolBtn = cfg.in_pool
            ? `<button class="btn-pool in-pool" onclick="app.toggleConfigPool('${cfg.id}', true)" title="Remove from Auto-Rotation Pool">✓ In Pool</button>`
            : `<button class="btn-pool" onclick="app.toggleConfigPool('${cfg.id}', false)" title="Add to Auto-Rotation Pool">+ Pool</button>`;

          return `
            <tr>
              <td>
                <input type="checkbox" class="config-row-check" data-id="${cfg.id}" ${isSelected ? 'checked' : ''} onchange="app.onConfigRowCheck(this, '${cfg.id}')">
              </td>
              <td class="text-muted font-bold">${rank}</td>
              <td><span class="badge ${badgeClass}">${cfg.protocol}</span></td>
              <td class="font-medium">${this.escapeHtml(cfg.name || 'Unnamed Node')}</td>
              <td class="text-mono text-sm">${cfg.address}:${cfg.port}</td>
              <td class="text-xs text-muted">${transportInfo}</td>
              <td><span class="text-mono ${latColor}">${latStr}</span></td>
              <td><span class="text-mono">${scoreStr}</span></td>
              <td>
                <div style="display: flex; gap: 0.35rem; align-items: center;">
                  <button class="btn btn-secondary btn-sm" onclick="app.connectProxy('${cfg.id}')" title="Connect">Connect</button>
                  ${poolBtn}
                  <button class="btn btn-secondary btn-sm" onclick="app.copyURI('${this.escapeHtml(cfg.raw_uri)}')" title="Copy URI">📋</button>
                </div>
              </td>
            </tr>
          `;
        }).join('');
      }

      // Check-all status
      const checkAll = document.getElementById('configs-check-all');
      if (checkAll) {
        checkAll.checked = configs.length > 0 && configs.every(c => this.state.configsSelectedIds.has(c.id));
      }
      this.updateBulkToolbar();

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

  async ingestConfigs(payload) {
    const box = document.getElementById('ingest-results-box');
    if (box) {
      box.innerHTML = `<div class="text-center py-4"><div class="empty-icon">⏳</div><p>Ingesting, parsing and deduplicating configurations...</p></div>`;
    }

    try {
      const res = await fetch('/api/parse', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Ingest failed');

      if (box) {
        box.innerHTML = `
          <div class="result-card">
            <div class="result-header">
              <span class="font-bold text-success">✅ Ingestion Complete</span>
              <span class="badge badge-vless">${data.parsed} Valid Configs</span>
            </div>
            <div class="result-grid">
              <div class="result-metric">
                <div class="result-metric-label">New Inserted</div>
                <div class="result-metric-val" style="color: var(--accent-emerald);">${data.inserted}</div>
              </div>
              <div class="result-metric">
                <div class="result-metric-label">Existing Updated</div>
                <div class="result-metric-val" style="color: var(--accent-cyan);">${data.updated}</div>
              </div>
              <div class="result-metric">
                <div class="result-metric-label">Batch Dups Skipped</div>
                <div class="result-metric-val" style="color: var(--accent-amber);">${data.batch_duplicates || 0}</div>
              </div>
              <div class="result-metric">
                <div class="result-metric-label">Parse Failures</div>
                <div class="result-metric-val" style="color: var(--accent-rose);">${data.failures || 0}</div>
              </div>
            </div>
          </div>
          <div class="text-muted text-xs">
            Deduplication Verified: All stored configs represent strictly unique endpoints.
          </div>
        `;
      }

      this.showToast(`Imported ${data.inserted} new configs (${data.updated} existing updated)`, 'success');
      this.refreshStatus();
      this.loadTopNodes();
    } catch (e) {
      if (box) {
        box.innerHTML = `<div class="text-center text-danger py-4"><p>❌ Error: ${e.message}</p></div>`;
      }
      this.showToast(e.message, 'error');
    }
  },

  setupEventSource() {
    if (this.state.eventSource) {
      this.state.eventSource.close();
    }

    const es = new EventSource('/api/test/stream');
    this.state.eventSource = es;

    const statusIndicator = document.getElementById('console-stream-status');

    es.onopen = () => {
      if (statusIndicator) {
        statusIndicator.textContent = '● Live SSE Connected';
        statusIndicator.className = 'text-xs text-emerald';
      }
    };

    es.onerror = () => {
      if (statusIndicator) {
        statusIndicator.textContent = '○ Reconnecting SSE...';
        statusIndicator.className = 'text-xs text-muted';
      }
    };

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
      case 'snapshot':
        this.handleSnapshot(msg);
        break;
      case 'start':
        this.handleBenchmarkStart(msg);
        break;
      case 'node_result':
        this.handleNodeResult(msg);
        break;
      case 'log':
        if (msg.entry) {
          this.appendLog(msg.entry);
        }
        break;
      case 'app_log':
        if (msg.log) {
          this.handleLiveAppLog(msg.log);
        }
        break;
      case 'cancelled':
        this.handleBenchmarkCancelled(msg);
        break;
      case 'complete':
        this.handleBenchmarkComplete(msg);
        break;
      case 'rotation_status':
        if (msg.status) {
          this.renderRotationStatus(msg.status);
        }
        break;
    }
  },

  handleSnapshot(msg) {
    if (msg.logs && msg.logs.length > 0) {
      this.state.logs = msg.logs;
      this.renderLogs();
    }

    this.state.benchmarkTotal = msg.total || 0;
    this.state.benchmarkTested = msg.tested || 0;
    this.state.benchmarkPassed = msg.passed || 0;
    this.state.benchmarkFailed = msg.failed || 0;
    this.state.benchmarkFastest = msg.fastest_ms || 0;

    const totalEl = document.getElementById('stat-live-total');
    const testedEl = document.getElementById('stat-live-tested');
    const passedEl = document.getElementById('stat-live-passed');
    const failedEl = document.getElementById('stat-live-failed');
    const fastestEl = document.getElementById('stat-live-fastest');
    const stageLabel = document.getElementById('benchmark-stage-label');
    const statusBadge = document.getElementById('benchmark-status-badge');
    const headerStatus = document.getElementById('benchmark-header-text');
    const headerPill = document.getElementById('benchmark-header-status');
    const startBtn = document.getElementById('btn-start-benchmark');
    const cancelBtn = document.getElementById('btn-cancel-benchmark');

    if (totalEl) totalEl.textContent = this.state.benchmarkTotal;
    if (testedEl) testedEl.textContent = this.state.benchmarkTested;
    if (passedEl) passedEl.textContent = this.state.benchmarkPassed;
    if (failedEl) failedEl.textContent = this.state.benchmarkFailed;
    if (fastestEl) fastestEl.textContent = this.state.benchmarkFastest > 0 ? `${Math.round(this.state.benchmarkFastest)} ms` : '— ms';

    if (msg.total > 0) {
      const pct = Math.min(100, Math.round((this.state.benchmarkTested / msg.total) * 100));
      const bar = document.getElementById('benchmark-progress-bar');
      const pctEl = document.getElementById('benchmark-progress-pct');
      const countsEl = document.getElementById('benchmark-progress-counts');
      if (bar) bar.style.width = `${pct}%`;
      if (pctEl) pctEl.textContent = `${pct}%`;
      if (countsEl) countsEl.textContent = `${this.state.benchmarkTested} / ${msg.total} nodes`;
    }

    if (msg.status === 'running') {
      this.state.benchmarkRunning = true;
      if (startBtn) { startBtn.classList.add('hidden'); startBtn.disabled = true; }
      if (cancelBtn) cancelBtn.classList.remove('hidden');
      if (statusBadge) { statusBadge.textContent = 'Running'; statusBadge.className = 'badge badge-primary'; }
      if (headerStatus) headerStatus.textContent = 'Testing Live';
      if (headerPill) headerPill.className = 'status-pill active';
      if (stageLabel) stageLabel.textContent = msg.current_stage || 'Testing...';
    } else {
      this.state.benchmarkRunning = false;
      if (startBtn) { startBtn.classList.remove('hidden'); startBtn.disabled = false; }
      if (cancelBtn) cancelBtn.classList.add('hidden');
      if (statusBadge) {
        statusBadge.textContent = msg.status === 'completed' ? 'Completed' : (msg.status === 'cancelled' ? 'Cancelled' : 'Idle');
        statusBadge.className = msg.status === 'completed' ? 'badge badge-success' : 'badge';
      }
      if (headerStatus) headerStatus.textContent = msg.status === 'completed' ? 'Completed' : 'Ready';
      if (headerPill) headerPill.className = 'status-pill idle';
      if (stageLabel) stageLabel.textContent = msg.current_stage || 'Ready to test';
    }

    if (msg.results && msg.results.length > 0) {
      const tbody = document.getElementById('benchmark-live-tbody');
      if (tbody) {
        tbody.innerHTML = msg.results.slice(0, 100).map(d => this.createNodeRowHtml(d)).join('');
      }
    }
  },

  handleBenchmarkStart(msg) {
    this.state.benchmarkRunning = true;
    this.state.benchmarkTotal = msg.total;
    this.state.benchmarkTested = 0;
    this.state.benchmarkPassed = 0;
    this.state.benchmarkFailed = 0;
    this.state.benchmarkFastest = 0;

    const startBtn = document.getElementById('btn-start-benchmark');
    const cancelBtn = document.getElementById('btn-cancel-benchmark');
    if (startBtn) { startBtn.classList.add('hidden'); startBtn.disabled = true; }
    if (cancelBtn) cancelBtn.classList.remove('hidden');

    const totalEl = document.getElementById('stat-live-total');
    const testedEl = document.getElementById('stat-live-tested');
    const passedEl = document.getElementById('stat-live-passed');
    const failedEl = document.getElementById('stat-live-failed');
    const fastestEl = document.getElementById('stat-live-fastest');
    const stageLabel = document.getElementById('benchmark-stage-label');
    const statusBadge = document.getElementById('benchmark-status-badge');
    const headerStatus = document.getElementById('benchmark-header-text');
    const headerPill = document.getElementById('benchmark-header-status');
    const bar = document.getElementById('benchmark-progress-bar');
    const pctEl = document.getElementById('benchmark-progress-pct');
    const countsEl = document.getElementById('benchmark-progress-counts');

    if (totalEl) totalEl.textContent = msg.total;
    if (testedEl) testedEl.textContent = 0;
    if (passedEl) passedEl.textContent = 0;
    if (failedEl) failedEl.textContent = 0;
    if (fastestEl) fastestEl.textContent = '— ms';
    if (bar) bar.style.width = '0%';
    if (pctEl) pctEl.textContent = '0%';
    if (countsEl) countsEl.textContent = `0 / ${msg.total} nodes`;

    if (stageLabel) stageLabel.textContent = msg.current_stage || 'Stage 0 Reachability';
    if (statusBadge) { statusBadge.textContent = 'Running'; statusBadge.className = 'badge badge-primary'; }
    if (headerStatus) headerStatus.textContent = 'Testing Live';
    if (headerPill) headerPill.className = 'status-pill active';

    const tbody = document.getElementById('benchmark-live-tbody');
    if (tbody) {
      tbody.innerHTML = `<tr><td colspan="7" class="text-center py-4 text-cyan">⚡ Benchmark active across ${msg.threads} parallel threads. Streaming results...</td></tr>`;
    }
  },

  handleNodeResult(msg) {
    this.state.benchmarkTested = msg.tested;
    this.state.benchmarkPassed = msg.passed;
    this.state.benchmarkFailed = msg.failed;
    this.state.benchmarkFastest = msg.fastest_ms;

    const testedEl = document.getElementById('stat-live-tested');
    const passedEl = document.getElementById('stat-live-passed');
    const failedEl = document.getElementById('stat-live-failed');
    const fastestEl = document.getElementById('stat-live-fastest');
    const stageLabel = document.getElementById('benchmark-stage-label');
    const bar = document.getElementById('benchmark-progress-bar');
    const pctEl = document.getElementById('benchmark-progress-pct');
    const countsEl = document.getElementById('benchmark-progress-counts');

    if (testedEl) testedEl.textContent = msg.tested;
    if (passedEl) passedEl.textContent = msg.passed;
    if (failedEl) failedEl.textContent = msg.failed;
    if (fastestEl && msg.fastest_ms > 0) fastestEl.textContent = `${Math.round(msg.fastest_ms)} ms`;
    if (stageLabel && msg.current_stage) stageLabel.textContent = msg.current_stage;

    if (this.state.benchmarkTotal > 0) {
      const pct = Math.min(100, Math.round((msg.tested / this.state.benchmarkTotal) * 100));
      if (bar) bar.style.width = `${pct}%`;
      if (pctEl) pctEl.textContent = `${pct}%`;
      if (countsEl) countsEl.textContent = `${msg.tested} / ${this.state.benchmarkTotal} nodes`;
    }

    if (msg.detail) {
      const tbody = document.getElementById('benchmark-live-tbody');
      if (tbody) {
        if (tbody.children.length === 1 && tbody.children[0].querySelector('td[colspan]')) {
          tbody.innerHTML = '';
        }
        const rowHtml = this.createNodeRowHtml(msg.detail);
        tbody.insertAdjacentHTML('afterbegin', rowHtml);

        if (tbody.children.length > 150) {
          tbody.lastElementChild.remove();
        }
      }
    }
  },

  handleBenchmarkComplete(msg) {
    this.state.benchmarkRunning = false;
    const startBtn = document.getElementById('btn-start-benchmark');
    const cancelBtn = document.getElementById('btn-cancel-benchmark');
    if (startBtn) { startBtn.classList.remove('hidden'); startBtn.disabled = false; }
    if (cancelBtn) cancelBtn.classList.add('hidden');

    const statusBadge = document.getElementById('benchmark-status-badge');
    const headerStatus = document.getElementById('benchmark-header-text');
    const headerPill = document.getElementById('benchmark-header-status');
    const stageLabel = document.getElementById('benchmark-stage-label');
    const bar = document.getElementById('benchmark-progress-bar');
    const pctEl = document.getElementById('benchmark-progress-pct');

    if (statusBadge) { statusBadge.textContent = 'Completed'; statusBadge.className = 'badge badge-success'; }
    if (headerStatus) headerStatus.textContent = 'Finished';
    if (headerPill) headerPill.className = 'status-pill idle';
    if (stageLabel) stageLabel.textContent = `Completed in ${msg.duration_s ? msg.duration_s.toFixed(1) : ''}s`;
    if (bar) bar.style.width = '100%';
    if (pctEl) pctEl.textContent = '100%';

    this.showToast(`Benchmark complete! ${msg.passed} working, ${msg.failed} failed.`, 'success');
    this.refreshStatus();
    this.loadTopNodes();
  },

  handleBenchmarkCancelled(msg) {
    this.state.benchmarkRunning = false;
    const startBtn = document.getElementById('btn-start-benchmark');
    const cancelBtn = document.getElementById('btn-cancel-benchmark');
    if (startBtn) { startBtn.classList.remove('hidden'); startBtn.disabled = false; }
    if (cancelBtn) cancelBtn.classList.add('hidden');

    const statusBadge = document.getElementById('benchmark-status-badge');
    const headerStatus = document.getElementById('benchmark-header-text');
    const headerPill = document.getElementById('benchmark-header-status');
    const stageLabel = document.getElementById('benchmark-stage-label');

    if (statusBadge) { statusBadge.textContent = 'Cancelled'; statusBadge.className = 'badge badge-danger'; }
    if (headerStatus) headerStatus.textContent = 'Cancelled';
    if (headerPill) headerPill.className = 'status-pill idle';
    if (stageLabel) stageLabel.textContent = 'Benchmark cancelled by user';

    this.showToast('Benchmark cancelled', 'info');
  },

  createNodeRowHtml(d) {
    let latClass = 'latency-slow';
    let latText = `${Math.round(d.latency_ms)} ms`;
    if (!d.success) {
      latClass = 'latency-failed';
      latText = 'Timeout';
    } else if (d.latency_ms < 500) {
      latClass = 'latency-fast';
    } else if (d.latency_ms < 1500) {
      latClass = 'latency-medium';
    }

    const protoClass = `badge-${(d.protocol || '').toLowerCase()}`;
    const statusBadge = d.success 
      ? `<span class="badge badge-success">Passed</span>` 
      : `<span class="badge badge-danger" title="${this.escapeHtml(d.error || '')}">Fail (${this.escapeHtml(d.stage || '')})</span>`;

    const actionBtn = d.success
      ? `<button class="btn btn-primary btn-xs" onclick="app.connectProxy('${d.config_id}')">Connect</button>`
      : `<span class="text-muted text-xs">—</span>`;

    return `
      <tr>
        <td>${statusBadge}</td>
        <td><span class="badge ${protoClass}">${this.escapeHtml((d.protocol || '').toUpperCase())}</span></td>
        <td>
          <div class="node-name-cell" title="${this.escapeHtml(d.name || d.address)}">${this.escapeHtml(d.name || d.address)}</div>
        </td>
        <td><span class="text-mono text-xs text-muted">${this.escapeHtml(d.address)}:${d.port}</span></td>
        <td><span class="latency-badge ${latClass}">${latText}</span></td>
        <td><span class="text-xs text-muted" title="${this.escapeHtml(d.error || '')}">${this.escapeHtml(d.error ? (d.error.length > 25 ? d.error.substring(0, 25) + '...' : d.error) : d.stage)}</span></td>
        <td>${actionBtn}</td>
      </tr>
    `;
  },

  async startBenchmark() {
    if (this.state.benchmarkRunning) return;

    const target = document.getElementById('benchmark-target-input')?.value.trim() || 'https://www.google.com/generate_204';
    const threads = parseInt(document.getElementById('benchmark-threads-input')?.value || '100', 10);
    const scope = document.getElementById('benchmark-scope-select')?.value || 'all';
    const proto = document.getElementById('benchmark-proto-select')?.value || '';

    const startBtn = document.getElementById('btn-start-benchmark');
    const cancelBtn = document.getElementById('btn-cancel-benchmark');

    if (startBtn) startBtn.disabled = true;

    try {
      const res = await fetch('/api/test', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ target: target, threads: threads, protocol: proto, scope: scope })
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Failed to start benchmark');

      this.showToast(`Benchmark launched on ${data.total} configs (${data.threads} threads)`, 'info');
      if (startBtn) startBtn.classList.add('hidden');
      if (cancelBtn) cancelBtn.classList.remove('hidden');
    } catch (e) {
      this.showToast(e.message, 'error');
      if (startBtn) startBtn.disabled = false;
    }
  },

  async cancelBenchmark() {
    try {
      const res = await fetch('/api/test/cancel', { method: 'POST' });
      const data = await res.json();
      this.showToast('Benchmark cancelled', 'info');
    } catch (e) {
      this.showToast(e.message, 'error');
    }
  },

  appendLog(entry) {
    this.state.logs.push(entry);
    if (this.state.logs.length > 500) {
      this.state.logs.shift();
    }

    const container = document.getElementById('operation-logs-container');
    if (!container) return;

    if (this.state.activeLogLevel !== 'all' && entry.level !== this.state.activeLogLevel) {
      return;
    }

    const line = document.createElement('div');
    line.className = 'terminal-line';
    line.innerHTML = `
      <span class="terminal-ts">[${this.escapeHtml(entry.timestamp)}]</span>
      <span class="terminal-source ${this.escapeHtml(entry.source)}">[${this.escapeHtml(entry.source)}]</span>
      <span class="terminal-msg ${this.escapeHtml(entry.level)}">${this.escapeHtml(entry.message)}</span>
    `;

    container.appendChild(line);

    if (this.state.autoScrollLogs) {
      container.scrollTop = container.scrollHeight;
    }
  },

  renderLogs() {
    const container = document.getElementById('operation-logs-container');
    if (!container) return;

    const filtered = this.state.activeLogLevel === 'all'
      ? this.state.logs
      : this.state.logs.filter(l => l.level === this.state.activeLogLevel);

    if (filtered.length === 0) {
      container.innerHTML = '<div class="terminal-line text-muted">[System] No log entries for current filter level.</div>';
      return;
    }

    container.innerHTML = filtered.map(entry => `
      <div class="terminal-line">
        <span class="terminal-ts">[${this.escapeHtml(entry.timestamp)}]</span>
        <span class="terminal-source ${this.escapeHtml(entry.source)}">[${this.escapeHtml(entry.source)}]</span>
        <span class="terminal-msg ${this.escapeHtml(entry.level)}">${this.escapeHtml(entry.message)}</span>
      </div>
    `).join('');

    if (this.state.autoScrollLogs) {
      container.scrollTop = container.scrollHeight;
    }
  },

  clearLogs() {
    this.state.logs = [];
    const container = document.getElementById('operation-logs-container');
    if (container) {
      container.innerHTML = '<div class="terminal-line text-muted">[System] Console output cleared by user.</div>';
    }
  },

  async connectProxy(configId) {
    try {
      this.showToast('Connecting to proxy node...', 'info');
      const res = await fetch('/api/connect', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ config_id: configId })
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Connection failed');

      this.showToast(`Connected to ${data.name || 'node'} on port 1080`, 'success');
      this.refreshStatus();
    } catch (e) {
      this.showToast(e.message, 'error');
    }
  },

  async disconnectProxy() {
    try {
      const res = await fetch('/api/disconnect', { method: 'POST' });
      if (!res.ok) throw new Error('Failed to disconnect');

      this.showToast('Proxy disconnected', 'info');
      this.refreshStatus();
    } catch (e) {
      this.showToast(e.message, 'error');
    }
  },

  async toggleSystemProxy(enabled) {
    try {
      const res = await fetch('/api/system-proxy', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ enabled: enabled })
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Failed to toggle system proxy');

      this.showToast(enabled ? 'OS System Proxy Enabled' : 'OS System Proxy Disabled', 'success');
      this.refreshStatus();
    } catch (e) {
      this.showToast(e.message, 'error');
      // Revert checkbox state
      const checkbox = document.getElementById('toggle-sys-proxy');
      if (checkbox) checkbox.checked = !enabled;
    }
  },

  async runDeduplication() {
    const btn = document.getElementById('btn-run-dedup');
    if (btn) btn.disabled = true;

    try {
      this.showToast('Scanning database for duplicates...', 'info');
      const res = await fetch('/api/dedup', { method: 'POST' });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Deduplication failed');

      if (data.removed > 0) {
        this.showToast(`Cleaned up ${data.removed} duplicate records out of ${data.scanned} scanned!`, 'success');
      } else {
        this.showToast(`Database is 100% clean! Scanned ${data.scanned} unique configs with 0 duplicates.`, 'success');
      }
      this.refreshStatus();
    } catch (e) {
      this.showToast(e.message, 'error');
    } finally {
      if (btn) btn.disabled = false;
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
    return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
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
              <button class="btn btn-secondary btn-xs" onclick="event.stopPropagation(); app.copyToClipboard('${this.escapeHtml(prettyJSON).replace(/'/g, "\\'")}');">Copy JSON</button>
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
  // CONFIGS SELECTION & ROTATION POOL METHODS
  // ==========================================================================

  onConfigRowCheck(input, id) {
    if (input.checked) {
      this.state.configsSelectedIds.add(id);
    } else {
      this.state.configsSelectedIds.delete(id);
    }
    this.updateBulkToolbar();
  },

  updateBulkToolbar() {
    const count = this.state.configsSelectedIds.size;
    const badge = document.getElementById('bulk-selected-count');
    const btn = document.getElementById('btn-bulk-add-pool');
    if (badge) badge.textContent = `${count} items selected`;
    if (btn) btn.disabled = count === 0;
  },

  async toggleConfigPool(configId, currentlyInPool) {
    try {
      const endpoint = currentlyInPool ? '/api/rotation/pool/remove' : '/api/rotation/pool/add';
      const res = await fetch(endpoint, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ config_id: configId })
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Pool update failed');
      this.showToast(currentlyInPool ? 'Removed from rotation pool' : 'Added to rotation pool', 'success');
      this.loadConfigs();
      this.loadRotationStatus();
      this.loadRotationPool();
    } catch (e) {
      this.showToast(e.message, 'error');
    }
  },

  async addConfigsToPool(ids) {
    try {
      const res = await fetch('/api/rotation/pool/add', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ config_ids: ids })
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Failed to add to pool');
      this.showToast(`Added ${data.added || ids.length} nodes to rotation pool`, 'success');
      this.state.configsSelectedIds.clear();
      this.updateBulkToolbar();
      this.loadConfigs();
      this.loadRotationStatus();
      this.loadRotationPool();
    } catch (e) {
      this.showToast(e.message, 'error');
    }
  },

  async addWorkingToPool() {
    try {
      const proto = this.state.configsProto;
      const url = proto ? `/api/rotation/pool/add-working?protocol=${proto}` : '/api/rotation/pool/add-working';
      const res = await fetch(url, { method: 'POST' });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Failed to add working nodes');
      this.showToast(`Added ${data.added || 0} working nodes to rotation pool`, 'success');
      this.loadConfigs();
      this.loadRotationStatus();
      this.loadRotationPool();
    } catch (e) {
      this.showToast(e.message, 'error');
    }
  },

  async clearRotationPool() {
    try {
      const res = await fetch('/api/rotation/pool/clear', { method: 'POST' });
      if (!res.ok) throw new Error('Failed to clear pool');
      this.showToast('Rotation pool cleared', 'info');
      this.loadConfigs();
      this.loadRotationStatus();
      this.loadRotationPool();
    } catch (e) {
      this.showToast(e.message, 'error');
    }
  },

  async saveRotationConfig(params) {
    try {
      const current = this.state.rotationStatus || {};
      const payload = {
        enabled: params.enabled !== undefined ? params.enabled : !!current.enabled,
        interval: params.interval || current.interval || '15m',
        target: params.target || current.target || 'https://www.google.com/generate_204',
        threads: params.threads || current.threads || 50,
      };

      const res = await fetch('/api/rotation/config', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Failed to save configuration');
      this.state.rotationStatus = data;
      this.renderRotationStatus(data);
      this.showToast('Auto-rotation parameters saved', 'success');
    } catch (e) {
      this.showToast(e.message, 'error');
    }
  },

  async triggerRotationNow() {
    try {
      const res = await fetch('/api/rotation/trigger', { method: 'POST' });
      if (!res.ok) throw new Error('Trigger failed');
      this.showToast('Rotation triggered! Benchmarking pool...', 'info');
      this.loadRotationStatus();
    } catch (e) {
      this.showToast(e.message, 'error');
    }
  },

  async loadRotationStatus() {
    try {
      const res = await fetch('/api/rotation/status');
      if (!res.ok) return;
      const data = await res.json();
      this.state.rotationStatus = data;
      this.renderRotationStatus(data);
    } catch (e) {
      console.error('Failed to load rotation status', e);
    }
  },

  async loadRotationPool() {
    try {
      const res = await fetch('/api/rotation/pool');
      if (!res.ok) return;
      const data = await res.json();
      this.renderRotationPool(data.configs || []);
    } catch (e) {
      console.error('Failed to load rotation pool', e);
    }
  },

  renderRotationStatus(data) {
    if (!data) return;
    this.state.rotationStatus = data;

    // Toggle & State
    const toggle = document.getElementById('toggle-rotation-enabled');
    const stateEl = document.getElementById('rot-stat-state');
    if (toggle) toggle.checked = !!data.enabled;
    if (stateEl) {
      if (data.is_benchmarking) {
        stateEl.textContent = 'Benchmarking...';
        stateEl.className = 'stat-value text-sm text-cyan';
      } else if (data.enabled) {
        stateEl.textContent = 'Active';
        stateEl.className = 'stat-value text-sm text-emerald';
      } else {
        stateEl.textContent = 'Disabled';
        stateEl.className = 'stat-value text-sm text-muted';
      }
    }

    // Interval badge
    const intervalBadge = document.getElementById('rot-stat-interval-badge');
    if (intervalBadge) intervalBadge.textContent = `Interval: ${data.interval || '15m'}`;

    // Selects in form
    const intervalSelect = document.getElementById('rot-interval-select');
    if (intervalSelect && data.interval) intervalSelect.value = data.interval;
    const targetInput = document.getElementById('rot-target-input');
    if (targetInput && data.target) targetInput.value = data.target;
    const threadsSelect = document.getElementById('rot-threads-select');
    if (threadsSelect && data.threads) threadsSelect.value = String(data.threads);

    // Pool counts
    const poolSize = document.getElementById('rot-stat-pool-size');
    const workingBadge = document.getElementById('rot-stat-working-badge');
    if (poolSize) poolSize.textContent = `${data.pool_size || 0} Nodes`;
    if (workingBadge) workingBadge.textContent = `${data.working_in_pool || 0} Working`;

    // Active node
    const activeNodeEl = document.getElementById('rot-stat-active-node');
    const activeMetricsEl = document.getElementById('rot-stat-active-metrics');
    if (data.active_node) {
      if (activeNodeEl) {
        activeNodeEl.textContent = data.active_node.name || `${data.active_node.address}:${data.active_node.port}`;
        activeNodeEl.title = data.active_node.name;
      }
      if (activeMetricsEl) {
        activeMetricsEl.textContent = `${data.active_node.protocol} | Active`;
      }
    } else {
      if (activeNodeEl) activeNodeEl.textContent = 'None';
      if (activeMetricsEl) activeMetricsEl.textContent = '—';
    }

    // History count & table
    const historyCount = document.getElementById('rot-history-count');
    if (historyCount) historyCount.textContent = `${(data.history || []).length} switches`;
    this.renderRotationHistory(data.history || []);
  },

  renderRotationPool(configs) {
    const tbody = document.getElementById('rotation-pool-tbody');
    if (!tbody) return;

    if (!configs || configs.length === 0) {
      tbody.innerHTML = `<tr><td colspan="7" class="text-center py-4 text-muted">No configurations in rotation pool. Add them from Configs Explorer.</td></tr>`;
      return;
    }

    tbody.innerHTML = configs.map((cfg, idx) => {
      const scoreStr = (cfg.score !== undefined && cfg.score !== null) ? cfg.score.toFixed(3) : '—';
      const latStr = (cfg.latency_ms !== undefined && cfg.latency_ms !== null) ? `${Math.round(cfg.latency_ms)} ms` : '—';
      const badgeClass = `badge-${cfg.protocol}`;

      return `
        <tr>
          <td class="text-muted font-bold">${idx + 1}</td>
          <td><span class="badge ${badgeClass}">${cfg.protocol}</span></td>
          <td class="font-medium">${this.escapeHtml(cfg.name || 'Unnamed')}</td>
          <td class="text-mono text-sm">${cfg.address}:${cfg.port}</td>
          <td><span class="text-mono ${cfg.latency_ms ? 'text-success' : ''}">${latStr}</span></td>
          <td><span class="text-mono">${scoreStr}</span></td>
          <td>
            <div style="display: flex; gap: 0.35rem;">
              <button class="btn btn-secondary btn-sm" onclick="app.connectProxy('${cfg.id}')" title="Connect Directly">Connect</button>
              <button class="btn btn-danger btn-sm" onclick="app.toggleConfigPool('${cfg.id}', true)" title="Remove from pool">✕</button>
            </div>
          </td>
        </tr>
      `;
    }).join('');
  },

  renderRotationHistory(history) {
    const tbody = document.getElementById('rotation-history-tbody');
    if (!tbody) return;

    if (!history || history.length === 0) {
      tbody.innerHTML = `<tr><td colspan="7" class="text-center py-4 text-muted">No rotation cycles recorded yet.</td></tr>`;
      return;
    }

    tbody.innerHTML = history.map(item => {
      const t = new Date(item.timestamp).toLocaleTimeString();
      const latStr = item.latency_ms > 0 ? `${Math.round(item.latency_ms)} ms` : '—';
      const scoreStr = item.score > 0 ? item.score.toFixed(3) : '—';
      const badgeClass = `badge-${item.protocol}`;

      return `
        <tr>
          <td class="text-mono text-xs text-muted">${t}</td>
          <td class="text-muted text-sm truncate" title="${this.escapeHtml(item.from_node)}">${this.escapeHtml(item.from_node || 'None')}</td>
          <td class="font-semibold text-emerald truncate" title="${this.escapeHtml(item.to_node)}">${this.escapeHtml(item.to_node)}</td>
          <td><span class="badge ${badgeClass}">${item.protocol}</span></td>
          <td><span class="text-mono text-success">${latStr}</span></td>
          <td><span class="text-mono">${scoreStr}</span></td>
          <td class="text-xs text-muted">${this.escapeHtml(item.reason || 'Best score')}</td>
        </tr>
      `;
    }).join('');
  },

  startRotationTicker() {
    if (this.state.rotationCountdownTimer) clearInterval(this.state.rotationCountdownTimer);
    this.state.rotationCountdownTimer = setInterval(() => {
      const countdownEl = document.getElementById('rot-stat-countdown');
      if (!countdownEl) return;

      const rot = this.state.rotationStatus;
      if (!rot || !rot.enabled) {
        countdownEl.textContent = 'Paused';
        return;
      }
      if (rot.is_benchmarking) {
        countdownEl.textContent = 'Testing...';
        return;
      }
      if (rot.next_rotation_at) {
        const diffMs = new Date(rot.next_rotation_at).getTime() - Date.now();
        if (diffMs <= 0) {
          countdownEl.textContent = '00:00';
          return;
        }
        const totalSec = Math.floor(diffMs / 1000);
        const m = Math.floor(totalSec / 60);
        const s = totalSec % 60;
        countdownEl.textContent = `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
      } else {
        countdownEl.textContent = '--:--';
      }
    }, 1000);
  },

  // ==========================================================================
  // TEST METHODOLOGY & EXECUTION CHAIN
  // ==========================================================================

  async loadMethodology() {
    try {
      const res = await fetch('/api/benchmark/methodology');
      if (!res.ok) return;
      const data = await res.json();
      if (data.chain) {
        this.state.methodology = data.chain;
        this.renderMethodology();
      }
    } catch (e) {
      console.error('Failed to load methodology', e);
    }
  },

  renderMethodology() {
    const chain = this.state.methodology;
    if (!chain) return;

    // Update count badge
    const countBadge = document.getElementById('methodology-steps-count');
    if (countBadge) {
      const activeCount = (chain.steps || []).filter(s => s.enabled).length;
      countBadge.textContent = `${activeCount} Active / ${chain.steps ? chain.steps.length : 0} Steps`;
    }

    // Update scoring mode select
    const modeSelect = document.getElementById('methodology-scoring-mode');
    if (modeSelect) {
      modeSelect.value = chain.scoring_mode || 'primary_test';
    }

    const container = document.getElementById('methodology-steps-container');
    if (!container) return;

    if (!chain.steps || chain.steps.length === 0) {
      container.innerHTML = `<div class="text-center py-4 text-muted">No test steps configured. Click "+ Add Step" or choose a Preset.</div>`;
      return;
    }

    // Sort by priority before rendering
    chain.steps.sort((a, b) => (a.priority || 0) - (b.priority || 0));

    let html = '';
    chain.steps.forEach((step, idx) => {
      const isFirst = idx === 0;
      const isLast = idx === chain.steps.length - 1;
      const disabledClass = !step.enabled ? 'disabled-step' : '';
      const typeClass = `type-${step.type}`;

      let typeBadge = '';
      if (step.type === 'tcp_ping') {
        typeBadge = `<span class="badge badge-cyan">TCP Ping</span>`;
      } else if (step.type === 'tls_handshake') {
        typeBadge = `<span class="badge badge-purple">TLS Handshake</span>`;
      } else if (step.type === 'http_delay') {
        typeBadge = `<span class="badge badge-emerald">HTTP Delay Toward URL</span>`;
      }

      const necessaryBadge = step.necessary
        ? `<span class="step-badge-necessary" title="Hard Filter: Failure disqualifies node and skips subsequent stages">⚠️ Necessary (Hard Filter)</span>`
        : `<span class="step-badge-optional" title="Optional: Failure does not disqualify node">Optional</span>`;

      const primaryBadge = step.is_primary
        ? `<span class="step-badge-primary">★ Primary Score</span>`
        : '';

      const targetField = step.type === 'http_delay'
        ? `
          <div class="step-param-item" style="grid-column: span 2;">
            <label class="step-param-label">Target URL</label>
            <input type="text" class="step-param-input" value="${this.escapeHtml(step.target_url || 'https://www.google.com/generate_204')}" onchange="app.updateMethodologyStep(${idx}, 'target_url', this.value)">
          </div>
          <div class="step-param-item">
            <label class="step-param-label">Expected Codes (CSV)</label>
            <input type="text" class="step-param-input" value="${(step.expect_codes || [200, 204]).join(', ')}" onchange="app.updateMethodologyStepExpectCodes(${idx}, this.value)">
          </div>
        `
        : '';

      html += `
        <div class="methodology-step-card ${typeClass} ${disabledClass}">
          <div class="step-card-header">
            <div class="step-meta-left">
              <span class="step-badge-priority">Stage #${idx + 1}</span>
              ${typeBadge}
              <input type="text" class="step-param-input font-bold" style="width: 170px;" value="${this.escapeHtml(step.name || '')}" onchange="app.updateMethodologyStep(${idx}, 'name', this.value)">
              ${necessaryBadge}
              ${primaryBadge}
            </div>

            <div class="step-actions-right">
              <label class="checkbox-label text-xs mr-2">
                <input type="checkbox" ${step.necessary ? 'checked' : ''} onchange="app.toggleMethodologyNecessary(${idx})">
                <span>Necessary</span>
              </label>

              <label class="checkbox-label text-xs mr-2">
                <input type="checkbox" ${step.enabled ? 'checked' : ''} onchange="app.toggleMethodologyStep(${idx})">
                <span>Enabled</span>
              </label>

              <button class="btn btn-secondary btn-xs" ${isFirst ? 'disabled' : ''} onclick="app.moveMethodologyStep(${idx}, -1)" title="Move Step Up">▲</button>
              <button class="btn btn-secondary btn-xs" ${isLast ? 'disabled' : ''} onclick="app.moveMethodologyStep(${idx}, 1)" title="Move Step Down">▼</button>
              <button class="btn btn-danger btn-xs" onclick="app.removeMethodologyStep(${idx})" title="Remove Step">🗑</button>
            </div>
          </div>

          <div class="step-params-grid">
            <div class="step-param-item">
              <label class="step-param-label">Timeout (ms)</label>
              <input type="number" class="step-param-input" value="${step.timeout_ms || 3000}" min="500" max="30000" step="100" onchange="app.updateMethodologyStep(${idx}, 'timeout_ms', parseInt(this.value, 10))">
            </div>

            <div class="step-param-item">
              <label class="step-param-label">Scoring Weight (0.0 - 1.0)</label>
              <input type="number" class="step-param-input" value="${step.weight !== undefined ? step.weight : 0.5}" min="0" max="1" step="0.1" onchange="app.updateMethodologyStep(${idx}, 'weight', parseFloat(this.value))">
            </div>

            <div class="step-param-item" style="display: flex; justify-content: center; align-items: flex-start;">
              <label class="step-param-label">Primary Metric</label>
              <label class="checkbox-label text-xs mt-1">
                <input type="radio" name="methodology-primary-radio" ${step.is_primary ? 'checked' : ''} onchange="app.setMethodologyPrimary(${idx})">
                <span>Use as Primary</span>
              </label>
            </div>

            ${targetField}
          </div>
        </div>
      `;

      if (!isLast) {
        html += `
          <div class="step-connector-flow">
            <div class="step-connector-line"></div>
            <span class="step-connector-arrow">↓</span>
            <span class="text-xs text-muted">Survivors advance to next stage (Failing Necessary step eliminates node)</span>
            <span class="step-connector-arrow">↓</span>
            <div class="step-connector-line"></div>
          </div>
        `;
      }
    });

    container.innerHTML = html;
  },

  onScoringModeChange(mode) {
    if (!this.state.methodology) return;
    this.state.methodology.scoring_mode = mode;
  },

  moveMethodologyStep(idx, direction) {
    const chain = this.state.methodology;
    if (!chain || !chain.steps) return;
    const targetIdx = idx + direction;
    if (targetIdx < 0 || targetIdx >= chain.steps.length) return;

    const temp = chain.steps[idx];
    chain.steps[idx] = chain.steps[targetIdx];
    chain.steps[targetIdx] = temp;

    // Normalize priorities
    chain.steps.forEach((s, i) => { s.priority = i + 1; });
    this.renderMethodology();
  },

  toggleMethodologyStep(idx) {
    const chain = this.state.methodology;
    if (!chain || !chain.steps || !chain.steps[idx]) return;
    chain.steps[idx].enabled = !chain.steps[idx].enabled;
    this.renderMethodology();
  },

  toggleMethodologyNecessary(idx) {
    const chain = this.state.methodology;
    if (!chain || !chain.steps || !chain.steps[idx]) return;
    chain.steps[idx].necessary = !chain.steps[idx].necessary;
    this.renderMethodology();
  },

  setMethodologyPrimary(idx) {
    const chain = this.state.methodology;
    if (!chain || !chain.steps) return;
    chain.steps.forEach((s, i) => {
      s.is_primary = (i === idx);
    });
    this.renderMethodology();
  },

  updateMethodologyStep(idx, field, value) {
    const chain = this.state.methodology;
    if (!chain || !chain.steps || !chain.steps[idx]) return;
    chain.steps[idx][field] = value;
  },

  updateMethodologyStepExpectCodes(idx, codesStr) {
    const chain = this.state.methodology;
    if (!chain || !chain.steps || !chain.steps[idx]) return;
    const codes = codesStr.split(',')
      .map(c => parseInt(c.trim(), 10))
      .filter(c => !isNaN(c) && c > 0);
    chain.steps[idx].expect_codes = codes;
  },

  removeMethodologyStep(idx) {
    const chain = this.state.methodology;
    if (!chain || !chain.steps) return;
    if (chain.steps.length <= 1) {
      this.showToast('At least one step must remain in the chain', 'error');
      return;
    }
    chain.steps.splice(idx, 1);
    chain.steps.forEach((s, i) => { s.priority = i + 1; });
    this.renderMethodology();
  },

  openAddStepModal() {
    const modal = document.getElementById('modal-add-step');
    if (modal) modal.classList.remove('hidden');
  },

  closeAddStepModal() {
    const modal = document.getElementById('modal-add-step');
    if (modal) modal.classList.add('hidden');
  },

  onNewStepTypeChange(type) {
    const wrap = document.getElementById('new-step-target-wrap');
    const nameInput = document.getElementById('new-step-name');
    if (wrap) wrap.style.display = (type === 'http_delay') ? 'block' : 'none';
    if (nameInput) {
      if (type === 'tcp_ping') nameInput.value = 'TCP Reachability';
      else if (type === 'tls_handshake') nameInput.value = 'TLS Handshake';
      else if (type === 'http_delay') nameInput.value = 'HTTP Delay Toward URL';
    }
  },

  confirmAddStep() {
    const chain = this.state.methodology;
    if (!chain) return;

    const typeRadio = document.querySelector('input[name="new-step-type"]:checked');
    const type = typeRadio ? typeRadio.value : 'tcp_ping';
    const nameInput = document.getElementById('new-step-name');
    const name = (nameInput && nameInput.value.trim()) || (type === 'http_delay' ? 'HTTP Delay' : (type === 'tls_handshake' ? 'TLS Handshake' : 'TCP Reachability'));
    const timeoutInput = document.getElementById('new-step-timeout');
    const timeoutMS = timeoutInput ? parseInt(timeoutInput.value, 10) || 3000 : 3000;
    const targetInput = document.getElementById('new-step-target');
    const targetURL = targetInput ? targetInput.value.trim() : 'https://www.google.com/generate_204';
    const necessaryCheck = document.getElementById('new-step-necessary');
    const necessary = necessaryCheck ? necessaryCheck.checked : true;

    if (!chain.steps) chain.steps = [];
    const newStep = {
      id: `step_${type}_${Date.now()}`,
      type: type,
      name: name,
      enabled: true,
      necessary: necessary,
      priority: chain.steps.length + 1,
      weight: 0.5,
      is_primary: type === 'http_delay' && !chain.steps.some(s => s.is_primary),
      timeout_ms: timeoutMS,
      target_url: type === 'http_delay' ? targetURL : '',
      expect_codes: type === 'http_delay' ? [200, 204] : []
    };

    chain.steps.push(newStep);
    this.closeAddStepModal();
    this.renderMethodology();
    this.showToast(`Added step "${name}" to chain`, 'info');
  },

  async applyMethodologyPreset(preset) {
    let steps = [];
    let scoringMode = 'primary_test';

    switch (preset) {
      case 'url_only':
        steps = [
          {
            id: 'step_http_delay',
            type: 'http_delay',
            name: 'Direct HTTP Delay',
            description: 'Direct measurement of proxy response time toward target URL',
            enabled: true,
            necessary: true,
            priority: 1,
            weight: 1.0,
            is_primary: true,
            timeout_ms: 7000,
            target_url: 'https://www.google.com/generate_204',
            expect_codes: [200, 204]
          }
        ];
        scoringMode = 'primary_test';
        break;

      case 'tcp_only':
        steps = [
          {
            id: 'step_tcp_ping',
            type: 'tcp_ping',
            name: 'TCP Port Ping',
            description: 'Fast host:port socket connect reachability check',
            enabled: true,
            necessary: true,
            priority: 1,
            weight: 1.0,
            is_primary: true,
            timeout_ms: 2500
          }
        ];
        scoringMode = 'primary_test';
        break;

      case 'deep':
        steps = [
          {
            id: 'step_tcp_ping',
            type: 'tcp_ping',
            name: 'TCP Reachability',
            description: 'Fast socket connect to host:port',
            enabled: true,
            necessary: true,
            priority: 1,
            weight: 0.2,
            is_primary: false,
            timeout_ms: 2500
          },
          {
            id: 'step_tls_handshake',
            type: 'tls_handshake',
            name: 'TLS / REALITY Handshake',
            description: 'Strict cryptographic certificate and SNI validation',
            enabled: true,
            necessary: true,
            priority: 2,
            weight: 0.3,
            is_primary: false,
            timeout_ms: 4000
          },
          {
            id: 'step_http_delay',
            type: 'http_delay',
            name: 'HTTP URL Real Delay',
            description: 'Measured round-trip time toward Google endpoint',
            enabled: true,
            necessary: true,
            priority: 3,
            weight: 0.5,
            is_primary: true,
            timeout_ms: 8000,
            target_url: 'https://www.google.com/generate_204',
            expect_codes: [200, 204]
          }
        ];
        scoringMode = 'weighted_average';
        break;

      case 'standard':
      default:
        steps = [
          {
            id: 'step_tcp_ping',
            type: 'tcp_ping',
            name: 'TCP Port Ping',
            description: 'Fast socket connect to proxy host:port',
            enabled: true,
            necessary: true,
            priority: 1,
            weight: 0.2,
            is_primary: false,
            timeout_ms: 2500
          },
          {
            id: 'step_tls_handshake',
            type: 'tls_handshake',
            name: 'TLS / REALITY Handshake',
            description: 'TLS handshake with SNI and ALPN negotiation',
            enabled: true,
            necessary: false,
            priority: 2,
            weight: 0.2,
            is_primary: false,
            timeout_ms: 3500
          },
          {
            id: 'step_http_delay',
            type: 'http_delay',
            name: 'HTTP URL Real Delay',
            description: 'In-process proxy GET measuring round-trip TTFB latency',
            enabled: true,
            necessary: true,
            priority: 3,
            weight: 0.6,
            is_primary: true,
            timeout_ms: 7000,
            target_url: 'https://www.google.com/generate_204',
            expect_codes: [200, 204]
          }
        ];
        scoringMode = 'primary_test';
        break;
    }

    this.state.methodology = {
      scoring_mode: scoringMode,
      steps: steps
    };

    this.renderMethodology();
    await this.saveMethodology();
    this.showToast(`Applied preset: ${preset}`, 'info');
  },

  async saveMethodology() {
    const chain = this.state.methodology;
    if (!chain) return;

    const btn = document.getElementById('btn-save-methodology');
    if (btn) btn.disabled = true;

    try {
      const res = await fetch('/api/benchmark/methodology', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(chain)
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Failed to save methodology');

      this.state.methodology = data.chain;
      this.renderMethodology();
      this.showToast('Test methodology chain saved and applied!', 'success');
    } catch (e) {
      console.error('Failed to save methodology', e);
      this.showToast(e.message || 'Error saving methodology', 'error');
    } finally {
      if (btn) btn.disabled = false;
    }
  }
};

document.addEventListener('DOMContentLoaded', () => {
  app.init();
});

