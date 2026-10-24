// Service worker de FilamentManager. La version est injectée par le serveur : chaque
// déploiement crée un nouveau cache et supprime les anciens.
const VERSION = '__VERSION__';
const CACHE = 'fm-' + VERSION;
const OFFLINE = '/offline';
const PRECACHE = [
  OFFLINE,
  '/static/app.css?v=' + VERSION,
  '/static/app.js?v=' + VERSION,
  '/static/vendor/htmx.min.js?v=' + VERSION,
  '/static/icon.svg',
];

self.addEventListener('install', (event) => {
  event.waitUntil(caches.open(CACHE).then((cache) => cache.addAll(PRECACHE)).then(() => self.skipWaiting()));
});

self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== CACHE).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  );
});

self.addEventListener('fetch', (event) => {
  const { request } = event;
  const url = new URL(request.url);
  if (request.method !== 'GET' || url.origin !== self.location.origin) return;

  // Pages : toujours le réseau (données à jour, aucune page privée en cache) ;
  // page hors ligne si le réseau est indisponible.
  if (request.mode === 'navigate') {
    event.respondWith(fetch(request).catch(() => caches.match(OFFLINE)));
    return;
  }

  // Fichiers statiques : cache d'abord (ils sont versionnés), puis réseau.
  if (url.pathname.startsWith('/static/')) {
    event.respondWith(
      caches.match(request).then((hit) => hit || fetch(request).then((resp) => {
        if (resp.ok) {
          const copy = resp.clone();
          caches.open(CACHE).then((cache) => cache.put(request, copy));
        }
        return resp;
      })),
    );
  }
});
