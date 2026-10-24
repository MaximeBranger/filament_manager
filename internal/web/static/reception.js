// Réception en série : chaque code scanné alimente une liste locale, enregistrée d'un coup.
(() => {
  'use strict';
  const video = document.getElementById('video');
  const status = document.getElementById('scan-status');
  const startButton = document.getElementById('scan-start');
  const list = document.getElementById('reception-list');
  const empty = document.getElementById('reception-empty');
  const newCard = document.getElementById('new-product');
  const newForm = document.getElementById('new-product-form');
  const receptionForm = document.getElementById('reception-form');
  const submitButton = document.getElementById('reception-submit');
  const errorBox = document.getElementById('reception-error');
  const refNote = document.getElementById('ref-note');
  const refLine = document.getElementById('ref-line');
  const newIntro = document.getElementById('new-intro');

  /** @type {{ean: string, productId: number, product: object|null, label: string, hex: string, quantity: number, error: string}[]} */
  const items = [];
  let pendingEAN = null; // code inconnu en cours de saisie : on ignore les autres scans

  function render() {
    list.replaceChildren(...items.map((it, i) => {
      const li = document.createElement('li');
      li.className = 'spool';

      const swatch = document.createElement('span');
      swatch.className = 'swatch';
      swatch.style.background = it.hex;

      const main = document.createElement('div');
      main.className = 'spool-main';
      const title = document.createElement('strong');
      title.textContent = it.label;
      const sub = document.createElement('span');
      sub.className = 'muted';
      sub.textContent = (it.product ? 'Nouveau produit · ' : '') + it.ean;
      main.append(title, sub);
      if (it.error) {
        const err = document.createElement('span');
        err.className = 'field-error';
        err.textContent = it.error;
        main.append(err);
      }

      const qty = document.createElement('input');
      qty.type = 'number';
      qty.min = '1';
      qty.max = '50';
      qty.value = String(it.quantity);
      qty.className = 'qty';
      qty.setAttribute('aria-label', 'Quantité');
      qty.addEventListener('change', () => {
        const n = parseInt(qty.value, 10);
        it.quantity = Number.isFinite(n) ? Math.min(50, Math.max(1, n)) : 1;
        qty.value = String(it.quantity);
      });

      const remove = document.createElement('button');
      remove.type = 'button';
      remove.className = 'btn small';
      remove.textContent = '✕';
      remove.setAttribute('aria-label', 'Retirer');
      remove.addEventListener('click', () => {
        items.splice(i, 1);
        render();
      });

      li.append(swatch, main, qty, remove);
      return li;
    }));
    empty.hidden = items.length > 0;
    submitButton.disabled = items.length === 0 || pendingEAN !== null;
  }

  async function onCode(raw) {
    const ean = raw.replace(/\s+/g, '');
    if (!ean || pendingEAN !== null) return;
    const existing = items.find((it) => it.ean === ean);
    if (existing) {
      existing.quantity = Math.min(50, existing.quantity + 1);
      status.textContent = `${existing.label} : ${existing.quantity} bobine(s)`;
      render();
      return;
    }
    try {
      const resp = await fetch('/api/products/lookup?ean=' + encodeURIComponent(ean), { headers: { Accept: 'application/json' } });
      if (!resp.ok) throw new Error(String(resp.status));
      const data = await resp.json();
      if (data.found) {
        items.push({ ean, productId: data.product.id, product: null, label: data.product.label, hex: data.product.hex, quantity: 1, error: '' });
        status.textContent = `Ajouté : ${data.product.label}`;
        render();
      } else {
        askNewProduct(ean, data.reference, data.line);
      }
    } catch (err) {
      status.textContent = 'Erreur réseau, réessayez.';
    }
  }

  // reference : valeurs proposées par le catalogue de référence (Open Filament Database), ou rien.
  function askNewProduct(ean, reference, line) {
    pendingEAN = ean;
    newForm.reset();
    delete newForm.elements.color_hex.dataset.touched;
    if (reference) {
      Object.entries(reference).forEach(([name, value]) => {
        const field = newForm.elements[name];
        if (field && value !== '') field.value = value;
      });
      newForm.elements.color_hex.dataset.touched = '1'; // la teinte de référence prime sur celle de la famille
    }
    newForm.elements.ean.value = ean;
    newForm.querySelectorAll('select[data-other]').forEach((s) => s.dispatchEvent(new Event('change', { bubbles: true })));
    refNote.hidden = !reference;
    newIntro.hidden = !!reference;
    refLine.textContent = line ? ` (gamme « ${line} »)` : '';
    newCard.hidden = false;
    status.textContent = reference ? `Reconnu dans la référence : ${ean}` : `Code inconnu : ${ean}`;
    newForm.elements.brand.focus();
    render();
  }

  function closeNewProduct() {
    pendingEAN = null;
    newCard.hidden = true;
    render();
  }

  newForm.addEventListener('submit', (e) => {
    e.preventDefault();
    const product = Object.fromEntries(new FormData(newForm).entries());
    product.ean = pendingEAN;
    const material = newForm.elements.material;
    const materialLabel = product.material === 'OTHER' ? product.material_other : material.selectedOptions[0].textContent;
    items.push({
      ean: pendingEAN,
      productId: 0,
      product,
      label: `${product.brand} ${materialLabel} ${product.color_name}`,
      hex: product.color_hex,
      quantity: 1,
      error: '',
    });
    closeNewProduct();
  });

  document.getElementById('new-cancel').addEventListener('click', closeNewProduct);

  document.getElementById('manual-ean').addEventListener('submit', (e) => {
    e.preventDefault();
    const input = e.target.elements.ean;
    onCode(input.value);
    input.value = '';
  });

  receptionForm.addEventListener('submit', async (e) => {
    e.preventDefault();
    errorBox.textContent = '';
    items.forEach((it) => { it.error = ''; });
    submitButton.disabled = true;
    const body = {
      purchase_date: receptionForm.elements.purchase_date.value,
      supplier: receptionForm.elements.supplier.value,
      items: items.map((it) => ({ product_id: it.productId, quantity: it.quantity, product: it.product || undefined })),
    };
    try {
      const resp = await fetch('/reception', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
        body: JSON.stringify(body),
      });
      const data = await resp.json();
      if (resp.ok) {
        submitButton.dataset.done = '1';
        FMScanner.stop();
        window.location.href = data.redirect;
        return;
      }
      errorBox.textContent = data.error || "L'enregistrement a échoué.";
      Object.entries(data.items || {}).forEach(([index, errs]) => {
        if (items[index]) items[index].error = Object.values(errs).join(' ');
      });
    } catch (err) {
      errorBox.textContent = 'Erreur réseau, réessayez.';
    }
    render();
  });

  async function start() {
    startButton.hidden = true;
    status.textContent = 'Visez un code-barre…';
    const ok = await FMScanner.start(video, onCode, (msg) => {
      status.textContent = msg;
      startButton.hidden = false;
    });
    if (!ok) startButton.hidden = false;
  }

  startButton.addEventListener('click', start);
  window.addEventListener('pagehide', () => FMScanner.stop());
  window.addEventListener('beforeunload', (e) => {
    if (items.length > 0 && !submitButton.dataset.done) e.preventDefault();
  });
  render();
  start();
})();
