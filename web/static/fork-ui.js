// [bootimus-fork L0] fork 自有的前端逻辑。
//
// 这些函数上游没有。放在本文件是为了让 web/static/app.js 的 diff 只剩
// 「改上游函数的那几行」——上游改 app.js 比其他文件都勤（近 100 个提交动了 42 次）。
//
// index.html 里本文件排在 app.js 之后；经典 script 的顶层声明是全局的，两边互相调用照旧。
// 以后新增 fork 专属界面逻辑请写这里，不要再往 app.js 里加。

// [bootimus-fork L1] fork-patch: download-proxy
async function loadProxyConfig() {
    try {
        const res = await authFetch(`${API_BASE}/proxy-config`);
        const data = await res.json();
        if (!data.success || !data.data) return;
        const addr = document.getElementById('proxy-address');
        const user = document.getElementById('proxy-username');
        const pass = document.getElementById('proxy-password');
        if (addr) addr.value = data.data.address || '';
        if (user) user.value = data.data.username || '';
        if (pass) pass.value = data.data.password || '';
    } catch (e) {
        console.error('Failed to load proxy settings:', e);
    }
}

async function saveProxyConfig(e) {
    e.preventDefault();
    const addrEl = document.getElementById('proxy-address');
    const userEl = document.getElementById('proxy-username');
    const passEl = document.getElementById('proxy-password');
    const hint = document.getElementById('proxy-result');
    const body = {
        address: (addrEl ? addrEl.value : '').trim(),
        username: (userEl ? userEl.value : '').trim(),
        password: passEl ? passEl.value : '',
    };
    try {
        const res = await authFetch(`${API_BASE}/proxy-config`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body),
        });
        const data = await res.json();
        if (data.success) {
            if (hint) hint.textContent = 'Saved';
            showAlert('Proxy settings saved', 'success');
        } else {
            if (hint) hint.textContent = '';
            showAlert(data.error || 'Failed to save proxy settings', 'error');
        }
    } catch (err) {
        showAlert('Failed to save proxy settings', 'error');
    }
}

async function openAutoInstallTemplateModal() {
    showModal('autoinstall-template-modal');
    const sel = document.getElementById('ai-tpl-select');
    const distroSel = document.getElementById('ai-tpl-distro');

    if (!aiTemplateCache) {
        try {
            const res = await authFetch(`${API_BASE}/autoinstall-templates`);
            const data = await res.json();
            aiTemplateCache = (data.data && data.data.templates) || [];
        } catch (e) {
            aiTemplateCache = [];
        }
    }

    sel.innerHTML = aiTemplateCache
        .map(t => `<option value="${escapeHtml(t.id)}">${escapeHtml(t.name)}</option>`)
        .join('');

    try {
        const res = await authFetch(`${API_BASE}/profiles`);
        const data = await res.json();
        distroSel.innerHTML = (data.data || [])
            .map(p => `<option value="${escapeHtml(p.profile_id)}">${escapeHtml(p.display_name)}</option>`)
            .join('');
    } catch (e) { /* 保留空列表 */ }

    onAutoInstallTemplateChange();
}

function currentAiTemplate() {
    const id = document.getElementById('ai-tpl-select').value;
    return (aiTemplateCache || []).find(t => t.id === id);
}

function onAutoInstallTemplateChange() {
    const tpl = currentAiTemplate();
    if (!tpl) return;
    document.getElementById('ai-tpl-filename').value = tpl.filename || '';
    document.getElementById('ai-tpl-summary').textContent = tpl.summary || '';
    const distroSel = document.getElementById('ai-tpl-distro');
    if (tpl.distro && [...distroSel.options].some(o => o.value === tpl.distro)) {
        distroSel.value = tpl.distro;
    }
    renderAiTemplateForm(tpl);
    previewAutoInstallTemplate();
}

