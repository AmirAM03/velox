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
    isTesting: false,
  },

  init() {
    this.bindEvents();
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

    // Start Benchmark
    document.getElementById('btn-start-benchmark')?.addEventListener('click', () => {
      this.startBenchmark();
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

  async startBenchmark() {
    const target = document.getElementById('benchmark-target-input')?.value.trim() || 'https://www.google.com';
    const limit = parseInt(document.getElementById('benchmark-limit-input')?.value || '50', 10);

    const box = document.getElementById('benchmark-status-box');
    const startBtn = document.getElementById('btn-start-benchmark');
    if (startBtn) startBtn.disabled = true;

    if (box) {
      box.innerHTML = `
        <div class="text-center py-4">
          <div class="empty-icon">⚡</div>
          <p class="font-bold">Benchmarking ${limit} configs against:</p>
          <p class="text-mono text-sm text-cyan mb-3">${this.escapeHtml(target)}</p>
          <p class="text-muted text-xs">Running Stage 0 (DNS+TCP) → Stage 1 (TLS) → Stage 2 (Proxy Request)...</p>
        </div>
      `;
    }

    try {
      const res = await fetch('/api/test', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ target: target, limit: limit })
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || 'Benchmark failed');

      if (box) {
        box.innerHTML = `
          <div class="result-card">
            <div class="result-header">
              <span class="font-bold text-success">✅ Benchmark Finished</span>
              <span class="badge badge-vless">${data.passed}/${data.total} Passed</span>
            </div>
            <div class="result-grid">
              <div class="result-metric">
                <div class="result-metric-label">Tested Configs</div>
                <div class="result-metric-val">${data.total}</div>
              </div>
              <div class="result-metric">
                <div class="result-metric-label">Working Nodes</div>
                <div class="result-metric-val" style="color: var(--accent-emerald);">${data.passed}</div>
              </div>
              <div class="result-metric">
                <div class="result-metric-label">Fastest Latency</div>
                <div class="result-metric-val" style="color: var(--accent-cyan);">${data.fastest_ms ? Math.round(data.fastest_ms) + ' ms' : '—'}</div>
              </div>
              <div class="result-metric">
                <div class="result-metric-label">Target URL</div>
                <div class="result-metric-val text-xs text-muted" style="word-break: break-all;">${this.escapeHtml(target)}</div>
              </div>
            </div>
          </div>
        `;
      }

      this.showToast(`Benchmark complete! ${data.passed} working nodes scored.`, 'success');
      this.refreshStatus();
      this.loadTopNodes();
    } catch (e) {
      if (box) {
        box.innerHTML = `<div class="text-center text-danger py-4"><p>❌ Error: ${e.message}</p></div>`;
      }
      this.showToast(e.message, 'error');
    } finally {
      if (startBtn) startBtn.disabled = false;
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
