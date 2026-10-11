import fs from "fs";
import path from "path";
import { makeWASocket, useMultiFileAuthState, DisconnectReason } from "@vansnowi/baileys";
import { makeWASocket as makeCallSocket, useMultiFileAuthState as useCallAuth, fetchLatestBaileysVersion, makeCacheableSignalKeyStore } from "@whiskeysockets/baileys";
import express from "express";
import QRCode from "qrcode";
import qrcodeTerminal from "qrcode-terminal";
import pino from "pino";
import { MenuState } from "./menu.js";
import { CallManager } from "./calls.js";
import { listAudioFiles, resolveAudioFile } from "./voip.js";

const DATA_DIR = path.resolve("data");
const AUTH_DIR = path.join(DATA_DIR, "auth");
const CALL_AUTH_DIR = path.join(DATA_DIR, "auth-call");
const CHATS_FILE = path.join(DATA_DIR, "chats.json");
const CONFIG_FILE = path.resolve("config.json");

const logger = pino({ level: "fatal" });

let config = loadConfig();

const menus = new Map();
const devMenus = new Map();
let currentQr = "";
let connectionState = "init";
let reconnectTimer = null;
let sock = null;
let callSock = null;
let calls = null;

const mainItems = [
  "Плей",
  "Стоп",
  "Луп",
  "Майд",
  "Микр",
  "Звони",
  "Выйти",
  "Файлы",
  "Статус",
  "Справочник",
];

const devItems = [
  "Очистить_состояние",
  "Широковещ",
  "Перезагрузка",
  "Конфиг",
  "Тест_звук",
  "Звонки",
  "Справочник",
];

function loadConfig() {
  try {
    const raw = fs.readFileSync(CONFIG_FILE, "utf8");
    return JSON.parse(raw);
  } catch (e) {
    console.warn("[config] не удалось прочитать config.json:", e.message);
    return {
      owner: "",
      prefix: "!",
      easter_egg_file: "",
      web_port: 3000,
      voip: {},
    };
  }
}

function ensureDirs() {
  try {
    if (!fs.existsSync(AUTH_DIR)) fs.mkdirSync(AUTH_DIR, { recursive: true });
    if (!fs.existsSync(path.resolve("audio"))) {
      fs.mkdirSync(path.resolve("audio"), { recursive: true });
    }
  } catch (e) {
    console.warn("[fs] ошибка создания папок:", e.message);
  }
}

function readChats() {
  try {
    if (!fs.existsSync(CHATS_FILE)) return [];
    return JSON.parse(fs.readFileSync(CHATS_FILE, "utf8"));
  } catch (e) {
    console.warn("[chats] битый chats.json:", e.message);
    return [];
  }
}

function saveChat(jid) {
  try {
    const chats = readChats();
    if (!chats.includes(jid)) {
      chats.push(jid);
      fs.writeFileSync(CHATS_FILE, JSON.stringify(chats, null, 2));
    }
  } catch (e) {
    console.warn("[chats] не удалось сохранить чат:", e.message);
  }
}

function getMenu(chatId) {
  if (!menus.has(chatId)) {
    menus.set(chatId, new MenuState(mainItems));
  }
  return menus.get(chatId);
}

function getDevMenu(chatId) {
  if (!devMenus.has(chatId)) {
    devMenus.set(chatId, new MenuState(devItems));
  }
  return devMenus.get(chatId);
}

function isOwner(sender) {
  return Boolean(config.owner) && sender === config.owner;
}

function sendText(chatId, text) {
  if (!sock) return;
  sock.sendMessage(chatId, { text }).catch((e) => {
    console.warn("[send] ошибка отправки сообщения:", e.message);
  });
}

async function sendPtt(chatId, filePath) {
  if (!sock) return;
  try {
    await sock.sendMessage(chatId, { audio: { url: filePath }, ptt: true });
  } catch (e) {
    console.warn("[send] ошибка отправки PTT:", e.message);
  }
}

