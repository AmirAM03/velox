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
  },

  init() {
    this.bindEvents();
    this.setupEventSource();
    this.refreshStatus();
    this.loadTopNodes();
    
    // Poll status periodically every 3 seconds
    setInterval(() => this.refreshStatus(), 3000);
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
      pill.addEventListener('click', () => {
        document.querySelectorAll('#configs-proto-filters .filter-pill').forEach(p => p.classList.remove('active'));
        pill.classList.add('active');
        this.state.configsProto = pill.dataset.proto;
        this.state.configsPage = 0;
        this.loadConfigs();
      });
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
      const configs = await res.json();
      const tbody = document.getElementById('top-nodes-tbody');
      if (!tbody) return;

      if (!configs || configs.length === 0) {
        tbody.innerHTML = `<tr><td colspan="7" class="text-muted text-center py-4">No configurations in database. Go to Ingest & Parse tab.</td></tr>`;
        return;
      }

      tbody.innerHTML = configs.map((cfg, idx) => {
        const scoreStr = cfg.score ? cfg.score.toFixed(4) : '—';
        const latStr = cfg.latency_ms ? `${Math.round(cfg.latency_ms)} ms` : '—';
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

      const res = await fetch(url);
      if (!res.ok) return;
      const data = await res.json();
      const configs = data.configs || [];
      const total = data.total || 0;

      const tbody = document.getElementById('all-configs-tbody');
      if (!tbody) return;

      if (configs.length === 0) {
        tbody.innerHTML = `<tr><td colspan="7" class="text-muted text-center py-4">No configs match your filter.</td></tr>`;
      } else {
        tbody.innerHTML = configs.map((cfg, idx) => {
          const rank = offset + idx + 1;
          const scoreStr = cfg.score ? cfg.score.toFixed(4) : '—';
          const badgeClass = `badge-${cfg.protocol}`;
          const transportInfo = `${cfg.network || 'tcp'} / ${cfg.security || 'none'}`;

          return `
            <tr>
              <td class="text-muted font-bold">${rank}</td>
              <td><span class="badge ${badgeClass}">${cfg.protocol}</span></td>
              <td class="font-medium">${this.escapeHtml(cfg.name || 'Unnamed Node')}</td>
              <td class="text-mono text-sm">${cfg.address}:${cfg.port}</td>
              <td class="text-xs text-muted">${transportInfo}</td>
              <td><span class="text-mono">${scoreStr}</span></td>
              <td>
                <div style="display: flex; gap: 0.35rem;">
                  <button class="btn btn-secondary btn-sm" onclick="app.connectProxy('${cfg.id}')" title="Connect">Connect</button>
                  <button class="btn btn-secondary btn-sm" onclick="app.copyURI('${this.escapeHtml(cfg.raw_uri)}')" title="Copy URI">📋</button>
                </div>
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
      case 'cancelled':
        this.handleBenchmarkCancelled(msg);
        break;
      case 'complete':
        this.handleBenchmarkComplete(msg);
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
    navigator.clipboard.writeText(uri).then(() => {
      this.showToast('Raw URI copied to clipboard!', 'success');
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
  }
};

document.addEventListener('DOMContentLoaded', () => {
  app.init();
});
