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
                body: JSON.stringify({ email, password })
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
            const storageBytes = data.data.storage_used || 0;
            const storageGB = (storageBytes / (1024 * 1024 * 1024)).toFixed(2);
            document.getElementById('stat-storage-used').textContent = `${storageGB} GB`;
            
            // Fetch jobs
            fetchJobs();
        } catch (error) {
            console.error('Failed to fetch stats:', error);
        }
    };
    
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
                
                const tr = document.createElement('tr');
                tr.innerHTML = `
                    <td>${job.repository_id}</td>
                    <td>
                        <span style="color: ${statusColor}; font-weight: 500;">
                            ${job.status.charAt(0).toUpperCase() + job.status.slice(1)}
                        </span>
                    </td>
                    <td>${new Date(job.started_at).toLocaleString()}</td>
                    <td>${job.duration_ms ? (job.duration_ms / 1000).toFixed(1) + 's' : '-'}</td>
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
}

// Add simple CSS for spinner animation
const style = document.createElement('style');
style.innerHTML = `
    .spin { animation: spin 1s linear infinite; }
    @keyframes spin { 100% { transform: rotate(360deg); } }
    .text-center { text-align: center; }
    .py-4 { padding-top: 16px; padding-bottom: 16px; }
`;
document.head.appendChild(style);
