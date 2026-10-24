// Comportements communs : champs « Autre », teinte par défaut d'une famille,
// envoi automatique de certains sélecteurs, confirmations.
(() => {
  'use strict';

  function syncOther(select) {
    const scope = select.closest('form') || document;
    const target = scope.querySelector(`[data-other-for="${select.dataset.other}"]`);
    if (target) target.hidden = select.value !== 'OTHER';
  }

  document.addEventListener('change', (e) => {
    const el = e.target;
    if (el.matches('select[data-other]')) syncOther(el);
    if (el.matches('select.js-family')) {
      const hex = (el.closest('form') || document).querySelector('input.js-hex');
      const opt = el.selectedOptions[0];
      if (hex && opt && hex.dataset.touched !== '1') hex.value = opt.dataset.hex;
    }
    if (el.matches('select[data-autosubmit]') && el.value) el.form.requestSubmit();
  });

  // Changer de périmètre recharge toute la page (titre, actions) : on intercepte l'événement
  // en phase de capture pour que htmx ne rafraîchisse pas seulement les résultats.
  document.addEventListener('change', (e) => {
    const el = e.target;
    if (!el.matches('select[data-autosubmit-always]')) return;
    e.stopPropagation();
    const url = new URL(el.form.action, window.location.href);
    if (el.value) url.searchParams.set(el.name, el.value);
    window.location.href = url.toString();
  }, true);

  // Confirmation propre à un bouton (un formulaire peut en avoir plusieurs).
  document.addEventListener('click', (e) => {
    const btn = e.target.closest('[data-confirm-click]');
    if (btn && !window.confirm(btn.dataset.confirmClick)) e.preventDefault();
  });

  // Copie d'un lien dans le presse-papiers.
  document.addEventListener('click', async (e) => {
    const btn = e.target.closest('[data-copy]');
    if (!btn) return;
    const input = document.getElementById(btn.dataset.copy);
    try {
      await navigator.clipboard.writeText(input.value);
      btn.textContent = 'Copié !';
    } catch (err) {
      input.select();
    }
  });

  document.addEventListener('input', (e) => {
    if (e.target.matches('input.js-hex')) e.target.dataset.touched = '1';
  });

  document.addEventListener('submit', (e) => {
    const msg = e.target.dataset.confirm;
    if (msg && !window.confirm(msg)) e.preventDefault();
  });

  // Si la session a expiré pendant une requête htmx, on suit la redirection vers la connexion.
  document.addEventListener('htmx:responseError', (e) => {
    const target = e.detail.xhr.getResponseHeader('HX-Redirect');
    if (target) window.location.href = target;
  });
})();
