// Instalator pierwszego uruchomienia (/setup): konto administratora i połączenie ze switchem.
(function () {
  const $ = (id) => document.getElementById(id);
  const form = $('setup-form'), errorBox = $('setup-error'), nextBtn = $('next'), backBtn = $('back');
  let step = 1, keyFetched = false;

  async function api(path, body) {
    const res = await fetch(path, { method: body ? 'POST' : 'GET', headers: body ? { 'Content-Type': 'application/json' } : {}, body: body ? JSON.stringify(body) : undefined, cache: 'no-store' });
    const text = await res.text();
    let data = {};
    try { data = JSON.parse(text); } catch (_) { data = { error: text.trim() }; }
    if (!res.ok) throw new Error(data.error || ('Błąd ' + res.status));
    return data;
  }

  const code = () => $('setup-code').value.trim();
  const skip = () => $('skip-switch').checked;
  const snmpVer = () => $('c-snmp-ver').value;

  function connection() {
    return {
      host: $('c-host').value.trim(), sshPort: $('c-ssh-port').value.trim(), sshUser: $('c-ssh-user').value.trim(),
      sshPassword: $('c-ssh-pass').value, sshHostKey: $('c-hostkey').value.trim(),
      snmpVersion: snmpVer(), snmpPort: $('c-snmp-port').value.trim(), snmpCommunity: $('c-community').value,
      snmpV3User: $('c-v3-user').value.trim(), snmpV3AuthPassword: $('c-v3-auth').value,
      snmpV3PrivPassword: $('c-v3-priv').value, snmpV3PrivProtocol: $('c-v3-priv-proto').value,
    };
  }

  function show(n) {
    step = n;
    form.querySelectorAll('fieldset[data-step]').forEach((f) => { f.hidden = Number(f.dataset.step) !== n; });
    document.querySelectorAll('[data-step-dot]').forEach((li) => {
      const d = Number(li.dataset.stepDot);
      li.classList.toggle('active', d === n);
      li.classList.toggle('done', d < n);
    });
    backBtn.hidden = n === 1;
    nextBtn.innerHTML = n === 3 ? 'Zakończ instalację <span>✓</span>' : 'Dalej <span>→</span>';
    errorBox.textContent = '';
    const first = form.querySelector('fieldset[data-step="' + n + '"] input:not([type=checkbox])');
    if (first) first.focus();
  }

  function validateStep1() {
    if (code().replace('-', '').length !== 8) return 'Podaj 8-znakowy kod instalatora z logu panelu.';
    if (!/^[A-Za-z0-9][A-Za-z0-9._-]{2,31}$/.test($('setup-user').value.trim())) return 'Login: 3–32 znaki (litery, cyfry, kropka, myślnik, podkreślenie).';
    if ($('setup-pass').value.length < 8) return 'Hasło musi mieć co najmniej 8 znaków.';
    if ($('setup-pass').value !== $('setup-pass2').value) return 'Hasła nie są takie same.';
    return '';
  }

  function validateStep2() {
    if (skip()) return '';
    const c = connection();
    if (!c.host) return 'Podaj adres IP switcha.';
    if (!c.sshUser) return 'Podaj login SSH.';
    if (!c.sshPassword && !$('c-ssh-pass').dataset.kept) return 'Podaj hasło SSH.';
    if (!/^SHA256:/.test(c.sshHostKey)) return 'Pobierz odcisk klucza SSH switcha (przycisk „Pobierz”).';
    if (keyFetched && !$('key-confirm').checked) return 'Potwierdź, że odcisk klucza SSH jest właściwy.';
    if (c.snmpVersion === '2c' && !c.snmpCommunity && !$('c-community').dataset.kept) return 'Podaj community SNMP.';
    if (c.snmpVersion === '3') {
      if (!c.snmpV3User) return 'Podaj użytkownika SNMPv3.';
      if ((c.snmpV3AuthPassword.length < 8 && !$('c-v3-auth').dataset.kept) || (c.snmpV3PrivPassword.length < 8 && !$('c-v3-priv').dataset.kept)) return 'Hasła SNMPv3 muszą mieć co najmniej 8 znaków.';
    }
    return '';
  }

  function summary() {
    const c = connection(), rows = [['Administrator', $('setup-user').value.trim()]];
    if (skip()) rows.push(['Switch', 'pominięty — ustawisz później']);
    else {
      rows.push(['Switch', c.host + ' (SSH ' + (c.sshPort || '22') + ', login ' + c.sshUser + ')']);
      rows.push(['Klucz SSH', c.sshHostKey]);
      rows.push(['SNMP', c.snmpVersion === '3' ? 'v3, użytkownik ' + c.snmpV3User + ', ' + c.snmpV3PrivProtocol.toUpperCase() : 'v2c (community)']);
      rows.push(['Porty chronione', $('c-protected').value.trim() || 'brak']);
    }
    const dl = $('setup-summary');
    dl.textContent = '';
    for (const [k, v] of rows) {
      const dt = document.createElement('dt'), dd = document.createElement('dd');
      dt.textContent = k; dd.textContent = v;
      dl.append(dt, dd);
    }
  }

  form.addEventListener('submit', async (e) => {
    e.preventDefault();
    const err = step === 1 ? validateStep1() : step === 2 ? validateStep2() : '';
    if (err) { errorBox.textContent = err; return; }
    if (step < 3) { if (step === 2) summary(); show(step + 1); return; }
    nextBtn.disabled = true;
    nextBtn.textContent = 'Zapisuję…';
    try {
      const body = { code: code(), username: $('setup-user').value.trim(), password: $('setup-pass').value };
      if (!skip()) { body.connection = connection(); body.protectedPorts = $('c-protected').value.trim(); }
      await api('/api/setup/complete', body);
      location.replace('/');
    } catch (ex) {
      errorBox.textContent = ex.message;
      nextBtn.disabled = false;
      nextBtn.innerHTML = 'Zakończ instalację <span>✓</span>';
    }
  });
  backBtn.addEventListener('click', () => show(step - 1));

  $('fetch-key').addEventListener('click', async () => {
    errorBox.textContent = '';
    const btn = $('fetch-key');
    btn.disabled = true; btn.textContent = 'Łączę…';
    try {
      const d = await api('/api/setup/hostkey', { code: code(), host: $('c-host').value.trim(), port: $('c-ssh-port').value.trim() });
      $('c-hostkey').value = d.fingerprint;
      keyFetched = true;
      $('key-confirm').checked = false;
      $('key-confirm-row').hidden = false;
    } catch (ex) { errorBox.textContent = ex.message; }
    btn.disabled = false; btn.textContent = 'Pobierz';
  });

  $('test-conn').addEventListener('click', async () => {
    const out = $('test-result'), btn = $('test-conn');
    out.textContent = 'Sprawdzam SSH i SNMP…';
    btn.disabled = true;
    try {
      const r = await api('/api/setup/test', { code: code(), connection: connection() });
      out.textContent = '';
      const line = (ok, label, msg) => { const s = document.createElement('div'); s.className = ok ? 'ok' : 'bad'; s.textContent = (ok ? '✓ ' : '✗ ') + label + (msg ? ': ' + msg : ''); out.append(s); };
      line(r.sshOk, 'SSH', r.sshOk ? 'zalogowano' : r.sshError);
      line(r.snmpOk, 'SNMP', r.snmpOk ? (r.name || r.model || 'odpowiada') : r.snmpError);
    } catch (ex) { out.textContent = ex.message; }
    btn.disabled = false;
  });

  $('c-snmp-ver').addEventListener('change', () => {
    document.querySelectorAll('[data-snmp]').forEach((el) => { el.hidden = el.dataset.snmp !== snmpVer(); });
  });
  $('c-hostkey').addEventListener('input', () => { keyFetched = false; $('key-confirm-row').hidden = true; });
  $('skip-switch').addEventListener('change', () => {
    form.querySelectorAll('fieldset[data-step="2"] input:not(#skip-switch), fieldset[data-step="2"] select, fieldset[data-step="2"] button').forEach((el) => { el.disabled = skip(); });
  });

  // Wartości z dotychczasowego .env (bez haseł) ułatwiają przejście na instalator.
  api('/api/setup/status').then((d) => {
    if (!d.needed) { location.replace('/login'); return; }
    const c = d.connection || {};
    if (c.source === 'env' && c.host) {
      const set = (id, v) => { if (v) $(id).value = v; };
      set('c-host', c.host); set('c-ssh-port', c.sshPort); set('c-ssh-user', c.sshUser); set('c-hostkey', c.sshHostKey);
      set('c-snmp-port', c.snmpPort); set('c-v3-user', c.snmpV3User);
      if (c.snmpVersion === '3') { $('c-snmp-ver').value = '3'; $('c-snmp-ver').dispatchEvent(new Event('change')); }
      if (c.snmpV3PrivProtocol) $('c-v3-priv-proto').value = c.snmpV3PrivProtocol;
      const kept = (id, has) => { if (has) { $(id).dataset.kept = '1'; $(id).placeholder = 'bez zmian (z .env)'; } };
      kept('c-ssh-pass', c.hasSSHPassword); kept('c-community', c.hasSNMPCommunity);
      kept('c-v3-auth', c.hasSNMPV3AuthPassword); kept('c-v3-priv', c.hasSNMPV3PrivPassword);
      const note = $('setup-env-note');
      note.textContent = 'Wczytano adres i loginy z pliku .env. Puste pola haseł oznaczają: użyj haseł z .env.';
      note.hidden = false;
    }
  }).catch(() => {});
})();