function renderAiTemplateForm(tpl) {
    const box = document.getElementById('ai-tpl-vars');
    box.innerHTML = (tpl.vars || []).map(v => {
        const id = 'ai-var-' + v.name;
        const help = v.help
            ? `<small style="color: var(--text-secondary); display: block; margin-top: 2px;">${escapeHtml(v.help)}</small>`
            : '';
        const badge = v.per_client
            ? ` <span style="font-size: 11px; color: var(--accent);">per client</span>`
            : '';
        let input;
        if (v.type === 'select') {
            input = `<select class="form-control" id="${id}" data-var="${v.name}" onchange="previewAutoInstallTemplate()">`
                + (v.options || []).map(o =>
                    `<option value="${escapeHtml(o.value)}"${o.value === v.default ? ' selected' : ''}>${escapeHtml(o.label)}</option>`).join('')
                + '</select>';
        } else if (v.type === 'textarea') {
            input = `<textarea class="form-control" id="${id}" data-var="${v.name}" rows="2" oninput="scheduleAiTemplatePreview()">${escapeHtml(v.default || '')}</textarea>`;
        } else {
            const type = v.type === 'password' ? 'password' : 'text';
            input = `<input type="${type}" class="form-control" id="${id}" data-var="${v.name}" value="${escapeHtml(v.default || '')}" oninput="scheduleAiTemplatePreview()">`;
        }
        // 支持按客户端取值的字段：勾上就把值换成占位符，由 bootimus 在下发时替换
        const perClient = v.placeholder
            ? `<label style="display: flex; align-items: center; gap: 4px; margin: 4px 0 0; font-size: 12px; font-weight: 400; color: var(--accent);">`
              + `<input type="checkbox" id="ai-pc-${v.name}" style="margin: 0; padding: 0; width: auto; height: auto; flex: 0 0 auto;" onchange="toggleAiPerClient('${v.name}', this.checked)">`
              + `Use per-client value <code style="font-size: 11px;">${escapeHtml(v.placeholder)}</code></label>`
            : '';
        // 主机名按各自系统的默认命名规则生成（Windows: DESKTOP-XXXXXXX，Linux: <家族>-xxxxx）
        const randomBtn = v.name === 'hostname'
            ? `<div><button type="button" class="btn btn-sm" style="margin-top: 4px;" onclick="randomizeAiHostname()">Random</button></div>`
            : '';
        const required = v.required ? ' <span style="color: var(--danger);">*</span>' : '';
        return `<div class="form-group" style="margin-bottom: 8px;">`
            + `<label for="${id}" style="margin-bottom: 2px;">${escapeHtml(v.label)}${required}${badge}</label>`
            + input + randomBtn + perClient + help + '</div>';
    }).join('');
}

function randomHostnameFor(tpl) {
    const lower = 'abcdefghijklmnopqrstuvwxyz0123456789';
    const upper = 'ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789';
    const pick = (set, n) => Array.from({ length: n }, () => set[Math.floor(Math.random() * set.length)]).join('');
    // Windows 自己生成的计算机名就是这个形态（DESKTOP- 加 7 位大写字母数字）
    if (tpl && tpl.format === 'autounattend') {
        return 'DESKTOP-' + pick(upper, 7);
    }
    // Linux 主机名：小写、可含连字符，用发行版家族做前缀
    const family = ((tpl && tpl.distro) || 'host').replace(/[^a-z0-9]/gi, '').toLowerCase().slice(0, 12) || 'host';
    return family + '-' + pick(lower, 5);
}

function randomizeAiHostname() {
    const tpl = currentAiTemplate();
    const input = document.getElementById('ai-var-hostname');
    if (!tpl || !input || input.disabled) return;
    input.value = randomHostnameFor(tpl);
    previewAutoInstallTemplate();
}

function toggleAiPerClient(name, on) {
    const tpl = currentAiTemplate();
    if (!tpl) return;
    const v = (tpl.vars || []).find(x => x.name === name);
    const input = document.getElementById('ai-var-' + name);
    if (!v || !input) return;
    if (on) {
        input.dataset.savedValue = input.value;
        input.value = v.placeholder;
        input.disabled = true;
    } else {
        input.value = input.dataset.savedValue || v.default || '';
        input.disabled = false;
    }
    previewAutoInstallTemplate();
}

function scheduleAiTemplatePreview() {
    clearTimeout(aiPreviewTimer);
    aiPreviewTimer = setTimeout(previewAutoInstallTemplate, 400);
}

function collectAiTemplateValues() {
    const values = {};
    document.querySelectorAll('#ai-tpl-vars [data-var]').forEach(el => {
        values[el.dataset.var] = el.value;
    });
    return values;
}

async function previewAutoInstallTemplate() {
    const tpl = currentAiTemplate();
    if (!tpl) return;
    const status = document.getElementById('ai-tpl-status');
    const preview = document.getElementById('ai-tpl-preview');
    if (status) status.textContent = 'Rendering…';
    try {
        const res = await authFetch(`${API_BASE}/autoinstall/render`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ template: tpl.id, values: collectAiTemplateValues() }),
        });
        const data = await res.json();
        if (!data.success) throw new Error(data.error || 'Render failed');
        preview.value = data.data.content;
        if (status) status.textContent = '';
    } catch (err) {
        preview.value = '';
        if (status) status.textContent = err.message;
    }
}

async function saveAutoInstallTemplate() {
    const distro = document.getElementById('ai-tpl-distro').value;
    const filename = document.getElementById('ai-tpl-filename').value.trim();
    const content = document.getElementById('ai-tpl-preview').value;
    if (!distro) { showNotification('Please select a distro', 'warning'); return; }
    if (!filename) { showNotification('File name is required', 'warning'); return; }
    if (!content) { showNotification('Nothing to save — fix the fields first', 'error'); return; }
    try {
        const res = await authFetch(`${API_BASE}/autoinstall-files/save`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ distro, filename, content }),
        });
        const data = await res.json();
        if (!data.success) throw new Error(data.error || 'Save failed');
        showNotification('Auto-install file saved', 'success');
        closeModal('autoinstall-template-modal');
        loadAutoInstallFiles();
    } catch (err) {
        showNotification('Failed to save file: ' + err.message, 'error');
    }
}
