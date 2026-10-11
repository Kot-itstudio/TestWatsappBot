import { getDb } from "../db.js";

let collection = null;

const LEVELS = ["deny", "inherit", "allow"];

export default {
    meta: {
        id: "roles",
        name: "Roles",
        version: "1.0.0",
        description: "Роли с разрешениями в стиле Discord",
        commands: ["роли", "roles"],
        needsAdmin: false,
        groupOnly: true,
    },

    async onCommand(sock, msg, { cmd, args, ctx }) {
        const userId = ctx.sender;
        const chat = ctx.chat;
        const sub = (args[0] || "").toLowerCase();
        const rest = args.slice(1);

        try {
            if (!sub) {
                await sendHelp(sock, msg, chat);
                return true;
            }
            if (["создать", "create"].includes(sub)) return await createRole(sock, msg, chat, rest);
            if (["удалить", "delete"].includes(sub)) return await deleteRole(sock, msg, chat, rest);
            if (["роль", "assign"].includes(sub)) return await assignRole(sock, msg, chat, rest);
            if (["выдать", "give"].includes(sub)) return await setPermission(sock, msg, chat, rest, "allow");
            if (["запретить", "ban"].includes(sub)) return await setPermission(sock, msg, chat, rest, "deny");
            if (["мои", "my"].includes(sub)) return await showMyPerms(sock, msg, chat, userId);
            if (["список", "list"].includes(sub)) return await listRoles(sock, msg, chat);
            await sock.sendMessage(chat, { text: "❌ Неизвестная подкоманда. Пиши /роли" }, { quoted: msg });
        } catch (e) {
            console.error("[roles] ошибка:", e.message);
            await sock.sendMessage(chat, { text: "❌ Ошибка обработки команды" }, { quoted: msg });
        }
        return true;
    },

    async onMessage(sock, msg, ctx) {
        try {
            const service = extractServiceMessage(msg);
            if (!service) return false;
            return await handleServiceEvent(sock, service, ctx);
        } catch (e) {
            console.error("[roles] событие группы:", e.message);
            return false;
        }
    },
};

async function getCollection() {
    if (collection) return collection;
    const db = getDb();
    collection = db.collection("roles");
    try {
        await collection.createIndex({ jid: 1 }, { unique: true });
    } catch (e) {
        console.error("[roles] индекс:", e.message);
    }
    return collection;
}

async function loadDoc(jid) {
    const col = await getCollection();
    let doc = await col.findOne({ jid });
    if (!doc) {
        doc = { jid, roles: {}, assignments: {} };
        try {
            await col.insertOne(doc);
        } catch (e) {
            console.error("[roles] инициализация:", e.message);
        }
    }
    return doc;
}

async function saveDoc(jid, doc) {
    const col = await getCollection();
    await col.updateOne({ jid }, { $set: doc });
}

function targetFromArgs(rest) {
    const raw = rest[0];
    if (!raw) return null;
    if (raw.includes("@")) return raw;
    const digits = raw.replace(/\D/g, "");
    return digits.length >= 8 ? `${digits}@s.whatsapp.net` : null;
}

async function createRole(sock, msg, chat, rest) {
    const name = rest.join(" ").trim().toLowerCase();
    if (!name) {
        await sock.sendMessage(chat, { text: "Использование: /роли создать <название>" }, { quoted: msg });
        return true;
    }
    const doc = await loadDoc(chat);
    if (doc.roles[name]) {
        await sock.sendMessage(chat, { text: `Роль «${name}» уже существует` }, { quoted: msg });
        return true;
    }
    doc.roles[name] = {};
    await saveDoc(chat, doc);
    await sock.sendMessage(chat, { text: `✅ Роль «${name}» создана` }, { quoted: msg });
    return true;
}

async function deleteRole(sock, msg, chat, rest) {
    const name = (rest[0] || "").toLowerCase();
    if (!name) {
        await sock.sendMessage(chat, { text: "Использование: /роли удалить <название>" }, { quoted: msg });
        return true;
    }
    const doc = await loadDoc(chat);
    if (!doc.roles[name]) {
        await sock.sendMessage(chat, { text: `Роль «${name}» не найдена` }, { quoted: msg });
        return true;
    }
    delete doc.roles[name];
    for (const [jid, list] of Object.entries(doc.assignments)) {
        doc.assignments[jid] = list.filter((r) => r !== name);
        if (!doc.assignments[jid].length) delete doc.assignments[jid];
    }
    await saveDoc(chat, doc);
    await sock.sendMessage(chat, { text: `🗑️ Роль «${name}» удалена` }, { quoted: msg });
    return true;
}

