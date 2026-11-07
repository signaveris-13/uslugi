// Mini App исполнителя: вход через initData Telegram, токен — в заголовке каждого запроса htmx.
(function () {
  var tg = window.Telegram && window.Telegram.WebApp;
  var token = null;
  try { token = sessionStorage.getItem("uslugi-app-token"); } catch (e) {}

  if (tg) {
    tg.ready();
    tg.expand();
    try { tg.setHeaderColor("#FFFFFF"); tg.setBackgroundColor("#F4F4F5"); } catch (e) {}
  }

  function status(text) {
    var el = document.getElementById("app-status");
    if (el) el.textContent = text;
  }

  function toast(text) {
    var el = document.createElement("div");
    el.className = "toast";
    el.textContent = text;
    document.body.appendChild(el);
    setTimeout(function () { el.remove(); }, 3500);
  }

  function saveToken(t) {
    token = t;
    try { sessionStorage.setItem("uslugi-app-token", t); } catch (e) {}
  }

  function auth(body) {
    return fetch("/app/auth", { method: "POST", body: body })
      .then(function (r) { return r.json().then(function (j) { return { ok: r.ok, j: j }; }); })
      .then(function (res) {
        if (!res.ok) throw res.j;
        saveToken(res.j.token);
      });
  }

  function open(path) {
    htmx.ajax("GET", path || "/app/my", { target: "#app", swap: "innerHTML" });
  }

  function login(then) {
    if (tg && tg.initData) {
      var b = new FormData();
      b.append("init_data", tg.initData);
      return auth(b).then(then).catch(function (e) { status((e && e.error) || "Не получилось войти."); });
    }
    var dev = document.getElementById("dev-login");
    if (dev) {
      dev.hidden = false;
      status("");
      dev.onsubmit = function (ev) {
        ev.preventDefault();
        auth(new FormData(dev)).then(then).catch(function (e) { toast((e && e.error) || "Не получилось войти."); });
      };
      return;
    }
    status("Открой приложение из бота в Telegram.");
  }

  function startPath() {
    var p = tg && tg.initDataUnsafe && tg.initDataUnsafe.start_param;
    if (p && /^edit_\d+$/.test(p)) return "/app/listings/" + p.slice(5);
    var q = new URLSearchParams(location.search).get("listing");
    if (q && /^\d+$/.test(q)) return "/app/listings/" + q;
    return "/app/my";
  }

  document.body.addEventListener("htmx:configRequest", function (ev) {
    if (token) ev.detail.headers["X-App-Token"] = token;
  });

  // Токен протух — входим заново и открываем список.
  document.body.addEventListener("app-reauth", function () {
    token = null;
    try { sessionStorage.removeItem("uslugi-app-token"); } catch (e) {}
    login(function () { open("/app/my"); });
  });

  document.body.addEventListener("htmx:responseError", function (ev) {
    if (ev.detail.xhr.status !== 401) toast("Что-то пошло не так. Попробуй ещё раз.");
  });
  document.body.addEventListener("htmx:sendError", function () {
    toast("Нет связи. Проверь интернет.");
  });

  function syncScreen(root) {
    // Кнопка «Назад» Telegram повторяет стрелку в шапке.
    if (tg && tg.BackButton) {
      var back = root.querySelector(".app-head .icon-btn");
      tg.BackButton.offClick(syncScreen.onBack || function () {});
      if (back) {
        syncScreen.onBack = function () { back.click(); };
        tg.BackButton.onClick(syncScreen.onBack);
        tg.BackButton.show();
      } else {
        tg.BackButton.hide();
      }
    }
    // «От …» и «Точная» требуют сумму, «Договорная» — нет.
    var row = root.querySelector("[data-hide-when-negotiable]");
    if (row) {
      var form = row.closest("form");
      var update = function () {
        var c = form.querySelector("input[name=price_type]:checked");
        row.classList.toggle("is-hidden", !!c && c.value === "negotiable");
      };
      form.querySelectorAll("input[name=price_type]").forEach(function (i) { i.addEventListener("change", update); });
      update();
    }
  }

  document.body.addEventListener("htmx:afterSwap", function (ev) {
    if (ev.detail.target.id === "app") {
      window.scrollTo(0, 0);
      syncScreen(ev.detail.target);
    }
  });

  document.body.addEventListener("change", function (ev) {
    var t = ev.target;
    // Фото загружаем сразу после выбора.
    if (t.matches("input[type=file][data-autoupload]") && t.files.length) {
      t.form.requestSubmit();
      return;
    }
    // Ограничение на число отмеченных чипов (дополнительные подкатегории).
    var box = t.closest("[data-max]");
    if (box && t.checked) {
      var max = parseInt(box.getAttribute("data-max"), 10);
      if (box.querySelectorAll("input:checked").length > max) {
        t.checked = false;
        toast("Можно выбрать не больше " + max + ".");
      }
    }
  });

  // Enter в однострочном поле не отправляет форму — только прячет клавиатуру.
  document.body.addEventListener("keydown", function (ev) {
    if (ev.key === "Enter" && ev.target.matches("form input:not([type=checkbox]):not([type=radio])")) {
      ev.preventDefault();
      ev.target.blur();
    }
  });

  if (token) open(startPath());
  else login(function () { open(startPath()); });
})();
