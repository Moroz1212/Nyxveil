package ru.nyxveil.android.licensing

import android.util.Log
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import org.json.JSONObject
import ru.nyxveil.android.BuildConfig
import ru.nyxveil.android.catalog.CatalogKeys
import ru.nyxveil.android.network.ControlPlaneNetworking
import ru.nyxveil.android.network.ProtectingSocketFactory
import java.io.IOException
import java.util.Base64
import java.util.concurrent.TimeUnit

/**
 * Control Plane HTTPS client. Base URL comes from BuildConfig only — never shown in UI.
 * User-facing errors are Russian only.
 */
class ControlPlaneClient(
    private val baseUrl: String = BuildConfig.CONTROL_PLANE_BASE_URL.trimEnd('/'),
    private val http: OkHttpClient = defaultClient(),
) {
    data class LicenseValidateResult(
        val valid: Boolean,
        val licenseId: String?,
        val plan: String?,
        val message: String?,
        val maxDevices: Int,
    )

    data class DeviceActivateResult(
        val deviceId: String,
        val activated: Boolean,
    )

    data class TicketIssueResult(
        val accessTicket: String,
        val expiresAt: Long,
        val nodeId: String?,
    )

    suspend fun validateLicense(licenseToken: String): LicenseValidateResult = withIo {
        val body = JSONObject().put("license_token", licenseToken).toString()
        val resp = post("/api/v1/license/validate", body)
        val json = parseJson(resp)
        LicenseValidateResult(
            valid = json.optBoolean("valid", false),
            licenseId = json.optStringOrNull("license_id"),
            plan = json.optStringOrNull("plan"),
            message = json.optStringOrNull("message"),
            maxDevices = json.optInt("max_devices", 0),
        )
    }

    suspend fun activateDevice(
        licenseToken: String,
        deviceId: String,
        publicKeyRaw32: ByteArray,
        deviceName: String,
    ): DeviceActivateResult = withIo {
        val body = JSONObject()
            .put("license_token", licenseToken)
            .put("device_id", deviceId)
            .put("public_key", Base64.getEncoder().encodeToString(publicKeyRaw32))
            .put("platform", "android")
            .put("device_name", deviceName)
            .toString()
        val resp = post("/api/v1/device/activate", body)
        val json = parseJson(resp)
        DeviceActivateResult(
            deviceId = json.optString("device_id", deviceId),
            activated = json.optBoolean("activated", false),
        )
    }

    suspend fun getCatalogKeys(licenseToken: String): CatalogKeys = withIo {
        val resp = getAuthed("/api/v1/catalog-keys", licenseToken)
        val json = parseJson(resp)
        val map = mutableMapOf<String, String>()
        val keys = json.optJSONObject("keys") ?: JSONObject()
        val it = keys.keys()
        while (it.hasNext()) {
            val k = it.next()
            map[k] = keys.getString(k)
        }
        CatalogKeys(
            issuer = json.optString("issuer"),
            keys = map,
            updatedAt = json.optLong("updated_at"),
        )
    }

    suspend fun getCatalogRaw(licenseToken: String): ByteArray = withIo {
        getAuthedBytes("/api/v1/catalog", licenseToken)
    }

    suspend fun issueTicket(
        licenseToken: String,
        deviceId: String,
        locationId: String?,
        nodeId: String? = null,
    ): TicketIssueResult = withIo {
        val body = JSONObject()
            .put("license_token", licenseToken)
            .put("device_id", deviceId)
        if (!locationId.isNullOrBlank()) body.put("location_id", locationId)
        if (!nodeId.isNullOrBlank()) body.put("node_id", nodeId)
        val resp = post("/api/v1/ticket/issue", body.toString())
        val json = parseJson(resp)
        TicketIssueResult(
            accessTicket = json.optString("access_ticket"),
            expiresAt = json.optLong("expires_at"),
            nodeId = json.optStringOrNull("node_id"),
        )
    }

    private fun post(path: String, jsonBody: String): String {
        val stage = path.substringAfterLast('/')
        Log.i(TAG, "stage=$stage method=POST")
        val req = Request.Builder()
            .url(baseUrl + path)
            .post(jsonBody.toRequestBody(JSON_MEDIA))
            .header("Accept", "application/json")
            .header("User-Agent", "Nyxveil-Android/1.0.0")
            .build()
        return execute(req, stage)
    }

    private fun getAuthed(path: String, token: String): String {
        val stage = path.trimEnd('/').substringAfterLast('/')
        Log.i(TAG, "stage=$stage method=GET auth=bearer_license")
        val req = Request.Builder()
            .url(baseUrl + path)
            .get()
            .header("Authorization", "Bearer $token")
            .header("Accept", "application/json")
            .header("User-Agent", "Nyxveil-Android/1.0.0")
            .build()
        return execute(req, stage)
    }

    private fun getAuthedBytes(path: String, token: String): ByteArray {
        val stage = path.trimEnd('/').substringAfterLast('/')
        Log.i(TAG, "stage=$stage method=GET auth=bearer_license")
        val req = Request.Builder()
            .url(baseUrl + path)
            .get()
            .header("Authorization", "Bearer $token")
            .header("Accept", "application/json")
            .header("User-Agent", "Nyxveil-Android/1.0.0")
            .build()
        try {
            http.newCall(req).execute().use { resp ->
                Log.i(TAG, "stage=$stage result=HTTP_${resp.code}")
                if (!resp.isSuccessful) throw mapHttpError(resp.code)
                return resp.body?.bytes() ?: ByteArray(0)
            }
        } catch (e: ControlPlaneException) {
            throw e
        } catch (e: IOException) {
            Log.e(TAG, "stage=$stage result=NETWORK class=${e.javaClass.simpleName}")
            throw ControlPlaneException("Нет связи с сервером лицензий. Проверьте интернет и повторите.")
        }
    }

    private fun execute(req: Request, stage: String): String {
        try {
            http.newCall(req).execute().use { resp ->
                val body = resp.body?.string().orEmpty()
                Log.i(TAG, "stage=$stage result=HTTP_${resp.code}")
                if (!resp.isSuccessful) throw mapHttpError(resp.code, body)
                return body
            }
        } catch (e: ControlPlaneException) {
            throw e
        } catch (e: IOException) {
            Log.e(TAG, "stage=$stage result=NETWORK class=${e.javaClass.simpleName}")
            throw ControlPlaneException("Нет связи с сервером лицензий. Проверьте интернет и повторите.")
        }
    }

    private fun parseJson(raw: String): JSONObject {
        return try {
            JSONObject(raw)
        } catch (_: Exception) {
            throw ControlPlaneException("Некорректный ответ сервера.")
        }
    }

    private fun mapHttpError(code: Int, body: String = ""): ControlPlaneException {
        val msg = when (code) {
            401, 403 -> "Лицензия отклонена или доступ запрещён."
            404 -> "Сервис лицензий временно недоступен."
            429 -> "Слишком много запросов. Подождите и повторите."
            in 500..599 -> "Ошибка сервера лицензий. Попробуйте позже."
            else -> "Не удалось выполнить запрос (код $code)."
        }
        // Do not surface raw body (may contain internals); keep Russian UX only.
        return ControlPlaneException(msg)
    }

    private suspend fun <T> withIo(block: () -> T): T =
        kotlinx.coroutines.withContext(kotlinx.coroutines.Dispatchers.IO) { block() }

    class ControlPlaneException(message: String) : Exception(message)

    companion object {
        private const val TAG = "NYX-CP"
        private val JSON_MEDIA = "application/json; charset=utf-8".toMediaType()

        fun defaultClient(): OkHttpClient = OkHttpClient.Builder()
            .connectTimeout(20, TimeUnit.SECONDS)
            .readTimeout(30, TimeUnit.SECONDS)
            .writeTimeout(30, TimeUnit.SECONDS)
            .socketFactory(
                ProtectingSocketFactory { socket ->
                    ControlPlaneNetworking.protectOrPassthrough(socket)
                },
            )
            .build()
    }
}

private fun JSONObject.optStringOrNull(key: String): String? {
    if (!has(key) || isNull(key)) return null
    val v = optString(key)
    return v.ifBlank { null }
}
