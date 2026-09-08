package ru.nyxveil.android.network

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.os.Handler
import android.os.Looper
import android.util.Log

/**
 * Monitors **physical** underlying networks (Wi‑Fi / cellular / ethernet).
 * Ignores TRANSPORT_VPN so establishing Nyxveil VPN cannot feedback-loop
 * "Network path changed" spam.
 */
class NetworkMonitor(context: Context) {
    fun interface PathChangeListener {
        /** Called only on real physical path change / loss (deduped + debounced). */
        fun onPhysicalPathEvent(event: PhysicalNetworkTracker.Event)
    }

    private val appContext = context.applicationContext
    private val cm = appContext.getSystemService(ConnectivityManager::class.java)
    private val tracker = PhysicalNetworkTracker()
    private val mainHandler = Handler(Looper.getMainLooper())
    private var listener: PathChangeListener? = null

    private val debounceRunnable = Runnable {
        tracker.pollDebounced()?.let { deliver(it) }
    }

    private val callback = object : ConnectivityManager.NetworkCallback() {
        override fun onAvailable(network: Network) {
            handle(network, caps = null, lost = false)
        }

        override fun onCapabilitiesChanged(network: Network, caps: NetworkCapabilities) {
            handle(network, caps = caps, lost = false)
        }

        override fun onLost(network: Network) {
            handle(network, caps = null, lost = true)
        }
    }

    fun setPathChangeListener(listener: PathChangeListener?) {
        this.listener = listener
    }

    fun start() {
        val request = NetworkRequest.Builder()
            .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
            .build()
        try {
            cm?.registerNetworkCallback(request, callback)
            // Seed from currently known non-VPN networks without spamming.
            @Suppress("DEPRECATION")
            val nets = cm?.allNetworks ?: emptyArray()
            nets.forEach { net ->
                val caps = cm?.getNetworkCapabilities(net) ?: return@forEach
                if (caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN)) return@forEach
                tracker.onAvailable(net.toString(), mapTransport(caps), isVpn = false)
            }
        } catch (e: Exception) {
            Log.w(TAG, "registerNetworkCallback failed: ${e.javaClass.simpleName}")
        }
    }

    fun stop() {
        mainHandler.removeCallbacks(debounceRunnable)
        try {
            cm?.unregisterNetworkCallback(callback)
        } catch (_: Exception) {
        }
    }

    /** Test seam. */
    internal fun trackerForTests(): PhysicalNetworkTracker = tracker

    private fun handle(network: Network, caps: NetworkCapabilities?, lost: Boolean) {
        val c = caps ?: cm?.getNetworkCapabilities(network)
        val isVpn = c?.hasTransport(NetworkCapabilities.TRANSPORT_VPN) == true
        val key = network.toString()
        val transport = mapTransport(c)
        val event = when {
            lost -> tracker.onLost(key, isVpn)
            caps != null -> tracker.onCapabilitiesChanged(key, transport, isVpn)
            else -> tracker.onAvailable(key, transport, isVpn)
        }
        if (event != null) {
            deliver(event)
        } else {
            mainHandler.removeCallbacks(debounceRunnable)
            mainHandler.postDelayed(debounceRunnable, PhysicalNetworkTracker.DEFAULT_DEBOUNCE_MS)
        }
    }

    private fun deliver(event: PhysicalNetworkTracker.Event) {
        Log.i(TAG, "physical_path kind=${event.kind} label=${event.label}")
        listener?.onPhysicalPathEvent(event)
    }

    private fun mapTransport(c: NetworkCapabilities?): PhysicalNetworkTracker.Transport {
        if (c == null) return PhysicalNetworkTracker.Transport.OTHER
        return when {
            c.hasTransport(NetworkCapabilities.TRANSPORT_WIFI) -> PhysicalNetworkTracker.Transport.WIFI
            c.hasTransport(NetworkCapabilities.TRANSPORT_CELLULAR) -> PhysicalNetworkTracker.Transport.CELLULAR
            c.hasTransport(NetworkCapabilities.TRANSPORT_ETHERNET) -> PhysicalNetworkTracker.Transport.ETHERNET
            else -> PhysicalNetworkTracker.Transport.OTHER
        }
    }

    companion object {
        private const val TAG = "NyxveilNet"
    }
}