function normalizePhone(input) {
  const digits = String(input || "").replace(/\D/g, "");
  return digits.length >= 10 ? digits : null;
}

function helpText() {
  const p = config.prefix;
  return [
    "Команды:",
    `${p}меню / ${p}menu — открыть меню`,
    `${p}меню вверх | ${p}menu up — навигация`,
    `${p}меню вниз | ${p}menu down — навигация`,
    `${p}меню <номер> | ${p}menu <n> — выбрать пункт`,
    `${p}выбор / ${p}accept — выполнить выбранный пункт`,
    `${p}плей <файл> / ${p}play — играть аудио (в звонке идёт собеседнику)`,
    `${p}стоп / ${p}stop — остановить плеер`,
    `${p}луп / ${p}loop — повтор трека`,
    `${p}майд / ${p}mute — мут микрофона`,
    `${p}микр / ${p}unmute — снять мут`,
    `${p}звони <номер> [файл] / ${p}call — позвонить и играть аудио`,
    `${p}выйти / ${p}hangup — завершить звонок`,
    `${p}файлы / ${p}files — список аудио`,
    `${p}статус / ${p}status — состояние`,
    `${p}справочник / ${p}help — эта справка`,
    `${p}разраб / ${p}dev — меню разработчика (owner)`,
  ].join("\n");
}

function runMenuItem(chatId, item) {
  const p = config.prefix;
  switch (item) {
    case "Плей":
      sendText(chatId, `Используйте ${p}плей <файл>`);
      break;
    case "Стоп":
      handleStop(chatId);
      break;
    case "Луп":
      handleLoop(chatId);
      break;
    case "Майд":
      handleMute(chatId, true);
      break;
    case "Микр":
      handleMute(chatId, false);
      break;
    case "Звони":
      sendText(chatId, `Используйте ${p}звони <номер> [файл]`);
      break;
    case "Выйти":
      handleHangup(chatId);
      break;
    case "Файлы":
      handleFiles(chatId);
      break;
    case "Статус":
      handleStatus(chatId);
      break;
    case "Справочник":
      sendText(chatId, helpText());
      break;
    default:
      sendText(chatId, "Неизвестный пункт меню");
  }
}

function runDevItem(chatId, item, args) {
  switch (item) {
    case "Очистить_состояние":
      menus.delete(chatId);
      devMenus.delete(chatId);
      if (calls) {
        calls.stopPlayer();
        calls.loop = false;
        calls.setMuted(false);
      }
      sendText(chatId, "Состояние очищено");
      break;
    case "Широковещ": {
      const text = args.join(" ").trim();
      if (!text) {
        sendText(chatId, "Укажите текст для рассылки");
        break;
      }
      broadcast(text);
      break;
    }
    case "Перезагрузка":
      sendText(chatId, "Перезапуск подключения...");
      try {
        sock?.logout();
      } catch (e) {
        console.warn("[dev] ошибка logout при перезагрузке:", e.message);
      }
      scheduleReconnect();
      break;
    case "Конфиг": {
      const safe = { ...config };
      sendText(chatId, JSON.stringify(safe, null, 2));
      break;
    }
    case "Тест_звук":
      handlePlay(chatId, ["test.mp3"]);
      break;
    case "Звонки":
      sendText(chatId, `Состояние звонка: ${calls ? calls.status().state : "нет"}`);
      break;
    case "Справочник":
      sendText(chatId, helpText());
      break;
    default:
      sendText(chatId, "Неизвестный dev-пункт");
  }
}

function broadcast(text) {
  const chats = readChats();
  if (!chats.length) {
    console.warn("[broadcast] нет сохранённых чатов");
    return;
  }
  let sent = 0;
  for (const jid of chats) {
    try {
      sock.sendMessage(jid, { text });
      sent++;
    } catch (e) {
      console.warn(`[broadcast] не отправлено в ${jid}:`, e.message);
    }
  }
  console.log(`[broadcast] отправлено в ${sent}/${chats.length} чатов`);
}

