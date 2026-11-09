// Raport zgodności: sprawdzenie poleceń odczytu i parserów na prawdziwym switchu.
(function () {
  const root = document.getElementById('compat-report');
  if (!root) return;
  const esc = (s) => String(s == null ? '' : s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const $id = (id) => document.getElementById(id);
  const LABEL = { ok: 'OK', warning: 'Uwaga', error: 'Błąd' };

  $id('compat-run').addEventListener('click', async () => {
    const btn = $id('compat-run'), list = $id('compat-list');
    btn.disabled = true;
    list.innerHTML = '<p class="mon-note">Sprawdzam polecenia na switchu…</p>';
    try {
      const res = await fetch('/api/compat', { cache: 'no-store' });
      if (res.status === 401) { location.href = '/login'; return; }
      const d = await res.json();
      if (!res.ok) throw new Error(d.error || 'Błąd ' + res.status);
      const counts = { ok: 0, warning: 0, error: 0 };
      d.checks.forEach((c) => counts[c.status]++);
      list.innerHTML = '<p class="mon-note">Wynik: ' + counts.ok + ' OK, ' + counts.warning + ' z uwagami, ' + counts.error + ' błędów.</p>' +
        '<ul class="mon-list compat-list">' + d.checks.map((c) => '<li class="compat-' + c.status + '"><span><span class="compat-badge">' + LABEL[c.status] + '</span> <b>' + esc(c.area) + '</b> <code>' + esc(c.command) + '</code><small>' + esc(c.detail) + '</small></span></li>').join('') + '</ul>';
      $id('compat-text').value = d.text;
      $id('compat-text-wrap').hidden = false;
    } catch (e) {
      list.innerHTML = '<p class="mon-error">Nie udało się wykonać sprawdzenia: ' + esc(e.message) + '</p>';
    } finally {
      btn.disabled = false;
    }
  });

  $id('compat-copy').addEventListener('click', async () => {
    const t = $id('compat-text');
    try { await navigator.clipboard.writeText(t.value); $id('compat-copy').textContent = 'Skopiowano'; }
    catch (_) { t.select(); document.execCommand('copy'); $id('compat-copy').textContent = 'Skopiowano'; }
    setTimeout(() => { $id('compat-copy').textContent = 'Kopiuj raport'; }, 2000);
  });
})();
