// Usługi switcha: odczyt stanu i przełączanie (SSH, HTTP, Telnet, SNMP, LLDP, DHCP).
(function () {
  const list = document.getElementById('services-list');
  const refreshBtn = document.getElementById('services-refresh');
  const unsaved = document.getElementById('services-unsaved');
  const saveBtn = document.getElementById('services-save');
  const updated = document.getElementById('services-updated');
  if (!list || !refreshBtn) return;

  const escapeHTML = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const STATE_LABEL = { enabled: 'Włączona', disabled: 'Wyłączona', unknown: 'Stan nieustalony' };
  let services = [];

  async function request(method, body) {
    const res = await fetch('/api/services', {
      method,
      headers: { 'Content-Type': 'application/json' },
      body: body ? JSON.stringify(body) : undefined,
      cache: 'no-store',
    });
    const raw = await res.text();
    let data = {};
    try { data = raw ? JSON.parse(raw) : {}; } catch (_) { data = { error: raw }; }
    if (res.status === 401) { location.href = '/login'; throw new Error('Sesja wygasła.'); }
    if (!res.ok) throw new Error(data.error || 'Błąd ' + res.status);
    return data;
  }

  function control(s) {
    if (s.locked && s.state !== 'disabled') {
      return '<span class="svc-lock">Zablokowane w panelu</span>';
    }
    if (s.state === 'unknown') {
      return '<div class="svc-actions">' +
        '<button class="btn svc-btn" data-svc="' + escapeHTML(s.id) + '" data-enable="true">Włącz</button>' +
        (s.locked ? '' : '<button class="btn svc-btn" data-svc="' + escapeHTML(s.id) + '" data-enable="false">Wyłącz</button>') +
        '</div>';
    }
    const on = s.state === 'enabled';
    return '<button class="svc-switch" role="switch" aria-checked="' + on + '" data-svc="' + escapeHTML(s.id) + '" data-enable="' + !on + '" aria-label="' + (on ? 'Wyłącz ' : 'Włącz ') + escapeHTML(s.name) + '"><span></span></button>';
  }

  function render() {
    if (!services.length) {
      list.innerHTML = '<p class="svc-empty">Kliknij „Odczytaj usługi”, aby pobrać stan ze switcha.</p>';
      return;
    }
    list.innerHTML = services.map((s) =>
      '<article class="svc-row svc-' + s.state + '">' +
        '<div class="svc-main">' +
          '<h3>' + escapeHTML(s.name) + '<span class="svc-state">' + STATE_LABEL[s.state] + '</span></h3>' +
          '<p>' + escapeHTML(s.description) + '</p>' +
          '<small>Źródło stanu: ' + escapeHTML(s.source) + (s.locked ? '. Panel korzysta z tej usługi, więc nie da się jej tu wyłączyć.' : '') + '</small>' +
        '</div>' +
        '<div class="svc-control">' + control(s) + '</div>' +
      '</article>'
    ).join('');
  }

  async function refresh() {
    refreshBtn.disabled = true;
    list.setAttribute('aria-busy', 'true');
    if (!services.length) list.innerHTML = '<p class="svc-empty">Odczytuję stan usług ze switcha. Może to potrwać kilka sekund…</p>';
    try {
      const data = await request('GET');
      services = data.services || [];
      render();
      if (updated) updated.textContent = 'Odczytano o ' + new Date().toLocaleTimeString('pl-PL');
    } catch (e) {
      list.innerHTML = '<p class="svc-error">Nie udało się odczytać usług: ' + escapeHTML(e.message) + '</p>';
    } finally {
      refreshBtn.disabled = false;
      list.removeAttribute('aria-busy');
    }
  }

  async function change(id, enable) {
    const s = services.find((x) => x.id === id);
    if (!s) return;
    const cmd = enable ? s.enableCommand : s.disableCommand;
    let msg = (enable ? 'Włączyć' : 'Wyłączyć') + ' usługę „' + s.name + '”?\n\nPolecenie na switchu: ' + cmd;
    if (!enable && s.warning) msg += '\n\nUwaga: ' + s.warning;
    msg += '\n\nPrzed zmianą panel zapisze kopię konfiguracji.';
    if (!confirm(msg)) return;
    list.querySelectorAll('[data-svc]').forEach((b) => { b.disabled = true; });
    try {
      await request('POST', { id, enabled: enable, acknowledgeWarning: !enable && !!s.warning });
      if (unsaved) unsaved.hidden = false;
      await refresh();
    } catch (e) {
      alert('Zmiana nie powiodła się: ' + e.message);
      render();
    }
  }

  list.addEventListener('click', (e) => {
    const b = e.target.closest('[data-svc]');
    if (b && !b.disabled) change(b.dataset.svc, b.dataset.enable === 'true');
  });
  refreshBtn.addEventListener('click', refresh);

  if (saveBtn) {
    saveBtn.addEventListener('click', async () => {
      if (!confirm('Zapisać bieżącą konfigurację do startup-config, aby zmiany przetrwały restart switcha?')) return;
      saveBtn.disabled = true;
      try {
        const res = await fetch('/api/save', { method: 'POST' });
        const d = await res.json().catch(() => ({}));
        if (!res.ok) throw new Error(d.error || 'Błąd ' + res.status);
        unsaved.hidden = true;
      } catch (e) {
        alert('Nie udało się zapisać konfiguracji: ' + e.message);
      } finally {
        saveBtn.disabled = false;
      }
    });
  }
})();
