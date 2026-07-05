// Toast notifications
function showToast(message, type = 'success') {
    const container = document.getElementById('toast-container');
    const toast = document.createElement('div');
    toast.className = `toast ${type}`;
    
    let iconName = type === 'success' ? 'check-circle' : 'alert-circle';
    
    const icon = document.createElement('i');
    icon.setAttribute('data-lucide', iconName);
    
    const span = document.createElement('span');
    span.textContent = message;
    
    toast.appendChild(icon);
    toast.appendChild(span);
    
    container.appendChild(toast);
    lucide.createIcons();
    
    setTimeout(() => {
        toast.style.opacity = '0';
        toast.style.transform = 'translateX(100%)';
        setTimeout(() => toast.remove(), 300);
    }, 3000);
}

// API Helper
async function apiCall(endpoint, options = {}) {
    try {
        const response = await fetch(endpoint, {
            ...options,
            headers: {
                'Content-Type': 'application/json',
                ...options.headers
            }
        });
        
        const data = await response.json();
        
        if (!response.ok) {
            throw new Error(data.message || 'Something went wrong');
        }
        
        return data;
    } catch (error) {
        showToast(error.message, 'error');
        throw error;
    }
}

// Login
const loginForm = document.getElementById('login-form');
if (loginForm) {
    loginForm.addEventListener('submit', async (e) => {
        e.preventDefault();
        
        const email = document.getElementById('email').value;
        const password = document.getElementById('password').value;
        const btn = loginForm.querySelector('button');
        const originalText = btn.innerHTML;
        
        btn.innerHTML = '<i data-lucide="loader" class="spin"></i> <span>Signing In...</span>';
        btn.disabled = true;
        lucide.createIcons();
        
        try {
            await apiCall('/api/v1/auth/login', {
                method: 'POST',
                body: JSON.stringify({ username: email, password })
            });
            
            showToast('Login successful!');
            setTimeout(() => {
                window.location.href = '/dashboard';
            }, 500);
        } catch (error) {
            btn.innerHTML = originalText;
            btn.disabled = false;
            lucide.createIcons();
        }
    });
}

// Logout
const logoutBtn = document.getElementById('logout-btn');
if (logoutBtn) {
    logoutBtn.addEventListener('click', async () => {
        try {
            await apiCall('/api/v1/auth/logout', { method: 'POST' });
            window.location.href = '/login';
        } catch (error) {
            console.error(error);
        }
    });
}

// Dashboard
if (document.getElementById('stats-container')) {
    window.fetchStats = async () => {
        try {
            const data = await apiCall('/api/v1/dashboard/stats');
            
            // Remove skeleton classes
            document.querySelectorAll('.skeleton-text').forEach(el => {
                el.classList.remove('skeleton-text');
            });
            
            document.getElementById('stat-total-repos').textContent = data.data.total_repositories || 0;
            document.getElementById('stat-success-clones').textContent = data.data.successful_clones || 0;
            document.getElementById('stat-failed-clones').textContent = data.data.failed_clones || 0;
            
            // Format storage (bytes to GB)
            const storageBytes = data.data.total_storage_bytes || 0;
            const storageGB = (storageBytes / (1024 * 1024 * 1024)).toFixed(2);
            document.getElementById('stat-storage-used').textContent = `${storageGB} GB`;
            
            // Fetch jobs
            fetchJobs();
            fetchAndRenderTimeline();

            // Fetch paperbin quota and warning
            try {
                const quotaData = await apiCall('/api/v1/dashboard/paperbin-quota');
                const banner = document.getElementById('quota-warning-banner');
                if (banner) {
                    banner.style.display = quotaData.data && quotaData.data.exceeded ? 'flex' : 'none';
                }
            } catch (e) {
                console.error('Failed to fetch paperbin quota status:', e);
            }
        } catch (error) {
            console.error('Failed to fetch stats:', error);
        }
    };

    async function fetchAndRenderTimeline() {
        const container = document.getElementById('timeline-chart-container');
        if (!container) return;

        try {
            const res = await apiCall('/api/v1/dashboard/timeline?limit=50');
            const jobs = res.data || [];
            if (jobs.length === 0) {
                container.innerHTML = '<p class="text-muted text-center py-4">No recent jobs to display on timeline.</p>';
                return;
            }

            container.innerHTML = jobs.map(job => {
                const repoName = escHtml(job.repository ? job.repository.name : 'Unknown');
                const rawStatus = job.status || '';
                const status = escHtml(rawStatus);
                const duration = escHtml(job.duration_ms ? (job.duration_ms / 1000).toFixed(1) + 's' : '0s');

                // Construct progress width (Gantt duration bar)
                const startTime = escHtml(job.started_at ? new Date(job.started_at).toLocaleTimeString() : 'Pending');
                const completedTime = escHtml(job.completed_at ? new Date(job.completed_at).toLocaleTimeString() : '');

                // Color mapping
                const barClass = rawStatus === 'success' ? 'success' : (rawStatus === 'failed' ? 'failed' : 'running');
                const widthPercent = rawStatus === 'running' ? '100%' : (job.duration_ms ? Math.min(100, Math.max(10, (job.duration_ms / 1000) * 2)) + '%' : '10%');

                return `
                <div class="timeline-row" style="margin-bottom: 12px;">
                    <div class="timeline-row-label" title="${repoName}">${repoName}</div>
                    <div class="timeline-bar-container" title="Status: ${status}\nDuration: ${duration}\nStarted: ${startTime}\nCompleted: ${completedTime}">
                        <div class="timeline-bar ${barClass}" style="width: ${widthPercent};"></div>
                        <span style="position:absolute; left:8px; top:2px; font-size:0.75rem; color:#fff; font-weight:600; text-shadow:0 1px 2px rgba(0,0,0,0.8);">${duration} (${status})</span>
                    </div>
                </div>`;
            }).join('');
        } catch (e) {
            container.innerHTML = '<p class="text-muted text-center py-4">Failed to load timeline data.</p>';
        }
    }
    
    async function fetchJobs() {
        try {
            const data = await apiCall('/api/v1/dashboard/recent-jobs?limit=5');
            const tbody = document.getElementById('jobs-body');
            tbody.innerHTML = '';
            
            if (!data.data || data.data.length === 0) {
                tbody.innerHTML = '<tr><td colspan="4" class="text-center text-muted py-4">No recent jobs</td></tr>';
                return;
            }
            
            data.data.forEach(job => {
                const statusColor = job.status === 'success' ? 'var(--success)' : 
                                   (job.status === 'failed' ? 'var(--danger)' : 'var(--warning)');
                
                const repoName = escHtml(job.repository ? job.repository.name : job.repository_id);
                let duration = '-';
                if (job.started_at && job.completed_at) {
                    const secs = (new Date(job.completed_at) - new Date(job.started_at)) / 1000;
                    if (secs >= 0) duration = secs.toFixed(1) + 's';
                }
                const tr = document.createElement('tr');
                tr.innerHTML = `
                    <td>${repoName}</td>
                    <td>
                        <span style="color: ${statusColor}; font-weight: 500;">
                            ${job.status.charAt(0).toUpperCase() + job.status.slice(1)}
                        </span>
                    </td>
                    <td>${job.started_at ? new Date(job.started_at).toLocaleString() : '-'}</td>
                    <td>${duration}</td>
                `;
                tbody.appendChild(tr);
            });
        } catch (error) {
            console.error('Failed to fetch jobs:', error);
        }
    }
    
    // Initial fetch
    fetchStats();
}

