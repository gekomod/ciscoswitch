// Monitorowanie: logi switcha, czas i NTP, mirroring portów.
(function () {
  const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const $id = (id) => document.getElementById(id);
  const now = () => new Date().toLocaleTimeString('pl-PL');

  async function call(url, method, body) {
    const res = await fetch(url, { method: method || 'GET', headers: { 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined, cache: 'no-store' });
    const raw = await res.text();
    let d = {};
    try { d = raw ? JSON.parse(raw) : {}; } catch (_) { d = { error: raw }; }
    if (res.status === 401) { location.href = '/login'; throw new Error('Sesja wygasła.'); }
    if (!res.ok) throw new Error(d.error || 'Błąd ' + res.status);
    return d;
  }

  // ---------- Logi ----------
  (function logs() {
    const root = $id('switch-logs');
    if (!root) return;
    const list = $id('logs-list'), level = $id('logs-level'), search = $id('logs-search'), btn = $id('logs-refresh'), info = $id('logs-updated');
    const LEVEL = ['Awaria', 'Alarm', 'Krytyczny', 'Błąd', 'Ostrzeżenie', 'Uwaga', 'Info', 'Debug'];
    let entries = [];

    function render() {
      const max = Number(level.value);
      const q = search.value.trim().toLowerCase();
      const shown = entries.filter((e) => (max >= 7 || (e.level >= 0 && e.level <= max)) &&
        (!q || (e.message + ' ' + e.program + ' ' + e.time).toLowerCase().includes(q))).slice().reverse();
      if (!entries.length) { list.innerHTML = '<p class="mon-note">Kliknij „Odczytaj logi”.</p>'; return; }
      if (!shown.length) { list.innerHTML = '<p class="mon-note">Brak wpisów pasujących do filtra.</p>'; return; }
      list.innerHTML = '<div class="table-scroll"><table class="mon-table"><thead><tr><th>Czas</th><th>Ważność</th><th>Źródło</th><th>Komunikat</th></tr></thead><tbody>' +
        shown.slice(0, 500).map((e) => '<tr class="log-l' + e.level + '"><td class="mon-nowrap">' + esc(e.time) + '</td><td><span class="log-sev">' + esc(e.level >= 0 ? LEVEL[e.level] : '—') + '</span></td><td>' + esc(e.program) + '</td><td>' + esc(e.message) + '</td></tr>').join('') +
        '</tbody></table></div>' + (shown.length > 500 ? '<p class="mon-note">Pokazano 500 najnowszych z ' + shown.length + ' wpisów.</p>' : '');
    }

    async function refresh() {
      btn.disabled = true;
      try {
        const d = await call('/api/logs');
        entries = d.entries || [];
        info.textContent = 'Odczytano o ' + now() + ', wpisów: ' + entries.length;
        render();
      } catch (e) {
        list.innerHTML = '<p class="mon-error">Nie udało się odczytać logów: ' + esc(e.message) + '</p>';
      } finally { btn.disabled = false; }
    }
    btn.addEventListener('click', refresh);
    level.addEventListener('change', render);
    search.addEventListener('input', render);
  })();

  // ---------- Czas i NTP ----------
  (function time() {
    const root = $id('time-ntp');
    if (!root) return;
    const btn = $id('time-refresh'), box = $id('time-state'), servers = $id('ntp-servers'), info = $id('time-updated');
    let data = null;

    function render() {
      box.innerHTML = '<dl class="files-boot">' +
        '<div><dt>Zegar switcha</dt><dd>' + esc(data.clock || 'Brak danych') + '</dd></div>' +
        '<div><dt>Strefa czasowa</dt><dd>' + esc(data.timezone || 'UTC (nie ustawiono)') + '</dd></div>' +
        '<div><dt>Czas letni</dt><dd>' + esc(data.summerTime || 'Nie ustawiono') + '</dd></div>' +
        '</dl>' + (data.ntpStatus ? '<details class="mon-details"><summary>Stan synchronizacji NTP</summary><pre class="files-raw">' + esc(data.ntpStatus) + '</pre></details>' : '');
      servers.innerHTML = data.ntpServers.length
        ? '<ul class="mon-list">' + data.ntpServers.map((s) => '<li><span>' + esc(s) + '</span><button class="btn" data-ntp-remove="' + esc(s) + '">Usuń</button></li>').join('') + '</ul>'
        : '<p class="mon-note">Brak serwerów NTP. Zegar switcha nie jest synchronizowany, więc czasy w logach mogą być błędne.</p>';
    }

    async function refresh() {
      btn.disabled = true;
      try {
        data = await call('/api/time');
        info.textContent = 'Odczytano o ' + now();
        render();
      } catch (e) {
        box.innerHTML = '<p class="mon-error">Nie udało się odczytać czasu: ' + esc(e.message) + '</p>';
      } finally { btn.disabled = false; }
    }

    async function change(body, question) {
      if (!confirm(question + '\n\nPrzed zmianą panel zapisze kopię konfiguracji.')) return false;
      try { await call('/api/time', 'POST', body); await refresh(); return true; } catch (e) { alert('Nie udało się: ' + e.message); return false; }
    }

    servers.addEventListener('click', (e) => {
      const b = e.target.closest('[data-ntp-remove]');
      if (b) change({ action: 'ntp-remove', host: b.dataset.ntpRemove }, 'Usunąć serwer NTP ' + b.dataset.ntpRemove + '?');
    });
    $id('ntp-add-form').addEventListener('submit', async (e) => {
      e.preventDefault();
      const host = $id('ntp-host').value.trim();
      if (!host) return;
      if (await change({ action: 'ntp-add', host }, 'Dodać serwer NTP ' + host + '?')) $id('ntp-host').value = '';
    });
    $id('tz-form').addEventListener('submit', (e) => {
      e.preventDefault();
      const name = $id('tz-name').value.trim(), offset = Number($id('tz-offset').value);
      change({ action: 'timezone', name, offset }, 'Ustawić strefę ' + name + ' (UTC' + (offset >= 0 ? '+' : '') + offset + ')?');
    });
    $id('summer-eu').addEventListener('click', () => change({ action: 'summer-eu', name: 'CEST' }, 'Ustawić czas letni według reguły UE (CEST, +1 h od ostatniej niedzieli marca do ostatniej niedzieli października)?'));
    $id('summer-remove').addEventListener('click', () => change({ action: 'summer-remove' }, 'Usunąć ustawienie czasu letniego?'));
    btn.addEventListener('click', refresh);
  })();

  // ---------- Mirroring ----------
  (function mirror() {
    const root = $id('mirroring');
    if (!root) return;
    const btn = $id('mirror-refresh'), list = $id('mirror-list'), info = $id('mirror-updated');
    const DIR = { both: 'oba kierunki', receive: 'tylko odbierany', transmit: 'tylko wysyłany' };
    const options = Array.from({ length: 52 }, (_, i) => '<option value="port1.0.' + (i + 1) + '">Port ' + (i + 1) + '</option>').join('');
    $id('mirror-source').innerHTML = options;
    $id('mirror-dest').innerHTML = options;
    $id('mirror-dest').value = 'port1.0.48';

    async function refresh() {
      btn.disabled = true;
      try {
        const d = await call('/api/mirror');
        const s = d.sessions || [];
        info.textContent = 'Odczytano o ' + now();
        list.innerHTML = s.length
          ? '<ul class="mon-list">' + s.map((m) => '<li><span>Ruch z <b>' + esc(m.source) + '</b> (' + esc(DIR[m.direction] || m.direction) + ') kopiowany na <b>' + esc(m.destination) + '</b></span><button class="btn" data-mirror-dest="' + esc(m.destination) + '" data-mirror-src="' + esc(m.source) + '">Zatrzymaj</button></li>').join('') + '</ul>'
          : '<p class="mon-note">Mirroring nie jest skonfigurowany.</p>';
      } catch (e) {
        list.innerHTML = '<p class="mon-error">Nie udało się odczytać mirroringu: ' + esc(e.message) + '</p>';
      } finally { btn.disabled = false; }
    }

    async function change(body, question) {
      if (!confirm(question + '\n\nPrzed zmianą panel zapisze kopię konfiguracji.')) return;
      try { await call('/api/mirror', 'POST', body); await refresh(); } catch (e) { alert('Nie udało się: ' + e.message); }
    }

    list.addEventListener('click', (e) => {
      const b = e.target.closest('[data-mirror-dest]');
      if (b) change({ destination: b.dataset.mirrorDest, source: b.dataset.mirrorSrc, remove: true }, 'Zatrzymać kopiowanie ruchu z ' + b.dataset.mirrorSrc + ' na ' + b.dataset.mirrorDest + '?');
    });
    $id('mirror-form').addEventListener('submit', (e) => {
      e.preventDefault();
      const source = $id('mirror-source').value, destination = $id('mirror-dest').value, direction = $id('mirror-direction').value;
      change({ source, destination, direction }, 'Kopiować ruch z ' + source + ' na ' + destination + '?\n\nUwaga: ' + destination + ' przestanie działać jak zwykły port. Urządzenie podłączone do niego straci normalną łączność; podłącz tam komputer z Wiresharkiem.');
    });
    btn.addEventListener('click', refresh);
  })();
})();
