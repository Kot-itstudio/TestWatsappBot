let collection = null;

const LEVELS = ["allow", "inherit", "deny"];

async function onGroupParticipantsUpdate(sock, update, ctx) {
    try {
        const action = mapParticipantAction(update);
        if (!action) return false;
        const env = resolveCtx(ctx, update && (update.author || update.sender));
        if (!env.db) {
            console.error("[roles] база недоступна, событие пропущено");
            return false;
        }
        return await reportViolation(sock, action, env);
    } catch (e) {
        console.error("[roles] событие группы:", e.message);
        return false;
    }
}

export { onGroupParticipantsUpdate };

const plugin = {
    meta: {
        id: "roles",
        name: "Roles",
        version: "1.0.0",
        description: "Роли с разрешениями в стиле Discord",
        commands: ["роли", "roles"],
        needsAdmin: false,
        groupOnly: true,
    },

    async onCommand(sock, msg, payload) {
        let ctx = msg;
        let args = [];
        if (payload && typeof payload === "object" && !Array.isArray(payload)) {
            ctx = payload.ctx || payload.context || payload;
            args = Array.isArray(payload.args) ? payload.args : [];
        } else if (Array.isArray(payload)) {
            args = payload;
        }
        const db = getDb(ctx) || getDb(msg) || getDb(payload) || getDb(globalThis.__pluginCtx);
        const chat = ctx.chat || msg.key.remoteJid;
        const sender = ctx.sender || ctx.author || msg.participant || msg.key.remoteJid;
        const env = { sock, msg, ctx, chat, sender, db };
        try {
            if (!requireDb(env)) {
                await sock.sendMessage(chat, { text: "⚠️ База данных недоступна, плагин не может работать" }, { quoted: msg });
                return true;
            }
        } catch (e) {
            console.error("[roles] контекст команды:", e.message);
            return true;
        }
        const sub = (args[0] || "").toLowerCase();
        const rest = args.slice(1);

        try {
            if (!sub) {
                await sendHelp(env);
                return true;
            }
            if (["создать", "create"].includes(sub)) return await createRole(env, rest);
            if (["удалить", "delete"].includes(sub)) return await deleteRole(env, rest);
            if (["роль", "assign"].includes(sub)) return await assignRole(env, rest);
            if (["снятие", "unassign"].includes(sub)) return await unassignRole(env, rest);
            if (["выдать", "give"].includes(sub)) return await setPermission(env, rest, "allow");
            if (["запретить", "denyperm"].includes(sub)) return await setPermission(env, rest, "deny");
            if (["мои", "my"].includes(sub)) return await showMyPerms(env);
            if (["список", "list"].includes(sub)) return await listRoles(env);
            await sock.sendMessage(chat, { text: "❌ Неизвестная подкоманда. Пиши /роли" }, { quoted: msg });
        } catch (e) {
            console.error("[roles] ошибка:", e.message);
            await sock.sendMessage(chat, { text: "❌ Ошибка обработки команды" }, { quoted: msg });
        }
        return true;
    },

    onGroupParticipantsUpdate,
};

export default plugin;

async function getOwnerJid(sock, chat) {
    try {
        const meta = await sock.groupMetadata(chat);
        const owner = (meta.participants || []).find((p) => p.admin === "owner" || p.admin === "superadmin");
        return owner ? owner.id : null;
    } catch (e) {
        console.error("[roles] не удалось получить владельца группы:", e.message);
        return null;
    }
}

function getDb(ctx) {
    const candidates = [
        ctx,
        ctx && ctx.services && ctx.services.db,
        ctx && ctx.database,
        ctx && typeof ctx.getDb === "function" ? ctx.getDb() : null,
        ctx && ctx.services && typeof ctx.services.get === "function" ? ctx.services.get("db") : null,
        ctx && ctx.services && typeof ctx.services.getDb === "function" ? ctx.services.getDb() : null,
        ctx && typeof ctx.getService === "function" ? ctx.getService("db") : null,
        globalThis.__pluginDb || null,
    ];
    for (const c of candidates) {
        if (!c) continue;
        try {
            if (typeof c.collection === "function") return c;
            if (c.db && typeof c.db.collection === "function") return c.db;
            if (c.client && typeof c.client.db === "function") return c.client.db();
        } catch (e) {
            console.error("[roles] поиск базы:", e.message);
        }
    }
    return null;
}

function requireDb(env) {
    if (!env.db) {
        console.error("[roles] база данных недоступна в контексте плагина");
        return false;
    }
    return true;
}