// Config page
const oauthForm = document.getElementById('oauth-form');
if (oauthForm) {
    oauthForm.addEventListener('submit', async (e) => {
        e.preventDefault();
        
        const provider = document.getElementById('provider').value;
        const client_id = document.getElementById('client_id').value;
        const client_secret = document.getElementById('client_secret').value;
        const btn = oauthForm.querySelector('button');
        
        btn.disabled = true;
        
        try {
            const result = await apiCall('/api/v1/config/oauth', {
                method: 'PUT',
                body: JSON.stringify({ provider, client_id, client_secret })
            });
            
            showToast(result.message || 'Settings saved successfully');
            if (client_secret) {
                document.getElementById('client_secret').value = '';
            }
        } catch (error) {
            console.error(error);
        } finally {
            btn.disabled = false;
        }
    });

    async function loadConfig() {
        try {
            const data = await apiCall('/api/v1/config');
            const cfg = data.data;
            if (cfg && cfg.oauth && cfg.oauth.github) {
                document.getElementById('client_id').value = cfg.oauth.github.client_id || '';
            }
            
            // Load Paperbin Quota Settings
            const quotaRes = await apiCall('/api/v1/config/quota');
            if (quotaRes && quotaRes.data) {
                document.getElementById('paperbin_quota_gb').value = quotaRes.data.quota_gb || '50';
            }
        } catch (error) {
            console.error('Failed to load configuration:', error);
        }
    }

    window.saveQuotaConfig = async function(e) {
        e.preventDefault();
        const quotaGB = document.getElementById('paperbin_quota_gb').value;
        const btn = document.querySelector('#quota-form button');
        
        btn.disabled = true;
        try {
            const result = await apiCall('/api/v1/config/quota', {
                method: 'PUT',
                body: JSON.stringify({ quota_gb: quotaGB })
            });
            showToast(result.message || 'Quota settings saved successfully');
        } catch (error) {
            console.error(error);
        } finally {
            btn.disabled = false;
        }
    };

    window.registerGitHubAppAutomatically = async function() {
        const origin = window.location.origin;
        
        // Clean hostname (alphanumeric and hyphens only) and generate a random 5-character suffix for uniqueness
        const hostClean = window.location.hostname.replace(/[^a-zA-Z0-9-]/g, '-');
        const rand = Math.random().toString(36).substring(2, 7);
        
        let name;
        const hostLower = hostClean.toLowerCase();
        if (hostLower === 'localhost' || hostLower === '127-0-0-1' || hostLower === '::1' || !hostLower) {
            name = "GitDuppy-" + rand;
        } else {
            // Truncate host part to keep the final name strictly under GitHub's 34-character limit:
            // "GitDuppy-" (9 chars) + "-" (1 char) + hostPart + "-" (1 char) + rand (5 chars) = 16 chars overhead
            const maxHostLen = 34 - 16;
            const hostPart = hostClean.substring(0, maxHostLen);
            name = "GitDuppy-" + hostPart + "-" + rand;
        }

        const manifest = {
            name: name,
            url: origin,
            redirect_url: origin + "/api/v1/oauth/github/manifest-callback",
            callback_urls: [
                origin + "/api/v1/oauth/github/callback"
            ],
            setup_url: origin + "/config",
            public: false,
            default_permissions: {
                metadata: "read",
                contents: "read",
                issues: "read",
                pull_requests: "read",
                statuses: "read"
            },
            default_events: []
        };

        const manifestInput = document.getElementById('github-manifest-input');
        const manifestForm = document.getElementById('github-manifest-form');
        if (!manifestInput || !manifestForm) {
            showToast('Failed to find automatic registration form.', 'error');
            return;
        }
        manifestInput.value = JSON.stringify(manifest);

        // GitHub's App-manifest endpoint (github.com/settings/apps/new) requires an
        // authenticated github.com session. When the manifest is POSTed while signed
        // out, GitHub discards the form body during its login redirect and renders a
        // 500 error. We cannot sign the user in to GitHub from here — creating this
        // App is precisely what would grant us OAuth (chicken-and-egg) — so make sure
        // they are signed in to GitHub before submitting.
        const signedIn = confirm(
            'A GitHub App will be created on your GitHub account.\n\n' +
            'You must be signed in to GitHub.com in this browser first, otherwise ' +
            'GitHub returns a 500 error.\n\n' +
            'OK – I am signed in to GitHub, continue.\n' +
            'Cancel – open GitHub sign-in in a new tab first.'
        );
        if (!signedIn) {
            window.open('https://github.com/login', '_blank', 'noopener');
            showToast('Sign in to GitHub, then click "Register GitHub App Automatically" again.', 'success');
            return;
        }

        // Obtain a one-time setup nonce bound to our session. GitHub echoes the
        // ?state value back to manifest-callback, where it is validated to
        // prevent CSRF-driven credential injection.
        try {
            const setupRes = await apiCall('/api/v1/oauth/github/manifest-setup', { method: 'POST' });
            const state = setupRes && setupRes.data ? setupRes.data.state : '';
            if (!state) {
                showToast('Failed to initialize GitHub App setup.', 'error');
                return;
            }
            manifestForm.action = 'https://github.com/settings/apps/new?state=' + encodeURIComponent(state);
        } catch (e) {
            return; // apiCall already surfaced the error
        }
        manifestForm.submit();
    };

    // Load active settings on configuration page load
    loadConfig();
}

