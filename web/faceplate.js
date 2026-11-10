// Front switcha na przeglądzie: 48 portów miedzianych i 4 SFP, kolor wg stanu,
// po kliknięciu panel szczegółów z akcjami, które panel już ma (ustawienia,
// monitoring, topologia, włączenie/wyłączenie). Dane pochodzą z /api/status
// (odświeżanego przez app.js) i z /api/port/switchport dla wybranego portu.
(function () {
  const COPPER = 48, TOTAL = 52;
  const ERR_MEMORY_MS = 5 * 60 * 1000;
  const anchor = document.getElementById('overview');
  if (!anchor) return;

  const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const portName = (n) => 'port1.0.' + n;
  const prevErrors = new Map();   // nazwa -> suma błędów z poprzedniego odczytu
  const errorSeen = new Map();    // nazwa -> czas ostatniego przyrostu błędów
  let protectedPorts = new Set();
  let selected = null;
  let switchportCache = new Map();

  const section = document.createElement('section');
  section.className = 'panel faceplate-panel';
  section.id = 'faceplate';
  section.setAttribute('aria-label', 'Front switcha');
  section.innerHTML =
    '<div class="fp-head"><h2>Porty</h2><div class="fp-legend">' +
      '<span><i class="fp-key fp-k-g"></i>1 Gb/s</span>' +
      '<span><i class="fp-key fp-k-x"></i>10 Gb/s</span>' +
      '<span><i class="fp-key fp-k-f"></i>10/100 Mb/s</span>' +
      '<span><i class="fp-key fp-k-down"></i>Brak łącza</span>' +
      '<span><i class="fp-key fp-k-off"></i>Wyłączony</span>' +
      '<span><i class="fp-key-dot"></i>Rosną błędy</span>' +
    '</div></div>' +
    '<div class="fp-scroll"><div class="fp-device" id="fp-device"></div></div>' +
    '<p class="fp-hint" id="fp-hint">Wczytuję stan portów…</p>' +
    '<div class="fp-detail" id="fp-detail" hidden></div>' +
    '<div class="fp-events"><h3>Ostatnie zdarzenia</h3><div id="fp-events"><p class="fp-note">Wczytuję…</p></div></div>';
  anchor.insertAdjacentElement('afterend', section);

  const device = section.querySelector('#fp-device');
  const detail = section.querySelector('#fp-detail');
  const hint = section.querySelector('#fp-hint');

  function ports() {
    const map = new Map();
    for (const p of (typeof latestPorts !== 'undefined' ? latestPorts : [])) map.set(p.name, p);
    return map;
  }

  function speedMb(p) {
    const m = String(p && p.speed || '').match(/(\d+)/);
    return m ? Number(m[1]) : 0;
  }

  function kind(p) {
    if (!p) return 'unknown';
    if (p.admin === 'down') return 'off';
    if (p.oper !== 'up') return 'down';
    const s = speedMb(p);
    if (s >= 10000) return 'x';
    if (s >= 1000) return 'g';
    return 'f';
  }

  function trackErrors(map) {
    const now = Date.now();
    for (const [name, p] of map) {
      const sum = (p.inErrors || 0) + (p.outErrors || 0);
      if (prevErrors.has(name) && sum > prevErrors.get(name)) errorSeen.set(name, now);
      prevErrors.set(name, sum);
    }
  }

  function hasRecentErrors(name) {
    const t = errorSeen.get(name);
    return !!t && Date.now() - t < ERR_MEMORY_MS;
  }

  function stateLabel(k) {
    return { x: 'aktywny, 10 Gb/s', g: 'aktywny, 1 Gb/s', f: 'aktywny, 10/100 Mb/s', down: 'brak łącza', off: 'wyłączony', unknown: 'brak danych' }[k];
  }

  function portButton(n, map) {
    const name = portName(n), p = map.get(name), k = kind(p);
    const err = hasRecentErrors(name);
    const desc = p && p.description ? ', ' + p.description : '';
    const label = 'Port ' + n + desc + ', ' + stateLabel(k) + (err ? ', rosną błędy' : '');
    return '<button type="button" class="fp-port fp-' + k + (n > COPPER ? ' fp-sfp' : '') + (selected === name ? ' is-selected' : '') + '" data-port="' + name + '" aria-pressed="' + (selected === name) + '" aria-label="' + esc(label) + '" title="' + esc(label) + '">' +
      '<span class="fp-led"></span><span class="fp-num">' + n + '</span>' + (err ? '<span class="fp-err"></span>' : '') + '</button>';
  }

  function render() {
    const map = ports();
    if (!map.size) {
      hint.textContent = 'Brak danych o portach. Sprawdź połączenie SNMP w nagłówku panelu.';
      return;
    }
    let html = '<div class="fp-brand"><b>Allied Telesis</b><small>AT-GS948MX</small></div>';
    for (let b = 0; b < 4; b++) {
      html += '<div class="fp-block">';
      for (let i = 0; i < 6; i++) html += portButton(b * 12 + i * 2 + 1, map);
      for (let i = 0; i < 6; i++) html += portButton(b * 12 + i * 2 + 2, map);
      html += '</div>';
    }
    html += '<div class="fp-sfp-block"><div class="fp-block fp-block-sfp">' + [49, 51, 50, 52].map((n) => portButton(n, map)).join('') + '</div><small>SFP</small></div>';
    device.innerHTML = html;
    const up = [...Array(TOTAL)].filter((_, i) => { const p = map.get(portName(i + 1)); return p && p.oper === 'up'; }).length;
    hint.textContent = 'Aktywne: ' + up + ' z ' + TOTAL + '. Kliknij port, aby zobaczyć szczegóły. Górny rząd to porty nieparzyste, dolny parzyste.';
    if (selected) renderDetail();
  }

  function bytes(v) {
    v = Number(v) || 0;
    const u = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
    let i = 0;
    while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
    return v.toLocaleString('pl-PL', { maximumFractionDigits: i ? 1 : 0 }) + ' ' + u[i];
  }

  // ---------- Wykres ruchu z historii monitoringu (portTrafficHistory z app.js, b/s) ----------
  function trafficChart(name) {
    const hist = (typeof portTrafficHistory !== 'undefined' && portTrafficHistory.get(name)) || [];
    const pts = hist.slice(-40);
    if (pts.length < 2) {
      return '<div class="fp-chart"><div class="fp-chart-head"><span>Ruch</span></div><p class="fp-note">Wykres pojawi się po kilku odczytach monitoringu ruchu.</p></div>';
    }
    const max = Math.max(1, ...pts.map((x) => Math.max(x.rx || 0, x.tx || 0)));
    const W = 300, H = 70;
    const line = (key) => pts.map((x, i) => (i * W / (pts.length - 1)).toFixed(1) + ',' + (H - 3 - ((x[key] || 0) / max) * (H - 8)).toFixed(1)).join(' ');
    const last = pts[pts.length - 1];
    const fmt = typeof formatRate === 'function' ? formatRate : (v) => Math.round(v) + ' b/s';
    const minutes = Math.max(1, Math.round((pts[pts.length - 1].at - pts[0].at) / 60000));
    return '<div class="fp-chart"><div class="fp-chart-head"><span>Ruch, ostatnie ' + minutes + ' min</span><span>szczyt ' + esc(fmt(max)) + '</span></div>' +
      '<svg viewBox="0 0 ' + W + ' ' + H + '" preserveAspectRatio="none" role="img" aria-label="Wykres ruchu portu: odbiór ' + esc(fmt(last.rx || 0)) + ', wysyłanie ' + esc(fmt(last.tx || 0)) + '">' +
      '<polyline class="fp-rx" points="' + line('rx') + '"></polyline><polyline class="fp-tx" points="' + line('tx') + '"></polyline></svg>' +
      '<div class="fp-chart-legend"><span><i class="fp-rx-key"></i>Odbiór ' + esc(fmt(last.rx || 0)) + '</span><span><i class="fp-tx-key"></i>Wysyłanie ' + esc(fmt(last.tx || 0)) + '</span></div></div>';
  }

  // ---------- Podłączone urządzenia: FDB, ARP, LLDP (te same polecenia co topologia) ----------
  let devCache = null, devLoading = false;
  const DEV_TTL = 60000;

  function deviceBox(name, p) {
    if (!p || p.oper !== 'up') return '<div class="fp-device-box"><b>Podłączone urządzenie</b><p class="fp-note">Brak łącza na tym porcie.</p></div>';
    if (!devCache || Date.now() - devCache.at > DEV_TTL) {
      return '<div class="fp-device-box"><b>Podłączone urządzenie</b><p class="fp-note">' + (devLoading ? 'Odczytuję tablice MAC, ARP i LLDP…' : 'Adresy MAC, IP i nazwa z LLDP.') + '</p>' +
        (devLoading ? '' : '<button type="button" class="btn" data-fp="devices">Sprawdź podłączone urządzenia</button>') + '</div>';
    }
    const macs = (devCache.fdb || []).filter((r) => r.port === name);
    const lldp = devCache.lldp && devCache.lldp.get(name);
    const rows = macs.slice(0, 6).map((r) => {
      const ips = (devCache.arp && devCache.arp.get(r.mac)) || [];
      return '<li><code>' + esc(r.mac) + '</code>' + (ips.length ? ' <span>' + esc(ips.join(', ')) + '</span>' : '') + (r.vlan ? ' <small>VLAN ' + esc(r.vlan) + '</small>' : '') + '</li>';
    }).join('');
    return '<div class="fp-device-box"><b>Podłączone urządzenie</b>' +
      (lldp && lldp.length ? '<p class="fp-lldp">LLDP: ' + esc(lldp[0]) + '</p>' : '') +
      (macs.length ? '<ul class="fp-macs">' + rows + '</ul>' + (macs.length > 6 ? '<p class="fp-note">i ' + (macs.length - 6) + ' więcej adresów (za portem jest pewnie switch albo punkt dostępowy).</p>' : '') : '<p class="fp-note">Switch nie zna jeszcze żadnego adresu MAC na tym porcie.</p>') +
      '<p class="fp-note">Odczyt z ' + new Date(devCache.at).toLocaleTimeString('pl-PL') + '. <button type="button" class="fp-link" data-fp="devices">Odśwież</button></p></div>';
  }

  async function loadDevices() {
    if (devLoading) return;
    devLoading = true;
    renderDetail();
    const run = async (command) => {
      try {
        const res = await fetch('/api/command', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ command }) });
        const d = await res.json().catch(() => ({}));
        return res.ok ? d.output || '' : '';
      } catch (_) { return ''; }
    };
    const [fdb, arp, lldp] = await Promise.all([run('show mac address-table'), run('show arp'), run('show lldp neighbors')]);
    devCache = {
      at: Date.now(),
      fdb: typeof parseFDB === 'function' ? parseFDB(fdb) : [],
      arp: typeof parseARP === 'function' ? parseARP(arp) : new Map(),
      lldp: typeof parseLLDP === 'function' ? parseLLDP(lldp) : new Map(),
    };
    devLoading = false;
    if (selected) renderDetail();
  }

  // Polska odmiana: przybył 1 błąd, przybyły 2–4 błędy, przybyło 5+ błędów (12–14 też „błędów”).
  function errorsPhrase(n) {
    const t = n % 100, u = n % 10;
    if (n === 1) return 'przybył 1 błąd';
    if (u >= 2 && u <= 4 && (t < 12 || t > 14)) return 'przybyły ' + n + ' błędy';
    return 'przybyło ' + n + ' błędów';
  }

  // ---------- Ostatnie zdarzenia z historii monitoringu ----------
  let eventsAt = 0;
  async function loadEvents() {
    if (Date.now() - eventsAt < 55000) return;
    eventsAt = Date.now();
    const box = section.querySelector('#fp-events');
    try {
      const res = await fetch('/api/monitor/history', { cache: 'no-store' });
      if (!res.ok) throw new Error('HTTP ' + res.status);
      const d = await res.json();
      const items = [];
      for (const e of d.events || []) items.push({ t: e.timestamp, kind: e.type === 'success' ? 'ok' : e.type === 'error' ? 'bad' : 'info', text: e.message });
      for (const e of d.portEvents || []) items.push({ t: e.timestamp, kind: e.state === 'up' ? 'ok' : 'warn', text: (e.message || (e.port + ': łącze ' + (e.state === 'up' ? 'w górę' : 'w dół'))) });
      for (const e of d.counterEvents || []) {
        const n = (e.errorRxDelta || 0) + (e.errorTxDelta || 0);
        items.push({ t: e.timestamp, kind: 'bad', text: e.message || (e.port + ': ' + errorsPhrase(n)) });
      }
      items.sort((a, b) => Date.parse(b.t) - Date.parse(a.t));
      box.innerHTML = items.length
        ? '<ul class="fp-event-list">' + items.slice(0, 8).map((x) => '<li class="fp-ev-' + x.kind + '"><time>' + esc(new Date(x.t).toLocaleString('pl-PL', { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit' })) + '</time><span>' + esc(x.text) + '</span></li>').join('') + '</ul>'
        : '<p class="fp-note">Brak zdarzeń. Panel zapisuje zmiany łączy, błędy i dostępność switcha.</p>';
    } catch (e) {
      box.innerHTML = '<p class="fp-note">Nie udało się wczytać zdarzeń.</p>';
    }
  }

  function renderDetail() {
    const p = ports().get(selected);
    const n = selected.split('.').pop();
    const k = kind(p);
    const sp = switchportCache.get(selected);
    const isProtected = protectedPorts.has(selected);
    const vlanText = !sp ? 'Odczytuję…' : sp.error ? 'Brak danych' :
      (sp.mode === 'trunk' ? (sp.configuredVlans && sp.configuredVlans.length ? sp.configuredVlans.join(', ') : 'brak') + (sp.defaultVlan ? ' (natywny ' + sp.defaultVlan + ')' : '') : (sp.defaultVlan || (sp.configuredVlans || []).join(', ') || '—'));
    const modeText = !sp ? 'Odczytuję…' : sp.error ? 'Brak danych' : (sp.mode ? sp.mode.charAt(0).toUpperCase() + sp.mode.slice(1) : '—');
    const errs = p ? (p.inErrors || 0) + (p.outErrors || 0) : 0;
    const drops = p ? (p.inDiscards || 0) + (p.outDiscards || 0) : 0;
    detail.hidden = false;
    detail.innerHTML =
      '<div class="fp-detail-head"><div><small>' + esc(selected) + '</small><h3>Port ' + esc(n) + '</h3>' + (p && p.description ? '<p class="fp-desc">' + esc(p.description) + '</p>' : '') + '</div>' +
        '<span class="fp-badge fp-badge-' + k + '">' + esc(stateLabel(k)) + '</span></div>' +
      '<dl class="fp-facts">' +
        '<div><dt>Tryb</dt><dd>' + esc(modeText) + '</dd></div>' +
        '<div><dt>VLAN</dt><dd>' + esc(vlanText) + '</dd></div>' +
        '<div><dt>Prędkość</dt><dd>' + esc(p && p.oper === 'up' ? p.speed || '—' : '—') + '</dd></div>' +
        '<div><dt>MTU</dt><dd>' + esc(p ? p.mtu || '—' : '—') + '</dd></div>' +
        '<div><dt>Odebrano</dt><dd>' + esc(bytes(p && p.inOctets)) + '</dd></div>' +
        '<div><dt>Wysłano</dt><dd>' + esc(bytes(p && p.outOctets)) + '</dd></div>' +
        '<div><dt>Błędy od startu</dt><dd class="' + (hasRecentErrors(selected) ? 'fp-bad' : '') + '">' + esc(errs.toLocaleString('pl-PL')) + (hasRecentErrors(selected) ? ', rosną' : '') + '</dd></div>' +
        '<div><dt>Odrzucone</dt><dd>' + esc(drops.toLocaleString('pl-PL')) + '</dd></div>' +
      '</dl>' +
      trafficChart(selected) +
      deviceBox(selected, p) +
      '<div class="fp-actions">' +
        '<button type="button" class="btn primary" data-fp="settings">Edytuj ustawienia portu</button>' +
        '<button type="button" class="btn" data-fp="monitor">Ruch i historia</button>' +
        '<button type="button" class="btn" data-fp="topology">Pokaż w topologii</button>' +
        (k === 'off'
          ? '<button type="button" class="btn" data-fp="enable">Włącz port</button>'
          : '<button type="button" class="btn fp-danger" data-fp="disable"' + (isProtected ? ' disabled' : '') + '>Wyłącz port</button>') +
      '</div>' +
      (isProtected ? '<p class="fp-note">Port chroniony (PROTECTED_PORTS): panel nie pozwala go zmieniać.</p>' : '') +
      '<p class="fp-note">Liczniki od uruchomienia switcha. „Rosną błędy” oznacza przyrost w ostatnich 5 minutach.</p>';
  }

  async function loadSwitchport(name) {
    if (switchportCache.has(name)) return;
    switchportCache.set(name, null);
    try {
      const res = await fetch('/api/port/switchport?port=' + encodeURIComponent(name), { cache: 'no-store' });
      const d = await res.json().catch(() => ({}));
      switchportCache.set(name, res.ok ? d : { error: true });
    } catch (_) {
      switchportCache.set(name, { error: true });
    }
    if (selected === name) renderDetail();
  }

  async function setPortState(name, action) {
    if (!confirm((action === 'disable' ? 'Wyłączyć' : 'Włączyć') + ' ' + name + '?' + (action === 'disable' ? '\n\nUrządzenie podłączone do tego portu straci łączność.' : ''))) return;
    try {
      const res = await fetch('/api/port', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ port: name, action }) });
      const d = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(d.error || 'Błąd ' + res.status);
      if (typeof load === 'function') await load();
    } catch (e) {
      alert('Nie udało się zmienić stanu portu: ' + e.message);
    }
  }

  device.addEventListener('click', (e) => {
    const b = e.target.closest('[data-port]');
    if (!b) return;
    selected = b.dataset.port;
    render();
    renderDetail();
    loadSwitchport(selected);
  });

  detail.addEventListener('click', (e) => {
    const b = e.target.closest('[data-fp]');
    if (!b || !selected) return;
    const a = b.dataset.fp;
    if (a === 'settings' && typeof openPortDialog === 'function') openPortDialog(selected);
    if (a === 'monitor' && typeof openPortDiagnostics === 'function') openPortDiagnostics(selected);
    if (a === 'topology' && typeof activatePage === 'function') { history.replaceState(null, '', '#topology'); activatePage('#topology'); }
    if (a === 'enable' || a === 'disable') setPortState(selected, a);
    if (a === 'devices') { devCache = null; loadDevices(); }
  });

  document.addEventListener('ports:updated', () => {
    trackErrors(ports());
    loadEvents();
    switchportCache = new Map([...switchportCache].filter(([k]) => k === selected));
    render();
  });

  fetch('/api/ports/protection', { cache: 'no-store' })
    .then((r) => (r.ok ? r.json() : {}))
    .then((d) => { protectedPorts = new Set(d.ports || []); if (selected) renderDetail(); })
    .catch(() => {});

  render();
  loadEvents();
})();
