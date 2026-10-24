// Pages dont l'URL contient un jeton secret (partage, invitation, lien de connexion) : la visite
// est comptée sous l'URL générique fournie par le serveur, le jeton ne part jamais chez Plausible.
(() => {
  const generic = document.currentScript.dataset.url;
  window.plausible = window.plausible || function () { (window.plausible.q = window.plausible.q || []).push(arguments); };
  window.plausible('pageview', { u: window.location.origin + generic });
})();
