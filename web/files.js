// Pliki i firmware: zawartość flash, ustawienia startu, wybór firmware
// i konfiguracji startowej, zapis bieżącej konfiguracji do nowego pliku.
(function () {
  const root = document.getElementById('files');
  if (!root) return;
  const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const $id = (id) => document.getElementById(id);
  const refreshBtn = $id('files-refresh');
  const bootBox = $id('files-boot');
  const listBox = $id('files-list');
  const updated = $id('files-updated');
  const saveForm = $id('files-save-form');
  let info = null;

  function size(v) {
    v = Number(v) || 0;
    const u = ['B', 'KB', 'MB', 'GB'];
    let i = 0;
    while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
    return v.toLocaleString('pl-PL', { maximumFractionDigits: i ? 1 : 0 }) + ' ' + u[i];
  }

  async function call(method, body) {
    const res = await fetch('/api/files', { method, headers: { 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined, cache: 'no-store' });
    const raw = await res.text();
    let d = {};
    try { d = raw ? JSON.parse(raw) : {}; } catch (_) { d = { error: raw }; }
    if (res.status === 401) { location.href = '/login'; throw new Error('Sesja wygasła.'); }
    if (!res.ok) throw new Error(d.error || 'Błąd ' + res.status);
    return d;
  }

  function inBoot(f) {
    return (info.boot || []).filter((b) => b.value.includes(f.base)).map((b) => b.key);
  }

  function render() {
    const boot = info.boot || [];
    bootBox.innerHTML = boot.length
      ? '<dl class="files-boot">' + boot.map((b) => '<div><dt>' + esc(b.key) + '</dt><dd>' + esc(b.value) + '</dd></div>').join('') + '</dl>'
      : '<pre class="files-raw">' + esc(info.rawBoot || 'Brak danych') + '</pre>';

    const files = info.files || [];
    if (!files.length) {
      listBox.innerHTML = '<p class="files-note">Nie udało się rozpoznać listy plików. Poniżej surowa odpowiedź switcha; prześlij ją, aby dopasować odczyt.</p><pre class="files-raw">' + esc(info.rawDir || '') + '</pre>';
      return;
    }
    const kindLabel = { firmware: 'Firmware', config: 'Konfiguracja', other: 'Inny' };
    listBox.innerHTML = '<div class="table-scroll"><table class="files-table"><thead><tr><th>Plik</th><th>Rodzaj</th><th>Rozmiar</th><th>Data</th><th><span class="sr-only">Akcje</span></th></tr></thead><tbody>' +
      files.map((f) => {
        const used = inBoot(f);
        let actions = '';
        if (f.kind === 'firmware') {
          actions = '<button class="btn" data-file-action="boot-image" data-name="' + esc(f.name) + '">Uruchamiaj ten firmware</button>' +
            '<button class="btn" data-file-action="backup-boot" data-name="' + esc(f.name) + '">Ustaw jako zapasowy</button>';
        } else if (f.kind === 'config') {
          actions = '<button class="btn" data-file-action="boot-config" data-name="' + esc(f.name) + '">Używaj przy starcie</button>';
        }
        return '<tr><td><b>' + esc(f.base) + '</b>' + (used.length ? '<small class="files-used">' + esc(used.join(', ')) + '</small>' : '') + '</td>' +
          '<td>' + esc(kindLabel[f.kind]) + '</td><td>' + esc(size(f.size)) + '</td><td>' + esc(f.date) + '</td>' +
          '<td class="files-actions">' + actions + '</td></tr>';
      }).join('') + '</tbody></table></div>';
  }

  async function refresh() {
    refreshBtn.disabled = true;
    if (!info) listBox.innerHTML = '<p class="files-note">Odczytuję zawartość pamięci flash…</p>';
    try {
      info = await call('GET');
      render();
      updated.textContent = 'Odczytano o ' + new Date().toLocaleTimeString('pl-PL');
    } catch (e) {
      listBox.innerHTML = '<p class="files-error">Nie udało się odczytać plików: ' + esc(e.message) + '</p>';
    } finally {
      refreshBtn.disabled = false;
    }
  }

  const describe = {
    'boot-image': (n) => 'Ustawić ' + n + ' jako firmware uruchamiany przy następnym starcie?\n\nZmiana zadziała dopiero po restarcie switcha. Upewnij się, że to właściwy plik dla AT-GS948MX; zły firmware może uniemożliwić start. Warto mieć ustawiony firmware zapasowy.',
    'backup-boot': (n) => 'Ustawić ' + n + ' jako firmware zapasowy?\n\nSwitch użyje go, jeśli główny firmware nie uruchomi się.',
    'boot-config': (n) => 'Ustawić ' + n + ' jako konfigurację wczytywaną przy starcie?\n\nPo restarcie switch uruchomi się z tą konfiguracją zamiast obecnej. Jeśli ten plik nie zawiera adresu zarządzającego i SSH, panel straci połączenie.',
  };

  listBox.addEventListener('click', async (e) => {
    const b = e.target.closest('[data-file-action]');
    if (!b) return;
    const action = b.dataset.fileAction, name = b.dataset.name;
    if (!confirm(describe[action](name.replace(/^.*[:/]/, '')) + '\n\nPrzed zmianą panel zapisze kopię konfiguracji.')) return;
    b.disabled = true;
    try {
      await call('POST', { action, name });
      await refresh();
    } catch (err) {
      alert('Nie udało się: ' + err.message);
      b.disabled = false;
    }
  });

  saveForm.addEventListener('submit', async (e) => {
    e.preventDefault();
    const input = $id('files-save-name');
    const name = input.value.trim();
    if (!name) { input.focus(); return; }
    const btn = saveForm.querySelector('button');
    btn.disabled = true;
    try {
      await call('POST', { action: 'save-as', name });
      input.value = '';
      await refresh();
    } catch (err) {
      alert('Nie udało się zapisać pliku: ' + err.message);
    } finally {
      btn.disabled = false;
    }
  });

  refreshBtn.addEventListener('click', refresh);

  // ---------- Wgrywanie firmware ----------
  const fwConfig = $id('fw-config'), fwFiles = $id('fw-files'), fwJob = $id('fw-job');
  let fwPoll = null;

  async function fwRefresh() {
    try {
      const res = await fetch('/api/firmware', { cache: 'no-store' });
      if (!res.ok) return;
      const d = await res.json();
      fwConfig.innerHTML = d.configError
        ? '<p class="mon-error">Kopiowanie do switcha wymaga ustawienia w .env: FIRMWARE_HTTP_ADDR=IP_komputera_z_panelem:8091 (adres, pod którym switch widzi ten komputer), a potem restartu panelu. ' + esc(d.configError) + '</p>'
        : '<p class="mon-note">Switch pobierze plik z ' + esc(d.address) + ' (serwer działa tylko w trakcie kopiowania).</p>';
      fwFiles.innerHTML = d.files.length
        ? '<ul class="mon-list">' + d.files.map((f) => '<li><span><b>' + esc(f.name) + '</b> ' + esc(size(f.size)) + '<small class="fw-sum">SHA-256: ' + esc(f.sha256 || '—') + '</small></span><span class="files-actions"><button class="btn" data-fw-install="' + esc(f.name) + '"' + (d.configError ? ' disabled' : '') + '>Kopiuj do switcha</button><button class="btn" data-fw-delete="' + esc(f.name) + '">Usuń z panelu</button></span></li>').join('') + '</ul>'
        : '<p class="mon-note">W panelu nie ma plików firmware.</p>';
      const j = d.job;
      if (j) {
        const cls = j.state === 'error' ? 'mon-error' : j.state === 'done' ? 'fw-done' : 'mon-note';
        fwJob.innerHTML = '<p class="' + cls + '"><b>' + esc(j.name) + ':</b> ' + esc(j.message) + (j.warning ? '<br>Uwaga: ' + esc(j.warning) : '') + ' <small>(start ' + esc(j.started) + (j.finished ? ', koniec ' + esc(j.finished) : '') + ')</small></p>';
      } else fwJob.innerHTML = '';
      clearTimeout(fwPoll);
      if (j && j.state === 'running') fwPoll = setTimeout(fwRefresh, 3000);
      else if (j && j.state === 'done' && info) refresh();
    } catch (_) { /* brak połączenia: następna próba przy kolejnym kliknięciu */ }
  }

  $id('fw-upload-form').addEventListener('submit', (e) => {
    e.preventDefault();
    const file = $id('fw-file').files[0];
    if (!file) return;
    if (!/\.rel$/i.test(file.name)) { alert('Wybierz plik firmware z rozszerzeniem .rel.'); return; }
    const bar = $id('fw-progress'), btn = e.target.querySelector('button');
    const fd = new FormData();
    fd.append('file', file);
    const xhr = new XMLHttpRequest();
    xhr.open('POST', '/api/firmware/upload');
    xhr.upload.onprogress = (ev) => { if (ev.lengthComputable) bar.value = Math.round(ev.loaded / ev.total * 100); };
    xhr.onloadend = () => {
      bar.hidden = true; btn.disabled = false;
      let d = {};
      try { d = JSON.parse(xhr.responseText || '{}'); } catch (_) {}
      if (xhr.status === 401) { location.href = '/login'; return; }
      if (xhr.status !== 200) alert('Wysyłanie nie powiodło się: ' + (d.error || 'błąd ' + xhr.status));
      else $id('fw-file').value = '';
      fwRefresh();
    };
    bar.value = 0; bar.hidden = false; btn.disabled = true;
    xhr.send(fd);
  });

  fwFiles.addEventListener('click', async (e) => {
    const inst = e.target.closest('[data-fw-install]'), del = e.target.closest('[data-fw-delete]');
    const name = inst ? inst.dataset.fwInstall : del ? del.dataset.fwDelete : '';
    if (!name) return;
    const q = inst
      ? 'Skopiować ' + name + ' do pamięci flash switcha?\n\nTo potrwa kilka minut. Panel sprawdzi wolne miejsce i rozmiar pliku po skopiowaniu. Firmware startowy się nie zmieni.'
      : 'Usunąć ' + name + ' z panelu? Plik na switchu (jeśli został skopiowany) zostanie.';
    if (!confirm(q)) return;
    try {
      const res = await fetch(inst ? '/api/firmware/install' : '/api/firmware/delete', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name }) });
      const d = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(d.error || 'Błąd ' + res.status);
    } catch (err) { alert('Nie udało się: ' + err.message); }
    fwRefresh();
  });

  refreshBtn.addEventListener('click', fwRefresh);
  fwRefresh();
})();
