// Passkeys (WebAuthn) : ajout depuis la page Compte, connexion depuis la page de connexion
// (bouton, ou suggestion du navigateur dans le champ email).
(() => {
  'use strict';
  if (!window.PublicKeyCredential) {
    const unsupported = document.getElementById('passkey-unsupported');
    const form = document.getElementById('passkey-register');
    if (unsupported) unsupported.hidden = false;
    if (form) form.hidden = true;
    return;
  }
  const status = document.getElementById('passkey-status');

  const toBuffer = (b64url) => {
    const b64 = b64url.replace(/-/g, '+').replace(/_/g, '/').padEnd(Math.ceil(b64url.length / 4) * 4, '=');
    return Uint8Array.from(atob(b64), (c) => c.charCodeAt(0)).buffer;
  };
  const toB64url = (buffer) => {
    let bin = '';
    new Uint8Array(buffer).forEach((b) => { bin += String.fromCharCode(b); });
    return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
  };

  async function post(url, body) {
    const resp = await fetch(url, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', Accept: 'application/json' },
      body: JSON.stringify(body || {}),
    });
    const data = await resp.json().catch(() => ({}));
    if (!resp.ok) throw new Error(data.error || 'Une erreur est survenue.');
    return data;
  }

  function explain(err) {
    if (err.name === 'NotAllowedError' || err.name === 'AbortError') return 'Opération annulée.';
    if (err.name === 'InvalidStateError') return 'Cette passkey est déjà enregistrée sur cet appareil.';
    return err.message || 'Une erreur est survenue.';
  }

  // Ajout d'une passkey ----------------------------------------------------------------
  const registerForm = document.getElementById('passkey-register');
  if (registerForm) {
    registerForm.addEventListener('submit', async (e) => {
      e.preventDefault();
      status.textContent = '';
      try {
        const { publicKey } = await post('/passkeys/register/begin', { name: registerForm.elements.name.value });
        publicKey.challenge = toBuffer(publicKey.challenge);
        publicKey.user.id = toBuffer(publicKey.user.id);
        (publicKey.excludeCredentials || []).forEach((c) => { c.id = toBuffer(c.id); });
        const cred = await navigator.credentials.create({ publicKey });
        const r = cred.response;
        await post('/passkeys/register/finish', {
          id: cred.id,
          rawId: toB64url(cred.rawId),
          type: cred.type,
          response: {
            clientDataJSON: toB64url(r.clientDataJSON),
            attestationObject: toB64url(r.attestationObject),
            transports: r.getTransports ? r.getTransports() : [],
          },
          clientExtensionResults: cred.getClientExtensionResults(),
        });
        window.location.reload();
      } catch (err) {
        status.textContent = explain(err);
      }
    });
  }

  // Connexion ----------------------------------------------------------------------------
  const loginButton = document.getElementById('passkey-login');
  if (!loginButton) return;
  document.getElementById('passkey-login-wrap').hidden = false;
  const next = document.querySelector('#login-form input[name="next"]').value;
  let pending = null;

  async function login(mediation) {
    if (pending) pending.abort();
    const controller = new AbortController();
    pending = controller;
    const { publicKey } = await post('/auth/passkey/begin');
    publicKey.challenge = toBuffer(publicKey.challenge);
    (publicKey.allowCredentials || []).forEach((c) => { c.id = toBuffer(c.id); });
    const cred = await navigator.credentials.get({ publicKey, mediation, signal: controller.signal });
    const r = cred.response;
    const data = await post('/auth/passkey/finish?next=' + encodeURIComponent(next), {
      id: cred.id,
      rawId: toB64url(cred.rawId),
      type: cred.type,
      response: {
        clientDataJSON: toB64url(r.clientDataJSON),
        authenticatorData: toB64url(r.authenticatorData),
        signature: toB64url(r.signature),
        userHandle: r.userHandle ? toB64url(r.userHandle) : null,
      },
      clientExtensionResults: cred.getClientExtensionResults(),
    });
    window.location.href = data.redirect;
  }

  loginButton.addEventListener('click', () => {
    status.textContent = '';
    login('optional').catch((err) => { status.textContent = explain(err); });
  });

  // Suggestion des passkeys dans le champ email (« autofill »), si le navigateur sait le faire.
  if (PublicKeyCredential.isConditionalMediationAvailable) {
    PublicKeyCredential.isConditionalMediationAvailable().then((ok) => {
      if (ok) login('conditional').catch(() => {});
    });
  }
})();
