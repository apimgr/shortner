// Vanilla JS progressive enhancement, per AI.md PART 16 "JavaScript
// Structure": ONE file, no framework, no build step. Every feature this
// file touches already works without JavaScript (native form POSTs,
// server-rendered success states) — this only improves the experience.
// All hooks are data-action attributes, never inline onclick.
(function () {
  "use strict";

  function showToast(message, kind) {
    var container = document.getElementById("toast-container");
    if (!container) {
      return;
    }
    var toast = document.createElement("div");
    toast.className = "toast" + (kind ? " toast-" + kind : "");
    toast.setAttribute("role", "status");
    toast.textContent = message;
    container.appendChild(toast);
    window.setTimeout(function () {
      toast.remove();
    }, 4000);
  }

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

  function setThemeCookie(theme) {
    var maxAge = 365 * 24 * 60 * 60;
    document.cookie = "theme=" + theme + "; path=/; max-age=" + maxAge + "; samesite=lax";
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

  // toggleTheme cycles dark -> light -> auto -> dark, per AI.md PART 16
  // "Theme Switching": no page reload — the class on <html> is swapped
  // directly and the theme cookie is set so the next server render (and
  // any no-JS navigation) picks up the same choice.
  function toggleTheme() {
    var order = ["dark", "light", "auto"];
    var current = currentThemeClass(document.documentElement);
    var next = order[(order.indexOf(current) + 1) % order.length];
    applyTheme(next);
    setThemeCookie(next);
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
        showToast("Copied to clipboard", "success");
      }, function () {
        showToast("Could not copy — please copy manually", "error");
      });
    }
  });

  // The cookie-consent banner is rendered server-side only when the request
  // carries no cookie_consent cookie (AI.md PART 16 "Server-side behavior":
  // no display:none, no reveal script), so there is nothing for JS to show
  // or hide here — its forms POST to /server/consent and work without JS.
  document.addEventListener("DOMContentLoaded", function () {
    // After a successful link creation (server-rendered success card),
    // offer a one-click copy of the new short URL.
    var successCopy = document.querySelector(".success-card [data-copy]");
    if (successCopy) {
      successCopy.focus({ preventScroll: true });
    }
  });
})();