function handleMenu(chatId, parts) {
  const menu = getMenu(chatId);
  const sub = (parts[0] || "").toLowerCase();
  if (!sub) {
    sendText(chatId, menu.render());
    return;
  }
  if (sub === "вверх" || sub === "up") {
    menu.up();
    sendText(chatId, menu.render());
    return;
  }
  if (sub === "вниз" || sub === "down") {
    menu.down();
    sendText(chatId, menu.render());
    return;
  }
  const picked = menu.pick(parts[0]);
  if (!picked) {
    sendText(chatId, "Неверный номер пункта");
    return;
  }
  sendText(chatId, menu.render());
}

function handleAccept(chatId) {
  const menu = getMenu(chatId);
  runMenuItem(chatId, menu.current());
}

function handleDev(chatId, parts) {
  if (!isOwner(chatId)) {
    sendText(chatId, "Команда не найдена");
    return;
  }
  const menu = getDevMenu(chatId);
  const sub = (parts[0] || "").toLowerCase();
  if (!sub) {
    sendText(chatId, menu.render());
    return;
  }
  if (sub === "вверх" || sub === "up") {
    menu.up();
    sendText(chatId, menu.render());
    return;
  }
  if (sub === "вниз" || sub === "down") {
    menu.down();
    sendText(chatId, menu.render());
    return;
  }
  if (/^\d+$/.test(sub)) {
    const picked = menu.pick(sub);
    if (!picked) {
      sendText(chatId, "Неверный номер пункта");
      return;
    }
    sendText(chatId, menu.render());
    return;
  }
  if (sub === "выполнить" || sub === "run") {
    runDevItem(chatId, menu.current(), parts.slice(1));
    return;
  }
  const alias = {
    "очистить_состояние": "Очистить_состояние",
    "широковещ": "Широковещ",
    "перезагрузка": "Перезагрузка",
    "конфиг": "Конфиг",
    "тест_звук": "Тест_звук",
    "звонки": "Звонки",
    "справочник": "Справочник",
  };
  const mapped = alias[sub];
  if (mapped) {
    runDevItem(chatId, mapped, parts.slice(1));
    return;
  }
  sendText(chatId, "Неизвестная dev-команда");
}

function handlePlay(chatId, parts) {
  if (!calls) {
    sendText(chatId, "Сервис звонков не готов");
    return;
  }
  const files = listAudioFiles();
  let name = parts[0];
  if (!name) {
    if (!files.length) {
      sendText(chatId, "Папка audio пуста");
      return;
    }
    name = files[0];
  }
  const resolved = resolveAudioFile(name);
  if (!resolved.ok) {
    sendText(chatId, `Ошибка: ${resolved.reason}`);
    return;
  }
  const inCall = calls.state === "active" || calls.state === "ringing" || calls.state === "dialing";
  const started = calls.play(resolved.file);
  if (!started) {
    sendText(chatId, "Не удалось запустить плеер (проверьте ffmpeg)");
    return;
  }
  if (inCall) {
    sendText(chatId, `Играет в звонок: ${resolved.name}`);
  } else {
    sendText(chatId, `Играет: ${resolved.name}`);
    sendPtt(chatId, resolved.file);
  }
  if (config.easter_egg_file && resolved.name === config.easter_egg_file) {
    sendText(chatId, "🥚 СЕКРЕТНАЯ ПАСХАЛКА!");
    sendPtt(chatId, resolved.file);
  }
}

function handleStop(chatId) {
  if (calls) {
    calls.stopPlayer();
    calls.loop = false;
  }
  sendText(chatId, "Плеер остановлен");
}

function handleLoop(chatId) {
  if (!calls) {
    sendText(chatId, "Сервис звонков не готов");
    return;
  }
  calls.loop = !calls.loop;
  sendText(chatId, `Повтор трека: ${calls.loop ? "включён" : "выключен"}`);
}

function handleMute(chatId, value) {
  if (calls) {
    calls.setMuted(value);
  }
  sendText(chatId, value ? "Микрофон заглушён" : "Микрофон включён");
}