function resolveCtx(raw, fallbackAuthor) {
    let ctx = raw;
    if (ctx && typeof ctx === "object" && !ctx.chat && (ctx.ctx || ctx.context)) {
        ctx = ctx.ctx || ctx.context;
    }
    if (!ctx || typeof ctx !== "object") ctx = {};
    const chat = ctx.chat || ctx.groupJid || ctx.jid;
    const sender = ctx.sender || ctx.author || fallbackAuthor;
    return { ...ctx, chat, sender, db: getDb(ctx) };
}

async function getCollection(db) {
    if (collection) return collection;
    collection = db.collection("roles");
    try {
        await collection.createIndex({ lid: 1 }, { unique: true });
    } catch (e) {
        console.error("[roles] индекс:", e.message);
    }
    return collection;
}

async function loadDoc(env) {
    const col = await getCollection(env.db);
    let doc = await col.findOne({ lid: env.chat });
    if (!doc) {
        doc = { lid: env.chat, roles: {}, assignments: {} };
        try {
            await col.insertOne(doc);
        } catch (e) {
            console.error("[roles] инициализация:", e.message);
        }
    }
    return doc;
}

async function saveDoc(env, doc) {
    const col = await getCollection(env.db);
    await col.updateOne({ lid: env.chat }, { $set: doc });
}

function targetFromArgs(rest) {
    const raw = rest[0];
    if (!raw) return null;
    if (raw.includes("@")) return raw;
    const digits = raw.replace(/\D/g, "");
    return digits.length >= 8 ? `${digits}@s.whatsapp.net` : null;
}

async function createRole(env, rest) {
    const name = rest.join(" ").trim().toLowerCase();
    if (!name) {
        await env.sock.sendMessage(env.chat, { text: "Использование: /роли создать <название>" }, { quoted: env.msg });
        return true;
    }
    const doc = await loadDoc(env);
    if (doc.roles[name]) {
        await env.sock.sendMessage(env.chat, { text: `Роль «${name}» уже существует` }, { quoted: env.msg });
        return true;
    }
    doc.roles[name] = {};
    await saveDoc(env, doc);
    await env.sock.sendMessage(env.chat, { text: `✅ Роль «${name}» создана` }, { quoted: env.msg });
    return true;
}

async function deleteRole(env, rest) {
    const name = (rest[0] || "").toLowerCase();
    if (!name) {
        await env.sock.sendMessage(env.chat, { text: "Использование: /роли удалить <название>" }, { quoted: env.msg });
        return true;
    }
    const doc = await loadDoc(env);
    if (!doc.roles[name]) {
        await env.sock.sendMessage(env.chat, { text: `Роль «${name}» не найдена` }, { quoted: env.msg });
        return true;
    }
    delete doc.roles[name];
    for (const [user, list] of Object.entries(doc.assignments)) {
        doc.assignments[user] = list.filter((r) => r !== name);
        if (!doc.assignments[user].length) delete doc.assignments[user];
    }
    await saveDoc(env, doc);
    await env.sock.sendMessage(env.chat, { text: `🗑️ Роль «${name}» удалена` }, { quoted: env.msg });
    return true;
}

async function assignRole(env, rest) {
    const target = targetFromArgs(rest);
    const name = (rest[1] || "").toLowerCase();
    if (!target || !name) {
        await env.sock.sendMessage(env.chat, { text: "Использование: /роли роль @номер <название>" }, { quoted: env.msg });
        return true;
    }
    const doc = await loadDoc(env);
    if (!doc.roles[name]) {
        await env.sock.sendMessage(env.chat, { text: `Роль «${name}» не найдена. Сначала: /роли создать ${name}` }, { quoted: env.msg });
        return true;
    }
    const list = doc.assignments[target] || [];
    if (list.includes(name)) {
        await env.sock.sendMessage(env.chat, { text: "Роль уже назначена" }, { quoted: env.msg });
        return true;
    }
    list.push(name);
    doc.assignments[target] = list;
    await saveDoc(env, doc);
    await env.sock.sendMessage(env.chat, { text: `✅ Роль «${name}» выдана` }, { quoted: env.msg });
    return true;
}

async function unassignRole(env, rest) {
    const target = targetFromArgs(rest);
    const name = (rest[1] || "").toLowerCase();
    if (!target || !name) {
        await env.sock.sendMessage(env.chat, { text: "Использование: /роли снятие @номер <название>" }, { quoted: env.msg });
        return true;
    }
    const doc = await loadDoc(env);
    const list = doc.assignments[target] || [];
    if (!list.includes(name)) {
        await env.sock.sendMessage(env.chat, { text: "У пользователя нет этой роли" }, { quoted: env.msg });
        return true;
    }
    doc.assignments[target] = list.filter((r) => r !== name);
    if (!doc.assignments[target].length) delete doc.assignments[target];
    await saveDoc(env, doc);
    await env.sock.sendMessage(env.chat, { text: `➖ Роль «${name}» снята` }, { quoted: env.msg });
    return true;
}

