// Bezpieczeństwo sieci: port security, DHCP snooping, ACL.
(function () {
  const root = document.getElementById('network-security');
  if (!root) return;
  const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const $id = (id) => document.getElementById(id);
  const btn = $id('sec-refresh'), info = $id('sec-updated');
  const VIOLATION = { protect: 'odrzuca nowe adresy', restrict: 'odrzuca i zapisuje w logu', shutdown: 'wyłącza port' };
  const num = (p) => p.replace('port1.0.', '');
  let data = null;

  async function call(method, body) {
    const res = await fetch('/api/security', { method, headers: { 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined, cache: 'no-store' });
    const raw = await res.text();
    let d = {};
    try { d = raw ? JSON.parse(raw) : {}; } catch (_) { d = { error: raw }; }
    if (res.status === 401) { location.href = '/login'; throw new Error('Sesja wygasła.'); }
    if (!res.ok) throw new Error(d.error || 'Błąd ' + res.status);
    return d;
  }

  function render() {
    const groups = new Map();
    for (const p of data.portSecurity) {
      const k = (p.maximum || '?') + '|' + (p.violation || '');
      if (!groups.has(k)) groups.set(k, { p, ports: [] });
      groups.get(k).ports.push(num(p.port));
    }
    $id('ps-state').innerHTML = groups.size
      ? '<ul class="mon-list">' + [...groups.values()].map((g) => '<li><span>Porty ' + esc(g.ports.join(', ')) + ': maks. <b>' + esc(g.p.maximum || 'domyślnie') + '</b> adresów MAC, po przekroczeniu ' + esc(VIOLATION[g.p.violation] || 'reakcja domyślna') + '</span></li>').join('') + '</ul>'
      : '<p class="mon-note">Port security nie jest włączone na żadnym porcie.</p>';

    $id('dhcp-state').innerHTML =
      '<div class="trf-global"><span class="svc-state ' + (data.dhcpSnooping ? 'trf-on' : '') + '">' + (data.dhcpSnooping ? 'DHCP snooping włączony' : 'DHCP snooping wyłączony') + '</span>' +
      '<button class="btn" id="dhcp-global-toggle">' + (data.dhcpSnooping ? 'Wyłącz' : 'Włącz') + '</button></div>' +
      '<p class="mon-note">Zaufane porty (serwer DHCP, uplink): <b>' + esc(data.trustedPorts.length ? data.trustedPorts.map(num).join(', ') : 'brak') + '</b></p>' +
      (data.vlanInterfaces.length
        ? '<ul class="mon-list">' + data.vlanInterfaces.map((v) => {
          const on = data.snoopingVlans.includes(v);
          return '<li><span>VLAN ' + esc(v) + ': <b>' + (on ? 'snooping włączony' : 'snooping wyłączony') + '</b></span><button class="btn" data-dhcp-vlan="' + esc(v) + '" data-enable="' + !on + '">' + (on ? 'Wyłącz' : 'Włącz') + '</button></li>';
        }).join('') + '</ul>'
        : '<p class="mon-note">Brak interfejsów VLAN w konfiguracji.</p>');

    $id('acl-state').innerHTML = (data.acls.length
      ? data.acls.map((a) => '<div class="sec-acl"><div class="sec-acl-head"><b>ACL ' + esc(a.id) + '</b><span>' + (a.ports.length ? 'Przypisany do portów ' + esc(a.ports.map(num).join(', ')) : 'Nieprzypisany') + '</span></div>' +
        '<ol>' + a.rules.map((r) => '<li><code>' + esc(r) + '</code></li>').join('') + '</ol></div>').join('')
      : '<p class="mon-note">Brak list ACL.</p>') +
      (data.unknownAclPorts.length ? '<p class="mon-error">Porty z przypisanym nieistniejącym ACL: ' + esc(data.unknownAclPorts.join(', ')) + '</p>' : '');
    $id('acl-apply-id').innerHTML = data.acls.map((a) => '<option value="' + esc(a.id) + '">ACL ' + esc(a.id) + '</option>').join('') || '<option value="">Brak ACL</option>';
  }

  async function refresh() {
    btn.disabled = true;
    try {
      data = await call('GET');
      info.textContent = 'Odczytano o ' + new Date().toLocaleTimeString('pl-PL');
      render();
    } catch (e) {
      $id('ps-state').innerHTML = '<p class="mon-error">Nie udało się odczytać ustawień: ' + esc(e.message) + '</p>';
    } finally { btn.disabled = false; }
  }

  async function change(body, question) {
    if (!confirm(question + '\n\nPrzed zmianą panel zapisze kopię konfiguracji.')) return;
    try { await call('POST', body); await refresh(); } catch (e) { alert('Nie udało się: ' + e.message); }
  }

  root.addEventListener('click', (e) => {
    if (e.target.id === 'dhcp-global-toggle') {
      const enable = !data.dhcpSnooping;
      change({ action: 'dhcp-global', enable }, enable ? 'Włączyć DHCP snooping na switchu?\n\nSam ten krok nie blokuje ruchu; filtrowanie zaczyna się po włączeniu snoopingu w VLAN-ie.' : 'Wyłączyć DHCP snooping na całym switchu?');
    }
    const v = e.target.closest('[data-dhcp-vlan]');
    if (v) {
      const enable = v.dataset.enable === 'true';
      change({ action: 'dhcp-vlan', vlan: Number(v.dataset.dhcpVlan), enable },
        enable ? 'Włączyć DHCP snooping w VLAN ' + v.dataset.dhcpVlan + '?\n\nOdpowiedzi DHCP będą przyjmowane tylko z zaufanych portów (' + (data.trustedPorts.map(num).join(', ') || 'brak') + '). Upewnij się, że jest wśród nich port do routera.' : 'Wyłączyć DHCP snooping w VLAN ' + v.dataset.dhcpVlan + '?');
    }
  });

  root.addEventListener('submit', (e) => {
    e.preventDefault();
    const f = e.target, remove = e.submitter && e.submitter.dataset.remove === 'true';
    if (f.id === 'ps-form') {
      const ports = $id('ps-ports').value.trim(), maximum = Number($id('ps-max').value), violation = $id('ps-violation').value;
      change({ action: 'port-security', ports, maximum, violation, remove }, remove ? 'Wyłączyć port security na portach ' + ports + '?' : 'Ograniczyć porty ' + ports + ' do ' + maximum + ' adresów MAC? Po przekroczeniu switch ' + VIOLATION[violation] + '.\n\nUwaga: urządzenia już podłączone liczą się do limitu; switch lub punkt dostępowy za portem ma wiele adresów MAC.');
    }
    if (f.id === 'dhcp-trust-form') {
      const ports = $id('dhcp-trust-ports').value.trim(), enable = !remove;
      change({ action: 'dhcp-trust', ports, enable }, enable ? 'Oznaczyć porty ' + ports + ' jako zaufane dla DHCP?' : 'Usunąć zaufanie DHCP z portów ' + ports + '?');
    }
    if (f.id === 'acl-rule-form') {
      const aclNumber = Number($id('acl-number').value), rule = $id('acl-action').value, source = $id('acl-src').value.trim() || 'any', destination = $id('acl-dst').value.trim() || 'any';
      change({ action: 'acl-rule', aclNumber, rule, source, destination, remove }, (remove ? 'Usunąć z ACL ' : 'Dodać do ACL ') + aclNumber + ' regułę: ' + rule + ' ruch z ' + source + ' do ' + destination + '?' + (!remove && rule === 'deny' ? '\n\nUważaj, by nie zablokować komputera, z którego używasz panelu.' : ''));
    }
    if (f.id === 'acl-apply-form') {
      const ports = $id('acl-apply-ports').value.trim(), acl = $id('acl-apply-id').value;
      if (!acl) return;
      change({ action: 'acl-apply', ports, acl, remove }, remove ? 'Zdjąć ACL ' + acl + ' z portów ' + ports + '?' : 'Przypisać ACL ' + acl + ' do portów ' + ports + '?\n\nRuch na tych portach będzie filtrowany według reguł listy.');
    }
  });

  btn.addEventListener('click', refresh);
})();