function handleFiles(chatId) {
  const files = listAudioFiles();
  if (!files.length) {
    sendText(chatId, "Папка audio пуста");
    return;
  }
  sendText(chatId, files.map((f, i) => `${i + 1}. ${f}`).join("\n"));
}

function handleStatus(chatId) {
  const st = calls ? calls.status() : { state: "disabled", playing: false, loop: false, muted: false };
  sendText(
    chatId,
    [
      `Подключение: ${connectionState}`,
      `Звонок: ${st.state}`,
      st.jid ? `Собеседник: ${st.jid}` : "",
      `Играет: ${st.playing ? "да" : "нет"}`,
      `Луп: ${st.loop ? "да" : "нет"}`,
      `Мут: ${st.muted ? "да" : "нет"}`,
    ]
      .filter(Boolean)
      .join("\n")
  );
}

async function handleCall(chatId, parts) {
  const phone = normalizePhone(parts[0]);
  if (!phone) {
    sendText(chatId, "Укажите номер телефона");
    return;
  }
  if (!calls) {
    sendText(chatId, "Сервис звонков не готов");
    return;
  }
  const jid = `${phone}@s.whatsapp.net`;
  let file = null;
  if (parts[1]) {
    const resolved = resolveAudioFile(parts[1]);
    if (!resolved.ok) {
      sendText(chatId, `Ошибка: ${resolved.reason}`);
      return;
    }
    file = resolved.file;
  }
  try {
    await calls.dial(jid);
    if (file) {
      calls.play(file);
    }
    sendText(chatId, `Звоним: ${phone}`);
  } catch (e) {
    console.warn("[call] ошибка звонка:", e.message);
    sendText(chatId, `Не удалось позвонить: ${e.message}`);
  }
}

function handleHangup(chatId) {
  if (calls) {
    calls.hangup();
  }
  sendText(chatId, "Звонок завершён");
}

function handleCommand(chatId, body) {
  const p = config.prefix;
  if (!body.startsWith(p)) return;
  const parts = body.slice(p.length).trim().split(/\s+/);
  const cmd = (parts.shift() || "").toLowerCase();
  switch (cmd) {
    case "меню":
    case "menu":
      handleMenu(chatId, parts);
      break;
    case "выбор":
    case "accept":
      handleAccept(chatId);
      break;
    case "плей":
    case "play":
      handlePlay(chatId, parts);
      break;
    case "стоп":
    case "stop":
      handleStop(chatId);
      break;
    case "луп":
    case "loop":
      handleLoop(chatId);
      break;
    case "майд":
    case "mute":
      handleMute(chatId, true);
      break;
    case "микр":
    case "unmute":
      handleMute(chatId, false);
      break;
    case "звони":
    case "call":
      handleCall(chatId, parts);
      break;
    case "выйти":
    case "hangup":
      handleHangup(chatId);
      break;
    case "файлы":
    case "files":
      handleFiles(chatId);
      break;
    case "статус":
    case "status":
      handleStatus(chatId);
      break;
    case "помощь":
    case "справочник":
    case "help":
      sendText(chatId, helpText());
      break;
    case "разраб":
    case "dev":
      handleDev(chatId, parts);
      break;
    default:
      break;
  }
}

function startWebPanel() {
  const app = express();
  app.get("/", (_req, res) => {
    res.sendFile(path.resolve("views/index.html"));
  });
  app.get("/qr.svg", async (_req, res) => {
    if (!currentQr) {
      res.type("svg").send("<svg xmlns='http://www.w3.org/2000/svg'></svg>");
      return;
    }
    try {
      const svg = await QRCode.toString(currentQr, { type: "svg" });
      res.type("svg").send(svg);
    } catch (e) {
      console.warn("[web] ошибка генерации QR:", e.message);
      res.status(500).send("QR error");
    }
  });
  app.get("/status", (_req, res) => {
    res.json({
      state: connectionState,
      call: calls ? calls.status() : { state: "disabled" },
      qr: Boolean(currentQr),
    });
  });
  const port = config.web_port || 3000;
  app.listen(port, () => {
    console.log(`[web] панель на http://localhost:${port}`);
  });
}