// Handle URL parameters for success/error messages from automated setup redirection.
// Runs on every page since the post-setup login flow now lands on the dashboard.
(function handleSetupRedirectParams() {
    const urlParams = new URLSearchParams(window.location.search);
    if (urlParams.has('success')) {
        if (urlParams.get('success') === 'github_setup') {
            showToast('GitHub App registered and login completed!', 'success');
        }
        window.history.replaceState({}, document.title, window.location.pathname);
    } else if (urlParams.has('error')) {
        const errorVal = urlParams.get('error');
        const errMsg = errorVal ? errorVal.replace(/_/g, ' ') : 'unknown error';
        showToast('Configuration failed: ' + errMsg, 'error');
        window.history.replaceState({}, document.title, window.location.pathname);
    }
})();

// ============================================================
// MAINTENANCE MODE (global banner + dashboard toggle)
// ============================================================
function applyMaintenanceState(enabled) {
    const banner = document.getElementById('maintenance-banner');
    if (banner) {
        banner.style.display = enabled ? 'flex' : 'none';
        if (enabled && window.lucide) lucide.createIcons();
    }
    const sw = document.getElementById('maintenance-switch');
    if (sw) sw.checked = enabled;
}

async function refreshMaintenanceState() {
    if (!document.getElementById('maintenance-banner') && !document.getElementById('maintenance-switch')) return;
    try {
        const res = await fetch('/api/v1/config/maintenance', { headers: { 'Content-Type': 'application/json' } });
        if (!res.ok) return;
        const data = await res.json();
        applyMaintenanceState(!!(data.data && data.data.enabled));
    } catch (e) { /* silent — banner just stays hidden */ }
}

window.toggleMaintenanceMode = async function(enabled) {
    try {
        const result = await apiCall('/api/v1/config/maintenance', {
            method: 'PUT',
            body: JSON.stringify({ enabled })
        });
        applyMaintenanceState(enabled);
        showToast(result.message || (enabled ? 'Maintenance mode enabled' : 'Maintenance mode disabled'), 'success');
    } catch (error) {
        // Revert the switch on failure (e.g. non-admin user).
        const sw = document.getElementById('maintenance-switch');
        if (sw) sw.checked = !enabled;
    }
};

refreshMaintenanceState();

// Add simple CSS for spinner animation
const style = document.createElement('style');
style.innerHTML = `
    .spin { animation: spin 1s linear infinite; }
    @keyframes spin { 100% { transform: rotate(360deg); } }
    .text-center { text-align: center; }
    .py-4 { padding-top: 16px; padding-bottom: 16px; }
`;
document.head.appendChild(style);

