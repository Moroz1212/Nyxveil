package ru.nyxveil.android.catalog

import android.util.Log
import java.time.Duration
import java.time.Instant

/**
 * Sanitized catalog pipeline state for diagnostics (no secrets / no CP URL).
 */
enum class CatalogState {
    Missing,
    Loading,
    NetworkError,
    HttpError,
    KeysError,
    ParseError,
    SignatureError,
    TemporalError,
    Ready,
}

data class CatalogDiag(
    val state: CatalogState = CatalogState.Missing,
    val userSummary: String = "—",
    val source: String = "—",
    val keyId: String = "—",
    val issuedAt: String = "—",
    val expiresAt: String = "—",
    val remainingSeconds: Long? = null,
    val signatureStatus: String = "—",
    val temporalStatus: String = "—",
    val httpStage: String = "—",
    val detail: String = "",
) {
    fun toUserCatalogLine(): String = userSummary
}

internal object CatalogLog {
    private const val TAG = "NYX-CATALOG"

    fun stage(stage: String, result: String, extra: String = "") {
        val msg = if (extra.isBlank()) {
            "stage=$stage result=$result"
        } else {
            "stage=$stage result=$result $extra"
        }
        Log.i(TAG, msg)
    }

    fun fail(stage: String, result: String, throwable: Throwable? = null, extra: String = "") {
        val cls = throwable?.javaClass?.simpleName.orEmpty()
        val sanitized = throwable?.message
            ?.replace(Regex("https?://\\S+"), "[redacted]")
            ?.take(160)
            .orEmpty()
        Log.e(
            TAG,
            "stage=$stage result=$result class=$cls ${extra.trim()} msg=$sanitized".trim(),
        )
    }
}

internal fun catalogUserMessage(state: CatalogState, fallback: String? = null): String {
    return when (state) {
        CatalogState.NetworkError ->
            "Не удалось загрузить список локаций.\nПроверьте подключение к интернету и повторите попытку."
        CatalogState.HttpError, CatalogState.KeysError ->
            fallback ?: "Не удалось загрузить список локаций.\nПроверьте подключение к интернету и повторите попытку."
        CatalogState.SignatureError, CatalogState.ParseError ->
            "Не удалось проверить данные серверов."
        CatalogState.TemporalError ->
            "Не удалось обновить данные серверов."
        CatalogState.Ready -> "готов"
        CatalogState.Loading -> "загрузка"
        CatalogState.Missing -> "—"
    }
}

internal fun Instant.asDiagString(): String = toString()

internal fun remainingSeconds(now: Instant, expires: Instant): Long =
    Duration.between(now, expires).seconds
