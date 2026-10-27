(function () {
  "use strict";

  document.querySelectorAll("[data-copy]").forEach(function (button) {
    button.addEventListener("click", async function () {
      var target = document.querySelector(button.getAttribute("data-copy"));
      if (!target) return;
      var text = target.textContent.trim();
      try {
        await navigator.clipboard.writeText(text);
        var old = button.getAttribute("aria-label");
        button.setAttribute("aria-label", "Copied");
        button.classList.add("is-copied");
        window.setTimeout(function () {
          button.setAttribute("aria-label", old || "Copy");
          button.classList.remove("is-copied");
        }, 1400);
      } catch (err) {
        window.prompt("Copy password", text);
      }
    });
  });

  document.querySelectorAll("form[data-confirm]").forEach(function (form) {
    form.addEventListener("submit", function (event) {
      if (!window.confirm(form.getAttribute("data-confirm"))) {
        event.preventDefault();
      }
    });
  });
})();
