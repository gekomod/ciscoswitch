// Informacja o wczytywaniu danych dla całego panelu.
// Każde zapytanie do /api/ uruchomione kliknięciem dostaje: pasek postępu u góry,
// stan „zajęty” na klikniętym przycisku, komunikat po zakończeniu oraz ostrzeżenie,
// gdy switch odpowiada wolno. Zapytania w tle (automatyczne odświeżanie) są ciche.
(function () {
  const CLICK_WINDOW_MS = 800;
  const SLOW_MS = 4000;
  const BAR_DELAY_MS = 150;
  let lastClick = { el: null, at: 0 };
  let pending = 0;
  let barTimer = null;

  document.addEventListener('click', (e) => {
    const el = e.target.closest('button, .btn, [role="button"]');
    if (el) lastClick = { el, at: performance.now() };
  }, true);

  const bar = document.createElement('div');
  bar.className = 'load-bar';
  bar.setAttribute('aria-hidden', 'true');
  const toasts = document.createElement('div');
  toasts.className = 'load-toasts';
  toasts.setAttribute('role', 'status');
  toasts.setAttribute('aria-live', 'polite');
  document.addEventListener('DOMContentLoaded', () => document.body.append(bar, toasts));

  function toast(text, kind) {
    const t = document.createElement('div');
    t.className = 'load-toast load-toast-' + kind;
    t.textContent = text;
    toasts.append(t);
    setTimeout(() => t.classList.add('is-leaving'), kind === 'error' ? 7000 : 3200);
    setTimeout(() => t.remove(), kind === 'error' ? 7400 : 3600);
    return t;
  }

  function seconds(ms) {
    return (ms / 1000).toLocaleString('pl-PL', { minimumFractionDigits: 1, maximumFractionDigits: 1 }) + ' s';
  }

  function setBusy(el, busy) {
    if (!el) return;
    const n = Number(el.dataset.loadCount || 0) + (busy ? 1 : -1);
    el.dataset.loadCount = String(Math.max(0, n));
    el.classList.toggle('is-loading', n > 0);
    if (n > 0) el.setAttribute('aria-busy', 'true'); else el.removeAttribute('aria-busy');
  }

  function start() {
    pending++;
    if (pending === 1) {
      clearTimeout(barTimer);
      barTimer = setTimeout(() => bar.classList.add('is-active'), BAR_DELAY_MS);
    }
  }

  function finish() {
    pending = Math.max(0, pending - 1);
    if (pending === 0) {
      clearTimeout(barTimer);
      bar.classList.remove('is-active');
    }
  }

  const originalFetch = window.fetch.bind(window);
  window.fetch = function (input, init) {
    const url = typeof input === 'string' ? input : (input && input.url) || '';
    const userAction = url.includes('/api/') && lastClick.el && performance.now() - lastClick.at < CLICK_WINDOW_MS;
    if (!userAction) return originalFetch(input, init);

    const button = lastClick.el;
    const method = ((init && init.method) || (input && input.method) || 'GET').toUpperCase();
    const label = (button.textContent || '').trim().replace(/…$/, '');
    const started = performance.now();
    setBusy(button, true);
    start();
    let slowToast = null;
    const slowTimer = setTimeout(() => {
      slowToast = toast('Switch odpowiada wolno. Odczyt nadal trwa…', 'slow');
    }, SLOW_MS);

    return originalFetch(input, init).then((res) => {
      const took = seconds(performance.now() - started);
      if (res.ok) {
        toast(method === 'GET' ? 'Dane odczytane w ' + took : 'Gotowe w ' + took + (label ? ': ' + label : ''), 'ok');
      } else if (res.status !== 401) {
        toast('Nie udało się' + (label ? ' (' + label + ')' : '') + '. Serwer zwrócił błąd ' + res.status + '. Szczegóły są w sekcji.', 'error');
      }
      return res;
    }, (err) => {
      if (err && err.name !== 'AbortError') {
        toast('Brak połączenia z panelem. Sprawdź, czy serwer panelu działa.', 'error');
      }
      throw err;
    }).finally(() => {
      clearTimeout(slowTimer);
      if (slowToast) slowToast.remove();
      setBusy(button, false);
      finish();
    });
  };
})();
