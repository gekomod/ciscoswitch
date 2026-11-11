// Ustawienia panelu (tylko administrator): zapis w data/settings.json, działają od razu.
(function () {
  const root = document.getElementById('panel-settings');
  if (!root) return;
  const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const $id = (id) => document.getElementById(id);
  const box = $id('settings-form-box'), saveBtn = $id('settings-save'), info = $id('settings-updated');
  const SOURCE = { panel: 'Ustawione w panelu', env: 'Z pliku .env', default: 'Wartość domyślna' };
  let items = [], detected = '';

  const display = (it) => {
    if (it.kind === 'ports') return (it.value || '').split(',').filter(Boolean).map((p) => p.replace('port1.0.', '')).join(',');
    if (it.kind === 'seconds') return String(it.value || '').replace(/s$/, '');
    return it.value || '';
  };

  function field(it) {
    const id = 'set-' + it.key, v = display(it);
    let input;
    if (it.kind === 'bool') {
      input = '<select id="' + id + '"><option value="true"' + (v === 'true' ? ' selected' : '') + '>Tak</option><option value="false"' + (v !== 'true' ? ' selected' : '') + '>Nie</option></select>';
    } else if (it.kind === 'choice') {
      input = '<select id="' + id + '">' + it.options.map((o) => '<option value="' + esc(o) + '"' + (o === v ? ' selected' : '') + '>' + esc(o.toUpperCase()) + '</option>').join('') + '</select>';
    } else if (it.kind === 'int' || it.kind === 'seconds') {
      input = '<input id="' + id + '" type="number" min="' + it.min + '" max="' + it.max + '" value="' + esc(v) + '">';
    } else {
      const ph = it.key === 'FIRMWARE_HTTP_ADDR' ? (detected ? 'automatycznie: ' + detected : 'automatycznie') : (it.kind === 'ports' ? 'np. 1,49' : '');
      input = '<input id="' + id + '" type="text" value="' + esc(v) + '" placeholder="' + esc(ph) + '" autocomplete="off">';
    }
    return '<div class="set-field" data-key="' + esc(it.key) + '" data-initial="' + esc(v) + '">' +
      '<label for="' + id + '">' + esc(it.label) + '</label>' + input +
      '<small>' + esc(it.help) + '</small>' +
      '<div class="set-meta"><span class="set-src set-src-' + it.source + '">' + SOURCE[it.source] + '</span>' +
      (it.source === 'panel' ? '<button type="button" class="set-reset" data-reset="' + esc(it.key) + '">Przywróć z .env lub domyślną</button>' : '') + '</div></div>';
  }

  function render() {
    const groups = [];
    for (const it of items) {
      let g = groups.find((x) => x.name === it.group);
      if (!g) groups.push(g = { name: it.group, items: [] });
      g.items.push(it);
    }
    box.innerHTML = groups.map((g) => '<h3 class="files-h3">' + esc(g.name) + '</h3><div class="set-grid">' + g.items.map(field).join('') + '</div>').join('');
    saveBtn.disabled = false;
  }

  async function load() {
    try {
      const res = await fetch('/api/settings', { cache: 'no-store' });
      if (res.status === 401) { location.href = '/login'; return; }
      if (res.status === 403) { box.innerHTML = '<p class="mon-note">Ustawienia może zmieniać tylko administrator panelu.</p>'; saveBtn.hidden = true; return; }
      const d = await res.json();
      items = d.settings || [];
      detected = d.detectedFirmwareAddress || '';
      render();
    } catch (e) {
      box.innerHTML = '<p class="mon-error">Nie udało się wczytać ustawień: ' + esc(e.message) + '</p>';
    }
  }

  async function save(values) {
    saveBtn.disabled = true;
    try {
      const res = await fetch('/api/settings', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ values }) });
      const d = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(d.error || 'Błąd ' + res.status);
      items = d.settings || items;
      render();
      info.textContent = 'Zapisano o ' + new Date().toLocaleTimeString('pl-PL') + '. Zmiany działają od razu.';
    } catch (e) {
      alert('Nie zapisano: ' + e.message);
      saveBtn.disabled = false;
    }
  }

  saveBtn.addEventListener('click', () => {
    const values = {};
    box.querySelectorAll('.set-field').forEach((f) => {
      const el = f.querySelector('input,select');
      if (el.value.trim() !== f.dataset.initial) values[f.dataset.key] = el.value.trim();
    });
    if (!Object.keys(values).length) { info.textContent = 'Brak zmian do zapisania.'; return; }
    save(values);
  });

  box.addEventListener('click', (e) => {
    const b = e.target.closest('[data-reset]');
    if (b && confirm('Przywrócić wartość z pliku .env albo domyślną?')) save({ [b.dataset.reset]: '' });
  });

  // Wczytanie przy każdym wejściu na stronę ustawień (menu panelu wysyła page:change).
  document.addEventListener('page:change', (e) => { if (e.detail && e.detail.hash === '#panel-settings') load(); });
  if (location.hash === '#panel-settings') load();
  $id('settings-reload').addEventListener('click', load);
})();
