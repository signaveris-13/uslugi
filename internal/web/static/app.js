// Кнопки с data-copy="#selector" копируют значение поля в буфер обмена.
document.addEventListener("click", function (e) {
  var btn = e.target.closest("[data-copy]");
  if (!btn) return;
  var field = document.querySelector(btn.getAttribute("data-copy"));
  if (!field) return;
  field.select();
  var done = function () {
    var text = btn.textContent;
    btn.textContent = "Скопировано";
    setTimeout(function () { btn.textContent = text; }, 1500);
  };
  if (navigator.clipboard) {
    navigator.clipboard.writeText(field.value).then(done);
  } else {
    document.execCommand("copy");
    done();
  }
});
