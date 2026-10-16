(function () {
  "use strict";
  var kind = document.querySelector("#cycle-kind");
  var quiet = document.querySelector("#quiet-enabled");

  function updateScheduleFields() {
    if (!kind) return;
    document.querySelectorAll("[data-schedule-fields]").forEach(function (fields) {
      var kinds = fields.getAttribute("data-schedule-fields").split(" ");
      fields.hidden = kinds.indexOf(kind.value) === -1;
    });
  }

  function updateQuietFields() {
    if (!quiet) return;
    document.querySelectorAll("[data-quiet-fields]").forEach(function (fields) {
      fields.hidden = quiet.value !== "true";
    });
  }

  if (kind) {
    updateScheduleFields();
    kind.addEventListener("change", updateScheduleFields);
  }
  if (quiet) {
    updateQuietFields();
    quiet.addEventListener("change", updateQuietFields);
  }

  var countdown = document.querySelector("[data-poweroff-countdown]");
  if (countdown) {
    var remaining = Number(countdown.getAttribute("data-poweroff-countdown"));
    var value = countdown.querySelector("[data-countdown-value]");
    var status = countdown.querySelector("[data-countdown-status]");
    window.setInterval(function () {
      if (remaining > 0) remaining -= 1;
      if (value) value.textContent = String(remaining);
      if (remaining === 0 && status) status.textContent = "Poweroff is now expected. Connection loss is normal.";
    }, 1000);
    return;
  }
  var state = document.querySelector("[data-duty-cycle-active='true']");
  if (!state) return;
  var automaticRefreshAllowed = !window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  function stopAutomaticRefresh() {
    automaticRefreshAllowed = false;
  }
  document.addEventListener("focusin", stopAutomaticRefresh, { once: true });
  document.addEventListener("keydown", stopAutomaticRefresh, { once: true });
  document.addEventListener("pointerdown", stopAutomaticRefresh, { once: true });
  document.addEventListener("scroll", stopAutomaticRefresh, { once: true, passive: true });
  function refreshWhenVisible() {
    if (!automaticRefreshAllowed) return;
    if (document.hidden) {
      window.setTimeout(refreshWhenVisible, 5000);
      return;
    }
    window.location.reload();
  }
  window.setTimeout(refreshWhenVisible, 5000);
}());
