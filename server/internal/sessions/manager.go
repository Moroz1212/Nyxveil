// Package sessions tracks live VPN sessions and allocates client tunnel IPs.
package sessions

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"

	"github.com/nyxveil/nvp/core/session"
)

var (
	ErrCapacityFull   = errors.New("sessions: capacity full")
	ErrPoolExhausted  = errors.New("sessions: VPN IP pool exhausted")
	ErrInvalidCIDR    = errors.New("sessions: invalid VPN subnet CIDR")
	ErrUnknownSession = errors.New("sessions: unknown session")
	ErrSpoofedSource  = errors.New("sessions: spoofed source IP")
	ErrNotIPv4        = errors.New("sessions: not an IPv4 packet")
)

// Record binds a live NVP session to an allocated tunnel address.
type Record struct {
	ID      string
	VPNIP   netip.Addr
	Session *session.Session
}

// Manager holds atomic session counts, capacity gating, and an IPv4 pool.
type Manager struct {
	mu       sync.Mutex
	capacity int32
	count    atomic.Int32
	pool     []netip.Addr
	free     []netip.Addr
	byIP     map[netip.Addr]*Record
	byID     map[string]*Record
	bySess   map[*session.Session]*Record
}

// New creates a manager with the given capacity and VPN client CIDR
// (e.g. 10.66.0.0/24). Network and broadcast addresses are skipped.
// The first usable host (.1) is reserved for the node itself and not allocated to clients.
func New(capacity int, cidr string) (*Manager, error) {
	if capacity <= 0 {
		capacity = 100
	}
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidCIDR, err)
	}
	if !prefix.Addr().Is4() {
		return nil, fmt.Errorf("%w: only IPv4 supported", ErrInvalidCIDR)
	}
	prefix = prefix.Masked()
	pool := enumerateHosts(prefix)
	if len(pool) == 0 {
		return nil, fmt.Errorf("%w: no usable hosts in %s", ErrInvalidCIDR, cidr)
	}
	free := make([]netip.Addr, len(pool))
	copy(free, pool)
	return &Manager{
		capacity: int32(capacity),
		pool:     pool,
		free:     free,
		byIP:     make(map[netip.Addr]*Record),
		byID:     make(map[string]*Record),
		bySess:   make(map[*session.Session]*Record),
	}, nil
}

// Capacity returns the configured maximum concurrent sessions.
func (m *Manager) Capacity() int {
	return int(m.capacity)
}

// SetCapacity updates capacity (atomic apply from Control Plane config).
func (m *Manager) SetCapacity(n int) {
	if n <= 0 {
		n = 1
	}
	m.mu.Lock()
	m.capacity = int32(n)
	m.mu.Unlock()
}

// Count returns the current number of active sessions.
func (m *Manager) Count() int {
	return int(m.count.Load())
}

// Available reports whether a new session may be accepted under capacity.
func (m *Manager) Available() bool {
	m.mu.Lock()
	capN := m.capacity
	m.mu.Unlock()
	return m.count.Load() < capN
}

