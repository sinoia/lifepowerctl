(function () {
  "use strict";

  document.addEventListener("submit", function (event) {
    var form = event.target.closest("form[data-busy-form]");
    if (!form || event.defaultPrevented || !form.checkValidity()) return;

    var button = event.submitter || form.querySelector("[data-submit-button]");
    var scope = form.closest("[data-operation-scope]") || form;
    var status = scope.querySelector("[data-operation-status]");
    var label = form.getAttribute("data-busy-label") || "Working…";

    if (form.dataset.submitting === "true") {
      event.preventDefault();
      return;
    }
    form.dataset.submitting = "true";
    scope.setAttribute("aria-busy", "true");

    if (button && button.name) {
      var action = document.createElement("input");
      action.type = "hidden";
      action.name = button.name;
      action.value = button.value;
      form.appendChild(action);
    }
    form.querySelectorAll("button, input[type='submit']").forEach(function (item) {
      item.disabled = true;
    });
    if (button && button.tagName === "BUTTON") {
      var labelNode = button.querySelector("[data-button-label]");
      if (!labelNode) {
        labelNode = document.createElement("span");
        labelNode.dataset.buttonLabel = "";
        labelNode.textContent = button.textContent.trim();
        button.replaceChildren(labelNode);
      }
      labelNode.textContent = label;
      var spinner = document.createElement("span");
      spinner.className = "spinner";
      spinner.setAttribute("aria-hidden", "true");
      button.prepend(spinner);
      button.classList.add("button-busy");
    }
    if (status) status.textContent = "Request submitted; waiting for completion.";
  });
}());
