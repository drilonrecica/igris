// igris web UI (SPEC §15.8): follows the run's event stream, fetches the
// page's live part again when something happens, reads the active tasks'
// tails while the page is visible, and sends the forms as protocol-1
// commands. Without it the page is the snapshot of its load and the forms
// post to /act.
"use strict";

(function () {
  var root = document.documentElement;
  root.classList.add("js"); // the stylesheet collapses fields and draws the sheet only with the script
  var csrfMeta = document.querySelector('meta[name="igris-csrf"]');
  var csrf = csrfMeta ? csrfMeta.content : "";
  var live = document.getElementById("live");
  var conn = document.getElementById("conn");
  var flash = document.getElementById("flash");
  var seq = 0;
  var tails = {}; // task → its last lines, kept across refreshes
  var stale = false; // an update waits until the owner stops typing
  var refreshing = false;
  var timer = 0;
  var stream = null;
  var flashTimer = 0;
  // over: the run ended, nothing left to read or refresh; a page loaded
  // after the end has no stream to follow (serve ended with the run).
  var over = live.dataset.ended === "1";
  var stopping = false; // this page confirmed Stop: Stopping until the end
  var sheetOpen = true; // the sheet is open, else a peek strip
  var sheetKey = ""; // the entry the sheet shows, kept across refreshes
  var seen = {}; // the entries the sheet has shown: a new one opens it
  var liveURL = live.dataset.live || "/live"; // the run's live part, or the plan's

  // collapse disables the text field of every form that hasn't been
  // opened: a disabled control is left out of the browser's own
  // validation, which would otherwise refuse to submit a hidden, empty,
  // required field before this script ever hears of the tap.
  function collapse() {
    var forms = live.querySelectorAll("form.has-field");
    for (var i = 0; i < forms.length; i++) {
      var field = forms[i].querySelector("input[type=text], textarea");
      if (field) {
        field.disabled = !forms[i].classList.contains("open");
      }
    }
  }

  // sheet draws what needs the owner as the sheet, one entry at a time
  // (SPEC §15.8): the one it showed before a refresh if it is still
  // there, else the first. It opens by itself when something new needs
  // the owner and otherwise stays as the owner left it: open over the
  // dimmed page, or folded to a peek strip above the thumb bar.
  function sheet() {
    var asks = over ? [] : Array.prototype.slice.call(live.querySelectorAll("section.question"));
    var at = 0;
    asks.forEach(function (q, i) {
      if (!seen[q.dataset.key]) {
        seen[q.dataset.key] = true;
        sheetOpen = true;
      }
      if (q.dataset.key === sheetKey) {
        at = i;
      }
    });
    sheetKey = asks.length > 0 ? asks[at].dataset.key : "";
    asks.forEach(function (q, i) {
      q.classList.toggle("sheet", i === at);
      q.classList.toggle("peek", i === at && !sheetOpen);
    });
    var bar = live.querySelector("nav.bar");
    if (bar) {
      root.style.setProperty("--bar", bar.offsetHeight + "px"); // the sheet sits on it
    }
    document.body.classList.toggle("has-sheet", asks.length > 0 && sheetOpen);
    document.body.classList.toggle("has-peek", asks.length > 0 && !sheetOpen);
  }

  // showSheet opens the sheet at the entry with id, or folds it.
  function showSheet(id, open) {
    var q = id ? document.getElementById(id) : null;
    if (q && q.dataset.key) {
      sheetKey = q.dataset.key;
    }
    sheetOpen = open;
    sheet();
    var now = live.querySelector("section.question.sheet");
    if (now) {
      now.scrollTop = 0;
      var to = now.querySelector(open ? "h2" : "button.peek");
      if (to) {
        to.setAttribute("tabindex", "-1");
        to.focus({ preventScroll: true });
      }
    }
  }

  // stopped shows Stopping on the page that confirmed Stop: the protocol
  // has no event for it, so the other pages say Working until the end.
  function stopped() {
    var word = live.querySelector(".glance .word");
    var line = live.querySelector(".glance .line");
    if (word) {
      word.textContent = "Stopping";
      word.className = "word plain";
    }
    if (line) {
      line.textContent = "the sessions stay open";
    }
  }

  function id() {
    seq += 1;
    return "web-" + Date.now().toString(36) + "-" + seq;
  }

  function say(text, isError) {
    flash.textContent = text;
    flash.className = isError ? "error" : "";
    flash.hidden = false;
    clearTimeout(flashTimer);
    flashTimer = setTimeout(function () { flash.hidden = true; }, isError ? 10000 : 3000);
  }

  function post(msg) {
    msg.v = 1;
    msg.id = id();
    return fetch("/api/v1/command", {
      method: "POST",
      credentials: "same-origin",
      headers: { "Content-Type": "application/json", "X-Igris-CSRF": csrf },
      body: JSON.stringify(msg)
    }).then(function (r) {
      return r.json().catch(function () { return { type: "error", error: "HTTP " + r.status }; });
    });
  }

  // busy: the owner is in a field or a confirmation, which a refresh
  // would take away.
  function busy() {
    var a = document.activeElement;
    if (a && live.contains(a) && (a.tagName === "INPUT" || a.tagName === "TEXTAREA" || a.tagName === "SELECT")) {
      return true;
    }
    return live.querySelector("form.confirming") !== null;
  }

  // formKey names a form by its hidden fields, the same after a refresh.
  function formKey(form) {
    var parts = [];
    var hidden = form.querySelectorAll("input[type=hidden]");
    for (var i = 0; i < hidden.length; i++) {
      if (hidden[i].name !== "csrf") {
        parts.push(hidden[i].name + "=" + hidden[i].value);
      }
    }
    return parts.join("&");
  }

  // typed keeps what the owner typed into the forms, and which fields
  // are open, put back after a refresh.
  function typed() {
    var kept = {};
    var fields = live.querySelectorAll("form.cmd input[type=text], form.cmd textarea");
    for (var i = 0; i < fields.length; i++) {
      var open = fields[i].form.classList.contains("open");
      if (fields[i].value !== "" || open) {
        kept[formKey(fields[i].form)] = { value: fields[i].value, open: open };
      }
    }
    return kept;
  }

  function retype(kept) {
    var fields = live.querySelectorAll("form.cmd input[type=text], form.cmd textarea");
    for (var i = 0; i < fields.length; i++) {
      var k = kept[formKey(fields[i].form)];
      if (k !== undefined) {
        fields[i].value = k.value;
        fields[i].form.classList.toggle("open", k.open);
      }
    }
    collapse();
  }

  function refresh() {
    if (over) {
      return;
    }
    if (refreshing) {
      stale = true;
      return;
    }
    if (busy()) {
      stale = true;
      return;
    }
    stale = false;
    refreshing = true;
    fetch(liveURL, { credentials: "same-origin" }).then(function (r) {
      if (r.status === 401) {
        throw new Error("this device is not paired any more: open the pairing URL `igris serve token` prints");
      }
      if (!r.ok) {
        throw new Error("the page could not be updated (HTTP " + r.status + ")");
      }
      return r.text();
    }).then(function (html) {
      if (over) {
        return; // ended while this was on its way
      }
      var doc = new DOMParser().parseFromString("<!doctype html><body>" + html, "text/html");
      var kept = typed();
      live.replaceChildren.apply(live, Array.prototype.slice.call(doc.body.childNodes));
      retype(kept);
      fillTails();
      tick();
      sheet();
      if (stopping) {
        stopped();
      }
    }).catch(function (e) {
      say(e.message, true);
    }).then(function () {
      refreshing = false;
      if (stale) {
        later();
      }
    });
  }

  function later() {
    clearTimeout(timer);
    timer = setTimeout(refresh, 300);
  }

  // fillTails shows each card's last lines, scrolled to the newest unless
  // the reader has scrolled up to read.
  function fillTails() {
    var pres = live.querySelectorAll("pre.tail");
    for (var i = 0; i < pres.length; i++) {
      var pre = pres[i];
      var lines = tails[pre.dataset.task];
      if (lines) {
        var follow = pre.scrollHeight - pre.scrollTop - pre.clientHeight < 8;
        pre.textContent = lines.join("\n");
        if (follow) {
          pre.scrollTop = pre.scrollHeight;
        }
      }
    }
  }

  // readTails asks for each active task's tail in turn, every few seconds
  // while the page is visible.
  function readTails() {
    if (over) {
      return;
    }
    if (document.visibilityState !== "visible") {
      setTimeout(readTails, 4000);
      return;
    }
    var pres = Array.prototype.slice.call(live.querySelectorAll("pre.tail"));
    var next = function () {
      var pre = pres.shift();
      if (!pre) {
        setTimeout(readTails, 4000);
        return;
      }
      var task = pre.dataset.task;
      post({ type: "tail", task: task, n: 40 }).then(function (rep) {
        if (rep.type === "tail") {
          tails[task] = rep.lines || [];
          fillTails();
        }
      }).catch(function () {}).then(next);
    };
    next();
  }

  // duration writes s seconds as internal/look does: the two largest
  // units, no spaces (45s, 4m12s, 1h47m, 2d3h).
  function duration(s) {
    if (s < 60) {
      return s + "s";
    }
    if (s < 3600) {
      return Math.floor(s / 60) + "m" + String(s % 60).padStart(2, "0") + "s";
    }
    if (s < 86400) {
      return Math.floor(s / 3600) + "h" + String(Math.floor(s / 60) % 60).padStart(2, "0") + "m";
    }
    return Math.floor(s / 86400) + "d" + Math.floor(s / 3600) % 24 + "h";
  }

  // age writes how long ago s seconds were, in one unit (25m ago).
  function age(s) {
    if (s < 60) {
      return s + "s ago";
    }
    if (s < 3600) {
      return Math.floor(s / 60) + "m ago";
    }
    if (s < 86400) {
      return Math.floor(s / 3600) + "h ago";
    }
    return Math.floor(s / 86400) + "d ago";
  }

  // tick keeps how long each task has run and how long ago each event
  // was current between refreshes, and stops with the run. An event's
  // title is its clock time in the device's zone.
  function tick() {
    if (over) {
      return;
    }
    var now = Date.now();
    var since = function (iso) {
      return Math.max(0, Math.floor((now - Date.parse(iso)) / 1000));
    };
    var spans = live.querySelectorAll(".ran[data-started]");
    for (var i = 0; i < spans.length; i++) {
      spans[i].textContent = duration(since(spans[i].dataset.started));
    }
    var times = live.querySelectorAll("time[datetime]");
    for (var j = 0; j < times.length; j++) {
      var iso = times[j].getAttribute("datetime");
      var d = new Date(iso);
      if (!isNaN(d)) {
        times[j].textContent = age(since(iso));
        times[j].title = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hour12: false });
      }
    }
  }

  function follow() {
    stream = new EventSource("/api/v1/events");
    stream.onopen = function () { conn.hidden = true; root.classList.remove("down"); };
    stream.onerror = function () { conn.hidden = false; root.classList.add("down"); };
    stream.onmessage = function (e) {
      var m;
      try {
        m = JSON.parse(e.data);
      } catch (err) {
        return;
      }
      conn.hidden = true;
      root.classList.remove("down");
      if (m.type === "tokens" || m.type === "quota") {
        return; // counters the page doesn't show
      }
      if (m.type === "snapshot") {
        if (m.ended) {
          ended(m.end); // opened after the end: nothing to follow
          return;
        }
        refresh(); // the first message, also after a reconnect
        return;
      }
      if (m.type === "run_stopped") {
        ended(m.detail);
        return;
      }
      later();
    };
  }

  // endLine is the outcome after the word Done, with its glyph and the
  // word's tone, as the server's (internal/look).
  function endLine(outcome) {
    switch (outcome) {
      case "completed": return ["✓ completed", ""];
      case "stuck": return ["▲ stuck", "ember"];
      case "failed":
      case "error": return ["✗ failed", "ember"];
    }
    return ["– stopped", "plain"];
  }

  // ended shows the run's end as the page of an ended run does: serve
  // ends with the run, so there is no page left to fetch. Nothing can be
  // sent any more, so the forms, the sheet and the bar go, and the status
  // word becomes the outcome.
  function ended(outcome) {
    over = true;
    stream.close();
    clearTimeout(timer);
    conn.hidden = true;
    root.classList.add("over");
    root.classList.remove("down");
    sheet();
    var gone = live.querySelectorAll("form.cmd, section.question, .actions, details, section[aria-label='Run mode'], nav.bar, section.next, .dot, a.to-sheet");
    for (var i = 0; i < gone.length; i++) {
      gone[i].remove();
    }
    var end = endLine(outcome);
    var word = live.querySelector(".glance .word");
    if (word) {
      word.textContent = "Done";
      word.className = "word" + (end[1] ? " " + end[1] : "");
    }
    var line = live.querySelector(".glance .line");
    if (line) {
      line.textContent = end[0];
    }
  }

  function confirming(form, on) {
    var box = form.querySelector(".confirm");
    var go = form.querySelector("button.go");
    if (!box || !go) {
      return;
    }
    box.hidden = !on;
    go.hidden = on;
    form.classList.toggle("confirming", on);
    if (!on && stale) {
      later();
    }
  }

  function send(form, submitter) {
    var data = new FormData(form);
    var msg = { type: data.get("type") };
    ["task", "question", "plan", "text"].forEach(function (k) {
      var v = data.get(k);
      if (v) {
        msg[k] = v;
      }
    });
    if (data.get("continue") === "1") {
      msg.continue = true;
    }
    if (data.get("yes") === "1") {
      msg.yes = true;
    }
    var buttons = form.querySelectorAll("button");
    buttons.forEach(function (b) { b.disabled = true; });
    post(msg).then(function (rep) {
      if (rep.type === "error") {
        say("Not sent: " + rep.error, true);
        return;
      }
      say("Sent.", false);
      if (msg.type === "stop") {
        stopping = true;
        stopped();
      }
      form.reset();
      form.classList.remove("open");
      collapse();
      confirming(form, false);
    }).catch(function () {
      say("Not sent: the run can't be reached", true);
    }).then(function () {
      buttons.forEach(function (b) { b.disabled = false; });
      if (document.activeElement === submitter) {
        submitter.blur();
      }
      later();
    });
  }

  live.addEventListener("submit", function (e) {
    var form = e.target;
    if (!form.classList || !form.classList.contains("cmd")) {
      return;
    }
    e.preventDefault();
    var submitter = e.submitter;
    if (form.classList.contains("has-field") && !form.classList.contains("open")) {
      // The first tap opens the field (collapsed with the script on);
      // the next sends, or asks the second confirmation.
      form.classList.add("open");
      var field = form.querySelector("input[type=text], textarea");
      if (field) {
        field.disabled = false;
        field.focus();
      }
      return;
    }
    if (form.dataset.confirm && !(submitter && submitter.name === "confirmed")) {
      confirming(form, true); // the second confirmation (SPEC §15.8)
      return;
    }
    send(form, submitter);
  });

  live.addEventListener("click", function (e) {
    if (!e.target.closest) {
      return;
    }
    var b = e.target.closest("button.cancel");
    if (b && b.form) {
      confirming(b.form, false);
      b.form.classList.remove("open");
      collapse();
      return;
    }
    if (e.target.closest("button.peek")) {
      showSheet("", true);
      return;
    }
    if (e.target.closest("button.fold")) {
      showSheet("", false);
      return;
    }
    // The stepper and a card's marker go to an entry of the sheet, which
    // without the script are links to it further down the page.
    var to = e.target.closest("a.step, a.to-sheet");
    if (to) {
      e.preventDefault();
      showSheet(to.getAttribute("href").slice(1), true);
    }
  });

  live.addEventListener("focusout", function () {
    if (stale) {
      setTimeout(function () { if (stale) { later(); } }, 0);
    }
  });

  tick();
  sheet();
  collapse();
  if (over) {
    root.classList.add("over");
    return;
  }
  setInterval(tick, 1000);
  follow();
  readTails();
})();
