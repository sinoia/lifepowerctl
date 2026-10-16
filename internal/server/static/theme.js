(function () {
  "use strict";

  var storageKey = "lifepowerctl-theme";
  var allowed = ["auto", "dark", "light"];
  var selected = "auto";

  try {
    selected = localStorage.getItem(storageKey) || "auto";
  } catch (_) {}
  if (allowed.indexOf(selected) === -1) selected = "auto";

  function apply(theme) {
    if (theme === "auto") {
      document.documentElement.removeAttribute("data-theme");
    } else {
      document.documentElement.setAttribute("data-theme", theme);
    }
  }

  function selectTheme(theme) {
    selected = allowed.indexOf(theme) === -1 ? "auto" : theme;
    apply(selected);
    try { localStorage.setItem(storageKey, selected); } catch (_) {}
    document.querySelectorAll("[data-theme-choice]").forEach(function (choice) {
      choice.checked = choice.value === selected;
    });
  }

  apply(selected);
  document.addEventListener("DOMContentLoaded", function () {
    document.querySelectorAll("[data-theme-choice]").forEach(function (choice) {
      choice.checked = choice.value === selected;
      choice.addEventListener("change", function () {
        if (!choice.checked) return;
        selectTheme(choice.value);
        var menu = choice.closest("[data-settings-menu]");
        if (menu) menu.open = false;
      });
    });
    document.addEventListener("click", function (event) {
      document.querySelectorAll("[data-settings-menu][open]").forEach(function (menu) {
        if (!menu.contains(event.target)) menu.open = false;
      });
    });
    document.addEventListener("keydown", function (event) {
      if (event.key !== "Escape") return;
      document.querySelectorAll("[data-settings-menu][open]").forEach(function (menu) {
        menu.open = false;
        var trigger = menu.querySelector("summary");
        if (trigger) trigger.focus();
      });
    });
  });
}());
