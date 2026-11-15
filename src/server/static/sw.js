"use strict";

const CACHE_NAME = "shortner-cache-v__BUILD_STAMP__";
const CACHE_PREFIX = "shortner-cache-v";
const PRECACHE = [
  "/static/css/common.css?v=__BUILD_STAMP__",
  "/static/css/components.css?v=__BUILD_STAMP__",
  "/static/css/public.css?v=__BUILD_STAMP__",
  "/static/js/app.js?v=__BUILD_STAMP__",
  "/static/icons/icon-72.png?v=__BUILD_STAMP__",
  "/static/icons/icon-96.png?v=__BUILD_STAMP__",
  "/static/icons/icon-128.png?v=__BUILD_STAMP__",
  "/static/icons/icon-144.png?v=__BUILD_STAMP__",
  "/static/icons/icon-152.png?v=__BUILD_STAMP__",
  "/static/icons/icon-180.png?v=__BUILD_STAMP__",
  "/static/icons/icon-192.png?v=__BUILD_STAMP__",
  "/static/icons/icon-384.png?v=__BUILD_STAMP__",
  "/static/icons/icon-512.png?v=__BUILD_STAMP__",
  "/static/icons/icon-maskable-192.png?v=__BUILD_STAMP__",
  "/static/icons/icon-maskable-512.png?v=__BUILD_STAMP__",
  "/offline.html"
];

self.addEventListener("message", (event) => {
  if (event.data && event.data.action === "skipWaiting") self.skipWaiting();
});

self.addEventListener("install", (event) => {
  event.waitUntil(caches.open(CACHE_NAME).then((cache) => cache.addAll(PRECACHE)).then(() => {
    if (!self.registration.active) return self.skipWaiting();
  }));
});

self.addEventListener("activate", (event) => {
  event.waitUntil(caches.keys().then((keys) => Promise.all(keys.filter((key) => key.startsWith(CACHE_PREFIX) && key !== CACHE_NAME).map((key) => caches.delete(key)))).then(() => self.clients.claim()));
});

self.addEventListener("fetch", (event) => {
  const request = event.request;
  const url = new URL(request.url);
  if (request.method !== "GET" || url.origin !== self.location.origin || url.pathname.startsWith("/api/")) return;

  if (request.mode === "navigate") {
    event.respondWith(fetch(request).then((response) => {
      const cacheControl = (response.headers.get("Cache-Control") || "").toLowerCase();
      const contentType = (response.headers.get("Content-Type") || "").toLowerCase();
      if (response.status === 200 && response.ok && contentType.includes("text/html") && !/\b(no-store|private)\b/.test(cacheControl)) {
        caches.open(CACHE_NAME).then((cache) => cache.put(request, response.clone()));
      }
      return response;
    }).catch(async () => (await caches.match(request)) || (await caches.match("/offline.html")) || offlineFallbackResponse()).catch(() => offlineFallbackResponse()));
    return;
  }

  if (url.pathname.startsWith("/static/")) {
    event.respondWith(caches.match(request).then((cached) => cached || fetch(request).then((response) => {
      if (response.ok) caches.open(CACHE_NAME).then((cache) => cache.put(request, response.clone()));
      return response;
    })).catch(() => new Response("", { status: 504, statusText: "Gateway Timeout" })));
  }
});

function offlineFallbackResponse() {
  return new Response("<!doctype html><html lang=\"en\"><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width,initial-scale=1\"><title>Offline · Shortner</title><main><h1>You are offline</h1><p>Check your connection and try again.</p></main></html>", {
    status: 503,
    statusText: "Service Unavailable",
    headers: { "Content-Type": "text/html; charset=utf-8" }
  });
}
