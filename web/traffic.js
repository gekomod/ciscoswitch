// Ochrona ruchu: wykrywanie pętli, storm control, IGMP snooping.
(function () {
  const root = document.getElementById('traffic-protection');
  if (!root) return;
  const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const $id = (id) => document.getElementById(id);
  const btn = $id('traffic-refresh'), info = $id('traffic-updated');
  const TYPE = { broadcast: 'Broadcast', multicast: 'Multicast', dlf: 'Nieznany unicast' };
  const ACTION = { 'port-disable': 'Wyłącz port', 'link-down': 'Rozłącz łącze', 'vlan-disable': 'Wyłącz VLAN na porcie', 'learn-disable': 'Wstrzymaj uczenie MAC', none: 'Tylko zapisz w logu' };
  let data = null;

  async function call(method, body) {
    const res = await fetch('/api/traffic', { method, headers: { 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined, cache: 'no-store' });
    const raw = await res.text();
    let d = {};
    try { d = raw ? JSON.parse(raw) : {}; } catch (_) { d = { error: raw }; }
    if (res.status === 401) { location.href = '/login'; throw new Error('Sesja wygasła.'); }
    if (!res.ok) throw new Error(d.error || 'Błąd ' + res.status);
    return d;
  }

  const portNum = (p) => p.replace('port1.0.', '');

  function groupPorts(items, keyFn, labelFn) {
    const groups = new Map();
    for (const it of items) {
      const k = keyFn(it);
      if (!groups.has(k)) groups.set(k, { label: labelFn(it), ports: [] });
      groups.get(k).ports.push(portNum(it.port));
    }
    return [...groups.values()];
  }

  function render() {
    const loop = $id('loop-state');
    loop.innerHTML = '<div class="trf-global"><span class="svc-state ' + (data.loopDetect ? 'trf-on' : '') + '">' + (data.loopDetect ? 'Wykrywanie pętli włączone' : 'Wykrywanie pętli wyłączone') + '</span>' +
      '<button class="btn" id="loop-toggle">' + (data.loopDetect ? 'Wyłącz wykrywanie' : 'Włącz wykrywanie') + '</button></div>' +
      (data.loopPorts.length
        ? '<ul class="mon-list">' + groupPorts(data.loopPorts, (x) => x.action, (x) => ACTION[x.action] || x.action).map((g) => '<li><span>Porty ' + esc(g.ports.join(', ')) + ': <b>' + esc(g.label) + '</b></span></li>').join('') + '</ul>'
        : '<p class="mon-note">Żaden port nie ma ustawionej reakcji na pętlę.</p>');

    const storm = $id('storm-state');
    storm.innerHTML = data.storm.length
      ? '<ul class="mon-list">' + groupPorts(data.storm, (x) => x.type + '|' + x.level, (x) => (TYPE[x.type] || x.type) + ' do ' + x.level + '% pasma').map((g) => '<li><span>Porty ' + esc(g.ports.join(', ')) + ': <b>' + esc(g.label) + '</b></span></li>').join('') + '</ul>'
      : '<p class="mon-note">Brak limitów. Burza broadcastów (np. przez pętlę) może zalać całą sieć.</p>';

    const igmp = $id('igmp-state');
    igmp.innerHTML = (data.igmpGlobalDisabled ? '<p class="mon-error">IGMP snooping jest wyłączony globalnie (no ip igmp snooping).</p>' : '') +
      (data.vlans.length
        ? '<ul class="mon-list">' + data.vlans.map((v) => {
          const on = v.snooping !== 'disabled';
          const label = v.snooping === 'disabled' ? 'Wyłączony' : v.snooping === 'enabled' ? 'Włączony' : 'Ustawienie domyślne switcha';
          return '<li><span>VLAN ' + esc(v.vlan) + ': <b>' + esc(label) + '</b></span><button class="btn" data-igmp="' + esc(v.vlan) + '" data-enable="' + !on + '">' + (on ? 'Wyłącz' : 'Włącz') + '</button></li>';
        }).join('') + '</ul>'
        : '<p class="mon-note">Brak interfejsów VLAN w konfiguracji.</p>');
  }

  async function refresh() {
    btn.disabled = true;
    try {
      data = await call('GET');
      info.textContent = 'Odczytano o ' + new Date().toLocaleTimeString('pl-PL');
      render();
    } catch (e) {
      $id('loop-state').innerHTML = '<p class="mon-error">Nie udało się odczytać ustawień: ' + esc(e.message) + '</p>';
    } finally { btn.disabled = false; }
  }

  async function change(body, question) {
    if (!confirm(question + '\n\nPrzed zmianą panel zapisze kopię konfiguracji.')) return;
    try { await call('POST', body); await refresh(); } catch (e) { alert('Nie udało się: ' + e.message); }
  }

  root.addEventListener('click', (e) => {
    if (e.target.id === 'loop-toggle') {
      const enable = !data.loopDetect;
      change({ action: 'loop-detect', enable }, enable ? 'Włączyć wykrywanie pętli na całym switchu?' : 'Wyłączyć wykrywanie pętli?\n\nPętla w sieci (np. kabel wpięty dwa razy do switcha) może wtedy zablokować całą sieć.');
    }
    const ig = e.target.closest('[data-igmp]');
    if (ig) {
      const enable = ig.dataset.enable === 'true';
      change({ action: 'igmp', vlan: Number(ig.dataset.igmp), enable }, (enable ? 'Włączyć' : 'Wyłączyć') + ' IGMP snooping w VLAN ' + ig.dataset.igmp + '?' + (enable ? '' : '\n\nRuch multicast (np. IPTV) trafi wtedy na wszystkie porty VLAN-u.'));
    }
  });

  root.addEventListener('submit', (e) => {
    e.preventDefault();
    const form = e.target;
    const remove = e.submitter && e.submitter.dataset.remove === 'true';
    if (form.id === 'loop-form') {
      const ports = $id('loop-ports').value.trim(), value = $id('loop-action').value;
      change({ action: 'loop-action', ports, value, remove }, remove ? 'Usunąć reakcję na pętlę z portów ' + ports + '?' : 'Ustawić na portach ' + ports + ': ' + ACTION[value] + ' po wykryciu pętli?');
    }
    if (form.id === 'storm-form') {
      const ports = $id('storm-ports').value.trim(), type = $id('storm-type').value, level = Number($id('storm-level').value);
      change({ action: 'storm', ports, type, level, remove }, remove ? 'Usunąć limit ' + TYPE[type] + ' z portów ' + ports + '?' : 'Ograniczyć ' + TYPE[type] + ' do ' + level + '% pasma na portach ' + ports + '?\n\nZbyt niski limit może zakłócić normalną pracę urządzeń.');
    }
  });

  btn.addEventListener('click', refresh);
})();
