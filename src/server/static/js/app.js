// Vanilla JS progressive enhancement, per AI.md PART 16 "JavaScript
// Structure": ONE file, no framework, no build step. Every feature this
// file touches already works without JavaScript (native form POSTs,
// server-rendered success states) — this only improves the experience.
// All hooks are data-action attributes, never inline onclick.
(function () {
  "use strict";

  var recoveryKey = "shortner_recovered";

  function cacheOfflineStrings() {
    var element = document.getElementById("offline-strings");
    if (!element || !window.caches || typeof window.Response !== "function") return;
    var strings = {
      lang: element.getAttribute("data-lang") || "en",
      dir: element.getAttribute("data-dir") || "ltr",
      title: element.getAttribute("data-title") || "Offline",
      heading: element.getAttribute("data-heading") || "You are offline",
      message: element.getAttribute("data-message") || "This page could not be loaded and no cached copy is available. Check your connection and try again."
    };
    window.caches.open("shortner-offline-strings").then(function (cache) {
      return cache.put("/offline-strings.json", new Response(JSON.stringify(strings), {
        headers: { "Content-Type": "application/json" }
      }));
    }).catch(function () { return null; });
  }

  cacheOfflineStrings();

  function showBootFailure() {
    var main = document.createElement("main");
    var title = document.createElement("h1");
    var message = document.createElement("p");
    main.className = "container";
    title.textContent = "This page could not finish loading";
    message.textContent = "Reload the page or check your connection and try again.";
    main.append(title, message);
    document.body.replaceChildren(main);
  }

  function recoverStuckPage() {
    var alreadyRecovered = false;
    try {
      alreadyRecovered = window.sessionStorage.getItem(recoveryKey) === "1";
      if (!alreadyRecovered) window.sessionStorage.setItem(recoveryKey, "1");
    } catch (_) {
      alreadyRecovered = true;
    }
    if (alreadyRecovered) {
      showBootFailure();
      return;
    }

    var cleanup = [];
    if (navigator.serviceWorker) {
      cleanup.push(navigator.serviceWorker.getRegistrations().then(function (items) {
        return Promise.all(items.map(function (registration) { return registration.unregister(); }));
      }));
    }
    if (window.caches) {
      cleanup.push(window.caches.keys().then(function (keys) {
        return Promise.all(keys.map(function (key) { return window.caches.delete(key); }));
      }));
    }
    var timeout = new Promise(function (resolve) { window.setTimeout(resolve, 3000); });
    Promise.race([Promise.all(cleanup), timeout]).then(function () {
      window.location.reload();
    }, function () {
      window.location.reload();
    });
  }

  var bootTimer = window.setTimeout(recoverStuckPage, 10000);
  function finishInitialRender() { window.clearTimeout(bootTimer); }
  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", finishInitialRender, { once: true });
  } else {
    finishInitialRender();
  }

  // Translations for the handful of strings this file creates at runtime.
  // The page is already fully rendered in the active language by the
  // server; this catalog only covers text that does not exist in the
  // markup. It is fetched from /locales/{lang}.json (AI.md PART 30) and
  // every lookup carries the English fallback baked in below, so the UI
  // stays correct before the fetch resolves and if it never does.
  var strings = {};

  function t(key, fallback) {
    var value = strings[key];
    return typeof value === "string" && value !== "" ? value : fallback;
  }

  function flattenLocale(tree, prefix, out) {
    Object.keys(tree).forEach(function (key) {
      var value = tree[key];
      var path = prefix ? prefix + "." + key : key;
      if (value && typeof value === "object") {
        flattenLocale(value, path, out);
      } else if (typeof value === "string") {
        out[path] = value;
      }
    });
  }

  function loadLocale() {
    var lang = document.documentElement.getAttribute("lang");
    if (!lang || !window.fetch) {
      return;
    }
    window
      .fetch("/locales/" + encodeURIComponent(lang) + ".json", { credentials: "same-origin" })
      .then(function (response) {
        return response.ok ? response.json() : null;
      })
      .then(function (tree) {
        if (tree) {
          flattenLocale(tree, "", strings);
        }
      })
      .catch(function () {
        /* the English fallbacks already in the code stay in effect */
        return null;
      });
  }

  function registerServiceWorker() {
    var installBanner = document.getElementById("pwa-install-banner");
    var updateBanner = document.getElementById("pwa-update-banner");
    var installButton = installBanner && installBanner.querySelector('[data-action="install-app"]');
    var offlineIndicator = document.getElementById("offline-indicator");
    var installPrompt = null;
    var waitingWorker = null;
    var updateAccepted = false;
    var reloading = false;
    var installDismissed = false;
    function reloadOnce() {
      if (reloading) return;
      reloading = true;
      window.location.reload();
    }
    function showWaitingUpdate(worker) {
      if (!worker || !navigator.serviceWorker.controller) return;
      waitingWorker = worker;
      if (updateBanner) updateBanner.hidden = false;
    }
    function updateApp() {
      updateAccepted = true;
      if (updateBanner) updateBanner.hidden = true;
      window.setTimeout(reloadOnce, 5000);
      navigator.serviceWorker.ready.then(function (registration) {
        if (registration.waiting) {
          waitingWorker = registration.waiting;
          waitingWorker.postMessage({ type: "SKIP_WAITING" });
        } else {
          reloadOnce();
        }
      }, reloadOnce);
    }
    function updateOnlineState() {
      if (offlineIndicator) offlineIndicator.hidden = navigator.onLine;
    }
    updateOnlineState();
    window.addEventListener("online", updateOnlineState);
    window.addEventListener("offline", updateOnlineState);
    window.addEventListener("beforeinstallprompt", function (event) {
      event.preventDefault();
      installPrompt = event;
      if (installButton) installButton.hidden = false;
      if (installBanner && !installDismissed && !window.matchMedia("(display-mode: standalone)").matches) installBanner.hidden = false;
    });
    window.addEventListener("appinstalled", function () {
      installPrompt = null;
      if (installBanner) installBanner.hidden = true;
    });
    if (window.location.protocol !== "https:" || !("serviceWorker" in navigator)) {
      return;
    }
    navigator.serviceWorker.addEventListener("controllerchange", function () {
      if (updateAccepted) reloadOnce();
    });
    var iosHelp = document.getElementById("pwa-ios-help");
    if (installBanner && /iPad|iPhone|iPod/.test(navigator.userAgent) && !navigator.standalone && !window.matchMedia("(display-mode: standalone)").matches && iosHelp) {
      iosHelp.hidden = false;
      installBanner.hidden = false;
      if (installButton) installButton.hidden = true;
    } else if (installButton) {
      installButton.hidden = true;
    }
    window.addEventListener("load", function () {
      navigator.serviceWorker.register("/sw.js", { scope: "/" }).then(function (registration) {
        window.setInterval(function () {
          registration.update().catch(function () { return null; });
        }, 60 * 60 * 1000);
        showWaitingUpdate(registration.waiting);
        registration.addEventListener("updatefound", function () {
          var worker = registration.installing;
          if (!worker) return;
          worker.addEventListener("statechange", function () {
            if (worker.state === "installed" && navigator.serviceWorker.controller) {
              showWaitingUpdate(worker);
            }
          });
        });
      }).catch(function () {
        /* The web app remains fully usable when offline support is unavailable. */
        return null;
      });
    });
    document.addEventListener("click", function (event) {
      var action = event.target.closest("[data-action]");
      if (!action) return;
      switch (action.getAttribute("data-action")) {
        case "install-app":
          if (installPrompt) {
            installPrompt.prompt();
            installPrompt.userChoice.then(function () { installPrompt = null; });
          }
          break;
        case "dismiss-install":
          installDismissed = true;
          if (installBanner) installBanner.hidden = true;
          break;
        case "update-app":
          updateApp();
          break;
        case "dismiss-update":
          if (updateBanner) updateBanner.hidden = true;
          break;
      }
    });
  }

  function enhanceAnnouncementDismissal() {
    document.querySelectorAll(".site-banner-dismiss").forEach(function (form) {
      form.addEventListener("submit", function (event) {
        event.preventDefault();
        var id = new FormData(form).get("id");
        if (!id) return;
        var cookie = document.cookie.match(/(?:^|;\s*)dismissed_announcements=([^;]*)/);
        var ids = cookie ? decodeURIComponent(cookie[1]).split(",").filter(Boolean) : [];
        if (ids.indexOf(id) === -1) ids.push(id);
        var secure = window.location.protocol === "https:" ? "; Secure" : "";
        document.cookie = "dismissed_announcements=" + encodeURIComponent(ids.join(",")) +
          "; path=/; max-age=31536000; samesite=lax" + secure;
        var banner = form.closest(".site-banner");
        if (banner) banner.remove();
      });
    });
  }

  // Announces a message to assistive technology through the polite live
  // region rendered by every page layout. Screen readers read the new
  // text without moving focus; sighted users are unaffected.
  function announce(message) {
    var region = document.getElementById("live-region");
    if (!region) {
      return;
    }
    region.textContent = "";
    window.setTimeout(function () {
      region.textContent = message;
    }, 50);
  }

  var toastCounter = 0;
  var toastQueue = [];
  var activeToasts = new Map();
  var maxVisibleToasts = 5;

  function toastDuration(kind) {
    if (kind === "error") return 0;
    if (kind === "warning") return 5000;
    var container = document.getElementById("toast-container");
    var configured = Number(container && container.getAttribute("data-duration"));
    return Number.isFinite(configured) && configured >= 0 ? configured * 1000 : 3000;
  }

  function renderToast(item) {
    var container = document.getElementById("toast-container");
    if (!container) return;
    var toast = document.createElement("div");
    toast.className = "toast toast-" + item.kind;
    toast.setAttribute("role", item.kind === "error" || item.kind === "warning" ? "alert" : "status");
    toast.setAttribute("aria-live", "polite");
    toast.setAttribute("data-toast-id", item.id);
    var icon = document.createElement("span");
    icon.className = "toast-icon";
    icon.setAttribute("aria-hidden", "true");
    icon.textContent = { success: "✓", error: "×", warning: "⚠", info: "ℹ" }[item.kind] || "ℹ";
    var text = document.createElement("span");
    text.className = "toast-message";
    text.textContent = item.message;
    var close = document.createElement("button");
    close.className = "toast-close";
    close.type = "button";
    close.setAttribute("aria-label", container.getAttribute("data-dismiss-label") || t("a11y.dismiss", "Dismiss"));
    close.textContent = "×";
    toast.appendChild(icon);
    toast.appendChild(text);
    toast.appendChild(close);
    var progress = document.createElement("div");
    progress.className = "toast-progress";
    toast.appendChild(progress);
    container.insertBefore(toast, container.firstChild);
    item.element = toast;
    item.remaining = item.duration;
    item.startedAt = Date.now();
    activeToasts.set(item.id, item);
    if (item.duration > 0) {
      progress.style.animationDuration = item.duration + "ms";
      item.timer = window.setTimeout(function () { dismissToast(item.id); }, item.duration);
    } else {
      progress.hidden = true;
    }
    toast.addEventListener("mouseenter", function () {
      if (!item.timer) return;
      window.clearTimeout(item.timer);
      item.timer = null;
      item.remaining = Math.max(0, item.remaining - (Date.now() - item.startedAt));
      progress.style.animationPlayState = "paused";
    });
    toast.addEventListener("mouseleave", function () {
      if (item.duration <= 0 || item.timer) return;
      item.startedAt = Date.now();
      progress.style.animationDelay = "-" + Math.max(0, item.duration - item.remaining) + "ms";
      progress.style.animationPlayState = "running";
      item.timer = window.setTimeout(function () { dismissToast(item.id); }, item.remaining);
    });
    toast.addEventListener("click", function (event) {
      event.preventDefault();
      dismissToast(item.id);
    });
  }

  function showToast(message, kind, duration) {
    var container = document.getElementById("toast-container");
    if (!container) return null;
    var type = ["success", "error", "warning", "info"].indexOf(kind) >= 0 ? kind : "info";
    var item = { id: "toast-" + (++toastCounter), message: String(message), kind: type,
      duration: Number.isFinite(duration) ? Math.max(0, duration) : toastDuration(type) };
    if (activeToasts.size >= maxVisibleToasts) toastQueue.push(item);
    else renderToast(item);
    return item.id;
  }

  function dismissToast(id) {
    var item = activeToasts.get(id);
    if (!item) {
      var queued = toastQueue.findIndex(function (candidate) { return candidate.id === id; });
      if (queued >= 0) toastQueue.splice(queued, 1);
      return;
    }
    if (item.timer) window.clearTimeout(item.timer);
    item.element.remove();
    activeToasts.delete(id);
    if (toastQueue.length > 0) renderToast(toastQueue.shift());
  }

  function dismissAllToasts() {
    activeToasts.forEach(function (item) {
      if (item.timer) window.clearTimeout(item.timer);
      item.element.remove();
    });
    activeToasts.clear();
    toastQueue = [];
  }

  window.showToast = showToast;
  window.dismissToast = dismissToast;
  window.dismissAllToasts = dismissAllToasts;

  document.addEventListener("keydown", function (event) {
    if (event.key === "Escape") {
      var container = document.getElementById("toast-container");
      var topToast = container && container.firstElementChild;
      if (topToast) dismissToast(topToast.getAttribute("data-toast-id"));
    }
  });

  function copyToClipboard(text) {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      return navigator.clipboard.writeText(text);
    }
    var input = document.createElement("textarea");
    input.value = text;
    input.setAttribute("readonly", "");
    input.style.position = "absolute";
    input.style.left = "-9999px";
    document.body.appendChild(input);
    input.select();
    try {
      document.execCommand("copy");
    } catch (err) {
      /* best-effort fallback only */
    }
    document.body.removeChild(input);
    return Promise.resolve();
  }

  var THEME_ICONS = { dark: "☽", light: "☀", auto: "🔄" };

  function currentThemeClass(root) {
    if (root.classList.contains("theme-light")) {
      return "light";
    }
    if (root.classList.contains("theme-auto")) {
      return "auto";
    }
    return "dark";
  }

  function applyTheme(theme) {
    var root = document.documentElement;
    root.classList.remove("theme-dark", "theme-light", "theme-auto");
    root.classList.add("theme-" + theme);

    var button = document.querySelector('[data-action="toggle-theme"]');
    if (button) {
      button.setAttribute("data-theme", theme);
      button.setAttribute("aria-label", "Theme: " + theme + " (select to switch between dark, light, and auto)");
      var icon = button.querySelector("[data-theme-icon]");
      if (icon) {
        icon.textContent = THEME_ICONS[theme] || THEME_ICONS.dark;
      }
    }
  }

  // toggleTheme changes only the current page view. Persistence uses the
  // consent-gated no-JavaScript form, per AI.md PART 16.
  function toggleTheme() {
    var order = ["dark", "light", "auto"];
    var current = currentThemeClass(document.documentElement);
    var next = order[(order.indexOf(current) + 1) % order.length];
    applyTheme(next);
  }

  function openDialog(id) {
    var dialog = document.getElementById(id);
    if (dialog && typeof dialog.showModal === "function") {
      dialog.showModal();
    }
  }

  function closeDialog(dialog) {
    if (dialog && typeof dialog.close === "function") {
      dialog.close();
    }
  }

  document.addEventListener("click", function (event) {
    var el = event.target.closest("[data-action]");
    if (!el) {
      return;
    }
    var action = el.getAttribute("data-action");

    switch (action) {
      case "toggle-theme":
        toggleTheme();
        break;
      // Nav toggle is a CSS-only checkbox/label pair (nav.tmpl) — no JS
      // wiring needed, so navigation still works with JS disabled.
      // The footer trigger is a real link to /server/privacy#cookies so the
      // preferences stay reachable without JS; with JS the dialog opens
      // instead of navigating.
      case "open-consent-preferences":
        event.preventDefault();
        openDialog("consent-preferences-dialog");
        break;
      case "close-dialog":
        closeDialog(el.closest("dialog"));
        break;
      default:
        break;
    }

    if (el.hasAttribute("data-copy")) {
      var value = el.getAttribute("data-copy");
      copyToClipboard(value).then(function () {
        var copied = t("a11y.copied_to_clipboard", "Copied to clipboard");
        showToast(copied, "success");
        announce(copied);
      }, function () {
        var failed = t("a11y.copy_failed", "Could not copy — please copy manually");
        showToast(failed, "error");
        announce(failed);
      });
    }
  });

  enhanceAnnouncementDismissal();

  // The cookie-consent banner is rendered server-side only when the request
  // carries no cookie_consent cookie (AI.md PART 16 "Server-side behavior":
  // no display:none, no reveal script), so there is nothing for JS to show
  // or hide here — its forms POST to /server/consent and work without JS.
  document.addEventListener("DOMContentLoaded", function () {
    loadLocale();
    registerServiceWorker();

    // After a successful link creation (server-rendered success card),
    // offer a one-click copy of the new short URL.
    var successTitle = document.getElementById("home-success-title");
    var successCopy = successTitle ? successTitle.closest(".section-card").querySelector("[data-copy]") : null;
    if (successCopy) {
      successCopy.focus({ preventScroll: true });
    }

  });
})();
