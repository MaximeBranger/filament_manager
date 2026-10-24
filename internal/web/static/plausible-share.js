// Pages de partage : la visite est comptée sous une URL générique, le jeton du lien
// ne part jamais chez Plausible.
window.plausible = window.plausible || function () { (window.plausible.q = window.plausible.q || []).push(arguments); };
window.plausible('pageview', { u: window.location.origin + '/share/*' });