async function setPermission(env, rest, level) {
    const roleName = (rest[0] || "").toLowerCase();
    const permKey = (rest[1] || "").toLowerCase();
    if (!roleName || !permKey) {
        await env.sock.sendMessage(env.chat, { text: "Использование: /роли выдать|запретить <роль> <разрешение>" }, { quoted: env.msg });
        return true;
    }
    const doc = await loadDoc(env);
    if (!doc.roles[roleName]) {
        await env.sock.sendMessage(env.chat, { text: `Роль «${roleName}» не найдена` }, { quoted: env.msg });
        return true;
    }
    doc.roles[roleName][permKey] = level;
    await saveDoc(env, doc);
    const label = level === "allow" ? "✅ разрешено" : "⛔ запрещено";
    await env.sock.sendMessage(env.chat, { text: `«${permKey}» для роли «${roleName}»: ${label}` }, { quoted: env.msg });
    return true;
}

function resolvePerm(doc, user, permKey) {
    const roles = doc.assignments[user] || [];
    let result = "inherit";
    for (const roleName of roles) {
        const value = doc.roles[roleName]?.[permKey];
        if (!value || value === "inherit") continue;
        if (LEVELS.indexOf(value) > LEVELS.indexOf(result)) result = value;
    }
    return result;
}

async function showMyPerms(env) {
    const doc = await loadDoc(env);
    const roles = doc.assignments[env.sender] || [];
    if (!roles.length) {
        await env.sock.sendMessage(env.chat, { text: "У тебя нет ролей. Админ может выдать: /роли роль @тебя <название>" }, { quoted: env.msg });
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
    await env.sock.sendMessage(env.chat, { text: lines.join("\n") }, { quoted: env.msg });
    return true;
}

async function listRoles(env) {
    const doc = await loadDoc(env);
    const names = Object.keys(doc.roles);
    if (!names.length) {
        await env.sock.sendMessage(env.chat, { text: "Ролей пока нет. Создание: /роли создать <название>" }, { quoted: env.msg });
        return true;
    }
    const lines = ["📋 Роли группы:"];
    for (const name of names) {
        const count = Object.values(doc.assignments).filter((list) => list.includes(name)).length;
        lines.push(`  • ${name} (${count} участн.)`);
    }
    await env.sock.sendMessage(env.chat, { text: lines.join("\n") }, { quoted: env.msg });
    return true;
}

function mapParticipantAction(update) {
    if (!update || !Array.isArray(update.participants)) return null;
    const action = update.action || update.type;
    if (action === "promote") return { type: "promote", who: update.participants };
    if (action === "demote") return { type: "demote", who: update.participants };
    return null;
}

const PERM_MAP = {
    promote: "назначать_админов",
    demote: "снимать_админов",
};

async function reportViolation(sock, action, env) {
    if (!env.chat || !env.sender) return false;
    const doc = await loadDoc(env);
    const permKey = PERM_MAP[action.type];
    if (!permKey) return false;
    const actorIds = [env.sender, ...action.who.filter((id) => id !== env.sender)];
    let decision = "inherit";
    for (const id of actorIds) {
        const value = resolvePerm(doc, id, permKey);
        if (value === "deny") {
            decision = "deny";
            break;
        }
        if (value === "allow") decision = "allow";
    }
    if (decision !== "deny") return false;

    const ownerJid = await getOwnerJid(sock, env.chat);
    if (!ownerJid) return false;

    const violatorTag = `@${env.sender.split("@")[0]}`;
    try {
        await sock.sendMessage(ownerJid, {
            text: `⚠️ Нарушение правил в группе ${env.chat}\n${violatorTag} выполнил запрещённое действие «${permKey}» (${action.type})`,
            mentions: [env.sender],
        });
    } catch (e) {
        console.error("[roles] не удалось уведомить владельца:", e.message);
    }
    return true;
}

async function sendHelp(env) {
    const help = [
        "🎭 Плагин «Роли» (/роли):",
        "/роли создать <название>",
        "/роли удалить <название>",
        "/роли роль @номер <название>",
        "/роли снятие @номер <название>",
        "/роли выдать <роль> <разрешение>",
        "/роли запретить <роль> <разрешение>",
        "/роли мои",
        "/роли список",
    ].join("\n");
    await env.sock.sendMessage(env.chat, { text: help }, { quoted: env.msg });
}
