// Lecture de codes-barres à la caméra (zxing-js). Un même code lu en continu n'est
// signalé qu'une fois tant qu'il n'a pas quitté le champ pendant 1,5 s.
window.FMScanner = (() => {
  'use strict';
  const REPEAT_MS = 1500;
  let reader = null;
  let last = '';
  let lastAt = 0;

  function formats() {
    const F = ZXing.BarcodeFormat;
    return [F.EAN_13, F.EAN_8, F.UPC_A, F.UPC_E, F.CODE_128];
  }

  async function start(video, onCode, onError) {
    if (!window.isSecureContext || !navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
      onError("La caméra n'est pas disponible ici (une connexion HTTPS est nécessaire). Utilisez la saisie manuelle.");
      return false;
    }
    const hints = new Map();
    hints.set(ZXing.DecodeHintType.POSSIBLE_FORMATS, formats());
    reader = new ZXing.BrowserMultiFormatReader(hints, 250);
    try {
      await reader.decodeFromConstraints(
        { video: { facingMode: { ideal: 'environment' } } },
        video,
        (result) => {
          if (!result) return;
          const code = result.getText();
          const now = Date.now();
          if (code === last && now - lastAt < REPEAT_MS) {
            lastAt = now;
            return;
          }
          last = code;
          lastAt = now;
          if (navigator.vibrate) navigator.vibrate(80);
          onCode(code);
        },
      );
      return true;
    } catch (err) {
      reader = null;
      onError(err && err.name === 'NotAllowedError'
        ? "Accès à la caméra refusé. Autorisez-le dans votre navigateur, ou utilisez la saisie manuelle."
        : "Impossible d'ouvrir la caméra. Utilisez la saisie manuelle.");
      return false;
    }
  }

  function stop() {
    if (reader) {
      reader.reset();
      reader = null;
    }
  }

  return { start, stop };
})();
