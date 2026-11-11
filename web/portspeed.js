// Okno ustawień portu: opcje prędkości i duplexu zależne od rodzaju portu.
// Porty 49–52 to gniazda SFP/SFP+ (1 Gb/s lub 10 Gb/s, bez półdupleksu i 10/100).
(function () {
  const dialog = document.getElementById('port-dialog');
  const port = document.getElementById('cfg-port');
  const speed = document.getElementById('cfg-speed');
  const duplex = document.getElementById('cfg-duplex');
  if (!dialog || !port || !speed || !duplex) return;

  let hint = document.getElementById('cfg-sfp-hint');
  if (!hint) {
    hint = document.createElement('p');
    hint.id = 'cfg-sfp-hint';
    hint.className = 'note';
    hint.hidden = true;
    hint.textContent = 'Gniazdo SFP/SFP+: moduł 1 Gb/s w gnieździe SFP+ zwykle wymaga ustawienia prędkości 1000 Mb/s; moduł 10 Gb/s: 10000 Mb/s albo auto.';
    duplex.closest('label').insertAdjacentElement('afterend', hint);
  }

  const isFiber = (name) => { const n = Number(String(name || '').split('.').pop()); return n >= 49 && n <= 52; };

  function adjust() {
    const fiber = isFiber(port.value);
    for (const o of speed.options) {
      const allowed = fiber ? ['auto', '1000', '10000'].includes(o.value) : o.value !== '10000';
      o.disabled = !allowed;
      o.hidden = !allowed;
    }
    for (const o of duplex.options) {
      const allowed = !(fiber && o.value === 'half');
      o.disabled = !allowed;
      o.hidden = !allowed;
    }
    if (speed.selectedOptions[0] && speed.selectedOptions[0].disabled) speed.value = 'auto';
    if (duplex.selectedOptions[0] && duplex.selectedOptions[0].disabled) duplex.value = 'auto';
    hint.hidden = !fiber;
  }

  port.addEventListener('change', adjust);
  new MutationObserver(adjust).observe(dialog, { attributes: true, attributeFilter: ['open'] });
  adjust();
})();