// ============================================================
// REPOSITORY LIST PAGE (/repos)
// ============================================================
const reposGrid = document.getElementById('repos-grid');
if (reposGrid) {
    let allRepos = [];

    async function loadRepos() {
        try {
            const data = await apiCall('/api/v1/repositories?per_page=100');
            allRepos = data.data || [];
            renderRepos(allRepos);
        } catch (e) {
            reposGrid.innerHTML = '<p class="text-muted">Failed to load repositories.</p>';
        }
    }

    function renderRepos(repos) {
        const empty = document.getElementById('repos-empty');
        if (!repos.length) {
            reposGrid.innerHTML = '';
            if (empty) empty.style.display = '';
            return;
        }
        if (empty) empty.style.display = 'none';

        reposGrid.innerHTML = repos.map(repo => {
            const status = repo.last_clone_status || repo.status || 'pending';
            const statusLabel = status === 'success' ? 'synced' : status;
            const lastSync = repo.last_clone_at ? timeAgo(new Date(repo.last_clone_at)) : 'Never';
            const desc = repo.description ? escHtml(repo.description) : '<span class="text-muted">No description</span>';
            return `
            <div class="repo-card glass-panel" onclick="window.location.href='/repos/${encodeURIComponent(repo.id)}'">
                <div class="repo-card-header">
                    <div class="repo-card-name">
                        <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15 22v-4a4.8 4.8 0 0 0-1-3.5c3 0 6-2 6-5.5.08-1.25-.27-2.48-1-3.5.28-1.15.28-2.35 0-3.5 0 0-1 0-3 1.5-2.64-.5-5.36-.5-8 0C6 2 5 2 5 2c-.3 1.15-.3 2.35 0 3.5A5.403 5.403 0 0 0 4 9c0 3.5 3 5.5 6 5.5-.39.49-.68 1.05-.85 1.65-.17.6-.22 1.23-.15 1.85v4"/><path d="M9 18c-4.51 2-5-2-7-2"/></svg>
                        ${escHtml(repo.name)}
                    </div>
                    <div style="display:flex; align-items:center; gap:6px;">
                        ${visibilityBadge(repo.visibility)}
                        <span class="status-badge ${statusLabel}">${statusLabel}</span>
                        <button class="btn btn-secondary btn-sm sync-card-btn" onclick="event.stopPropagation(); triggerSyncNow(${jsArg(repo.id)}, this)" title="Sync Now" style="padding:4px 8px; border-radius:4px; height:24px; display:inline-flex; align-items:center; justify-content:center;">
                            <i data-lucide="refresh-cw" style="width:12px; height:12px;"></i>
                        </button>
                    </div>
                </div>
                <p class="repo-card-desc">${desc}</p>
                <div class="repo-card-footer">
                    <span title="Branch">
                        <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="6" y1="3" x2="6" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/></svg>
                        ${escHtml(repo.branch)}
                    </span>
                    <span title="Last synced">
                        <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>
                        ${lastSync}
                    </span>
                </div>
            </div>`;
        }).join('');
    }

    window.filterRepos = function(q) {
        const filtered = allRepos.filter(r =>
            r.name.toLowerCase().includes(q.toLowerCase()) ||
            (r.description || '').toLowerCase().includes(q.toLowerCase())
        );
        renderRepos(filtered);
    };

    let isPaperbinView = false;

    window.togglePaperbinView = async function() {
        isPaperbinView = !isPaperbinView;
        const title = document.getElementById('repos-page-title');
        const desc = document.getElementById('repos-page-desc');
        const activeContainer = document.getElementById('repos-container');
        const paperbinContainer = document.getElementById('paperbin-container');
        const toggleBtn = document.getElementById('paperbin-toggle-btn');
        const searchWrap = document.getElementById('repo-search-wrap');
        
        if (isPaperbinView) {
            title.textContent = 'Paperbin';
            desc.textContent = 'Restore deleted repositories and branches (retained for 30 days).';
            activeContainer.style.display = 'none';
            paperbinContainer.style.display = 'block';
            searchWrap.style.display = 'none';
            
            toggleBtn.className = 'btn btn-secondary';
            toggleBtn.innerHTML = '<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="19" y1="12" x2="5" y2="12"/><polyline points="12 19 5 12 12 5"/></svg><span id="paperbin-btn-text">Back to Active</span>';
            
            await loadPaperbin();
        } else {
            title.textContent = 'Repositories';
            desc.textContent = 'All mirrored git repositories.';
            activeContainer.style.display = 'block';
            paperbinContainer.style.display = 'none';
            searchWrap.style.display = 'block';
            
            toggleBtn.className = 'btn btn-secondary';
            toggleBtn.innerHTML = '<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 6h18"/><path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6"/><path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/><line x1="10" y1="11" x2="10" y2="17"/><line x1="14" y1="11" x2="14" y2="17"/></svg><span id="paperbin-btn-text">Paperbin</span>';
            
            await loadRepos();
        }
    };

    async function loadPaperbin() {
        const deletedReposGrid = document.getElementById('deleted-repos-grid');
        const deletedReposEmpty = document.getElementById('deleted-repos-empty');
        const deletedBranchesBody = document.getElementById('deleted-branches-body');
        const deletedBranchesEmpty = document.getElementById('deleted-branches-empty');
        const deletedBranchesTable = document.getElementById('deleted-branches-table');
        
        deletedReposGrid.innerHTML = '<div class="repo-skeleton glass-panel"></div>';
        deletedBranchesBody.innerHTML = '<tr><td colspan="5" class="text-center py-4 text-muted">Loading pruned branches...</td></tr>';
        
        try {
            const data = await apiCall('/api/v1/repositories/paperbin');
            const payload = data.data || { repositories: [], branches: [] };
            
            // Render Deleted Repositories
            const repos = payload.repositories || [];
            if (repos.length === 0) {
                deletedReposGrid.innerHTML = '';
                deletedReposEmpty.style.display = 'block';
            } else {
                deletedReposEmpty.style.display = 'none';
                deletedReposGrid.innerHTML = repos.map(repo => {
                    const deletedDate = repo.deleted_at ? new Date(repo.deleted_at).toLocaleDateString() : 'Unknown';
                    const desc = repo.description ? escHtml(repo.description) : '<span class="text-muted">No description</span>';
                    return `
                    <div class="repo-card glass-panel paperbin-card" style="cursor: default;">
                        <div class="repo-card-header">
                            <div class="repo-card-name">
                                <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M15 22v-4a4.8 4.8 0 0 0-1-3.5c3 0 6-2 6-5.5.08-1.25-.27-2.48-1-3.5.28-1.15.28-2.35 0-3.5 0 0-1 0-3 1.5-2.64-.5-5.36-.5-8 0C6 2 5 2 5 2c-.3 1.15-.3 2.35 0 3.5A5.403 5.403 0 0 0 4 9c0 3.5 3 5.5 6 5.5-.39.49-.68 1.05-.85 1.65-.17.6-.22 1.23-.15 1.85v4"/><path d="M9 18c-4.51 2-5-2-7-2"/></svg>
                                ${escHtml(repo.name)}
                            </div>
                            <span class="status-badge error">deleted</span>
                        </div>
                        <p class="repo-card-desc">${desc}</p>
                        <div class="repo-card-footer mt-4" style="display:flex; justify-content:space-between; align-items:center; width:100%;">
                            <span class="text-muted text-sm" style="display:flex; align-items:center; gap:6px;">
                                <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/></svg>
                                Deleted: ${deletedDate}
                            </span>
                            <div class="card-actions" style="display:flex; gap:8px;">
                                <button class="btn btn-secondary btn-sm" onclick="restoreRepo(${jsArg(repo.id)})">
                                    <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21.5 2v6h-6M21.34 15.57a10 10 0 1 1-.57-8.38l5.67-5.67"/></svg>
                                    <span>Restore</span>
                                </button>
                                <button class="btn btn-danger btn-sm" onclick="deleteRepoPermanent(${jsArg(repo.id)}, ${jsArg(repo.name)})">
                                    <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 6h18"/><path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6"/><path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/><line x1="10" y1="11" x2="10" y2="17"/><line x1="14" y1="11" x2="14" y2="17"/></svg>
                                    <span>Purge</span>
                                </button>
                            </div>
                        </div>
                    </div>`;
                }).join('');
            }
            
            // Render Deleted Branches
            const branches = payload.branches || [];
            if (branches.length === 0) {
                deletedBranchesBody.innerHTML = '';
                deletedBranchesTable.style.display = 'none';
                deletedBranchesEmpty.style.display = 'block';
            } else {
                deletedBranchesEmpty.style.display = 'none';
                deletedBranchesTable.style.display = 'table';
                deletedBranchesBody.innerHTML = branches.map(br => {
                    const prunedDate = br.deleted_at ? new Date(br.deleted_at).toLocaleDateString() + ' ' + new Date(br.deleted_at).toLocaleTimeString([], {hour: '2-digit', minute:'2-digit'}) : 'Unknown';
                    const parentName = escHtml(br.repository ? br.repository.name : 'Unknown Repository');
                    const shortSHA = escHtml(br.commit_sha ? br.commit_sha.substring(0, 7) : 'Unknown');
                    return `
                    <tr>
                        <td style="font-weight:600; color:var(--primary); padding-left: 24px;">
                            <div style="display:flex; align-items:center; gap:8px;">
                                <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="6" y1="3" x2="6" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/></svg>
                                ${escHtml(br.branch_name)}
                            </div>
                        </td>
                        <td>${parentName}</td>
                        <td><code class="commit-sha" style="cursor:default;">${shortSHA}</code></td>
                        <td>${prunedDate}</td>
                        <td class="text-right" style="text-align: right; padding-right: 24px;">
                            <div style="display:inline-flex; gap:8px;">
                                <button class="btn btn-secondary btn-sm" onclick="restoreBranch(${jsArg(br.repository_id)}, ${jsArg(br.id)}, ${jsArg(br.branch_name)})">
                                    <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21.5 2v6h-6M21.34 15.57a10 10 0 1 1-.57-8.38l5.67-5.67"/></svg>
                                    <span>Restore</span>
                                </button>
                                <button class="btn btn-danger btn-sm" onclick="deleteBranchPermanent(${jsArg(br.repository_id)}, ${jsArg(br.id)}, ${jsArg(br.branch_name)})">
                                    <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 6h18"/><path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6"/><path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/><line x1="10" y1="11" x2="10" y2="17"/><line x1="14" y1="11" x2="14" y2="17"/></svg>
                                    <span>Purge</span>
                                </button>
                            </div>
                        </td>
                    </tr>`;
                }).join('');
            }
            
            lucide.createIcons();
        } catch (e) {
            deletedReposGrid.innerHTML = '<p class="text-muted">Failed to load paperbin contents.</p>';
            deletedBranchesBody.innerHTML = '<tr><td colspan="5" class="text-center py-4 text-muted">Failed to load pruned branches.</td></tr>';
        }
    }

    window.restoreRepo = async function(id) {
        if (confirm('Are you sure you want to restore this repository?')) {
            try {
                await apiCall(`/api/v1/repositories/${id}/restore`, { method: 'POST' });
                showToast('Repository restored successfully', 'success');
                await loadPaperbin();
            } catch (e) {
                showToast('Failed to restore repository: ' + e.message, 'error');
            }
        }
    };

    window.deleteRepoPermanent = async function(id, name) {
        if (confirm(`WARNING: This will permanently delete repository "${name}" and all its cloned files on disk! This action CANNOT be undone.\n\nAre you absolutely sure?`)) {
            try {
                await apiCall(`/api/v1/repositories/${id}/force`, { method: 'DELETE' });
                showToast('Repository permanently deleted', 'success');
                await loadPaperbin();
            } catch (e) {
                showToast('Failed to delete repository permanently: ' + e.message, 'error');
            }
        }
    };

    window.restoreBranch = async function(repoId, branchId, name) {
        if (confirm(`Are you sure you want to restore the branch "${name}"?`)) {
            try {
                await apiCall(`/api/v1/repositories/${repoId}/paperbin/branches/${branchId}/restore`, { method: 'POST' });
                showToast(`Branch "${name}" restored successfully`, 'success');
                await loadPaperbin();
            } catch (e) {
                showToast('Failed to restore branch: ' + e.message, 'error');
            }
        }
    };

    window.deleteBranchPermanent = async function(repoId, branchId, name) {
        if (confirm(`Are you sure you want to permanently delete branch "${name}" from the paperbin? This action CANNOT be undone.`)) {
            try {
                await apiCall(`/api/v1/repositories/${repoId}/paperbin/branches/${branchId}`, { method: 'DELETE' });
                showToast(`Branch "${name}" permanently deleted from paperbin`, 'success');
                await loadPaperbin();
            } catch (e) {
                showToast('Failed to delete branch: ' + e.message, 'error');
            }
        }
    };

    window.openAddRepoModal = function() {
        document.getElementById('add-repo-modal').style.display = 'flex';
    };

    window.closeAddRepoModal = function() {
        document.getElementById('add-repo-modal').style.display = 'none';
        document.getElementById('add-repo-form').reset();
        toggleAddAuthFields('none');
    };

    window.toggleAddAuthFields = function(val) {
        document.getElementById('add-auth-token-fields').style.display = val === 'https' ? 'block' : 'none';
        document.getElementById('add-auth-ssh-fields').style.display = val === 'ssh' ? 'block' : 'none';
    };

    window.submitAddRepo = async function(e) {
        e.preventDefault();
        const payload = {
            name: document.getElementById('add-repo-name').value,
            url: document.getElementById('add-repo-url').value,
            branch: document.getElementById('add-repo-branch').value,
            auth_type: document.getElementById('add-repo-auth').value,
            storage_path: 'repos/' + document.getElementById('add-repo-name').value,
            clone_interval_minutes: parseInt(document.getElementById('add-repo-interval').value),
            retention_days: parseInt(document.getElementById('add-repo-retention').value),
            lfs_enabled: document.getElementById('add-repo-lfs').checked,
            mirror_wiki: document.getElementById('add-repo-wiki').checked,
            mirror_issues: document.getElementById('add-repo-issues').checked,
            mirror_pull_requests: document.getElementById('add-repo-prs').checked,
            mirror_releases: document.getElementById('add-repo-releases').checked
        };
        
        if (payload.auth_type === 'https') {
            payload.credentials = {
                token: document.getElementById('add-repo-token').value
            };
        } else if (payload.auth_type === 'ssh') {
            payload.credentials = {
                ssh_key: document.getElementById('add-repo-sshkey').value
            };
        }

        try {
            await apiCall('/api/v1/repositories', {
                method: 'POST',
                body: JSON.stringify(payload)
            });
            showToast('Repository added successfully');
            closeAddRepoModal();
            loadRepos();
        } catch (error) {
            console.error(error);
        }
    };

    window.triggerSyncNow = async function(id, btn) {
        const originalHTML = btn.innerHTML;
        btn.disabled = true;
        btn.innerHTML = '<svg class="spin" xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21.5 2v6h-6M21.34 15.57a10 10 0 1 1-.57-8.38l5.67-5.67"/></svg>';
        try {
            await apiCall(`/api/v1/repositories/${id}/clone`, { method: 'POST' });
            showToast('Sync job queued successfully');
        } catch (error) {
            console.error(error);
        } finally {
            btn.innerHTML = originalHTML;
            btn.disabled = false;
        }
    };

    loadRepos();
}

