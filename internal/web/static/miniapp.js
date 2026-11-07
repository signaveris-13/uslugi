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

  // С какого экрана начать: ?s=… в ссылке бота или start_param прямой ссылки.
  // l_12 — карточка, c_5 — категория, e_12 — своё объявление, my, profile, new.
  function startPath() {
    var qs = new URLSearchParams(location.search);
    var p = qs.get("s") || (tg && tg.initDataUnsafe && tg.initDataUnsafe.start_param) || "";
    var m;
    if ((m = /^l_(\d+)$/.exec(p))) return "/app/l/" + m[1];
    if ((m = /^c_(\d+)$/.exec(p))) return "/app/c/" + m[1];
    if ((m = /^(?:e|edit)_(\d+)$/.exec(p))) return "/app/listings/" + m[1];
    if (p === "my") return "/app/my";
    if (p === "profile") return "/app/profile";
    if (p === "support") return "/app/support";
    if (p === "reviews") return "/app/reviews";
    if ((m = /^support_(\d+)$/.exec(p))) return "/app/support/" + m[1];
    var legacy = qs.get("listing");
    if (legacy && /^\d+$/.test(legacy)) return "/app/listings/" + legacy;
    return "/app/home";
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

  // Сервер просит открыть чат мастера: Telegram — внутри Telegram, остальное — снаружи.
  document.body.addEventListener("app-open", function (ev) {
    var u = ev.detail && ev.detail.url;
    if (!u) return;
    if (ev.detail.text) toast(ev.detail.text);
    if (u.indexOf("https://t.me/") === 0 && tg && tg.openTelegramLink) tg.openTelegramLink(u);
    else if (u.indexOf("tel:") === 0) window.location.href = u;
    else if (tg && tg.openLink) tg.openLink(u);
    else window.open(u, "_blank");
  });

  document.body.addEventListener("app-toast", function (ev) {
    if (ev.detail && ev.detail.text) toast(ev.detail.text);
  });

  // Подтверждение — нативным окном Telegram, а не window.confirm.
  document.body.addEventListener("htmx:confirm", function (ev) {
    var q = ev.detail.question;
    if (!q) return;
    ev.preventDefault();
    if (tg && tg.showConfirm) tg.showConfirm(q, function (ok) { if (ok) ev.detail.issueRequest(true); });
    else if (window.confirm(q)) ev.detail.issueRequest(true);
  });

  document.body.addEventListener("click", function (ev) {
    var b = ev.target.closest("[data-share]");
    if (!b) return;
    var u = b.getAttribute("data-share");
    if (tg && tg.openTelegramLink) tg.openTelegramLink(u);
    else window.open(u, "_blank");
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
      var back = root.querySelector("[aria-label=\"Назад\"]");
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

  var keepScroll = false;
  document.body.addEventListener("htmx:beforeRequest", function (ev) {
    keepScroll = !!(ev.detail.elt && ev.detail.elt.hasAttribute && ev.detail.elt.hasAttribute("data-keep-scroll"));
  });
  document.body.addEventListener("htmx:afterSwap", function (ev) {
    if (ev.detail.target.id === "app") {
      var y = window.scrollY;
      window.scrollTo(0, keepScroll ? y : 0);
      syncScreen(ev.detail.target);
      var f = ev.detail.target.querySelector("[data-autofocus]");
      if (f && !f.value) f.focus();
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
    if (ev.key === "Enter" && !ev.target.closest(".composer") && ev.target.matches("form input:not([type=checkbox]):not([type=radio]):not([type=search])")) {
      ev.preventDefault();
      ev.target.blur();
    }
  });

  if (token) open(startPath());
  else login(function () { open(startPath()); });
})();
