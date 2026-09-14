// app.js - cworker 控制台核心前端交互逻辑
(function () {
    'use strict';

    // 全局状态单例
    const state = {
        currentTab: 'nodes',
        refreshInterval: 2000,
        refreshTimer: null,
        nodes: [],
        jobs: [],
        overview: {},
        currentFsNode: '',
        currentFsPath: '',
        activeLogWs: null,
        activeLogJobId: '',
        activeLogBuffer: '',
        pendingKillJob: null
    };

    // DOM 元素引用缓存
    const els = {
        navNodes: document.getElementById('nav-tab-nodes'),
        navJobs: document.getElementById('nav-tab-jobs'),
        navFiles: document.getElementById('nav-tab-files'),
        viewNodes: document.getElementById('view-nodes'),
        viewJobs: document.getElementById('view-jobs'),
        viewFiles: document.getElementById('view-files'),
        topViewTitle: document.getElementById('top-view-title'),
        topPrimaryBtn: document.getElementById('top-primary-action-btn'),
        refreshSelect: document.getElementById('refresh-interval-select'),
        
        // 侧边栏与指标卡
        badgeNodes: document.getElementById('badge-nodes-count'),
        badgeJobs: document.getElementById('badge-jobs-running'),
        footerOnline: document.getElementById('footer-online-count'),
        footerRunning: document.getElementById('footer-running-count'),
        statTotalNodes: document.getElementById('stat-total-nodes'),
        statOnlineNodes: document.getElementById('stat-online-nodes'),
        statActiveJobs: document.getElementById('stat-active-jobs'),
        statAvgCpu: document.getElementById('stat-avg-cpu'),
        statTotalMem: document.getElementById('stat-total-mem'),
        
        // 表格
        nodesTable: document.getElementById('nodes-table-body'),
        jobsTable: document.getElementById('jobs-table-body'),
        fsTable: document.getElementById('fs-table-body'),
        
        // 过滤项
        jobNodeFilter: document.getElementById('job-node-filter'),
        jobStatusPills: document.getElementById('job-status-pills'),
        jobSearchInput: document.getElementById('job-search-input'),
        
        // 文件区
        fsNodeSelect: document.getElementById('fs-node-select'),
        fsPathInput: document.getElementById('fs-path-input'),
        fsBrowseBtn: document.getElementById('fs-browse-btn'),
        fsFileInput: document.getElementById('fs-file-input'),
        fsUploadBtn: document.getElementById('fs-upload-trigger-btn'),
        fsDropZone: document.getElementById('fs-drop-zone'),
        
        // 模态弹窗
        modalRun: document.getElementById('modal-run-job'),
        modalKill: document.getElementById('modal-kill-confirm'),
        modalClean: document.getElementById('modal-clean-jobs'),
        modalAddNode: document.getElementById('modal-add-node'),
        modalMyCard: document.getElementById('modal-my-card'),
        modalTerminal: document.getElementById('modal-terminal-logs'),
        modalTransfer: document.getElementById('modal-transfer'),
        
        // 终端
        terminalOutput: document.getElementById('terminal-output'),
        terminalJobId: document.getElementById('terminal-job-id'),
        terminalJobStatus: document.getElementById('terminal-job-status'),
        terminalAutoscroll: document.getElementById('terminal-autoscroll'),
        terminalClearBtn: document.getElementById('terminal-clear-btn'),
        terminalDownloadBtn: document.getElementById('terminal-download-btn'),
        
        toastContainer: document.getElementById('toast-container')
    };

    // ==================== 工具函数 ====================

    function showToast(msg, type = 'success') {
        const toast = document.createElement('div');
        toast.className = `toast ${type}`;
        toast.innerHTML = `<span>${type === 'success' ? '✔' : '✖'}</span> <span>${escapeHtml(msg)}</span>`;
        els.toastContainer.appendChild(toast);
        setTimeout(() => {
            toast.style.opacity = '0';
            setTimeout(() => toast.remove(), 300);
        }, 3200);
    }

    function escapeHtml(str) {
        if (!str) return '';
        return String(str)
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;')
            .replace(/'/g, '&#039;');
    }

    function formatBytes(bytes) {
        if (!bytes || bytes === 0) return '0 B';
        const k = 1024;
        const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
        const i = Math.floor(Math.log(bytes) / Math.log(k));
        return parseFloat((bytes / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
    }

    function formatUptime(startTimeStr, endTimeStr) {
        if (!startTimeStr) return '-';
        const start = new Date(startTimeStr);
        const end = endTimeStr ? new Date(endTimeStr) : new Date();
        const diffSec = Math.max(0, Math.floor((end - start) / 1000));
        
        const m = Math.floor(diffSec / 60);
        const s = diffSec % 60;
        const h = Math.floor(m / 60);
        if (h > 0) return `${h}h ${m % 60}m`;
        if (m > 0) return `${m}m ${s}s`;
        return `${s}s`;
    }

    function copyToClipboard(text, successMsg = '已复制到剪贴板') {
        if (navigator.clipboard && window.isSecureContext) {
            navigator.clipboard.writeText(text).then(() => showToast(successMsg));
        } else {
            const ta = document.createElement('textarea');
            ta.value = text;
            ta.style.position = 'fixed';
            ta.style.opacity = '0';
            document.body.appendChild(ta);
            ta.select();
            document.execCommand('copy');
            ta.remove();
            showToast(successMsg);
        }
    }

    function openModal(modalEl) {
        if (modalEl) modalEl.classList.add('open');
    }

    function closeModal(modalEl) {
        if (modalEl) modalEl.classList.remove('open');
    }

    // ==================== 标签页切换 ====================

    function switchTab(tab) {
        state.currentTab = tab;

        document.querySelectorAll('.sidebar-nav .nav-item').forEach(el => el.classList.remove('active'));
        document.querySelectorAll('.tab-view').forEach(el => el.style.display = 'none');

        if (tab === 'nodes') {
            els.navNodes.classList.add('active');
            els.viewNodes.style.display = 'flex';
            els.topViewTitle.innerHTML = '<span>🖥️</span> 节点矩阵 (Nodes)';
            els.topPrimaryBtn.innerText = '+ 添加 Worker';
            els.topPrimaryBtn.style.display = 'inline-flex';
            fetchNodes();
        } else if (tab === 'jobs') {
            els.navJobs.classList.add('active');
            els.viewJobs.style.display = 'flex';
            els.topViewTitle.innerHTML = '<span>⚡</span> 任务治理 (Jobs)';
            els.topPrimaryBtn.innerText = '+ 派发新任务';
            els.topPrimaryBtn.style.display = 'inline-flex';
            fetchJobs();
        } else if (tab === 'files') {
            els.navFiles.classList.add('active');
            els.viewFiles.style.display = 'flex';
            els.topViewTitle.innerHTML = '<span>📁</span> 文件互传 (Files)';
            els.topPrimaryBtn.style.display = 'none';
            if (!state.currentFsPath) {
                browseFiles(els.fsNodeSelect.value || '', '.');
            }
        }
    }

    // ==================== API 交互: Overview ====================

    async function fetchOverview() {
        try {
            const res = await fetch('/api/ui/overview');
            if (!res.ok) return;
            const data = await res.json();
            state.overview = data;

            els.badgeNodes.innerText = data.nodes_count || 0;
            els.badgeJobs.innerText = data.active_jobs || 0;
            els.footerOnline.innerText = `${data.online_count || 0} 在线`;
            els.footerRunning.innerText = `${data.active_jobs || 0} 运行中`;

            els.statTotalNodes.innerText = data.nodes_count || 0;
            els.statOnlineNodes.innerText = `${data.online_count || 0} 在线 (${data.nodes_count - data.online_count || 0} 离线)`;
            els.statActiveJobs.innerText = data.active_jobs || 0;
            els.statAvgCpu.innerText = (data.avg_cpu || 0).toFixed(1) + '%';
            els.statTotalMem.innerText = (data.total_free_mem_mb ? (data.total_free_mem_mb / 1024).toFixed(1) : 0) + ' GB';

            // 更新下拉列表
            updateNodeDropdowns(data.nodes || []);
        } catch (e) {
            console.error('fetchOverview failed', e);
        }
    }

    function updateNodeDropdowns(nodeList) {
        const updateSelect = (selectEl, defaultText) => {
            const currentVal = selectEl.value;
            selectEl.innerHTML = `<option value="">${defaultText}</option>`;
            nodeList.forEach(n => {
                const opt = document.createElement('option');
                opt.value = n.name;
                opt.innerText = `${n.name} (${n.status === 'ONLINE' ? '在线' : '离线'})`;
                selectEl.appendChild(opt);
            });
            selectEl.value = currentVal;
        };

        updateSelect(els.jobNodeFilter, '全部节点 (全集群)');
        updateSelect(document.getElementById('run-job-node'), '自动负载均衡 (推荐)');
        updateSelect(document.getElementById('clean-jobs-node'), '全集群所有已知 Worker');
        updateSelect(els.fsNodeSelect, '本机 (Local)');
        updateSelect(document.getElementById('transfer-src-node'), '本地 (Local)');
        updateSelect(document.getElementById('transfer-dst-node'), '本地 (Local)');
    }

    // ==================== API 交互: 节点管理 (Nodes) ====================

    async function fetchNodes() {
        try {
            const res = await fetch('/api/ui/nodes');
            if (!res.ok) throw new Error('拉取节点列表失败: ' + res.status);
            const data = await res.json();
            state.nodes = data.nodes || [];
            renderNodesTable(data.nodes || [], data.known || []);
        } catch (e) {
            els.nodesTable.innerHTML = `<tr><td colspan="8" style="text-align: center; color: #ef4444; padding: 24px;">${escapeHtml(e.message)}</td></tr>`;
        }
    }

    function renderNodesTable(nodes, knownList) {
        if (!nodes || nodes.length === 0) {
            els.nodesTable.innerHTML = `<tr><td colspan="8" style="text-align: center; color: var(--text-dim); padding: 32px;">暂无节点。点击右上角 "+ 添加 Worker" 开始连接。</td></tr>`;
            return;
        }

        const knownMap = {};
        (knownList || []).forEach(k => { knownMap[k.name.toLowerCase()] = k; });

        let html = '';
        nodes.forEach(n => {
            const isOnline = n.status === 'ONLINE';
            const statusDot = `<span class="status-dot ${isOnline ? 'online' : 'offline'}"></span> <span style="font-size: 12px; font-weight: 600; color: ${isOnline ? '#4ade80' : '#f87171'}">${n.status}</span>`;
            
            const cpu = isOnline ? (n.metrics ? n.metrics.cpu_percent : 0) : 0;
            const cpuBar = isOnline ? `
                <div style="display: flex; align-items: center; gap: 8px;">
                    <div class="progress-bar-wrap"><div class="progress-bar-fill" style="width: ${Math.min(100, Math.max(0, cpu))}%;"></div></div>
                    <span style="font-size: 12px; font-family: var(--font-mono);">${cpu.toFixed(1)}%</span>
                </div>` : '<span style="color: var(--text-dim);">-</span>';

            const memUsedMB = (n.metrics && isOnline) ? (n.metrics.mem_total_mb - n.metrics.mem_free_mb) : 0;
            const memTotalMB = (n.metrics && isOnline) ? n.metrics.mem_total_mb : 0;
            const memStr = isOnline ? `${(memUsedMB / 1024).toFixed(1)}G / ${(memTotalMB / 1024).toFixed(1)}G` : '<span style="color: var(--text-dim);">-</span>';

            const knownEntry = knownMap[n.name.toLowerCase()] || {};
            const token = knownEntry.token || '';
            const tokenCell = token ? `
                <div style="display: flex; align-items: center; gap: 6px;">
                    <span class="token-val" data-token="${escapeHtml(token)}" style="font-family: var(--font-mono); font-size: 11px; color: var(--text-dim);">••••••••••••</span>
                    <button class="btn btn-secondary btn-sm btn-peek-token" title="查看/隐藏">👁️</button>
                    <button class="btn btn-secondary btn-sm btn-copy-token" title="复制完整 Token">复制</button>
                </div>` : '<span style="color: var(--text-dim); font-size: 12px;">无 (未配置)</span>';

            html += `
                <tr data-node="${escapeHtml(n.name)}">
                    <td>${statusDot}</td>
                    <td><strong>${escapeHtml(n.name)}</strong></td>
                    <td style="font-family: var(--font-mono); font-size: 12px; color: var(--text-muted);">${escapeHtml(n.address)}</td>
                    <td>${cpuBar}</td>
                    <td style="font-size: 12px;">${memStr}</td>
                    <td><span class="badge ${n.active_jobs > 0 ? 'badge-running' : 'badge-completed'}">${n.active_jobs || 0}</span></td>
                    <td>${tokenCell}</td>
                    <td style="text-align: right;">
                        <button class="btn btn-danger btn-sm btn-remove-node" data-name="${escapeHtml(n.name)}">移除</button>
                    </td>
                </tr>
            `;
        });

        els.nodesTable.innerHTML = html;

        // 绑定节点操作事件
        els.nodesTable.querySelectorAll('.btn-peek-token').forEach(btn => {
            btn.onclick = (e) => {
                const span = e.target.closest('tr').querySelector('.token-val');
                const raw = span.getAttribute('data-token');
                if (span.innerText === '••••••••••••') {
                    span.innerText = raw.length > 16 ? raw.slice(0, 16) + '...' : raw;
                    span.style.color = '#38bdf8';
                } else {
                    span.innerText = '••••••••••••';
                    span.style.color = 'var(--text-dim)';
                }
            };
        });

        els.nodesTable.querySelectorAll('.btn-copy-token').forEach(btn => {
            btn.onclick = (e) => {
                const span = e.target.closest('tr').querySelector('.token-val');
                const raw = span.getAttribute('data-token');
                copyToClipboard(raw, 'Token 复制成功');
            };
        });

        els.nodesTable.querySelectorAll('.btn-remove-node').forEach(btn => {
            btn.onclick = async (e) => {
                const name = e.target.getAttribute('data-name');
                if (!confirm(`确定要从已知节点账本中移除 '${name}' 吗？`)) return;
                try {
                    const res = await fetch(`/api/ui/nodes?name=${encodeURIComponent(name)}`, { method: 'DELETE' });
                    if (!res.ok) throw new Error(await res.text());
                    showToast(`已移除节点 '${name}'`);
                    fetchNodes();
                    fetchOverview();
                } catch (err) {
                    showToast(err.message, 'error');
                }
            };
        });
    }

    // ==================== API 交互: 任务治理 (Jobs) ====================

    async function fetchJobs() {
        const node = els.jobNodeFilter.value;
        const activePill = els.jobStatusPills.querySelector('.pill-item.active');
        const status = activePill ? activePill.getAttribute('data-status') : '';
        const search = (els.jobSearchInput.value || '').trim();

        try {
            const query = new URLSearchParams();
            if (node) query.set('node', node);
            if (status) query.set('status', status);
            if (search) query.set('search', search);

            const res = await fetch(`/api/ui/jobs?${query.toString()}`);
            if (!res.ok) throw new Error('拉取任务列表失败: ' + res.status);
            const jobs = await res.json();
            state.jobs = jobs || [];
            renderJobsTable(state.jobs);
        } catch (e) {
            els.jobsTable.innerHTML = `<tr><td colspan="9" style="text-align: center; color: #ef4444; padding: 24px;">${escapeHtml(e.message)}</td></tr>`;
        }
    }

    function renderJobsTable(jobs) {
        if (!jobs || jobs.length === 0) {
            els.jobsTable.innerHTML = `<tr><td colspan="9" style="text-align: center; color: var(--text-dim); padding: 32px;">暂无匹配任务。点击右上角 "+ 派发新任务" 派发一个。</td></tr>`;
            return;
        }

        let html = '';
        jobs.forEach(j => {
            const isRunning = j.status === 'RUNNING';
            let badgeClass = 'badge-completed';
            if (isRunning) badgeClass = 'badge-running';
            else if (j.status === 'FAILED') badgeClass = 'badge-failed';
            else if (j.status === 'STOPPED') badgeClass = 'badge-stopped';

            const uptime = formatUptime(j.start_time, j.end_time);
            const cpuStr = isRunning && j.metrics ? `${j.metrics.cpu_percent.toFixed(1)}%` : '-';
            const memStr = isRunning && j.metrics ? `${j.metrics.memory_mb}M` : '-';

            const cmdDisplay = escapeHtml(j.command.length > 35 ? j.command.slice(0, 32) + '...' : j.command);
            const fullCmd = escapeHtml(j.command);

            html += `
                <tr data-job-id="${escapeHtml(j.id)}">
                    <td style="font-family: var(--font-mono); font-weight: 600; color: var(--primary);">${escapeHtml(j.id)}</td>
                    <td><span style="font-size: 12px; background: var(--bg-elevated); padding: 2px 6px; border-radius: 4px;">${escapeHtml(j.node)}</span></td>
                    <td>${escapeHtml(j.name || '-')}</td>
                    <td><span class="badge ${badgeClass}">${j.status}</span></td>
                    <td style="font-family: var(--font-mono); font-size: 12px;">${cpuStr}</td>
                    <td style="font-family: var(--font-mono); font-size: 12px;">${memStr}</td>
                    <td style="font-size: 12px; color: var(--text-muted);">${uptime}</td>
                    <td title="${fullCmd}" style="font-family: var(--font-mono); font-size: 12px; color: var(--text-dim);">${cmdDisplay}</td>
                    <td style="text-align: right; white-space: nowrap;">
                        <button class="btn btn-secondary btn-sm btn-view-logs" data-id="${escapeHtml(j.id)}" data-status="${escapeHtml(j.status)}" data-cmd="${fullCmd}">日志</button>
                        ${isRunning ? `<button class="btn btn-danger btn-sm btn-kill-job" data-id="${escapeHtml(j.id)}" data-cmd="${fullCmd}" style="margin-left: 6px;">终止</button>` : ''}
                    </td>
                </tr>
            `;
        });

        els.jobsTable.innerHTML = html;

        // 绑定事件
        els.jobsTable.querySelectorAll('.btn-view-logs').forEach(btn => {
            btn.onclick = (e) => {
                const id = e.target.getAttribute('data-id');
                const status = e.target.getAttribute('data-status');
                openTerminal(id, status);
            };
        });

        els.jobsTable.querySelectorAll('.btn-kill-job').forEach(btn => {
            btn.onclick = (e) => {
                const id = e.target.getAttribute('data-id');
                const cmd = e.target.getAttribute('data-cmd');
                state.pendingKillJob = { id, cmd };
                document.getElementById('kill-job-id-text').innerText = id;
                document.getElementById('kill-job-cmd-text').innerText = cmd;
                openModal(els.modalKill);
            };
        });
    }

    // ==================== 实时流式终端日志 (WebSocket) ====================

    function openTerminal(jobId, status) {
        state.activeLogJobId = jobId;
        state.activeLogBuffer = '';
        els.terminalJobId.innerText = jobId;
        els.terminalJobStatus.innerText = status || 'RUNNING';
        els.terminalJobStatus.className = `badge ${status === 'RUNNING' ? 'badge-running' : 'badge-completed'}`;
        els.terminalOutput.innerHTML = '';
        openModal(els.modalTerminal);

        // 关闭旧连接
        if (state.activeLogWs) {
            state.activeLogWs.close();
            state.activeLogWs = null;
        }

        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const wsUrl = `${protocol}//${window.location.host}/api/ui/jobs/stream?job_id=${encodeURIComponent(jobId)}`;
        const ws = new WebSocket(wsUrl);
        state.activeLogWs = ws;

        document.getElementById('terminal-footer-hint').innerText = '正在建立 WebSocket 实时通道...';

        ws.onopen = () => {
            document.getElementById('terminal-footer-hint').innerText = '✔ WebSocket 实时流已连接，输出直通中...';
        };

        ws.onmessage = (event) => {
            const rawText = event.data;
            state.activeLogBuffer += rawText;
            const html = ansiToHtml(rawText);
            
            // 增量插入
            const span = document.createElement('span');
            span.innerHTML = html;
            els.terminalOutput.appendChild(span);

            if (els.terminalAutoscroll.checked) {
                els.terminalOutput.scrollTop = els.terminalOutput.scrollHeight;
            }
        };

        ws.onclose = () => {
            document.getElementById('terminal-footer-hint').innerText = '日志流已结束 (WebSocket 连接关闭)';
        };

        ws.onerror = () => {
            document.getElementById('terminal-footer-hint').innerText = '⚠️ WebSocket 连接异常断开';
        };
    }

    // ==================== API 交互: 文件系统 (Files) ====================

    async function browseFiles(node, path) {
        state.currentFsNode = node;
        state.currentFsPath = path;
        els.fsNodeSelect.value = node;
        els.fsPathInput.value = path;

        els.fsTable.innerHTML = `<tr><td colspan="5" style="text-align: center; color: var(--text-dim); padding: 24px;">正在扫描目录...</td></tr>`;

        try {
            const query = new URLSearchParams();
            if (node) query.set('node', node);
            query.set('path', path);

            const res = await fetch(`/api/ui/fs/ls?${query.toString()}`);
            if (!res.ok) throw new Error(await res.text());
            const files = await res.json();
            renderFilesTable(files || []);
        } catch (e) {
            els.fsTable.innerHTML = `<tr><td colspan="5" style="text-align: center; color: #ef4444; padding: 24px;">${escapeHtml(e.message)}</td></tr>`;
        }
    }

    function renderFilesTable(files) {
        if (!files || files.length === 0) {
            els.fsTable.innerHTML = `<tr><td colspan="5" style="text-align: center; color: var(--text-dim); padding: 32px;">当前目录为空</td></tr>`;
            return;
        }

        // 目录排前，文件排后
        files.sort((a, b) => {
            if (a.is_dir && !b.is_dir) return -1;
            if (!a.is_dir && b.is_dir) return 1;
            return a.name.localeCompare(b.name);
        });

        let html = '';
        // 增加返回上一级选项
        if (state.currentFsPath && state.currentFsPath !== '.' && state.currentFsPath !== '/' && state.currentFsPath !== '\\') {
            html += `
                <tr class="fs-parent-row" style="cursor: pointer;">
                    <td>📁</td>
                    <td colspan="4" style="color: var(--primary); font-weight: 600;">.. (返回上一级目录)</td>
                </tr>
            `;
        }

        files.forEach(f => {
            const icon = f.is_dir ? '📁' : '📄';
            const sizeStr = f.is_dir ? '<DIR>' : formatBytes(f.size);
            const modTime = f.mod_time ? new Date(f.mod_time).toLocaleString() : '-';

            html += `
                <tr data-name="${escapeHtml(f.name)}" data-is-dir="${f.is_dir}">
                    <td>${icon}</td>
                    <td><span class="${f.is_dir ? 'fs-dir-link' : ''}" style="${f.is_dir ? 'color: var(--primary); cursor: pointer; font-weight: 500;' : ''}">${escapeHtml(f.name)}</span></td>
                    <td style="font-family: var(--font-mono); font-size: 12px; color: var(--text-muted);">${sizeStr}</td>
                    <td style="font-size: 12px; color: var(--text-dim);">${modTime}</td>
                    <td style="text-align: right;">
                        ${!f.is_dir ? `<button class="btn btn-secondary btn-sm btn-download-file" data-name="${escapeHtml(f.name)}">下载</button>` : ''}
                    </td>
                </tr>
            `;
        });

        els.fsTable.innerHTML = html;

        // 目录点击下钻
        els.fsTable.querySelectorAll('.fs-dir-link').forEach(el => {
            el.onclick = () => {
                const name = el.closest('tr').getAttribute('data-name');
                const newPath = state.currentFsPath === '.' ? name : `${state.currentFsPath.replace(/[\\\/]$/, '')}/${name}`;
                browseFiles(state.currentFsNode, newPath);
            };
        });

        const parentRow = els.fsTable.querySelector('.fs-parent-row');
        if (parentRow) {
            parentRow.onclick = () => {
                const parts = state.currentFsPath.replace(/[\\\/]$/, '').split(/[\\\/]/);
                parts.pop();
                const newPath = parts.length === 0 ? '.' : parts.join('/') || '/';
                browseFiles(state.currentFsNode, newPath);
            };
        }

        // 文件下载
        els.fsTable.querySelectorAll('.btn-download-file').forEach(btn => {
            btn.onclick = (e) => {
                const fileName = e.target.getAttribute('data-name');
                const filePath = `${state.currentFsPath.replace(/[\\\/]$/, '')}/${fileName}`;
                const query = new URLSearchParams({
                    node: state.currentFsNode,
                    path: filePath
                });
                window.open(`/api/ui/fs/download?${query.toString()}`, '_blank');
            };
        });
    }

    // ==================== 表单提交与动作事件绑定 ====================

    function setupEventListeners() {
        // Tab 导航
        els.navNodes.onclick = () => switchTab('nodes');
        els.navJobs.onclick = () => switchTab('jobs');
        els.navFiles.onclick = () => switchTab('files');

        // 顶部操作主按钮
        els.topPrimaryBtn.onclick = () => {
            if (state.currentTab === 'nodes') {
                openModal(els.modalAddNode);
            } else if (state.currentTab === 'jobs') {
                openModal(els.modalRun);
            }
        };

        // 自动刷新选择
        els.refreshSelect.onchange = () => {
            state.refreshInterval = parseInt(els.refreshSelect.value, 10);
            resetRefreshLoop();
        };

        // 过滤项
        els.jobNodeFilter.onchange = () => fetchJobs();
        els.jobSearchInput.oninput = () => fetchJobs();
        els.jobStatusPills.querySelectorAll('.pill-item').forEach(pill => {
            pill.onclick = () => {
                els.jobStatusPills.querySelectorAll('.pill-item').forEach(p => p.classList.remove('active'));
                pill.classList.add('active');
                fetchJobs();
            };
        });

        // 模态框通用关闭 (带有 data-close 属性)
        document.querySelectorAll('[data-close]').forEach(btn => {
            btn.onclick = () => {
                const targetId = btn.getAttribute('data-close');
                closeModal(document.getElementById(targetId));
            };
        });

        // 模态框点击背景关闭
        document.querySelectorAll('.modal-overlay').forEach(overlay => {
            overlay.onclick = (e) => {
                if (e.target === overlay) {
                    closeModal(overlay);
                    if (overlay === els.modalTerminal && state.activeLogWs) {
                        state.activeLogWs.close();
                        state.activeLogWs = null;
                    }
                }
            };
        });

        // 1. 提交派发新任务
        document.getElementById('btn-submit-run-job').onclick = async () => {
            const node = document.getElementById('run-job-node').value;
            const name = document.getElementById('run-job-name').value.trim();
            const dir = document.getElementById('run-job-dir').value.trim();
            const cmd = document.getElementById('run-job-cmd').value.trim();

            if (!cmd) {
                showToast('执行命令不能为空', 'error');
                return;
            }

            try {
                const res = await fetch('/api/ui/jobs/run', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ node, name, dir, command: cmd })
                });
                if (!res.ok) throw new Error(await res.text());
                const jobInfo = await res.json();
                showToast(`任务已派发: ${jobInfo.id} (PID: ${jobInfo.pid})`);
                closeModal(els.modalRun);
                document.getElementById('run-job-cmd').value = '';
                switchTab('jobs');
                fetchJobs();
                fetchOverview();
            } catch (err) {
                showToast(`派发失败: ${err.message}`, 'error');
            }
        };

        // 2. 确认终止任务
        document.getElementById('btn-submit-kill-job').onclick = async () => {
            if (!state.pendingKillJob) return;
            const jobId = state.pendingKillJob.id;
            try {
                const res = await fetch('/api/ui/jobs/kill', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ job_id: jobId })
                });
                if (!res.ok) throw new Error(await res.text());
                showToast(`[OK] 任务 ${jobId} 已终止，子进程树彻底清理`);
                closeModal(els.modalKill);
                fetchJobs();
                fetchOverview();
            } catch (err) {
                showToast(`终止失败: ${err.message}`, 'error');
            }
        };

        // 3. 打开清理历史模态框
        document.getElementById('btn-open-clean-modal').onclick = () => {
            openModal(els.modalClean);
        };

        document.getElementById('btn-submit-clean-jobs').onclick = async () => {
            const node = document.getElementById('clean-jobs-node').value;
            const isAll = document.getElementById('clean-radio-all').checked;
            const days = parseInt(document.getElementById('clean-days-input').value, 10) || 7;

            try {
                const res = await fetch('/api/ui/jobs/clean', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ node, all: isAll, days: isAll ? 0 : days })
                });
                if (!res.ok) throw new Error(await res.text());
                const data = await res.json();
                let count = 0;
                let bytes = 0;
                Object.values(data).forEach(r => { count += r.cleaned_count; bytes += r.freed_bytes; });
                showToast(`清理成功：清理了 ${count} 个历史任务，释放磁盘 ${formatBytes(bytes)}`);
                closeModal(els.modalClean);
                fetchJobs();
                fetchOverview();
            } catch (err) {
                showToast(`清理失败: ${err.message}`, 'error');
            }
        };

        // 4. 添加 Worker 节点
        document.getElementById('btn-submit-add-node').onclick = async () => {
            const name = document.getElementById('add-node-name').value.trim();
            const target = document.getElementById('add-node-target').value.trim();
            const token = document.getElementById('add-node-token').value.trim();

            if (!name || !target) {
                showToast('节点别名与网络地址为必填项', 'error');
                return;
            }

            try {
                const res = await fetch('/api/ui/nodes', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({ name, target, token })
                });
                if (!res.ok) throw new Error(await res.text());
                showToast(`已成功添加并探活节点 '${name}'`);
                closeModal(els.modalAddNode);
                document.getElementById('add-node-name').value = '';
                document.getElementById('add-node-target').value = '';
                document.getElementById('add-node-token').value = '';
                fetchNodes();
                fetchOverview();
            } catch (err) {
                showToast(`添加节点失败: ${err.message}`, 'error');
            }
        };

        // 5. 本机名片 / Token
        document.getElementById('btn-open-my-card').onclick = async () => {
            try {
                const res = await fetch('/api/ui/overview');
                if (!res.ok) return;
                const data = await res.json();
                const card = data.local_card || {};
                document.getElementById('my-card-hostname').value = card.name || '-';
                document.getElementById('my-card-token').value = card.token || '无 (未生成)';
                const pairingCmd = `cw node add ${card.name || 'node'} ${card.ip || '127.0.0.1'}:${card.port || 19000} --token ${card.token || ''}`;
                document.getElementById('my-card-pairing-cmd').value = pairingCmd;
                openModal(els.modalMyCard);
            } catch (e) {
                showToast('获取名片失败', 'error');
            }
        };

        document.getElementById('btn-toggle-my-token').onclick = () => {
            const input = document.getElementById('my-card-token');
            input.type = input.type === 'password' ? 'text' : 'password';
        };

        document.getElementById('btn-copy-my-token').onclick = () => {
            copyToClipboard(document.getElementById('my-card-token').value, 'Token 复制成功');
        };

        document.getElementById('btn-copy-pairing-cmd').onclick = () => {
            copyToClipboard(document.getElementById('my-card-pairing-cmd').value, '配对命令已复制');
        };

        // 6. 终端清屏与下载
        els.terminalClearBtn.onclick = () => {
            els.terminalOutput.innerHTML = '';
            state.activeLogBuffer = '';
        };

        els.terminalDownloadBtn.onclick = () => {
            const blob = new Blob([state.activeLogBuffer], { type: 'text/plain;charset=utf-8' });
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = `${state.activeLogJobId || 'job'}.log`;
            a.click();
            URL.revokeObjectURL(url);
        };

        // 7. 文件管理
        els.fsBrowseBtn.onclick = () => {
            browseFiles(els.fsNodeSelect.value, els.fsPathInput.value.trim() || '.');
        };

        els.fsUploadBtn.onclick = () => {
            els.fsFileInput.click();
        };

        els.fsFileInput.onchange = async () => {
            const file = els.fsFileInput.files[0];
            if (!file) return;

            const targetPath = `${state.currentFsPath.replace(/[\\\/]$/, '')}/${file.name}`;
            const query = new URLSearchParams({
                node: state.currentFsNode,
                path: targetPath
            });

            showToast(`正在上传 '${file.name}' (${formatBytes(file.size)})...`);

            try {
                const res = await fetch(`/api/ui/fs/upload?${query.toString()}`, {
                    method: 'POST',
                    body: file
                });
                if (!res.ok) throw new Error(await res.text());
                showToast(`'${file.name}' 上传成功！`);
                browseFiles(state.currentFsNode, state.currentFsPath);
            } catch (err) {
                showToast(`上传失败: ${err.message}`, 'error');
            } finally {
                els.fsFileInput.value = '';
            }
        };

        // 8. 跨机互传 Modal
        document.getElementById('fs-open-transfer-modal-btn').onclick = () => {
            openModal(els.modalTransfer);
        };

        document.getElementById('btn-submit-transfer').onclick = async () => {
            const srcNode = document.getElementById('transfer-src-node').value;
            const srcPath = document.getElementById('transfer-src-path').value.trim();
            const dstNode = document.getElementById('transfer-dst-node').value;
            const dstPath = document.getElementById('transfer-dst-path').value.trim();
            const recursive = document.getElementById('transfer-recursive').checked;
            const concurrency = parseInt(document.getElementById('transfer-concurrency').value, 10) || 8;

            if (!srcPath || !dstPath) {
                showToast('源路径与目标路径均不能为空', 'error');
                return;
            }

            const pBox = document.getElementById('transfer-progress-box');
            pBox.style.display = 'block';
            document.getElementById('transfer-status-text').innerText = '正在通过底层流式管道传输...';
            document.getElementById('transfer-percent-text').innerText = '传输中';
            document.getElementById('transfer-progress-fill').style.width = '50%';

            try {
                const res = await fetch('/api/ui/fs/transfer', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/json' },
                    body: JSON.stringify({
                        src_node: srcNode,
                        src_path: srcPath,
                        dst_node: dstNode,
                        dst_path: dstPath,
                        recursive,
                        concurrency
                    })
                });
                if (!res.ok) throw new Error(await res.text());
                document.getElementById('transfer-progress-fill').style.width = '100%';
                document.getElementById('transfer-percent-text').innerText = '100%';
                document.getElementById('transfer-status-text').innerText = '✔ 传输完成并通过三方 SHA-256 校验';
                showToast('跨机互传完成！');
                setTimeout(() => {
                    closeModal(els.modalTransfer);
                    pBox.style.display = 'none';
                }, 1000);
            } catch (err) {
                showToast(`互传失败: ${err.message}`, 'error');
                document.getElementById('transfer-status-text').innerText = '传输失败: ' + err.message;
            }
        };
    }

    // ==================== 定时轮询与初始化 ====================

    function resetRefreshLoop() {
        if (state.refreshTimer) {
            clearInterval(state.refreshTimer);
            state.refreshTimer = null;
        }

        if (state.refreshInterval > 0) {
            state.refreshTimer = setInterval(() => {
                fetchOverview();
                if (state.currentTab === 'nodes') {
                    fetchNodes();
                } else if (state.currentTab === 'jobs') {
                    fetchJobs();
                }
            }, state.refreshInterval);
        }
    }

    // 页面初始化
    function init() {
        setupEventListeners();
        fetchOverview();
        fetchNodes();
        resetRefreshLoop();
    }

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', init);
    } else {
        init();
    }
})();
