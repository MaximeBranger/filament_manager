// Scan unitaire : dès qu'un code est lu, on bascule sur la fiche correspondante.
(() => {
  'use strict';
  const video = document.getElementById('video');
  const status = document.getElementById('scan-status');
  const button = document.getElementById('scan-start');

  async function start() {
    button.hidden = true;
    status.textContent = 'Visez le code-barre…';
    const ok = await FMScanner.start(video, (code) => {
      FMScanner.stop();
      status.textContent = `Code détecté : ${code}`;
      window.location.href = '/scan/lookup?ean=' + encodeURIComponent(code);
    }, (msg) => {
      status.textContent = msg;
      button.hidden = false;
    });
    if (!ok) button.hidden = false;
  }

  button.addEventListener('click', start);
  window.addEventListener('pagehide', () => FMScanner.stop());
  start();
})();
