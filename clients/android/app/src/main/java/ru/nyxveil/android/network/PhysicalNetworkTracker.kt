package ru.nyxveil.android.network

/**
 * Pure physical-network identity + dedupe/debounce logic (unit-testable, no Android deps).
 *
 * VPN networks are never tracked. Capability updates on the same physical network are ignored.
 * Appearance of an additional network (e.g. Wi‑Fi while on LTE) does not force a switch.
 * Loss of the current physical network (optionally followed by a new one) emits events.
 */
class PhysicalNetworkTracker(
    private val debounceMs: Long = DEFAULT_DEBOUNCE_MS,
    private val nowMs: () -> Long = { System.currentTimeMillis() },
) {
    enum class Transport { WIFI, CELLULAR, ETHERNET, OTHER }

    data class Identity(
        val networkKey: String,
        val transport: Transport,
    ) {
        fun label(): String = when (transport) {
            Transport.WIFI -> "wifi"
            Transport.CELLULAR -> "cellular"
            Transport.ETHERNET -> "ethernet"
            Transport.OTHER -> "other"
        }
    }

    enum class EventKind {
        /** Active physical network replaced (e.g. Wi‑Fi → LTE after Wi‑Fi lost). */
        CHANGED,
        /** Active physical network lost and none remain. */
        LOST,
    }

    data class Event(
        val kind: EventKind,
        val from: Identity?,
        val to: Identity?,
        val label: String,
    )

    private val known = linkedMapOf<String, Identity>()
    private var active: Identity? = null
    private var lastEmittedKey: String? = null
    private var lastEmitAtMs: Long = 0L
    private var pending: Event? = null
    private var pendingAtMs: Long = 0L

    fun activeIdentity(): Identity? = active

    /**
     * @param isVpn true if network has TRANSPORT_VPN — ignored entirely.
     * @return event to deliver now, or null (ignored / debounced).
     */
    fun onAvailable(
        networkKey: String,
        transport: Transport,
        isVpn: Boolean,
    ): Event? {
        if (isVpn) return null
        val id = Identity(networkKey, transport)
        known[networkKey] = id
        if (active == null) {
            return maybeEmit(Event(EventKind.CHANGED, null, id, id.label()), forceKey = id.networkKey)
                .also { active = id }
        }
        // Additional network while one is active: remember but do not switch (no reconnect storm).
        return null
    }

    fun onCapabilitiesChanged(
        networkKey: String,
        transport: Transport,
        isVpn: Boolean,
    ): Event? {
        if (isVpn) return null
        val id = Identity(networkKey, transport)
        known[networkKey] = id
        // Same active network capability refresh — never a path change.
        if (active?.networkKey == networkKey) {
            active = id
            return null
        }
        if (active == null) {
            return onAvailable(networkKey, transport, false)
        }
        return null
    }

    fun onLost(networkKey: String, isVpn: Boolean): Event? {
        if (isVpn) return null
        known.remove(networkKey)
        if (active?.networkKey != networkKey) {
            return null
        }
        val from = active
        val next = selectBestRemaining()
        active = next
        return if (next == null) {
            maybeEmit(Event(EventKind.LOST, from, null, "lost"), forceKey = "lost:${from?.networkKey}")
        } else {
            maybeEmit(
                Event(EventKind.CHANGED, from, next, "${from?.label()}→${next.label()}"),
                forceKey = next.networkKey,
            )
        }
    }

    /** Flush debounce: return pending event if debounce window elapsed. */
    fun pollDebounced(now: Long = nowMs()): Event? {
        val p = pending ?: return null
        if (now - pendingAtMs < debounceMs) return null
        pending = null
        lastEmittedKey = emitKey(p)
        lastEmitAtMs = now
        return p
    }

    private fun selectBestRemaining(): Identity? {
        val order = listOf(Transport.WIFI, Transport.ETHERNET, Transport.CELLULAR, Transport.OTHER)
        return known.values.minByOrNull { order.indexOf(it.transport).let { i -> if (i < 0) 99 else i } }
    }

    private fun maybeEmit(event: Event, forceKey: String): Event? {
        val now = nowMs()
        val key = forceKey
        if (key == lastEmittedKey && now - lastEmitAtMs < debounceMs * 4) {
            return null
        }
        // Collapse bursts into one debounced emission.
        pending = event
        pendingAtMs = now
        if (lastEmitAtMs == 0L || now - lastEmitAtMs >= debounceMs) {
            pending = null
            lastEmittedKey = key
            lastEmitAtMs = now
            return event
        }
        return null
    }

    private fun emitKey(e: Event): String = when (e.kind) {
        EventKind.LOST -> "lost:${e.from?.networkKey}"
        EventKind.CHANGED -> e.to?.networkKey ?: e.label
    }

    companion object {
        const val DEFAULT_DEBOUNCE_MS = 750L
    }
}
