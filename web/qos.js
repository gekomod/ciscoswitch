// QoS: włączenie i priorytet ruchu z portów (CoS dla ruchu bez znacznika).
(function () {
  const root = document.getElementById('qos');
  if (!root) return;
  const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const $id = (id) => document.getElementById(id);
  const LEVEL = { 1: 'Tło (najniższy)', 0: 'Zwykły (domyślny)', 2: 'Niski', 3: 'Ważny', 4: 'Wideo', 5: 'Głos', 6: 'Sterowanie siecią', 7: 'Sterowanie siecią' };
  const btn = $id('qos-refresh'), info = $id('qos-updated');
  let data = null;

  async function call(url, method, body) {
    const res = await fetch(url, { method: method || 'GET', headers: { 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined, cache: 'no-store' });
    const raw = await res.text();
    let d = {};
    try { d = raw ? JSON.parse(raw) : {}; } catch (_) { d = { error: raw }; }
    if (res.status === 401) { location.href = '/login'; throw new Error('Sesja wygasła.'); }
    if (!res.ok) throw new Error(d.error || 'Błąd ' + res.status);
    return d;
  }

  function render() {
    const groups = new Map();
    for (const p of data.ports) {
      if (!groups.has(p.cos)) groups.set(p.cos, []);
      groups.get(p.cos).push(p.port.replace('port1.0.', ''));
    }
    $id('qos-state').innerHTML =
      '<div class="trf-global"><span class="svc-state ' + (data.enabled ? 'trf-on' : '') + '">' + (data.enabled ? 'QoS włączony' : 'QoS wyłączony') + '</span>' +
      '<button class="btn" id="qos-toggle">' + (data.enabled ? 'Wyłącz QoS' : 'Włącz QoS') + '</button></div>' +
      (groups.size
        ? '<ul class="mon-list">' + [...groups.entries()].sort((a, b) => b[0] - a[0]).map(([cos, ports]) => '<li><span>Porty ' + esc(ports.join(', ')) + ': <b>' + esc(LEVEL[cos] || 'CoS ' + cos) + '</b> <small>(CoS ' + cos + ')</small></span></li>').join('') + '</ul>'
        : '<p class="mon-note">Żaden port nie ma ustawionego priorytetu; cały ruch bez znacznika jest traktowany jednakowo.</p>') +
      (!data.enabled && groups.size ? '<p class="mon-error">Priorytety portów działają dopiero po włączeniu QoS.</p>' : '') +
      (data.raw ? '<details class="mon-details"><summary>Szczegóły ze switcha (show mls qos)</summary><pre class="files-raw">' + esc(data.raw) + '</pre></details>' : '');
  }

  async function refresh() {
    btn.disabled = true;
    try {
      data = await call('/api/qos');
      info.textContent = 'Odczytano o ' + new Date().toLocaleTimeString('pl-PL');
      render();
    } catch (e) {
      $id('qos-state').innerHTML = '<p class="mon-error">Nie udało się odczytać QoS: ' + esc(e.message) + '</p>';
    } finally { btn.disabled = false; }
  }

  async function change(body, question) {
    if (!confirm(question + '\n\nPrzed zmianą panel zapisze kopię konfiguracji.')) return;
    try { await call('/api/traffic', 'POST', body); await refresh(); } catch (e) { alert('Nie udało się: ' + e.message); }
  }

  root.addEventListener('click', (e) => {
    if (e.target.id !== 'qos-toggle') return;
    const enable = !data.enabled;
    change({ action: 'qos-global', enable }, enable
      ? 'Włączyć QoS na switchu?\n\nSwitch zacznie kolejkować ruch według priorytetów. W małej sieci zwykle nic nie zauważysz, dopóki łącze nie jest zapchane.'
      : 'Wyłączyć QoS?\n\nPriorytety portów przestaną działać; cały ruch będzie traktowany jednakowo.');
  });

  $id('qos-form').addEventListener('submit', (e) => {
    e.preventDefault();
    const remove = e.submitter && e.submitter.dataset.remove === 'true';
    const ports = $id('qos-ports').value.trim(), level = Number($id('qos-level').value);
    change({ action: 'qos-port', ports, level, remove }, remove
      ? 'Przywrócić domyślny priorytet na portach ' + ports + '?'
      : 'Ustawić priorytet „' + LEVEL[level] + '” dla ruchu z portów ' + ports + '?');
  });

  btn.addEventListener('click', refresh);
})();