// Allocate reserves a VPN IP and registers the session. Returns ErrCapacityFull
// or ErrPoolExhausted without mutating state on failure.
func (m *Manager) Allocate(sess *session.Session) (*Record, error) {
	if sess == nil {
		return nil, errors.New("sessions: nil session")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.bySess[sess]; exists {
		return nil, errors.New("sessions: session already allocated")
	}
	if m.count.Load() >= m.capacity {
		return nil, ErrCapacityFull
	}
	if len(m.free) == 0 {
		return nil, ErrPoolExhausted
	}
	ip := m.free[0]
	m.free = m.free[1:]
	id, err := newSessionID()
	if err != nil {
		m.free = append([]netip.Addr{ip}, m.free...)
		return nil, err
	}
	rec := &Record{ID: id, VPNIP: ip, Session: sess}
	m.byIP[ip] = rec
	m.byID[id] = rec
	m.bySess[sess] = rec
	m.count.Add(1)
	return rec, nil
}

// ReleaseBySession frees the IP and removes the session mapping.
func (m *Manager) ReleaseBySession(sess *session.Session) {
	if sess == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.bySess[sess]
	if !ok {
		return
	}
	m.releaseLocked(rec)
}

// ReleaseByID frees a session by its allocated ID.
func (m *Manager) ReleaseByID(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.byID[id]
	if !ok {
		return ErrUnknownSession
	}
	m.releaseLocked(rec)
	return nil
}

func (m *Manager) releaseLocked(rec *Record) {
	delete(m.byIP, rec.VPNIP)
	delete(m.byID, rec.ID)
	delete(m.bySess, rec.Session)
	m.free = append(m.free, rec.VPNIP)
	m.count.Add(-1)
}

// LookupByIP returns the session record for a client VPN address.
func (m *Manager) LookupByIP(ip netip.Addr) (*Record, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.byIP[ip]
	return rec, ok
}

// LookupByID returns the session record by session ID.
func (m *Manager) LookupByID(id string) (*Record, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.byID[id]
	return rec, ok
}

// ValidateSource checks that an IPv4 packet's source matches the session's VPN IP.
func (m *Manager) ValidateSource(sess *session.Session, pkt []byte) error {
	src, _, err := ParseIPv4Endpoints(pkt)
	if err != nil {
		return err
	}
	m.mu.Lock()
	rec, ok := m.bySess[sess]
	m.mu.Unlock()
	if !ok {
		return ErrUnknownSession
	}
	if src != rec.VPNIP {
		return ErrSpoofedSource
	}
	return nil
}

// ParseIPv4Endpoints extracts source and destination from an IPv4 packet.
func ParseIPv4Endpoints(pkt []byte) (src, dst netip.Addr, err error) {
	if len(pkt) < 20 {
		return netip.Addr{}, netip.Addr{}, ErrNotIPv4
	}
	if pkt[0]>>4 != 4 {
		return netip.Addr{}, netip.Addr{}, ErrNotIPv4
	}
	var src4, dst4 [4]byte
	copy(src4[:], pkt[12:16])
	copy(dst4[:], pkt[16:20])
	return netip.AddrFrom4(src4), netip.AddrFrom4(dst4), nil
}

// DestIP returns the IPv4 destination of pkt.
func DestIP(pkt []byte) (netip.Addr, error) {
	_, dst, err := ParseIPv4Endpoints(pkt)
	return dst, err
}

func enumerateHosts(prefix netip.Prefix) []netip.Addr {
	addr := prefix.Addr()
	bits := prefix.Bits()
	if bits >= 31 {
		// /31 and /32 have no classic network/broadcast split for clients.
		if bits == 32 {
			return nil
		}
		// /31: both addresses usable per RFC 3021; still reserve first for node.
		b := addr.Next()
		if b.IsValid() && prefix.Contains(b) {
			return []netip.Addr{b}
		}
		return nil
	}
	var out []netip.Addr
	// Skip network (.0) and first host (.1 = node), skip broadcast.
	host := addr.Next() // first host after network = node address
	if !host.IsValid() || !prefix.Contains(host) {
		return nil
	}
	cur := host.Next() // first client
	broadcast := lastAddr(prefix)
	for cur.IsValid() && prefix.Contains(cur) {
		if cur == broadcast {
			break
		}
		out = append(out, cur)
		cur = cur.Next()
	}
	return out
}

func lastAddr(prefix netip.Prefix) netip.Addr {
	addr := prefix.Addr().As4()
	var mask net.IPMask = net.CIDRMask(prefix.Bits(), 32)
	var bcast [4]byte
	for i := 0; i < 4; i++ {
		bcast[i] = addr[i] | ^mask[i]
	}
	return netip.AddrFrom4(bcast)
}

func newSessionID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// NodeAddress returns the reserved .1 (or first host) address for the TUN iface.
func NodeAddress(cidr string) (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(cidr)
	if err != nil {
		return netip.Prefix{}, err
	}
	prefix = prefix.Masked()
	host := prefix.Addr().Next()
	if !host.IsValid() || !prefix.Contains(host) {
		return netip.Prefix{}, fmt.Errorf("%w: no node address in %s", ErrInvalidCIDR, cidr)
	}
	return netip.PrefixFrom(host, prefix.Bits()), nil
}