async function assignRole(sock, msg, chat, rest) {
    const target = targetFromArgs(rest);
    const name = (rest[1] || "").toLowerCase();
    if (!target || !name) {
        await sock.sendMessage(chat, { text: "Использование: /роли роль @номер <название>" }, { quoted: msg });
        return true;
    }
    const doc = await loadDoc(chat);
    if (!doc.roles[name]) {
        await sock.sendMessage(chat, { text: `Роль «${name}» не найдена. Сначала: /роли создать ${name}` }, { quoted: msg });
        return true;
    }
    const list = doc.assignments[target] || [];
    if (list.includes(name)) {
        await sock.sendMessage(chat, { text: "Роль уже назначена" }, { quoted: msg });
        return true;
    }
    list.push(name);
    doc.assignments[target] = list;
    await saveDoc(chat, doc);
    await sock.sendMessage(chat, { text: `✅ Роль «${name}» выдана` }, { quoted: msg });
    return true;
}

async function setPermission(sock, msg, chat, rest, level) {
    const roleName = (rest[0] || "").toLowerCase();
    const permKey = (rest[1] || "").toLowerCase();
    if (!roleName || !permKey) {
        await sock.sendMessage(chat, { text: "Использование: /роли выдать|запретить <роль> <разрешение>" }, { quoted: msg });
        return true;
    }
    const doc = await loadDoc(chat);
    if (!doc.roles[roleName]) {
        await sock.sendMessage(chat, { text: `Роль «${roleName}» не найдена` }, { quoted: msg });
        return true;
    }
    doc.roles[roleName][permKey] = level;
    await saveDoc(chat, doc);
    const label = level === "allow" ? "✅ разрешено" : "⛔ запрещено";
    await sock.sendMessage(chat, { text: `«${permKey}» для роли «${roleName}»: ${label}` }, { quoted: msg });
    return true;
}

function resolvePerm(doc, userJid, permKey) {
    const roles = doc.assignments[userJid] || [];
    let result = "inherit";
    for (const roleName of roles) {
        const value = doc.roles[roleName]?.[permKey];
        if (!value || value === "inherit") continue;
        if (LEVELS.indexOf(value) > LEVELS.indexOf(result)) result = value;
    }
    return result;
}

async function showMyPerms(sock, msg, chat, userId) {
    const doc = await loadDoc(chat);
    const roles = doc.assignments[userId] || [];
    if (!roles.length) {
        await sock.sendMessage(chat, { text: "У тебя нет ролей. Админ может выдать: /роли роль @тебя <название>" }, { quoted: msg });
        return true;
    }
    const lines = [`Твои роли: ${roles.join(", ")}`, "Разрешения:"];
    for (const roleName of roles) {
        const perms = doc.roles[roleName] || {};
        const entries = Object.entries(perms).filter(([, v]) => v !== "inherit");
        if (!entries.length) {
            lines.push(`  «${roleName}» — без настроек`);
            continue;
        }
        for (const [key, value] of entries) {
            const icon = value === "allow" ? "✅" : "⛔";
            lines.push(`  «${roleName}»: ${icon} ${key}`);
        }
    }
    await sock.sendMessage(chat, { text: lines.join("\n") }, { quoted: msg });
    return true;
}

async function listRoles(sock, msg, chat) {
    const doc = await loadDoc(chat);
    const names = Object.keys(doc.roles);
    if (!names.length) {
        await sock.sendMessage(chat, { text: "Ролей пока нет. Создание: /роли создать <название>" }, { quoted: msg });
        return true;
    }
    const lines = ["📋 Роли группы:"];
    for (const name of names) {
        const count = Object.values(doc.assignments).filter((list) => list.includes(name)).length;
        lines.push(`  • ${name} (${count} участн.)`);
    }
    await sock.sendMessage(chat, { text: lines.join("\n") }, { quoted: msg });
    return true;
}

function extractServiceMessage(msg) {
    if (!msg.participant) return null;
    if (msg.promote) return { type: "promote", who: msg.promote };
    if (msg.demote) return { type: "demote", who: msg.demote };
    if (msg.subject) return { type: "rename" };
    return null;
}

const PERM_MAP = {
    promote: "назначать_админов",
    demote: "снимать_админов",
    rename: "переименовывать_группу",
};

async function handleServiceEvent(sock, service, ctx) {
    const actor = ctx.sender;
    const doc = await loadDoc(ctx.chat);
    const permKey = PERM_MAP[service.type];
    if (!permKey) return false;
    const decision = resolvePerm(doc, actor, permKey);
    if (decision !== "deny") return false;

    if ((doc.assignments[actor] || []).length) {
        delete doc.assignments[actor];
        await saveDoc(ctx.chat, doc);
        await sock.sendMessage(ctx.chat, { text: `⚠️ Роль снята за нарушение «${permKey}»` });
    }
    return true;
}

async function sendHelp(sock, msg, chat) {
    const help = [
        "🎭 Плагин «Роли» (/роли):",
        "/роли создать <название>",
        "/роли удалить <название>",
        "/роли роль @номер <название>",
        "/роли выдать <роль> <разрешение>",
        "/роли запретить <роль> <разрешение>",
        "/роли мои",
        "/роли список",
    ].join("\n");
    await sock.sendMessage(chat, { text: help }, { quoted: msg });
}
