package ru.nyxveil.android.vpn

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test
import java.net.InetAddress

class HostIpAndTypeConfigTest {
    @Test
    fun hostIpIsNotNetworkBase() {
        val host = InetAddress.getByName("10.66.0.25")
        assertFalse(NyxveilVpnService.isNetworkBaseAddress(host, 24))
        assertTrue(NyxveilVpnService.isNetworkBaseAddress(InetAddress.getByName("10.66.0.0"), 24))
    }

    @Test
    fun parseTypeConfigPreservesHostIpAndEffectiveMtu() {
        val json = """
            {
              "vpn_ip":"10.66.0.25",
              "vpn_prefix":24,
              "dns_servers":["10.66.0.1"],
              "typeconfig_mtu":1420,
              "max_datagram_payload":1243,
              "nvp_overhead":100,
              "effective_tunnel_mtu":1135,
              "node_id":"n1",
              "transport":"quic"
            }
        """.trimIndent()
        val cfg = DefaultVpnSessionEngine.parseTypeConfig(json)
        assertEquals("10.66.0.25", cfg.vpnIp)
        assertEquals(24, cfg.vpnPrefix)
        assertEquals(1135, cfg.effectiveTunnelMtu)
        assertEquals(listOf("10.66.0.1"), cfg.dnsServers)
    }
}
