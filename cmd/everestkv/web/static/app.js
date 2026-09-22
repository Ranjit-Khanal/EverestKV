const statusDot = document.getElementById("status-dot");
const statusText = document.getElementById("status-text");

async function refreshStatus() {
  try {
    const res = await fetch("/api/status");
    const data = await res.json();
    if (data.connected) {
      statusDot.className = "dot ok";
      statusText.textContent = `connected to ${data.server}`;
    } else {
      statusDot.className = "dot bad";
      statusText.textContent = `disconnected: ${data.error}`;
    }
  } catch (e) {
    statusDot.className = "dot bad";
    statusText.textContent = "dashboard offline";
  }
}
refreshStatus();
setInterval(refreshStatus, 5000);

function showResult(el, text, kind) {
  el.textContent = text;
  el.className = "result" + (kind ? " " + kind : "");
}

document.getElementById("get-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const key = document.getElementById("get-key").value.trim();
  const el = document.getElementById("get-result");
  if (!key) return;
  try {
    const res = await fetch(`/api/get?key=${encodeURIComponent(key)}`);
    const data = await res.json();
    if (!res.ok) {
      showResult(el, data.error || "error", "error");
      return;
    }
    showResult(el, data.found ? data.value : "(nil)", data.found ? "ok" : "");
  } catch (err) {
    showResult(el, String(err), "error");
  }
});

document.getElementById("set-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const key = document.getElementById("set-key").value.trim();
  const value = document.getElementById("set-value").value;
  const el = document.getElementById("set-result");
  if (!key) return;
  try {
    const res = await fetch("/api/set", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ key, value }),
    });
    const data = await res.json();
    if (!res.ok) {
      showResult(el, data.error || "error", "error");
      return;
    }
    showResult(el, "OK", "ok");
    loadKeys();
  } catch (err) {
    showResult(el, String(err), "error");
  }
});

const keysBody = document.getElementById("keys-body");
const keysEmpty = document.getElementById("keys-empty");

async function loadKeys() {
  try {
    const res = await fetch("/api/keys");
    const data = await res.json();
    if (!res.ok) return;
    const entries = data.entries || [];
    keysBody.innerHTML = "";
    keysEmpty.hidden = entries.length > 0;
    for (const { key, value } of entries) {
      const tr = document.createElement("tr");
      const keyTd = document.createElement("td");
      keyTd.textContent = key;
      const valueTd = document.createElement("td");
      valueTd.textContent = value;
      tr.append(keyTd, valueTd);
      keysBody.appendChild(tr);
    }
  } catch (e) {
    // status indicator already surfaces connectivity problems
  }
}
document.getElementById("keys-refresh").addEventListener("click", loadKeys);
loadKeys();
setInterval(loadKeys, 5000);

const consoleEl = document.getElementById("console");
function appendLine(text, cls) {
  const div = document.createElement("div");
  div.className = "line " + cls;
  div.textContent = text;
  consoleEl.appendChild(div);
  consoleEl.scrollTop = consoleEl.scrollHeight;
}

document.getElementById("console-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const input = document.getElementById("console-input");
  const line = input.value.trim();
  if (!line) return;
  appendLine("> " + line, "cmd");
  input.value = "";
  try {
    const res = await fetch("/api/command", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ line }),
    });
    const data = await res.json();
    if (!res.ok) {
      appendLine(data.error || "error", "err");
      return;
    }
    appendLine(data.reply, data.isError ? "err" : "reply");
    if (!data.isError) loadKeys();
  } catch (err) {
    appendLine(String(err), "err");
  }
});