function scheduleReconnect() {
  if (reconnectTimer) return;
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    startSock();
  }, 3000);
}

async function startCallSock() {
  try {
    if (!fs.existsSync(CALL_AUTH_DIR)) {
      fs.mkdirSync(CALL_AUTH_DIR, { recursive: true });
    }
    const auth = await useCallAuth(CALL_AUTH_DIR);
    const version = await fetchLatestBaileysVersion().catch((e) => {
      console.warn("[call] не удалось получить версию WhatsApp:", e.message);
      return null;
    });
    callSock = makeCallSocket({
      logger,
      auth: {
        creds: auth.creds,
        keys: makeCacheableSignalKeyStore(auth.keys, logger),
      },
      version: version?.version,
      browser: ["Chrome", "Windows", "120.0.0"],
    });
    callSock.ev.on("connection.update", (update) => {
      if (update.qr) {
        console.log("[call] QR второй сессии звонков:");
        qrcodeTerminal.generate(update.qr, { small: true });
      }
      if (update.connection === "open") {
        console.log("[call] сессия звонков открыта");
      }
      if (update.connection === "close") {
        const code = update.lastDisconnect?.error?.output?.statusCode;
        if (code !== DisconnectReason.loggedOut) {
          console.warn("[call] сессия звонков закрылась, переподключение. Код:", code);
          setTimeout(startCallSock, 3000);
        }
      }
    });
    callSock.ev.on("call", (events) => {
      for (const ev of events) {
        const note = calls.handleEvent(ev);
        if (note) {
          console.log(`[call] ${ev.from}: ${ev.status} (${note})`);
        }
      }
    });
    calls = new CallManager(callSock, config.voip || {});
  } catch (e) {
    console.warn("[call] не удалось запустить сессию звонков:", e.message);
  }
}

async function startSock() {
  ensureDirs();
  config = loadConfig();
  const { state } = await useMultiFileAuthState(AUTH_DIR);
  sock = makeWASocket({
    logger,
    auth: state,
  });

  sock.ev.on("connection.update", (update) => {
    const { connection, lastDisconnect, qr } = update;
    if (qr) {
      currentQr = qr;
      connectionState = "qr";
      console.log("[wa] QR основной сессии:");
      qrcodeTerminal.generate(qr, { small: true });
    }
    if (connection === "open") {
      currentQr = "";
      connectionState = "open";
      console.log("[wa] соединение открыто");
    }
    if (connection === "close") {
      currentQr = "";
      const code =
        lastDisconnect?.error?.output?.statusCode ||
        lastDisconnect?.error?.data?.status;
      connectionState = "close";
      if (code !== DisconnectReason.loggedOut) {
        console.warn("[wa] соединение закрыто, переподключение. Код:", code);
        scheduleReconnect();
      } else {
        console.log("[wa] выход выполнен, переподключение не требуется");
      }
    }
  });

  sock.ev.on("messages.upsert", ({ messages }) => {
    for (const m of messages) {
      if (!m.message) continue;
      const remote = m.key.remoteJid;
      if (!remote || remote.includes("@broadcast")) continue;
      saveChat(remote);
      const text = m.message.conversation || m.message.extendedTextMessage?.text;
      if (typeof text === "string") {
        handleCommand(remote, text.trim());
      }
    }
  });

  sock.ev.on("call", async (events) => {
    for (const call of events) {
      if (call.status !== "offer") continue;
      console.log(`[call] входящий звонок из ${call.from}`);
      if (config.voip?.auto_reject_incoming) {
        try {
          await sock.rejectCall(call.id, call.from);
          console.log("[call] входящий звонок отклонён");
        } catch (e) {
          console.warn("[call] не удалось отклонить звонок:", e.message);
        }
      }
    }
  });
}

startWebPanel();
startSock().catch((e) => {
  console.error("[bot] ошибка запуска:", e.message);
});
startCallSock();
