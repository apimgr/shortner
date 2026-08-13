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

  function toggleTheme() {
    var root = document.documentElement;
    var current = root.getAttribute("data-theme");
    var prefersDark = window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches;
    var effectiveCurrent = current === "light" || current === "dark" ? current : (prefersDark ? "dark" : "light");
    var next = effectiveCurrent === "dark" ? "light" : "dark";
    root.setAttribute("data-theme", next);
    setThemeCookie(next);
  }

  function toggleNav(button) {
    var target = document.getElementById(button.getAttribute("aria-controls"));
    if (!target) {
      return;
    }
    var isOpen = target.classList.toggle("is-open");
    button.setAttribute("aria-expanded", isOpen ? "true" : "false");
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
      case "toggle-nav":
        toggleNav(el);
        break;
      case "open-consent-preferences":
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

  // Cookie-consent banner: shown only when no consent cookie exists yet
  // (server sets/reads cookie_consent; on first paint before any request
  // roundtrip we just check document.cookie so the banner never flashes
  // for returning visitors after JS loads).
  document.addEventListener("DOMContentLoaded", function () {
    var banner = document.getElementById("cookie-consent");
    if (banner && document.cookie.indexOf("cookie_consent=") === -1) {
      banner.hidden = false;
    }

    // After a successful link creation (server-rendered success card),
    // offer a one-click copy of the new short URL.
    var successCopy = document.querySelector(".success-card [data-copy]");
    if (successCopy) {
      successCopy.focus({ preventScroll: true });
    }
  });
})();
