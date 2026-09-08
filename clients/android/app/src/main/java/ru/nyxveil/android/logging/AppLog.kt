package ru.nyxveil.android.logging

import android.content.Context
import android.util.Log
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import java.io.File
import java.time.Instant
import java.time.ZoneOffset
import java.time.format.DateTimeFormatter
import java.util.Locale
import java.util.concurrent.CopyOnWriteArrayList

enum class LogLevel { DEBUG, INFO, WARN, ERROR }

enum class LogCategory {
    APP, CONTROLPLANE, CATALOG, LICENSE, VPN, TRANSPORT, AUTH,
    TYPECONFIG, TUN, DATAPLANE, DNS, MTU, RECONNECT, ERROR,
}

data class LogEntry(
    val timestampMs: Long,
    val level: LogLevel,
    val category: LogCategory,
    val message: String,
) {
    fun formatLine(): String {
        val ts = DateTimeFormatter.ofPattern("HH:mm:ss")
            .withZone(ZoneOffset.systemDefault())
            .format(Instant.ofEpochMilli(timestampMs))
        return "$ts ${level.name.padEnd(5)} ${category.name.padEnd(12)} $message"
    }
}

/**
 * In-memory + rotating file logs. Never store secrets.
 */
object AppLog {
    private const val TAG = "NYX"
    private const val MAX_MEMORY = 800
    private const val MAX_FILE_BYTES = 512 * 1024
    private const val MAX_ROTATED = 2

    private val memory = CopyOnWriteArrayList<LogEntry>()
    private val _entries = MutableStateFlow<List<LogEntry>>(emptyList())
    val entries: StateFlow<List<LogEntry>> = _entries.asStateFlow()

    @Volatile
    private var logDir: File? = null

    fun init(context: Context) {
        logDir = File(context.filesDir, "logs").also { it.mkdirs() }
    }

    fun i(category: LogCategory, message: String) = append(LogLevel.INFO, category, message)
    fun w(category: LogCategory, message: String) = append(LogLevel.WARN, category, message)
    fun e(category: LogCategory, message: String, t: Throwable? = null) {
        val extra = t?.let { " class=${it.javaClass.simpleName}" }.orEmpty()
        append(LogLevel.ERROR, category, sanitize(message) + extra)
    }
    fun d(category: LogCategory, message: String) = append(LogLevel.DEBUG, category, message)

    fun clear() {
        memory.clear()
        _entries.value = emptyList()
        logDir?.listFiles()?.forEach { it.delete() }
    }

    fun snapshotText(filter: ((LogEntry) -> Boolean)? = null): String {
        val list = if (filter == null) memory.toList() else memory.filter(filter)
        return list.joinToString("\n") { it.formatLine() }
    }

    fun exportFile(context: Context): File {
        val dir = File(context.cacheDir, "logs").also { it.mkdirs() }
        val out = File(dir, "nyxveil-share.log")
        out.writeText(snapshotText())
        return out
    }

    private fun append(level: LogLevel, category: LogCategory, raw: String) {
        val message = sanitize(raw)
        val entry = LogEntry(System.currentTimeMillis(), level, category, message)
        memory.add(entry)
        while (memory.size > MAX_MEMORY) {
            memory.removeAt(0)
        }
        _entries.value = memory.toList()
        val androidTag = "NYX-${category.name}"
        when (level) {
            LogLevel.ERROR -> Log.e(androidTag, message)
            LogLevel.WARN -> Log.w(androidTag, message)
            LogLevel.DEBUG -> Log.d(androidTag, message)
            LogLevel.INFO -> Log.i(androidTag, message)
        }
        writeFile(entry)
    }

    private fun writeFile(entry: LogEntry) {
        val dir = logDir ?: return
        try {
            val current = File(dir, "nyxveil.log")
            current.appendText(entry.formatLine() + "\n")
            if (current.length() > MAX_FILE_BYTES) {
                rotate(dir)
            }
        } catch (_: Exception) {
        }
    }

    private fun rotate(dir: File) {
        val oldest = File(dir, "nyxveil.$MAX_ROTATED.log")
        if (oldest.exists()) oldest.delete()
        for (i in MAX_ROTATED - 1 downTo 1) {
            val src = File(dir, "nyxveil.$i.log")
            val dst = File(dir, "nyxveil.${i + 1}.log")
            if (src.exists()) {
                if (dst.exists()) dst.delete()
                src.renameTo(dst)
            }
        }
        val current = File(dir, "nyxveil.log")
        val first = File(dir, "nyxveil.1.log")
        if (first.exists()) first.delete()
        current.renameTo(first)
        current.writeText("")
    }

    fun sanitize(input: String): String {
        var s = input
        s = s.replace(Regex("(?i)Bearer\\s+\\S+"), "Bearer [redacted]")
        s = s.replace(Regex("(?i)(license[_\\s-]*token|access[_\\s-]*ticket|authorization)\\s*[:=]\\s*\\S+"), "$1=[redacted]")
        s = s.replace(Regex("https?://[^\\s\"']+"), "[redacted-url]")
        s = s.replace(Regex("(?i)(private[_\\s-]*key|seed)[=:]\\S+"), "$1=[redacted]")
        if (s.length > 400) s = s.take(400) + "…"
        return s
    }
}
