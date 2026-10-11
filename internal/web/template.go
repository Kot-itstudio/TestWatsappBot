package web

const indexHTML = `<!DOCTYPE html>
<html lang="ru">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>WA Panel</title>
<style>
:root{--bg:#0b1220;--card:#121a2b;--line:#243049;--text:#e6edf7;--muted:#8b98b3;--accent:#3ba55d;--danger:#e5484d}
*{box-sizing:border-box;margin:0;padding:0}
body{background:var(--bg);color:var(--text);font:14px/1.5 system-ui,sans-serif;padding:24px;max-width:1100px;margin:auto}
h1{font-size:20px;margin-bottom:16px}
.grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(320px,1fr));gap:16px}
.card{background:var(--card);border:1px solid var(--line);border-radius:12px;padding:16px}
.card h2{font-size:14px;color:var(--muted);margin-bottom:10px;text-transform:uppercase;letter-spacing:.05em}
pre{background:#0a0f1c;border:1px solid var(--line);border-radius:8px;padding:12px;overflow:auto;font-size:12px;line-height:1.35}
.row{display:flex;gap:8px;flex-wrap:wrap;margin-top:10px}
input,select{background:#0a0f1c;border:1px solid var(--line);color:var(--text);border-radius:8px;padding:8px 10px;font:inherit}
input[type=text]{flex:1;min-width:180px}
button{background:var(--accent);border:0;color:#fff;border-radius:8px;padding:8px 14px;font:inherit;cursor:pointer}
button:hover{filter:brightness(1.1)}
button.danger{background:var(--danger)}
.badge{display:inline-block;padding:2px 10px;border-radius:999px;font-size:12px;background:var(--line)}
.badge.ok{background:var(--accent)}
.badge.err{background:var(--danger)}
table{width:100%;border-collapse:collapse;font-size:13px}
td,th{padding:6px 8px;border-bottom:1px solid var(--line);text-align:left}
label{color:var(--muted);font-size:12px}
#qrText{white-space:pre-wrap;word-break:break-all}
.muted{color:var(--muted)}
</style>
</head>
<body>
<h1>Панель управления ботом</h1>
<div class="grid">
<div class="card">
<h2>Подключение</h2>
<p><span id="stateBadge" class="badge">{{.State}}</span></p>
<div id="qrWrap" class="hidden"><pre id="qrText"></pre></div>
<div class="row"><button class="danger" onclick="logout()">Выйти (logout)</button></div>
</div>
<div class="card">
<h2>Звонок</h2>
<table>
<tr><td>Состояние</td><td id="callState">idle</td></tr>
<tr><td>Номер</td><td id="callJid">—</td></tr>
<tr><td>Играет</td><td id="callPlaying">нет</td></tr>
<tr><td>Луп</td><td id="callLoop">нет</td></tr>
<tr><td>Мут</td><td id="callMuted">нет</td></tr>
</table>
</div>
<div class="card">
<h2>Команда</h2>
<div class="row">
<input type="text" id="cmdInput" placeholder="{{.Prefix}}меню вверх">
<button onclick="sendCmd()">Выполнить</button>
</div>
<div class="row">
<select id="quickCmd">
<option value="">Быстрая команда…</option>
<option>{{.Prefix}}меню</option>
<option>{{.Prefix}}меню вверх</option>
<option>{{.Prefix}}меню вниз</option>
<option>{{.Prefix}}выбор</option>
<option>{{.Prefix}}файлы</option>
<option>{{.Prefix}}статус</option>
<option>{{.Prefix}}луп</option>
<option>{{.Prefix}}майд</option>
<option>{{.Prefix}}микр</option>
<option>{{.Prefix}}стоп</option>
<option>{{.Prefix}}выйти</option>
<option>{{.Prefix}}справочник</option>
</select>
</div>
</div>
<div class="card">
<h2>Позвонить</h2>
<div class="row">
<input type="text" id="phoneInput" placeholder="+7 999 123-45-67">
<input type="text" id="fileInput" placeholder="файл (необязательно)">
<button onclick="callNumber()">Позвонить</button>
</div>
</div>
<div class="card" style="grid-column:1/-1">
<h2>Журнал событий</h2>
<pre id="log">Загрузка…</pre>
</div>
</div>
<script>
const stateBadge = document.getElementById("stateBadge");
const qrWrap = document.getElementById("qrWrap");
const qrText = document.getElementById("qrText");
const logBox = document.getElementById("log");

async function refresh(){
 try{
  const res = await fetch("/api/status");
  const st = await res.json();
  stateBadge.textContent = st.state;
  stateBadge.className = "badge " + (st.state === "connected" ? "ok" : (st.state === "closed" || st.state === "loggedout" ? "err" : ""));
  if(st.qr){ qrWrap.classList.remove("hidden"); qrText.textContent = st.qr; }
  else { qrWrap.classList.add("hidden"); qrText.textContent = ""; }
  const c = st.call || {};
  document.getElementById("callState").textContent = c.state || "idle";
  document.getElementById("callJid").textContent = c.jid || "—";
  document.getElementById("callPlaying").textContent = yn(c.playing);
  document.getElementById("callLoop").textContent = yn(c.loop);
  document.getElementById("callMuted").textContent = yn(c.muted);
 }catch(e){}
 try{
  const res = await fetch("/api/events?limit=50");
  const data = await res.json();
  logBox.textContent = (data.events||[]).join("\\n") || "пусто";
  logBox.scrollTop = logBox.scrollHeight;
 }catch(e){}
}
function yn(v){ return v ? "да" : "нет"; }
async function sendCmd(){
 const el = document.getElementById("cmdInput");
 const text = el.value.trim();
 if(!text) return;
 await fetch("/api/command",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({text})});
 el.value = "";
 refresh();
}
document.getElementById("quickCmd").addEventListener("change", e=>{
 if(e.target.value){ document.getElementById("cmdInput").value = e.target.value; e.target.selectedIndex = 0; }
});
document.getElementById("cmdInput").addEventListener("keydown", e=>{ if(e.key==="Enter") sendCmd(); });
async function callNumber(){
 const phone = document.getElementById("phoneInput").value.trim();
 if(!phone) return;
 const file = document.getElementById("fileInput").value.trim();
 let cmd = "{{.Prefix}}звони " + phone;
 if(file) cmd += " " + file;
 await fetch("/api/command",{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({text:cmd})});
 refresh();
}
async function logout(){
 await fetch("/api/logout",{method:"POST"});
 refresh();
}
refresh();
setInterval(refresh, 2000);
</script>
</body>
</html>
`