// ============================================================
// REPOSITORY BROWSER PAGE (/repos/:id)
// ============================================================
const repoBrowser = document.getElementById('repo-browser');
if (repoBrowser) {
    const REPO_ID = repoBrowser.dataset.repoId;
    let currentRef = '';
    let currentPath = '';
    let currentCommitSha = '';
    let allRefs = [];

    let logWS = null;
    function startLiveLogStream(id) {
        if (logWS) {
            logWS.close();
        }

        const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
        const wsUrl = `${protocol}//${window.location.host}/api/v1/repositories/${id}/logs/stream`;
        
        const logsPanel = document.getElementById('live-logs-panel');
        const logsConsole = document.getElementById('live-logs-console');
        const logsStatus = document.getElementById('live-logs-status');

        if (!logsPanel || !logsConsole || !logsStatus) return;

        logsPanel.style.display = 'block';
        logsConsole.textContent = '';
        logsStatus.className = 'status-badge warning';
        logsStatus.textContent = 'streaming...';

        logWS = new WebSocket(wsUrl);
        
        logWS.onmessage = (event) => {
            logsConsole.textContent += event.data + '\n';
            logsConsole.scrollTop = logsConsole.scrollHeight;
        };

        logWS.onerror = (error) => {
            logsStatus.className = 'status-badge error';
            logsStatus.textContent = 'error';
        };

        logWS.onclose = () => {
            logsStatus.className = 'status-badge success';
            logsStatus.textContent = 'idle';
        };
    }

    window.syncRepoNow = async function() {
        const btn = document.getElementById('sync-now-btn');
        const originalHTML = btn.innerHTML;
        btn.disabled = true;
        btn.innerHTML = '<svg class="spin" xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M21.5 2v6h-6M21.34 15.57a10 10 0 1 1-.57-8.38l5.67-5.67"/></svg> <span>Syncing...</span>';
        
        try {
            await apiCall(`/api/v1/repositories/${REPO_ID}/clone`, { method: 'POST' });
            showToast('Sync job queued successfully');
            startLiveLogStream(REPO_ID);
        } catch (error) {
            console.error(error);
        } finally {
            btn.innerHTML = originalHTML;
            btn.disabled = false;
        }
    };

    window.downloadRepoZip = function() {
        window.location.href = `/api/v1/repos/${REPO_ID}/download?ref=${encodeURIComponent(currentRef)}`;
    };

    async function initRepoBrowser() {
        // Load repo metadata
        try {
            const data = await apiCall(`/api/v1/repositories/${REPO_ID}`);
            const repo = data.data;
            document.getElementById('repo-name-title').textContent = repo.name;
            document.getElementById('repo-description').textContent = repo.description || '';
            const badge = document.getElementById('repo-status-badge');
            const status = repo.last_clone_status || repo.status || 'pending';
            badge.className = `status-badge ${status === 'success' ? 'synced' : status}`;
            badge.textContent = status === 'success' ? 'synced' : status;
            badge.insertAdjacentHTML('afterend', visibilityBadge(repo.visibility));
            currentRef = repo.branch || 'main';
            document.getElementById('current-branch-label').textContent = currentRef;

            if (status === 'cloning' || status === 'pending') {
                startLiveLogStream(REPO_ID);
            }
        } catch (e) {
            document.getElementById('repo-name-title').textContent = 'Repository';
        }

        // Load refs
        try {
            const data = await apiCall(`/api/v1/repos/${REPO_ID}/refs`);
            allRefs = data.data || [];
            renderBranchList(allRefs);
        } catch (e) { /* ignore if not cloned yet */ }

        // Load initial tree
        await loadTree('', currentRef);
    }

    function renderBranchList(refs) {
        const container = document.getElementById('branch-list');
        const branches = refs.filter(r => r.type === 'branch');
        const tags = refs.filter(r => r.type === 'tag');
        let html = '';
        if (branches.length) {
            html += '<div class="branch-type-label">Branches</div>';
            html += branches.map(b => `
                <div class="branch-item ${b.name === currentRef ? 'active' : ''}" onclick="switchRef(${jsArg(b.name)})">
                    <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="6" y1="3" x2="6" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/></svg>
                    ${escHtml(b.name)}
                </div>`).join('');
        }
        if (tags.length) {
            html += '<div class="branch-type-label">Tags</div>';
            html += tags.map(t => `
                <div class="branch-item ${t.name === currentRef ? 'active' : ''}" onclick="switchRef(${jsArg(t.name)})">
                    <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 2H2v10l9.29 9.29c.94.94 2.48.94 3.42 0l6.58-6.58c.94-.94.94-2.48 0-3.42L12 2z"/><path d="M7 7h.01"/></svg>
                    ${escHtml(t.name)}
                </div>`).join('');
        }
        container.innerHTML = html || '<div class="branch-type-label">No refs found</div>';
    }

    window.filterRefs = function(q) {
        const filtered = allRefs.filter(r => r.name.toLowerCase().includes(q.toLowerCase()));
        renderBranchList(filtered);
    };

    window.toggleBranchMenu = function() {
        const dd = document.getElementById('branch-dropdown');
        dd.style.display = dd.style.display === 'none' ? 'block' : 'none';
    };

    window.switchRef = function(ref) {
        currentRef = ref;
        document.getElementById('current-branch-label').textContent = ref;
        document.getElementById('branch-dropdown').style.display = 'none';
        currentPath = '';
        renderBranchList(allRefs);
        loadTree('', ref);
        closeFilePanel();
    };

    // Close branch dropdown on outside click
    document.addEventListener('click', function(e) {
        const switcher = document.getElementById('branch-switcher');
        if (switcher && !switcher.contains(e.target)) {
            const dd = document.getElementById('branch-dropdown');
            if (dd) dd.style.display = 'none';
        }
    });

    async function loadTree(path, ref) {
        currentPath = path;
        document.getElementById('file-tree-loading').style.display = 'flex';
        document.getElementById('file-table').style.display = 'none';
        const commitBar = document.getElementById('latest-commit-bar');
        commitBar.style.display = 'none';

        updateBreadcrumb(path);

        try {
            const params = new URLSearchParams({ ref: ref || currentRef });
            if (path) params.set('path', path);
            const data = await apiCall(`/api/v1/repos/${REPO_ID}/tree?${params}`);
            const { entries, commit } = data.data;

            // Update commit bar
            if (commit) {
                currentCommitSha = commit.sha;
                document.getElementById('lc-message').textContent = commit.message;
                document.getElementById('lc-author').textContent = commit.author;
                document.getElementById('lc-sha').textContent = commit.sha;
                document.getElementById('lc-date').textContent = timeAgo(new Date(commit.date));
                commitBar.style.display = 'flex';
            }

            renderFileTable(entries, path);
        } catch (e) {
            document.getElementById('file-tree-loading').innerHTML = `<span class="text-muted">⚠ ${e.message || 'Could not load repository — has it been cloned yet?'}</span>`;
        }
    }

    function renderFileTable(entries, basePath) {
        const tbody = document.getElementById('file-table-body');
        const loading = document.getElementById('file-tree-loading');
        const table = document.getElementById('file-table');

        if (!entries || entries.length === 0) {
            loading.style.display = 'flex';
            loading.innerHTML = '<span class="text-muted">This directory is empty.</span>';
            return;
        }

        loading.style.display = 'none';
        table.style.display = '';

        // Parent dir row
        let rows = '';
        if (basePath) {
            const parent = basePath.includes('/') ? basePath.substring(0, basePath.lastIndexOf('/')) : '';
            rows += `<tr onclick="navigateTo(${jsArg(parent)})" style="cursor:pointer">
                <td class="file-icon"><svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/></svg></td>
                <td class="file-name-cell"><span class="file-name-link">..</span></td>
                <td class="file-commit-msg"></td>
                <td class="file-date"></td>
            </tr>`;
        }

        rows += entries.map(e => {
            const isDir = e.type === 'tree';
            const icon = isDir
                ? `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" style="color:#e3b341"><path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/></svg>`
                : `<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><polyline points="14 2 14 8 20 8"/></svg>`;
            const click = isDir
                ? `navigateTo(${jsArg(e.path)})`
                : `loadFile(${jsArg(e.path)}, ${jsArg(e.name)})`;
            const date = e.last_date ? timeAgo(new Date(e.last_date)) : '';
            return `<tr onclick="${click}" style="cursor:pointer">
                <td class="file-icon">${icon}</td>
                <td class="file-name-cell"><span class="file-name-link">${escHtml(e.name)}</span></td>
                <td class="file-commit-msg">${escHtml(e.last_message || '')}</td>
                <td class="file-date">${date}</td>
            </tr>`;
        }).join('');

        tbody.innerHTML = rows;
    }

    window.navigateTo = function(path) {
        closeFilePanel();
        loadTree(path, currentRef);
    };

    window.openCommit = function() {
        if (currentCommitSha) {
            window.location.href = `/repos/${REPO_ID}/commit/${currentCommitSha}`;
        }
    };

    function updateBreadcrumb(path) {
        const bc = document.getElementById('file-breadcrumb');
        let html = `<span class="bc-item bc-root" onclick="navigateTo('')"><svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="m3 9 9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/><polyline points="9 22 9 12 15 12 15 22"/></svg></span>`;
        if (path) {
            const parts = path.split('/');
            let accumulated = '';
            parts.forEach((part, i) => {
                accumulated = accumulated ? accumulated + '/' + part : part;
                const acc = accumulated;
                html += `<span class="bc-sep">/</span>`;
                if (i === parts.length - 1) {
                    html += `<span class="bc-item" style="cursor:default;color:var(--text-main)">${escHtml(part)}</span>`;
                } else {
                    html += `<span class="bc-item" onclick="navigateTo(${jsArg(acc)})">${escHtml(part)}</span>`;
                }
            });
        }
        bc.innerHTML = html;
        lucide.createIcons();
    }

    async function loadFile(path, name) {
        document.getElementById('file-content-panel').style.display = 'block';
        document.getElementById('fc-name').textContent = name;
        document.getElementById('fc-size').textContent = '';
        const codeEl = document.getElementById('fc-code');
        codeEl.textContent = 'Loading...';

        try {
            const params = new URLSearchParams({ ref: currentRef, path });
            const data = await apiCall(`/api/v1/repos/${REPO_ID}/blob?${params}`);
            const file = data.data;
            document.getElementById('fc-size').textContent = formatSize(file.size);
            if (file.is_binary) {
                codeEl.textContent = '[Binary file — cannot display]';
                codeEl.removeAttribute('class');
            } else {
                codeEl.textContent = file.content;
                codeEl.className = `language-${file.extension || 'plaintext'}`;
                if (window.hljs) hljs.highlightElement(codeEl);
            }
        } catch (e) {
            codeEl.textContent = 'Failed to load file: ' + e.message;
        }
    }

    window.loadFile = loadFile;

    window.closeFilePanel = function() {
        document.getElementById('file-content-panel').style.display = 'none';
    };

    // Commit history
    let commitsLoaded = false;
    let commitsVisible = false;

    window.toggleCommitsPanel = function() {
        const panel = document.getElementById('commits-panel');
        commitsVisible = !commitsVisible;
        panel.style.display = commitsVisible ? 'block' : 'none';
        document.getElementById('commits-toggle-btn').innerHTML = commitsVisible
            ? '<i data-lucide="chevron-up"></i>'
            : '<i data-lucide="chevron-down"></i>';
        lucide.createIcons();
        if (commitsVisible && !commitsLoaded) loadCommits();
    };

    async function loadCommits() {
        const list = document.getElementById('commits-list');
        list.innerHTML = '<div class="text-muted text-center py-4"><i data-lucide="loader" class="spin"></i> Loading commits...</div>';
        lucide.createIcons();
        try {
            const data = await apiCall(`/api/v1/repos/${REPO_ID}/commits?ref=${currentRef}&limit=30`);
            const commits = data.data || [];
            commitsLoaded = true;
            if (!commits.length) {
                list.innerHTML = '<div class="text-muted text-center py-4">No commits found.</div>';
                return;
            }
            list.innerHTML = commits.map(c => `
                <div class="commit-list-item">
                    <div class="cli-left">
                        <div class="cli-msg" onclick="window.location.href='/repos/${REPO_ID}/commit/${c.sha}'">${escHtml(c.message)}</div>
                        <div class="cli-meta">
                            <span>${escHtml(c.author)}</span>
                            <span>${timeAgo(new Date(c.date))}</span>
                        </div>
                    </div>
                    <div class="cli-right">
                        <span class="commit-sha" onclick="window.location.href='/repos/${REPO_ID}/commit/${c.sha}'">${c.short_sha}</span>
                    </div>
                </div>`).join('');
        } catch (e) {
            list.innerHTML = '<div class="text-muted text-center py-4">Failed to load commits.</div>';
        }
    }

    initRepoBrowser();
}

