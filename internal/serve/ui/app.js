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
  var over = false; // the run ended: nothing left to read or refresh
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

  // sheet marks the page as having a sheet, which the stylesheet dims
  // the rest for.
  function sheet() {
    document.body.classList.toggle("has-sheet", !over && live.querySelector(".question.sheet") !== null);
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
      localTimes();
      tick();
      sheet();
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

  function fillTails() {
    var pres = live.querySelectorAll("pre.tail");
    for (var i = 0; i < pres.length; i++) {
      var lines = tails[pres[i].dataset.task];
      if (lines) {
        pres[i].textContent = lines.join("\n");
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

  // tick keeps how long each task has run current between refreshes,
  // and stops with the run.
  function tick() {
    if (over) {
      return;
    }
    var spans = live.querySelectorAll(".ran[data-started]");
    var now = Date.now();
    for (var i = 0; i < spans.length; i++) {
      var s = Math.max(0, Math.floor((now - Date.parse(spans[i].dataset.started)) / 1000));
      var text;
      if (s < 60) {
        text = s + "s";
      } else if (s < 3600) {
        text = Math.floor(s / 60) + "m" + String(s % 60).padStart(2, "0") + "s";
      } else {
        text = Math.floor(s / 3600) + "h" + String(Math.floor(s / 60) % 60).padStart(2, "0") + "m";
      }
      spans[i].textContent = text;
    }
  }

  // localTimes shows the events' times, rendered in UTC, in the
  // device's time zone.
  function localTimes() {
    var times = live.querySelectorAll("time[datetime]");
    for (var i = 0; i < times.length; i++) {
      var d = new Date(times[i].getAttribute("datetime"));
      if (!isNaN(d)) {
        times[i].textContent = d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit", hour12: false });
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

  // endWord is the status word for a run's outcome, as the server's.
  function endWord(outcome) {
    switch (outcome) {
      case "completed": return "Done";
      case "failed": return "Failed";
      case "stuck": return "Stuck";
    }
    return "Stopped";
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
    var gone = live.querySelectorAll("form.cmd, section.question, .actions, details, section[aria-label='Run mode'], nav.bar");
    for (var i = 0; i < gone.length; i++) {
      gone[i].remove();
    }
    var word = live.querySelector(".glance .word");
    if (word) {
      word.textContent = endWord(outcome);
      word.classList.remove("needs");
    }
    var line = live.querySelector(".glance .line");
    if (line) {
      line.textContent = "ended: " + (outcome || "unknown");
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
    var b = e.target.closest ? e.target.closest("button.cancel") : null;
    if (b && b.form) {
      confirming(b.form, false);
      b.form.classList.remove("open");
      collapse();
    }
  });

  live.addEventListener("focusout", function () {
    if (stale) {
      setTimeout(function () { if (stale) { later(); } }, 0);
    }
  });

  localTimes();
  sheet();
  collapse();
  setInterval(tick, 1000);
  follow();
  readTails();
})();
