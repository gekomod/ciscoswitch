// Powiadomienia (tylko administrator): Telegram, webhook (Home Assistant, ntfy), e-mail.
(function () {
  const root = document.getElementById('notifications');
  if (!root) return;
  const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const $id = (id) => document.getElementById(id);
  const box = $id('notify-box'), info = $id('notify-updated');
  let view = null;
  const clear = new Set();

  async function call(url, method, body) {
    const res = await fetch(url, { method: method || 'GET', headers: { 'Content-Type': 'application/json' }, body: body ? JSON.stringify(body) : undefined, cache: 'no-store' });
    const raw = await res.text();
    let d = {};
    try { d = raw ? JSON.parse(raw) : {}; } catch (_) { d = { error: raw }; }
    if (res.status === 401) { location.href = '/login'; throw new Error('Sesja wygasła.'); }
    if (res.status === 403) throw new Error('Powiadomienia może zmieniać tylko administrator.');
    if (!res.ok) throw new Error(d.error || 'Błąd ' + res.status);
    return d;
  }

  const status = (name) => {
    const s = view.status && view.status[name];
    if (!s) return '<span class="set-src">Jeszcze nie wysyłano</span>';
    return s.ok ? '<span class="set-src ntf-ok">Ostatnio wysłano ' + esc(s.at) + '</span>' : '<span class="set-src ntf-bad">Błąd ' + esc(s.at) + ': ' + esc(s.error) + '</span>';
  };
  const secret = (id, has, label, key) => '<label for="' + id + '">' + label + '</label><input id="' + id + '" type="password" autocomplete="off" placeholder="' + (has && !clear.has(key) ? 'zapisane; wpisz, aby zmienić' : '') + '">' +
    (has && !clear.has(key) ? '<button type="button" class="set-reset" data-clear="' + key + '">Usuń zapisane</button>' : '');
  const toggle = (id, on, label) => '<label class="ntf-toggle"><input id="' + id + '" type="checkbox"' + (on ? ' checked' : '') + '> ' + label + '</label>';

  function render() {
    const v = view, r = v.rules || {};
    const portsMode = !r.ports ? 'none' : r.ports === 'all' ? 'all' : 'list';
    box.innerHTML =
      '<h3 class="files-h3">Kiedy powiadamiać</h3><div class="set-grid"><div class="set-field">' +
        toggle('ntf-r-switch', r.switch, 'Utrata i powrót połączenia ze switchem') +
        toggle('ntf-r-errors', r.errors, 'Przyrost błędów na portach') +
        '<label for="ntf-r-portsmode">Zmiany łącza (port w górę lub w dół)</label><select id="ntf-r-portsmode"><option value="none"' + (portsMode === 'none' ? ' selected' : '') + '>Nie powiadamiaj</option><option value="list"' + (portsMode === 'list' ? ' selected' : '') + '>Tylko wybrane porty</option><option value="all"' + (portsMode === 'all' ? ' selected' : '') + '>Wszystkie porty</option></select>' +
        '<input id="ntf-r-ports" type="text" placeholder="np. 1,49 (uplinki, serwery)" value="' + esc(portsMode === 'list' ? r.ports : '') + '"' + (portsMode === 'list' ? '' : ' hidden') + '>' +
        '<small>Zdarzenia z 10 sekund trafiają do jednej wiadomości, a to samo zdarzenie nie powtarza się częściej niż co 2 minuty.</small></div></div>' +
      '<h3 class="files-h3">Kanały</h3><div class="set-grid">' +
        '<div class="set-field"><b>Telegram</b>' + toggle('ntf-tg-on', v.telegram.enabled, 'Włączony') +
          secret('ntf-tg-token', v.telegram.hasToken, 'Token bota (od @BotFather)', 'telegramToken') +
          '<label for="ntf-tg-chat">Chat ID</label><input id="ntf-tg-chat" type="text" value="' + esc(v.telegram.chatId) + '" placeholder="np. 123456789 albo -100… dla grupy">' +
          '<div class="set-meta">' + status('telegram') + '</div></div>' +
        '<div class="set-field"><b>Webhook (Home Assistant, ntfy…)</b>' + toggle('ntf-wh-on', v.webhook.enabled, 'Włączony') +
          secret('ntf-wh-url', v.webhook.hasUrl, 'Adres' + (v.webhook.host ? ' (zapisany: ' + esc(v.webhook.host) + ')' : ''), 'webhookUrl') +
          '<small>Panel wysyła POST z JSON: title, message, lines, timestamp. W Home Assistant: wyzwalacz „Webhook”, treść w trigger.json.message.</small>' +
          '<div class="set-meta">' + status('webhook') + '</div></div>' +
        '<div class="set-field"><b>E-mail</b>' + toggle('ntf-em-on', v.email.enabled, 'Włączony') +
          '<label for="ntf-em-host">Serwer SMTP</label><input id="ntf-em-host" type="text" value="' + esc(v.email.host) + '" placeholder="np. smtp.gmail.com">' +
          '<label for="ntf-em-port">Port</label><input id="ntf-em-port" type="number" min="1" max="65535" value="' + esc(v.email.port || 587) + '">' +
          '<label for="ntf-em-user">Użytkownik</label><input id="ntf-em-user" type="text" value="' + esc(v.email.user) + '" autocomplete="off">' +
          secret('ntf-em-pass', v.email.hasPassword, 'Hasło (np. hasło aplikacji)', 'emailPassword') +
          '<label for="ntf-em-from">Od</label><input id="ntf-em-from" type="email" value="' + esc(v.email.from) + '">' +
          '<label for="ntf-em-to">Do (kilka adresów po przecinku)</label><input id="ntf-em-to" type="text" value="' + esc(v.email.to) + '">' +
          '<small>Port 587: STARTTLS, port 465: TLS.</small><div class="set-meta">' + status('email') + '</div></div>' +
      '</div>';
    $id('ntf-r-portsmode').addEventListener('change', (e) => { $id('ntf-r-ports').hidden = e.target.value !== 'list'; });
  }

  function collect() {
    const mode = $id('ntf-r-portsmode').value;
    return {
      telegram: { enabled: $id('ntf-tg-on').checked, token: $id('ntf-tg-token').value.trim(), chatId: $id('ntf-tg-chat').value.trim() },
      webhook: { enabled: $id('ntf-wh-on').checked, url: $id('ntf-wh-url').value.trim() },
      email: { enabled: $id('ntf-em-on').checked, host: $id('ntf-em-host').value.trim(), port: Number($id('ntf-em-port').value) || 0, user: $id('ntf-em-user').value.trim(), password: $id('ntf-em-pass').value, from: $id('ntf-em-from').value.trim(), to: $id('ntf-em-to').value.trim() },
      rules: { switch: $id('ntf-r-switch').checked, errors: $id('ntf-r-errors').checked, ports: mode === 'all' ? 'all' : mode === 'list' ? $id('ntf-r-ports').value.trim() : '' },
    };
  }

  async function load() {
    try { view = await call('/api/notify'); clear.clear(); render(); }
    catch (e) { box.innerHTML = '<p class="mon-error">' + esc(e.message) + '</p>'; }
  }

  async function save() {
    try {
      view = await call('/api/notify', 'POST', { config: collect(), clear: [...clear] });
      clear.clear(); render();
      info.textContent = 'Zapisano o ' + new Date().toLocaleTimeString('pl-PL');
      return true;
    } catch (e) { alert('Nie zapisano: ' + e.message); return false; }
  }

  $id('notify-save').addEventListener('click', save);
  $id('notify-test').addEventListener('click', async () => {
    if (!(await save())) return;
    try {
      const d = await call('/api/notify/test', 'POST');
      if (!d.channels.length) { info.textContent = 'Żaden kanał nie jest włączony.'; return; }
      info.textContent = d.channels.map((c) => c + ': ' + (d.results[c].ok ? 'wysłano' : 'błąd')).join(', ');
      view = await call('/api/notify'); render();
    } catch (e) { alert('Test nie powiódł się: ' + e.message); }
  });
  box.addEventListener('click', (e) => {
    const b = e.target.closest('[data-clear]');
    if (b) { clear.add(b.dataset.clear); const snapshot = collect(); render(); restore(snapshot); info.textContent = 'Zapisz zmiany, aby usunąć dane.'; }
  });
  // po przebudowie (usunięcie sekretu) przywraca wpisane, niezapisane wartości
  function restore(c) {
    $id('ntf-tg-on').checked = c.telegram.enabled; $id('ntf-tg-chat').value = c.telegram.chatId;
    $id('ntf-wh-on').checked = c.webhook.enabled; $id('ntf-em-on').checked = c.email.enabled;
    $id('ntf-em-host').value = c.email.host; $id('ntf-em-port').value = c.email.port || 587; $id('ntf-em-user').value = c.email.user;
    $id('ntf-em-from').value = c.email.from; $id('ntf-em-to').value = c.email.to;
    $id('ntf-r-switch').checked = c.rules.switch; $id('ntf-r-errors').checked = c.rules.errors;
  }

  document.addEventListener('page:change', (e) => { if (e.detail && e.detail.hash === '#notifications') load(); });
  if (location.hash === '#notifications') load();
})();