// ============================================================
// COMMIT DETAIL PAGE (/repos/:id/commit/:sha)
// ============================================================
const commitPage = document.getElementById('commit-page');
if (commitPage) {
    const REPO_ID = commitPage.dataset.repoId;
    const SHA = commitPage.dataset.sha;

    async function loadCommitDetail() {
        try {
            const data = await apiCall(`/api/v1/repos/${REPO_ID}/commit/${SHA}`);
            const c = data.data;

            document.getElementById('commit-sha-label').textContent = c.short_sha;
            document.getElementById('commit-full-message').textContent = c.message;
            document.getElementById('commit-author').textContent = `${c.author} <${c.author_email}>`;
            document.getElementById('commit-date').textContent = new Date(c.date).toLocaleString();
            document.getElementById('commit-sha-full').textContent = c.sha;

            // File stats summary
            if (c.file_stats && c.file_stats.length) {
                const totalAdd = c.file_stats.reduce((s, f) => s + f.additions, 0);
                const totalDel = c.file_stats.reduce((s, f) => s + f.deletions, 0);
                const statsBar = document.getElementById('file-stats-bar');
                statsBar.style.display = 'block';
                statsBar.innerHTML = `<span>${c.file_stats.length} file${c.file_stats.length !== 1 ? 's' : ''} changed&nbsp;&nbsp;</span>
                    <span class="diff-stats-add">+${totalAdd}</span>&nbsp;&nbsp;
                    <span class="diff-stats-del">-${totalDel}</span>`;
            }

            // Render diff
            renderDiff(c.diff || [], c.file_stats || []);
        } catch (e) {
            document.getElementById('diff-container').innerHTML = '<div class="text-muted text-center py-4">Failed to load commit: ' + escHtml(e.message) + '</div>';
        }
    }

    function renderDiff(lines, fileStats) {
        const container = document.getElementById('diff-container');
        if (!lines || !lines.length) {
            container.innerHTML = '<div class="text-muted text-center py-4">No diff available (initial commit or merge commit).</div>';
            return;
        }

        // Split by file sections (diff --git lines)
        const fileSections = [];
        let current = null;
        for (const line of lines) {
            if (line.startsWith('diff --git')) {
                if (current) fileSections.push(current);
                current = { header: line, lines: [] };
            } else if (current) {
                current.lines.push(line);
            }
        }
        if (current) fileSections.push(current);

        // Build stat lookup
        const statMap = {};
        fileStats.forEach(f => { statMap[f.name] = f; });

        container.innerHTML = fileSections.map(section => {
            // Extract filename
            const match = section.header.match(/diff --git a\/(.+) b\//);
            const fname = match ? match[1] : section.header;
            const stat = statMap[fname] || {};

            let lineNum = 0;
            const rows = section.lines.map(line => {
                if (line.startsWith('@@')) {
                    // Parse hunk header to get line number
                    const m = line.match(/@@ -\d+(?:,\d+)? \+(\d+)/);
                    if (m) lineNum = parseInt(m[1]) - 1;
                    return `<tr class="diff-hunk"><td></td><td>${escHtml(line)}</td></tr>`;
                } else if (line.startsWith('+')) {
                    lineNum++;
                    return `<tr class="diff-add"><td>${lineNum}</td><td>${escHtml(line)}</td></tr>`;
                } else if (line.startsWith('-')) {
                    return `<tr class="diff-del"><td></td><td>${escHtml(line)}</td></tr>`;
                } else if (line.startsWith('\\')) {
                    return '';
                } else {
                    lineNum++;
                    return `<tr class="diff-ctx"><td>${lineNum}</td><td>${escHtml(line)}</td></tr>`;
                }
            }).join('');

            return `<div class="diff-file-block glass-panel">
                <div class="diff-file-header">
                    <span class="diff-file-name">${escHtml(fname)}</span>
                    <span>
                        ${stat.additions ? `<span class="diff-stats-add">+${stat.additions}</span>` : ''}
                        ${stat.deletions ? `<span class="diff-stats-del">-${stat.deletions}</span>` : ''}
                    </span>
                </div>
                <div class="diff-body"><table>${rows}</table></div>
            </div>`;
        }).join('');
    }

    loadCommitDetail();
}

// ============================================================
// HELPERS
// ============================================================
function timeAgo(date) {
    const diff = Math.floor((Date.now() - date.getTime()) / 1000);
    if (diff < 60) return 'just now';
    if (diff < 3600) return Math.floor(diff / 60) + 'm ago';
    if (diff < 86400) return Math.floor(diff / 3600) + 'h ago';
    if (diff < 86400 * 30) return Math.floor(diff / 86400) + 'd ago';
    if (diff < 86400 * 365) return Math.floor(diff / (86400 * 30)) + 'mo ago';
    return Math.floor(diff / (86400 * 365)) + 'y ago';
}

function formatSize(bytes) {
    if (!bytes) return '';
    if (bytes < 1024) return bytes + ' B';
    if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB';
    return (bytes / (1024 * 1024)).toFixed(1) + ' MB';
}

function visibilityBadge(visibility) {
    if (visibility !== 'public' && visibility !== 'private') return '';
    const isPrivate = visibility === 'private';
    const label = isPrivate ? 'Private' : 'Public';
    const icon = isPrivate
        ? '<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="11" width="18" height="11" rx="2" ry="2"/><path d="M7 11V7a5 5 0 0 1 10 0v4"/></svg>'
        : '<svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>';
    return `<span class="visibility-badge ${visibility}" title="${label} repository">${icon}${label}</span>`;
}

function escHtml(str) {
    if (!str) return '';
    return String(str).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
}

// jsArg produces an HTML-attribute-safe, JS-safe quoted literal for use as an
// inline event-handler argument (e.g. onclick="fn(${jsArg(value)})").
function jsArg(val) {
    return escHtml(JSON.stringify(val == null ? '' : String(val)));
}

