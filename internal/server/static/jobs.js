(function () {
  "use strict";

  document.querySelectorAll("[data-job-form]").forEach(function (form) {
    var mode = form.querySelector("select[name='mode']");
    if (!mode) return;

    function updateExecutionFields() {
      form.querySelectorAll("[data-execution-fields]").forEach(function (fields) {
        var active = fields.getAttribute("data-execution-fields") === mode.value;
        fields.hidden = !active;
        fields.querySelectorAll("input, textarea, select").forEach(function (control) {
          control.disabled = !active;
          control.required = active && control.hasAttribute("data-execution-required");
        });
      });
    }

    updateExecutionFields();
    mode.addEventListener("change", updateExecutionFields);
  });

  var error = document.getElementById("job-form-error");
  if (error) {
    error.focus();
    error.scrollIntoView({ block: "center" });
  }
}());
